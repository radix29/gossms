package tui

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// HelpDialog is the F1 help modal. It embeds dialogs.ModalDialog and
// renders a static, scrollable list of keyboard/mouse shortcuts.
type HelpDialog struct {
	dialogs.ModalDialog
	app    *App
	lines  []string // helpLines wrapped to the current text width (applySize)
	heads  []bool   // per row of lines: true when it came from a heading line
	scroll int
}

// helpMinHeight is the dialog's height floor: below it the help is a sliver
// even when the terminal has room (it is clamped to the screen regardless).
const helpMinHeight = 28

// NewHelpDialog creates the help dialog.
func NewHelpDialog(app *App) *HelpDialog {
	d := &HelpDialog{app: app}
	d.InitModal(app.screen, "goSSMS Help", 62, helpMinHeight)
	d.applySize()
	return d
}

// Show re-fits the dialog to the terminal, then shows it.
func (d *HelpDialog) Show() {
	d.applySize()
	d.ModalDialog.Show()
}

// Relayout re-fits the dialog to a resized terminal: ModalDialog.Relayout
// only recentres at the size last requested.
func (d *HelpDialog) Relayout() { d.applySize() }

// applySize sizes the dialog to its widest help line, or to the screen when
// that is narrower, and wraps helpLines to the text width that leaves. A fixed
// 62-column box with no horizontal scroll cut every line over 57 columns (B20);
// wrapping at the drawn width means no tail is ever lost, whatever the
// terminal. The height grows with the screen down to helpMinHeight.
func (d *HelpDialog) applySize() {
	textW := 0
	for _, l := range helpLines {
		textW = max(textW, core.DisplayWidth(l))
	}
	w, sh := textW+4, 0 // border + one column of padding on each side
	if d.app != nil && d.app.screen != nil {
		var sw int
		sw, sh = d.app.screen.Size()
		w = min(w, max(sw-2, 0))
	}
	d.lines, d.heads = wrapHelpLines(helpLines, w-4)
	h := len(d.lines) + 5 // borders, top padding row, Close button row and the row under it
	if sh > 0 {
		h = min(h, max(sh-2, helpMinHeight))
	}
	d.SetSize(w, h)
	d.scroll = max(0, min(d.scroll, len(d.lines)-d.dataH()))
}

// dataH is the number of help rows drawn: those between the top padding row
// and the Close button row (ButtonRowY). It was InnerRect().H-2, one too many,
// so the button row overdrew the last help row and the final line of the help
// could never be scrolled into view.
func (d *HelpDialog) dataH() int { return d.ButtonRowY() - d.InnerRect().Y - 1 }

// wrapHelpLines wraps each help line wider than w at a space, keeping its own
// spacing (core.WrapText collapses it, which would scramble the key/description
// columns). A continuation hangs under the description: the column after the
// first run of two or more spaces past the line's indent, else the indent
// itself, so a wrapped entry still reads as one entry under its key.
//
// heads marks each output row whose source line is a heading (isHelpHeading).
// Draw styles a row by it rather than by the row's own first column: a
// heading's continuation hangs at column 0 like an unindented line, but a
// heading that hung under a two-space gap would start with a space and lose
// its style, and a row is a heading only because its source line is.
func wrapHelpLines(lines []string, w int) (out []string, heads []bool) {
	out = make([]string, 0, len(lines))
	heads = make([]bool, 0, len(lines))
	for _, line := range lines {
		head := isHelpHeading(line)
		if w <= 0 {
			out, heads = append(out, line), append(heads, head)
			continue
		}
		hang := helpHang(line)
		if hang > w/2 {
			hang = min(helpIndent(line), w/2)
		}
		for core.DisplayWidth(line) > w {
			first, rest := splitHelpLine(line, w, hang)
			out, heads = append(out, first), append(heads, head)
			line = strings.Repeat(" ", hang) + rest
		}
		out, heads = append(out, line), append(heads, head)
	}
	return out, heads
}

