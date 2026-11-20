package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/tuikit/tuikittest"
)

func typeProfileName(d *ProfileDialog, s string) {
	for _, r := range s {
		d.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
}

// openProfile selects History row i and presses Profile....
func openProfile(t *testing.T, d *ConnectDialog, i int) *ProfileDialog {
	t.Helper()
	d.history.SetSelected(i)
	d.applyHistory(i)
	d.btnFocus = connectBtnProfile
	d.doButton()
	pd := d.app.profileDialog()
	if !pd.Visible() {
		t.Fatal("Profile... did not open the Profile dialog")
	}
	return pd
}

func TestConnectProfileNamesTheSelectedConnection(t *testing.T) {
	scratchConfigHome(t)
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "older", User: "sa"},
		config.Connection{Server: "newer", User: "sa"},
	)
	// Row 1 is "older" (History is most recent first): not the first row, so a
	// dialog ignoring the selection fails.
	pd := openProfile(t, d, 1)
	if pd.conn != "older,1433,,sa" || pd.fName.Value() != "" || pd.allow() != config.AllowAdmin {
		t.Fatalf("dialog opened on %q, name %q, allow %q", pd.conn, pd.fName.Value(), pd.allow())
	}
	typeProfileName(pd, "dev")
	// F4 opens the list, as in the Connect dialog; Enter there picks the item
	// rather than pressing OK. Enter on the closed dropdown opens it again (the
	// widget gets Enter first: dialogs.FieldRingKey), so OK is pressed from the
	// name field.
	pd.HandleKey(key(tcell.KeyTab))
	pd.HandleKey(key(tcell.KeyF4))
	for range 3 {
		pd.HandleKey(key(tcell.KeyUp))
	}
	pd.HandleKey(key(tcell.KeyEnter))
	if !pd.Visible() || pd.allow() != config.AllowRead {
		t.Fatalf("Enter on the open list: visible %v, allow %q", pd.Visible(), pd.allow())
	}
	pd.HandleKey(key(tcell.KeyEnter))
	if !pd.Visible() || !pd.ddAllow.OverlayActive() {
		t.Fatalf("Enter on the closed dropdown: visible %v, list open %v", pd.Visible(), pd.ddAllow.OverlayActive())
	}
	pd.HandleKey(key(tcell.KeyEscape))
	pd.HandleKey(key(tcell.KeyBacktab))
	pd.HandleKey(key(tcell.KeyEnter))
	if pd.Visible() {
		t.Fatalf("OK left the dialog open: %q", pd.status)
	}

	got, err := d.app.cfg.ProfileByName("dev")
	if err != nil || got.Server != "older" {
		t.Fatalf("ProfileByName(dev) = %+v, %v", got, err)
	}
	if got.MaxAllow != config.AllowRead {
		t.Errorf("ceiling %q, want read", got.MaxAllow)
	}
	// History shows the name first and keeps the highlight on the entry.
	if i := d.history.Selected(); d.historyConns[i].Server != "older" {
		t.Errorf("highlight moved to %q", d.historyConns[i].Server)
	}
	if label := matchLabel(d.historyConns[d.history.Selected()]); label != "[dev] older,1433,,sa" {
		t.Errorf("History label %q", label)
	}
	// Saved to disk: a fresh Load sees it.
	savesInFlight.Wait()
	if p, err := config.Load().ProfileByName("dev"); err != nil || p.Server != "older" {
		t.Errorf("not saved: %+v, %v", p, err)
	}
}

