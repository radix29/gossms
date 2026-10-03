package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pageActionRunners hand their work closure's result to a completion callback
// that runs on the UI goroutine. Their work closures are the one place a
// func(ctx context.Context) error may assign a captured variable: the variable
// is declared beside the call for exactly that handoff, and nothing reads it
// until the completion does.
var pageActionRunners = map[string]bool{
	"runPageAction":     true,
	"runPageActionOnce": true,
}

// applyHelperExemptions are package functions an apply closure may call with
// page state, by name. commitRename mirrors a rename into the dialog's boxed
// name, and only when the rename really ran (not under Script Changes): every
// sibling page and the header resolve the object by that name, so it has to
// move before the reload the Apply ends in reads it back. See prop_apply.go.
var applyHelperExemptions = map[string]bool{
	"commitRename": true,
}

// TestApplyClosuresDoNotWritePageState enforces docs/ui-rules.md's "An apply
// closure never writes page state". An apply closure runs on the pipeline's
// goroutine while the page's own callbacks read the same state on the UI
// goroutine, and it runs under Script Changes as well, where nothing reached
// the server. AG Listener Properties cleared its pending addresses at the end
// of its apply: a data race, and after Script Changes a page that showed
// addresses "To be added" but was no longer dirty, so the next Apply sent
// nothing.
//
// Every func(ctx context.Context) error literal in the package is checked,
// not only the ones a page returns as its propApply — telling those apart
// statically needs the whole load function traced, and the only other such
// closures are page-action work closures, exempted by call site above. So is
// every method of that shape used as a value (d.applyFns[0] = d.configure),
// which runs on the pipeline goroutine just the same.
//
// A write is rarely in the closure itself, so the check follows what the
// closure calls: a local closure it captured (commitCurrent, which wrote the
// editor fields into the grid's model on the pipeline goroutine from 14
// applies), a func-typed field (d.commitInputs()), a method of a captured
// value, and a package function handed captured state — where a write through
// that parameter is a write to page state.
//
// Identifiers resolve through go/types, over this package's own declarations
// only: every import is an empty stub, so the check never waits on
// type-checking tcell or gosmo, and the type errors that leaves are ignored.
// A captured variable, a parameter or a package function is declared here, so
// it resolves all the same.
//
// What it cannot see is a local that aliases page state: a range variable
// over a captured slice (commitApplied(ctx, &e.orig, …) moved a permission
// cell's baseline that way), or r := d.rows. Telling an alias from a copy
// needs each value traced, not just its declaration (and with the imports
// stubbed a gosmo element type is unknown), and the package has several
// applies that edit copies of page state on purpose (cloneXEEvents, a
// decryptor's options).
func TestApplyClosuresDoNotWritePageState(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}
	// Defs and Uses say where each identifier was declared, which is what
	// tells a captured variable from the closure's own.
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: stubImporter{}, Error: func(error) {}}
	conf.Check("github.com/radix29/gossms/internal/tui", fset, files, info)

	pkg := applyIndex{
		info:     info,
		closures: map[types.Object]*ast.FuncLit{},
		fields:   map[string][]*ast.FuncLit{},
		funcs:    map[string]*ast.FuncDecl{},
		methods:  map[string][]*ast.FuncDecl{},
	}
	for _, f := range files {
		pkg.add(f)
	}

	c := &applyChecker{t: t, fset: fset, pkg: pkg, seen: map[string]bool{}}
	checked := 0
	for _, f := range files {
		exempt := map[*ast.FuncLit]bool{}
		calls := map[*ast.SelectorExpr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			calls[sel] = true
			if pageActionRunners[sel.Sel.Name] {
				for _, arg := range call.Args {
					if lit, ok := arg.(*ast.FuncLit); ok {
						exempt[lit] = true
					}
				}
			}
			return true
		})

		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				if isContextErrorFunc(x.Type) && !exempt[x] {
					checked++
					c.check(x, x.Body, nil, "")
				}
			case *ast.SelectorExpr:
				// A method value: x.m not called here, so handed on and run
				// by whoever holds it.
				if calls[x] {
					return true
				}
				id, ok := x.X.(*ast.Ident)
				if !ok {
					return true
				}
				for _, m := range pkg.methodsOf(pkg.staticType(id), x.Sel.Name) {
					if isContextErrorFunc(m.Type) {
						checked++
						c.check(m, m.Body, pkg.receiverObjs(m), "method value "+x.Sel.Name+" → ")
					}
				}
			}
			return true
		})
	}
	// Without this, a parse that stopped resolving the closures would pass.
	if checked < 50 {
		t.Fatalf("only %d func(ctx context.Context) error closures found; the shape match has broken and this test proves nothing", checked)
	}
}

