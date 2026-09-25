package pack

import (
	"os"
	"path/filepath"

	"github.com/JustSteveKing/herald/providers"
)

// Default loads the three layers in the order herald uses everywhere: the
// embedded snapshot, then anything synced, then anything local.
func Default() (*Set, error) {
	return Load(DefaultLayers()...)
}

// DefaultLayers is where packs are read from, lowest precedence first.
//
// The split between the two directories on disk is deliberate. SyncDir holds
// what `herald sync` downloaded: it is a cache of somebody else's repository,
// it is safe to delete, and sync overwrites it wholesale. LocalDir holds what
// you wrote: a provider that is not public, or one you are still working on,
// or an override of a synced pack whose fixture has gone stale. Sync never
// touches it, which is the only reason it is safe to keep work there.
func DefaultLayers() []Layer {
	layers := []Layer{
		{FS: providers.FS, Root: ".", Source: Embedded},
	}

	if dir := SyncDir(); dir != "" {
		layers = append(layers, Layer{
			FS:     os.DirFS(dir),
			Root:   ".",
			Dir:    dir,
			Source: Synced,
		})
	}

	if dir := LocalDir(); dir != "" {
		layers = append(layers, Layer{
			FS:     os.DirFS(dir),
			Root:   ".",
			Dir:    dir,
			Source: Local,
		})
	}

	return layers
}

// SyncDir is where `herald sync` writes. It is XDG data rather than XDG
// config because it is downloaded, replaceable and not yours to edit.
func SyncDir() string {
	if dir := os.Getenv("HERALD_SYNC_DIR"); dir != "" {
		return dir
	}

	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}

	return filepath.Join(base, "herald", "providers")
}

// LocalDir is where your own packs live. It is XDG config because it is
// hand-written and nothing here will ever overwrite it.
func LocalDir() string {
	if dir := os.Getenv("HERALD_LOCAL_DIR"); dir != "" {
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

	return filepath.Join(base, "herald", "providers")
}
