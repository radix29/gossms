// Package version holds gossms's own version metadata, mirroring the same
// pattern gosmo/version/version.go uses. Commit/Date are filled in
// automatically at build time from the VCS info the Go toolchain embeds in
// every binary (see init, below) — no ldflags or Makefile required. Version/
// Commit/Date remain vars, not consts, so a packaging build without a .git
// checkout can still override them at link time via:
//
//	-ldflags "-X github.com/radix29/gossms/internal/version.Version=... \
//	          -X github.com/radix29/gossms/internal/version.Commit=...  \
//	          -X github.com/radix29/gossms/internal/version.Date=..."
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
	Version = "v0.0.2"
	Commit  = "unknown"
	Date    = "unknown"
)

// init fills Commit/Date from the build info the Go toolchain stamps into
// every binary built from a VCS checkout (go help buildvcs) — the commit
// revision and commit time, plus whether the tree had uncommitted changes.
// Left alone (at "unknown") when that info isn't present, or when -ldflags
// -X already set one of them, so that override path still wins.
func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
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
