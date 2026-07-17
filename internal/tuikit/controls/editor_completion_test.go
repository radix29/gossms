package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// testCompletionProvider returns a CompletionProvider offering candidates
// (exact Text/Label match) whose name has, as a case-insensitive prefix,
// whatever identifier characters immediately precede the cursor — a
// minimal stand-in for internal/tui's real SQL-aware provider, exercising
// exactly the same Editor/provider contract.
func testCompletionProvider(candidates ...string) CompletionProvider {
	return func(lines [][]rune, row, col int) ([]CompletionItem, int) {
		if row >= len(lines) {
			return nil, col
		}
		line := lines[row]
		c := core.Clamp(col, 0, len(line))
		start := c
		for start > 0 && core.IsWordRune(line[start-1]) {
			start--
		}
		prefix := strings.ToLower(string(line[start:c]))
		var items []CompletionItem
		for _, cand := range candidates {
			if strings.HasPrefix(strings.ToLower(cand), prefix) {
				items = append(items, CompletionItem{Text: cand, Label: cand})
			}
		}
		return items, start
	}
}

func typeString(e *Editor, s string) {
	for _, r := range s {
		e.HandleKey(runeKey(r, tcell.ModNone))
	}
}

func TestEditorCompletionTriggersOnTyping(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers", "Orders", "Products"))

	typeString(e, "Cu")
	if !e.CompletionActive() {
		t.Fatal("expected completion popup open after typing \"Cu\"")
	}
	if len(e.completionItems) != 1 || e.completionItems[0].Text != "Customers" {
		t.Fatalf("completionItems = %+v, want [Customers]", e.completionItems)
	}
}

func TestEditorCompletionNoMatchStaysClosed(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "Zz")
	if e.CompletionActive() {
		t.Fatal("expected no popup when nothing matches the typed prefix")
	}
}

func TestEditorCompletionCommitReplacesPrefixAndUndoRestores(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "Cu")
	if !e.CompletionActive() {
		t.Fatal("expected popup open before commit")
	}

	e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected popup closed after commit")
	}
	if got := e.Text(); got != "Customers" {
		t.Fatalf("Text() after commit = %q, want %q", got, "Customers")
	}
	if e.cursorCol != len("Customers") {
		t.Fatalf("cursorCol after commit = %d, want %d", e.cursorCol, len("Customers"))
	}

	// A single Ctrl+Z must restore exactly the typed prefix, not the
	// pre-commit undo history entry from before typing even began.
	e.HandleKey(key(tcell.KeyCtrlZ, tcell.ModNone))
	if got := e.Text(); got != "Cu" {
		t.Fatalf("Text() after undo = %q, want %q", got, "Cu")
	}
}

func TestEditorCompletionTabAlsoCommits(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Products"))

	typeString(e, "Pro")
	e.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if got := e.Text(); got != "Products" {
		t.Fatalf("Text() after Tab commit = %q, want %q", got, "Products")
	}
}

func TestEditorCompletionEscapeDismissesAndSuppresses(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "Cu")
	if !e.CompletionActive() {
		t.Fatal("expected popup open")
	}

	e.HandleKey(key(tcell.KeyEscape, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected popup closed after Escape")
	}

	// Still on the same token: must stay suppressed.
	typeString(e, "s")
	if e.CompletionActive() {
		t.Fatal("expected popup to stay suppressed while still on the same token")
	}

	// Moving to a new token clears the suppression.
	typeString(e, " C")
	if !e.CompletionActive() {
		t.Fatal("expected popup to reopen once the cursor left the suppressed token")
	}
}

func TestEditorCompletionSpaceNeverOpensFromClosedState(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, " ")
	if e.CompletionActive() {
		t.Fatal("expected a bare space to never open the popup from closed")
	}

	// A non-blank character right after still opens it.
	typeString(e, "C")
	if !e.CompletionActive() {
		t.Fatal("expected popup to open once a non-blank character was typed")
	}
}

func TestEditorCompletionEnterNeverOpensOnBlankLine(t *testing.T) {
	e := newTestEditor("Cu")
	e.SetCompletionProvider(testCompletionProvider("Customers"))
	e.cursorCol = len("Cu")

	e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected Enter's blank new line to never open the popup")
	}
}

func TestEditorCompletionDotAloneDoesNotOpen(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("objects"))

	typeString(e, "sys")
	e.closeCompletion() // in case typing opened it; test the dot from closed
	typeString(e, ".")
	if e.CompletionActive() {
		t.Fatal("expected '.' alone to not open the popup — only a started word (letter or '[') may")
	}

	// The next word's first letter opens it.
	typeString(e, "o")
	if !e.CompletionActive() {
		t.Fatal("expected popup open once a word started after the '.'")
	}
}

