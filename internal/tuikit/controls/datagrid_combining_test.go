package controls

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// recordingTerm is a mock terminal that also keeps every byte written to it.
type recordingTerm struct {
	vt.MockTerm
	mu  sync.Mutex
	out bytes.Buffer
}

func (r *recordingTerm) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.out.Write(b)
	r.mu.Unlock()
	return r.MockTerm.Write(b)
}

func (r *recordingTerm) written() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out.String()
}

// TestGridCombiningMarkKeepsSeparatorColumn pins B21's diagnosis. Arabic
// stopwords ending in a shadda (U+0651, a combining mark) looked one column
// wide in a tmux capture, so the row's separators and the dialog's border
// seemed shifted. They are not: tmux's own cell grid, checked live, holds
// every separator in the header's column; the apparent shift is the capture's
// text read by a viewer that counts the mark as a column.
//
// What a terminal needs for that is the mark sent straight after its base,
// in one cell write, so it attaches instead of taking a cell of its own — and
// every separator placed by absolute column. Both are asserted on the bytes
// tcell actually emits.
func TestGridCombiningMarkKeepsSeparatorColumn(t *testing.T) {
	term := &recordingTerm{MockTerm: vt.NewMockTerm(vt.MockOptSize{X: 40, Y: 10})}
	s, err := tcell.NewTerminfoScreenFromTty(term)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)

	clusters := []string{
		"\u0646\u0651", // nūn + shadda
		"e\u0301",      // decomposed é
	}
	g := newTestDataGrid()
	g.SetData([]string{"Stopword", "Language"}, [][]string{
		{"أن", "Arabic"},                       // no mark
		{"\u0625\u0646\u0651", "Arabic"},       // إنّ: ends in a shadda
		{"\u0641\u0625\u0646\u0651", "Arabic"}, // فإنّ: ends in a shadda
		{"e\u0301", "French"},                  // decomposed é
	})
	g.Draw(s)
	s.Show()

	out := term.written()
	for _, c := range clusters {
		if !strings.Contains(out, c) {
			t.Errorf("cluster %+q not emitted intact (base and mark in one write)", c)
		}
	}

	// sepCol is the column of the first '|' on screen row y, as the terminal
	// holds it — not as tcell's own buffer does.
	sepCol := func(y int) int {
		for x := range 40 {
			if term.GetCell(vt.Coord{X: vt.Col(x), Y: vt.Row(y)}).C == "|" {
				return x
			}
		}
		return -1
	}
	want := sepCol(0) // header row
	if want < 0 {
		t.Fatal("no separator on the header row")
	}
	for r := range 4 {
		y := 2 + r // header, rule, then data rows
		if got := sepCol(y); got != want {
			t.Errorf("data row %d: separator at column %d, want %d (the header's)", r, got, want)
		}
	}
}
