package tui

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_export.go is the Extended Events viewer's Export menu: the
// events the filter passes, in the visible columns, values whole rather than
// cut to the grid's width — as CSV, as tab-separated text, or as a script
// that creates a table and inserts them (SSMS's Export to Table, as T-SQL the
// user runs where they choose). Grouping does not change what is exported:
// the events are, not the group rows.

// xeInsertBatch is how many rows one INSERT carries: T-SQL's limit for a
// VALUES list.
const xeInsertBatch = 1000

// xeMaxQueryWindowEvents caps the INSERT script sent to a query window; past
// it the script is megabytes of editor text, and a file is the better place.
const xeMaxQueryWindowEvents = 10_000

// xeExportFormat is one of Export's formats.
type xeExportFormat int

const (
	xeExportCSV xeExportFormat = iota
	xeExportTSV
	xeExportInserts
)

// showExportMenu pops the Export formats.
func (v *XEventViewer) showExportMenu() {
	v.popMenu(xeToolExport, []controls.MenuItem{
		{Label: "To CSV File...", Action: func() { v.exportToFile(xeExportCSV) }},
		{Label: "To Tab-Separated File...", Action: func() { v.exportToFile(xeExportTSV) }},
		{Label: "As INSERT Script to File...", Action: func() { v.exportToFile(xeExportInserts) }},
		{Label: "As INSERT Script to New Query",
			Enabled: func() bool { return len(v.shown) <= xeMaxQueryWindowEvents },
			Note:    fmt.Sprintf("over %d events — use a file", xeMaxQueryWindowEvents),
			Action:  v.exportToQueryWindow},
	})
}

// exportBaseName is the stem of an export's file and table name: the session,
// or for a merged file set its pattern's file part, made safe for a file name.
func (v *XEventViewer) exportBaseName() string {
	name := v.session
	if v.files != "" {
		name = "merged"
	}
	return strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "*", "", "?", "").Replace(name)
}

// exportToFile asks where, then writes the shown events there in format f.
func (v *XEventViewer) exportToFile(f xeExportFormat) {
	if len(v.shown) == 0 {
		v.app.setStatus("Nothing to export")
		return
	}
	ext := map[xeExportFormat]string{xeExportCSV: ".csv", xeExportTSV: ".txt", xeExportInserts: ".sql"}[f]
	v.app.fileDialog.ShowSave("Export Events", v.exportBaseName()+"-events"+ext, func(path string) {
		// Rendered here, on the UI goroutine: shown grows under a live feed.
		data, err := v.exportBytes(f)
		if err != nil {
			v.app.setStatus(fmt.Sprintf("Export failed: %v", err))
			return
		}
		n := len(v.shown)
		v.app.safego("exporting events", func() {
			err := fileutil.WriteAtomic(path, data, 0o644)
			v.app.postAndWake(func() {
				if err != nil {
					v.app.setStatus(fmt.Sprintf("Export failed: %v", err))
					return
				}
				v.app.setStatus(fmt.Sprintf("Exported %d events to %s", n, path))
			})
		})
		v.app.setStatus(fmt.Sprintf("Exporting %d events to %s...", n, path))
	})
}

// exportToQueryWindow opens the INSERT script in a new query window on the
// viewer's server, unconnected to any database in particular: where the table
// goes is the user's choice.
func (v *XEventViewer) exportToQueryWindow() {
	if len(v.shown) == 0 {
		v.app.setStatus("Nothing to export")
		return
	}
	if !v.app.requireConn(v.host) {
		return
	}
	v.app.openQueryWithText(v.host, "", v.insertScript())
}

// exportBytes renders the shown events in format f.
func (v *XEventViewer) exportBytes(f xeExportFormat) ([]byte, error) {
	switch f {
	case xeExportCSV:
		return v.exportCSV()
	case xeExportInserts:
		return []byte(v.insertScript()), nil
	}
	return []byte(v.exportText()), nil
}

