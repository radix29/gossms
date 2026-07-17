package tui

import "testing"

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
