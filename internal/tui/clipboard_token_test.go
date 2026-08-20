package tui

import (
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// tokenDialog is a host shaped like propsheet.PropertySheet: it hands back
// itself as the clipboard target whichever of its fields has focus, and
// distinguishes them only through ClipboardTargetToken. That shape is the
// whole reason the token exists — pasteInto's "is this still the target?"
// check passes unconditionally against it.
type tokenDialog struct {
	fields  []*widgets.InputField
	focused int
}

func newTokenDialog() *tokenDialog {
	return &tokenDialog{fields: []*widgets.InputField{
		widgets.NewInputField("", 20, false), widgets.NewInputField("", 20, false),
	}}
}

func (d *tokenDialog) field() *widgets.InputField { return d.fields[d.focused] }

func (d *tokenDialog) Visible() bool                                { return true }
func (d *tokenDialog) Draw(tcell.Screen)                            {}
func (d *tokenDialog) HandleKey(*tcell.EventKey) bool               { return false }
func (d *tokenDialog) HandleMouse(*tcell.EventMouse) bool           { return false }
func (d *tokenDialog) Relayout()                                    {}
func (d *tokenDialog) HasSelection() bool                           { return d.field().HasSelection() }
func (d *tokenDialog) SelectedText() string                         { return d.field().SelectedText() }
func (d *tokenDialog) Cut() string                                  { return d.field().Cut() }
func (d *tokenDialog) Paste(text string)                            { d.field().Paste(text) }
func (d *tokenDialog) SelectAll()                                   { d.field().SelectAll() }
func (d *tokenDialog) FocusedClipboardTarget() core.ClipboardTarget { return d }
func (d *tokenDialog) ClipboardTargetToken() any                    { return d.field() }

func newTokenDialogApp(t *testing.T) (*App, *tokenDialog) {
	t.Helper()
	a := newClipboardTestApp()
	d := newTokenDialog()
	a.dialogStack = []Dialog{d}
	return a, d
}

// A paste aimed at one field of a self-targeting host and delivered after
// focus moved to another must be dropped. Without the token the identity
// check cannot see the move — the host is the target either way — and the
// text lands in whichever field the user moved to.
func TestPasteIsDroppedWhenFocusMovedWithinTheSameHost(t *testing.T) {
	a, d := newTokenDialogApp(t)
	target := a.activeClipboardTarget()
	token := clipboardTargetToken(a.topDialog())

	d.focused = 1 // the read comes back after the user tabbed on
	a.pasteInto(target, token, "hunter2")

	if got := d.fields[0].Value(); got != "" {
		t.Errorf("field 0 = %q, want it untouched — focus had left it", got)
	}
	if got := d.fields[1].Value(); got != "" {
		t.Errorf("the paste landed in the field the user moved to: %q", got)
	}
}

// The same paste with focus still where it was goes in, so the guard is not
// simply refusing everything.
func TestPasteAppliesWhenFocusHasNotMoved(t *testing.T) {
	a, d := newTokenDialogApp(t)
	a.pasteInto(a.activeClipboardTarget(), clipboardTargetToken(a.topDialog()), "hunter2")

	if got := d.fields[0].Value(); got != "hunter2" {
		t.Fatalf("field 0 = %q, want the pasted text", got)
	}
}

// A host that hands back itself resolves the real field on every call, so
// App.pasteInto's "is this still the target?" check cannot see focus move
// between two of its fields — the paste lands wherever focus went. Such a
// host has to distinguish them through core.ClipboardTargetTokener, and
// App.clipboardTargetToken asserts for that on the *dialog*, so the dialog is
// where the method must be reachable.
//
// The test recognises the shape rather than naming propsheet.PropertySheet:
// a target that is itself a core.ClipboardHost is one that answered with
// itself. That covers the next host built this way, which is the point —
// nothing else about it looks wrong.
func TestASelfTargetingClipboardHostCarriesAToken(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	hostType := reflect.TypeOf((*core.ClipboardHost)(nil)).Elem()
	tokenerType := reflect.TypeOf((*core.ClipboardTargetTokener)(nil)).Elem()

	selfTargeting := 0
	for _, d := range a.allDialogs {
		h, ok := d.(core.ClipboardHost)
		if !ok {
			continue
		}
		target := h.FocusedClipboardTarget()
		if target == nil || !reflect.TypeOf(target).Implements(hostType) {
			continue
		}
		selfTargeting++
		if !reflect.TypeOf(d).Implements(tokenerType) {
			t.Errorf("%T hands back a self-resolving clipboard target but is not a "+
				"core.ClipboardTargetTokener — a paste will follow focus to the "+
				"wrong field", d)
		}
	}
	if selfTargeting == 0 {
		t.Fatal("no self-targeting clipboard host was found — the Properties and " +
			"New-<object> dialogs embed a propsheet.PropertySheet, which is one, " +
			"so this test has stopped checking anything")
	}
}
