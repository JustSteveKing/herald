// Package pack reads provider packs: the directory of payload fixtures and
// signing rules that makes one provider's webhooks reproducible.
//
// A pack is data, deliberately. Adding a provider should be a directory and a
// pull request, not a release of this binary, because the people who know what
// a provider actually sends are the provider and the person who just read the
// delivery in their logs. Only two schemes in the wild resist being written
// down this way, and they are named in builtins.go over in signer.
//
// This package knows the shape of a pack and nothing about signing or sending.
// signer turns Signature into bytes; deliver turns an Event into a request.
package pack

import (
	"fmt"
	"io/fs"
	"time"
)

// Provider is one directory under providers/, read from its provider.yaml.
type Provider struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Homepage string `yaml:"homepage"`
	// Docs points at the provider's own description of its signature scheme.
	// Every Signature below is a claim about someone else's system, so the
	// source of that claim travels with it.
	Docs string `yaml:"docs"`

	Secret    Secret           `yaml:"secret"`
	Delivery  DeliveryID       `yaml:"delivery"`
	Transport Transport        `yaml:"transport"`
	Signing   []Signature      `yaml:"signing"`
	Timestamp Timestamp        `yaml:"timestamp"`
	Meta      map[string]Event `yaml:"events"`

	// Notes carries a caveat that applies to the whole provider, shown before
	// a send. Mollie classic uses it to say that the receiving application
	// will call the real Mollie API, so the fixture only covers half the test.
	Notes string `yaml:"notes"`

	// dir is where this pack was loaded from, relative to its layer, so
	// fixtures can be found. displayDir is the same thing as a path a person
	// can open, which the embedded layer does not have.
	dir        string
	displayDir string
	source     Source
	fsys       fs.FS
}

// Dir is the directory this provider was loaded from, or "embedded" when it
// came out of the binary and has no path.
func (p Provider) Dir() string {
	if p.displayDir == "" {
		return "embedded"
	}
	return p.displayDir
}

// Source says which of the three layers this copy came from.
func (p Provider) Source() Source { return p.source }

// Source is where a pack was found. Packs resolve local first, so a provider
// you are working on shadows the synced copy without needing to be pushed
// anywhere, and a private provider never has to leave the machine.
type Source int

const (
	// Embedded was compiled into the binary. It is the snapshot taken at
	// release, and it is what makes herald work before it has ever seen the
	// network.
	Embedded Source = iota + 1
	// Synced was pulled by `herald sync` into the XDG data directory.
	Synced
	// Local was hand-written in the XDG config directory. It wins.
	Local
)

func (s Source) String() string {
	switch s {
	case Embedded:
		return "embedded"
	case Synced:
		return "synced"
	case Local:
		return "local"
	}
	return "unknown"
}

// Secret describes the shared secret the receiving end will verify with. The
// encoding matters: Standard Webhooks secrets are base64 behind a prefix, and
// signing with the printable form produces a signature that verifies against
// nothing.
type Secret struct {
	Label   string `yaml:"label"`
	Example string `yaml:"example"`
	// Prefix is stripped before decoding. "whsec_" for Standard Webhooks.
	Prefix string `yaml:"prefix"`
	// Encoding is how the secret is stored once the prefix is off: "raw" (the
	// default, sign with the bytes as typed) or "base64".
	Encoding string `yaml:"encoding"`
}

// Transport is everything about the request that is not the signature.
type Transport struct {
	Method      string            `yaml:"method"`
	ContentType string            `yaml:"contentType"`
	Headers     map[string]string `yaml:"headers"`
}

// Signature is one computed header. It is a list on Provider rather than a
// single value because GitHub still sends its SHA-1 header alongside the
// SHA-256 one, and a handler that reads the wrong one is exactly the bug worth
// reproducing.
type Signature struct {
	// Header is the header name to set.
	Header string `yaml:"header"`
	// Scheme selects the algorithm family: "hmac" (the default) or "builtin",
	// which hands the whole request to a named Go implementation.
	Scheme string `yaml:"scheme"`
	// Builtin names the implementation when Scheme is "builtin".
	Builtin string `yaml:"builtin"`

	// Algorithm is sha256, sha1 or sha512.
	Algorithm string `yaml:"algorithm"`
	// Encoding is how the digest is rendered: "hex" or "base64".
	Encoding string `yaml:"encoding"`
	// Payload is the string that gets signed, with placeholders. Stripe signs
	// "{timestamp}.{body}", Slack signs "v0:{timestamp}:{body}", Square signs
	// "{url}{body}". Writing it out is why most providers need no code.
	Payload string `yaml:"payload"`
	// Format renders the header value once {signature} is known.
	Format string `yaml:"format"`
}

// Timestamp controls how {timestamp} renders and how stale a delivery has to
// be before the provider would have rejected it.
type Timestamp struct {
	// Format is "unix" (the default), "unixmilli" or "rfc3339".
	Format string `yaml:"format"`
	// Tolerance is the replay window the provider documents. herald does not
	// enforce it; it is the number --skew is measured against, so that
	// "--skew 6m" can say out loud that Stripe would have rejected this.
	Tolerance time.Duration `yaml:"tolerance"`
}

// DeliveryID shapes the per-delivery identifier, so that a delivery looks like one.
// It is cosmetic for most providers and load-bearing for Standard Webhooks,
// where the id is part of the signed string.
type DeliveryID struct {
	// Format is "uuid" (the default) or "token", a 22-character base62 string.
	Format string `yaml:"format"`
	Prefix string `yaml:"prefix"`
}

// Event is optional metadata for one fixture. Fixtures are discovered from
// events/, so a provider only appears here when it needs a description or a
// header that varies by event.
type Event struct {
	Description string            `yaml:"description"`
	Headers     map[string]string `yaml:"headers"`
	// Notes is a caveat for this event alone.
	Notes string `yaml:"notes"`
}

// Fixture is one payload on disk, discovered rather than declared.
type Fixture struct {
	// ID is the filename without its extension: "payment_intent.succeeded".
	ID string
	// Path is the file it was read from.
	Path string
	// Body is the payload exactly as it will be sent. Byte for byte matters:
	// every scheme here signs the raw body, so reformatting the JSON on the
	// way out would invalidate the signature the handler is about to check.
	Body []byte
	// ContentType overrides the provider default when the extension says so,
	// which is how Mollie classic sends a form body from a JSON-shaped pack.
	ContentType string
	Meta        Event
}

func (p Provider) String() string {
	return fmt.Sprintf("%s (%s, %s)", p.ID, p.source, p.Dir())
}
