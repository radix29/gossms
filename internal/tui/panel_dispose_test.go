package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/tuikit/layout"
)

// TestClosePanelAtCancelsQueryStoreReads: closing a Query Store panel must
// cancel its report, plan and series reads. closePanelAt used to dispose panels
// through a four-way type switch that left QueryStorePanel out, so a panel
// closed mid-read kept all three running on the shared Object Explorer pool
// until qsReadTimeout.
func TestClosePanelAtCancelsQueryStoreReads(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Query Store")
	i := a.panels.AddPanel(p)

	var report, plans, series bool
	p.cancel = func() { report = true }
	p.planCancel = func() { plans = true }
	p.seriesCancel = func() { series = true }

	a.closePanelAt(i)

	if !report || !plans || !series {
		t.Errorf("closePanelAt cancelled report=%v plans=%v series=%v, want all three", report, plans, series)
	}
	if a.panelHosted(p) {
		t.Error("the Query Store panel is still hosted after closePanelAt")
	}
}

// disposingPanel is a panel type closePanelAt has never heard of: it is
// disposed only if closePanelAt goes through layout.Disposable.
type disposingPanel struct {
	stubPanel
	closed bool
}

func (p *disposingPanel) Close() { p.closed = true }

type stubPanel struct{}

func (stubPanel) SetBounds(x, y, w, h int)           {}
func (stubPanel) Draw(tcell.Screen)                  {}
func (stubPanel) HandleKey(*tcell.EventKey) bool     { return false }
func (stubPanel) HandleMouse(*tcell.EventMouse) bool { return false }
func (stubPanel) Title() string                      { return "stub" }

func TestClosePanelAtDisposesAnyDisposablePanel(t *testing.T) {
	a := newTestApp()
	p := &disposingPanel{}
	a.closePanelAt(a.panels.AddPanel(p))
	if !p.closed {
		t.Error("closePanelAt did not call Close on a layout.Disposable panel")
	}
}

// panelsWithClose is every panel type in the package that releases something
// on close. The scan below must find exactly these, so it cannot pass by
// finding nothing, and a new entry is a deliberate edit.
var panelsWithClose = []string{
	"AGDashboard",
	"ActivityMonitor",
	"LogViewer",
	"QueryPanel",
	"QueryStorePanel",
}

// TestEveryPanelCloseIsDisposable holds closePanelAt's single interface check
// to its promise that no panel type can be missed. It scans the package for
// panel types — a type with both Draw and Title methods — and requires:
//
//   - a Close method, where one exists, to be exactly func(), so the type
//     satisfies layout.Disposable. A Close() error would compile, and would
//     silently never be called;
//   - a panel type holding a context.CancelFunc field — an in-flight read — to
//     declare Close at all.
func TestEveryPanelCloseIsDisposable(t *testing.T) {
	methods := map[string]map[string]*ast.FuncType{} // receiver type -> method -> signature
	cancelFields := map[string]bool{}                // types with a context.CancelFunc field

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				id, ok := recv.(*ast.Ident)
				if !ok {
					continue
				}
				if methods[id.Name] == nil {
					methods[id.Name] = map[string]*ast.FuncType{}
				}
				methods[id.Name][d.Name.Name] = d.Type
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range st.Fields.List {
						if sel, ok := field.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "CancelFunc" {
							if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "context" {
								cancelFields[ts.Name.Name] = true
							}
						}
					}
				}
			}
		}
	}

	var withClose []string
	for typ, ms := range methods {
		if ms["Draw"] == nil || ms["Title"] == nil {
			continue
		}
		c := ms["Close"]
		if c == nil {
			if cancelFields[typ] {
				t.Errorf("panel %s holds a context.CancelFunc but has no Close(); closePanelAt cannot cancel its read", typ)
			}
			continue
		}
		withClose = append(withClose, typ)
		if c.Params.NumFields() != 0 || c.Results.NumFields() != 0 {
			t.Errorf("panel %s's Close is not func(), so it does not satisfy layout.Disposable and closePanelAt never calls it", typ)
		}
	}
	slices.Sort(withClose)
	if !slices.Equal(withClose, panelsWithClose) {
		t.Errorf("panel types with Close = %v, want %v", withClose, panelsWithClose)
	}
}

// Compile-time: each of panelsWithClose is disposed by closePanelAt.
var (
	_ layout.Disposable = (*AGDashboard)(nil)
	_ layout.Disposable = (*ActivityMonitor)(nil)
	_ layout.Disposable = (*LogViewer)(nil)
	_ layout.Disposable = (*QueryPanel)(nil)
	_ layout.Disposable = (*QueryStorePanel)(nil)
)
