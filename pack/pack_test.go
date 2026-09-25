package pack_test

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/JustSteveKing/herald/pack"
	"github.com/JustSteveKing/herald/signer"
)

// embedded loads only what ships in the binary. It ignores the sync and local
// directories, so the suite tests the repository rather than whatever happens
// to be on the machine running it.
func embedded(t *testing.T) *pack.Set {
	t.Helper()

	t.Setenv("HERALD_SYNC_DIR", t.TempDir())
	t.Setenv("HERALD_LOCAL_DIR", t.TempDir())

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	return set
}

// TestEveryPackIsUsable is the gate a pull request adding a provider has to
// get through. Everything it checks is something that would otherwise be found
// by someone whose webhook did not verify.
func TestEveryPackIsUsable(t *testing.T) {
	set := embedded(t)

	if set.Len() == 0 {
		t.Fatal("no packs are embedded")
	}

	builtins := signer.Builtins()

	for _, p := range set.All() {
		t.Run(p.ID, func(t *testing.T) {
			if p.Docs == "" {
				t.Error("no docs link: every signing rule here is a claim about someone else's system and needs a source")
			} else if _, err := url.ParseRequestURI(p.Docs); err != nil {
				t.Errorf("docs is not a URL: %v", err)
			}

			if len(p.Signing) > 0 && p.Secret.Example == "" {
				t.Error("signs its webhooks but gives no example secret, so nothing can be sent without --secret")
			}

			for _, sig := range p.Signing {
				sig = sig.Effective()
				if sig.Scheme != "builtin" {
					continue
				}
				if !slices.Contains(builtins, sig.Builtin) {
					t.Errorf("names builtin %q, which this binary does not have. Known: %s",
						sig.Builtin, strings.Join(builtins, ", "))
				}
			}

			fixtures, err := p.Fixtures()
			if err != nil {
				t.Fatalf("listing fixtures: %v", err)
			}
			if len(fixtures) == 0 {
				t.Fatal("no events, so there is nothing to send")
			}

			for _, listed := range fixtures {
				f, err := p.Fixture(listed.ID)
				if err != nil {
					t.Errorf("%s: %v", listed.ID, err)
					continue
				}

				if len(f.Body) == 0 {
					t.Errorf("%s: empty payload", f.ID)
				}

				// The body is signed as it sits on disk, so a JSON fixture that
				// does not parse would be delivered, verify correctly, and then
				// fail inside the handler for a reason that looks like the
				// handler's fault.
				if f.ContentType == "application/json" && !json.Valid(f.Body) {
					t.Errorf("%s: not valid JSON", f.ID)
				}
			}

			// Metadata is keyed by event id, and a typo there silently does
			// nothing: no description, and worse, no header override.
			for id := range p.Meta {
				if !slices.ContainsFunc(fixtures, func(f pack.Fixture) bool { return f.ID == id }) {
					t.Errorf("events: has metadata for %q, but there is no such fixture", id)
				}
			}
		})
	}
}

func TestLocalOverridesSynced(t *testing.T) {
	synced, local := t.TempDir(), t.TempDir()
	t.Setenv("HERALD_SYNC_DIR", synced)
	t.Setenv("HERALD_LOCAL_DIR", local)

	write(t, synced, "acme", "name: Synced\ndocs: https://example.com\nsigning: []\n")
	write(t, local, "acme", "name: Local\ndocs: https://example.com\nsigning: []\n")

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	p, ok := set.Provider("acme")
	if !ok {
		t.Fatal("acme did not load")
	}

	if p.Name != "Local" {
		t.Errorf("name is %q, want Local: a pack you wrote must beat one you downloaded", p.Name)
	}
	if p.Source() != pack.Local {
		t.Errorf("source is %s, want local", p.Source())
	}
}

func TestSyncedOverridesEmbedded(t *testing.T) {
	synced := t.TempDir()
	t.Setenv("HERALD_SYNC_DIR", synced)
	t.Setenv("HERALD_LOCAL_DIR", t.TempDir())

	write(t, synced, "stripe", "name: Newer Stripe\ndocs: https://example.com\nsigning: []\n")

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	p, _ := set.Provider("stripe")
	if p.Name != "Newer Stripe" {
		t.Errorf("name is %q: a synced pack must replace the embedded one", p.Name)
	}
	// Replaced whole, not merged. A half-and-half provider would sign with one
	// version's rules and send another version's payload.
	if len(p.Signing) != 0 {
		t.Error("the embedded signing rules survived into a synced override")
	}
}

func TestDirectoryNameWinsOverADisagreeingID(t *testing.T) {
	local := t.TempDir()
	t.Setenv("HERALD_SYNC_DIR", t.TempDir())
	t.Setenv("HERALD_LOCAL_DIR", local)

	write(t, local, "acme", "id: something-else\nname: Acme\ndocs: https://example.com\n")

	if _, err := pack.Default(); err == nil {
		t.Fatal("a pack whose id disagrees with its directory loaded without complaint")
	}
}

func TestInvalidSigningIsRefused(t *testing.T) {
	tests := map[string]string{
		"unknown algorithm":            "name: A\ndocs: https://e.com\nsigning:\n  - header: X-Sig\n    algorithm: md5\n    encoding: hex\n    payload: \"{body}\"\n",
		"missing payload":              "name: A\ndocs: https://e.com\nsigning:\n  - header: X-Sig\n    algorithm: sha256\n    encoding: hex\n",
		"format ignores the signature": "name: A\ndocs: https://e.com\nsigning:\n  - header: X-Sig\n    algorithm: sha256\n    encoding: hex\n    payload: \"{body}\"\n    format: \"t={timestamp}\"\n",
		"builtin with no name":         "name: A\ndocs: https://e.com\nsigning:\n  - header: X-Sig\n    scheme: builtin\n",
	}

	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HERALD_SYNC_DIR", t.TempDir())
			t.Setenv("HERALD_LOCAL_DIR", dir)

			write(t, dir, "acme", yaml)

			if _, err := pack.Default(); err == nil {
				t.Error("loaded without complaint, and would have signed with some default nobody chose")
			}
		})
	}
}

func write(t *testing.T, root, id, yaml string) {
	t.Helper()

	if err := mkdirAll(root, id); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(root, id, "provider.yaml", yaml); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(root, id+"/events"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(root, id, "events/test.json", "{}\n"); err != nil {
		t.Fatal(err)
	}
}
