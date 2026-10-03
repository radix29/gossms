package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

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

// Put is how core draws a cell; it lands in SetContent like any other write.
func (s *recordingScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	r, n := utf8.DecodeRuneInString(str)
	s.SetContent(x, y, r, nil, style)
	return str[n:], 1
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
	waitForSavesAtCleanup(t)
}

// waitForSavesAtCleanup holds the test's end until every background save
// has finished. Registered after the scratch directory, so it runs first: a
// save still writing there makes TempDir's RemoveAll fail the test.
func waitForSavesAtCleanup(t *testing.T) {
	t.Cleanup(savesInFlight.Wait)
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

// The button row is the Tab stop past either end of the ring, crossed with
// Left/Right. It replaced F1, which cycled the buttons from anywhere in the form
// and was the only keyboard way to reach them — F1 is Help everywhere else.
func TestConnectTabReachesTheButtonRow(t *testing.T) {
	d := twoPaneConnectDialog(t, config.Connection{Server: "srv1", User: "sa"})
	key := func(k tcell.Key) { d.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) }

	// Backtab from History, the ring's first stop.
	if d.focusedWidget() != d.history {
		t.Fatal("setup: the dialog did not open on History")
	}
	key(tcell.KeyBacktab)
	if !d.onButtons || d.btnFocus != connectBtnConnect {
		t.Fatalf("Backtab from History: onButtons=%v btnFocus=%d, want the row on Connect", d.onButtons, d.btnFocus)
	}
	if d.FocusedClipboardTarget() != nil {
		t.Error("a field still answers Copy/Paste while the buttons have focus")
	}
	server := d.fServer.Value()
	d.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
	if d.fServer.Value() != server {
		t.Error("a letter typed on the button row edited a field")
	}

	seen := map[int]bool{d.btnFocus: true}
	for range connectButtonCount {
		key(tcell.KeyLeft)
		seen[d.btnFocus] = true
	}
	if d.btnFocus != connectBtnDelete {
		t.Errorf("Left stopped on button %d, want Delete at the left end", d.btnFocus)
	}
	for range connectButtonCount {
		key(tcell.KeyRight)
		seen[d.btnFocus] = true
	}
	if d.btnFocus != connectBtnCancel || len(seen) != connectButtonCount {
		t.Errorf("Right ended on %d having reached %d of %d buttons", d.btnFocus, len(seen), connectButtonCount)
	}

	// Tab off the row lands on the ring's first stop, with Connect — what Enter
	// in a field fires — highlighted again.
	key(tcell.KeyTab)
	if d.onButtons || d.focusedWidget() != d.history || d.btnFocus != connectBtnConnect {
		t.Errorf("Tab off the row: onButtons=%v focused=%T btnFocus=%d", d.onButtons, d.focusedWidget(), d.btnFocus)
	}

	// F1 no longer moves the highlight.
	key(tcell.KeyF1)
	if d.btnFocus != connectBtnConnect || d.onButtons {
		t.Errorf("F1 moved the button focus to %d", d.btnFocus)
	}
}

// Left/Right step over a gated button, so the highlight never rests on one
// Enter would refuse.
func TestConnectButtonRowSkipsGatedButtons(t *testing.T) {
	d := twoPaneConnectDialog(t) // no history: Delete is gated
	if !d.buttonsDisabled()[connectBtnDelete] {
		t.Fatal("setup: Delete is live with nothing to delete")
	}
	d.setFocus(0)
	d.stepFocus(-1) // back past the first stop is the row
	if !d.onButtons {
		t.Fatal("Backtab from the first stop did not reach the button row")
	}
	d.btnFocus = connectBtnReset
	d.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	if d.btnFocus != connectBtnReset {
		t.Errorf("Left from Reset moved to %d, onto gated Delete", d.btnFocus)
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
