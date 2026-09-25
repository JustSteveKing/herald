package pack

import (
	"fmt"
	"strings"
)

// knownAlgorithms and knownEncodings are closed sets on purpose. A pack is
// fetched from the internet and a typo in it must fail loudly here, because
// the alternative is a signature computed by some fallback that nobody chose
// and a handler that rejects it for reasons the person will go looking for in
// their own code.
var (
	knownAlgorithms = map[string]bool{"sha1": true, "sha256": true, "sha512": true}
	knownEncodings  = map[string]bool{"hex": true, "base64": true}
	knownSchemes    = map[string]bool{"hmac": true, "builtin": true}
	knownSecretEnc  = map[string]bool{"": true, "raw": true, "base64": true}
	knownTimeFmt    = map[string]bool{"": true, "unix": true, "unixmilli": true, "rfc3339": true}
)

func (p Provider) validate() error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !knownSecretEnc[p.Secret.Encoding] {
		return fmt.Errorf("secret encoding %q is not one of raw, base64", p.Secret.Encoding)
	}
	if !knownTimeFmt[p.Timestamp.Format] {
		return fmt.Errorf("timestamp format %q is not one of unix, unixmilli, rfc3339", p.Timestamp.Format)
	}

	for i, sig := range p.Signing {
		if err := sig.validate(); err != nil {
			return fmt.Errorf("signing[%d]: %w", i, err)
		}
	}

	return nil
}

func (s Signature) validate() error {
	if s.Header == "" {
		return fmt.Errorf("header is required")
	}

	s = s.Effective()
	if !knownSchemes[s.Scheme] {
		return fmt.Errorf("scheme %q is not one of hmac, builtin", s.Scheme)
	}

	if s.Scheme == "builtin" {
		if s.Builtin == "" {
			return fmt.Errorf("scheme is builtin but no builtin is named")
		}
		return nil
	}

	if !knownAlgorithms[s.Algorithm] {
		return fmt.Errorf("algorithm %q is not one of sha1, sha256, sha512", s.Algorithm)
	}
	if !knownEncodings[s.Encoding] {
		return fmt.Errorf("encoding %q is not one of hex, base64", s.Encoding)
	}
	if s.Payload == "" {
		return fmt.Errorf("payload is required, and {body} is usually what you want")
	}
	if !strings.Contains(s.Format, "{signature}") {
		return fmt.Errorf("format %q never uses {signature}", s.Format)
	}

	return nil
}

// Effective fills in the defaults a pack is allowed to leave out, so callers
// never have to repeat the "if empty then hmac" dance.
func (s Signature) Effective() Signature {
	if s.Scheme == "" {
		s.Scheme = "hmac"
	}
	if s.Format == "" {
		s.Format = "{signature}"
	}
	return s
}

// Effective is the same for the provider's transport.
func (t Transport) Effective() Transport {
	if t.Method == "" {
		t.Method = "POST"
	}
	if t.ContentType == "" {
		t.ContentType = "application/json"
	}
	return t
}
