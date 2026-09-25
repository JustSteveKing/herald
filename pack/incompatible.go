package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Incompatible is a provider herald deliberately does not ship.
//
// The list exists because "no such provider" is the wrong answer to `herald
// send paypal`. There is a reason, it is the same reason every time, and it is
// worth saying once rather than making each person work it out.
//
// The rule: if verifying a delivery means fetching a public key or certificate
// from a domain the provider controls, there is no shared secret and nothing
// outside that provider can produce a delivery that verifies. Shipping a pack
// that sends the payload unsigned would be worse than shipping nothing,
// because it passes against a handler that never verifies anything, and that
// is precisely the handler worth catching.
type Incompatible struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Docs string `yaml:"docs"`
	// Scheme is the short label: what it signs with.
	Scheme string `yaml:"scheme"`
	// Summary is one sentence, for an error message.
	Summary string `yaml:"summary"`
	// Detail is the paragraph, for the README.
	Detail string `yaml:"detail"`
	// Workaround is how to test against that provider instead. Every entry
	// has one, because a list of things herald will not do is only useful if
	// it also says what does.
	Workaround string `yaml:"workaround"`
}

type incompatibleFile struct {
	Incompatible []Incompatible `yaml:"incompatible"`
}

// Incompatible returns one entry by id.
func (s *Set) Incompatible(id string) (Incompatible, bool) {
	e, ok := s.incompatible[id]
	return e, ok
}

// Incompatibles returns every entry, sorted by name.
func (s *Set) Incompatibles() []Incompatible {
	out := make([]Incompatible, 0, len(s.incompatible))
	for _, e := range s.incompatible {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loadIncompatible reads one layer's incompatible.yaml, if it has one.
//
// Entries merge by id rather than replacing the list wholesale, which is the
// opposite of how a provider resolves. A provider is replaced whole because
// half of one version's signing rules and half of another's would sign
// something nobody chose. This is prose, so a team adding their own entry
// should not silently delete the four that ship.
func loadIncompatible(layer Layer, into map[string]Incompatible) error {
	root := layer.Root
	if root == "" {
		root = "."
	}

	raw, err := fs.ReadFile(layer.FS, path.Join(root, "incompatible.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading incompatible.yaml in %s: %w", layer.Dir, err)
	}

	var file incompatibleFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("incompatible.yaml in %s: %w", layer.Dir, err)
	}

	for _, entry := range file.Incompatible {
		if entry.ID == "" {
			return fmt.Errorf("incompatible.yaml in %s: an entry has no id", layer.Dir)
		}
		if entry.Summary == "" {
			return fmt.Errorf("incompatible.yaml in %s: %s has no summary, so there is nothing to tell anyone", layer.Dir, entry.ID)
		}
		into[entry.ID] = entry
	}

	return nil
}
