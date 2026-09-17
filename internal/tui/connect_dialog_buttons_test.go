package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// recordingScreen keeps what was painted, which fakeSizedScreen throws away.
type recordingScreen struct {
	tcell.Screen
	w, h  int
	runes map[[2]int]rune
}

func (s *recordingScreen) Size() (int, int) { return s.w, s.h }
func (s *recordingScreen) SetContent(x, y int, primary rune, _ []rune, _ tcell.Style) {
	s.runes[[2]int{x, y}] = primary
}
func (s *recordingScreen) ShowCursor(int, int) {}

// drawnFieldText reads back the text a field actually painted into its box —
// what the user sees, as opposed to what Value() reports.
func drawnFieldText(f *widgets.InputField) string {
	s := &recordingScreen{w: 200, h: 50, runes: map[[2]int]rune{}}
	f.Draw(s)
	var b strings.Builder
	for x := f.InputX() + 1; x < f.InputX()+1+f.Width(); x++ {
		if r, ok := s.runes[[2]int{x, f.RectY()}]; ok && r != 0 {
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// scratchConfigHome points config.Save at a temporary directory. Every test
// here saves, and Save writes to the real os.UserConfigDir otherwise — a test
// run would delete the user's own saved connections.
func scratchConfigHome(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// Reset clears the right pane back to the dialog's opening defaults, and
// leaves History alone: the saved connections are not what it clears.
func TestConnectResetClearsTheFormAndKeepsHistory(t *testing.T) {
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "srv", Database: "master", User: "sa", Password: "p",
			RememberPassword: true, AuthMethod: config.AuthEntraPassword},
	)
	d.fExtraProps.SetText("Application Name=x")
	d.cbTrust.SetChecked(false)
	d.setEncryptMode(config.EncryptOptional)

	d.btnFocus = connectBtnReset
	d.doButton()

	if v := d.fServer.Value(); v != "" {
		t.Errorf("Server Name = %q after Reset, want empty", v)
	}
	for name, got := range map[string]string{
		"Database Name":            d.fDatabase.Value(),
		"User Name":                d.fUser.Value(),
		"Password":                 d.fPassword.Value(),
		"Tenant ID":                d.fTenantID.Value(),
		"Client ID":                d.fClientID.Value(),
		"Host Name In Certificate": d.fHostCert.Value(),
		"Custom Properties":        d.fExtraProps.Text(),
	} {
		if got != "" {
			t.Errorf("%s = %q after Reset, want empty", name, got)
		}
	}
	if d.cbRemember.Checked() {
		t.Error("Remember Password stayed ticked after Reset")
	}
	if !d.cbTrust.Checked() {
		t.Error("Trust Server Certificate is not back on after Reset")
	}
	if got := d.authMethod(); got != config.AuthSQLServer {
		t.Errorf("Authentication = %v after Reset, want SQL Server", got)
	}
	if got := config.AllEncryptModes()[d.ddEncrypt.Selected()]; got != config.EncryptMandatory {
		t.Errorf("Encrypt = %v after Reset, want Mandatory", got)
	}
	if len(d.historyConns) != 1 {
		t.Errorf("Reset changed History: %d rows, want 1", len(d.historyConns))
	}
	// An emptied Server field is what Show reads to pre-fill, so a reset
	// dialog reopens on the most recent connection rather than staying blank.
	d.Hide()
	d.Show()
	if d.fServer.Value() != "srv" {
		t.Errorf("reopening after Reset left Server Name %q, want the most recent connection",
			d.fServer.Value())
	}
}

// Delete asks before removing, and No leaves the saved connections alone.
func TestConnectDeleteAsksFirst(t *testing.T) {
	scratchConfigHome(t)
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "keep", User: "sa"},
		config.Connection{Server: "drop", User: "sa"},
	)
	d.btnFocus = connectBtnDelete
	d.doButton()
	if !d.app.confirmDialog.Visible() {
		t.Fatal("Delete removed the connection without asking")
	}
	// Escape answers No.
	d.app.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if len(d.app.cfg.Connections) != 2 {
		t.Fatalf("answering No deleted anyway: %d connections left, want 2", len(d.app.cfg.Connections))
	}

	d.btnFocus = connectBtnDelete
	d.doButton()
	answerConfirm(t, d.app, false)

	if len(d.app.cfg.Connections) != 1 || d.app.cfg.Connections[0].Server != "keep" {
		t.Fatalf("after Yes the saved connections are %+v, want only \"keep\"", d.app.cfg.Connections)
	}
	// The pane and the form both follow: the deleted row was the selected one.
	if len(d.historyConns) != 1 || d.historyConns[0].Server != "keep" {
		t.Errorf("History still lists %+v", d.historyConns)
	}
	if d.fServer.Value() != "keep" {
		t.Errorf("right pane shows %q after the delete, want the row that took its place", d.fServer.Value())
	}
}

