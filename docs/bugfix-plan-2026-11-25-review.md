# Bug-fix plan — review of gossms, gosmo and tuikit (2026-11-25)

Three bugs, each reproduced against the current trees (gossms `32f3c5d`,
gosmo and tuikit at their working-tree HEADs). None is in
`docs/open-threads.md`, `~/go/gosmo/OPEN-THREADS.md` or `docs/decisions.md`.
Back-compatibility breaks in gosmo and tuikit are allowed; none of the fixes
below needs one.

| ID | Repo | Severity | Summary |
|----|------|----------|---------|
| B1 | tuikit | crash | A block (column) edit that removes a wide glyph leaves the caret past the row end; the next typed key panics |
| B2 | gossms | wrong result, can cut a write short | `--timeout` overflows `time.Duration`: a large value times out at once or after a fraction of a second |
| B3 | gosmo + gossms | correct values refused | Trailing fractional zeros count as precision: `1.50` is refused for `decimal(5,1)`, `12.5000` for `decimal(10,2)`, `12.50000` for `money`, `10:00:00.1230000` for `time(3)` |

## Recommended implementation order

1. **B1 (tuikit)**: the only crash. Small fix in one function, no API change,
   and no other item depends on it.
2. **B2 (gossms CLI)**: a single function plus a doc line. It fixes a wrapped
   timeout that can cut `db set` (no transaction) off partway.
3. **B3 (gosmo, then gossms)**: crosses repositories and changes documented
   behaviour, so it goes last: gosmo's converter first (it is also what
   `--param` money uses), then gossms's `cellparse`, then `docs/cli.md`.

Each step is its own commit (the user commits). Read `docs/testing.md` before
calling any of them done; each needs a run of the built binary, not only
green tests.

---

## B1 — tuikit editor: a block edit leaves the caret past the row end, then panics

### Symptom

```
row 0: 👍/      (2 runes, 3 columns)
row 1: 'a漢     (3 runes, 4 columns), caret at its end
```

Alt+Shift+Up makes a block over display column [3,4). Backspace (or Delete,
Shift+Delete, Ctrl+Backspace, Ctrl+Delete) collapses the block. On row 1 the
range [3,4) is the right half of `漢`, so the whole glyph goes and the row
becomes `'a` (2 runes). The caret is left at rune column **3** on that 2-rune
row. Then:

- **Esc, then any typed character: `panic: index out of range [3] with length 3`.**
  In gossms this panic happens on the UI goroutine of the query editor.
- Enter splits the row in the wrong place (`'a` moves down whole, leaving an
  empty row above it).

Found by running tuikit's own editor monkey past its 300 seeds:
seeds 1557, 2173, 3037, 3565, 4294 and 6904 all fail with
`cursor col N outside [0,N-1]`, each after a block Backspace or Delete.

### Cause

`controls/editor/editor_block.go`, `setBlockCaret` (line 42) stores each
corner as `core.RuneIndexAtColumn(line, x)`. Past a row's end that function
returns a **virtual** index (`len(line) + (x - width)`, its documented
round-trip with `ColumnOfRune`), so a row narrower than `x` gets a caret past
its end. `blockCollapse` (line 85), `blockBackspace` (line 128) and `blockDelete`
(line 163) all end in `setBlockCaret`. Deleting a glyph that straddles the
block's left edge is how a row ends up narrower than `x`. `blockPadLine` already
clamps with `min(core.RuneIndexAtColumn(line, x), len(line))`;
`setBlockCaret` does not.

### Fix

In `setBlockCaret`, clamp both corners to their row's length, as
`blockPadLine` does:

```go
e.selAnchorRow, e.selAnchorCol = top, min(core.RuneIndexAtColumn(e.doc.Line(top), x), len(e.doc.Line(top)))
e.cursorRow, e.cursorCol = bot, min(core.RuneIndexAtColumn(e.doc.Line(bot), x), len(e.doc.Line(bot)))
```

`blockCaret.x` keeps the real column, so the zero-width block stays at `x`.
`blockCaretX` compares the stored corners, which are now the clamped ones, so it
still matches. The next block insert pads short rows out to `x` through
`blockPadLine`, so the block stays rectangular. Update the doc comment: "the
corners are stored as the rune index at x on their own rows, clamped to the
row's end".

Check afterwards whether any caller relied on the virtual past-end corner: grep
`blockCaret`, `selAnchorCol` and `cursorCol` in `editor_block.go` and
`editor_selection.go`. The editor monkey's `check` already holds the cursor to
`[0, len]` and lets only a block anchor go past the end.

