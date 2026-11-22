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
// The word boundary leaves a backup header's SoftwareVersionMajor alone
// (maint_restore.go): that is the version a backup was taken on, not the
// server's. Arithmetic (VersionMajor*10, compatCeiling) and display reads are
// not comparisons and pass.
var handVersionComparison = regexp.MustCompile(
	`\bVersion(Major|Build)\b\s*(<|<=|>|>=|==|!=)` +
		`|(<|<=|>|>=|==|!=)\s*[\w.()]*\bVersion(Major|Build)\b`)

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
	} {
		if handVersionComparison.MatchString(line) {
			t.Errorf("matches a line that is not a server-version comparison: %s", line)
		}
	}
}
