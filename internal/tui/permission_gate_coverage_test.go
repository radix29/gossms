package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ungatedWritablePages are the writable pages that deliberately declare no
// requiredRight, each with the reason it cannot. A page listed here has been
// looked at; a page missing from here and from every withRequires call has
// not, which is what the test below is for.
var ungatedWritablePages = map[string]string{
	// Its writes create and drop database users, so what permits them is
	// ALTER ANY USER in each mapped database — a different answer per row of
	// the grid, which one page-level banner cannot state. See loginPropPages.
	"pageLoginUserMapping": "per-row database rights; one page-level banner would be wrong",
}

// TestEveryWritablePropertyPageIsGated.
//
// A propPage that returns a real apply writes to the server, and the only
// thing that stops it being offered to a login that cannot perform the write
// is a withRequires call at the site that mounts it. Forgetting one is silent
// by construction: the page loads, the fields edit, OK runs the ALTER, and the
// server refuses it — the failure permission_gate.go exists to turn into a
// read-only page with a banner *before* the user types anything.
//
// Read out of the source, for the reason TestEveryGatedRightIsOneGosmoActually-
// Probes is: Go cannot enumerate the pages at run time, and a hand-written list
// here would go stale on the first page added, which is the case this test
// exists for.
//
// Three shapes are not failures:
//   - a page returning nil or a `func(context.Context) error { return nil }`
//     apply is read-only (Effective Permissions is the example);
//   - a page built by another page builder — either by delegating its load
//     (pageLoginSecurables wraps pagePrincipalServerPermissions) or by being
//     returned outright (pageRoleGeneral returns roleGeneralPage) — is not
//     mounted itself. The wrapper inherits its writability and is checked in
//     its place, which is where the withRequires call belongs;
//   - a page in ungatedWritablePages, which names why.
func TestEveryWritablePropertyPageIsGated(t *testing.T) {
	writable, wrapped, gated := scanPropPages(t)

	if len(writable) < 40 {
		t.Fatalf("found only %d writable property pages; the parser has stopped seeing them", len(writable))
	}
	if len(gated) < 40 {
		t.Fatalf("found only %d withRequires call sites; the parser has stopped seeing them", len(gated))
	}

	for name, file := range writable {
		if gated[name] || wrapped[name] {
			continue
		}
		if why, ok := ungatedWritablePages[name]; ok {
			t.Logf("%s is deliberately ungated: %s", name, why)
			continue
		}
		t.Errorf("%s (%s) returns an apply but is never passed to withRequires — "+
			"it is offered, and editable, to a login that cannot perform its writes",
			name, file)
	}

	// The other direction: an entry here that no longer names an ungated
	// writable page is a stale exemption, and would hide the next one.
	for name := range ungatedWritablePages {
		if _, ok := writable[name]; !ok {
			t.Errorf("ungatedWritablePages names %s, which is no longer a writable page", name)
		} else if gated[name] {
			t.Errorf("ungatedWritablePages names %s, which is now gated — drop the exemption", name)
		}
	}
}

// scanPropPages parses internal/tui and returns, by page-builder name: the
// pages that return a real apply (to the file declaring them), the pages built
// by another page builder rather than mounted directly, and the pages passed to
// withRequires.
func scanPropPages(t *testing.T) (writable map[string]string, wrapped, gated map[string]bool) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	writable, wrapped, gated = map[string]string{}, map[string]bool{}, map[string]bool{}
	// wrapper -> the page builder it is built from, resolved into writable
	// below once every file has been parsed.
	wraps := map[string]string{}
	declaredIn := map[string]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || !returnsPropPage(fd) {
				continue
			}
			declaredIn[fd.Name.Name] = path
			w, from := pageApplyShape(fd.Body)
			if w {
				writable[fd.Name.Name] = path
			}
			if from != "" {
				wraps[fd.Name.Name] = from
				wrapped[from] = true
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || (fn.Name != "withRequires" && fn.Name != "withRequiresOn") {
				return true
			}
			// The page is built inline as the first argument:
			// withRequires(pageLoginGeneral(sc, namePtr), "", right...).
			if inner, ok := call.Args[0].(*ast.CallExpr); ok {
				if id, ok := inner.Fun.(*ast.Ident); ok {
					gated[id.Name] = true
				}
			}
			return true
		})
	}
	// A wrapper is as writable as what it wraps, however many links deep:
	// pageRoleGeneral is the page that gets mounted, so it is the one that
	// has to carry the gate. Iterated to a fixed point rather than once,
	// since a wrapper may be parsed before what it wraps.
	for changed := true; changed; {
		changed = false
		for outer, inner := range wraps {
			if _, ok := writable[outer]; ok {
				continue
			}
			if _, ok := writable[inner]; ok {
				writable[outer] = declaredIn[outer]
				changed = true
			}
		}
	}
	return writable, wrapped, gated
}

func returnsPropPage(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
		return false
	}
	id, ok := fd.Type.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "propPage"
}

// pageApplyShape reports whether a page builder's body returns a real apply,
// and the name of the page builder it is built from, if any — either the page
// whose load it delegates to, or the page it returns outright.
func pageApplyShape(body *ast.BlockStmt) (writable bool, builtFrom string) {
	// The construction shape is read off the body's own statements, not
	// through Inspect: `return findRole(ctx, ...)` inside a closure is also a
	// one-result call return, and taking that one would name a lookup
	// function as the page this page is built from.
	if len(body.List) == 1 {
		if ret, ok := body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			if call, ok := ret.Results[0].(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok {
					builtFrom = id.Name
				}
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		switch len(ret.Results) {
		case 1:
			call, ok := ret.Results[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			// return pageX(...).load(ctx), inside the wrapper's own load.
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "load" {
				return true
			}
			if inner, ok := sel.X.(*ast.CallExpr); ok {
				if id, ok := inner.Fun.(*ast.Ident); ok {
					builtFrom = id.Name
				}
			}
		case 3:
			if id, ok := ret.Results[1].(*ast.Ident); ok && id.Name == "nil" {
				return true
			}
			if lit, ok := ret.Results[1].(*ast.FuncLit); ok && isNoOpApply(lit) {
				return true
			}
			writable = true
		}
		return true
	})
	return writable, builtFrom
}

// isNoOpApply reports whether an inline apply is the read-only page's
// `func(context.Context) error { return nil }`.
func isNoOpApply(lit *ast.FuncLit) bool {
	if len(lit.Body.List) != 1 {
		return false
	}
	ret, ok := lit.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	return ok && id.Name == "nil"
}
