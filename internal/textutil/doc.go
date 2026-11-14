// Package textutil holds small string helpers that more than one gossms package
// needs and none owns. It imports nothing but the standard library, so any
// package — internal/cli, internal/query, internal/fileutil, internal/tui —
// can use it without pulling in tcell or each other.
//
// Two "one line" helpers, deliberately different:
//
//   - OneLine keeps every character but the line breaks and tabs (Breaks),
//     each of which becomes one space. For output that is data — the CLI's
//     text renderer — where a run of spaces inside a value is part of it.
//   - Collapse folds every whitespace run to one space and trims the ends. For
//     a display line — a plan's statement text, a log grid cell — where a
//     T-SQL statement's indentation is noise.
package textutil
