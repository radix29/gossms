# tuikit review, round 2: three bugs and how to fix them (2026-11-19)

These bugs come from reading tuikit at `6ab9a24`, after the first review's fixes landed
(tuikit PR #7, merged as `6ab9a24`; its five bugs are excluded here). A throwaway probe
test reproduced each bug and was deleted afterwards. The probe output is quoted under each
bug. None of the three is listed in `docs/open-threads.md` or settled in `docs/decisions.md`.
Backward compatibility may be broken where a fix needs it. Each break is called out under
its bug.

Each fix lands in tuikit first. Build and test there (`go build ./... && go vet ./...`,
`go test ./...`, `staticcheck ./...`), then build goSSMS against it through the active
`replace` and drive the widget in the binary, as `docs/testing.md` describes. Green unit
tests are not verification.

## Recommended implementation order

1. **Bug 1: the editor's line-state cache stops replaying too early** (high). After an
   Undo of Ctrl+/ over two or more lines, code can draw as a comment or a comment as code.
   A user can then misread what a batch will run. The fix stays inside the editor package
   and changes no exported API, so it goes first.
2. **Bug 2: FileDialog Tab completion is case-sensitive on Windows hosts** (medium). On a
   miss, Tab silently moves focus instead of completing. It shows up in every
   Backup/Restore browse against a Windows SQL Server. The fix breaks the `PathRules`
   interface, so goSSMS's `serverFS` has to be checked in the same pass.
3. **Bug 3: a dragged Splitter bar lands one cell off the pointer** (low, cosmetic). It is
   a small, self-contained diff and can go last or ride along with either of the others.

The three bugs are independent. The order follows severity and blast radius.

---

## Bug 1: `prefixStates` stops at convergence inside the edited span

**Where:** `controls/editor/common.go`: `prefixStates.at` (the `c.version+1 ==
doc.Version() && doc.dirtyFrom > 0` branch) and `replay` (`stopOnConverge`).
`controls/editor/document.go`: `replaceRange`, `touch`, and the `dirtyTo` field and its
comment. Every `LineStateAt` user goes through this code: goSSMS's T-SQL highlighter
(`internal/tui/sqledit/highlighter.go`, `lineStatesKey`) and tuikit's
`XMLHighlighter`.

**What is wrong.** When the cache is exactly one version behind, `at` resumes from
`dirtyFrom` and stops the replay at the first line `i > from` whose cached entering state
equals the newly computed one. That shortcut is sound only if line `from` is the only line
that changed, which holds for `setLine`. `replaceRange` also leaves `dirtyFrom = row > 0`
when it swaps a span of n lines for n other lines. In that case the entering state of
line `row+1` can match while lines `row+1 … row+n-1` themselves changed, so every state
below them stays stale. The `dirtyTo` comment says the opposite ("an over-wide range costs
time, not correctness"), and nothing reads `dirtyTo` at all.

Same-count `replaceRange` calls with a span of two or more lines come from:

- **Undo and Redo** of any multi-line step (`applyStep`). Ctrl+/ over a selection is the
  common case: Ctrl+/ itself goes through `setLine` per line, and each call bumps the
  version, so the forward edit does a full replay and is correct. Its undo is one splice.
- **Move Lines** (Ctrl+Shift+Up/Down, `MoveLinesUp` and `MoveLinesDown`).
- **`ReplaceSelectedLines`** (goSSMS's Format Selection, `format_document.go`) when the
  formatter keeps the line count.

**Probe.** A toy `/* */` and `--` state machine. The text is `SELECT 0`, `x = 1`,
`/* open`, `close */`, `SELECT 2`. The probe selects rows 1–2, presses Ctrl+/, then Undo:

```
after Ctrl+/: states=[false false false false false]
after undo:   states=[false false false false false] fresh=[false false false true false]
```

After the undo, `close */` is inside the block comment again, but the cache still says it
begins in code, and it stays wrong until a later mutation forces a full replay. In goSSMS
the line is coloured as code. The opposite case also happens: a different text makes live
code draw as a comment.

**Fix.** Stop relying on the single-mutation `dirtyFrom` for this cache. Use the splice
log that the wrap and search caches already use:

1. In `prefixStates.at`, call `doc.changedSince(c.version)` whenever the cached document
   is the same but the version differs. If it returns `(row, oldN, newN, true)`, splice
   the states slice so it follows the new line count: `slices.Replace(c.states, row,
   row+oldN, make([]S, newN)...)`. Then replay from `row`, allowing a convergence stop
   only once `i >= row+newN`, past the last line the edit wrote. If `ok` is false, replay
   from 0 as today.
2. Change `replay`'s `stopOnConverge bool` into a `convergeFrom int`. Convergence is
   checked only for `i >= convergeFrom`, and `len(lines)` disables it.
3. Drop the `len(c.states) != doc.Len()` start-over test. The splice makes resuming
   across a line-count change sound, which also makes Enter and Backspace-join cheap.
   Keep the `c.doc != doc` and `!c.valid` tests.
4. `Document.dirtyTo` is now dead. Remove it and fix the `dirtyFrom` comment, which points
   at `prefixStates`. Keep `dirtyFrom`, because `TextRevision.DirtyFrom` feeds goSSMS's
   `sqlparse.PrefixCache`. That cache only needs a prefix and has no convergence step, so
   it is correct as it stands. Update the cross-reference comment in
   `internal/tui/sqlparse/prefix_cache.go` ("The invalidation rule is
   controls.prefixStates'"), which will no longer be true.

**Compatibility:** nothing exported changes.

**Tests (tuikit):**
- At the Document level: a same-count `replaceRange` of two lines at `row >= 1` where
  convergence happens inside the span. Assert every state matches a fresh replay.
- At the Editor level: the probe above (Ctrl+/ over two lines, Undo), plus
  `MoveLinesDown` over a `/*` line.
- A monkey test: random `setLine`/`replaceRange`/`edit` calls with a random number of
  `LineStateAt` reads in between, checking the cached states against a fresh replay
  after each step. Also run it against `XMLHighlighter`'s cache.

**Live check (goSSMS):** in a query editor, type the five-line script above, select
lines 2–3, press Ctrl+/ and then Ctrl+Z. `close */` must be comment-coloured. Repeat with
Ctrl+Shift+Down moving a `/* open` line. Read colours with `tmux capture-pane -e`.

---

## Bug 2: FileDialog Tab completion ignores the host's case rules

**Where:** `dialogs/file_dialog_complete.go`: `completeField` (`strings.HasPrefix(e.Name,
base)`, `common == base`, `searchDir != d.dir`) and `commonPrefix`. The caller is
`dialogs/file_dialog_input.go`, `KeyTab`, which cycles focus when `completeField`
returns false.

**What is wrong.** The prefix match and the common-prefix computation compare bytes
exactly. Windows file names, and the macOS default, are case-insensitive, and goSSMS
browses the SQL Server host's file system through `WindowsPathRules` on Windows servers.
So typing `health` and pressing Tab finds no candidate next to `HealthClinic_full.bak`.
`completeField` then returns false, and Tab **moves focus to the next field**. The user
loses the completion and also gets thrown out of the field. Typing `c:\back` in the path
field fails the same way, against `Backup`.

**Probe** (fake Windows FS from `file_dialog_remote_test.go`, dialog shown on `C:\Backup`):

```
name="health" focus 2 -> 3          (Tab moved focus; no completion)
path complete ok=false value="c:\\back"
```

**Fix.**
1. Add `FoldsCase() bool` to `PathRules`. `WindowsPathRules` returns true and
   `PosixPathRules` returns false. `LocalFileSystem` returns `runtime.GOOS == "windows"
   || runtime.GOOS == "darwin"`. tuikit's dependency rule allows `runtime`, and
   `LocalFileSystem` is already OS-dependent through `path/filepath`.
2. In `completeField`, when `fs.FoldsCase()`, match candidates on a case-folded rune
   prefix. Compare `len([]rune(base))` runes with `strings.EqualFold`; don't
   `strings.ToLower` both strings, because folding can change byte lengths. Then make
   `commonPrefix` fold-aware: it takes a fold flag, compares runes with
   `unicode.SimpleFold` equivalence, and returns the prefix in the **first candidate's
   on-disk casing**.
3. Replace the `common == base` no-op test with an exact comparison of the completed
   value against the field's current value. That way Tab on `backup` with a unique
   `Backup` directory still rewrites the value to `Backup\` and counts as a completion.
4. With a single candidate, take `IsDir` from the matched `FileEntry` instead of calling
   `fs.Exists`. That saves one round trip on a remote host, where each call blocks the
   event loop (see `BlockingFileSystem`).
5. Compare `searchDir != d.dir` with folding too, so `c:\backup\x` on a folding host
   doesn't re-`List` a directory the dialog already holds.

**Compatibility:** `PathRules` (and so `FileSystem`) gains a method. Any outside
implementer that doesn't embed `WindowsPathRules`/`PosixPathRules` must add it. goSSMS's
`serverFS` embeds `dialogs.PathRules` (`internal/tui/server_filesystem.go`), so it picks
up the method from whichever rules it was given. Confirm it builds.

**Tests (tuikit):** on the fake Windows FS: `health`+Tab → `HealthClinic_full.bak` and
focus unchanged; `c:\back` → `c:\Backup\`; Tab on `backup` → `Backup\`; an ambiguous
folded prefix extends only to the shared folded prefix. On `PosixPathRules`, `health`
still does not complete. The existing completion tests keep passing.

**Live check (goSSMS):** Backup Database against `win10cli\SQL2017` (Windows host) →
Destination → browse. In the path field type `c:\program` and press Tab: the field must
complete to `c:\Program Files\` (or stop at the shared prefix, if several entries match)
and keep focus. Against the Linux test server, `Var`+Tab must not complete `var`.

---

## Bug 3: Splitter drag puts the bar one cell off the pointer

**Where:** `layout/splitter.go`: `HandleMouse` (`sp.ratio = ratioBase + delta/W`),
`SplitPos` (`int(float64(W) * ratio)`), and `HandleKey` (a fixed 0.05 ratio step).

**What is wrong.** A drag stores `ratioBase + (pointer - dragBase)/W`, and `SplitPos`
floors `W*ratio`. Floating-point error leaves `W*ratio` just below the integer the pointer
is on, often enough to matter, and the floor then lands one cell short. In an 80-column
layout starting from goSSMS's Object Explorer ratio (0.3, `internal/tui/app.go`), 10
target columns are off by one. From 0.7, 33 of the 64 reachable columns are. The bar then
trails the pointer, and some columns can't be reached in one drag at all.

Two smaller problems sit in the same code:

- **The keyboard step can't move the bar on a narrow pane.** The fixed 0.05 step is half
  a cell on a 10-wide split, so some Ctrl+Left/Right presses don't move it.
- **The bar can sit outside the split.** `SetLimits` accepts `maxRatio = 1`, and
  `SplitPos` then returns `X+W`. The bar draws outside the rect and `SecondRect` gets
  `W = -1`.

**Probe** (vertical splitter at 0.3, bounds 80 wide, press on the bar, drag across
columns 9–71):

```
start=24, pointer column != bar column at [11 13 16 22 27 31 47 52 57 62]
```

**Fix.**
1. Track the drag in cells. On press, record `basePos = SplitPos()`. On motion, set
   `target = basePos + (pointer - dragBase)` and store `ratio = (float64(target-origin) +
   0.5) / float64(size)`. The half cell puts the ratio at the cell's centre, so the floor
   in `SplitPos` returns `target` exactly. Then clamp to the limits as today. Put this in
   one `setPos(target int)` helper.
2. Run the keyboard step through `setPos` too:
   `SplitPos() ± max(1, round(0.05*size))` cells, so every press moves the bar at least
   one cell.
3. Clamp `SplitPos` to `[origin, origin+size-1]` so no ratio puts the bar outside the
   rect.

**Compatibility:** `Ratio()` after a drag now reads as a cell centre (for example
`0.30625` instead of `0.3`). Callers that persist the ratio see slightly different
numbers that restore the same position. No exported signature changes.

**Tests (tuikit):** drag to every column for widths 10–300 and start ratios 0.25, 0.3,
0.5 and 0.7, both directions. Assert `SplitPos() == pointer` whenever the pointer is
within the limits. On a 10-wide split, Ctrl+Right moves the bar at least one cell. With
`SetLimits(0, 1)` and `SetRatio(1)`, the bar stays inside the rect and `SecondRect().W >=
0`.

**Live check (goSSMS):** drag the Object Explorer divider with tmux mouse events (see the
TUI harness notes) to columns 11, 13, 16 and 22 of an 80-column terminal. The divider must
sit exactly under the pointer each time. Repeat for the results splitter (horizontal,
0.5).

---

## Also noticed (not counted, lower value)

- **Ctrl+letter bindings depend on a host-side fold.** tuikit's widgets match the legacy
  `tcell.KeyCtrlA…KeyCtrlZ` codes (`InputField` Ctrl+A/Ctrl+U, the Editor's key table,
  `PropertySheet` Ctrl+Z). Under tcell's advanced key reporting (`NewEventKeyEx`), and
  for any Ctrl+Shift chord, these arrive as `KeyRune` plus `ModCtrl`. goSSMS folds them
  back in `internal/tui/app_events.go` (`normalizeCtrlRune`), but another tuikit host
  gets dead Ctrl shortcuts unless it copies that function. A fix would move the fold into
  `core` (for example a `core.NormalizeKey`) and document that hosts must call it.
- **`charts.Series.At` left-aligns a shorter series.** `HistoryChart`/`StackedHistory`
  index every series by the longest series' bucket. A series that is shorter because it
  started later (a rate that needs two samples) has its newest sample drawn one or more
  columns left of "now". goSSMS's Activity Monitor feeds equal-length series, so it never
  shows. Right-aligning (`At(i - (maxLen - len))`) would match the "newest at the right
  edge" contract.
