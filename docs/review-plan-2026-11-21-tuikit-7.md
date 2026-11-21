# tuikit review, round 7: five bugs and how to fix them (2026-11-21)

These bugs come from reading tuikit at `c05922a` (PR #14, `fix/tuikit-bugs-6`, merged).
A throwaway probe test reproduced each one and was deleted afterwards. The probe output
is quoted under each bug. None of the five is listed in `docs/open-threads.md` or settled
in `docs/decisions.md`. Backward compatibility may be broken where a fix needs it, and
each break is called out under its bug.

Each fix lands in tuikit first. Build and test there (`go build ./... && go vet ./...`,
`go test ./...`, `staticcheck ./...`). Then build goSSMS against it through the active
`replace` and drive the widget in the binary, as `docs/testing.md` describes. Green unit
tests are not verification.

## Recommended implementation order

1. **Bug 1: TreeView leaves blank rows under a collapse while rows hide above it**
   (medium). Object Explorer shows it whenever the user collapses a big node near the
   bottom. The fix is one clamp in one function, with no API change.
2. **Bug 2: re-sorting a DataGrid drops the Ctrl+click row selection** (medium). The
   user marks rows, clicks a header, and then Copy takes only the cursor row. The fix
   stays inside `controls` and changes no API.
3. **Bug 3: FieldRing presses a disabled button** (medium as an API trap, latent in
   goSSMS today). The fix is small but changes `ButtonRowKey`/`ButtonsKey`
   semantics and, optionally, the signature of `ButtonClicked`. Check goSSMS callers in
   the same pass.
4. **Bug 4: ClipScreen lets a wide glyph spill one column past the clip** (low-medium).
   A clamped dialog holding CJK text loses its right border cell. The fix is
   self-contained in `core/clip_screen.go`.
5. **Bug 5: WrapHanging splits grapheme clusters** (low). In goSSMS it affects only
   the F1 help text, which is ASCII. It is a correctness bug for any other caller of
   `TextDialog`. Self-contained.

The five bugs are independent. The order follows user impact, then blast radius.

---

## Bug 1: `TreeView.SetNodes` keeps the selection's screen line even when that leaves blank rows

**Where:** `controls/treeview.go`: `SetNodes` (`tv.scroll = max(0, i-line)`) and
`ensureVisible`. It is reached through every `SetNodes` caller, including
`TreeModel.Rebuild`, so goSSMS's Object Explorer hits it on every collapse and every
reload that shrinks a subtree.

**What is wrong.** `SetNodes` keeps the selected row on the same screen line, which is
right when rows are inserted or removed *above* it. Nothing caps `scroll` at
`len(nodes)-h`, though. `ensureVisible` only keeps the selection inside the view, and
`SetNodes` handles only the extreme case `scroll >= len`. After a collapse that removes
rows *below* the selection, the view keeps its offset. The bottom of the pane goes
blank while rows stay hidden above, and the scrollbar thumb sits in the lower half.
`ListBox.ensureVisible` already has this clamp (`maxScroll := len(items) - rect.H`).
TreeView's does not.

**Probe.** A 10-row tree holds 20 nodes, then an expanded "Tables" with 30 children, then
"Views". The probe scrolls so that Tables sits on line 4 and presses Left:

```
before: sel 20 scroll 16 len 52
after:  sel 20 scroll 16 len 22 rows shown 6
│    n16 … n19 │
│[+] Tables    │
│    Views     │
│              █   (4 blank rows; n0–n15 are hidden above)
```

**Fix.** In `ensureVisible(h)`, after the two selection rules, clamp
`tv.scroll = min(tv.scroll, max(0, len(tv.nodes)-h))` before the existing `max(0, …)`.
This is the ListBox rule. The selection stays visible because `sel <= len-1`. The
"keeps its screen line" promise still holds in every case that has no blank space, and
yields only when keeping the line would show empty rows. Update the `SetNodes` comment to
say so. The `h <= 0` guard stays in front, so a tree filled before its first
`SetBounds` is unaffected.

**Tests.** Add the probe as `TestTreeViewCollapseNearBottomFillsView`, asserting
`scroll == len-h` and the selected node still on screen. Keep
`TestTreeView…KeepsScreenLine` (or the equivalent) green: insertions above the selection
must still hold the line.

**Drive.** In goSSMS, expand a database's Tables with enough rows to scroll, scroll to
the bottom, select Tables and press Left. The pane should fill with the nodes above it.

---

## Bug 2: `DataGrid.SetSourceReordered` drops Ctrl+click marks

**Where:** `controls/datagrid_sort.go`: `SetSourceReordered`, which calls
`SetSourcePreservingView` → `setSourcePreserving(…, keepSelection=false)` → `SetSource`
→ `resetView` → `ClearMarkedRows`. goSSMS callers are `query_panel_sort.go:183`
(result-grid header sort) and `detail_browser_sort.go:189`.

**What is wrong.** A re-sort installs *the same rows* in another order, and both
permutations are in hand. The cursor is mapped through them, but the Ctrl+click marks
are thrown away. A user who marks rows, sorts to check them, and copies gets only the
cursor row. The selection vanishes without notice.

**Probe.** Rows are `c a d b`, the cursor is on `c`, and the user Ctrl+clicks `a` and `b`.
Then the grid is re-sorted ascending:

```
marked before: [0 1 3]        (c, a, b)
marked after:  [2] count 1    (only the cursor row, c)
```

Expected after: `[0 1 2]` (a, b, c in the new order).

**Fix.** In `SetSourceReordered`, before installing:

1. If `g.marking`, map each `SelectedRows()` index `r` to its underlying row: `orig =
   oldPerm[r]`, or `r` when `oldPerm` is nil. Skip indexes out of range.
2. After `SetSourcePreservingView` and the cursor move, build the inverse of `newPerm`
   (underlying row → shown index). Reinstall the marks as `marking = true`,
   `markLo, markHi = 0, -1` (empty base run), and `markFlips` = the mapped shown
   indexes. Drop any row `newPerm` no longer holds.

A Shift block selection is still collapsed. After a re-sort its rows are not
contiguous, and a block also carries a column range that marks cannot express. Say so
in the doc comment, which today lists only what is kept.

No API change. A small helper `setMarkedRows(rows []int)` in `datagrid_selection.go`
keeps the mark representation in one file.

**Tests.** `TestSetSourceReorderedKeepsMarks` (the probe), a case with `oldPerm != nil`
(re-sort of a sorted view), and one where `newPerm` omits a marked row.

**Drive.** Run a multi-row query in goSSMS, Ctrl+click three rows, click a column header,
and check that the same three rows stay highlighted. Then Ctrl+C and check the
clipboard.

---

## Bug 3: `FieldRing` (and `ButtonRowKey`/`ButtonsKey`) fire a disabled button

**Where:** `dialogs/field_ring.go`: `press`, `Key`, `Mouse`. `dialogs/focus_ring.go`:
`ButtonRowKey` and `ButtonsKey` (the `KeyEnter` case), and `RingMouse`.
`dialogs/modal.go`: `ButtonClicked`/`buttonGroupHit`, which take no disabled set.

**What is wrong.** `FieldRing.Disabled` exists so that a dialog can grey a button, and
`DrawButtonsGated` greys it. Input ignores it:

- Enter in a field fires `DefaultBtn` even when that button is disabled.
- Tab onto the button row leaves `BtnFocus` on a disabled `DefaultBtn`, drawn as
  focused, and Enter fires it.
- A click on a disabled button fires it (`ButtonClicked` hit-tests labels only).

goSSMS's Connect dialog enforces the gate itself (`doButton`), and no goSSMS
`FieldRing` sets `Disabled` yet, so the bug is latent there. Any user of the documented
field gets an "OK" that works while greyed.

**Probe.** A FieldRing with one InputField, buttons `OK` (disabled) and `Cancel`, and
`DefaultBtn` 0. The probe sends Enter in the field, then Tab and Enter:

```
pressed: [0 0] btnFocus 0
```

**Fix.**

1. `FieldRing.press` returns without calling `Press` when `BtnFocus` is disabled. The
   `Mouse` press callback does the same check.
2. `ButtonRowKey` and `ButtonsKey` fire on Enter only when `*btnFocus` is not disabled.
   This changes behaviour: a caller relying on the callback to refuse sees no call.
   Every in-tree caller passes `nil` or enforces the gate itself, so nothing changes for
   them.
3. Entering the row (`FieldRing.Step` onto the buttons, and `Reset`) moves `BtnFocus`
   to the nearest enabled button (`StepButton` from `DefaultBtn`, then the other way)
   so that the focus highlight never sits on a greyed button.
4. **Compatibility break (optional, recommended):** give `ModalDialog.ButtonClicked`,
   and the split-row counterpart, a `disabled []bool` parameter. A press on a disabled
   button still consumes the press (`d.fresh = false`) but returns -1. Callers that
   don't gate pass `nil`. The non-breaking alternative is a new `ButtonClickedGated`
   with `ButtonClicked` delegating to it. Pick the break only if the goSSMS call sites
   are updated in the same pass (`grep -rn ButtonClicked internal/tui`).

**Tests.** The probe as `TestFieldRingRefusesDisabledButton` (keys and a click), plus
`ButtonRowKey` and `ButtonsKey` table cases with a disabled focused button.

**Drive.** No goSSMS dialog gates a FieldRing button today. Drive the Connect dialog
(which goes through `ButtonRowKey` with `buttonsDisabled()`) to confirm nothing
regressed. Then drive Find/Replace (a FieldRing) to confirm its buttons still fire.

---

## Bug 4: `ClipScreen.Put` writes a wide grapheme that straddles the clip's right edge

**Where:** `core/clip_screen.go`: `Put` (and `SetContent`). Every drawing helper
(`DrawText`, `DrawTextClipped`, `putGrapheme`) bottoms out in `Put`. `dialogs.Stack.Draw`
wraps every dialog in a `ClipScreen`, so the clip is what confines a clamped dialog.

**What is wrong.** `Put` tests only the lead cell `(x, y)` against the clip. A
two-column grapheme whose lead is the clip's last column is written whole. Its second
half lands one column outside the clip, and on a real terminal it overwrites that cell:
the dialog's right border, or whatever the next layer drew there. `Buffer.write` and
`Buffer.Blit` already substitute a space for a wide glyph without room, as tcell does at
the screen edge. ClipScreen is the one writer that doesn't.

**Probe.** The clip is `W:10`, with a border `│` at x=10, and `DrawText(c, 8, 0, "a中文")`
places 中 at x=9:

```
mock:   cell9="中" w=2 cell10="│" w=1    (tcell emits 中 across 9–10; the │ is never drawn)
buffer: cell9="中" w=2 cell10=""  w=0    (border cell overwritten)
```

**Fix.** In `Put`, when `(x, y)` is inside the clip, measure the first grapheme
(`splitGrapheme(str, 1)` plus `DisplayWidth`, already used in the outside branch). If
`x+w > c.clip.Right()`, write a space in its place
(`c.Screen.Put(x, y, " ", style)`) and return `(rest, w)`, so that a caller advancing by
the returned width stays aligned. Do the same in `SetContent` for a wide `primary`
(`RuneWidth(primary) == 2`). Measure only in the edge column (`x == clip.Right()-1`) so
that the common path costs nothing extra.

**Tests.** The probe as `TestClipScreenWideGlyphAtClipEdgeBecomesSpace`, against both
`tuikittest.NewScreen` and `NewMockScreen`, and the left-edge mirror (a lead at
`clip.X-1` stays dropped and does not touch `clip.X`).

**Drive.** Shrink the goSSMS terminal until the Connect dialog clamps, then type a CJK
server name long enough to reach the field's edge. The right border should stay intact.

---

## Bug 5: `WrapHanging` measures and cuts by rune, splitting grapheme clusters

**Where:** `core/wrap_hanging.go`: `splitHanging` (`for i, r := range line` with
`DisplayWidth(string(r))`). The only caller is `dialogs.TextDialog`, which goSSMS uses
for F1 help.

**What is wrong.** Everything else in `core` measures by grapheme cluster
(`core/runecol.go` explains why). `splitHanging` sums per-rune widths and may cut
between any two runes. A ZWJ sequence measures 6 columns instead of 2, so the line
wraps early. A cut can land inside a cluster, leaving a skin-tone modifier or the rest
of a family at the start of the next row. This is the only per-rune width measurement
left in tuikit (`grep 'DisplayWidth(string('`).

**Probe:**

```
WrapHanging(["ab 👨‍👩‍👧 cdefgh"], 5) → "ab" | "👨‍👩‍" | "👧" | "cdefg" | "h"
WrapHanging(["abcd👍🏽xyz"], 6)      → "abcd👍" | "🏽xyz"
```

**Fix.** Walk `displaywidth.StringGraphemes(line)` in `splitHanging`, tracking each
cluster's byte offset and width. Treat a cluster equal to `" "` as a break candidate
(`col > hang`). On overflow with no candidate, cut at the overflowing cluster's start,
or after it when it is the first cluster (to guarantee progress, as `splitGraphemeWidth`
does). `hangColumn` already measures with `DisplayWidth`, so it needs no change.

**Tests.** The two probe lines as cases in `wrap_hanging_test.go`: each row's
`DisplayWidth <= w`, the rows rejoined (minus hang spaces) equal the input, and no row
starts with a combining or modifier rune.

**Drive.** Not reachable in goSSMS with real content (F1 help is ASCII). Run the unit
test, and if wanted, open F1 at a narrow width to confirm the help still wraps the same.

---

## Also noticed, not counted

- `core.ParseFormattedNumber` accepts misplaced thousands separators: `"1,5"` → 15,
  `"1433,1434"` → 14331434, `"12,34,5"` → 12345. A text cell holding a list or a
  comma-decimal then sorts as a number (`NewTextCellKey`, the Detail Browser sort) and
  right-aligns. A fix would accept commas only in 3-digit groups after a 1–3 digit lead.
- `dialogs.Stack.HandleMouse` hands the real-position `ButtonNone` (pointer motion too)
  to every open dialog, not only the top one. Its own `core.ReleaseNowhere` exists so
  that layers underneath don't light hover. No current dialog holds a hover-tracking
  widget, so nothing shows it yet.
