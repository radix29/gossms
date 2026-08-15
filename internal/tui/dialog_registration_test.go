package tui

import (
	"reflect"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// Every dialog App owns has to appear in buildUI's allDialogs list, and the
// list is maintained by hand. A dialog left out of it is not broken loudly —
// it is simply never drawn, never offered input, and never relayouted on a
// resize, because syncDialogStack only ever considers what allDialogs names.
// Nothing else in the package would notice, which is exactly why this is worth
// a test rather than a code comment.
//
// The check is reflective so it covers dialogs that don't exist yet: adding a
// field to App and forgetting the one line in buildUI fails here.
func TestEveryAppDialogFieldIsRegisteredInAllDialogs(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	registered := make(map[uintptr]bool, len(a.allDialogs))
	for i, d := range a.allDialogs {
		if d == nil {
			t.Fatalf("allDialogs[%d] is nil", i)
		}
		p := reflect.ValueOf(d).Pointer()
		if registered[p] {
			t.Errorf("allDialogs lists the same dialog twice (index %d, %T)", i, d)
		}
		registered[p] = true
	}

	dialogType := reflect.TypeOf((*Dialog)(nil)).Elem()
	av := reflect.ValueOf(a).Elem()
	found := 0
	for i := range av.NumField() {
		f := av.Type().Field(i)
		// Pointer() rather than Interface(): these fields are unexported, and
		// Interface() panics on those. Identity is all this needs.
		if f.Type.Kind() != reflect.Ptr || !f.Type.Implements(dialogType) {
			continue
		}
		found++
		fv := av.Field(i)
		if fv.IsNil() {
			t.Errorf("buildUI left App.%s nil", f.Name)
			continue
		}
		if !registered[fv.Pointer()] {
			t.Errorf("App.%s (%s) is missing from buildUI's allDialogs list — it "+
				"will never draw, take input, or relayout", f.Name, f.Type)
		}
	}

	// Without this the whole test passes vacuously the day the fields move
	// behind a struct or the interface changes shape.
	if found == 0 {
		t.Fatal("found no dialog-typed fields on App — this test has stopped checking anything")
	}
	if found != len(a.allDialogs) {
		t.Errorf("App has %d dialog fields but allDialogs lists %d entries — one "+
			"of them names something that is not an App field", found, len(a.allDialogs))
	}
}
