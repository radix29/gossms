# Bug-fix plan — review of 2026-11-13

Three bugs found reviewing gossms, gosmo and tuikit. Each was reproduced, not
argued: B1 and B2 by a random-operation stress test over the tuikit widgets
(no such test exists in tuikit today — the pure packages are fuzzed, the widget
state machines are not), B3 against the live server (`win10cli`, major 17).

Nothing is implemented. Delete this file once the three items are done.

| ID | Repo | Severity | What |
|---|---|---|---|
| B1 | tuikit (crashes gossms) | High — panic on the UI goroutine, unsaved query text lost | `Editor.Undo()`/`Redo()` leave a stale selection |
| B2 | tuikit (crashes gossms) | Medium — panic, needs a grid under 3 rows high | `DataGrid` PgUp/PgDn page by a negative height |
| B3 | gosmo | Low — wrong stored value, library callers only | float → decimal/money rounds ties to even, SQL Server rounds them away from zero |

## Recommended implementation order

1. **B1** — highest impact, smallest change, reachable from the Edit menu.
2. **B2** — same repo and same test harness as B1; do it in the same sitting.
3. **Stress tests for tuikit** (step T below) — the harness that found B1 and
   B2, landed after both fixes so it goes in green.
4. **B3** — independent, gosmo only; needs one live run.
5. **gossms verification pass** — drive the built binary in tmux for B1 and B2
   (`docs/testing.md`: green tests are not verification).

B1, B2 and T are one tuikit change set; B3 is one gosmo change set. gossms
itself needs no code change: both `replace` directives are active, so it picks
the fixes up on rebuild.

---

## B1 — `Editor.Undo()` / `Redo()` leave a selection pointing outside the document

**Where:** `~/go/tuikit/controls/editor/editor_undo.go` — `applyStep` (line
173), reached by `undo` (189) and `redo` (200); public entry points
`Undo`/`Redo` at `editor_actions.go:64-65`.

**What happens:** `applyStep` replaces lines and restores the cursor, but leaves
`selecting`, `selBlock` and the anchor (`selAnchorRow`/`selAnchorCol`) as they
were. When the step removes rows, the anchor can name a row that no longer
exists. The next thing that reads the selection indexes `doc.Line(row)` out of
range and panics.

Ctrl+Z is not affected: `applyKey` drops the selection after every
non-movement key. The public `Undo()`/`Redo()` skip that, and gossms calls
them from **Edit ▸ Undo / Redo** (`internal/tui/menu.go:240,246`,
`editorUndo`/`editorRedo`).

**Minimal reproduction** (panics at the last line; `SelectedText()` and
`Cut()` panic the same way, `Draw` survives):

```go
e := editor.NewEditor(nil)
e.SetText("abc")
e.SetBounds(0, 0, 40, 10)
e.Focus(true)
e.DuplicateLines()                                         // 2 rows, cursor on row 1
e.HandleKey(tuikittest.Key(tcell.KeyLeft, tcell.ModShift)) // selection anchored on row 1
e.Undo()                                                   // 1 row; HasSelection() still true
e.HandleKey(tuikittest.Rune('x'))                          // index out of range [1] with length 1
```

In gossms: paste several lines at the end of a query, Shift+Up, Edit ▸ Undo
from the menu, then type or copy.

**Fix:** in `applyStep`, after restoring the cursor, drop the selection
(`e.selecting = false`, `e.selBlock = false`). One place covers undo and redo,
key and API paths alike, and matches what Ctrl+Z already does. Do not clamp the
anchor instead: a clamped anchor keeps a selection the user never made over
text that just changed.

`Undo()`/`Redo()` should also finish as the key path does — `desiredCol`
refreshed and `ensureCursorVisible()` — check whether the menu path currently
leaves the caret off screen after undoing an edit far from the viewport, and
fix it in the same two functions if so.

**Tests (tuikit):**
- `TestUndoDropsSelectionItCanNoLongerHold` in `editor_test.go`: the
  reproduction above, then `SelectedText()`, `Cut()` and a typed rune; assert
  no panic and `HasSelection() == false`. Same for `Redo()` (duplicate, undo,
  select on the last row, redo of a step that shrinks the document — e.g.
  delete-lines).
- Block selection variant (`selBlock`) with Alt+Shift+arrow before `Undo()`.