// Deleting the last saved connection empties the form rather than leaving it
// showing a connection that no longer exists.
func TestConnectDeleteOfTheLastRowClearsTheForm(t *testing.T) {
	scratchConfigHome(t)
	d := twoPaneConnectDialog(t, config.Connection{Server: "only", User: "sa"})
	d.btnFocus = connectBtnDelete
	d.doButton()
	answerConfirm(t, d.app, false)

	if len(d.historyConns) != 0 {
		t.Fatalf("History still holds %d rows", len(d.historyConns))
	}
	if d.fServer.Value() != "" {
		t.Errorf("Server Name = %q after deleting the last connection, want empty", d.fServer.Value())
	}
	if d.history.Selected() >= 0 {
		t.Errorf("History selection = %d on an empty list, want -1", d.history.Selected())
	}
	if !d.buttonsDisabled()[connectBtnDelete] {
		t.Error("Delete is still live with nothing left to delete")
	}
}

// Every button but Cancel refuses to act while it is drawn gated — otherwise a
// greyed button quietly works anyway, which is worse than a dead one.
func TestConnectGatedButtonsDoNothing(t *testing.T) {
	d := twoPaneConnectDialog(t)
	if !d.buttonsDisabled()[connectBtnConnect] {
		t.Fatal("Connect is not gated on an empty Server Name")
	}
	d.btnFocus = connectBtnConnect
	d.doButton()
	if d.connecting {
		t.Error("Connect dialled with no server name")
	}
	d.btnFocus = connectBtnDelete
	d.doButton()
	if d.app.confirmDialog.Visible() {
		t.Error("Delete asked about a connection that isn't there")
	}
}

// F1 cycles the whole button row, not the two buttons it had before Reset and
// Delete joined it.
func TestConnectF1CyclesEveryButton(t *testing.T) {
	d := twoPaneConnectDialog(t)
	seen := map[int]bool{}
	for range connectButtonCount {
		seen[d.btnFocus] = true
		d.HandleKey(tcell.NewEventKey(tcell.KeyF1, "", tcell.ModNone))
	}
	if len(seen) != connectButtonCount {
		t.Errorf("F1 reached %d of %d buttons", len(seen), connectButtonCount)
	}
	if d.btnFocus != connectBtnConnect {
		t.Errorf("a full cycle ended on button %d, want back on Connect", d.btnFocus)
	}
}

// The highlighted History row and the right pane must agree on a reopened
// dialog: the list survives Hide, and a connection made in between renumbers
// every row.
func TestConnectHistoryHighlightFollowsTheForm(t *testing.T) {
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "oldest", User: "sa"},
		config.Connection{Server: "middle", User: "sa"},
		config.Connection{Server: "newest", User: "sa"},
	)
	// Row 2 is "oldest"; pick it, then reopen with the form still on it.
	d.history.SetSelected(2)
	d.applyHistory(2)
	d.Hide()
	d.Show()
	if got := d.history.Selected(); got != 2 {
		t.Errorf("History highlight = row %d, want the row the form shows (2)", got)
	}
	if d.fServer.Value() != "oldest" {
		t.Errorf("right pane = %q, want oldest", d.fServer.Value())
	}
}

// A connection picked from History is identified by the head of its server
// name, so the pre-filled fields read from their first column — SetValue alone
// leaves a value longer than the box showing its tail.
func TestConnectPreFillReadsFromTheStart(t *testing.T) {
	long := "a-very-long-server-name.corp.example.com"
	d := twoPaneConnectDialog(t, config.Connection{
		Server: long, Port: 1433, User: "sa", Database: "AdventureWorks2022",
	})
	if got := drawnFieldText(d.fServer); got == "" || !strings.HasPrefix(long, got) {
		t.Errorf("Server Name shows %q, want the start of %q", got, long)
	}
	if d.fServer.Value() != long {
		t.Errorf("Server Name value = %q, want %q", d.fServer.Value(), long)
	}
}
