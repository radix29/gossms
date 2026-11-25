# Bug-fix plan, 2026-11-25 (silent value changes)

Three defects found (D2 fixed 2026-11-25; two remain) reviewing gossms,
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
| D3 | `internal/cli/cmd/param.go` (`parseParam`) | `--param amount=12,50:money` runs with 1250.00; `$5` is accepted too | Wrong value sent, no error |

Each step, in the repo it touches: `go build ./... && go test ./... &&
gofmt -l . && go vet ./... && staticcheck ./... && deadcode -test ./...`;
then gossms's `go test ./...` and the item's live repro with the built
binary (`docs/testing.md`). D1 goes through the `dev-with-local-gosmo`
skill (the `replace` is active). Update `docs/cli.md` in the same change as
D3.

## Recommended implementation order

1. **D3** — a few lines in `parseParam`, gossms only; wrong rows matched or
   written with no error.
2. **D1** — a gosmo change (tag/`replace` discipline, a library others use),
   and the rarest input (an offset that pushes the instant past year 1 or
   9999); do it last so the gossms-only fixes are not held behind it.

---

## D3 — `--param` sends a mis-read `money`/`smallmoney` value

**Cause.** `parseParam` (`internal/cli/cmd/param.go`) checks a `decimal`/
`numeric` value (`decimalFits`, line 136), a date and a bit, and leaves every
other type to the server, documenting (`docs/cli.md` § Parameters) that "a
value that does not convert is `SQL_ERROR` (Msg 8114)". That holds for `int`,
`decimal`, `float` and `real`, which refuse `1,5` and `$5`, but **not for
`money`/`smallmoney`**: SQL Server's string-to-money conversion ignores commas
anywhere and accepts a currency symbol. A European decimal comma becomes a
value 100 times larger.

**Repro (live, verified 2026-11-25, through `sp_executesql` exactly as
`parameterize` sends it):**

| type | value | becomes |
|---|---|---|
| `money` | `1,5` | `15.00` |
| `money` | `12,50` | `1250.00` |
| `money` | `1,2,3.5` | `123.50` |
| `money` | `$5` | `5.00` |
| `smallmoney` | `1,5` | `15.00` |
| `int`, `bigint`, `decimal(9,2)`, `float`, `real` | `1,5` / `1,000` / `$5` | Msg 8114/245 |

So `gossms query run -Q "UPDATE dbo.acct SET bal = @a WHERE id = 1" --param
a=12,50:money --allow write` writes 1250.00.

**Fix.** Before quoting, check `money`/`smallmoney` values with gosmo's own
money check, so the CLI, `data import` and gosmo agree on what a money
amount is: `gosmo.BulkColumn{Type: m[1]}.Converter()` refuses anything but
`[+-]digits[.digits]`, more than four decimals, and out-of-range values
(`moneyConv`, `~/go/gosmo/bulkvalue.go`). Its message (`"12,50" is not a money
amount`) becomes the `USAGE_ERROR`, with the hint "Write the amount with a
point and no grouping or currency sign: 1250.00 or 12.50." Rejecting more
than four decimals is a tightening of the documented "`money` rounds to its
own precision": recommended (it is what `decimalFits` already does for
`decimal`, and what `data import` does for money); drop that sentence for
money in `docs/cli.md`. Ask the user if rounding should stay allowed — then
check only the shape (`paramDecimal`) and range.

*Optional, same change:* run `int`/`bigint`/`smallint`/`tinyint`/`float`/
`real` through the same `Converter` for a `USAGE_ERROR` before connecting
instead of the server's Msg 8114 after it — not a correctness fix (the
server refuses them), a better error.

**Tests** (`internal/cli/cmd/param_test.go`, `TestParseParamRefuses`):
`a=1,5:money`, `a=12,50:smallmoney`, `a=$5:money`, `a=1.23456:money`,
`a=214749:smallmoney` (out of range) are `USAGE_ERROR`; `a=-12.50:money`,
`a=1250:money`, `a=+0.0001:smallmoney` pass, quoted as written. Live: the
`UPDATE` above is refused before connecting; `docs/cli.md` § Parameters
updated (the "does not convert" bullet and the rounding sentence).

---

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
