package widgets

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// Which of core's control interfaces each widget implements. A host's focus
// ring type-asserts on these, so losing a method is a silent behaviour change
// there (a ring skipping disabled entries through core.Enabler would stop
// skipping), not a compile error.
var (
	_ interface {
		core.Focusable
		core.Enabler
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*Button)(nil)
	_ interface {
		core.Focusable
		core.Enabler
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*CheckBox)(nil)
	_ interface {
		core.Focusable
		core.Enabler
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*RadioBox)(nil)
	_ interface {
		core.Focusable
		core.Enabler
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
	} = (*DropDown)(nil)
	_ interface {
		core.Focusable
		core.Enabler
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.ClipboardTarget
	} = (*InputField)(nil)
)

func TestWidgetsReportFocus(t *testing.T) {
	for _, w := range []core.Focusable{
		NewButton("OK", nil), NewCheckBox("x"), NewRadioBox("", []string{"a"}),
		NewDropDown("", []string{"a"}, 5), NewInputField("", 5, false),
	} {
		w.Focus(true)
		if !w.Focused() {
			t.Errorf("%T: Focused() = false after Focus(true)", w)
		}
		w.Focus(false)
		if w.Focused() {
			t.Errorf("%T: Focused() = true after Focus(false)", w)
		}
	}
}

// Bounds is the drawn extent: every cell inside it is one the widget painted
// and every cell outside is not, so a host hit-testing or anchoring a popup by
// it lines up with the screen.
func TestWidgetBoundsMatchWhatIsDrawn(t *testing.T) {
	cases := []struct {
		name string
		w    interface {
			core.Bounded
			Draw(tcell.Screen)
		}
		want core.Rect
	}{
		{"Button", NewButton("OK", nil), core.Rect{X: 3, Y: 2, W: 6, H: 1}},                                 // "[ OK ]"
		{"CheckBox", NewCheckBox("Encrypt"), core.Rect{X: 3, Y: 2, W: 11, H: 1}},                            // "[ ] Encrypt"
		{"RadioBox", NewRadioBox("Mode", []string{"Fast", "Thorough"}), core.Rect{X: 3, Y: 2, W: 12, H: 3}}, // label + "( ) Thorough"
		{"DropDown", NewDropDown("Auth:", []string{"SQL"}, 6), core.Rect{X: 3, Y: 2, W: 14, H: 1}},          // "Auth: [     v]"
		{"InputField", NewInputField("Name:", 6, false), core.Rect{X: 3, Y: 2, W: 14, H: 1}},                // "Name: [      ]"
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.w.(interface{ SetBounds(x, y int) }).SetBounds(3, 2)
			if got := c.w.Bounds(); got != c.want {
				t.Fatalf("Bounds() = %+v, want %+v", got, c.want)
			}
			scr := tuikittest.NewScreen(30, 8)
			c.w.Draw(scr)
			for y := range 8 {
				for x := range 30 {
					drawn := scr.RuneAt(x, y) != ' ' && scr.RuneAt(x, y) != 0
					if drawn && !c.want.Contains(x, y) {
						t.Errorf("drew %q at (%d,%d), outside Bounds %+v", scr.RuneAt(x, y), x, y, c.want)
					}
				}
			}
			// The last column is the closing glyph, not padding.
			if r := scr.RuneAt(c.want.Right()-1, c.want.Y+c.want.H-1); r == ' ' && c.name != "RadioBox" {
				t.Errorf("Bounds' last column is blank; want the widget's closing glyph")
			}
		})
	}
}

