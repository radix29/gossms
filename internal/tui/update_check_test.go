package tui

import (
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.9.0", "v1.10.0", -1}, // numeric, not lexical, comparison
		{"v1.10.0", "v1.9.0", 1},
		{"v2.0.0", "v1.9.9", 1},
		// A pre-release, including the pseudo-version a checkout build
		// stamps since Go 1.24, ranks below its release.
		{"v0.0.11-0.20260911113756-cf929d309586", "v0.0.11", -1},
		{"v0.0.11", "v0.0.11-0.20260911113756-cf929d309586", 1},
		{"v0.0.11-0.20260911113756-cf929d309586+dirty", "v0.0.11", -1},
		{"v0.0.11-0.20260911113756-cf929d309586", "v0.0.10", 1}, // built after v0.0.10
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0.0-rc1", "v1.0.0-rc1", 0},
		{"v1.0.0-rc1", "v0.9.9", 1}, // the release triple still dominates
		// Pre-release identifiers, per semver 2.0.0 §11.
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1}, // a shorter prefix ranks lower
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1},
		{"v1.0.0-alpha.beta", "v1.0.0-beta", -1},
		{"v1.0.0-beta.2", "v1.0.0-beta.11", -1}, // numeric identifiers compare numerically
		{"v1.0.0-beta.11", "v1.0.0-rc.1", -1},
		{"v1.0.0-1", "v1.0.0-alpha", -1}, // numeric ranks below alphanumeric
		// Build metadata never affects precedence.
		{"v1.0.0+build.5", "v1.0.0", 0},
		{"v1.0.0+dirty", "v1.0.0+other", 0},
		{"v1.0.1+build.5", "v1.0.1", 0},
		{"v1.0.0-rc.1+dirty", "v1.0.0-rc.1", 0},
		{"(devel)", "v1.0.0", -1}, // unparseable tag compares as v0.0.0
		{"v1.0.0", "(devel)", 1},
		{"(devel)", "(devel)", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"v1.2.3", true},
		{"v1.2.3-rc1", true},
		{"v0.0.11-0.20260911113756-cf929d309586+dirty", true}, // checkout build
		{"v0.0.0", true},
		{"1.2.3", true}, // "v" prefix not required
		{"(devel)", false},
		{"", false},
		{"vX.Y.Z", false},
	}
	for _, c := range cases {
		if got := isReleaseVersion(c.v); got != c.want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// TestUpdateDialogShowResultDevBuild pins down that version.Version's
// "(devel)" placeholder (see its doc comment) reports "this isn't a
// comparable build" rather than being compared numerically: it parses as
// v0.0.0, which would tell every plain `git clone && go build` user that a
// new version is available.
func TestUpdateDialogShowResultDevBuild(t *testing.T) {
	d := &UpdateDialog{}
	d.ShowResult("(devel)", githubRelease{TagName: "v1.2.3"}, nil)

	joined := strings.Join(d.lines, "\n")
	if !strings.Contains(joined, "development build") {
		t.Fatalf("lines = %q, want a development-build notice", joined)
	}
	if strings.Contains(joined, "new version") || strings.Contains(joined, "newer than") || strings.Contains(joined, "already running the latest") {
		t.Fatalf("lines = %q, must not make a newer/older/latest claim for an unresolved dev build", joined)
	}
}

// TestUpdateDialogShowResultRealVersions confirms the ordinary
// newer/older/latest comparison still applies for two real release tags —
// the dev-build special case must not swallow the normal path.
func TestUpdateDialogShowResultRealVersions(t *testing.T) {
	d := &UpdateDialog{}
	d.ShowResult("v1.0.0", githubRelease{TagName: "v1.2.3"}, nil)

	joined := strings.Join(d.lines, "\n")
	if !strings.Contains(joined, "A new version of goSSMS is available.") {
		t.Fatalf("lines = %q, want the new-version-available message", joined)
	}
}

// TestUpdateDialogShowResultPseudoVersion covers the version a build from a
// checkout actually reports since Go 1.24: a pseudo-version, which is a
// pre-release of the next patch. Built after v0.0.10 it is newer than that
// release; against v0.0.11 it is older, and must not read as "latest".
func TestUpdateDialogShowResultPseudoVersion(t *testing.T) {
	const pseudo = "v0.0.11-0.20260911113756-cf929d309586+dirty"
	cases := []struct {
		latest, want string
	}{
		{"v0.0.10", "newer than the latest published release"},
		{"v0.0.11", "A new version of goSSMS is available."},
	}
	for _, c := range cases {
		d := &UpdateDialog{}
		d.ShowResult(pseudo, githubRelease{TagName: c.latest}, nil)
		if joined := strings.Join(d.lines, "\n"); !strings.Contains(joined, c.want) {
			t.Errorf("against %s: lines = %q, want %q", c.latest, joined, c.want)
		}
	}
}

func TestGithubReleaseReleasesURL(t *testing.T) {
	withHTML := githubRelease{TagName: "v1.2.3", HTMLURL: "https://github.com/radix29/gossms/releases/tag/v1.2.3"}
	if got := withHTML.releasesURL(); got != withHTML.HTMLURL {
		t.Errorf("releasesURL() = %q, want the release's own HTMLURL %q", got, withHTML.HTMLURL)
	}

	withoutHTML := githubRelease{TagName: "v1.2.3"}
	if got := withoutHTML.releasesURL(); got != githubReleasesPage {
		t.Errorf("releasesURL() = %q, want fallback %q", got, githubReleasesPage)
	}
}
