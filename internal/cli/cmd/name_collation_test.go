package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// equalFoldAllowed is every function of this package that may call
// strings.EqualFold, with why: docs/db-rules.md's rule is that SQL Server
// names compare under the scope's collation (gosmo.SameName with
// serverCollation or catalogCollation), never case-blind. Each entry is the
// enclosing function (Type.method for a method) and the reason it is not a
// name comparison, or is the rule's exact-first convenience.
var equalFoldAllowed = map[string]string{
	// matchName convenience: exact first, then a unique fold — over catalog
	// spelling, not a comparison.
	"matchName":              "matchName convenience: exact first, unique fold",
	"matchColumns":           "matchName convenience: exact first, unique fold",
	"matchFileColumn":        "matchName convenience: exact first, unique fold",
	"resolveStep":            "matchName convenience: exact first, unique fold",
	"resolveType":            "matchName convenience: exact first, then fold",
	"explicitMoves":          "matchName convenience: exact first, then fold",
	"plannedPaths":           "matchName convenience: exact first, then fold (one database's files, by id)",
	"Deps.maintIndexRebuild": "matchName convenience: exact first, then fold",
	"Deps.maintUpdateStats":  "matchName convenience: exact first, then fold",

	// Names case-insensitive by definition.
	"aliasOf":             "configuration name, case-insensitive by definition",
	"resolveConfig":       "configuration name, case-insensitive by definition",
	"plannedSet":          "configuration name, case-insensitive by definition",
	"resolveScopedConfig": "configuration name, case-insensitive by definition",
	"Deps.dbCreate":       "collation name, case-insensitive by definition",

	// Not names: values and status text.
	"Deps.dbList":            "database state value (--state), not a name",
	"Deps.dbScopedConfigSet": "a scoped configuration's value, not a name",
	"checkAgentRunning":      "Agent status text, not a name",

	// User-facing list filters, case-insensitive by design.
	"listFilter.matches":           "object list filter is case-insensitive by design",
	"sessionFilter.keep":           "session list filter is case-insensitive by design",
	"Deps.maintIndexFragmentation": "--schema list filter is case-insensitive by design, as object list's",
	"Deps.jobList":                 "job category list filter is case-insensitive by design",

	// Refusing a duplicate in any case only refuses more.
	"readParams":   "--param given twice in any case is refused: refusing more is safe",
	"parseRestore": "--with-move naming a file twice in any case is refused: refusing more is safe",
}

// TestCLIComparesNamesUnderCollation fails on any strings.EqualFold call in
// this package's non-test files outside equalFoldAllowed, and on an entry
// that no longer calls it. The rule was broken here three reviews running
// (2026-11-23 B4, 2026-11-27 B3, 2026-11-27 second pass B3).
func TestCLIComparesNamesUnderCollation(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	fset := token.NewFileSet()
	used := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			key := funcKey(fn)
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "EqualFold" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
					return true
				}
				if _, ok := equalFoldAllowed[key]; ok {
					used[key] = true
					return true
				}
				t.Errorf("%s: %s calls strings.EqualFold: compare a SQL Server name with gosmo.SameName under "+
					"serverCollation or catalogCollation (docs/db-rules.md), or add %s to equalFoldAllowed with why it is not one",
					fset.Position(call.Pos()), key, key)
				return true
			})
		}
	}
	for key := range equalFoldAllowed {
		if !used[key] {
			t.Errorf("equalFoldAllowed: %s no longer calls strings.EqualFold: remove the entry", key)
		}
	}
}

// funcKey is fn's name, Type.name for a method.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
