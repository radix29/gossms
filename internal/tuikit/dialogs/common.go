package dialogs

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// messageBoxOverhead is how much narrower a message's wrapped content is
// than the dialog around it: DrawBase's 1-cell border on each side (via
// InnerRect) plus the 1-cell left/right margin every message-driven
// dialog in this package leaves before its own text (see AlertDialog.Draw
// et al. — text starts at inner.X+1 and is clipped to inner.W-2).
const messageBoxOverhead = 4

// maxMessageWidthNum/maxMessageWidthDen cap a text-driven dialog at 2/3 of
// the screen's width — wide enough to comfortably show most messages on
// one line, without ever stretching a dialog almost edge-to-edge for a
// single long sentence.
const maxMessageWidthNum, maxMessageWidthDen = 2, 3

// fitMessage sizes a dialog to its message: wide enough to show it on one
// line when that fits within 2/3 of the screen's width, word-wrapped onto
// more lines instead of growing past that cap when it doesn't. minW is the
// floor the dialog is never narrower than (room for the title and button
// row); baseH is the dialog's total height with the message on a single
// line. The line count is further capped so the dialog's total height
// never exceeds the screen — recentre() would otherwise clamp rect.H
// without shrinking the message itself, drawing the tail of a long,
// heavily-wrapped message over the separator/button row on a short
// terminal — dropping any lines that don't fit and ellipsizing the last
// one kept. Returns the dialog size to pass to SetSize and the message
// split into the lines the caller draws one per row.
func (d *ModalDialog) fitMessage(message string, minW, baseH int) (w, h int, lines []string) {
	w = core.Max(minW, core.DisplayWidth(message)+messageBoxOverhead)
	if d.screen != nil {
		if sw, _ := d.screen.Size(); sw > 0 {
			if maxW := sw * maxMessageWidthNum / maxMessageWidthDen; w > maxW {
				w = core.Max(minW, maxW)
			}
		}
	}
	contentW := w - messageBoxOverhead
	lines = core.WrapText(message, contentW)

	if d.screen != nil {
		if _, sh := d.screen.Size(); sh > 0 {
			if maxLines := core.Max(1, sh-baseH+1); maxLines < len(lines) {
				remainder := strings.Join(lines[maxLines-1:], " ")
				lines = append(lines[:maxLines-1], core.Truncate(remainder, contentW))
			}
		}
	}
	return w, baseH + len(lines) - 1, lines
}
