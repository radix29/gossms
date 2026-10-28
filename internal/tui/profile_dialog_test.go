package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
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
	pd := d.app.profileDialog
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
	// rather than pressing OK, and the next Enter is OK.
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
	if d.app.profileDialog.Visible() {
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
	if !pd.onButtons || pd.btnFocus != 0 {
		t.Fatalf("two Tabs: onButtons %v btn %d, want OK", pd.onButtons, pd.btnFocus)
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
	pd := NewProfileDialog(nil)
	pd.ShowProfile(config.Connection{Server: "a"}, nil)
	if pd.FocusedClipboardTarget() == nil {
		t.Error("no clipboard target on the name field")
	}
	pd.HandleKey(key(tcell.KeyBacktab))
	if pd.FocusedClipboardTarget() != nil {
		t.Error("a clipboard target on the button row")
	}
}
