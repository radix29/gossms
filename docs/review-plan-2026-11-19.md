# tuikit review: five bugs and how to fix them (2026-11-19)

The bugs come from reading tuikit at `751f1db` (sibling checkout `~/go/tuikit`). Each was
reproduced by a throwaway probe test, deleted afterwards. The probe output is quoted
under each bug. Nothing here is listed in `docs/open-threads.md` or settled in
`docs/decisions.md`. We may break backward compatibility wherever a fix needs it. Each
break is called out under its bug.

Every fix lands in tuikit first. Build and test there (`go test ./...`, `staticcheck`,
and `go test -race` where noted), then build goSSMS against it through the active
`replace` and drive the widget in the binary (see docs/testing.md). Green unit tests do
not count as verification.

## Recommended implementation order

1. **Bug 1: Editor In-Selection replace** (high: it rewrites text the user did not
   select). It is the only data-corrupting bug. It also needs one behavior decision
   (1b), so start it first.
2. **Bug 2: TypedConfirmDialog with long names** (high: the safety gate for Delete
   Database cannot be read). It is self-contained, a small diff, and easy to drive live.
3. **Bug 3: PanelManager combo with no panels** (medium: it captures the keyboard). The
   diff is a few lines.
4. **Bug 4: Stack.Sync keeps a hidden-then-reshown dialog in its old slot** (medium-low).
   It changes `ModalDialog.Show` semantics, so it goes after the simpler fixes, with the
   dialog tests rerun in full.
5. **Bug 5: Buffer.Blit at the left edge** (low, cosmetic). It is a one-branch fix, so
   it can ride along with any of the above.

Bugs 2, 3 and 5 are independent and could go in any order. 1 comes first because of its
severity, and 4 comes last because it touches the base type that every dialog embeds.

---

## Bug 1: In-Selection Replace leaves its scope and goes stale after edits

**Where:** `controls/editor/editor_search.go`: `SetSearch` (scope captured at line ~101),
`FindNext` (428), `ReplaceCurrent` (496), `replaceMatch` (563), `ReplaceAll` (582),
`matchInScope` (625). `PrependLines` (`editor.go` ~440/461) is the only edit path that
updates the scope.

**What is wrong.** There are two facets, and both are confirmed.

- **1a. The scope is stale.** `selStartRow/Col` and `selEndRow/Col` are captured once at
  `SetSearch` and never adjusted, except by `PrependLines`. Any later edit moves the
  text under them: a Replace on the scope's end row, or a line typed or pasted above the
  scope (goSSMS's Find dialog recompiles only when the options change, so the scope
  survives edits). `ReplaceAll` then filters against positions that name other text.
  Probe (`"foo foo foo;bar"`, select cols 0–11, `foo`→`zzzzzz`, Replace once, then
  Replace All): `n=1 text="zzzzzz zzzzzz foo;bar"`. The third `foo`, inside the original
  selection, is left unreplaced. A shrinking replacement pulls text past the selection's
  end into scope instead. A line pasted above the scope gives `n=0`, because the scope
  now covers the wrong row. Had that row held matches, they would have been replaced.
- **1b. Find and Replace ignore the scope.** `FindNext` deliberately searches the whole
  document, starting after the selection. goSSMS's first Replace press calls `FindNext`,
  and the second press replaces what it found. So with "selection only" checked, the
  first replacement lands on the first match *after* the selection. Probe (`"foo
  foo;foo"`, select 0–7): `first Find selects row 0 cols 8-11 (outside the 0-7
  selection)`.

**Fix.**

1. Make the scope track the document. Store a `scopeVer` with it. Before `ReplaceAll`
   and `matchInScope` read the scope, bring it up to date:
   - `replaceMatch` shifts the columns itself. On the start or end row, a position at or
     after `m.endCol` moves by `len(repl) - (m.endCol - m.startCol)`. Then it sets
     `scopeVer = doc.Version()`.
   - Any other change goes through `doc.changedSince(scopeVer)`. Rows wholly above the
     scope shift by `newN - oldN`, and edits wholly below it do nothing. A span that
     touches the start or end row, or an `ok == false` result (the log was outrun, or the
     edit was `SetText`), invalidates the scope (`selValid = false`). `ReplaceAll` then
     replaces nothing, and goSSMS already reports "No matches inside the selection". Do
     not guess at a column. The rule is that an uncertain scope replaces nothing.
   - Fold `PrependLines`'s hand adjustment into the same mechanism, so there is one path.
