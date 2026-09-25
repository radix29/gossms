package controls

import (
	"slices"
	"testing"
)

// A LineBuffer must install exactly the document SetText builds from the same
// text — same lines, same widest line — or Results to Text would render
// differently depending on the size of the set.
func TestLineBufferMatchesSetText(t *testing.T) {
	for _, text := range []string{
		"",
		"one line",
		"a\nb\n",
		"crlf\r\nline",
		"tab\there",
		"wide 日本語\nnarrow",
		"\n\n",
	} {
		want := NewEditor(nil)
		want.SetIndentWidth(4)
		want.SetText(text)

		b := NewLineBuffer(4)
		b.AppendText(text)
		got := NewEditor(nil)
		got.SetLineBuffer(b)

		if !slices.EqualFunc(got.doc.all(), want.doc.all(), slices.Equal) {
			t.Errorf("%q: lines = %q, want %q", text, got.doc.all(), want.doc.all())
		}
		if g, w := got.doc.maxDisplayWidth(), want.doc.maxDisplayWidth(); g != w {
			t.Errorf("%q: max width = %d, want %d", text, g, w)
		}
	}
}

// The buffer is shared with the editor it is installed in, so the editor's
// width cache must not write into it: the Results to Text memo installs the
// same buffer again on a tab switch back, and a buffer whose widths had been
// overwritten by some later document would size that one's scrollbar wrong.
func TestSetLineBufferDoesNotWriteIntoTheBuffer(t *testing.T) {
	b := NewLineBuffer(4)
	b.AppendText("short\na much longer line")
	e := NewEditor(nil)
	e.SetLineBuffer(b)
	e.SetText("x\ny\nz")
	_ = e.doc.maxDisplayWidth()

	if want := []int{5, 18}; !slices.Equal(b.lineW, want) {
		t.Fatalf("buffer widths = %v after a later SetText, want %v", b.lineW, want)
	}
	e.SetLineBuffer(b)
	if got := e.doc.maxDisplayWidth(); got != 18 {
		t.Errorf("reinstalled buffer max width = %d, want 18", got)
	}
}
