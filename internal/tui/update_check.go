package tui

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
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

// errUpdateCheckPanicked is what the update dialog reports when the check
// goroutine panicked — the panic itself is already logged and on the status
// bar, so this only has to get the dialog out of its checking state.
var errUpdateCheckPanicked = errors.New("the update check stopped unexpectedly — see the log for details")

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
// latest release from GitHub on a background goroutine — same postAndWake
// handoff as connectServer (see app_connections.go).
func (a *App) checkForUpdates() {
	a.updateDialog.ShowChecking(version.Version)

	// safegoRepair: ShowChecking latches the dialog into its loading state,
	// which only ShowResult leaves — a panic strands it there.
	a.safegoRepair("the update check", func() {
		a.updateDialog.ShowResult(version.Version, githubRelease{}, errUpdateCheckPanicked)
	}, func() {
		rel, err := fetchLatestRelease()
		a.postAndWake(func() {
			a.updateDialog.ShowResult(version.Version, rel, err)
		})
	})
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
	// The 10s context bounds how long the response may take, not how big it
	// may be; cap the bytes decoded so a broken or hostile response can't
	// grow the heap. GitHub's real payload is a few KB.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return githubRelease{}, err
	}
	return rel, nil
}

// compareVersions compares two "vMAJOR.MINOR.PATCH[-pre][+build]" tags by
// semver 2.0.0 precedence, returning -1/0/1 for a<b, a==b, a>b. A
// pre-release ranks below its release, which matters because the pseudo-
// version Go 1.24+ stamps into a build from a checkout is one:
// "v0.0.11-0.20260911113756-cf929d309586" is a commit after v0.0.10 and
// before v0.0.11, and must not compare equal to v0.0.11 and report "up to
// date". Build metadata ("+dirty") is ignored. Segments that aren't
// parseable as integers (including a whole non-tag string like "(devel)")
// count as 0.
func compareVersions(a, b string) int {
	ca, preA := parseVersion(a)
	cb, preB := parseVersion(b)
	if c := slices.Compare(ca[:], cb[:]); c != 0 {
		return c
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	return comparePrerelease(preA, preB)
}

// comparePrerelease orders two non-empty pre-release strings by semver
// 2.0.0 §11: dot-separated identifiers compared left to right, numeric ones
// numerically and below any alphanumeric one, alphanumeric ones in ASCII
// order, and a shorter run of equal identifiers ranking lower.
func comparePrerelease(a, b string) int {
	ia, ib := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(ia), len(ib)) {
		na, errA := strconv.ParseUint(ia[i], 10, 64)
		nb, errB := strconv.ParseUint(ib[i], 10, 64)
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil:
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(ia[i], ib[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(ia), len(ib))
}

// parseVersion splits a "vMAJOR.MINOR.PATCH[-pre][+build]" tag into its
// three numeric components and its pre-release string, dropping the "v"
// prefix and any build metadata.
func parseVersion(v string) (core [3]int, pre string) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	for i, seg := range strings.SplitN(v, ".", 3) {
		if n, err := strconv.Atoi(seg); err == nil {
			core[i] = n
		}
	}
	return core, pre
}

// isReleaseVersion reports whether v looks like a parseable
// "vMAJOR.MINOR.PATCH"-style tag — a release, a pre-release, or the
// pseudo-version a build from a checkout carries — as opposed to
// version.Version's own "(devel)" placeholder, which is left only when the
// binary carries no module version at all (`go run`, or a build with
// -buildvcs=false; see internal/version's doc comment). Used to avoid
// comparing an unresolved dev build against a real release and reporting a
// misleading "new version available"/"newer than latest" claim — every
// numeric segment parses to 0 for "(devel)", which compareVersions would
// otherwise read as v0.0.0. A pseudo-version needs no such guard:
// compareVersions ranks it correctly against a release.
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
