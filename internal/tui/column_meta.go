package tui

import (
	"strconv"

	"github.com/radix29/gossms/internal/query"
)

// columnMetaMessages renders the "Output Column Metadata" block appended to
// the Messages tab when the Meta toggle is on (see App.metaEnabled): one
// "Result N" heading per result set, then one line per column naming its
// declared SQL Server type.
//
//	Result 1
//	col1 nvarchar(50)
//	col2 int
//
//	Result 2
//	col1 float
//	2 datetime
//
// A column the query didn't name — a bare expression, "SELECT 1+1" — shows
// its 1-based position instead, since an empty name would leave the type
// line starting with a space and reading as if it belonged to the one above.
//
// Returns nil when there are no result sets, so a script that only ran DML
// gets no empty block.
func columnMetaMessages(sets []query.ResultSet) []query.Message {
	if len(sets) == 0 {
		return nil
	}
	msgs := make([]query.Message, 0, len(sets)*3)
	for i, rs := range sets {
		if i > 0 {
			msgs = append(msgs, query.Message{Text: ""})
		}
		msgs = append(msgs, query.Message{Text: "Result " + strconv.Itoa(i+1)})
		for c, name := range rs.Columns {
			if name == "" {
				name = strconv.Itoa(c + 1)
			}
			line := name
			// ColumnTypes is parallel to Columns, but a set built by
			// something other than a real scan (a test fake, a future
			// synthesised set) may not carry it — show the name alone
			// rather than index out of range.
			if c < len(rs.ColumnTypes) && rs.ColumnTypes[c] != "" {
				line += " " + rs.ColumnTypes[c]
			}
			msgs = append(msgs, query.Message{Text: line})
		}
	}
	return msgs
}