// isHelpHeading reports whether a helpLines entry is a heading: anything not
// indented and not blank. Entries are indented; headings and their underlines
// start at column 0.
func isHelpHeading(line string) bool { return line != "" && line[0] != ' ' }

// splitHelpLine cuts line into a head of at most w columns and the rest,
// breaking at the last space that fits beyond the hang (so the continuation
// always makes progress), or mid-word when no such space exists. The head is
// right-trimmed and the rest left-trimmed of spaces.
func splitHelpLine(line string, w, hang int) (head, rest string) {
	cut, col := -1, 0
	for i, r := range line {
		rw := core.DisplayWidth(string(r))
		if col+rw > w {
			if cut < 0 {
				cut = i
			}
			break
		}
		if r == ' ' && col > hang {
			cut = i
		}
		col += rw
	}
	if cut <= 0 {
		cut = len(line)
	}
	return strings.TrimRight(line[:cut], " "), strings.TrimLeft(line[cut:], " ")
}

// helpIndent is the number of leading spaces in line.
func helpIndent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// helpHang is the column a wrapped line continues at: just past the first run
// of two or more spaces after the indent (the key/description gap), or the
// indent when the line has no such gap.
func helpHang(line string) int {
	ind := helpIndent(line)
	if gap := strings.Index(line[ind:], "  "); gap >= 0 {
		start := ind + gap
		return core.DisplayWidth(line[:start]) + helpIndent(line[start:])
	}
	return ind
}

