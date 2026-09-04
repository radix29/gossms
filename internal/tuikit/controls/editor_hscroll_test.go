package controls

import (
	"strings"
	"testing"
)

// ensureColumnVisible is the horizontal half of ensureCursorVisible, called by
// both it and selectMatch. Nothing else pinned the arithmetic, so a change to
// either caller's copy used to go unnoticed.

// TestEditorScrollsSidewaysToFollowTheCaret drives the ensureCursorVisible
// route: a caret walked past the right edge of an unwrapped editor.
func TestEditorScrollsSidewaysToFollowTheCaret(t *testing.T) {
	e := widthEditor(strings.Repeat("x", 200), 20, 3)
	e.cursorCol = 60
	e.ensureCursorVisible()

	if e.scrollCol != 41 {
		t.Fatalf("scrollCol = %d with the caret at display column 60 and a 20-column content area, want 41 (caret on the last column)", e.scrollCol)
	}
	e.cursorCol = 5
	e.ensureCursorVisible()
	if e.scrollCol != 5 {
		t.Errorf("scrollCol = %d after the caret moved back to column 5, want 5", e.scrollCol)
	}
}

// TestEditorScrollsSidewaysToAFarMatch drives the selectMatch route: a match
// inside the viewport vertically but far off it horizontally.
func TestEditorScrollsSidewaysToAFarMatch(t *testing.T) {
	e := widthEditor(strings.Repeat("x", 100)+"needle", 20, 3)
	if err := e.SetSearch(SearchOptions{Query: "needle"}); err != nil {
		t.Fatalf("SetSearch: %v", err)
	}
	if !e.FindNext(1) {
		t.Fatal("FindNext found nothing")
	}
	if e.scrollCol != 87 {
		t.Fatalf("scrollCol = %d after finding a match ending at display column 106, want 87 (its end on the last column)", e.scrollCol)
	}
}

// TestEditorDoesNotScrollSidewaysWhenWrapped: wrapping means there is no
// horizontal scroll to do, and a non-zero scrollCol would draw the wrapped
// text shifted off its left edge.
func TestEditorDoesNotScrollSidewaysWhenWrapped(t *testing.T) {
	e := widthEditor(strings.Repeat("x", 200), 20, 3)
	e.SetWrapMode(true)
	e.cursorCol = 150
	e.ensureCursorVisible()
	e.ensureColumnVisible() // selectMatch's route, which has no wrap check of its own
	if e.scrollCol != 0 {
		t.Errorf("scrollCol = %d in wrap mode, want 0", e.scrollCol)
	}
}
