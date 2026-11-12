// Package format is Edit > Format Document's engine: per-language
// pretty-printers for a query panel's text (SQL, XML, JSON).
//
// Pure text in, text out: the standard library and internal/sqltext only, no
// TUI or database imports, so it can be driven without the app.
package format
