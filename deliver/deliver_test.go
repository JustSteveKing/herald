package deliver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JustSteveKing/herald/pack"
)

// receiver records what arrived and answers however the test wants. It is the
// application under test, standing in for the thing herald exists to exercise.
type receiver struct {
	mu       sync.Mutex
	requests []capture
	status   int
	verify   func(capture) bool
}

type capture struct {
	header http.Header
	body   []byte
	url    string
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)

	c := capture{header: req.Header.Clone(), body: body, url: "http://" + req.Host + req.URL.Path}

	r.mu.Lock()
	r.requests = append(r.requests, c)
	r.mu.Unlock()

	if r.verify != nil && !r.verify(c) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) at(i int) capture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[i]
}

func load(t *testing.T, id string) (pack.Provider, pack.Fixture) {
	t.Helper()

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	p, ok := set.Provider(id)
	if !ok {
		t.Fatalf("no provider %q", id)
	}

	fixtures, err := p.Fixtures()
	if err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("%s has no fixtures", id)
	}

	f, err := p.Fixture(fixtures[0].ID)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	return p, f
}

// verifyStripe is written from Stripe's documented verification steps rather
// than from herald's signer: split the header on commas, take t and v1,
// recompute HMAC-SHA256 over "t.body" and compare. If herald and this
// disagree, herald is wrong.
func verifyStripe(secret string) func(capture) bool {
	return func(c capture) bool {
		var timestamp, signature string
		for _, part := range strings.Split(c.header.Get("Stripe-Signature"), ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch key {
			case "t":
				timestamp = value
			case "v1":
				signature = value
			}
		}

		if timestamp == "" || signature == "" {
			return false
		}

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "." + string(c.body)))

		return hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil))))
	}
}

// verifyStandardWebhooks follows the spec: strip whsec_, base64 decode the
// secret, sign "id.timestamp.body", and accept any of the space separated
// signatures whose version is v1.
func verifyStandardWebhooks(secret string) func(capture) bool {
	return func(c capture) bool {
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			return false
		}

		id := c.header.Get("webhook-id")
		timestamp := c.header.Get("webhook-timestamp")

		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(id + "." + timestamp + "." + string(c.body)))
		want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

		for _, candidate := range strings.Fields(c.header.Get("webhook-signature")) {
			version, value, ok := strings.Cut(candidate, ",")
			if ok && version == "v1" && hmac.Equal([]byte(value), []byte(want)) {
				return true
			}
		}

		return false
	}
}

func TestStripeDeliveryVerifies(t *testing.T) {
	p, f := load(t, "stripe")
	const secret = "whsec_test_secret"

	rec := &receiver{verify: verifyStripe(secret)}
	server := httptest.NewServer(rec)
	defer server.Close()

	results, err := Send(context.Background(), p, f, Options{Target: server.URL, Secret: secret})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(results) != 1 || !results[0].OK() {
		t.Fatalf("delivery was rejected: %+v", results)
	}

	// The body must arrive byte for byte, because that is what was signed.
	if got := string(rec.at(0).body); got != string(f.Body) {
		t.Errorf("body changed in transit:\n got %s\nwant %s", got, f.Body)
	}
}

func TestStandardWebhooksDeliveryVerifies(t *testing.T) {
	p, f := load(t, "standardwebhooks")
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

	rec := &receiver{verify: verifyStandardWebhooks(secret)}
	server := httptest.NewServer(rec)
	defer server.Close()

	results, err := Send(context.Background(), p, f, Options{Target: server.URL, Secret: secret})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if !results[0].OK() {
		t.Fatalf("delivery was rejected: %+v", results[0])
	}

	// The id in the header is the one that was signed, so a receiver that
	// reads it back gets something that looks like a real Svix message id.
	if id := rec.at(0).header.Get("webhook-id"); !strings.HasPrefix(id, "msg_") {
		t.Errorf("webhook-id is %q, which is not shaped like one of theirs", id)
	}
}

func TestBadSignatureIsRejected(t *testing.T) {
	p, f := load(t, "stripe")
	const secret = "whsec_test_secret"

	rec := &receiver{verify: verifyStripe(secret)}
	server := httptest.NewServer(rec)
	defer server.Close()

	results, err := Send(context.Background(), p, f, Options{Target: server.URL, Secret: secret, Corrupt: true})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if results[0].OK() {
		t.Error("a corrupted signature was accepted")
	}
	// The header still parsed: the receiver got as far as comparing, which is
	// the difference between testing verification and testing error handling.
	if got := rec.at(0).header.Get("Stripe-Signature"); !strings.HasPrefix(got, "t=") {
		t.Errorf("corrupt broke the header shape: %s", got)
	}
}

