package output

import (
	"bytes"
	"io"
	"sort"
)

// renderCapped renders r and, when that passes o.MaxOutputBytes, drops
// trailing rows — the last set's first — until it fits, marking each set that
// lost rows truncated by max-output-bytes. Whole rows only, so the document is
// always well formed: JSON is never cut mid-value. r's sets are left as
// written, so a summary rendered after (csv's stderr) reports the cut.
//
// When even no rows at all do not fit (a huge message or record), the
// envelope is written anyway, rowless, with a warning: a document over the
// limit beats one that cannot be parsed.
func renderCapped(w io.Writer, r *Result, o Options) error {
	if o.MaxOutputBytes <= 0 {
		return render(w, r, o)
	}
	var buf bytes.Buffer
	if err := render(&buf, r, o); err != nil {
		return err
	}
	if int64(buf.Len()) <= o.MaxOutputBytes {
		_, err := w.Write(buf.Bytes())
		return err
	}

	total := 0
	for _, s := range r.Sets {
		total += len(s.Rows)
	}
	size := func(keep int) (int64, error) {
		var b bytes.Buffer
		err := render(&b, keepRows(r, keep), o)
		return int64(b.Len()), err
	}
	// The largest row count that fits: rendered size only grows with rows
	// kept (a row adds its line; a column only widens).
	var serr error
	keep := sort.Search(total, func(k int) bool {
		n, err := size(k + 1)
		if err != nil {
			serr = err
		}
		return n > o.MaxOutputBytes
	})
	if serr != nil {
		return serr
	}
	capped := keepRows(r, keep)
	*r = *capped
	if keep == 0 {
		if n, err := size(0); err == nil && n > o.MaxOutputBytes {
			r.Warnings = append(r.Warnings[:len(r.Warnings):len(r.Warnings)], "The output is over --max-output-bytes "+
				"with every row left out; its messages or record alone are larger.")
		}
	}
	return render(w, r, o)
}

// keepRows is a copy of r keeping its first keep rows in set order; a set that
// loses rows is marked truncated by max-output-bytes.
func keepRows(r *Result, keep int) *Result {
	c := *r
	c.Sets = make([]Set, len(r.Sets))
	for i, s := range r.Sets {
		n := min(len(s.Rows), keep)
		keep -= n
		if n < len(s.Rows) {
			s.Rows = s.Rows[:n]
			markTruncated(&s, TruncatedByMaxOutputBytes)
		}
		c.Sets[i] = s
	}
	return &c
}
