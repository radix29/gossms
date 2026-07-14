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
//  2. debug.BuildInfo.Main.Version, which Go itself populates when someone
//     runs `go install github.com/radix29/gossms/cmd/gossms@<tag>` (or
//     @latest) — checked in init, below, only when ldflags didn't already
//     set Version.
//
//  3. The literal "(devel)" default, left alone for a plain `git clone &&
//     go build`/`go run` with no ldflags — matching the same convention
//     `go version -m` itself uses for an unresolved main-module version.
//     Commit/Date still populate from the VCS info the Go toolchain embeds
//     in every binary built from a checkout (go help buildvcs) even in
//     this case, so the About dialog still shows commit + build date.
//
// Shared between cmd/gossms and internal/tui (the About dialog).
package version

import (
	"runtime"
	"runtime/debug"
)

// Name is gossms's program name, for display purposes.
const Name = "gossms"

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
