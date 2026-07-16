package tui

import (
	"encoding/csv"
	"fmt"
	"os"

	"github.com/radix29/gossms/internal/query"
)

// promptWriteResults implements Results To File: asks for a path, writes
// every result set as CSV, and reports the outcome as an extra message on
// the result (so it shows up in the Messages tab too).
func (p *QueryPanel) promptWriteResults(res *query.Result) {
	p.app.fileDialog.ShowSave("Results To File", "results.csv", func(path string) {
		n, err := writeCSV(path, res.Sets)
		msg := query.Message{Text: fmt.Sprintf("%d row(s) written to %s", n, path)}
		if err != nil {
			msg = query.Message{Text: fmt.Sprintf("write results: %v", err), IsError: true}
		}
		res.Messages = append(res.Messages, msg)
		if p.result == res {
			p.renderActiveTab()
		}
		p.app.setStatus(msg.Text)
	})
}

// writeCSV writes every result set to path as CSV — a header row then data
// rows per set, sets separated by a blank line — returning the total number
// of data rows written. A failing Close (e.g. a disk-full flush error the OS
// only reports at close time) is reported too, not silently dropped, unless
// an earlier error already explains the failure.
func writeCSV(path string, sets []query.ResultSet) (n int, err error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	w := csv.NewWriter(f)
	for i, set := range sets {
		if i > 0 {
			w.Flush()
			if _, err = f.WriteString("\n"); err != nil {
				return n, err
			}
		}
		if err = w.Write(set.Columns); err != nil {
			return n, err
		}
		for _, row := range set.Rows {
			if err = w.Write(row); err != nil {
				return n, err
			}
			n++
		}
	}
	w.Flush()
	err = w.Error()
	return n, err
}
