// Package export writes result rows as delimited text — CSV, and TSV with the
// same rules — for every consumer that saves a result: the TUI's Results To
// File and the command line's csv/tsv output. One writer, so the two produce the
// same bytes for the same rows.
//
// The NULL convention is settled (docs/decisions.md § SQL NULL in results): a
// SQL NULL is an empty field, an empty string is "", the string 'NULL' is NULL.
//
// Standard library only; it knows nothing about query or the TUI.
package export
