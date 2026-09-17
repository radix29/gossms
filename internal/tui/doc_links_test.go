package tui

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// so an entry cannot outlive the file it excuses.
	exemptSource := map[string]string{
		"CHANGELOG.md":                   "history; the paths it names were real when the entry was written",
		"docs/review-plan-2026-09-17.md": "D1's table names the dangling paths as its subject matter",
	}
	for rel := range exemptSource {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s is exempt but no longer exists; drop the entry", rel)
		}
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
