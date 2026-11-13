package cli

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestNoTUIImports pins the command line's import rule (ARCHITECTURE.md § Why
// split this way): nothing under internal/cli may reach internal/tui, any
// tuikit package, or tcell. The CLI runs without a screen; the one lexer it
// shares with the editor is internal/sqltext, outside tuikit. tuikit/core is
// not the leaf it looks like — it imports tcell, which is why output calls
// displaywidth.String directly.
//
// internal/scripting is held to the same rule as a root of its own: it is
// the Script As table the TUI and the script commands share, and moved out of
// internal/tui for exactly that.
func TestNoTUIImports(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not on PATH")
	}
	// The test's working directory is internal/cli, so ./... is
	// internal/cli/... . -deps lists the non-test dependencies only: a test
	// file may import what it likes.
	out, err := exec.Command("go", "list", "-deps", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, "./...", "../scripting").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	imports := map[string][]string{}
	var roots []string
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		imports[fields[0]] = fields[1:]
		if strings.Contains(fields[0], "/internal/cli") || strings.HasSuffix(fields[0], "/internal/scripting") {
			roots = append(roots, fields[0])
		}
	}
	if len(roots) == 0 {
		t.Fatalf("go list found no internal/cli package:\n%s", out)
	}
	// tuikit is its own module (github.com/radix29/tuikit), so "/internal/tui"
	// no longer covers it; every tuikit package also reaches tcell, but naming
	// it reports the first tuikit package in the chain rather than tcell.
	forbidden := func(pkg string) bool {
		return strings.Contains(pkg, "/internal/tui") || strings.HasPrefix(pkg, "github.com/radix29/tuikit") ||
			strings.Contains(pkg, "gdamore/tcell")
	}

	// Breadth first from the cli packages, so each offender is reported with
	// its shortest import chain.
	via := map[string]string{}
	queue := slices.Clone(roots)
	for _, r := range roots {
		via[r] = ""
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if forbidden(pkg) {
			chain := []string{pkg}
			for p := via[pkg]; p != ""; p = via[p] {
				chain = append(chain, p)
			}
			slices.Reverse(chain)
			t.Errorf("%s reaches %s:\n  %s", chain[0], pkg, strings.Join(chain, "\n  -> "))
			continue
		}
		for _, imp := range imports[pkg] {
			if _, seen := via[imp]; !seen {
				via[imp] = pkg
				queue = append(queue, imp)
			}
		}
	}
}