### Tests (tuikit)

- `controls/editor`: a unit test for the exact sequence above (SetText,
  Down, End, Alt+Shift+Up, then each of Backspace / Delete / Ctrl+Backspace),
  asserting the cursor stays within its row. Follow it with Esc and a typed rune,
  and assert there is no panic and the text is `👍/\n'ax\n/`.
- Leave `editorMonkeySeeds` at 300 for CI time. Run seeds 300–9000 once by hand
  after the fix; every one listed above must pass.
- `go test -race ./controls/editor/...`, `staticcheck ./...`.

### Verify

Build gossms against the tree (the `replace` is active). In tmux, type the
two rows into a query editor, make a block with Alt+Shift+Up (or
Alt+mouse-drag if the terminal eats Alt+Shift+arrows), press Backspace, Esc,
then type. The editor must not crash and the character must land at the end of
`'a`.

---

## B2 — gossms CLI: `--timeout` overflows

### Symptom

Against win10cli, read-only:

```
$ gossms query run -Q "SELECT 1" --timeout 9300000000 …
STATUS: ERROR  ERROR_CODE: TIMEOUT
MESSAGE: the script ran longer than --timeout -9146744073 s and was cancelled   (exit 6)
```

`--timeout 99999999999` happens to wrap to a large positive value and runs.
`--timeout 18446744074` wraps to about 0.29 s. A caller (a script, an agent)
that passes a big number to mean "effectively no limit" gets an immediate
TIMEOUT or an arbitrary short one. `db set` runs one `ALTER DATABASE` per
`--option` with no transaction, so a short wrapped timeout can stop it after
some options have been applied.

### Cause

`internal/cli/cmd/query.go:330`, `readTimeout`: `time.Duration(secs) *
time.Second` with `secs` only checked `>= 0`. It is shared by `query`, `plan`,
`data` (top/count/…), `data export`, `data import`, `data profile` and `db
set`. Every other seconds option is already bounded: `--connect-timeout` (1–30),
`--duration`, `--interval`, `--wait` (`maxJobWait`). The TUI's equivalents are
too (`propsheet.Int` ranges, at most 2³¹ s).

### Fix

Bound `--timeout` in `readTimeout`: refuse anything above a ceiling with
`USAGE_ERROR`, exit 2, before connecting. A good ceiling is `2147483647`
(int32 seconds, about 68 years, well inside `time.Duration`). The hint says
`0 means no limit.` A refusal is better than silently clamping, which is the
CLI's convention for a value it cannot honour (see `--connect-timeout`). Name
the range in `timeoutOption`'s help text and in `docs/cli.md` § Running T-SQL
(the `--timeout <sec>` row): "0 to 2147483647; 0 no limit".

### Tests (gossms)

- `internal/cli/cmd`: `--timeout 9300000000` and `--timeout 18446744074` are
  `USAGE_ERROR` exit 2 with nothing dialled, for `query run` and one other
  command sharing `readTimeout` (`data count`). `--timeout 2147483647` is
  accepted. The agent schema/guide tests pick up the help text change. Rerun
  `TestAgentGuide*` and the schema golden if one exists.

### Verify

`go build -o gossms ./cmd/gossms`. Run the three values above against
win10cli with `SELECT 1` (read-only): exit 2, exit 2, exit 0.

---

## B3 — exact values refused for trailing fractional zeros

### Symptom

gosmo's `BulkColumn.Converter` (`bulkvalue.go`) counts the fractional digits
as written, trailing zeros included:

| Column | Value | Result today | Server's `CAST` |
|---|---|---|---|
| `decimal(5,1)` | `1.50` | `1.50 has more than 1 decimal places` | `1.5`, exact |
| `decimal(10,2)` | `12.5000` | refused | `12.50`, exact |
| `decimal(3,0)` | `100.0` | refused | `100`, exact |
| `money` | `12.50000` | `has more than 4 decimal places` | `12.5000`, exact |
| `smallmoney` | `1.000000` | refused | exact |

None of these would be rounded, so the refusal guards against nothing. Where it
shows:

- **`data import`**: a CSV/TSV/JSONL exported from a wider-scale column
  (`decimal(18,4)` writes `12.5000`) does not load into a `decimal(18,2)`
  column, and neither does any hand-written `10.50` for scale 1.
- **`query run --param x=12.50000:money`** is refused (live, win10cli). The
  same command's `decimal` check (`decimalFits`, `internal/cli/cmd/param.go`)
  already ignores trailing zeros, and `docs/cli.md` § Parameters says so
  ("leading and trailing zeros do not count"). So `money` and `decimal`
  disagree within one command.
