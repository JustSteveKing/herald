package signer

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Builtin is a signature scheme that cannot be written down as data.
//
// There are two kinds of provider that end up here. One signs something other
// than a string built from the request, which the placeholder engine can
// already express. The other derives its input from the request in a way that
// needs real code: sorting, decoding, hashing a part of it first. Twilio is
// the second kind. Keep this map small, because every entry in it is a
// provider that can only be added by releasing a new binary, which is the
// thing packs exist to avoid.
type Builtin func(req *Request, secret []byte, ctx Context) (string, error)

var builtins = map[string]Builtin{
	"twilio": twilio,
}

// Builtins lists the registered names, for `herald doctor` to check a pack
// against the binary it is running under.
func Builtins() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// twilio signs the full request URL with every POST parameter appended to it,
// sorted by key, as key immediately followed by value and no separator
// anywhere.
//
// Two details come from Twilio's own validator rather than from their prose,
// and both are the kind of thing a from-memory implementation gets wrong.
// Repeated parameters contribute each of their distinct values, sorted, with
// the key repeated before each one; they are not joined in arrival order. And
// Twilio's receiving-side validator tries the URL both with and without an
// explicit port, because their generation is inconsistent about it, which
// means a signature herald produces for one form still verifies.
//
// The consequence that matters in practice: the URL is signed, so a signature
// is only good for the exact address it was made for. A handler behind a proxy
// that terminates TLS sees http where Twilio signed https, and rejects real
// requests. Reproducing that locally is most of the reason this provider is
// worth having.
func twilio(req *Request, secret []byte, _ Context) (string, error) {
	values, err := url.ParseQuery(string(req.Body))
	if err != nil {
		return "", fmt.Errorf("twilio signs form parameters and the body did not parse as a form: %w", err)
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(req.URL)
	for _, key := range keys {
		for _, value := range sortedUnique(values[key]) {
			b.WriteString(key)
			b.WriteString(value)
		}
	}

	mac := hmac.New(sha1.New, secret)
	mac.Write([]byte(b.String()))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func sortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))

	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}

	sort.Strings(out)
	return out
}