// stubImporter hands back an empty package for every import: the checks need
// only the package's own declarations resolved.
type stubImporter struct{}

func (stubImporter) Import(path string) (*types.Package, error) {
	p := types.NewPackage(path, filepath.Base(path))
	p.MarkComplete()
	return p, nil
}

// applyIndex is what the package declares that an apply can reach by name.
type applyIndex struct {
	info *types.Info
	// closures are local func variables (x := func(...) {...}), by object.
	closures map[types.Object]*ast.FuncLit
	// fields are func literals assigned to a field (d.commitInputs = func),
	// by field name.
	fields map[string][]*ast.FuncLit
	// funcs and methods are the package's declarations, by name.
	funcs   map[string]*ast.FuncDecl
	methods map[string][]*ast.FuncDecl
}

func (p applyIndex) add(f *ast.File) {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			if fd.Recv == nil {
				p.funcs[fd.Name.Name] = fd
			} else {
				p.methods[fd.Name.Name] = append(p.methods[fd.Name.Name], fd)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) != len(s.Rhs) {
				return true
			}
			for i, rhs := range s.Rhs {
				lit, ok := rhs.(*ast.FuncLit)
				if !ok {
					continue
				}
				switch lhs := s.Lhs[i].(type) {
				case *ast.Ident:
					if o := p.info.ObjectOf(lhs); o != nil {
						p.closures[o] = lit
					}
				case *ast.SelectorExpr:
					p.fields[lhs.Sel.Name] = append(p.fields[lhs.Sel.Name], lit)
				}
			}
		case *ast.ValueSpec:
			for i, v := range s.Values {
				if lit, ok := v.(*ast.FuncLit); ok && i < len(s.Names) {
					if o := p.info.Defs[s.Names[i]]; o != nil {
						p.closures[o] = lit
					}
				}
			}
		}
		return true
	})
}

// methodsOf is typ's methods named name. A receiver whose type can't be read
// from the source (staticType's "") follows nothing: matching on the name
// alone walks every type's read or reset into the apply.
func (p applyIndex) methodsOf(typ, name string) []*ast.FuncDecl {
	if typ == "" {
		return nil
	}
	var out []*ast.FuncDecl
	for _, md := range p.methods[name] {
		if astTypeName(md.Recv.List[0].Type) == typ {
			out = append(out, md)
		}
	}
	return out
}

// staticType is the name of the package type id's variable has, through a
// pointer and type arguments. "" when it has none the stubbed imports let the
// checker work out.
func (p applyIndex) staticType(id *ast.Ident) string {
	v, ok := p.info.ObjectOf(id).(*types.Var)
	if !ok {
		return ""
	}
	t := v.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() == v.Pkg() {
		return n.Obj().Name()
	}
	return ""
}

// astTypeName is the name of the type e spells, through * and type arguments.
func astTypeName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		default:
			return ""
		}
	}
}

// applyChecker walks an apply and everything it calls that can reach page
// state.
type applyChecker struct {
	t    *testing.T
	fset *token.FileSet
	pkg  applyIndex
	// seen stops a followed function being walked twice with the same
	// tainted parameters — and recursion from walking forever.
	seen map[string]bool
}

