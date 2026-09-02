package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// A click in a text field starts a gesture that belongs to that field until
// the release, wherever the pointer goes — invariant 1 in ARCHITECTURE.md
// § The mouseDragging idiom. dialogs.FieldGesture is how a dialog holds one,
// and dialog_drag_test.go exercises the dialogs one at a time.
//
// That per-dialog list is what these two tests exist to backstop. It is
// maintained by hand, so it says nothing at all about a dialog nobody added to
// it — which is how three dialogs (Options here, PromptDialog and
// TypedConfirmDialog in tuikit) kept hand-rolling the protocol long after the
// other seven were converted, each getting it wrong, and every test still
// passing. Options pressed OK from a selection drag that reached the button
// row; Prompt did the same and its OK *accepts*, so a drag renamed the object;
// TypedConfirm's answered the confirmation the retyping exists to slow down.
//
// TestEveryDialogWithATextFieldOwnsAFieldGesture is the ownership half and
// TestFieldGestureCallsAreOrderedCorrectly the usage half. Neither is
// sufficient alone: Options owned no gesture, and a dialog that owns one can
// still call it in an order that does nothing.

// gestureFieldType is the widget whose press starts a drag a FieldGesture has
// to own; gestureWalkStops are the three that own their own and end the walk.
//
// controls.Editor hit-tests inside its own HandleMouse (it has no separate
// HitTest, which is why a host calls it unconditionally).
// propsheet.PropertySheet routes every press through its own dragZone — the
// sheet-level form of the same invariant — and propsheet.TextRow is the form
// row that wraps an InputField, reached by a dialog that holds its rows
// directly (New Index, New Statistics) rather than only through the sheet. A
// text field behind any of the three is that widget's problem, not the
// dialog's.
func gestureFieldType() reflect.Type { return reflect.TypeOf((*widgets.InputField)(nil)) }

func gestureWalkStops() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf((*propsheet.PropertySheet)(nil)),
		reflect.TypeOf((*propsheet.TextRow)(nil)),
		reflect.TypeOf((*controls.Editor)(nil)),
	}
}

// dialogsWithoutAGesture names the dialogs allowed to own an InputField and no
// FieldGesture. It is empty, and a new entry needs a reason written beside it:
// every dialog that has ever qualified for this list turned out to have the
// bug instead.
var dialogsWithoutAGesture = map[string]string{}

// TestEveryDialogWithATextFieldOwnsAFieldGesture walks the built dialogs
// rather than the source, so it covers the tuikit dialogs App registers as
// well as this package's own — the bug reached both.
func TestEveryDialogWithATextFieldOwnsAFieldGesture(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	field, gesture := gestureFieldType(), reflect.TypeOf(dialogs.FieldGesture{})
	checked, withField := 0, 0
	for _, d := range a.allDialogs {
		name := reflect.TypeOf(d).String()
		checked++
		if !reaches(reflect.ValueOf(d), field, map[uintptr]bool{}) {
			continue
		}
		withField++
		if why, ok := dialogsWithoutAGesture[name]; ok {
			t.Logf("%s exempt: %s", name, why)
			continue
		}
		if !reaches(reflect.ValueOf(d), gesture, map[uintptr]bool{}) {
			t.Errorf("%s owns a *widgets.InputField but no dialogs.FieldGesture — a "+
				"text-selection drag out of the field will freeze at its rect, and one "+
				"reaching the button row will press the button under the pointer", name)
		}
	}

	// Both guards keep the test from passing vacuously: the first if
	// allDialogs stops being reachable, the second if the walk stops
	// recognising an InputField (a rename, a move behind another struct).
	if checked == 0 {
		t.Fatal("no dialogs to check — this test has stopped checking anything")
	}
	if withField == 0 {
		t.Fatal("no dialog was found to own an input field — the walk has stopped " +
			"recognising them")
	}
	for name := range dialogsWithoutAGesture {
		if !hasDialogNamed(a, name) {
			t.Errorf("dialogsWithoutAGesture names %s, which is not a registered dialog — "+
				"a stale exemption silently covers nothing", name)
		}
	}
}