2. Scope Find and Replace when `InSelection` is set (**behavior change: needs your
   call**). `FindNext` and `ReplaceCurrent` would consider only matches where
   `matchInScope` is true, wrapping within the scope, which is how SSMS's "Look in:
   Selection" behaves. The comment on `SearchOptions.InSelection` gives the reason it was
   left unscoped: "a find stopping at a selection boundary would look like 'no more
   matches'". goSSMS sets `InSelection` only in replace mode, so plain Find is
   unaffected. The alternative is to keep `FindNext` global and make only
   `ReplaceCurrent` refuse an out-of-scope match, but then Replace finds a match it
   will not replace. I recommend scoping both.
3. Add a scope highlight. Optional, and not part of the fix.

**API break:** the `SearchOptions.InSelection` doc changes meaning (Find is scoped too).
No signatures change.

**Tests (tuikit):** the probe cases become `TestReplaceAllInSelectionAfterReplaceCurrent`
(growing and shrinking replacements), `TestInSelectionScopeFollowsLinesInsertedAbove`,
`TestInSelectionScopeInvalidatedByEditOnBoundaryRow` and
`TestFindNextInSelectionStaysInScope`. Add the scope to `editor_monkey_test.go`'s
invariants: after any edit sequence, ReplaceAll touches only text that was inside the
selection.

**Live:** in goSSMS, open Ctrl+H, select part of a line that has matches after it, check
Selection, then press Replace ×2 and Replace All. Repeat after typing a line above the
selection.

---

## Bug 2: TypedConfirmDialog cannot show a long required name, and its input overruns the box

**Where:** `dialogs/typed_confirm_dialog.go`. The input width is
`max(20, DisplayWidth(required)+16)` at line 99, the dialog width comes from
`fitMessage(message, …)` at 107 (the message only), and the prompt goes through
`DrawTextClipped` at 173.

**What is wrong.** The dialog sizes itself from the message alone. A required string of
41 columns or more makes the input wider than the dialog, so it draws through the right
border and beyond the box. The prompt `Type "<name>" to confirm:` is hard-clipped with
no ellipsis, so the user cannot read the name they must type. Probe (160×40 screen,
68-column name): dialog spans x=50..109, input spans x=52..143, and the prompt row reads
`Type "Sales_ArchiveArchiveArchiveArchiveArchiveArchiveAr`. This dialog is the
retype-to-confirm gate for destructive actions. SQL Server names run to 128 characters,
and generated names (SharePoint content databases, GUID-suffixed names) commonly pass 40.

**Fix.**

- Size the dialog from the widest of the message, the prompt (`TypeToConfirm` with the
  name) and the input (`required+16`, plus 2 for the brackets), capped at the
  `maxMessageWidth` fraction of the screen, as `fitMessage` caps the message.
- Clamp the input width to `inner.W - 4`. InputField already scrolls horizontally, so a
  name longer than the box is still typeable.
- When the prompt still doesn't fit, wrap it with `core.WrapText`, which hard-breaks an
  unbroken token, and grow the height by the extra lines, shifting the input and status
  rows down as `extra` already does for message lines. Never cut the name: the user has
  to be able to read all of it.
- `Relayout` repeats the same sizing.

**API break:** none.

**Tests:** `TestTypedConfirmLongNameFitsAndShowsWhole`. With names of 20, 60 and 128
characters on 80×24 and 200×50 screens, assert that the input's right bracket is inside
`Rect()`, that the concatenated prompt rows contain the whole name, and that no cell
right of `Rect().Right()` is written. Also test typing the whole 128-character name and
confirming.

**Live:** in goSSMS, delete a database whose name is over 60 characters. Create a
disposable one, following the live-server rule.

---

## Bug 3: PanelManager opens an invisible tab combo with no panels, and it captures every key

**Where:** `layout/panel_manager.go`, the combo-arrow branch of `HandleMouse` (~465),
`HandleKey`'s `comboOpen` branch, and `RemovePanel` (103).

