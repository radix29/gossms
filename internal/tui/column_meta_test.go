package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/query"
)

func metaText(msgs []query.Message) string {
	lines := make([]string, len(msgs))
	for i, m := range msgs {
		lines[i] = m.Text
	}
	return strings.Join(lines, "\n")
}

// TestColumnMetaMessages pins the whole block for a two-result script,
// including the blank line between sets and the fallback to a column's
// 1-based position when the query didn't name it.
func TestColumnMetaMessages(t *testing.T) {
	sets := []query.ResultSet{
		{Columns: []string{"col1", "sql text"}, ColumnTypes: []string{"nvarchar(50)", "xml"}},
		{Columns: []string{"od]d", ""}, ColumnTypes: []string{"float", "datetime"}},
	}
	want := strings.Join([]string{
		"Result 1",
		"[col1] nvarchar(50)",
		"[sql text] xml",
		"",
		"Result 2",
		"[od]]d] float",
		"2 datetime",
	}, "\n")
	if got := metaText(columnMetaMessages(sets)); got != want {
		t.Errorf("columnMetaMessages =\n%s\n\nwant\n%s", got, want)
	}
}

// TestColumnMetaMessagesNoSets confirms a script that returned no grid gets
// no block at all rather than an empty heading.
func TestColumnMetaMessagesNoSets(t *testing.T) {
	if got := columnMetaMessages(nil); got != nil {
		t.Errorf("columnMetaMessages(nil) = %v, want nil", got)
	}
	if got := columnMetaMessages([]query.ResultSet{}); got != nil {
		t.Errorf("columnMetaMessages(empty) = %v, want nil", got)
	}
}

// TestColumnMetaMessagesMissingTypes covers a result set carrying no
// ColumnTypes: the names still list, without a trailing space where a type
// would have gone.
func TestColumnMetaMessagesMissingTypes(t *testing.T) {
	sets := []query.ResultSet{{Columns: []string{"a", "b"}}}
	want := "Result 1\n[a]\n[b]"
	if got := metaText(columnMetaMessages(sets)); got != want {
		t.Errorf("columnMetaMessages =\n%s\n\nwant\n%s", got, want)
	}
}

// TestColumnMetaMessagesNotErrors confirms the block is plain output — a
// metadata line styled as an error would turn the Messages tab red and make
// a successful query look like a failed one (messagesHighlighter).
func TestColumnMetaMessagesNotErrors(t *testing.T) {
	sets := []query.ResultSet{{Columns: []string{"a"}, ColumnTypes: []string{"int"}}}
	for _, m := range columnMetaMessages(sets) {
		if m.IsError {
			t.Errorf("message %q marked IsError", m.Text)
		}
	}
}