var helpLines = []string{
	"goSSMS - Go SQL Server Management Studio",
	"",
	"KEYBOARD SHORTCUTS",
	"------------------",
	"",
	"Global",
	"  F1          Show this help",
	"  F10         Activate menu bar",
	"  (in menu)   Left/Right switch menus, Up/Down select an item,",
	"              Enter activates, Escape/F10 closes",
	"  Ctrl+Q      Quit",
	"  Click status bar  Show message history",
	"  F9          Connect to server",
	"  Ctrl+Shift+O Connect too, on terminals that encode it",
	"  Ctrl+O      Open a .sql file as a new query, or a .sqlplan as a plan",
	"  Ctrl+N      New query panel",
	"  Ctrl+W      Close current query",
	"  Ctrl+S      Save query (a plan panel saves its .sqlplan)",
	"  Ctrl+C/X/V  Copy / cut / paste (editor and dialog fields)",
	"  Ctrl+Space  Open context menu (keyboard right-click equivalent)",
	"  Alt+P       XEvent Profiler: start the Standard trace and watch it",
	"              (Tools > XEvent Profiler also has TSQL)",
	"",
	"Object Explorer",
	"  Arrow keys  Navigate tree",
	"  Enter/+     Expand node",
	"  -/Backspace Collapse node",
	"  F5          Refresh node",
	"  Right click Context menu (also Shift+F10, Menu key, Ctrl+Space)",
	"  Ctrl+Left   Shrink explorer",
	"  Ctrl+Right  Grow explorer",
	"",
	"Query Editor",
	"  F5          Execute (runs only the selection, if any)",
	"  Shift+Arrow Select text with the keyboard",
	"  Click+drag  Select text with the mouse",
	"  Ctrl+Z       Undo",
	"  Ctrl+Y       Redo",
	"  Ctrl+Up     Grow editor / shrink results",
	"  Ctrl+Down   Shrink editor / grow results",
	"  Ctrl+PgUp/PgDn  Previous / next result tab (grids and Messages)",
	"  Alt+Z       Word wrap on/off (display only)",
	"",
	"Execution Plan",
	"  1/2/3       Plan graph / operator tree / raw XML",
	"  [ / ]       Previous / next statement of the batch",
	"  m           Missing-index details (when the banner is showing)",
	"  / , n/N     Search operators, next / previous match",
	"  File > Save Execution Plan As...  writes the .sqlplan",
	"  Query menu  Live Query Statistics (toolbar Live):",
	"              Execute shows the running statement's plan",
	"              live — rows of estimate, elapsed, progress —",
	"              and it becomes the actual plan at the end.",
	"              Turns Actual Execution Plan on; needs",
	"              VIEW SERVER STATE",
	"",
	"IntelliSense (autocomplete)",
	"  (as you type) Suggests schemas/tables/views/columns, incl. \"sys.\" — opens once",
	"              a word starts (a letter or '['); space/'.'/digits never open it",
	"  Ctrl+Space  Open/force suggestions (auto-completes a started word with",
	"              one match; with nothing typed, always shows the list)",
	"  (filtering) Matches anywhere in a name, names starting with it first;",
	"              a list of only mid-name matches opens unselected (Down picks)",
	"  Tab/Enter   Accept the selected suggestion",
	"  Up/Down, PgUp/PgDn  Move the selection",
	"  Escape      Dismiss (won't reopen for that word until you move on)",
	"  Ctrl+R      Refresh the cached table/column list for this database",
	"  (Tools > Options has an on/off toggle for this feature)",
	"",
	"Connect Dialog",
	"  Tab         Move focus: History -> the visible tab's controls",
	"              -> buttons",
	"  Up/Down     Pick a saved connection in History (fills the form)",
	"  Enter       In History, connect to the highlighted connection",
	"  Ctrl+PgUp/PgDn  Connection Properties / Connection String tab",
	"  Left/Right  On the buttons, move along Delete, Reset, Connect,",
	"              Cancel (Reset clears the form; Delete, set apart at",
	"              the left, removes the highlighted saved connection",
	"              after confirming)",
	"  Esc         Close; while connecting, cancel the attempt",
	"",
	"Back Up / Restore Database",
	"  Tab         Move focus: fields -> buttons",
	"  Left/Right  On the buttons, move along them; Enter presses one",
	"  Enter       In a field, Start Backup / Analyze",
	"  Esc         Close (Restore's Backup Information and File",
	"              Locations views: back to the form)",
	"",
	"Properties Dialogs",
	"  Tab         Move focus: page list -> page -> buttons",
	"  F5          Reload the current page from the server",
	"  Ctrl+Z      Revert this page to its loaded values",
	"  Ctrl+C      Copy the focused row's value",
	"  Esc         Close; while OK/Apply/Script Changes runs, stop it",
	"              instead (so does the Cancel button)",
	"  Read-only   On a read-only page Tab reaches only its grids: they",
	"              scroll and select (the rows beside them follow) but",
	"              edit nothing",
	"",
	"Progress Dialog (a confirmed Delete, Rename, Offline, failover, ...)",
	"  Esc/Enter   Cancel the statement running; the dialog stays up until",
	"              the server has stopped it. Greyed on a failover or a",
	"              revert to a snapshot, which cannot be safely interrupted",
	"",
	"Focus / Panel Tabs",
	"  Ctrl+Tab         Cycle focus: Explorer -> Query Editor -> Results",
	"  Ctrl+Shift+Tab   Cycle focus in reverse: Explorer -> Results -> Editor",
	"  Ctrl+Shift+Right Next panel/tab (Query N, Object Explorer Details, ...)",
	"  Ctrl+Shift+Left  Previous panel/tab",
	"  Ctrl+0..9        Jump to panel N, counted from the left (0 = Object",
	"                   Explorer Details); only while a panel has focus",
	"  Some terminals reserve Ctrl+Tab/Ctrl+Shift+Tab for their own tab",
	"  switching and never forward it — Ctrl+Shift+Left/Right and Ctrl+0..9",
	"  are the reliable fallback on those.",
	"  The currently focused pane's title/header bar is highlighted.",
	"",
	"Extended Events viewer (Watch Live Data / View Target Data)",
	"  F5          Start the data feed (live) / re-read the target",
	"  F6          Pause / resume the grid (the feed keeps reading)",
	"  F7          Edit the filter: column op value [AND|OR ...], or text",
	"  F8          Choose columns (remembered per session)",
	"  Ctrl+F      Find an event containing text; F3 / Shift+F3 next / previous",
	"  Ctrl+F2     Bookmark the event; F2 / Shift+F2 next / previous bookmark",
	"  Enter/Space Open or close a group row (Grouping ▾); Right opens, Left closes",
	"  Alt+Up/Down Scroll the event details pane",
	"  Right-click Open SQL in a query window, Show Plan, open XML,",
	"              Filter by This Value, Group by This Column, Bookmark,",
	"              Copy Event Details, Copy Rows with Headers; on a group",
	"              row, Filter by This Group (its whole path of groups)",
	"  Toolbar     Grouping, Aggregation (on group rows, under its column),",
	"              Settings (save / apply a layout), Export (CSV,",
	"              tab-separated, INSERT script)",
	"",
	"Query Store / Plan Compare",
	"  Tab         Move focus between the panel's grids",
	"  F5          Re-read the report",
	"  Toolbar     Force/Unforce Plan, Show Plan, Script, Track Query,",
	"              Compare Plans; a row too narrow shows them under More",
	"  [ / ]       Plan Compare: plan A's previous / next statement",
	"  { / }       Plan Compare: plan B's previous / next statement",
	"              (or click the A: / B: statement pickers)",
	"  Enter       Plan Compare: open the operator's plan at it — plan B",
	"              when the cursor is on a B column (or right-click the row)",
	"  Query menu  Compare Showplan... (against a .sqlplan) and Compare",
	"              with ▸ (another open plan); also on a right-click in a",
	"              plan's graph or tree",
	"",
	"Replication Monitor (Tools menu, or Launch Replication Monitor on",
	"the Replication folders and a publication)",
	"  Tab         Move focus: publications, subscriptions and agents,",
	"              the agent's sessions, the session's actions",
	"  F5          Re-read everything now",
	"  Ctrl+Up/Dn  Resize the panes",
	"  Toolbar     Auto refresh rate (or Off), History window, Failed",
	"              sessions only",
	"  Show Value  On an action's Error details cell: the whole error",
	"",
	"MOUSE",
	"-----",
	"  Click       Select / focus",
	"  Dbl-click   Open / expand",
	"  Right-click Context menu",
	"  Ctrl+click  Add/remove one grid row from the selection",
	"  Shift+click Extend the grid selection (Alt+click does the same, for",
	"              terminals that keep Shift for their own text selection)",
	"  Drag splitter to resize panels",
	"  Scroll wheel in tree / results",
	"  Help > Key Diagnostics logs mouse events too, which is how a",
	"  modifier the terminal kept for itself is told from a wrong binding.",
}

