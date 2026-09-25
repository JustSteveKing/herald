// Package sync downloads provider packs from a GitHub repository into the
// local sync directory.
//
// The packs are data and the binary is code, and they are released on
// different clocks. A provider that changes its signature header should be
// fixable by editing a YAML file and running `herald sync`, not by waiting for
// someone to cut a release. That is also what makes a pull request from the
// provider worth anything: it reaches people the day it merges.
package sync

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultRepo is where packs come from unless told otherwise. Pointing
	// HERALD_PACKS_REPO at a fork is how an organisation ships private
	// providers to its own people without publishing them.
	DefaultRepo = "JustSteveKing/herald"
	// DefaultRef is the branch or tag to pull.
	DefaultRef = "main"

	// maxArchive caps what will be read from the network. A pack repository is
	// a few hundred kilobytes of text; anything at this size is a mistake or
	// something worse, and unpacking it would fill the disk quietly.
	maxArchive = 64 << 20
	maxFile    = 4 << 20
)

// Options configures a sync.
type Options struct {
	Repo string
	Ref  string
	// Dir is where packs land. Empty means pack.SyncDir().
	Dir    string
	Client *http.Client
	// BaseURL is the GitHub API root. It exists so the tests can drive a real
	// Run against a local server rather than asserting on its parts.
	BaseURL string
}

// Manifest records what the sync directory currently holds, so `herald
// providers` can say how old it is without going back to the network.
type Manifest struct {
	Repo      string    `json:"repo"`
	Ref       string    `json:"ref"`
	Commit    string    `json:"commit"`
	FetchedAt time.Time `json:"fetchedAt"`
	Providers int       `json:"providers"`
}

// Run downloads the providers tree and replaces the sync directory with it.
//
// The replacement is atomic: everything is extracted beside the target and
// moved into place at the end. A sync interrupted halfway leaves the previous
// packs intact, because the alternative is a tool that signs with half of one
// version and half of another and cannot say which.
func Run(ctx context.Context, opts Options) (Manifest, error) {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.Ref == "" {
		opts.Ref = DefaultRef
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Dir == "" {
		return Manifest{}, fmt.Errorf("no target directory")
	}

	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.github.com"
	}

	url := fmt.Sprintf("%s/repos/%s/tarball/%s", strings.TrimSuffix(opts.BaseURL, "/"), opts.Repo, opts.Ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "herald")
	// A token is only needed for a private repository or a rate limited
	// network, so it stays optional rather than becoming a setup step. Both
	// names are read because GH_TOKEN is what the gh CLI sets and the one
	// people already have exported.
	//
	// herald does not shell out to gh for a token. Reading someone's stored
	// credentials because a download failed is not a thing a tool should do
	// without being asked; the error below asks.
	token := cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := opts.Client.Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return Manifest{}, describeFailure(url, opts, res.StatusCode, res.Status, token != "")
	}

	if err := os.MkdirAll(filepath.Dir(opts.Dir), 0o755); err != nil {
		return Manifest{}, err
	}

	staging, err := os.MkdirTemp(filepath.Dir(opts.Dir), ".herald-sync-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(staging)

	commit, count, err := extract(io.LimitReader(res.Body, maxArchive), staging)
	if err != nil {
		return Manifest{}, err
	}
	if count == 0 {
		return Manifest{}, fmt.Errorf("%s has no providers/ directory at %s", opts.Repo, opts.Ref)
	}

	manifest := Manifest{
		Repo:      opts.Repo,
		Ref:       opts.Ref,
		Commit:    commit,
		FetchedAt: time.Now().UTC(),
		Providers: count,
	}
	if err := writeManifest(staging, manifest); err != nil {
		return Manifest{}, err
	}

	// Rename cannot replace a non-empty directory, so the old one moves aside
	// first and is only deleted once the new one is in place.
	previous := opts.Dir + ".previous"
	_ = os.RemoveAll(previous)
	if err := os.Rename(opts.Dir, previous); err != nil && !os.IsNotExist(err) {
		return Manifest{}, err
	}
	if err := os.Rename(staging, opts.Dir); err != nil {
		_ = os.Rename(previous, opts.Dir)
		return Manifest{}, err
	}
	_ = os.RemoveAll(previous)

	return manifest, nil
}