// OnChange reports user edits only: a host that set the value already knows,
// and firing from a setter would re-enter the host's own callback.
func TestInputFieldOnChangeFiresOnUserEditsOnly(t *testing.T) {
	f := NewInputField("", 20, false)
	f.Focus(true)
	var got []string
	f.OnChange = func(v string) { got = append(got, v) }

	f.SetValue("abc")                               // setter: silent
	f.HandleKey(tuikittest.Key(tcell.KeyLeft))      // moves only: silent
	f.HandleKey(tuikittest.Rune('x'))               // "abxc"
	f.HandleKey(tuikittest.Key(tcell.KeyBackspace)) // "abc"
	f.HandleKey(tuikittest.Key(tcell.KeyDelete))    // "ab"
	f.HandleKey(tuikittest.Key(tcell.KeyDelete))    // at the end: nothing to delete, silent
	f.SelectAll()
	if cut := f.Cut(); cut != "ab" {
		t.Fatalf("Cut() = %q, want %q", cut, "ab")
	}
	f.Paste("") // nothing pasted: silent
	f.Paste("zz")
	f.HandleKey(tuikittest.Key(tcell.KeyCtrlU)) // ""
	f.HandleKey(tuikittest.Key(tcell.KeyCtrlU)) // already empty: silent
	f.HandleMouse(tuikittest.Press(2, 0))       // click-to-position: silent
	f.HandleMouse(tuikittest.Release(2, 0))

	want := []string{"abxc", "abc", "ab", "", "zz", ""}
	if !slices.Equal(got, want) {
		t.Errorf("OnChange calls = %q, want %q", got, want)
	}
}

func TestCheckBoxOnChangeFiresOnUserTogglesOnly(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.SetBounds(0, 0)
	c.Focus(true)
	var got []bool
	c.OnChange = func(v bool) { got = append(got, v) }

	c.SetChecked(true)                    // setter: silent
	c.HandleKey(tuikittest.Rune(' '))     // false
	c.HandleMouse(tuikittest.Press(1, 0)) // true
	c.HandleMouse(tuikittest.Press(2, 0)) // held: silent
	c.HandleMouse(tuikittest.Release(2, 0))
	c.HandleKey(tuikittest.Key(tcell.KeyEnter)) // false

	if want := []bool{false, true, false}; !slices.Equal(got, want) {
		t.Errorf("OnChange calls = %v, want %v", got, want)
	}
}

func TestRadioBoxOnChangeFiresOnUserMovesOnly(t *testing.T) {
	r := NewRadioBox("", []string{"A", "B", "C"})
	r.SetBounds(0, 0)
	r.Focus(true)
	var got []int
	r.OnChange = func(i int) { got = append(got, i) }

	r.SetSelected(1)                           // setter: silent
	r.HandleKey(tuikittest.Key(tcell.KeyDown)) // 2
	r.HandleKey(tuikittest.Key(tcell.KeyDown)) // at the end: refused, silent
	r.HandleMouse(tuikittest.Press(1, 2))      // the selected option again: silent
	r.HandleMouse(tuikittest.Release(1, 2))
	r.HandleMouse(tuikittest.Press(1, 0)) // 0

	if want := []int{2, 0}; !slices.Equal(got, want) {
		t.Errorf("OnChange calls = %v, want %v", got, want)
	}
}

func TestDropDownOnChangeFiresOnUserPicksOnly(t *testing.T) {
	d := NewDropDown("", []string{"A", "B", "C"}, 6)
	d.SetBounds(0, 0)
	d.Focus(true)
	var got []int
	d.OnChange = func(i int) { got = append(got, i) }

	d.SetSelected(1)                            // setter: silent
	d.SetItems([]string{"A", "B", "C", "D"})    // setter (resets to 0): silent
	d.HandleKey(tuikittest.Key(tcell.KeyEnter)) // opens: silent
	d.HandleKey(tuikittest.Key(tcell.KeyDown))  // 1, while open
	d.HandleKey(tuikittest.Key(tcell.KeyUp))    // 0
	d.HandleKey(tuikittest.Key(tcell.KeyUp))    // at the top: silent
	d.HandleKey(tuikittest.Rune('c'))           // jump to "C": 2
	d.HandleKey(tuikittest.Key(tcell.KeyEscape))
	d.Draw(tuikittest.NewScreen(20, 10))
	d.HandleMouse(tuikittest.Press(1, 0)) // opens: silent
	d.HandleMouse(tuikittest.Release(1, 0))
	d.HandleMouse(tuikittest.Press(2, 4)) // picks "D": 3
	if d.OverlayActive() {
		t.Fatal("list still open after a pick")
	}

	if want := []int{1, 0, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("OnChange calls = %v, want %v", got, want)
	}
}
