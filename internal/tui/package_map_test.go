package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// D3: ARCHITECTURE.md § Package map opens "internal/tui is a flat package, so
// every file is listed individually with its purpose", and the section's value
// rests entirely on that being true — it is what lets the same section promise
// that "a file absent from a summarized directory has not been omitted". Ten
// files had accumulated below the map by the 2026-09-17 review (the Service
// Broker family, its Detail Browser view and latest.go), which makes every
// absence meaningless rather than significant.
//
// Both directions are checked. A file with no row is the drift this exists to
// stop; a row with no file is the other half, and is how a renamed file leaves
// two wrong answers behind instead of one.

// tuiMapRowRe matches one internal/tui row of the map's tree: seven spaces of
// indent is the depth internal/tui's own files sit at, so a tuikit or
// sub-package row cannot match. Rows for the sub-directories end in "/" and
// are skipped by the .go suffix test.
var tuiMapRowRe = regexp.MustCompile(`(?m)^│ {7}[├└]── ([A-Za-z0-9_./-]+)`)

func TestPackageMapListsEveryTUIFile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "ARCHITECTURE.md")
	src, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}

	mapped := map[string]bool{}
	for _, m := range tuiMapRowRe.FindAllStringSubmatch(string(src), -1) {
		if strings.HasSuffix(m[1], ".go") {
			mapped[m[1]] = true
		}
	}
	if len(mapped) == 0 {
		t.Fatal("no internal/tui rows found in ARCHITECTURE.md's package map; the row pattern no " +
			"longer matches the document and this test proves nothing")
	}

	onDisk := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, "internal", "tui"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		onDisk[name] = true
		if !mapped[name] {
			t.Errorf("internal/tui/%s is missing from ARCHITECTURE.md § Package map, which claims to "+
				"list every file — add a row naming what it is", name)
		}
	}

	for name := range mapped {
		if !onDisk[name] {
			t.Errorf("ARCHITECTURE.md § Package map lists internal/tui/%s, which does not exist — "+
				"drop the row, or point it at the file that replaced it", name)
		}
	}
}
