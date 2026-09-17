package tui

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// D1: a plan document is deleted once it is implemented — the convention — and
// what survives it is the pointers. Five deleted plans were still cited from
// .md files and from Go comments at the 2026-09-17 review, each sending a
// reader to a path that has not existed for weeks. This walks every .md file
// and every Go comment for a repo-relative `*.md` path and fails on one that
// does not resolve, so the next plan deletion is caught at the deletion rather
// than at the next review.
//
// exemptSource carries the few files whose dead paths are their subject.
var (
	docPathRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_./-]*\.md`)
	urlRe     = regexp.MustCompile(`https?://\S+`)
)

// suffixMatch reports whether any tracked file's path ends in ref — the form a
// comment uses when it names a document from its package root, such as
// "tuikit/README.md" for internal/tuikit/README.md.
func suffixMatch(root, ref string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil && strings.HasSuffix(rel, "/"+ref) {
			found = true
		}
		return nil
	})
	return found
}

// staleExemptions reports every exemption that has stopped earning its place,
// as the message to print for it.
//
// Two ways it goes stale. The first is the file being deleted out from under
// the entry. The second is subtler and is how the 2026-09-11 plan's entry got
// here: a plan is deleted once its items are done, a new dated plan takes its
// place, and the exemption keyed to the old filename survives both — it named
// a path that no longer existed, and go test ./... was red from that alone. So
// an exempt review-plan under docs/ must also be the newest such plan:
// retiring a plan without retiring its entry fails at the retirement, which is
// where the edit is.
func staleExemptions(root string, exempt map[string]string) []string {
	var msgs []string
	plans, _ := filepath.Glob(filepath.Join(root, "docs", "review-plan-*.md"))
	newest := ""
	for _, p := range plans {
		// The names are review-plan-YYYY-MM-DD.md, so lexical order is date
		// order.
		if base := filepath.Base(p); base > newest {
			newest = base
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(exempt)) {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			msgs = append(msgs, fmt.Sprintf("%s is exempt but no longer exists; drop the entry", rel))
			continue
		}
		if dir, base := filepath.Split(rel); filepath.Clean(dir) == "docs" &&
			strings.HasPrefix(base, "review-plan-") && base != newest {
			msgs = append(msgs, fmt.Sprintf("%s is exempt but docs/%s is newer; a retired plan takes its "+
				"exemption with it — drop the entry and delete the plan", rel, newest))
		}
	}
	return msgs
}

func TestStaleReviewPlanExemptionIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review-plan-2026-01-01.md", "review-plan-2026-02-02.md"} {
		if err := os.WriteFile(filepath.Join(root, "docs", name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		exempt map[string]string
		want   string
	}{
		{"newest plan", map[string]string{"docs/review-plan-2026-02-02.md": "r"}, ""},
		{"retired plan", map[string]string{"docs/review-plan-2026-01-01.md": "r"}, "is newer"},
		{"deleted plan", map[string]string{"docs/review-plan-2025-12-31.md": "r"}, "no longer exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := staleExemptions(root, tc.exempt)
			if tc.want == "" {
				if len(msgs) != 0 {
					t.Fatalf("want no report, got %q", msgs)
				}
				return
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0], tc.want) {
				t.Fatalf("want one report containing %q, got %q", tc.want, msgs)
			}
		})
	}
}

func TestNoDanglingDocReference(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Reported once per (source, target) pair; the same dead path is cited
	// from several files and each citation is its own edit.
	report := func(rel, ref string, line int) {
		t.Errorf("%s:%d: references %s, which does not exist — replace the pointer with the "+
			"fact it was pointing at, in the document that now owns it", rel, line, ref)
	}
	check := func(rel, ref string, line int) {
		// Another repo, or todo/ — scratch, which CLAUDE.md keeps out of
		// cleanups and which never held a document anyone can restore.
		if strings.Contains(ref, "gosmo/") || strings.HasPrefix(ref, "todo/") {
			return
		}
		if _, err := os.Stat(filepath.Join(root, ref)); err == nil {
			return
		}
		// Also allow a path written relative to the citing file's directory,
		// and one written from a package root ("tuikit/README.md").
		if _, err := os.Stat(filepath.Join(root, filepath.Dir(rel), ref)); err == nil {
			return
		}
		if suffixMatch(root, ref) {
			return
		}
		report(rel, ref, line)
	}

	scanText := func(rel, text string) {
		for i, line := range strings.Split(text, "\n") {
			// A URL's own path is the remote repo's business, not this tree's.
			line = urlRe.ReplaceAllString(line, " ")
			for _, ref := range docPathRe.FindAllString(line, -1) {
				check(rel, ref, i+1)
			}
		}
	}

	// Sources whose dead paths are the point. Each is checked to still exist,
	// so an entry cannot outlive the file it excuses. Add the entry with the
	// document it excuses, never ahead of it: an exemption committed before
	// its plan lands fails this test on main until the plan is written.
	exemptSource := map[string]string{
		"CHANGELOG.md": "history; the paths it names were real when the entry was written",
	}
	for _, msg := range staleExemptions(root, exemptSource) {
		t.Error(msg)
	}

	fset := token.NewFileSet()
	checkedMD, checkedGo := 0, 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// todo/ is scratch, not source.
			if name := d.Name(); name == ".git" || name == "todo" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(path, ".md"):
			if _, ok := exemptSource[rel]; ok {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checkedMD++
			scanText(rel, string(src))
		case strings.HasSuffix(path, ".go"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
			if err != nil {
				return err
			}
			checkedGo++
			for _, group := range f.Comments {
				for _, c := range group.List {
					line := fset.Position(c.Pos()).Line
					text := urlRe.ReplaceAllString(c.Text, " ")
					for _, ref := range docPathRe.FindAllString(text, -1) {
						// In Go comments only a path counts: a bare
						// "readme.md" in a file-dialog test is a fixture
						// name, not a citation.
						if strings.Contains(ref, "/") {
							check(rel, ref, line)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkedMD == 0 || checkedGo == 0 {
		t.Fatalf("walked %d .md and %d .go files; the root is wrong and this test proves nothing",
			checkedMD, checkedGo)
	}
}