func hasDialogNamed(a *App, name string) bool {
	for _, d := range a.allDialogs {
		if reflect.TypeOf(d).String() == name {
			return true
		}
	}
	return false
}

// reaches reports whether v holds a value of type want.
//
// The walk is over the built dialog's value, not its type, so a widget held
// only through an interface (a []focusable focus ring) still counts — see
// holdsTextEntry in clipboard_host_test.go, which walks for the same reason.
// It stops at *App, since a dialog reaching a field by way of the whole
// application does not own one, and at the two widgets that own their own
// gesture.
func reaches(v reflect.Value, want reflect.Type, seen map[uintptr]bool) bool {
	if !v.IsValid() {
		return false
	}
	t := v.Type()
	if t == want {
		return true
	}
	if t == reflect.TypeOf((*App)(nil)) {
		return false
	}
	for _, stop := range gestureWalkStops() {
		if t == stop {
			return false
		}
	}
	switch v.Kind() {
	case reflect.Ptr:
		// A nil pointer has nothing to walk, so the question falls back to the
		// declared type: a sub-struct the dialog builds lazily — every property
		// dialog's pages, for one — still owns whatever is in it.
		if v.IsNil() {
			return reachesType(t.Elem(), want, map[reflect.Type]bool{})
		}
		if seen[v.Pointer()] {
			return false
		}
		seen[v.Pointer()] = true
		return reaches(v.Elem(), want, seen)
	case reflect.Interface:
		if v.IsNil() {
			return false
		}
		return reaches(v.Elem(), want, seen)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if reaches(v.Index(i), want, seen) {
				return true
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if reaches(v.MapIndex(k), want, seen) {
				return true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if reaches(v.Field(i), want, seen) {
				return true
			}
		}
	}
	return false
}

// gestureSourceDirs are the two packages a dialog with a FieldGesture lives
// in. This test reads their source rather than importing them — tuikit knows
// nothing about tui and must not start to — because duplicating the checker in
// each package is how the two halves of a rule drift apart.
func gestureSourceDirs() []string { return []string{".", "../tuikit/dialogs"} }

// TestFieldGestureCallsAreOrderedCorrectly is the usage half. Owning a
// FieldGesture proves nothing on its own: the three calls only work in one
// order, and the order is a property of what the surrounding calls do rather
// than of any one of them.
//
//   - Release above ConsumeOutsideClick, which returns early on a release
//     outside the dialog — exactly the event that strands the latch.
//   - Replay below ConsumeOutsideClick and above the last ButtonClicked. The
//     dialogs with a progress or inspect mode ask ButtonClicked in that mode
//     first and legitimately precede the replay; those modes show no text
//     field, so the form's own ButtonClicked — the last one — is the bound.
//   - Claim somewhere, so a press is routed through the gesture rather than
//     left to InputField.HandleMouse's own bounds check, which is skipped
//     while the widget is latched.
//   - Clear on the path that opens the dialog — the method that calls Show —
//     so a latch does not survive into the next showing. The opening method is
//     named Show, ShowX, show or start depending on the dialog, so it is
//     identified by what it calls rather than by its name.
func TestFieldGestureCallsAreOrderedCorrectly(t *testing.T) {
	type methods struct {
		handleMouse *ast.FuncDecl
		claims      bool
		cleared     bool
	}
	fset := token.NewFileSet()
	// Keyed by "package.Type" so two dialogs of the same name in the two
	// packages stay apart.
	owners := map[string]bool{}
	found := map[string]*methods{}

	for _, dir := range gestureSourceDirs() {
		names, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			f, err := parser.ParseFile(fset, name, src, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			pkg := f.Name.Name
			for _, decl := range f.Decls {
				switch d := decl.(type) {
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
						if structHasFieldGesture(st) {
							owners[pkg+"."+ts.Name.Name] = true
						}
					}
				case *ast.FuncDecl:
					recv := receiverTypeName(d)
					if recv == "" {
						continue
					}
					key := pkg + "." + recv
					m := found[key]
					if m == nil {
						m = &methods{}
						found[key] = m
					}
					if d.Name.Name == "HandleMouse" {
						m.handleMouse = d
					}
					if callFound(d, "Claim") {
						m.claims = true
					}
					// The opening path, whatever it is called: the method that
					// puts the dialog on screen is the one that must drop a
					// latch left behind by the last showing.
					if callFound(d, "Clear") && callFound(d, "Show") {
						m.cleared = true
					}
				}
			}
		}
	}

	if len(owners) == 0 {
		t.Fatal("no type with a dialogs.FieldGesture field was found — this test has " +
			"stopped checking anything")
	}
	for owner := range owners {
		m := found[owner]
		if m == nil || m.handleMouse == nil {
			t.Errorf("%s owns a FieldGesture but has no HandleMouse to drive it", owner)
			continue
		}
		if !m.claims {
			t.Errorf("%s never calls FieldGesture.Claim — its press is being routed by "+
				"the widget's own bounds check, which a latch from the previous showing skips",
				owner)
		}
		if !m.cleared {
			t.Errorf("%s never calls FieldGesture.Clear on the path that shows it — a latch "+
				"will survive into the next showing and route every click to that field", owner)
		}
		release := firstCallLine(m.handleMouse, fset, "Release")
		replay := firstCallLine(m.handleMouse, fset, "Replay")
		consume := firstCallLine(m.handleMouse, fset, "ConsumeOutsideClick")
		lastButton := lastCallLine(m.handleMouse, fset, "ButtonClicked")
		switch {
		case release < 0:
			t.Errorf("%s.HandleMouse never calls FieldGesture.Release", owner)
		case consume >= 0 && release > consume:
			t.Errorf("%s.HandleMouse calls Release (line %d) below ConsumeOutsideClick "+
				"(line %d) — a release outside the dialog is consumed first and strands the latch",
				owner, release, consume)
		}
		switch {
		case replay < 0:
			t.Errorf("%s.HandleMouse never calls FieldGesture.Replay — a drag out of the "+
				"field will freeze at its rect", owner)
		case consume >= 0 && replay < consume:
			t.Errorf("%s.HandleMouse calls Replay (line %d) above ConsumeOutsideClick "+
				"(line %d)", owner, replay, consume)
		case lastButton >= 0 && replay > lastButton:
			t.Errorf("%s.HandleMouse calls Replay (line %d) below its last ButtonClicked "+
				"(line %d) — a selection drag reaching the button row will press the button "+
				"under the pointer", owner, replay, lastButton)
		}
	}
}

