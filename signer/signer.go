// Package signer turns a pack.Signature into the headers a provider would have
// sent.
//
// The interface takes the whole request rather than the body, and that is the
// one design decision here worth defending. Twilio signs the full URL plus its
// sorted form parameters; Square signs the notification URL concatenated with
// the body. Under a sign(body, secret) interface both of those are impossible,
// and you find that out after ten providers are already written against it.
// Taking the request costs nothing for the eight providers that only sign the
// body.
package signer

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/JustSteveKing/herald/pack"
)

// Request is the delivery being signed. It is deliberately not *http.Request:
// signing happens before there is a request to send, and a dry run signs a
// delivery it never intends to make.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Context is everything about this particular delivery that a signature can
// depend on but the pack cannot know.
type Context struct {
	// Timestamp is what the provider would claim the time was. Moving it back
	// past the provider's tolerance is how you find out whether a handler
	// checks the replay window, and most do not.
	Timestamp time.Time
	// ID is the per-delivery identifier. Sending the same one twice is a
	// duplicate delivery; a handler that is not idempotent will show it here.
	ID string
	// Event is the event id, for packs whose headers name it.
	Event string
	// Corrupt breaks the digest after it is computed but before it is
	// formatted, so the header keeps its shape and only the comparison fails.
	// A handler that rejects this for the wrong reason, parse error rather
	// than mismatch, is one that will also reject a real rotated secret badly.
	Corrupt bool
}

// Sign computes every signature the provider declares and sets the headers on
// req. Headers that are not signatures are the caller's business; deliver sets
// those before calling this, because a pack is allowed to sign them.
func Sign(p pack.Provider, req *Request, secret string, ctx Context) error {
	key, err := decodeSecret(p.Secret, secret)
	if err != nil {
		return err
	}

	for i, sig := range p.Signing {
		sig = sig.Effective()

		value, err := compute(sig, p, req, key, ctx)
		if err != nil {
			return fmt.Errorf("%s signing[%d] (%s): %w", p.ID, i, sig.Header, err)
		}

		req.Header.Set(sig.Header, value)
	}

	return nil
}

func compute(sig pack.Signature, p pack.Provider, req *Request, key []byte, ctx Context) (string, error) {
	var digest string

	switch sig.Scheme {
	case "builtin":
		fn, ok := builtins[sig.Builtin]
		if !ok {
			return "", fmt.Errorf("no builtin named %q", sig.Builtin)
		}
		d, err := fn(req, key, ctx)
		if err != nil {
			return "", err
		}
		digest = d

	default:
		payload := Expand(sig.Payload, p, req, ctx)
		mac, err := newHash(sig.Algorithm)
		if err != nil {
			return "", err
		}
		h := hmac.New(mac, key)
		h.Write([]byte(payload))
		digest = encode(sig.Encoding, h.Sum(nil))
	}

	if ctx.Corrupt {
		digest = corrupt(digest)
	}

	return strings.ReplaceAll(Expand(sig.Format, p, req, ctx), "{signature}", digest), nil
}

// Expand fills the placeholders a pack may use. Unknown placeholders are left
// alone rather than blanked, because a signature built from a silently empty
// value verifies against nothing and gives no clue why.
func Expand(s string, p pack.Provider, req *Request, ctx Context) string {
	parsed, _ := url.Parse(req.URL)
	path, host := "", ""
	if parsed != nil {
		path, host = parsed.Path, parsed.Host
	}

	return strings.NewReplacer(
		"{body}", string(req.Body),
		"{timestamp}", FormatTime(p.Timestamp, ctx.Timestamp),
		"{id}", ctx.ID,
		"{event}", ctx.Event,
		"{url}", req.URL,
		"{path}", path,
		"{host}", host,
		"{method}", req.Method,
	).Replace(s)
}

// FormatTime renders the timestamp the way the provider writes it.
func FormatTime(t pack.Timestamp, at time.Time) string {
	switch t.Format {
	case "unixmilli":
		return strconv.FormatInt(at.UnixMilli(), 10)
	case "rfc3339":
		return at.UTC().Format(time.RFC3339)
	default:
		return strconv.FormatInt(at.Unix(), 10)
	}
}

func newHash(algorithm string) (func() hash.Hash, error) {
	switch algorithm {
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	case "sha512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("unknown algorithm %q", algorithm)
}

func encode(encoding string, sum []byte) string {
	if encoding == "base64" {
		return base64.StdEncoding.EncodeToString(sum)
	}
	return hex.EncodeToString(sum)
}

// decodeSecret turns the secret as a person pastes it into the bytes the
// provider actually keys the HMAC with. Standard Webhooks is the reason this
// exists: its secrets are base64 behind a whsec_ prefix, and signing with the
// printable form produces a signature that is wrong in a way no error message
// will explain.
func decodeSecret(s pack.Secret, secret string) ([]byte, error) {
	secret = strings.TrimPrefix(secret, s.Prefix)

	if s.Encoding == "base64" {
		raw, err := base64.StdEncoding.DecodeString(secret)
		if err != nil {
			return nil, fmt.Errorf("secret is declared base64 but did not decode: %w", err)
		}
		return raw, nil
	}

	return []byte(secret), nil
}

// corrupt changes exactly one character of the digest, keeping its length and
// its alphabet, so the header still parses and only the comparison fails.
func corrupt(digest string) string {
	if digest == "" {
		return digest
	}

	last := digest[len(digest)-1]
	replacement := byte('a')
	if last == 'a' {
		replacement = 'b'
	}

	return digest[:len(digest)-1] + string(replacement)
}
