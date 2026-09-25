package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		header := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	return server
}

func TestExtractTakesOnlyProviders(t *testing.T) {
	dir := t.TempDir()

	raw := tarball(t, map[string]string{
		"JustSteveKing-herald-3b7d9e2/README.md":                     "not a pack",
		"JustSteveKing-herald-3b7d9e2/signer/signer.go":              "package signer",
		"JustSteveKing-herald-3b7d9e2/providers/embed.go":            "package providers",
		"JustSteveKing-herald-3b7d9e2/providers/acme/provider.yaml":  "name: Acme\n",
		"JustSteveKing-herald-3b7d9e2/providers/acme/events/a.json":  "{}",
		"JustSteveKing-herald-3b7d9e2/providers/other/provider.yaml": "name: Other\n",
	})

	commit, providers, err := extract(bytes.NewReader(raw), dir)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if commit != "3b7d9e2" {
		t.Errorf("commit: got %q, want 3b7d9e2", commit)
	}
	if providers != 2 {
		t.Errorf("providers: got %d, want 2", providers)
	}

	if _, err := os.Stat(filepath.Join(dir, "acme", "provider.yaml")); err != nil {
		t.Errorf("acme pack missing: %v", err)
	}
	// Source files from the repository are not packs and must not be written
	// out, or a sync would scatter Go files through a data directory.
	if _, err := os.Stat(filepath.Join(dir, "embed.go")); !os.IsNotExist(err) {
		t.Error("embed.go was extracted and should not have been")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "signer")); err == nil {
		t.Error("something landed outside the target directory")
	}
}

// TestExtractRefusesTraversal is the one that matters. The archive comes from
// the network, and HERALD_PACKS_REPO means it does not have to come from a
// repository anyone here controls.
func TestExtractRefusesTraversal(t *testing.T) {
	dir := t.TempDir()

	raw := tarball(t, map[string]string{
		"owner-repo-abc123/providers/../../escaped.yaml": "name: Escaped\n",
	})

	if _, _, err := extract(bytes.NewReader(raw), dir); err == nil {
		t.Fatal("extract accepted a path that escapes the target directory")
	}
}

func TestRunIsAtomicAndWritesAManifest(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "providers")

	// A pack that is already there and must survive a failed sync.
	if err := os.MkdirAll(filepath.Join(target, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "existing", "provider.yaml"), []byte("name: Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer failing.Close()

	if _, err := Run(context.Background(), Options{Dir: target, BaseURL: failing.URL, Repo: "x/y"}); err == nil {
		t.Fatal("a 500 from the API should have failed the sync")
	}

	if _, err := os.Stat(filepath.Join(target, "existing", "provider.yaml")); err != nil {
		t.Errorf("a failed sync removed the packs that were already there: %v", err)
	}

	raw := tarball(t, map[string]string{
		"owner-repo-deadbee/providers/acme/provider.yaml": "name: Acme\n",
	})
	server := serve(t, raw)

	manifest, err := Run(context.Background(), Options{
		Dir:     target,
		BaseURL: server.URL,
		Repo:    "owner/repo",
		Ref:     "main",
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if manifest.Providers != 1 {
		t.Errorf("providers: got %d, want 1", manifest.Providers)
	}
	if manifest.Commit != "deadbee" {
		t.Errorf("commit: got %q, want deadbee", manifest.Commit)
	}

	if _, err := os.Stat(filepath.Join(target, "acme", "provider.yaml")); err != nil {
		t.Errorf("the new pack is not there: %v", err)
	}
	// A sync replaces the directory rather than merging into it, so a provider
	// that has been removed upstream goes away here too.
	if _, err := os.Stat(filepath.Join(target, "existing")); !os.IsNotExist(err) {
		t.Error("a pack removed upstream survived the sync")
	}

	if read, ok := ReadManifest(target); !ok || read.Commit != "deadbee" {
		t.Errorf("manifest did not round trip: %+v", read)
	}
}