**What is wrong.** With no panels, the tab bar draws `EmptyText` and no `[v]`. The
right four columns still toggle `comboOpen`, though. `Draw` skips the list
(`len(pm.panels) > 0`). `HandleKey` returns true for every key while the combo is open,
and `OverlayActive` reports true, so the host routes every mouse event there first. The
keyboard stays dead until the user happens to press Escape, with nothing on screen to
explain why. Removing the last panel while the combo is open (through `OnCloseTab` or a
host command) leaves the same state. Probe: `comboOpen=true overlay=true`, `key F5
consumed=true`.

**Fix.** `setComboOpen(true)` refuses when `len(pm.panels) == 0`. `RemovePanel` closes
the combo when the last panel goes. The arrow hit-test requires panels too, so the click
falls through as an empty bar's would.

**API break:** none.

**Tests:** `TestPanelManagerEmptyComboNeverOpens` (click the arrow region with no panels,
then check that a key is not consumed and `OverlayActive` is false) and
`TestRemovingLastPanelClosesCombo`.

---

## Bug 4: Stack.Sync keeps a dialog that was hidden and reshown in its old z-order slot

**Where:** `dialogs/stack.go` `Sync` (89) and `dialogs/modal.go` `Show` (98).

**What is wrong.** `Sync` drops entries that are no longer visible and appends newly
visible ones in `ShowOrder` order. A dialog that was hidden and shown again *between two
Syncs* is still visible when Sync runs. It is never dropped or re-sorted, so it keeps its
old slot, under any dialog opened in the meantime. That breaks the documented rule that
"the one shown last is on top". The case is a reused instance: a message box whose OK
handler hides it, opens a confirm, and then shows the message box again with new text.
Probe: open A, then B. In one handler, hide B, show C, and show B again. `Top()` is C,
although B was shown last. B is buried and C takes the keys.

**Fix.** Make "re-show while open" and "hide then show" distinguishable:

- `ModalDialog.Show` bumps `shownAt` only when the dialog was not visible. Re-showing an
  open dialog (refreshing its contents) keeps its number. This preserves the documented
  "re-showing an open dialog leaves its place unchanged".
- `Stack` records the `ShowOrder` of each entry when it pushes it (`open []openEntry{d,
  order}`). In `Sync`, an entry whose current `ShowOrder` differs from the recorded one
  was closed and reopened. Remove it from its slot and push it with the newly opened
  group, sorted by order as now.

**API break:** `ShowOrder` no longer changes on a re-show of an open dialog. A host that
used a re-show to raise a dialog would now need Hide then Show. goSSMS has no such use;
grep `\.Show\w*(` calls on dialogs that are already open to confirm.

**Tests:** extend `dialogs/stack_test.go` with the probe case,
`TestStackReshowOfOpenDialogKeepsPlace` (the existing contract) and
`TestStackHideShowBetweenSyncsGoesOnTop`.

---

## Bug 5: Buffer.Blit leaves the first destination cell stale when the source starts mid-glyph

**Where:** `core/buffer.go` `Blit` (259), in the `cell.width == 0` branch.

**What is wrong.** When `src.X` lands on the trailing half of a wide grapheme (a CJK login
or program name in an Activity Monitor dashboard, scrolled horizontally by an odd
amount), the loop skips the cell with `dx++` and never writes `dst`. Whatever the screen
held there from the previous frame stays. The right edge is already handled (a wide
glyph that does not fit becomes a space). The left edge is not, despite the doc's "no
stray half-glyph". Probe: the source row is `中a`, blitted from x=1 onto a destination
holding a stale `X`, and the result is `"Xa "` instead of `" a "`.

**Fix.** In the trailing-cell branch, write a space in the trailing cell's own style
(`bufferCell` keeps `style` there) before `dx++`. Apply the same to an off-buffer source
cell (the zero cell): write a space in `tcell.StyleDefault`, or document that the caller
clamps `src`. The space is better, because a canvas that shrank under an older scroll
offset otherwise shows stale cells.

**API break:** none.

**Tests:** `TestBlitBlanksLeadingHalfGlyph` (the probe) and
`TestBlitPastBufferEdgeBlanksDestination`.

---

## Also noticed (not counted, lower value)

- `TreeView`'s `clicks` (`controls/treeview.go`) pairs presses by row index and is not
  reset in `SetNodes`. A refresh that lands between two clicks can turn "click node A,
  then click node B in A's old row" into a double-click that activates B. `ListBox`
  resets its tracker in `SetItems` for this reason. The fix is `tv.clicks.Reset()` in
  `SetNodes`, or keying the tracker by node ID.
