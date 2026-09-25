// Package providers is the snapshot of provider packs compiled into the
// binary at release.
//
// It exists so that a fresh install works with no network. `herald sync`
// replaces it with whatever is current, but the binary is never empty and
// never fails because GitHub is having a morning. The cost is that a release
// carries fixtures that may be months behind, which is why `herald providers`
// prints where each one came from.
package providers

import "embed"

//go:embed */provider.yaml */events/*
var FS embed.FS
