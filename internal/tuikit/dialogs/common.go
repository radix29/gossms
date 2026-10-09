package dialogs

import (
	"github.com/radix29/gossms/internal/tuikit/core"
)

// messageBoxOverhead is how much narrower a message's wrapped content is than
// the dialog: DrawBase's 1-cell border each side plus the 1-cell margin every
// message-driven dialog leaves (text starts at inner.X+1, clipped to inner.W-2).
const messageBoxOverhead = 4

// maxMessageWidthNum/maxMessageWidthDen cap a text-driven dialog at 2/3 of the
// screen width: roomy for most messages, never edge-to-edge for one long
// sentence.
const maxMessageWidthNum, maxMessageWidthDen = 2, 3

// fitMessage sizes a dialog to its message: wide enough for one line when that
// fits within 2/3 of the screen width, else word-wrapped onto more lines. minW is
// the width floor (room for title and button row); baseH is the total height
// with a one-line message. Line breaks are kept: each line is a paragraph
// wrapped on its own, and the width is the widest one's. The line count is
// capped so the dialog never exceeds the screen height (recentre would clamp
// rect.H without shrinking the message, drawing a long message's tail over the
// separator/button row); surplus lines are dropped and the last kept one
// ellipsized. Returns the size for SetSize and the lines to draw one per row.
func (d *ModalDialog) fitMessage(message string, minW, baseH int) (w, h int, lines []string) {
	w = max(minW, core.ParagraphsWidth(message)+messageBoxOverhead)
	if d.screen != nil {
		if sw, _ := d.screen.Size(); sw > 0 {
			if maxW := sw * maxMessageWidthNum / maxMessageWidthDen; w > maxW {
				w = max(minW, maxW)
			}
		}
	}
	contentW := w - messageBoxOverhead
	maxLines := 0
	if d.screen != nil {
		if _, sh := d.screen.Size(); sh > 0 {
			maxLines = max(1, sh-baseH+1)
		}
	}
	// WrapParagraphsLimit is WrapParagraphs for a message that already fits in
	// maxLines, so the cap needs no second wrap; it has no unlimited form, which the
	// screenless branch falls back to. Paragraph-aware, not WrapText: a "\n\n" is
	// the gap between a question and its consequence, which strings.Fields would
	// fold into one run-on paragraph.
	if maxLines > 0 && contentW > 0 {
		lines = core.WrapParagraphsLimit(message, contentW, maxLines)
	} else {
		lines = core.WrapParagraphs(message, contentW)
	}
	return w, baseH + len(lines) - 1, lines
}
