package main

import (
	"strings"
	"testing"

	"github.com/JustSteveKing/herald/pack"
)

func packs(t *testing.T) *pack.Set {
	t.Helper()

	t.Setenv("HERALD_SYNC_DIR", t.TempDir())
	t.Setenv("HERALD_LOCAL_DIR", t.TempDir())

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	return set
}

// TestNamingAnIncompatibleProviderExplainsItself is the behaviour the list
// exists for. "no provider paypal" would send someone to the issue tracker to
// ask for a pack that should not exist.
func TestNamingAnIncompatibleProviderExplainsItself(t *testing.T) {
	set := packs(t)

	_, err := provider(set, "paypal")
	if err == nil {
		t.Fatal("paypal resolved to a pack")
	}

	message := err.Error()

	for _, want := range []string{"cannot be faked", "Instead:", "simulator", "developer.paypal.com"} {
		if !strings.Contains(message, want) {
			t.Errorf("the error never mentions %q:\n%s", want, message)
		}
	}
	// The generic "did you mean" list would bury the real answer.
	if strings.Contains(message, "Known:") {
		t.Errorf("fell through to the unknown-provider message:\n%s", message)
	}
}

func TestNamingSomethingThatIsNotAProviderListsWhatIs(t *testing.T) {
	set := packs(t)

	_, err := provider(set, "notaprovider")
	if err == nil {
		t.Fatal("notaprovider resolved")
	}

	if !strings.Contains(err.Error(), "stripe") {
		t.Errorf("the error does not say what is available:\n%s", err)
	}
}

func TestSecretFallsBackThroughTheEnvironment(t *testing.T) {
	set := packs(t)
	stripe, _ := set.Provider("stripe")

	if got := resolveSecret(stripe, "from-the-flag"); got != "from-the-flag" {
		t.Errorf("flag lost: %q", got)
	}

	t.Setenv("HERALD_SECRET", "general")
	if got := resolveSecret(stripe, ""); got != "general" {
		t.Errorf("HERALD_SECRET ignored: %q", got)
	}

	t.Setenv("HERALD_SECRET_STRIPE", "specific")
	if got := resolveSecret(stripe, ""); got != "specific" {
		t.Errorf("the provider specific variable should win: %q", got)
	}

	// A provider whose id has a dash reaches the same naming scheme.
	classic, _ := set.Provider("mollie-classic")
	t.Setenv("HERALD_SECRET_MOLLIE_CLASSIC", "dashed")
	if got := resolveSecret(classic, ""); got != "dashed" {
		t.Errorf("mollie-classic did not map to HERALD_SECRET_MOLLIE_CLASSIC: %q", got)
	}
}

func TestParseHeadersRejectsNonsense(t *testing.T) {
	if _, err := parseHeaders([]string{"not-a-pair"}); err == nil {
		t.Error("accepted a header with no value")
	}

	got, err := parseHeaders([]string{"X-Thing=value=with=equals"})
	if err != nil {
		t.Fatal(err)
	}
	if got["X-Thing"] != "value=with=equals" {
		t.Errorf("only the first separator should split: %q", got["X-Thing"])
	}
}
