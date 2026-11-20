package tui

import (
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
)

// UpdateDialog shows the result of Help > Check for Updates: the installed
// version, the latest version published on GitHub, and a link to the
// releases page.
type UpdateDialog struct {
	dialogs.MessageDialog
	app     *App
	lines   []string
	linkRow int // index into lines that gets the "link" style, -1 = none
	// check is the GitHub request in flight (App.checkForUpdates); a newer
	// showing's supersedes it.
	check latest
}

// NewUpdateDialog creates the Check for Updates dialog.
func NewUpdateDialog(app *App) *UpdateDialog {
	d := &UpdateDialog{app: app}
	// H=15, two rows more than a single-line message needs: the dev-build
	// result (see ShowResult) spans two lines, and at H=13 the link row
	// lands on the same screen row as DrawSeparator's line, which is drawn
	// after the content and paints over it.
	d.InitMessage(dialogs.MessageDialogSpec{
		Title: "Check for Updates", W: 74, H: 15,
		OnButton: func(i int) { d.activateButton(d.Buttons()[i]) },
	})
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
	d.sync(0)
	d.MessageDialog.Show()
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
		d.sync(0)
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
	d.sync(len(d.buttonLabels()) - 1)
}

// sync hands d.lines, the link row's style and the button row to the
// MessageDialog, focusing button focus.
func (d *UpdateDialog) sync(focus int) {
	ml := make([]dialogs.MessageLine, len(d.lines))
	for i, l := range d.lines {
		ml[i] = dialogs.MessageLine{Text: l}
		if i == d.linkRow {
			ml[i].Style = theme.StyleDialogAccent().Underline(true)
		}
	}
	d.SetLines(ml)
	d.SetButtons(d.buttonLabels(), focus)
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
