package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Layer is one place packs are read from. Layers are given to Load in
// precedence order, lowest first, and a provider found in a later layer
// replaces the whole earlier one rather than merging with it. Merging would
// mean a synced pack could half-change a provider you had overridden, and you
// would be debugging a signature built from two sources.
type Layer struct {
	FS fs.FS
	// Root is the directory inside FS holding one subdirectory per provider.
	Root string
	// Dir is the human-readable path, for `herald providers` to print. It can
	// be empty for the embedded layer, which has no path on disk.
	Dir    string
	Source Source
}

// Set is the resolved providers, one per id, and the providers that are
// deliberately absent.
type Set struct {
	byID         map[string]Provider
	incompatible map[string]Incompatible
}

// Load reads every layer in order and returns what won. A layer that does not
// exist is not an error: a fresh install has no synced directory and no local
// one, and that is the normal case rather than a broken one.
func Load(layers ...Layer) (*Set, error) {
	set := &Set{byID: map[string]Provider{}, incompatible: map[string]Incompatible{}}

	for _, layer := range layers {
		root := layer.Root
		if root == "" {
			root = "."
		}

		if err := loadIncompatible(layer, set.incompatible); err != nil {
			return nil, err
		}

		entries, err := fs.ReadDir(layer.FS, root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", layer.Dir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			provider, err := loadProvider(layer, path.Join(root, entry.Name()), entry.Name())
			if err != nil {
				return nil, err
			}
			set.byID[provider.ID] = provider
		}
	}

	return set, nil
}

func loadProvider(layer Layer, dir, name string) (Provider, error) {
	raw, err := fs.ReadFile(layer.FS, path.Join(dir, "provider.yaml"))
	if err != nil {
		return Provider{}, fmt.Errorf("%s: %w", path.Join(dir, "provider.yaml"), err)
	}

	var provider Provider
	if err := yaml.Unmarshal(raw, &provider); err != nil {
		return Provider{}, fmt.Errorf("%s: %w", path.Join(dir, "provider.yaml"), err)
	}

	// The directory name is the id. Letting provider.yaml disagree with its own
	// directory would make `herald send stripe` ambiguous the first time
	// someone copied a pack to start a new one and forgot to edit the field.
	if provider.ID != "" && provider.ID != name {
		return Provider{}, fmt.Errorf("%s: id is %q but the directory is %q", dir, provider.ID, name)
	}
	provider.ID = name
	provider.dir = dir
	provider.source = layer.Source
	provider.fsys = layer.FS
	if layer.Dir != "" {
		provider.displayDir = path.Join(layer.Dir, name)
	}

	if err := provider.validate(); err != nil {
		return Provider{}, fmt.Errorf("%s: %w", dir, err)
	}

	return provider, nil
}

// Provider returns one provider by id.
func (s *Set) Provider(id string) (Provider, bool) {
	p, ok := s.byID[id]
	return p, ok
}

// All returns every provider, sorted by id so output is stable between runs.
func (s *Set) All() []Provider {
	out := make([]Provider, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len is how many providers resolved.
func (s *Set) Len() int { return len(s.byID) }

// contentTypes maps a fixture extension to what the request should say it is.
// The extension is how a pack sends a form body without needing a field for
// it, which is the whole of Mollie's classic webhook.
var contentTypes = map[string]string{
	".json": "application/json",
	".xml":  "application/xml",
	".form": "application/x-www-form-urlencoded",
	".txt":  "",
}

// Fixtures lists every payload in the pack, sorted by id.
func (p Provider) Fixtures() ([]Fixture, error) {
	entries, err := fs.ReadDir(p.fsys, path.Join(p.dir, "events"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ID, err)
	}

	var out []Fixture
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		id, contentType, ok := splitFixtureName(entry.Name())
		if !ok {
			continue
		}

		out = append(out, Fixture{
			ID:          id,
			Path:        path.Join(p.dir, "events", entry.Name()),
			ContentType: contentType,
			Meta:        p.Meta[id],
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Fixture reads one payload by event id.
func (p Provider) Fixture(id string) (Fixture, error) {
	fixtures, err := p.Fixtures()
	if err != nil {
		return Fixture{}, err
	}

	for _, f := range fixtures {
		if f.ID != id {
			continue
		}

		body, err := fs.ReadFile(p.fsys, f.Path)
		if err != nil {
			return Fixture{}, fmt.Errorf("%s: %w", f.Path, err)
		}
		// Editors add a trailing newline and most fixtures are hand-edited at
		// some point. A provider signs the bytes it sends, so the newline would
		// be signed too and everything would still verify, but the body would
		// differ from what the provider actually sends by one byte for no
		// reason. Trim it once, here, rather than in every pack.
		f.Body = trimFinalNewline(body)
		return f, nil
	}

	return Fixture{}, fmt.Errorf("%s has no event %q", p.ID, id)
}

func splitFixtureName(name string) (id, contentType string, ok bool) {
	for ext, ct := range contentTypes {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext), ct, true
		}
	}
	return "", "", false
}

func trimFinalNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		return b[:n-1]
	}
	return b
}