// describeFailure turns a status code into something worth reading.
//
// GitHub answers a request for a private repository with 404 rather than 403,
// so that it does not confirm the repository exists. That is the right thing
// for GitHub to do and a miserable error to receive, because the obvious
// reading is that the ref is wrong. Saying so here costs nothing and saves
// somebody checking a tag name that was correct all along.
func describeFailure(url string, opts Options, code int, status string, authorised bool) error {
	switch code {
	case http.StatusNotFound:
		if authorised {
			return fmt.Errorf("%s: %s. The token was sent, so check that %s exists and has a ref called %s",
				url, status, opts.Repo, opts.Ref)
		}
		return fmt.Errorf("%s: %s. GitHub answers 404 rather than 403 for a private repository, "+
			"so this is either the wrong repo or ref, or %s is private and no token was sent. "+
			"Set GITHUB_TOKEN (gh auth token prints one) and try again",
			url, status, opts.Repo)

	case http.StatusUnauthorized, http.StatusForbidden:
		if authorised {
			return fmt.Errorf("%s: %s. A token was sent and refused, so it is expired, "+
				"or it cannot read %s, or you are rate limited", url, status, opts.Repo)
		}
		return fmt.Errorf("%s: %s. No token was sent. Set GITHUB_TOKEN (gh auth token prints one)", url, status)
	}

	return fmt.Errorf("%s: %s", url, status)
}

// extract unpacks providers/** from a GitHub tarball, returning the commit the
// archive was made from and how many providers it held.
func extract(r io.Reader, dest string) (commit string, providers int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", 0, err
	}
	defer gz.Close()

	seen := map[string]bool{}
	tr := tar.NewReader(gz)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}

		// GitHub wraps everything in one directory named owner-repo-sha.
		root, rest, ok := strings.Cut(header.Name, "/")
		if !ok {
			continue
		}
		if commit == "" {
			if i := strings.LastIndex(root, "-"); i >= 0 {
				commit = root[i+1:]
			}
		}

		rest, ok = strings.CutPrefix(rest, "providers/")
		if !ok || rest == "" {
			continue
		}
		// Only pack content. A .go file in providers/ is the embed declaration
		// and has no business on disk.
		if !isPackFile(rest) {
			continue
		}

		target, err := safeJoin(dest, rest)
		if err != nil {
			return "", 0, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", 0, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", 0, err
			}
			if err := writeFile(target, io.LimitReader(tr, maxFile)); err != nil {
				return "", 0, err
			}
			if id, _, ok := strings.Cut(rest, "/"); ok && !seen[id] {
				seen[id] = true
			}
		}
	}

	return commit, len(seen), nil
}

func isPackFile(name string) bool {
	switch filepath.Ext(name) {
	case ".yaml", ".yml", ".json", ".xml", ".form", ".txt", ".md":
		return true
	}
	return false
}

// safeJoin refuses any path that would land outside dest. An archive is
// attacker-controlled input the moment the repository is not yours, and a
// single ../ entry in it would otherwise write anywhere the user can.
//
// It refuses rather than sanitises. Quietly rewriting ../../x.yaml to x.yaml
// would make a malformed or hostile archive look like it synced cleanly, and
// the whole point of noticing is to be able to say which repository did it.
func safeJoin(dest, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive entry %q is an absolute path", name)
	}

	for _, element := range strings.Split(name, "/") {
		if element == ".." {
			return "", fmt.Errorf("archive entry %q escapes the target directory", name)
		}
	}

	target := filepath.Join(dest, name)
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the target directory", name)
	}

	return target, nil
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	return err
}

func writeManifest(dir string, m Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(raw, '\n'), 0o644)
}

// ReadManifest returns what the last sync recorded, if there was one.
func ReadManifest(dir string) (Manifest, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Manifest{}, false
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, false
	}

	return m, true
}
