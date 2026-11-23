package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// handVersionComparison matches the server's VersionMajor or VersionBuild
// compared by hand, on either side of the operator. gosmo's
// ServerInfo.AtLeast/AtLeastBuild is the one comparison: it counts an unread
// version and an Azure edition (VersionMajor 12 on a Managed Instance) as
// newest, and the hand-written forms it replaced each spelled that a little
// differently — one forgot the unread case, so an unread version hid a
// feature. gosmo enforces the same rule on itself
// (TestVersionGatesGoThroughMajorAtLeast).
//
// It also matches the derived form — a major from serverMajor (or any name
// ending in major) compared with int(gosmo.SQLServerNNNN) or a version
// literal, or tested for the unread 0 — which is how B3 of the 2026-11-23
// review hid Resource Governor's tempdb rows on Managed Instance: nine copies
// of `major == 0 || major >= int(gosmo.X)`, one without the `== 0`.
//
// The word boundary leaves a backup header's SoftwareVersionMajor alone
// (maint_restore.go): that is the version a backup was taken on, not the
// server's, and `> 0` there is a presence test. Arithmetic (VersionMajor*10,
// compatCeiling) and display reads are not comparisons and pass.
var handVersionComparison = regexp.MustCompile(
	`\bVersion(Major|Build)\b\s*(<|<=|>|>=|==|!=)` +
		`|(<|<=|>|>=|==|!=)\s*[\w.()]*\bVersion(Major|Build)\b` +
		`|(<|<=|>|>=)\s*int\(gosmo\.SQLServer\d+\)` +
		`|int\(gosmo\.SQLServer\d+\)\s*(<|<=|>|>=)` +
		`|(?i:major)(\([\w.]*\))?\s*(<|<=|>|>=)\s*[1-9]` +
		`|(?i:major)(\([\w.]*\))?\s*(==|!=)\s*0\b`)

// versionComparisonAllowed are hand comparisons kept on purpose, by file and
// matched text, each with its reason.
var versionComparisonAllowed = map[string]string{
	// MAX_DURATION is offered only on a *known* on-prem SQL Server 2025: an
	// unread version guessing "newest" would offer an option the server
	// rejects, and Azure's support is unconfirmed (open-threads V3). The
	// positive-known form AtLeast deliberately does not provide.
	"internal/tui/xevent_session_advanced.go\x00major >= 1": "known on-prem 2025 only (V3)",
}

func TestVersionGatesGoThroughAtLeast(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "todo", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if m := handVersionComparison.FindString(line); m != "" {
				if _, ok := versionComparisonAllowed[filepath.ToSlash(rel)+"\x00"+m]; ok {
					continue
				}
				t.Errorf("%s:%d compares the server version by hand (%q) — use ServerInfo.AtLeast/AtLeastBuild",
					rel, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The guard is only as good as its pattern: every spelling it replaced must
// match, and the reads it must leave alone must not.
func TestHandVersionComparisonPattern(t *testing.T) {
	for _, line := range []string{
		`return info == nil || info.IsAzure() || info.VersionMajor >= 15`,
		`info.VersionMajor != 0 && info.VersionMajor < 16`,
		`orAlter = info.VersionMajor > 13 || info.VersionMajor == 13 && info.VersionBuild >= 4001`,
		`if 16 <= info.VersionMajor {`,
		`return sc.Server.Info().VersionMajor >= int(gosmo.SQLServer2025)`,
		`hasCustomPolicy := major == 0 || major >= int(gosmo.SQLServer2019)`,
		`serverMajor(sc) >= int(gosmo.SQLServer2025)`,
		`if int(gosmo.SQLServer2017) <= pf.major {`,
		`if major := serverMajor(l.sc); major != 0 && major < 14 {`,
		`if major >= 17 && !azure {`,
	} {
		if !handVersionComparison.MatchString(line) {
			t.Errorf("does not match a hand comparison: %s", line)
		}
	}
	for _, line := range []string{
		`if h.SoftwareVersionMajor > 0 {`,
		`{Key: "majorVersion", Value: strconv.Itoa(info.VersionMajor)},`,
		`return info.VersionMajor * 10`,
		`return info.AtLeast(gosmo.SQLServer2019)`,
		`compatItems := compatItemsFor(int(d.CompatibilityLevel), serverMajor(sc))`,
		`if serverAtLeast(l.sc, gosmo.SQLServer2017) {`,
	} {
		if handVersionComparison.MatchString(line) {
			t.Errorf("matches a line that is not a server-version comparison: %s", line)
		}
	}
}
