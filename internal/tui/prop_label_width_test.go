package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// labelArg names every constructor whose label is drawn into propsheet's
// fixed LabelWidth column, and which argument carries it.
//
// Text, Password, Int and Select pad theirs with core.PadRight; Static clips
// its own with DrawTextClipped at the same width. Both hard-clip, without an
// ellipsis. Check, Radio, Section, Note and Hint are deliberately absent —
// their text is drawn on its own line at full width and is not constrained.
var labelArg = map[string]int{
	"propsheet.Text":     0,
	"propsheet.Password": 0,
	"propsheet.Int":      0,
	"propsheet.Select":   0,
	"propsheet.Static":   0,
	// Application-level wrappers that pass a label straight through.
	"selectPreserving": 0,
	"dbOptSelectRow":   2,
	"dbOptBoolRow":     2,
}

// TestNoPropertySheetLabelIsTruncated is a ratchet, not a style check.
//
// A label wider than LabelWidth is cut off with no ellipsis and no other sign,
// so the page looks finished and says something different from what it means.
// The case that made this worth writing: "Auto update statistics
// asynchronously" on Database Properties > Options rendered as "Auto update
// statistics asynchr", immediately below "Auto update statistics" — two rows
// that set different options, reading as the same one. Eleven labels were over
// the limit when this was added (2026-08-20).
//
// It reads the source rather than the built forms on purpose: most pages need
// a live server to construct, and the labels are string literals, so the
// question can be answered statically for every page at once.
func TestNoPropertySheetLabelIsTruncated(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			i, ok := labelArg[calleeName(call.Fun)]
			if !ok || i >= len(call.Args) {
				return true
			}
			// A label built at run time can't be checked here; those are rare
			// and none of them is near the limit today.
			lit, ok := call.Args[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			label, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			if w := core.DisplayWidth(label); w > propsheet.LabelWidth {
				t.Errorf("%s: label %q is %d columns, and the sheet cuts it to %d — it will render as %q",
					fset.Position(lit.Pos()), label, w, propsheet.LabelWidth,
					core.PadRight(label, propsheet.LabelWidth))
			}
			return true
		})
	}
	// If the constructor names ever change, this test would pass by checking
	// nothing at all.
	if checked < 100 {
		t.Fatalf("only %d labels were checked; labelArg has probably fallen out of date with propsheet", checked)
	}
}

// calleeName renders a call's function as "pkg.Name" or "Name".
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if pkg, ok := f.X.(*ast.Ident); ok {
			return pkg.Name + "." + f.Sel.Name
		}
	}
	return ""
}
