package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radix29/gossms/internal/version"
)

// githubReleasesAPI is the GitHub REST endpoint for gossms's latest
// published (non-draft, non-prerelease) release.
const githubReleasesAPI = "https://api.github.com/repos/radix29/gossms/releases/latest"

// githubReleasesPage is shown as the fallback link when a release response
// doesn't carry its own html_url.
const githubReleasesPage = "https://github.com/radix29/gossms/releases/latest"

// githubRelease holds the fields of GitHub's release API response gossms
// actually uses.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// releasesURL returns the page to point the user at, falling back to the
// generic releases page if GitHub didn't return one.
func (r githubRelease) releasesURL() string {
	if r.HTMLURL != "" {
		return r.HTMLURL
	}
	return githubReleasesPage
}

// checkForUpdates opens UpdateDialog in its loading state, then fetches the
// latest release from GitHub on a background goroutine — same
// postEvent+wakeEventLoop handoff as connectServer (see app_connections.go).
func (a *App) checkForUpdates() {
	a.updateDialog.ShowChecking(version.Version)

	go func() {
		rel, err := fetchLatestRelease()
		a.postEvent(func() {
			a.updateDialog.ShowResult(version.Version, rel, err)
		})
		a.wakeEventLoop()
	}()
}

// fetchLatestRelease calls the GitHub API for gossms's latest release.
// GitHub requires a User-Agent header on API requests or it returns 403.
func fetchLatestRelease() (githubRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesAPI, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gossms-update-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("github returned %s", resp.Status)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return githubRelease{}, err
	}
	return rel, nil
}

// compareVersions compares two "vMAJOR.MINOR.PATCH"-style tags numerically,
// returning -1/0/1 for a<b, a==b, a>b. Segments that aren't parseable as
// integers (including a whole non-tag string like "(devel)") count as 0.
func compareVersions(a, b string) int {
	pa, pb := parseVersionParts(a), parseVersionParts(b)
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

// parseVersionParts splits a "vMAJOR.MINOR.PATCH[-pre][+build]" tag into its
// three numeric components, dropping the "v" prefix and any pre-release or
// build metadata suffix first.
func parseVersionParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts [3]int
	for i, seg := range strings.SplitN(v, ".", 3) {
		if n, err := strconv.Atoi(seg); err == nil {
			parts[i] = n
		}
	}
	return parts
}

// isReleaseVersion reports whether v looks like a parseable
// "vMAJOR.MINOR.PATCH"-style release tag, as opposed to version.Version's
// own "(devel)" placeholder for a plain `git clone && go build`/`go run`
// with no ldflags (see internal/version's doc comment for exactly when
// "(devel)" applies). Used to avoid comparing an unresolved dev build
// against a real release and reporting a misleading "new version
// available"/"newer than latest" claim — every numeric segment parses to 0
// for "(devel)", which compareVersions would otherwise read as v0.0.0.
func isReleaseVersion(v string) bool {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return false
	}
	for _, seg := range strings.SplitN(v, ".", 3) {
		if _, err := strconv.Atoi(seg); err != nil {
			return false
		}
	}
	return true
}