func structHasFieldGesture(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if typeName(f.Type) == "FieldGesture" {
			return true
		}
	}
	return false
}

// typeName renders the leaf name of a field's type: FieldGesture for both the
// bare form used inside package dialogs and the dialogs.FieldGesture form used
// outside it.
func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.StarExpr:
		return typeName(v.X)
	}
	return ""
}

func receiverTypeName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	return typeName(d.Recv.List[0].Type)
}

func callFound(d *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(d, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

// firstCallLine and lastCallLine are the positions of a named method call
// inside d, or -1. Lines are compared rather than offsets so the message names
// something a reader can go and look at.
func firstCallLine(d *ast.FuncDecl, fset *token.FileSet, name string) int {
	return callLine(d, fset, name, true)
}

func lastCallLine(d *ast.FuncDecl, fset *token.FileSet, name string) int {
	return callLine(d, fset, name, false)
}

func callLine(d *ast.FuncDecl, fset *token.FileSet, name string, first bool) int {
	got := -1
	ast.Inspect(d, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return true
		}
		line := fset.Position(call.Pos()).Line
		if got < 0 || (!first && line > got) {
			got = line
		}
		return true
	})
	return got
}

// The ownership walk is the half that cannot be exercised by mutating a real
// dialog — every dialog owns a gesture now, and taking one away stops the
// package compiling. These pin the two things it has to get right, both of
// which would make it approve a dialog with no gesture.

