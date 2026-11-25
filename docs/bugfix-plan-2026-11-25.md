# Bug-fix plan, 2026-11-25 (silent value changes)

Three defects found (D2 and D3 fixed 2026-11-25; D1 remains) reviewing gossms,
gosmo and tuikit, each **reproduced live on win10cli (17.0)** with
disposable tempdb objects, all dropped afterwards. None is in `docs/open-threads.md`, `docs/decisions.md` or
`~/go/gosmo/OPEN-THREADS.md`. All three change data with no error: a value
or a whole column becomes another and the run reports success. Two are in
gossms's command line, one in gosmo (reached through `gossms data import`).
tuikit needs no change. Close an item by deleting it; delete this file with
the last one. Nobody commits but the user.

| # | Where | Effect | Severity |
|---|---|---|---|
| D1 | gosmo `bulkvalue.go` (`timeConv`) | A `datetimeoffset` whose UTC instant is outside 0001–9999 is bulk-loaded as a different value (a day later, or clamped) | Silent corruption in an import that promises to refuse it |

Each step, in the repo it touches: `go build ./... && go test ./... &&
gofmt -l . && go vet ./... && staticcheck ./... && deadcode -test ./...`;
then gossms's `go test ./...` and the item's live repro with the built
binary (`docs/testing.md`). D1 goes through the `dev-with-local-gosmo`
skill (the `replace` is active).

## D1 — gosmo bulk load stores an out-of-UTC-range `datetimeoffset` as another value

**Cause.** `BulkColumn.Converter` promises to refuse what go-mssqldb would
load wrong. `timeConv` (`~/go/gosmo/bulkvalue.go:463`) range-checks a
`datetimeoffset` by `t.Year()` **in the value's own offset**, but SQL Server
stores the UTC instant plus the offset, and go-mssqldb encodes
`dateTime2(val.In(time.UTC))` (`encodeDateTimeOffset`, v1.12.0
`types.go`). `0001-01-01 00:30 +01:00` is `0000-12-31 23:30` UTC: the day
count goes negative and wraps. `9999-12-31 23:30 -01:00` is year 10000 UTC.
The server refuses both as text (`TRY_CAST(N'0001-01-01 00:30:00 +01:00' AS
datetimeoffset)` is NULL), but the bulk stream is not checked and stores a
different value. gossms reaches it through `data import`
(`internal/query/cellparse.go` `timeParser` accepts both; `data_import.go`
uses `NewCellParser`).

**Repro (live, verified 2026-11-25):** `Database.BulkInsert` into a tempdb
`datetimeoffset(7)` column, one row each, all report `n=1, err=nil`:

| sent | stored |
|---|---|
| `2026-01-01 10:00:00 +01:00` | `2026-01-01 10:00:00.0000000 +01:00` (control) |
| `0001-01-01 00:30:00 +01:00` | `0001-01-02 00:30:00.0000000 +01:00` — a day later |
| `9999-12-31 23:30:00 -01:00` | `9999-12-31 22:59:59.9999999 -01:00` — clamped |

**Fix (gosmo).** In `timeConv`'s `datetimeoffset` arm, check the UTC
instant as well: refuse when `t.UTC()` falls outside `0001-01-01
00:00:00` – `9999-12-31 23:59:59.9999999` with "… is out of range for
datetimeoffset (its UTC time must be within years 1 to 9999)". Check the
offset too while there: SQL Server takes ±14:00 in whole minutes; a
`time.Time` from a zone with a seconds offset (LMT, e.g. Europe/Amsterdam
before 1937) has its offset truncated by `offset /= 60` in the driver, so
the stored local time shifts — refuse a non-minute or beyond-±14:00 offset
rather than store it. Update the `datetimeoffset` line of `Converter`'s doc
comment. No API change; no gossms change needed beyond the `replace`
picking it up (`cellparse.go`'s own `parseOffset` already limits ±14:00).

**Tests (gosmo `bulkvalue_test.go`):** the two out-of-range values above and
a `+00:00:30` LMT offset are refused as `*BulkValueError`; `0001-01-01
00:30 -01:00` (UTC still year 1), `9999-12-31 23:30 +01:00` and the control
pass unchanged. Live: `TestLiveVersionSweep` is unaffected (no read
changed); rerun the repro through `gossms data import` of a one-row CSV into
a tempdb `datetimeoffset` column — refused naming line 2 and the column,
nothing copied. gossms: a `cellparse_test.go` case through
`NewCellParser` for the same value.

---

## Reviewed and clean

Probed rather than read where possible:

- gossms: the safety classifier against ~110 adversarial scripts (merged
  statements without `;`, CTE-led DML, `IF`/`WHILE`/`TRY` nesting, labels,
  `EXEC` forms and qualifications, `OPENQUERY`/`OPENROWSET`/`OPENDATASOURCE`,
  comments ending at `\r`, U+2028/NEL/VT separators, unterminated literals,
  keywords as identifiers) — every one classed at or above its real effect;
  `--param` date and bit literals under `SET LANGUAGE British`; `sqltext`
  lexer and `GoSeparatorAt`; `export` `RecordReader` and formula
  neutralization; `query` executor message loop and cell rendering;
  Object Explorer's new active-database marker and Use Database.
- gosmo: `QuoteNameIfNeeded`/reserved keywords, `SplitName`, collation
  `SameName`/`NameKey`, `bulkvalue` integer/decimal/money/float/char
  converters, index `WITH` clause and partition compression, the new
  catalog `Types` read.
- tuikit: `Truncate`/`TruncateLeft`/`TruncateLine`/`PadRight`/`WrapText`/
  `WrapTextLimit` width invariants over 200 000 random grapheme mixes
  (emoji ZWJ, flags, combining marks, CJK, tabs, CRLF) — none exceeded its
  width; `ParseFormattedNumber`, `ParseClock`, `FormatBytes`, `SafeFileName`;
  charts scale/format/bucket math; `loop.Latest`; `layout.Splitter`
  collapse.