// Draw renders the help dialog.
func (d *HelpDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	contentStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	headStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Bold(true)

	inner := d.InnerRect()
	dataH := d.dataH()

	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(d.lines) {
			break
		}
		line := d.lines[idx]
		st := contentStyle
		if d.heads[idx] {
			st = headStyle
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: inner.Y + 1 + row, W: inner.W, H: 1}, ' ', contentStyle)
		core.DrawTextClipped(s, inner.X+1, inner.Y+1+row, inner.W-2, st, line)
	}

	if len(d.lines) > dataH {
		d.DrawContentScrollbar(s, inner.Y+1, dataH, len(d.lines), d.scroll)
	}

	d.DrawButtons(s, []string{"Close"}, 0)
}

// HandleKey processes keyboard events.
func (d *HelpDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	dataH := d.dataH()
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
	case tcell.KeyUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.KeyDown:
		if d.scroll+dataH < len(d.lines) {
			d.scroll++
		}
	case tcell.KeyPgUp:
		d.scroll = max(0, d.scroll-dataH)
	case tcell.KeyPgDn:
		d.scroll = max(0, min(len(d.lines)-dataH, d.scroll+dataH))
	}
	return true
}

// HandleMouse handles mouse events.
func (d *HelpDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"Close"}) == 0 {
		d.Hide()
		return true
	}
	dataH := d.dataH()
	if d.ScrollbarDrag(ev, d.Rect().Right()-1, d.InnerRect().Y+1, dataH, len(d.lines), &d.scroll) {
		return true
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		if d.scroll+dataH < len(d.lines) {
			d.scroll++
		}
	}
	return true
}