func TestDuplicateReusesTheDeliveryID(t *testing.T) {
	p, f := load(t, "standardwebhooks")

	rec := &receiver{}
	server := httptest.NewServer(rec)
	defer server.Close()

	if _, err := Send(context.Background(), p, f, Options{
		Target:    server.URL,
		Secret:    "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw",
		Count:     3,
		Duplicate: true,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if rec.count() != 3 {
		t.Fatalf("got %d deliveries, want 3", rec.count())
	}

	first := rec.at(0).header.Get("webhook-id")
	for i := 1; i < 3; i++ {
		if got := rec.at(i).header.Get("webhook-id"); got != first {
			t.Errorf("delivery %d had id %s, want %s: a retry must look like the same delivery", i, got, first)
		}
	}
}

func TestCountWithoutDuplicateGivesFreshIDs(t *testing.T) {
	p, f := load(t, "standardwebhooks")

	rec := &receiver{}
	server := httptest.NewServer(rec)
	defer server.Close()

	if _, err := Send(context.Background(), p, f, Options{
		Target: server.URL,
		Secret: "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw",
		Count:  3,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		id := rec.at(i).header.Get("webhook-id")
		if seen[id] {
			t.Errorf("delivery %d reused id %s without --duplicate", i, id)
		}
		seen[id] = true
	}
}

func TestSkewAgesTheTimestamp(t *testing.T) {
	p, f := load(t, "stripe")

	rec := &receiver{}
	server := httptest.NewServer(rec)
	defer server.Close()

	const skew = -6 * time.Minute
	before := time.Now().Add(skew)

	if _, err := Send(context.Background(), p, f, Options{
		Target: server.URL,
		Secret: "whsec_test_secret",
		Skew:   skew,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	header := rec.at(0).header.Get("Stripe-Signature")
	raw, _, _ := strings.Cut(strings.TrimPrefix(header, "t="), ",")

	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("timestamp %q did not parse: %v", raw, err)
	}

	sent := time.Unix(seconds, 0)
	if sent.After(before.Add(2 * time.Second)) {
		t.Errorf("timestamp is %s, which is not %s in the past", sent, -skew)
	}
	// Stripe documents five minutes, so six is outside it by a minute.
	if age := time.Since(sent); age < p.Timestamp.Tolerance {
		t.Errorf("delivery is %s old, inside the %s tolerance it was meant to break", age, p.Timestamp.Tolerance)
	}
}

// TestTwilioSignatureFollowsTheTarget is the reason signing takes the whole
// request. The same fixture sent to two URLs must produce two signatures.
func TestTwilioSignatureFollowsTheTarget(t *testing.T) {
	p, f := load(t, "twilio")

	rec := &receiver{}
	one := httptest.NewServer(rec)
	defer one.Close()
	two := httptest.NewServer(rec)
	defer two.Close()

	for _, target := range []string{one.URL, two.URL} {
		if _, err := Send(context.Background(), p, f, Options{
			Target: target,
			Secret: "12345678901234567890123456789012",
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	first := rec.at(0).header.Get("X-Twilio-Signature")
	second := rec.at(1).header.Get("X-Twilio-Signature")

	if first == second {
		t.Error("the signature did not change with the URL, so the URL is not being signed")
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	p, f := load(t, "stripe")

	rec := &receiver{}
	server := httptest.NewServer(rec)
	defer server.Close()

	results, err := Send(context.Background(), p, f, Options{
		Target: server.URL,
		Secret: "whsec_test_secret",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if rec.count() != 0 {
		t.Errorf("dry run made %d requests", rec.count())
	}
	if results[0].Request.Header.Get("Stripe-Signature") == "" {
		t.Error("dry run did not sign, so it cannot show what would be sent")
	}
}

func TestUnsignedProviderStillDelivers(t *testing.T) {
	p, f := load(t, "mollie-classic")

	rec := &receiver{}
	server := httptest.NewServer(rec)
	defer server.Close()

	results, err := Send(context.Background(), p, f, Options{Target: server.URL})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if !results[0].OK() {
		t.Fatalf("delivery failed: %+v", results[0])
	}

	if got := rec.at(0).header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("content type is %q, want the form type the fixture extension asks for", got)
	}
	if got := string(rec.at(0).body); got != "id=tr_WDqYK6vllg" {
		t.Errorf("body is %q", got)
	}
}