func TestConnectProfileRefusesBadAndDuplicateNames(t *testing.T) {
	scratchConfigHome(t)
	a := config.Connection{Server: "a", User: "sa"}
	d := twoPaneConnectDialog(t, a, config.Connection{Server: "b", User: "sa"})
	if err := d.app.cfg.SetProfile(a.GeneratedName(), "dev", config.AllowRead); err != nil {
		t.Fatal(err)
	}
	d.reloadHistory()
	pd := openProfile(t, d, 0) // "b"
	for _, name := range []string{"DEV", "-x", "a b"} {
		pd.fName.SetValue(name)
		pd.HandleKey(key(tcell.KeyEnter))
		if !pd.Visible() || pd.status == "" {
			t.Errorf("%q: closed %v, status %q — want it refused in place", name, !pd.Visible(), pd.status)
		}
	}
	if p, _ := d.app.cfg.ProfileByName("dev"); p.Server != "a" {
		t.Errorf("dev moved to %q", p.Server)
	}
	// Escape cancels with nothing applied.
	pd.HandleKey(key(tcell.KeyEscape))
	if pd.Visible() {
		t.Error("Escape left the dialog open")
	}
}

// Clearing the name turns the entry back into a plain recent connection.
func TestConnectProfileClears(t *testing.T) {
	scratchConfigHome(t)
	a := config.Connection{Server: "a", User: "sa"}
	d := twoPaneConnectDialog(t, a)
	if err := d.app.cfg.SetProfile(a.GeneratedName(), "dev", config.AllowWrite); err != nil {
		t.Fatal(err)
	}
	d.reloadHistory()
	pd := openProfile(t, d, 0)
	if pd.fName.Value() != "dev" || pd.allow() != config.AllowWrite {
		t.Fatalf("pre-fill %q/%q, want dev/write", pd.fName.Value(), pd.allow())
	}
	pd.fName.SetValue("")
	pd.HandleKey(key(tcell.KeyEnter))
	if c := d.app.cfg.Connections[0]; c.Profile != "" || c.MaxAllow != "" {
		t.Errorf("after clearing: %q/%q", c.Profile, c.MaxAllow)
	}
	if label := matchLabel(d.historyConns[0]); label != "a,1433,,sa" {
		t.Errorf("label %q", label)
	}
}

func TestConnectProfileIsGatedWithoutASelection(t *testing.T) {
	d := twoPaneConnectDialog(t)
	if !d.buttonsDisabled()[connectBtnProfile] {
		t.Fatal("Profile... is live with no History row")
	}
	d.btnFocus = connectBtnProfile
	d.doButton()
	if d.app.profileDialog().Visible() {
		t.Error("a gated Profile... opened the dialog")
	}
}

// The button row is reachable by Tab, past either end of the ring.
func TestProfileDialogButtonRowByKeyboard(t *testing.T) {
	scratchConfigHome(t)
	d := twoPaneConnectDialog(t, config.Connection{Server: "a", User: "sa"})
	pd := openProfile(t, d, 0)
	typeProfileName(pd, "x")
	pd.HandleKey(key(tcell.KeyTab))
	pd.HandleKey(key(tcell.KeyTab))
	if !pd.onButtons() || pd.btnFocus != 0 {
		t.Fatalf("two Tabs: onButtons %v btn %d, want OK", pd.onButtons(), pd.btnFocus)
	}
	pd.HandleKey(key(tcell.KeyRight))
	pd.HandleKey(key(tcell.KeyEnter)) // Cancel
	if pd.Visible() || d.app.cfg.Connections[0].Profile != "" {
		t.Errorf("Cancel: visible %v, profile %q", pd.Visible(), d.app.cfg.Connections[0].Profile)
	}
}

// The Profile dialog takes text, so it hosts the clipboard; on a button it
// answers an explicit nil.
func TestProfileDialogClipboardTarget(t *testing.T) {
	pd := NewProfileDialog()
	pd.ShowProfile(config.Connection{Server: "a"}, nil)
	if pd.FocusedClipboardTarget() == nil {
		t.Error("no clipboard target on the name field")
	}
	pd.HandleKey(key(tcell.KeyBacktab))
	if pd.FocusedClipboardTarget() != nil {
		t.Error("a clipboard target on the button row")
	}
}

