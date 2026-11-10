// Package export writes result rows as delimited text — CSV, and TSV with the
// same rules — for every consumer that saves a result: the TUI's Results To
// File and the command line's csv/tsv output. One writer, so the two produce the
// same bytes for the same rows.
//
// JSONWriter and XMLWriter write one result set as a JSON array or an XML
// document, for the results grid's Export; WriteTable writes a set in any of
// the four formats. KindOf, IsJSONNumber and ColumnKeys are the JSON value and
// key rules, shared with the command line's json and jsonl.
//
// RecordReader reads the format back, for import: the writer's exact inverse,
// which encoding/csv is not (it cannot tell an empty field from "").
//
// The NULL convention is settled (docs/decisions.md § SQL NULL in results): a
// SQL NULL is an empty field, an empty string is "", the string 'NULL' is NULL.
//
// Standard library only; it knows nothing about query or the TUI.
package export
