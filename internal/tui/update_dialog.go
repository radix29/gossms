package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// UpdateDialog shows the result of Help > Check for Updates: the installed
// version, the latest version published on GitHub, and a link to the
// releases page.
type UpdateDialog struct {
	dialogs.ModalDialog
	app      *App
	lines    []string
	linkRow  int // index into lines that gets the "link" style, -1 = none
	btnFocus int
}

// NewUpdateDialog creates the Check for Updates dialog.
func NewUpdateDialog(app *App) *UpdateDialog {
	d := &UpdateDialog{app: app}
	// H=15 (2 more than a single-line message needs): the dev-build result
	// (see ShowResult) spans two lines instead of one, and at H=13 that
	// pushed the link row down onto the same screen row as DrawSeparator's
	// line, which is drawn after the content and painted right over it —
	// the link was there but invisible. 15 leaves a two-row margin so the
	// content and the separator never land on the same row.
	d.InitModal(app.screen, "Check for Updates", 74, 15)
	return d
}

// ShowChecking opens the dialog in its loading state, before the background
// request to GitHub has come back.
func (d *UpdateDialog) ShowChecking(installed string) {
	d.lines = []string{
		"Installed version : " + installed,
		"",
		"Checking github.com/radix29/gossms for the latest release...",
	}
	d.linkRow = -1
	d.btnFocus = 0
	d.ModalDialog.Show()
}

// ShowResult replaces the loading state with the outcome of the check. rel
// is the zero value and err is non-nil when the request failed.
func (d *UpdateDialog) ShowResult(installed string, rel githubRelease, err error) {
	if err != nil {
		d.lines = []string{
			"Installed version : " + installed,
			"",
			"Could not check for updates:",
			err.Error(),
		}
		d.linkRow = -1
		d.btnFocus = 0
		return
	}

	d.lines = []string{
		"Installed version : " + installed,
		"Available version : " + rel.TagName,
		"",
	}
	switch {
	case !isReleaseVersion(installed):
		// installed is "(devel)" (a plain git clone && go build/go run, see
		// internal/version's doc comment) or some other unresolved build —
		// comparing it against a real release would read as v0.0.0 and
		// always falsely claim an update is available. Split across two
		// lines (like the request-failed branch above) — the dialog draws
		// each d.lines entry as one clipped row, not word-wrapped.
		d.lines = append(d.lines,
			"You are running a development build, not a tagged release —",
			"comparison against the latest release isn't meaningful.")
	case compareVersions(installed, rel.TagName) < 0:
		d.lines = append(d.lines, "A new version of goSSMS is available.")
	case compareVersions(installed, rel.TagName) > 0:
		d.lines = append(d.lines, "You are running a version newer than the latest published release.")
	default:
		d.lines = append(d.lines, "You are already running the latest version of goSSMS.")
	}
	d.lines = append(d.lines, "", "Here you can find the latest releases:", rel.releasesURL())
	d.linkRow = len(d.lines) - 1
	// Default focus to Close (the last button either way), not the newly
	// added Copy Link, so Enter still means "dismiss" by default.
	d.btnFocus = len(d.buttonLabels()) - 1
}

// buttonLabels returns the dialog's current button row: just Close while
// there's no link yet (checking, or the request failed), Copy Link and
// Close once a release was resolved.
func (d *UpdateDialog) buttonLabels() []string {
	if d.linkRow >= 0 {
		return []string{"Copy Link", "Close"}
	}
	return []string{"Close"}
}

// activateButton runs whichever button label was clicked or Enter-ed.
func (d *UpdateDialog) activateButton(label string) {
	switch label {
	case "Copy Link":
		d.copyLink()
	case "Close":
		d.Hide()
	}
}

// copyLink sends the releases-page URL to the clipboard, the same
// App.writeClipboard path Ctrl+C uses elsewhere (native OS clipboard tool,
// falling back to tcell's OSC 52).
func (d *UpdateDialog) copyLink() {
	if d.linkRow < 0 || d.linkRow >= len(d.lines) {
		return
	}
	d.app.writeClipboard(d.lines[d.linkRow])
	d.app.setStatus("Copied release link to clipboard")
}

// Draw renders the dialog.
func (d *UpdateDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	textStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	linkStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Underline(true)

	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the button row
	for row := 0; row < dataH && row < len(d.lines); row++ {
		st := textStyle
		if row == d.linkRow {
			st = linkStyle
		}
		core.DrawTextClipped(s, inner.X+1, inner.Y+1+row, inner.W-2, st, d.lines[row])
	}

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttonLabels(), d.btnFocus)
}

// HandleKey processes keyboard events.
func (d *UpdateDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	labels := d.buttonLabels()
	switch ev.Key() {
	case tcell.KeyEscape:
		d.Hide()
	case tcell.KeyEnter:
		d.activateButton(labels[d.btnFocus])
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % len(labels)
	case tcell.KeyBacktab, tcell.KeyLeft:
		d.btnFocus = (d.btnFocus - 1 + len(labels)) % len(labels)
	}
	return true
}

// HandleMouse processes mouse events.
func (d *UpdateDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	labels := d.buttonLabels()
	if i := d.ButtonClicked(ev, labels); i >= 0 {
		d.btnFocus = i
		d.activateButton(labels[i])
	}
	return true
}
