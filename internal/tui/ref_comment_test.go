package tui

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoCommentNamesARenamedGosmoHandle.
//
// The 2026-09-17 gosmo rename gave every lookup-free handle a Ref suffix, and
// the rename updated the call sites, both CLAUDE.md files, gosmo's
// ARCHITECTURE.md and its diagrams — but not the prose in this package, which
// went on naming Server.Database for another day. Nothing catches that: a
// comment compiles whatever it says, and these comments sit at exactly the
// call sites the suffix was introduced to make legible, so a stale one is
// worse than none. The pattern is the one doc_links_test.go and
// gate/names_test.go use — read this package's own source rather than trust a
// hand-kept list.
//
// Deliberately narrow: it pins the handle names that were renamed, not every
// identifier a comment might name. Widening it to "every gosmo method a
// comment mentions still exists" would need the whole gosmo API surface and
// would fail on prose that names a method in passing.
func TestNoCommentNamesARenamedGosmoHandle(t *testing.T) {
	// Each pattern is a spelling that stopped existing in the rename, with
	// what to write instead. The contrast forms ("X, not Y") are listed
	// because that is how these comments are all phrased.
	bad := []struct {
		re   *regexp.Regexp
		want string
	}{
		{regexp.MustCompile(`\bServer\.Database\b(?:[^A-Za-z]|$)`), "Server.DatabaseRef or Server.DatabaseByName"},
		{regexp.MustCompile(`\bDatabase, not DatabaseByName\b`), "DatabaseRef, not DatabaseByName"},
		{regexp.MustCompile(`\bDatabaseByName, not Database\b(?:[^A-Za-z]|$)`), "DatabaseByName, not DatabaseRef"},
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found only %d .go files; the glob has stopped seeing the package", len(files))
	}
	checked := 0
	for _, name := range files {
		// This file's own prose quotes the removed spellings to explain
		// itself; every other file in the package is fair game.
		if name == "ref_comment_test.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				checked++
				text := c.Text
				for _, b := range bad {
					if loc := b.re.FindString(text); loc != "" {
						t.Errorf("%s: comment names %q, which the Ref rename removed — write %s instead\n\t%s",
							fset.Position(c.Pos()), strings.TrimSpace(loc), b.want, text)
					}
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("read only %d comments; the parser has stopped seeing them", checked)
	}
}
