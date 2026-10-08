package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// rowRecordingScreen records which screen row each drawn cell landed on, so a
// test can ask where a given piece of text was drawn.
type rowRecordingScreen struct {
	fakeSizedScreen
	cells map[[2]int]string
}

func newRowRecordingScreen(w, h int) *rowRecordingScreen {
	return &rowRecordingScreen{w: w, h: h, cells: map[[2]int]string{}}
}

func (s *rowRecordingScreen) SetContent(x, y int, r rune, comb []rune, _ tcell.Style) {
	s.cells[[2]int{x, y}] = string(append([]rune{r}, comb...))
}

func (s *rowRecordingScreen) Put(x, y int, str string, _ tcell.Style) (string, int) {
	if str == "" {
		return "", 0
	}
	s.cells[[2]int{x, y}] = str
	return str, 1
}

// rowOf answers the screen row whose text contains want, or -1.
func (s *rowRecordingScreen) rowOf(want string) int {
	for y := 0; y < s.h; y++ {
		var b strings.Builder
		for x := 0; x < s.w; x++ {
			if c, ok := s.cells[[2]int{x, y}]; ok {
				b.WriteString(c)
			} else {
				b.WriteByte(' ')
			}
		}
		if strings.Contains(b.String(), want) {
			return y
		}
	}
	return -1
}

// TestQueryListDialogLastRowAboveButtons pins B22: with more queries than fit,
// Down to the last one scrolls it onto a row above the button row, not onto
// the button row itself, where DrawButtons overdrew it.
func TestQueryListDialogLastRowAboveButtons(t *testing.T) {
	a := newTestApp()
	scr := newRowRecordingScreen(80, 30)
	a.screen = scr

	d := &QueryListDialog{app: a}
	d.InitModal(a.screen, "Query List", 56, 16)
	for i := range 20 {
		d.titles = append(d.titles, "Query "+strconv.Itoa(i))
		d.indices = append(d.indices, i)
	}
	d.ModalDialog.Show()
	for range len(d.titles) {
		d.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	d.Draw(scr)

	y := scr.rowOf("Query 19")
	if y < 0 || y >= d.ButtonRowY() {
		t.Errorf("last query drawn on row %d, want above the button row %d", y, d.ButtonRowY())
	}
}

// TestTasksDialogLastRowAboveButtons is B22 for TasksDialog.
func TestTasksDialogLastRowAboveButtons(t *testing.T) {
	a := newTestApp()
	scr := newRowRecordingScreen(80, 30)
	a.screen = scr
	for i := range 20 {
		a.tasks = append(a.tasks, &Task{Label: "Task " + strconv.Itoa(i)})
	}

	d := NewTasksDialog(a)
	d.Show()
	for range len(a.tasks) {
		d.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	d.Draw(scr)

	y := scr.rowOf("Task 19")
	if y < 0 || y >= d.ButtonRowY() {
		t.Errorf("last task drawn on row %d, want above the button row %d", y, d.ButtonRowY())
	}
}

// TestUpdateDialogRowsAboveSeparator is B22 for UpdateDialog, whose rows must
// also clear DrawSeparator's line one above the button row: at the smallest
// height that still holds every line, the last one is drawn above it. (The
// separator is drawn after the rows, so an overlap cannot be seen on the
// screen; this pins that the bound gives up no row it has room for.)
func TestUpdateDialogRowsAboveSeparator(t *testing.T) {
	a := newTestApp()
	scr := newRowRecordingScreen(80, 30)
	a.screen = scr

	d := NewUpdateDialog(a)
	d.ModalDialog.Show()
	d.ShowResult("v1.0.0", githubRelease{}, errors.New("LAST-LINE"))
	d.SetSize(d.Rect().W, len(d.lines)+6) // borders, padding row, separator, button row, row under it
	d.Draw(scr)

	y := scr.rowOf("LAST-LINE")
	if sep := d.ButtonRowY() - 1; y < 0 || y >= sep {
		t.Errorf("last line drawn on row %d, want above the separator row %d", y, sep)
	}
}

// TestListDialogsButtonRowIsTabReachable pins U13: Tab moves focus onto
// Close and Enter then presses it (both dialogs' Enter had only ever run the
// first button), and a key the dialog does nothing with reports unhandled.
func TestListDialogsButtonRowIsTabReachable(t *testing.T) {
	key := func(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, "", tcell.ModNone) }
	a := newTestApp()
	a.screen = newRowRecordingScreen(80, 30)
	dialogs := map[string]interface {
		Dialog
		Show()
	}{
		"Query List":       NewQueryListDialog(a),
		"Background Tasks": NewTasksDialog(a),
	}
	for name, d := range dialogs {
		d.Show()
		if d.HandleKey(key(tcell.KeyF5)) {
			t.Errorf("%s: F5 reported handled", name)
		}
		d.HandleKey(key(tcell.KeyTab))
		d.HandleKey(key(tcell.KeyEnter))
		if d.Visible() {
			t.Errorf("%s: Tab, Enter did not press Close", name)
		}
		d.Show()
		d.HandleKey(key(tcell.KeyBacktab))
		d.HandleKey(key(tcell.KeyBacktab))
		if d.HandleKey(key(tcell.KeyEnter)); !d.Visible() {
			t.Errorf("%s: Backtab twice should be back on the first button, not Close", name)
		}
	}
}