// showProfileOnScreen opens a Profile dialog drawn once on a screen, so its
// widgets have their bounds, and counts the accepts.
func showProfileOnScreen(t *testing.T) (*ProfileDialog, *tuikittest.Screen, *int) {
	t.Helper()
	pd := NewProfileDialog()
	scr := tuikittest.NewScreen(100, 40)
	pd.SetScreen(scr)
	accepts := 0
	pd.ShowProfile(config.Connection{Server: "a"}, func(string, config.AllowClass) error {
		accepts++
		return nil
	})
	pd.Draw(scr)
	return pd, scr, &accepts
}

func mouseAt(d interface{ HandleMouse(*tcell.EventMouse) bool }, x, y int, b tcell.ButtonMask) bool {
	return d.HandleMouse(tcell.NewEventMouse(x, y, b, tcell.ModNone))
}

// A click on Allowed Operations opens its list and leaves it open. The click
// opened it and then focused the drop-down, and focusing blurred every entry
// first, the target too: DropDown.Focus(false) closed the list again.
func TestProfileDialogClickOpensTheAllowedOperationsList(t *testing.T) {
	pd, _, _ := showProfileOnScreen(t)
	b := pd.ddAllow.Bounds()
	mouseAt(pd, b.Right()-2, b.Y, tcell.Button1)
	mouseAt(pd, b.Right()-2, b.Y, tcell.ButtonNone)
	if !pd.ddAllow.OverlayActive() {
		t.Fatal("a click on the drop-down did not leave its list open")
	}
	if pd.focused() != focusable(pd.ddAllow) {
		t.Error("the click did not focus the drop-down")
	}
}

// The list overlays the button row, so a pick's held button dragged onto OK
// must not accept: the list sees the press before ConsumeOutsideClick, and
// RingMouse calls TrackPress ahead of it so the button row knows the press.
func TestProfileDialogDragFromAListPickDoesNotPressOK(t *testing.T) {
	pd, scr, accepts := showProfileOnScreen(t)
	b := pd.ddAllow.Bounds()
	mouseAt(pd, b.Right()-2, b.Y, tcell.Button1)
	mouseAt(pd, b.Right()-2, b.Y, tcell.ButtonNone)
	pd.Draw(scr)
	mouseAt(pd, b.Right()-3, b.Y+1, tcell.Button1) // the first item
	if pd.ddAllow.OverlayActive() {
		t.Fatal("the pick did not land — test premise is wrong")
	}
	okX, by := pd.ButtonRowStartX(profileButtons)+2, pd.ButtonRowY()
	mouseAt(pd, okX, by, tcell.Button1)
	if *accepts != 0 || !pd.Visible() {
		t.Fatal("a drag from a list pick pressed OK")
	}
	mouseAt(pd, okX, by, tcell.ButtonNone)
	typeProfileName(pd, "x")
	mouseAt(pd, okX, by, tcell.Button1)
	if *accepts != 1 {
		t.Error("a fresh click on OK did not accept — the test's button position is wrong")
	}
}

func TestProfileDialogDragOutOfTheNameFieldDoesNotPressOK(t *testing.T) {
	pd, _, accepts := showProfileOnScreen(t)
	typeProfileName(pd, "prod")
	r, by := pd.Rect(), pd.ButtonRowY()
	if got := dragOutOf(t, pd, pd.fName, r.Right()-2, by); got != "prod" {
		t.Errorf("selected %q, want %q — the selection froze when the drag left the field", got, "prod")
	}
	for mx := r.X; mx < r.Right(); mx++ {
		mouseAt(pd, mx, by, tcell.Button1)
	}
	if *accepts != 0 || !pd.Visible() {
		t.Error("a selection drag across the button row pressed a button")
	}
}

// The wheel inside the box is left to the host (dialogs.RingMouse), and one
// outside it is swallowed by the modal.
func TestProfileDialogLeavesTheWheelToTheHost(t *testing.T) {
	pd, _, _ := showProfileOnScreen(t)
	b := pd.fName.Bounds()
	if mouseAt(pd, b.X+1, b.Y, tcell.WheelDown) {
		t.Error("a wheel tick inside the box was spoken for")
	}
	if !mouseAt(pd, 0, 0, tcell.WheelDown) {
		t.Error("a wheel tick outside the modal box leaked")
	}
}