func TestEditorCompletionDigitDoesNotOpen(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "5")
	if e.CompletionActive() {
		t.Fatal("expected a digit (numeric literal) to not open the popup")
	}
}

func TestEditorCompletionOpenBracketOpens(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "[")
	if !e.CompletionActive() {
		t.Fatal("expected '[' (bracket-quoted identifier start) to open the popup")
	}

	// A digit-led fragment inside brackets still counts as a word start.
	e.SetText("")
	e.SetCompletionProvider(testCompletionProvider("2024data"))
	typeString(e, "[20")
	if !e.CompletionActive() {
		t.Fatal("expected a bracket-quoted fragment starting with a digit to keep the popup open")
	}
}

func TestEditorCompletionDeleteBackspaceUndoNeverOpenFromClosed(t *testing.T) {
	e := newTestEditor("Cu X")
	e.SetCompletionProvider(testCompletionProvider("Customers"))
	e.cursorCol = 2

	e.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected Delete to never open the popup from closed")
	}
	e.HandleKey(key(tcell.KeyBackspace2, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected Backspace to never open the popup from closed")
	}
	e.HandleKey(key(tcell.KeyCtrlZ, tcell.ModNone))
	if e.CompletionActive() {
		t.Fatal("expected undo to never open the popup from closed")
	}
}

func TestEditorCompletionModifiedKeysNotHijackedWhileOpen(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Aaa", "Aab", "Aac"))

	typeString(e, "Aa")
	if !e.CompletionActive() || e.completionSel != 0 {
		t.Fatalf("popup open = %v sel = %d, want open at 0", e.CompletionActive(), e.completionSel)
	}
	// Ctrl+Up/Down belong to the host (panel resize) or the editor (word
	// ops), never to popup selection.
	e.HandleKey(key(tcell.KeyDown, tcell.ModCtrl))
	if e.completionSel != 0 {
		t.Fatalf("completionSel after Ctrl+Down = %d, want 0 (modified keys must fall through)", e.completionSel)
	}
}

func TestEditorCompletionClosesOnSetTextAndFocusLoss(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "Cu")
	if !e.CompletionActive() {
		t.Fatal("expected popup open")
	}
	e.SetText("SELECT 1")
	if e.CompletionActive() {
		t.Fatal("expected SetText to close the popup")
	}

	typeString(e, " Cu")
	if !e.CompletionActive() {
		t.Fatal("expected popup open again")
	}
	e.SetActive(false)
	if e.CompletionActive() {
		t.Fatal("expected losing focus to close the popup")
	}
}

func TestEditorCompletionCtrlSpaceOpensEvenAfterSpace(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers", "Orders"))

	typeString(e, " ")
	if e.CompletionActive() {
		t.Fatal("expected a bare space to not open the popup")
	}
	e.HandleKey(runeKey(' ', tcell.ModCtrl))
	if !e.CompletionActive() {
		t.Fatal("expected explicit Ctrl+Space to open the popup regardless of the blank-char gate")
	}
}

func TestEditorCompletionNeverOpensFromPureNavigation(t *testing.T) {
	e := newTestEditor("Customers")
	e.SetCompletionProvider(testCompletionProvider("Customers", "Orders"))
	e.cursorCol = len("Customers")

	for _, k := range []tcell.Key{
		tcell.KeyLeft, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyHome, tcell.KeyEnd, tcell.KeyUp, tcell.KeyDown,
	} {
		e.HandleKey(key(k, tcell.ModNone))
		if e.CompletionActive() {
			t.Fatalf("key %v: unexpectedly opened completion from a closed state", k)
		}
	}
}

func TestEditorCompletionReadOnlyNeverOpens(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))
	e.SetReadOnly(true)

	typeString(e, "C")
	if e.CompletionActive() {
		t.Fatal("expected no completion popup in a read-only editor")
	}
}

func TestEditorCompletionCtrlSpaceSingleMatchAutoCommits(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	e.HandleKey(runeKey(' ', tcell.ModCtrl))
	if e.CompletionActive() {
		t.Fatal("expected popup NOT to open when Ctrl+Space finds exactly one match")
	}
	if got := e.Text(); got != "Customers" {
		t.Fatalf("Text() = %q, want %q (auto-committed)", got, "Customers")
	}
}

func TestEditorCompletionCtrlSpaceMultipleMatchesOpensPopup(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Customers", "CustomerAddresses"))

	e.HandleKey(runeKey(' ', tcell.ModCtrl))
	if !e.CompletionActive() {
		t.Fatal("expected popup open when Ctrl+Space finds more than one match")
	}
	if len(e.completionItems) != 2 {
		t.Fatalf("completionItems = %+v, want 2 items", e.completionItems)
	}
}

