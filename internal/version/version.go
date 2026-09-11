// Package version holds gossms's own version metadata, mirroring the same
// pattern gosmo/version/version.go uses. Version/Commit/Date are never
// hand-edited — they resolve automatically, in priority order:
//
//  1. -ldflags -X, set by .github/workflows/release.yml from the pushed git
//     tag ($GITHUB_REF_NAME / git describe):
//
//     -ldflags "-X github.com/radix29/gossms/internal/version.Version=... \
//     -X github.com/radix29/gossms/internal/version.Commit=...  \
//     -X github.com/radix29/gossms/internal/version.Date=..."
//
//  2. debug.BuildInfo.Main.Version — checked in init, below, only when
//     ldflags didn't already set Version. Go populates it with the tag for
//     `go install github.com/radix29/gossms/cmd/gossms@<tag>` (or @latest),
//     and, since Go 1.24, with a pseudo-version for a plain `git clone &&
//     go build`: "v0.0.11-0.20260911113756-cf929d309586" is a commit after
//     v0.0.10, with "+dirty" appended when the tree had uncommitted
//     changes. A pseudo-version is a semver pre-release of the next patch,
//     and the update check ranks it below that release.
//
//  3. The literal "(devel)" default, left alone only when the binary
//     carries no module version at all: `go run`, or `go build
//     -buildvcs=false` — matching the convention `go version -m` itself
//     uses for an unresolved main-module version. Commit/Date populate
//     from the VCS info the Go toolchain embeds in a binary built from a
//     checkout (go help buildvcs), so the About dialog shows commit + build
//     date whenever that info is there.
//
// Shared between cmd/gossms and internal/tui (the About dialog).
package version

import (
	"runtime"
	"runtime/debug"
)

// Name is gossms's program name, for display purposes.
const Name = "gossms"

// License and Copyright are what Help > About shows. gossms is GPL-3.0, so
// a binary that carries it owes the user the licence notice; keep these in
// step with the LICENSE file at the repo root.
const (
	License   = "GPL-3.0-or-later"
	Copyright = "© 2026 radix29"
)

var (
	Version = "(devel)"
	Commit  = "unknown"
	Date    = "unknown"
)

// init resolves Version from debug.BuildInfo.Main.Version (source 2 above)
// and Commit/Date from the VCS info the Go toolchain stamps into every
// binary built from a checkout (go help buildvcs) — the commit revision and
// commit time, plus whether the tree had uncommitted changes. Each is left
// alone when -ldflags -X already set it, so that override path always wins.
func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if Version == "(devel)" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			if Date == "unknown" {
				Date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision != "" && Commit == "unknown" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if dirty {
			revision += "-dirty"
		}
		Commit = revision
	}
}

// Runtime returns the "GOOS/GOARCH" pair the binary was built for.
func Runtime() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
