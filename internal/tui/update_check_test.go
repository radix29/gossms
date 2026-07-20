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
		{"v1.0.0-rc1", "v1.0.0", 0}, // pre-release suffix is ignored
		{"(devel)", "v1.0.0", -1},   // unparseable tag compares as v0.0.0
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

// TestUpdateDialogShowResultDevBuild is a regression test: version.Version's
// own "(devel)" placeholder (see its doc comment) used to parse as v0.0.0,
// so every plain `git clone && go build` user was told "A new version of
// goSSMS is available" — never true "you're already on the latest" and
// never the accurate "this isn't a comparable build" message.
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
