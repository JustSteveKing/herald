// Package deliver sends a signed fixture and reports what came back.
//
// The controls here are the reason herald exists rather than a shell function
// that curls a file. Sending one well-formed delivery to a handler proves the
// happy path and nothing else, and the happy path is not where webhook
// handlers fail. They fail on the second copy of a delivery they already
// processed, on an event that arrives before the one it depends on, on a burst
// that opens more database connections than the pool has, and on a signature
// that is correct but six minutes old.
package deliver

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/JustSteveKing/herald/pack"
	"github.com/JustSteveKing/herald/signer"
)

// Options is one run of deliveries.
type Options struct {
	// Target is the full URL to send to. It is part of the signed string for
	// Twilio and Square, so it is not merely where the request goes.
	Target string
	Secret string

	// Count is how many times to send. Each one gets a fresh delivery id
	// unless Duplicate is set.
	Count int
	// Duplicate reuses one delivery id across every send, which is what a
	// provider does when it retries because your handler timed out after
	// already committing. A handler that is not idempotent double-charges
	// somebody here.
	Duplicate bool
	// Interval waits between sends. Zero sends them as fast as the client
	// will go, which is the burst case.
	Interval time.Duration

	// Skew moves the timestamp. Negative values age the delivery, so
	// -6m against a provider documenting a five minute tolerance is a
	// delivery the provider itself would have refused to make.
	Skew time.Duration
	// Corrupt breaks the signature while leaving the header well formed.
	Corrupt bool

	// Headers are added after the pack's own and before signing, so a pack
	// that signs a header you overrode still signs the value you set.
	Headers map[string]string

	Timeout time.Duration
	// DryRun builds and signs the request, then does not send it. Nothing
	// here is destructive, but the target might be, and seeing the exact
	// bytes first is cheaper than reading a log afterwards.
	DryRun bool
}

// Result is one delivery.
type Result struct {
	Attempt int
	Request *signer.Request
	// DeliveryID is what the provider would call this delivery. Two results
	// sharing one is the duplicate case.
	DeliveryID string
	Timestamp  time.Time

	Status   int
	Body     string
	Duration time.Duration
	Err      error
}

// OK is whether the endpoint accepted it. Providers vary on what they treat
// as success and most accept any 2xx, so herald does too.
func (r Result) OK() bool { return r.Err == nil && r.Status >= 200 && r.Status < 300 }

// Send builds, signs and sends the fixture, returning one Result per attempt.
// It returns results for the attempts it made even when one fails, because a
// burst that starts failing at the fourth request is the finding.
func Send(ctx context.Context, p pack.Provider, f pack.Fixture, opts Options) ([]Result, error) {
	if opts.Target == "" {
		return nil, fmt.Errorf("no target URL")
	}
	if opts.Count < 1 {
		opts.Count = 1
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}

	client := &http.Client{Timeout: opts.Timeout}

	// One id for the whole run when duplicating, so every attempt is the same
	// delivery as far as the receiver can tell.
	shared, err := NewID(p.Delivery)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, opts.Count)

	for attempt := 1; attempt <= opts.Count; attempt++ {
		if attempt > 1 && opts.Interval > 0 {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(opts.Interval):
			}
		}

		id := shared
		if !opts.Duplicate {
			if id, err = NewID(p.Delivery); err != nil {
				return results, err
			}
		}

		result, err := one(ctx, client, p, f, opts, id, attempt)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}

	return results, nil
}

func one(ctx context.Context, client *http.Client, p pack.Provider, f pack.Fixture, opts Options, id string, attempt int) (Result, error) {
	at := time.Now().Add(opts.Skew)

	req, err := Build(p, f, opts, id, at)
	if err != nil {
		return Result{}, err
	}

	result := Result{Attempt: attempt, Request: req, DeliveryID: id, Timestamp: at}
	if opts.DryRun {
		return result, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, strings.NewReader(string(req.Body)))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header = req.Header.Clone()

	started := time.Now()
	res, err := client.Do(httpReq)
	result.Duration = time.Since(started)
	if err != nil {
		result.Err = err
		return result, nil
	}
	defer res.Body.Close()

	// Cap the body. A handler that answers with a full HTML error page is
	// common and printing all of it helps nobody.
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	result.Status = res.StatusCode
	result.Body = strings.TrimSpace(string(body))

	return result, nil
}

// Build assembles and signs one request without sending it.
func Build(p pack.Provider, f pack.Fixture, opts Options, id string, at time.Time) (*signer.Request, error) {
	transport := p.Transport.Effective()

	contentType := transport.ContentType
	if f.ContentType != "" {
		contentType = f.ContentType
	}

	req := &signer.Request{
		Method: transport.Method,
		URL:    opts.Target,
		Header: http.Header{},
		Body:   f.Body,
	}

	ctx := signer.Context{
		Timestamp: at,
		ID:        id,
		Event:     f.ID,
		Corrupt:   opts.Corrupt,
	}

	req.Header.Set("Content-Type", contentType)
	for name, value := range transport.Headers {
		req.Header.Set(name, signer.Expand(value, p, req, ctx))
	}
	// Event metadata wins over the provider default, which is how GitHub's
	// X-GitHub-Event says pull_request for an event called pull_request.opened.
	for name, value := range f.Meta.Headers {
		req.Header.Set(name, signer.Expand(value, p, req, ctx))
	}
	for name, value := range opts.Headers {
		req.Header.Set(name, value)
	}

	if len(p.Signing) > 0 {
		if err := signer.Sign(p, req, opts.Secret, ctx); err != nil {
			return nil, err
		}
	}

	return req, nil
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// NewID makes a delivery identifier shaped the way the provider shapes them.
func NewID(spec pack.DeliveryID) (string, error) {
	switch spec.Format {
	case "token":
		out := make([]byte, 22)
		for i := range out {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base62))))
			if err != nil {
				return "", err
			}
			out[i] = base62[n.Int64()]
		}
		return spec.Prefix + string(out), nil

	default:
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		raw[6] = (raw[6] & 0x0f) | 0x40
		raw[8] = (raw[8] & 0x3f) | 0x80
		return spec.Prefix + fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
	}
}
