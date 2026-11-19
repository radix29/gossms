package tui

import (
	"time"

	"github.com/radix29/tuikit/dialogs"
)

// maxStatusHistoryLines caps the retained history at the last 256 messages;
// oldest entries are dropped first.
const maxStatusHistoryLines = 256

// StatusHistoryDialog is a read-only modal listing every status-bar/log message
// recorded this session, newest first, each prefixed with its timestamp. The
// log mechanics (a selectable, line-numbered read-only editor, prepend-only
// sync, Record from any goroutine) are tuikit's dialogs.LogDialog;
// tuikit's dialogs.KeyDiagnosticsDialog is the other one.
//
// Unlike KeyDiagnosticsDialog, Show() does not reset the log: history
// accumulates across the session, in memory only, with no persistence.
//
// Record is called from background goroutines (App.logStatus reaches it from
// the Object Explorer's loader goroutine); LogDialog.Record is safe for that.
type StatusHistoryDialog struct {
	*dialogs.LogDialog
}

// NewStatusHistoryDialog creates the status history dialog.
func NewStatusHistoryDialog(app *App) *StatusHistoryDialog {
	return &StatusHistoryDialog{dialogs.NewLogDialog(dialogs.LogDialogSpec{
		Title: "Status History", W: 90, H: 26,
		Max:    maxStatusHistoryLines,
		Empty:  "No messages recorded yet this session.",
		Gutter: true,
	})}
}

// Record prepends a timestamped line to the history. Safe from any goroutine.
func (d *StatusHistoryDialog) Record(msg string) {
	d.LogDialog.Record(time.Now().Format("15:04:05") + "  " + msg)
}