func TestEditorCompletionCtrlSpaceFallsBackToContextMenuWithoutProvider(t *testing.T) {
	e := newTestEditor("")
	called := false
	e.OnRightClick = func(x, y int) { called = true }

	e.HandleKey(runeKey(' ', tcell.ModCtrl))
	if !called {
		t.Fatal("expected OnRightClick to fire for Ctrl+Space when no completion provider is set")
	}
}

func TestEditorCompletionPlaceholderNotSelectableOrCommittable(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(func(lines [][]rune, row, col int) ([]CompletionItem, int) {
		return []CompletionItem{{Label: "Loading suggestions...", Placeholder: true}}, col
	})

	typeString(e, "x")
	if !e.CompletionActive() {
		t.Fatal("expected popup open showing the placeholder row")
	}
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
	if got := e.Text(); got != "x" {
		t.Fatalf("Text() = %q, want %q — placeholder must never commit", got, "x")
	}
}

func TestEditorCompletionRectFlipsAboveNearBottomEdge(t *testing.T) {
	e := newTestEditor("Aa\n\n\n\nAa") // 5 lines; last one ("Aa") sits on row 4
	e.SetCompletionProvider(testCompletionProvider("Aaa", "Aab", "Aac"))
	e.SetBounds(0, 0, 40, 5) // rows 0..4; no room below row 4 for a 3-row popup

	e.cursorRow, e.cursorCol = 4, 2
	e.updateCompletion()
	if !e.CompletionActive() {
		t.Fatal("expected popup open")
	}
	rect := e.completionRect()
	if rect.Y >= e.cursorRow {
		t.Fatalf("completionRect().Y = %d, want above cursor row %d (no room below)", rect.Y, e.cursorRow)
	}

	// Same popup near the top of the buffer has room below and must not flip.
	e.closeCompletion()
	e.cursorRow, e.cursorCol = 0, 2
	e.updateCompletion()
	rect = e.completionRect()
	if rect.Y <= e.cursorRow {
		t.Fatalf("completionRect().Y = %d, want below cursor row %d (room available)", rect.Y, e.cursorRow)
	}
}

// TestEditorCompletionRectReservesDetailColumn pins a real bug: Draw and
// completionRect independently derived the label/detail column split from
// two different formulas, so a popup whose items had short labels but
// non-empty Detail text (e.g. "Id  int, not null") got a rect sized as if
// detail existed but a Draw that then computed detailW as 0 and never
// wrote it — column type info silently never appeared. Now both read
// completionColumnWidths, so this checks they can't disagree again.
func TestEditorCompletionRectReservesDetailColumn(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(func(lines [][]rune, row, col int) ([]CompletionItem, int) {
		return []CompletionItem{
			{Text: "Id", Label: "Id", Detail: "int, not null"},
			{Text: "Name", Label: "Name", Detail: "nvarchar(50)"},
		}, col
	})
	typeString(e, "x")
	if !e.CompletionActive() {
		t.Fatal("expected popup open")
	}

	labelW, detailW := e.completionColumnWidths()
	if detailW == 0 {
		t.Fatal("completionColumnWidths() detailW = 0, want > 0 for items with Detail text")
	}
	rect := e.completionRect()
	wantW := 2 + labelW + 2 + detailW
	if rect.W != wantW {
		t.Fatalf("completionRect().W = %d, want %d (2 + labelW %d + 2 + detailW %d) — Draw's row layout must reserve exactly this", rect.W, wantW, labelW, detailW)
	}
}

func TestEditorCompletionRowNavigationClamps(t *testing.T) {
	e := newTestEditor("")
	e.SetCompletionProvider(testCompletionProvider("Aaa", "Aab", "Aac"))

	typeString(e, "Aa")
	if len(e.completionItems) != 3 {
		t.Fatalf("completionItems = %+v, want 3 items", e.completionItems)
	}
	if e.completionSel != 0 {
		t.Fatalf("initial completionSel = %d, want 0", e.completionSel)
	}
	e.HandleKey(key(tcell.KeyUp, tcell.ModNone))
	if e.completionSel != 0 {
		t.Fatalf("completionSel after Up at top = %d, want 0 (clamped)", e.completionSel)
	}
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	if e.completionSel != 2 {
		t.Fatalf("completionSel after 3x Down = %d, want 2 (clamped)", e.completionSel)
	}
	e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
	if got := e.Text(); got != "Aac" {
		t.Fatalf("Text() after commit = %q, want %q", got, "Aac")
	}
}
