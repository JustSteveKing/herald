package signer

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/herald/pack"
)

// The delivery every generated vector was computed for. Changing any of these
// means regenerating scripts/gen_vectors.py output.
const (
	vectorBody  = `{"hello":"world"}`
	vectorTime  = 1735689600
	vectorURL   = "http://localhost:8000/webhooks"
	vectorID    = "msg_2mKqR7vXbN3pLwZs8Yt1Fd"
	vectorEvent = "test.event"
)

type packVector struct {
	provider string
	secret   string
	header   string
	want     string
}

func request(body string) *Request {
	return &Request{
		Method: "POST",
		URL:    vectorURL,
		Header: http.Header{},
		Body:   []byte(body),
	}
}

func context() Context {
	return Context{
		Timestamp: time.Unix(vectorTime, 0),
		ID:        vectorID,
		Event:     vectorEvent,
	}
}

// TestPackVectors checks every shipped pack against values computed by a
// separate implementation of the same rules, in a different language, from the
// same YAML. Agreeing with myself would prove nothing; this catches the case
// where the Go engine and the written-down rule have drifted apart.
func TestPackVectors(t *testing.T) {
	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	for _, v := range packVectors {
		t.Run(v.provider+"/"+v.header, func(t *testing.T) {
			provider, ok := set.Provider(v.provider)
			if !ok {
				t.Fatalf("no provider %q", v.provider)
			}

			req := request(vectorBody)
			if err := Sign(provider, req, v.secret, context()); err != nil {
				t.Fatalf("signing: %v", err)
			}

			if got := req.Header.Get(v.header); got != v.want {
				t.Errorf("%s\n got %s\nwant %s", v.header, got, v.want)
			}
		})
	}
}

// TestGitHubPublishedVector uses the example from GitHub's own documentation
// on validating webhook deliveries. It is the only signature here whose
// expected value did not originate in this repository.
func TestGitHubPublishedVector(t *testing.T) {
	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	provider, _ := set.Provider("github")
	req := request("Hello, World!")

	if err := Sign(provider, req, "It's a Secret to Everybody", context()); err != nil {
		t.Fatalf("signing: %v", err)
	}

	const want = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := req.Header.Get("X-Hub-Signature-256"); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestStandardWebhooksPublishedVector uses the example from the Standard
// Webhooks specification. It is the one that proves the secret handling,
// because signing the printable whsec_ string instead of the decoded bytes
// produces a plausible-looking signature that verifies against nothing.
func TestStandardWebhooksPublishedVector(t *testing.T) {
	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	provider, _ := set.Provider("standardwebhooks")
	req := request(`{"test": 2432232314}`)

	ctx := Context{
		Timestamp: time.Unix(1614265330, 0),
		ID:        "msg_p5jXN8AQM9LWM0D4loKWxJek",
	}

	if err := Sign(provider, req, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", ctx); err != nil {
		t.Fatalf("signing: %v", err)
	}

	const want = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	if got := req.Header.Get("webhook-signature"); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestTwilioMatchesSDK follows the construction in twilio-python's
// RequestValidator.compute_signature: the URL, then each distinct value of
// each parameter, keys sorted and values sorted, key repeated before every
// value, no separators. The repeated parameter is the interesting case,
// because joining values in arrival order is the obvious wrong guess and
// produces a signature that looks fine.
func TestTwilioMatchesSDK(t *testing.T) {
	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	provider, _ := set.Provider("twilio")

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "single valued parameters",
			body: "CallSid=CA1234567890ABCDE&Caller=%2B14158675309&Digits=1234&From=%2B14158675309&To=%2B18005551212",
			want: "GcktA2Mwo5ZdznWKqivG1r6lyMU=",
		},
		{
			// b then a, so arrival order and sorted order disagree.
			name: "repeated parameter sorts its values",
			body: "Tag=b&Tag=a",
			want: "5ctAt5qxLAZwET84N4FxZqo31pA=",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &Request{
				Method: "POST",
				URL:    "https://mycompany.com/myapp.php?foo=1&bar=2",
				Header: http.Header{},
				Body:   []byte(tc.body),
			}

			if err := Sign(provider, req, "12345678901234567890123456789012", context()); err != nil {
				t.Fatalf("signing: %v", err)
			}

			if got := req.Header.Get("X-Twilio-Signature"); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestCorruptKeepsTheShape is the contract behind --bad-signature. The header
// has to stay structurally valid so that a handler rejects it on comparison
// rather than on parsing. A handler that 500s here instead of 401ing is the
// finding.
func TestCorruptKeepsTheShape(t *testing.T) {
	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	provider, _ := set.Provider("stripe")

	good := request(vectorBody)
	if err := Sign(provider, good, "whsec_test_secret", context()); err != nil {
		t.Fatalf("signing: %v", err)
	}

	ctx := context()
	ctx.Corrupt = true
	bad := request(vectorBody)
	if err := Sign(provider, bad, "whsec_test_secret", ctx); err != nil {
		t.Fatalf("signing: %v", err)
	}

	g, b := good.Header.Get("Stripe-Signature"), bad.Header.Get("Stripe-Signature")
	if g == b {
		t.Fatal("corrupt produced the same signature")
	}
	if len(g) != len(b) {
		t.Errorf("corrupt changed the length: %d against %d", len(b), len(g))
	}
	if !strings.HasPrefix(b, "t=1735689600,v1=") {
		t.Errorf("corrupt broke the header shape: %s", b)
	}
}

func TestUnknownPlaceholderIsLeftAlone(t *testing.T) {
	req := request(vectorBody)
	got := Expand("{body}/{nonsense}", pack.Provider{}, req, context())

	if want := vectorBody + "/{nonsense}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