**Verify in gossms:** tmux, the menu sequence above; the panel must survive and
show no selection.

## B2 — `DataGrid` PgUp/PgDn move the selection out of range when the grid is under 3 rows

**Where:** `~/go/tuikit/controls/datagrid.go:313` — `dataRows()` is
`g.rect.H - 3`, negative for a grid 0–2 rows high — and
`controls/datagrid_input.go:130-135`:

```go
case tcell.KeyPgUp:
    g.selRow = max(0, g.selRow-dataH)
case tcell.KeyPgDn:
    g.selRow = min(g.rows.Len()-1, g.selRow+dataH)
```

**What happens:** with `dataH < 0`, PgDn subtracts: `selRow` goes negative
(−1 … −3). PgUp adds: `selRow` can pass `rows.Len()-1`. `ensureVisible`
returns early for `dataH <= 0`, so nothing shows — until the grid grows again
and any key calls `ensureVisible`, which copies the negative `selRow` into
`scrollRow`; `Draw` bounds `dataIdx` only from above and calls
`rows.Row(-1)`: `index out of range [-1]` on the UI goroutine. The comment at
`datagrid_input.go:107-111` describes exactly this panic for the empty-grid
case; the short-grid case was missed. A host that indexes its own slice with
`SelectedRow()` panics earlier.

**Minimal reproduction:**

```go
g := controls.NewDataGrid()
g.SetData([]string{"c"}, [][]string{{"1"}, {"2"}, {"3"}, {"4"}})
g.SetBounds(0, 0, 25, 1)
g.Focus(true)
g.HandleKey(tuikittest.Key(tcell.KeyPgDn)) // SelectedRow() == -2
g.SetBounds(0, 0, 25, 10)
g.HandleKey(tuikittest.Key(tcell.KeyDown))
g.Draw(screen)                             // panics
```

**Reachability in gossms — not yet driven:** needs a focused grid under 3 rows:
a results or Details pane squeezed by the splitter or a very small terminal,
or a key delivered before the first layout. Step 1 of the fix is to try this
in tmux (shrink the terminal until the results grid is 1–2 rows, PgDn, enlarge,
Down) and record whether gossms's layout can produce it. The tuikit fix stands
either way: it is a library, and the panic is in its own `Draw`.

**Fix (both parts):**
1. `dataRows()` returns `max(0, g.rect.H-3)`. Fourteen call sites read it
   (`grep -n "dataRows()" controls/*.go`); each already treats `<= 0` as "no
   rows visible" (`ensureVisible`, `showsVScrollbar`, the wheel and scrollbar
   guards), and `SetScroll`'s bound `rows.Len()-dataRows()` stops exceeding
   `rows.Len()`. Re-read each of the fourteen when making the change.
2. PgUp/PgDn page by `max(1, dataH)`, so the keys still move on a grid with no
   visible data rows instead of doing nothing.

No clamp of `selRow` after the key switch: with (1) and (2) no path produces an
out-of-range row, and a clamp would hide the next one.

**Tests (tuikit):** in `datagrid_zerorect_test.go` (it already covers the
zero-rect cases): for heights 0, 1, 2 and 3, PgDn, PgUp, End, Home each leave
`0 <= SelectedRow() < rows`; then grow the grid, press Down, `Draw` — no
panic.

## T — keep the stress tests that found B1 and B2

Add to tuikit, after B1 and B2 so they land green:

- `controls/editor/editor_monkey_test.go`: seeded `math/rand/v2` (as
  `editor_wrap_test.go` already uses), ~300 seeds × 300 operations drawn from
  keys with modifiers, runes (wide, combining, emoji), mouse events inside and
  outside the rect, `SetBounds`, wrap toggle, `Paste`/`Cut`, every public
  action (`Undo`, `Redo`, `DuplicateLines`, `DeleteLines`, `MoveLinesUp/Down`,
  `IndentLines`, `DedentLines`, `ToggleLineComments`, case transforms,
  `ReplaceText`, `GoToLine`) and search/replace. After every operation: `Draw`
  and `DrawOverlay`, and the cursor inside the document. At the end: undo
  everything restores the initial text. On failure print the seed and the last
  operations.
- `controls/datagrid_monkey_test.go`: same shape over keys, mouse, `SetData`,
  `SetDataPreservingView`, `SetBounds` (including 0–2 rows), cell-cursor and
  browse-only toggles, `ShowValue`; invariant `0 <= SelectedRow() < rows` when
  there are rows.

