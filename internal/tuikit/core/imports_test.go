package core

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// allowedExternal is everything outside the standard library and tuikit
// itself that a tuikit package may import. It is the whole of the library's
// dependency surface: a new entry here is a new requirement for every
// module that uses tuikit.
var allowedExternal = []string{
	"github.com/clipperhouse/displaywidth",
	"github.com/gdamore/tcell/v3",
	"github.com/gdamore/tcell/v3/color",
}

// TestTuikitImportsOnlyItsDeclaredDependencies pins tuikit's dependency
// invariant, so the packages can become a module of their own: a tuikit
// package imports the standard library, other tuikit packages, tcell and
// displaywidth, and nothing else — in particular nothing of the host
// application (gosmo, or any other package of the module that contains
// tuikit). Test files may use further third-party packages (tcell/v3/vt, for
// one), but they too must not reach into the host application: a test that
// does would not survive the move.
//
// It lives in core and lists ../... so that it runs from whichever module
// tuikit lives in.
func TestTuikitImportsOnlyItsDeclaredDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not on PATH")
	}
	// The test's working directory is the core package, so ../... is tuikit.
	out, err := exec.Command("go", "list", "-f",
		`{{.ImportPath}}|{{join .Imports " "}}|{{join .TestImports " "}} {{join .XTestImports " "}}`,
		"../...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	type pkg struct {
		path         string
		imports, tst []string
	}
	var pkgs []pkg
	for line := range strings.Lines(string(out)) {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 3 {
			continue
		}
		pkgs = append(pkgs, pkg{parts[0], strings.Fields(parts[1]), strings.Fields(parts[2])})
	}
	// tuikit's import path is core's, minus "/core".
	var root string
	for _, p := range pkgs {
		if before, ok := strings.CutSuffix(p.path, "/core"); ok {
			root = before
		}
	}
	if root == "" || len(pkgs) < 2 {
		t.Fatalf("go list did not find tuikit's packages:\n%s", out)
	}

	isStd := func(imp string) bool {
		first, _, _ := strings.Cut(imp, "/")
		return !strings.Contains(first, ".")
	}
	inTuikit := func(imp string) bool { return imp == root || strings.HasPrefix(imp, root+"/") }
	// hostModule is the module that contains tuikit, while it still lives
	// inside one (github.com/radix29/gossms/internal/tuikit → …/gossms).
	hostModule, _, _ := strings.Cut(root, "/internal/")

	for _, p := range pkgs {
		for _, imp := range p.imports {
			if isStd(imp) || inTuikit(imp) || slices.Contains(allowedExternal, imp) {
				continue
			}
			t.Errorf("%s imports %s; tuikit may import only the standard library, tuikit and %s",
				p.path, imp, strings.Join(allowedExternal, ", "))
		}
		for _, imp := range p.tst {
			if inTuikit(imp) || isStd(imp) {
				continue
			}
			if (hostModule != root && strings.HasPrefix(imp, hostModule+"/")) || strings.Contains(imp, "gosmo") {
				t.Errorf("a test of %s imports %s, which is outside tuikit", p.path, imp)
			}
		}
	}
}