- **The same rule in gossms's own layer**: `internal/query/cellparse.go:106`
  refuses `c.fracDigits > maxFrac` for date/time cells, counting trailing
  zeros. `10:00:00.1230000` does not load into `time(3)`, and an export from
  `datetime2(7)` does not load into `datetime2(3)`, though every value is
  exact.

`docs/cli.md` § Data (Conversion) defines the rule as "more fractional digits
than the column keeps is an error, **not rounding**". A trailing zero never
rounds anything.

### Fix — gosmo (`~/go/gosmo/bulkvalue.go`)

- `decimalConv` (line 351): measure `strings.TrimRight(frac, "0")` against
  `scale`. If the trimmed text fits, send it trimmed down to at most `scale`
  digits (`frac = trimmed`), so go-mssqldb never sees more digits than the
  column's scale. `decimalText` stays as is. The converter stays idempotent
  (its documented contract): its output, passed in again, is unchanged.
- `moneyConv` (line 380): the same for the 4-place check (line 395). Build
  `digits` (line 398) from the trimmed `frac`.
- Update `Converter`'s doc comment: "decimal text … with no more than Scale
  significant decimal places (trailing zeros do not count)", and the same for
  money.
- Tests (`bulkvalue_test.go`): the five rows above accepted, with their sent
  text (`1.5`, `12.5`, `100`, `12.5`, `1`). `1.234` for `(5,2)` and
  `1.23456` for money still refused. An idempotency case: convert twice and get
  the same result.
- gosmo `CHANGELOG.md`/`RELEASE.md`: follow `~/go/gosmo/CLAUDE.md`'s release
  rules. Mention it to the user rather than editing release notes unasked.

### Fix — gossms

- `internal/query/cellparse.go`, `cutClock`: keep `fracDigits` as the digits
  written (it parses `nsec`), and compare the **significant** count (trailing
  zeros trimmed) against `maxFrac` in `timeParser` (line 106). The
  `datetime`/`smalldatetime` cap of 3 stays as it is.
- `internal/cli/cmd/param.go`: nothing to change. `moneyFits` gets the gosmo fix
  for free. Leave a test there pinning `12.50000:money` as accepted, so the two
  checks cannot drift apart again.
- `docs/cli.md`: § Parameters (line ~913), the money clause: "has more than
  four decimals (trailing zeros do not count)". § Data, Conversion (line
  ~1266): "a fractional digit the column cannot keep is an error, not
  rounding; trailing zeros do not count".
- Tests: `internal/query` cellparse cases (`10:00:00.1230000` into `time(3)`
  accepted, `10:00:00.1234` refused; `2026-01-02 10:00:00.120` into
  `datetime` accepted). `internal/cli/cmd` data import offline test, if the
  gosmotest fake covers it: a CSV with `12.5000` into a `decimal(10,2)`
  column loads.

### Verify

Live on win10cli, with the user's go-ahead and the disposable-object rule:
create a throwaway database with a table `(d decimal(10,2), m money, t
time(3))`. `data import` a CSV holding `12.5000,12.50000,10:00:00.1230000`,
read the row back, then drop the database. Also run `query run --param
m=12.50000:money -Q "SELECT @m"` (read-only).

---

## Reviewed and not reported

Checked during the review and found sound, or already settled or documented:
the CLI safety classifier (about 60 hand-written evasions, plus 90 s of
`FuzzClassify`), the `--param` parser's date/time/bit/GUID/code-page checks,
`tsql.Spans`/`UnquoteName`/`SplitName`, `SameName`/`NameKey`, `PlanRestore`,
`RestoreRelocation.Moves`, server-path helpers, `RecordReader`/`RecordWriter`,
`textenc`, the clipboard `Dispatcher`, tuikit's width/wrap helpers (200k random
cases), `InputField` (30k monkey seeds), the DataGrid monkey (19k seeds) and
prefix-state monkeys (6k seeds). Fuzzers run past CI length, all clean:
`FuzzFormatSQL`, `FuzzLexersAgree`, `FuzzScope`, `FuzzSQLExpressionCompletion`,
`FuzzRecordReader`, `FuzzParseFilter` and gosmo `FuzzNames`. Known and documented,
so not reported: `GO /* c */` is not a separator, a procedure or dynamic SQL
ending the wrapping transaction, and server-side rounding of
`time`/`datetime2` parameters.
