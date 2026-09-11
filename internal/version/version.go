// Package version holds gossms's version metadata, mirroring gosmo/version.
// Version/Commit/Date are never hand-edited; they resolve in priority order:
//
//  1. -ldflags -X, set by .github/workflows/release.yml from the pushed tag.
//  2. debug.BuildInfo.Main.Version, when ldflags didn't set Version: the tag
//     for `go install ...@<tag>`, or a pseudo-version for a plain `go build` in
//     a checkout ("v0.0.11-0.20260911113756-cf929d309586", "+dirty" if the tree
//     had changes). A pseudo-version is a pre-release of the next patch, so the
//     update check ranks it below that release.
//  3. The literal "(devel)", only when the binary has no module version (`go
//     run`, `go build -buildvcs=false`), matching `go version -m`.
//
// Commit/Date come from the VCS info embedded by the toolchain (go help
// buildvcs). Used by cmd/gossms and the About dialog.
package version

import (
	"runtime"
	"runtime/debug"
)

// Name is gossms's program name, for display purposes.
const Name = "gossms"

// License and Copyright are shown in Help > About (GPL-3.0 requires the
// notice). Keep in step with LICENSE.
const (
	License   = "GPL-3.0-or-later"
	Copyright = "© 2026 radix29"
)

var (
	Version = "(devel)"
	Commit  = "unknown"
	Date    = "unknown"
)

// init resolves Version from debug.BuildInfo (source 2) and Commit/Date from
// the embedded VCS info (revision, time, modified). Values set by -ldflags -X
// always win.
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
