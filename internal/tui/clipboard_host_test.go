package tui

import (
	"reflect"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// textEntryTypes are the widgets a user can type into or select text in. A
// dialog that owns one of them can be asked to Copy/Cut/Paste, so it has to be
// a core.ClipboardHost — see TestEveryDialogWithTextEntryIsAClipboardHost.
//
// propsheet.PropertySheet is here as a whole rather than for the rows inside
// it: it is itself the host and the target, and a dialog embedding one inherits
// both.
func textEntryTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf((*widgets.InputField)(nil)),
		reflect.TypeOf((*controls.Editor)(nil)),
		reflect.TypeOf((*propsheet.PropertySheet)(nil)),
	}
}

// Ctrl+C/X/V are handled centrally, before any dialog sees the key
// (App.handleKey), and App.activeClipboardTarget asks the frontmost dialog for
// its focused field. A dialog that owns a text widget but does not implement
// core.ClipboardHost therefore has a clipboard that does nothing at all.
//
// It used to be worse than nothing: the resolver named three dialogs and let
// every other one fall through to the query panel behind it, so Ctrl+X in the
// Find dialog cut the query editor's selection — invisibly, behind the dialog,
// reported as "Cut to clipboard". The fall-through is gone; this is what stops
// the next dialog with a text field from silently arriving inert.
//
// The check is reflective over the *types* so it covers dialogs that don't
// exist yet, and it deliberately says nothing about dialogs with no text entry
// (Help, About, Confirm): an inert clipboard is the right answer there.
func TestEveryDialogWithTextEntryIsAClipboardHost(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	hostType := reflect.TypeOf((*core.ClipboardHost)(nil)).Elem()
	checked, withText := 0, 0
	for _, d := range a.allDialogs {
		dt := reflect.TypeOf(d)
		checked++
		if !holdsTextEntry(reflect.ValueOf(d), map[uintptr]bool{}) {
			continue
		}
		withText++
		if !dt.Implements(hostType) {
			t.Errorf("%s owns a text-entry widget but is not a core.ClipboardHost — "+
				"Ctrl+C/X/V will do nothing while it is open", dt)
		}
	}

	// Both guards keep the test from passing vacuously: the first if
	// allDialogs stops being reachable, the second if holdsTextEntry stops
	// recognising the widgets (a rename, a move behind another struct).
	if checked == 0 {
		t.Fatal("no dialogs to check — this test has stopped checking anything")
	}
	if withText == 0 {
		t.Fatal("no dialog was found to own a text-entry widget — holdsTextEntry " +
			"has stopped recognising them")
	}
}

// holdsTextEntry reports whether v reaches one of textEntryTypes.
//
// The walk is over the built dialog's value rather than its type alone,
// because a dialog can reach its widgets through an interface — a
// []focusable, a Panel field — and an interface-typed field declares nothing
// about what is in it. A type-only walk answers "no text entry" for a dialog
// that keeps its fields in nothing but its focus ring, and the dialog then
// arrives with an inert clipboard and this test silently approving. Today no
// dialog is shaped that way; buildUI has populated the rings by the time this
// runs, so following them costs nothing and stops the day one is.
//
// A nil pointer has nothing to follow, so the walk falls back to that field's
// declared type: a sub-struct built lazily still counts. seen holds the
// pointers already visited, breaking the cycles the dialog graph really has —
// every dialog holds an *App, and App holds every dialog — and *App itself is
// skipped outright, since a dialog reaching a text field only by way of the
// whole application is not a dialog that owns one.
func holdsTextEntry(v reflect.Value, seen map[uintptr]bool) bool {
	if !v.IsValid() {
		return false
	}
	t := v.Type()
	for _, w := range textEntryTypes() {
		if t == w {
			return true
		}
	}
	if t == reflect.TypeOf((*App)(nil)) {
		return false
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return typeHoldsTextEntry(t.Elem(), map[reflect.Type]bool{})
		}
		if seen[v.Pointer()] {
			return false
		}
		seen[v.Pointer()] = true
		return holdsTextEntry(v.Elem(), seen)
	case reflect.Interface:
		if v.IsNil() {
			return false
		}
		return holdsTextEntry(v.Elem(), seen)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if holdsTextEntry(v.Index(i), seen) {
				return true
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if holdsTextEntry(v.MapIndex(k), seen) {
				return true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if holdsTextEntry(v.Field(i), seen) {
				return true
			}
		}
	}
	return false
}

// typeHoldsTextEntry is the same question asked of a type, for the branches
// where the value is nil and there is nothing to walk. Unexported fields are
// reached by type, so nothing here needs a widget to be non-nil.
func typeHoldsTextEntry(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	for _, w := range textEntryTypes() {
		if t == w {
			return true
		}
	}
	if t == reflect.TypeOf((*App)(nil)) {
		return false
	}
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return typeHoldsTextEntry(t.Elem(), seen)
	case reflect.Map:
		return typeHoldsTextEntry(t.Key(), seen) || typeHoldsTextEntry(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if typeHoldsTextEntry(t.Field(i).Type, seen) {
				return true
			}
		}
	}
	return false
}

// The reachability walk has to follow an interface-typed field. A dialog that
// keeps its widgets in nothing but its []focusable focus ring owns them just
// as much as one with named fields, and a walk that stops at the interface
// answers "no text entry" — approving a dialog whose clipboard is inert.
func TestHoldsTextEntryFollowsAnInterfaceField(t *testing.T) {
	type ring struct{ fields []focusable }
	r := &ring{fields: []focusable{widgets.NewInputField("Server:", 10, false)}}

	if !holdsTextEntry(reflect.ValueOf(r), map[uintptr]bool{}) {
		t.Error("holdsTextEntry missed an input field held only through a []focusable")
	}
	if typeHoldsTextEntry(reflect.TypeOf(r), map[reflect.Type]bool{}) {
		t.Error("typeHoldsTextEntry claims to see through an interface field, which " +
			"a type-only walk cannot — one of the two walks is not doing what it says")
	}
}

// The value walk stops at a nil pointer, so the type walk takes over there: a
// sub-struct a dialog builds lazily still counts as owned.
func TestHoldsTextEntryFallsBackToTheTypeAtANilPointer(t *testing.T) {
	// Both of these are read only through reflection, which staticcheck's
	// U1000 cannot see — the walk under test is the reader.
	//lint:ignore U1000 reached by holdsTextEntry's reflection walk
	type page struct{ input *widgets.InputField }
	type lazy struct {
		//lint:ignore U1000 reached by the reflection walk under test
		p *page
	}

	if !holdsTextEntry(reflect.ValueOf(&lazy{}), map[uintptr]bool{}) {
		t.Error("holdsTextEntry missed a text field behind an unbuilt sub-struct")
	}
}