// check reports every write in body to page state. scope is the function
// body belongs to; for a literal, any variable declared outside it is
// captured, which is page state. tainted are the parameters (or receiver)
// that hold page state because the caller handed it in: a write through one
// is a write to page state, while reassigning it is only a local change.
func (c *applyChecker) check(scope ast.Node, body *ast.BlockStmt, tainted map[types.Object]bool, via string) {
	info := c.pkg.info
	var names []string
	for o := range tainted {
		names = append(names, o.Name())
	}
	slices.Sort(names)
	key := fmt.Sprintf("%d/%s", scope.Pos(), strings.Join(names, ","))
	if c.seen[key] {
		return
	}
	c.seen[key] = true

	_, isLit := scope.(*ast.FuncLit)
	captured := func(id *ast.Ident) bool {
		if id == nil || id.Name == "_" {
			return false
		}
		// Only a variable holds state: not a package name, a constant, a
		// function or nil.
		v, ok := info.ObjectOf(id).(*types.Var)
		if !ok {
			return false
		}
		if tainted[v] {
			return true
		}
		decl := v.Pos()
		return isLit && (decl < scope.Pos() || decl > scope.End())
	}
	report := func(target ast.Expr, at token.Pos) {
		id := assignedRoot(target)
		if !captured(id) {
			return
		}
		if _, bare := target.(*ast.Ident); bare && tainted[info.ObjectOf(id)] {
			return // reassigning a parameter changes nothing the caller holds
		}
		c.t.Errorf("%s: %san apply assigns %s, which holds page state; "+
			"an apply runs off the UI goroutine and under Script Changes, "+
			"so it never writes page state (docs/ui-rules.md)", c.fset.Position(at), via, id.Name)
	}
	// taint maps a callee's parameters to the arguments that hold page state.
	taint := func(params *ast.FieldList, args []ast.Expr) map[types.Object]bool {
		out := map[types.Object]bool{}
		var objs []types.Object
		for _, f := range params.List {
			for _, n := range f.Names {
				objs = append(objs, info.Defs[n])
			}
		}
		for i, a := range args {
			if i < len(objs) && objs[i] != nil && captured(assignedRoot(stripAddr(a))) {
				out[objs[i]] = true
			}
		}
		return out
	}

	ast.Inspect(body, func(m ast.Node) bool {
		switch s := m.(type) {
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				for _, lhs := range s.Lhs {
					report(lhs, s.Pos())
				}
			}
		case *ast.IncDecStmt:
			report(s.X, s.Pos())
		case *ast.CallExpr:
			name := ""
			switch fn := s.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
				_, isFunc := info.Uses[fn].(*types.Func)
				if lit, ok := c.pkg.closures[info.Uses[fn]]; ok && captured(fn) {
					c.check(lit, lit.Body, taint(lit.Type.Params, s.Args), via+name+" → ")
				} else if fd, ok := c.pkg.funcs[name]; ok && isFunc && !applyHelperExemptions[name] {
					if tt := taint(fd.Type.Params, s.Args); len(tt) > 0 {
						c.check(fd, fd.Body, tt, via+name+" → ")
					}
				}
			case *ast.SelectorExpr:
				name = fn.Sel.Name
				recv, ok := fn.X.(*ast.Ident)
				if !ok || !captured(recv) {
					break
				}
				for _, md := range c.pkg.methodsOf(c.pkg.staticType(recv), name) {
					tt := taint(md.Type.Params, s.Args)
					for o := range c.pkg.receiverObjs(md) {
						tt[o] = true
					}
					c.check(md, md.Body, tt, via+name+" → ")
				}
				for _, lit := range c.pkg.fields[name] {
					c.check(lit, lit.Body, taint(lit.Type.Params, s.Args), via+name+" → ")
				}
			}
		}
		return true
	})
}

// receiverObjs is a method's receiver, as a tainted set.
func (p applyIndex) receiverObjs(fd *ast.FuncDecl) map[types.Object]bool {
	out := map[types.Object]bool{}
	for _, f := range fd.Recv.List {
		for _, n := range f.Names {
			if o := p.info.Defs[n]; o != nil {
				out[o] = true
			}
		}
	}
	return out
}

// stripAddr is e without a leading &.
func stripAddr(e ast.Expr) ast.Expr {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		return u.X
	}
	return e
}

// isContextErrorFunc reports whether ft is func(<x> context.Context) error.
func isContextErrorFunc(ft *ast.FuncType) bool {
	if ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) > 1 ||
		ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	sel, ok := ft.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Context" {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "context" {
		return false
	}
	r, ok := ft.Results.List[0].Type.(*ast.Ident)
	return ok && r.Name == "error"
}

// assignedRoot is the variable an assignment target writes through: x for
// x, x.f, x[i], *x and (x).
func assignedRoot(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil
		}
	}
}
