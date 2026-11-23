# Bug-fix plan, 2026-11-23

Defects found in a read-through of gossms, `~/go/gosmo` and
`~/go/tuikit`, all in the **result-export path** (`internal/export` and its two
callers: the command line's `internal/cli/output` and the TUI's Export Results
/ Results To File). Each was reproduced against the live server
(`win10cli`, master) with the built binary, not only read. Nothing is fixed
yet. Close an item by deleting it; delete this file with the last one.
Nobody commits but the user. gosmo and tuikit are untouched by this plan: no
defect was found in either (see "Reviewed and clean").

Separate from `docs/review-plan-2026-11-23.md`, whose items W9/W10 and R1 are
still open; B1 is not in it or in `docs/open-threads.md`.

## Recommended implementation order

1. **B1 — CSV/TSV formula injection** (needs a decision from the user on
   default and scope before any code, and touches the lossless round-trip
   contract with `data import`).

Each step: `go test ./internal/export/... ./internal/cli/...`, then
`gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary with the repro below
(`docs/testing.md`). Docs in the same change: `docs/cli.md` § JSON / Value
formatting, `docs/decisions.md` § Export Results.

---

## B1 — CSV/TSV cells starting `=`, `+`, `-`, `@` are written bare (spreadsheet formula injection, CWE-1236)

**Repro** (live):

```sh
gossms query run ... -o csv -q -Q "SELECT N'=1+1' f, N'@SUM(1+1)*cmd|'' /C calc''!A0' g"
# f,g
# =1+1,@SUM(1+1)*cmd|' /C calc'!A0
```

Opened in Excel/LibreOffice the cell is a formula. Query Results To File and
Export Results (grid menus) share the writer, so a database value that an
attacker controls reaches a DBA's spreadsheet as code.

**Cause.** `RecordWriter.needsQuotes` (`internal/export/record.go`) mirrors
`encoding/csv`'s rule (quote, separator, line break, leading space, `\.`) and
nothing about the first character. Quoting does not help here (a quoted `"=1+1"`
is still a formula); the cell text must change.

**Decision needed from the user before any code** (it is a contract change,
not a mechanical fix):

1. **Scope.** `RecordWriter` is also the lossless inverse of `RecordReader`
   (`data import` reads these files, `docs/decisions.md` § Results To File:
   "lossless"). Prefixing `'` changes the data. So the default cannot change
   silently for the CLI or for `data import` round trips.
2. **Recommended shape:** an opt-in `RecordWriter` setting
   (`NeutralizeFormulas bool`), exposed as a Tools > Options checkbox
   ("Export: neutralize spreadsheet formulas", default **on** for the TUI's
   Export Results since a human opens those in a spreadsheet) and a CLI flag
   `--csv-neutralize-formulas` (default off, so pipes and `data import` keep
   lossless output). OWASP's rule: a **string-typed** cell whose text starts
   with `=`, `+`, `-`, `@`, TAB or CR gets a leading `'`. Numeric columns
   (`export.KindOf` == `KindNumber`) are exempt, so `-5` stays `-5`;
   `WriteTable` already receives `types`, `RecordWriter.WriteRecord` does not,
   so the type kinds need threading through (one more constructor option).
3. **Alternative if the user prefers no switch:** leave as is and document it
   in `docs/cli.md` § csv and `docs/decisions.md` as a settled non-goal, so it
   stops being re-raised. A decision either way belongs in `decisions.md`.

**Tests (if implemented).** String cell `=1+1` → `'=1+1` with the option on,
unchanged off; numeric `-5` unchanged on; `RecordReader` round trip pinned for
the off default; TSV same; header row not neutralized only if the user wants
that (OWASP says yes, headers are cell text too — recommend neutralizing them).

---

## Reviewed and clean

Read, probed or fuzzed with no defect found, so they are not worth a re-read:

- **tuikit:** `core/strutil.go` (a 200k-case property probe of `Truncate`,
  `TruncateLeft`, `TruncateLine`, `PadRight/Left`, `WrapText(Limit)` and
  `DisplayWidthAtMost` over combining marks, ZWJ, flags, CJK and tabs: all
  invariants held; the probe file was deleted), `widgets/input_field.go`,
  `controls/editor` undo/redo and the `Document` splice log,
  `editor_search.go` find/replace, `layout/splitter.go` collapse,
  `controls/treeview.go` title row and mouse zones, `dialogs/file_system.go`
  path rules, `SetSourceReordered`.
- **gosmo:** `tsql` quoting and `Spans`, `ScriptCollector.String`,
  `Scripter.triggerState`, `rows.Err()` coverage of every query site.
- **gossms:** `sqltext` lexer, GO separator, `SplitBatches`, `StatementAt`;
  `cli/safety` classifier (a 45-statement probe, including merged statements,
  dynamic SQL, `EXEC` forms and msdb procedures); `config` load/merge;
  `fileutil` atomic writes; the update checker's version compare; completion
  `BULK` options.
- `go test -race -count=1` over all three repos: green, no race reports.

Not exercised: anything needing Azure, Fabric, Linux or Managed Instance (see
`docs/open-threads.md` V2–V8), the Windows clipboard, and the TUI against the
live server beyond start-up.
