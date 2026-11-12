package propsheet

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// Which of core's control interfaces Form and the rows with a popup implement;
// see the widgets package's control_test.go for why these are pinned. Form
// finds a row's overlay only through core.Overlay, so a row losing either
// method would stop drawing its popup last, or stop getting first refusal of
// input while it is open.
var (
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
	} = (*Form)(nil)
	_ core.Overlay = (*SelectRow)(nil)
	_ core.Overlay = (*EditorRow)(nil)
	_ core.Overlay = (*GridRow)(nil)
)

// A SelectRow hears a key or click through its DropDown's OnChange, given the
// item's text; SetSelected stays silent.
func TestSelectRowOnChangeFiresOnUserPicks(t *testing.T) {
	r := Select("Recovery", []string{"FULL", "BULK_LOGGED", "SIMPLE"}, 0)
	var got []string
	r.SetOnChange(func(v string) { got = append(got, v) })
	scr := tuikittest.NewScreen(80, 10)
	r.Layout(0, 0, 80)
	r.Draw(scr, true) // focuses the dropdown

	r.SetSelected(1) // setter: silent
	r.HandleKey(tuikittest.Key(tcell.KeyEnter))
	r.HandleKey(tuikittest.Key(tcell.KeyDown))
	r.HandleKey(tuikittest.Key(tcell.KeyEnter))

	if want := []string{"SIMPLE"}; !slices.Equal(got, want) {
		t.Errorf("OnChange calls = %q, want %q", got, want)
	}
}