// A dialog keeping its fields in nothing but a focus ring owns them just as
// much as one with named fields, and a walk that stops at the interface
// answers "no text field".
func TestGestureWalkFollowsAnInterfaceField(t *testing.T) {
	type ring struct{ fields []focusable }
	r := &ring{fields: []focusable{widgets.NewInputField("Name:", 10, false)}}

	if !reaches(reflect.ValueOf(r), gestureFieldType(), map[uintptr]bool{}) {
		t.Error("the walk missed an input field held only through a []focusable")
	}
}

// A PropertySheet, its TextRow and an Editor route their own presses, so the
// fields inside them are not the dialog's to own. A walk that counted them
// would demand a FieldGesture from every property dialog and from New Index
// and New Statistics, and the exemptions needed to silence that would hide a
// dialog that really does own a loose field.
func TestGestureWalkStopsAtWidgetsThatOwnTheirOwnGesture(t *testing.T) {
	type sheeted struct {
		row *propsheet.TextRow
		ed  *controls.Editor
	}
	// A TextRow really does hold an InputField, which is what makes the stop
	// load-bearing rather than decorative — remove it and this fails.
	d := &sheeted{row: propsheet.Text("Name", "", 20), ed: controls.NewEditor(nil)}
	if reaches(reflect.ValueOf(d), gestureFieldType(), map[uintptr]bool{}) {
		t.Error("the walk counted a field inside a propsheet row or an Editor, which " +
			"route their own presses")
	}
	// And the stop is specific to those — a loose field beside them still
	// counts, or the stop would hide the thing the test exists to find.
	type both struct {
		sheeted
		loose *widgets.InputField
	}
	b := &both{sheeted: *d, loose: widgets.NewInputField("Name:", 10, false)}
	if !reaches(reflect.ValueOf(b), gestureFieldType(), map[uintptr]bool{}) {
		t.Error("the walk missed a loose input field sitting beside a propsheet row")
	}
}

// The value walk stops at a nil pointer, so the type walk takes over there. No
// dialog is shaped that way today — every one of them builds its widgets in
// its constructor — which is exactly why this is pinned here rather than left
// to the dialogs to exercise: a dialog that builds a field lazily must still
// be found, and nothing else would notice if the fallback were dropped.
func TestGestureWalkFallsBackToTheTypeAtANilPointer(t *testing.T) {
	// Read only through reflection, which staticcheck's U1000 cannot see —
	// the walk under test is the reader.
	//lint:ignore U1000 reached by the gesture walk's reflection
	type page struct{ input *widgets.InputField }
	type lazy struct {
		//lint:ignore U1000 reached by the reflection walk under test
		p *page
	}

	if !reaches(reflect.ValueOf(&lazy{}), gestureFieldType(), map[uintptr]bool{}) {
		t.Error("the walk missed an input field behind an unbuilt sub-struct")
	}
	// The stops apply to the type walk too, or the fallback would reintroduce
	// every dialog holding an unbuilt propsheet row.
	//lint:ignore U1000 reached by the gesture walk's reflection
	type lazySheet struct{ row *propsheet.TextRow }
	if reaches(reflect.ValueOf(&lazySheet{}), gestureFieldType(), map[uintptr]bool{}) {
		t.Error("the type walk counted a field inside an unbuilt propsheet row")
	}
}

// reachesType is the same question asked of a type, for the branches where the
// value is nil and there is nothing to walk. It cannot see through an
// interface, which is why the value walk above is the primary one.
func reachesType(t, want reflect.Type, seen map[reflect.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	if t == want {
		return true
	}
	if t == reflect.TypeOf((*App)(nil)) {
		return false
	}
	for _, stop := range gestureWalkStops() {
		if t == stop {
			return false
		}
	}
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return reachesType(t.Elem(), want, seen)
	case reflect.Map:
		return reachesType(t.Key(), want, seen) || reachesType(t.Elem(), want, seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if reachesType(t.Field(i).Type, want, seen) {
				return true
			}
		}
	}
	return false
}
