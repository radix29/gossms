package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
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

// TestNoPropertySheetPageTitleIsTruncated is LabelWidth's ratchet for the page
// list. A title wider than propsheet.PageTitleWidth is clipped the same way,
// with no ellipsis: "External Resource Pools" rendered as "External Resource
// Pool" (B9, 2026-10-01).
//
// Titles reach the sheet two ways, both checked: a propPage's title field, and
// a create dialog's pages slice of string literals.
func TestNoPropertySheetPageTitleIsTruncated(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	check := func(e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		title, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		checked++
		if w := core.DisplayWidth(title); w > propsheet.PageTitleWidth {
			t.Errorf("%s: page title %q is %d columns, and the page list cuts it to %d — it will render as %q",
				fset.Position(lit.Pos()), title, w, propsheet.PageTitleWidth,
				core.PadRight(title, propsheet.PageTitleWidth))
		}
	}
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
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return true
			}
			switch key.Name {
			case "title":
				// Only a page's title: a dialog's own title is drawn in its
				// frame, not the page list.
				if isPageTitleField(file, kv) {
					check(kv.Value)
				}
			case "pages":
				if cl, ok := kv.Value.(*ast.CompositeLit); ok {
					for _, e := range cl.Elts {
						check(e)
					}
				}
			}
			return true
		})
	}
	if checked < 100 {
		t.Fatalf("only %d page titles were checked; the propPage/pages shapes have probably changed", checked)
	}
}

// pageTitleTypes are the struct types whose title field is a page title:
// propPage itself, and the specs whose builder copies title into one.
var pageTitleTypes = map[string]bool{
	"propPage":      true,
	"rgIntPageSpec": true, // rgIntPage: title: spec.title
}

// isPageTitleField reports whether kv is an element of a pageTitleTypes
// composite literal, written as T{…}, T[X]{…}, or elided inside []T{{…}}.
func isPageTitleField(file *ast.File, kv *ast.KeyValueExpr) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if pageTitleTypes[litTypeName(cl.Type)] && slices.Contains(cl.Elts, ast.Expr(kv)) {
			found = true
			return false
		}
		if at, ok := cl.Type.(*ast.ArrayType); ok && pageTitleTypes[litTypeName(at.Elt)] {
			for _, e := range cl.Elts {
				if inner, ok := e.(*ast.CompositeLit); ok && inner.Type == nil && slices.Contains(inner.Elts, ast.Expr(kv)) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// litTypeName is a composite literal type's bare name, generic arguments
// dropped: rgIntPageSpec[O] is "rgIntPageSpec".
func litTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return litTypeName(t.X)
	case *ast.IndexListExpr:
		return litTypeName(t.X)
	}
	return ""
}