// exportText renders the shown events as tab-separated text, one line per
// event: line breaks and tabs inside a value are folded to spaces.
func (v *XEventViewer) exportText() string {
	var b strings.Builder
	b.WriteString(strings.Join(v.headers(), "\t"))
	b.WriteString("\n")
	for _, e := range v.shown {
		for i, c := range v.columns {
			if i > 0 {
				b.WriteByte('\t')
			}
			if val, ok := e.Value(c); ok {
				b.WriteString(flattenLogText(val.Display()))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// exportCSV renders the shown events as RFC 4180 CSV: values whole, line
// breaks kept inside their quotes.
func (v *XEventViewer) exportCSV() ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(v.headers()); err != nil {
		return nil, err
	}
	row := make([]string, len(v.columns))
	for _, e := range v.shown {
		for i, c := range v.columns {
			row[i] = ""
			if val, ok := e.Value(c); ok {
				row[i] = val.Display()
			}
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// xeSQLType is the column type the INSERT script declares for column c of
// the shown events: bigint where every value is a number that fits one,
// decimal(20,0) where they are integers some of which only fit a uint64 (a
// query_hash, a plan_handle's hash), float for any other numbers, datetime2
// for the timestamp, nvarchar otherwise — (max) only when a value needs it.
//
// A uint64 past bigint's range typed float would round: a 64-bit hash keeps
// about 16 of its 20 digits, so two different hashes can import equal. And
// nvarchar(n) counts UTF-16 code units, so a character outside the Basic
// Multilingual Plane (an emoji) takes two: sized in runes, the INSERT fails
// "String or binary data would be truncated".
func (v *XEventViewer) xeSQLType(c xevent.Column) string {
	if c.Kind == xevent.ColTimestamp {
		return "datetime2(6)"
	}
	allInt, allWide, allNum, seen, longest := true, true, true, false, 0
	for _, e := range v.shown {
		val, ok := e.Value(c)
		if !ok {
			continue
		}
		s := val.Display()
		seen = true
		longest = max(longest, utf16Len(s))
		if !isSQLNumber(s) {
			allInt, allWide, allNum = false, false, false
			continue
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			allInt = false
			if _, err := strconv.ParseUint(s, 10, 64); err != nil {
				allWide = false
			}
		}
	}
	switch {
	case seen && allInt:
		return "bigint"
	case seen && allWide:
		return "decimal(20,0)"
	case seen && allNum:
		return "float"
	case longest > 4000:
		return "nvarchar(max)"
	}
	return "nvarchar(" + strconv.Itoa(max(1, longest)) + ")"
}

// utf16Len is s's length in UTF-16 code units, the unit nvarchar(n) counts.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += max(1, utf16.RuneLen(r))
	}
	return n
}

// isSQLNumber reports whether s is a plain decimal number T-SQL reads as a
// literal: digits with an optional sign, point and exponent. Stricter than
// strconv.ParseFloat, which also takes "NaN", "Inf" and hex.
func isSQLNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i++
	}
	digits, point, exp := 0, false, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.' && !point && !exp:
			point = true
		case (c == 'e' || c == 'E') && !exp && digits > 0:
			exp = true
			digits = 0
			if i+1 < len(s) && (s[i+1] == '-' || s[i+1] == '+') {
				i++
			}
		default:
			return false
		}
	}
	return digits > 0
}

// insertScript renders the shown events as a CREATE TABLE and INSERTs of
// xeInsertBatch rows each. The table is named after the session in dbo; the
// timestamp is the viewer's local time, as the grid shows it, which the
// script's header says.
func (v *XEventViewer) insertScript() string {
	table := "[dbo]." + gosmo.QuoteName("xevents_"+v.exportBaseName())
	headers := v.headers()
	types := make([]string, len(v.columns))
	for i, c := range v.columns {
		types[i] = v.xeSQLType(c)
	}
	var b strings.Builder
	source := "event session " + v.session
	if v.files != "" {
		source = "event files " + v.files
	}
	fmt.Fprintf(&b, "-- %d events from %s, exported by goSSMS %s.\n", len(v.shown), source,
		time.Now().Format("2006-01-02 15:04:05"))
	b.WriteString("-- timestamp is in the exporting machine's local time.\n")
	b.WriteString("CREATE TABLE ")
	b.WriteString(table)
	b.WriteString(" (\n")
	cols := make([]string, len(headers))
	for i, h := range headers {
		cols[i] = gosmo.QuoteName(h)
		sep := ","
		if i == len(headers)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "    %s %s NULL%s\n", cols[i], types[i], sep)
	}
	b.WriteString(");\n")
	colList := strings.Join(cols, ", ")
	for i, e := range v.shown {
		if i%xeInsertBatch == 0 {
			if i > 0 {
				b.WriteString(";\n")
			}
			b.WriteString("INSERT INTO ")
			b.WriteString(table)
			b.WriteString(" (")
			b.WriteString(colList)
			b.WriteString(") VALUES\n")
		} else {
			b.WriteString(",\n")
		}
		b.WriteString("(")
		for j, c := range v.columns {
			if j > 0 {
				b.WriteString(", ")
			}
			val, ok := e.Value(c)
			switch {
			case !ok:
				b.WriteString("NULL")
			case types[j] == "bigint" || types[j] == "decimal(20,0)" || types[j] == "float":
				b.WriteString(val.Display())
			case types[j] == "datetime2(6)":
				b.WriteString("'")
				b.WriteString(val.Display())
				b.WriteString("'")
			default:
				b.WriteString("N'")
				b.WriteString(strings.ReplaceAll(val.Display(), "'", "''"))
				b.WriteString("'")
			}
		}
		b.WriteString(")")
	}
	if len(v.shown) > 0 {
		b.WriteString(";\n")
	}
	return b.String()
}
