// Package state remembers the small things between runs.
//
// Targets only. Secrets are deliberately not persisted: herald would become a
// file full of signing secrets sitting in a config directory, for the sake of
// saving a paste. They come from a flag, the environment, or the pack's
// example, all of which are somebody's explicit choice each time.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State is what carries over between sessions.
type State struct {
	// Targets is the last URL used per provider. Per provider rather than one
	// global, because a Stripe endpoint and a GitHub endpoint are different
	// routes in the same application and swapping between them is the whole
	// motion this tool is for.
	Targets map[string]string `json:"targets"`
	// Provider is whichever one was open last.
	Provider string `json:"provider"`

	path string
}

// Load reads the saved state, returning an empty one if there is none. A
// missing or unreadable file is not an error worth surfacing: the tool works
// without it and the only cost is retyping a URL.
func Load() *State {
	s := &State{Targets: map[string]string{}, path: path()}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}

	var read State
	if err := json.Unmarshal(raw, &read); err != nil {
		return s
	}

	if read.Targets == nil {
		read.Targets = map[string]string{}
	}
	read.path = s.path

	return &read
}

// Save writes the state back, and says nothing if it cannot.
func (s *State) Save() {
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(s.path, append(raw, '\n'), 0o644)
}

// Target is the remembered URL for a provider.
func (s *State) Target(provider string) string { return s.Targets[provider] }

// SetTarget remembers a URL for a provider.
func (s *State) SetTarget(provider, target string) {
	if s.Targets == nil {
		s.Targets = map[string]string{}
	}
	s.Targets[provider] = target
}

func path() string {
	if dir := os.Getenv("HERALD_STATE_FILE"); dir != "" {
		return dir
	}

	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}

	return filepath.Join(base, "herald", "state.json")
}