Run time measured in the scratch version: under 30 s for 3000 editor seeds, 3 s
for 6000 grid seeds — cut the seed counts to keep `go test ./...` fast.

Two things the scratch run also showed, for a decision rather than a fix:

- **`SearchOptions.Replace` holding a line break** puts a raw `'\n'` inside one
  document line (`replaceMatch`, `editor_search.go:550`, inserts the runes into
  the row). gossms's Replace box is a single-line `InputField`, so it cannot
  send one; a library caller can. Either split the replacement into rows or
  refuse it in `SetSearch`.
- The tree, list box and input field ran clean (1000–3000 seeds each).

## B3 — gosmo bulk load: a float bound for decimal or money rounds ties the wrong way

**Where:** `~/go/gosmo/bulkvalue.go:300` — `numberText` formats a float with
`strconv.FormatFloat(v, 'f', places, 64)`; called by `decimalConv` (319) and
`moneyConv` (348).

**What happens:** `FormatFloat` rounds an exact tie to even. SQL Server rounds
it away from zero. `BulkColumn.Converter`'s contract (line 116) says a float is
"rounded to Scale as CAST rounds", and `BulkInsert` exists to load what an
INSERT would store.

| Value → column | gosmo sends | SQL Server stores (live, major 17) |
|---|---|---|
| `2.5` → `decimal(5,0)` | `2` | `3` |
| `-2.5` → `decimal(5,0)` | `-2` | `-3` |
| `0.125` → `decimal(5,2)` | `0.12` | `0.13` |
| `0.03125` → `money` | `0.0312` | `0.0313` |
| `0.375` → `decimal(5,2)` | `0.38` | `0.38` (agree) |
| `1.005` → `decimal(5,2)` | `1.00` | `1.00` (agree: the float is below the tie) |

Server side: `SELECT CAST(2.5e0 AS decimal(5,0)), CAST(0.125e0 AS decimal(5,2)),
CAST(0.03125e0 AS money)`.

**Scope:** only a `float64`/`float32` value whose binary value is an exact
decimal tie at the column's scale. Text is unaffected (more places than the
scale is refused, never rounded), so gossms's `data import` — which sends cell
text through `query.NewCellParser` — is not exposed. Other gosmo callers are.

**Fix:** in `numberText`, round a float half away from zero. `FormatFloat`
already rounds the exact binary value, so only the tie differs:

1. Keep `FormatFloat(v, 'f', places, 64)` as the fast path.
2. A tie is possible only when the shortest round-trip text
   (`FormatFloat(v, 'f', -1, 64)`) has exactly `places+1` fractional digits
   ending in `5`. Only then confirm it is exact (`new(big.Rat).SetFloat64(v)`
   equals the parsed text) and, if so, round the magnitude up: add one unit in
   the last kept place to the truncated digits, carrying.
3. `1.005` is the regression case: its shortest text ends in 5 but the value is
   below the tie, so the exactness check must keep it at `1.00`.

No API change, so no compatibility break is needed. Update the `Converter` doc
comment only if the wording changes.

**Tests (gosmo):**
- Table test beside the existing bulk-value tests: every row of the table
  above for decimal and money, plus negative money, `0.5 → decimal(5,0)` = `1`,
  and a carry (`9.995` at scale 2 is below the tie → `9.99`; `0.5` at scale 0
  with precision 1; `99.5 → decimal(3,0)` = `100`, `999.5 → decimal(3,0)`
  refused as not fitting).
- Idempotence still holds: the result is text, passed again unchanged.
- Live (`live_*_test.go` style, disposable table): bulk-load the tie values as
  floats into `decimal(5,2)`, `decimal(5,0)` and `money`, INSERT the same
  floats as parameters into a second table, and compare the two tables
  row for row. Run on 17 and on the oldest instance (13).

## Done means

- tuikit: `go test ./...`, `go test -race -count=1 ./controls/...`,
  `go vet`, `staticcheck`, `deadcode -test` clean.
- gosmo: `go test ./...` plus the live comparison on 17 and 13.
- gossms: `go build`, `go test ./...`, and the tmux pass for B1 (Edit ▸ Undo
  with a selection) and B2 (short grid, PgDn).
- This file deleted in the commit that closes the last item.
