# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. Read this first; it points
to the rest of the docs rather than repeating them.

## What this is

goSSMS is a portable, cross-platform terminal TUI reimplementation of SQL
Server Management Studio, written in Go 1.26. Runs on Linux/macOS/Windows
from a single build — no CGO, no build-tag-split files. `runtime.GOOS`
branching exists in exactly two places: `internal/tui/os_clipboard.go`,
which shells out to `xclip`/`wl-copy`/`pbcopy`/`clip` with an OSC-52
fallback, and `internal/version/version.go` for the version string.

- Module: `github.com/radix29/gossms` — https://github.com/radix29/gossms
- Depends on `github.com/radix29/gosmo`, the author's own companion library
  for SQL Server management objects — https://github.com/radix29/gosmo —
  and `github.com/gdamore/tcell/v3` (`v3.4.1`) for the TUI backend.
- `go.mod`'s `require` pins a gosmo tag, but the `replace
  github.com/radix29/gosmo => ../gosmo` directive is deliberately **active**
  during development, so builds use the sibling checkout rather than the
  pinned tag, and `HEAD` may depend on gosmo code that isn't tagged yet.
- The author's local layout has both repos as siblings: `~/go/gossms` and
  `~/go/gosmo`.
- Version is resolved automatically from the pushed git tag — see
  `internal/version/version.go` — never hand-edited.

## Required reading by task

Read what the task actually touches — a one-file fix needs none of this.
Each entry names a section, not a whole document; read the section.

| If touching… | Read first |
|---|---|
| User-facing behavior, features, or keys | `README.md` |
| Anything spanning packages | `ARCHITECTURE.md` § Package map, § Which document owns what |
| Anything under `internal/tuikit/**` | `internal/tuikit/README.md` § Design principles |
| A new tuikit control or dialog | `internal/tuikit/README.md` § Adding a new control |
| `HandleMouse`, overlays, focus, drag | `ARCHITECTURE.md` § The mouseDragging idiom |
| A goroutine delivering a result to the UI | `ARCHITECTURE.md` § Async result delivery: postAndWake |
| gosmo changes / the `replace` directive | the `dev-with-local-gosmo` skill; `ARCHITECTURE.md` § Developing against a local gosmo checkout |
| Known bugs, deferred scope, release blockers | `docs/open-threads.md` — check before reporting something as newly found |
| Why a design is the way it is | search `docs/journal.md` — work since the current tag only, trimmed each release; older entries are in git history (not required reading) |

## The one rule that matters most: verify against real source, don't guess

Guessing at `tcell`/`gosmo` API shapes from training data produces code
that *looks* plausible but doesn't match the real API. Before writing any
code that calls into either, check the real source if there's any
uncertainty — `go doc`, grep the module cache (`go env GOMODCACHE`), or read
the sibling `~/go/gosmo` checkout directly. Don't rely on memory of
"similar" APIs from other versions or packages.

Standing gotchas:

- `Screen.PollEvent`/`PostEvent` don't exist in tcell v3 — replaced by a
  channel, `EventQ()`.
- The modifier accessor is `Modifiers()`, not `Mod()`.
- `gosmo.Database.Name` / `.State` / `.RecoveryModel` etc. are *methods*,
  not fields.
- `gosmo.Server` has **both** `Database(name)` and `DatabaseByName(name)`,
  and they are not interchangeable — picking the wrong one fails quietly.
  `Database` is a lightweight handle with no query and zero-valued
  metadata, and the only one that works under a `WithScript`-derived
  context; `DatabaseByName` queries `sys.databases`. Both are documented in
  full on the methods themselves — `go doc gosmo.Server.Database`.

## Build & verify

```sh
go build -o gossms ./cmd/gossms   # build
go run ./cmd/gossms                # run without building a binary
go test ./...                      # test
gofmt -w .                         # format in place
go vet ./...                       # vet
```

No Makefile — this project uses the plain `go` toolchain only. Use these
directly rather than eyeballing correctness; a real shell is available
here.

Version/Commit/Date (`internal/version`) resolve automatically, in priority
order: `-ldflags -X` (set by `.github/workflows/release.yml` from the
pushed git tag) → `debug.BuildInfo.Main.Version` (populated by `go install
.../cmd/gossms@<tag>`) → the literal `"(devel)"` default for a plain
`git clone && go build`/`go run`. Nothing here is hand-edited before a
release.

### Green tests are not verification

`go test ./...` passing means the unit tests pass, not that the change
works. Nearly every real bug in this project's history was caught by driving
the built binary, not by a test. For anything touching the TUI or the
database layer, verify by running it:

- **TUI behavior** — drive the binary headlessly under tmux:
  `tmux new-session -d -s t -x 100 -y 30 <binary>`, `tmux send-keys -t t …`,
  then `tmux capture-pane -t t -p` (plain) or `-p -e` (with SGR codes, to
  tell focused from merely selected). Send `Escape` in its **own**
  `send-keys` call — batched with a following key, tcell reads `\x1b<key>`
  as Alt+key. Verify focus against a capture after each keystroke instead of
  counting `Tab`s, and capture the full pane height before concluding a
  dialog closed; both tmux quirks and this app's focus-order gotchas cost
  real time when assumed.
- **Database behavior** — a real SQL Server instance is used for this, not a
  mock. Connection details are deliberately not in the repo; ask for them.
  Create throwaway databases/logins, exercise the actual write path, then
  drop them — never mutate pre-existing ones.
- When a fix is subtle, A/B it: keep a pre-fix binary and show the old
  behavior reproducing and the new one not.

Tests should assert an outcome, not that nothing panicked — a test that only
checks "no crash" passes on the very behavior it was written to pin down.

**A round-trip test proves two functions are inverses, never that either is
right.** Where a load half and a write half read the same parallel
label/code tables — the schedule dropdowns, the permission-state tables, any
`items[]`/`values[]` pair — a fault in the shared table cancels out and the
round-trip passes: swapping two entries in `weekdayBits` leaves the checkbox
labelled Monday setting Tuesday's bit, and `populate`→`readFrequency` still
agrees. Pin such a pair by *naming* it (`"Monday"` → `gosmo.WeekdayMonday`)
and by asserting the two slices are the same length; a label added to one and
not the other is otherwise absorbed silently. See
`agent_schedule_form_test.go` and `docs/journal.md` (2026-08-20).

Check a new test by mutating the code it covers and confirming it fails —
that is what surfaced the blind spot above, and it takes one `sed` and one
`go test`.

**A Properties page can be driven end to end from a test — use
`internal/tui/fakedb_test.go`, don't conclude it can't be done.** `gosmo.Server`
now has a constructor over a caller-supplied `*sql.DB` (`gosmo.NewServer`), and
the harness on top of it gives you `newFakeConn` (a `*db.ServerConn` backed by a
scripted `database/sql` driver that records every statement executed),
`loadPage`, and `textRow` for addressing a form by label rather than by an index
into `Rows()`. `database_props_files_page_test.go` is the worked example: it runs
the real `load` and `apply` closures and asserts the statements that reached the
server. The `gosmo.WithScript` harness the New-X dialogs use does *not* work
here — a Properties page's `load` and `apply` both open with a by-name read, and
`WithScript` intercepts writes only.

Rules for writing one, all learned the hard way (`docs/journal.md`,
2026-08-20 and 2026-08-21). **Drive a row with `Edit`, never
`SetValue`/`SetSelected`** — the latter move the dirty baseline with the value,
so apply skips the row and a
test that asserts "nothing was written" passes for the wrong reason; the
`editText`/`editSelect`/`editRadio`/`toggleByName` helpers check for it. And
**address rows and grid cells by name, never by index** — every one of these
pages reads its grid back positionally against the slice it was built from, so a
test that also works in indices agrees with a page that has them misaligned.
Where a page keeps a filtered or pending-removal subset alongside the full list,
exercise *two* edits: the lists only diverge after the first. And **act on an
object that is not the first one in its list** — a page that ignores the
selection entirely still passes when the test picks row 0 (a real missed mutant
on User Membership, 2026-08-21).

Three more the harness itself needs, each of which produced a wrong answer
before it existed. **Scope a `fakeResponse` with `db:` when the page reads the
same query in several databases** — without it every database gets the identical
answer and the misalignment the test exists to catch is unreachable. **Scope one
with `arg:` for a by-name read**, and put it *before* the list read: responses
are matched by substring in order, and `DatabaseByName`'s query contains
`FROM sys.databases` too, so behind the list answer every object resolves to
whichever row sorts first. **Assert with `StatementsIn(db)`, not `Statements()`,
for anything database-scoped** — the bare `USE` is stripped as plumbing, and
with it the only record of where the write landed; pair it with
`assertNoStatementsIn` on the databases that should *not* have been touched.

One trap that fails silently in the passing direction: `eachDatabase`
(`db_scan.go`) and gosmo's `userMappingsIn` both drop a database whose read
fails rather than failing the page, so an under-scripted fake yields an empty
grid and an apply that writes nothing. A test on such a page must first assert
the page actually loaded its rows.

What that harness proves is bounded, and staying inside the bound is the rule:
queries are matched by substring and answered with whatever the test scripted,
so it shows the page asked for the right things and built the right request —
never that the T-SQL is valid or that SQL Server would accept it. Statement text
is gosmo's own tests; acceptance is a live run. An assertion there that reaches
for server semantics is asserting the fake.

## Scope discipline

Avoid unrequested tidying. A bug fix does not need surrounding cleanup, new
abstractions, compatibility shims, feature flags, or speculative validation.
Do the simplest behavior-preserving change that satisfies the task. Trust
internal code and framework guarantees; validate at system boundaries.

## Changing gosmo

`gosmo` is the author's own library, not a third-party dependency — add
or change functionality in it when gossms needs something it doesn't
have yet, rather than working around a missing capability inside gossms.
Build and test inside `gosmo` itself before relying on a change from
gossms.

**gosmo is a general-purpose library with users beyond gossms. Never remove
or narrow a gosmo capability because gossms doesn't call it.** "No callers in
gossms" is not evidence of dead code there — gossms is one consumer of a
published SMO-shaped API, and an unused method is a method some other
application (or a future gossms page) depends on. This applies to whole
files, exported methods, exported types and their fields, and struct fields
that only some code paths populate. The `*Seq` iterators in `iter.go` are the
standing example: 75 exported methods, zero gossms callers, all deliberately
kept.

When a gosmo audit turns up something unused, the allowed moves are: make it
faster, make its doc comment accurate about what it actually does, or add a
test that pins it. Removing it, or replacing a general form with the narrow
one gossms happens to need, is not one of them — bring it up instead of
acting on it. Optimisation must be behaviour-preserving at the API surface:
same signature, same results, same errors. Report unused gosmo surface as
"unused, kept deliberately", never as a deletion candidate. The same rule is
stated from gosmo's side in `~/go/gosmo/CLAUDE.md` § This is a library, not
gossms's back end.

This is the one place the two repos differ: inside **gossms**, dead code is
ordinary cleanup. The no-removal rule is about gosmo only.

## Coding conventions

- **Comments: describe what the code does. Add the "why" only when getting
  it wrong reintroduces a bug, and then say so concretely.** The bar is
  whether the next person to touch this code would break something without
  the note — not whether the reasoning was interesting.
  - Worth writing, and load-bearing here: the invariant a `mouseDragging`
    latch protects, why `postAndWake`'s two halves can't be called by hand,
    why `SelectRow.orig` is read back from the widget, why `escapeSingle`
    goes on top of bracket-quoting. Each of those is a shipped bug that a
    plausible "simplification" would bring straight back.
  - Not worth writing: alternatives considered and rejected for no lasting
    reason, restatements of what the line plainly says, and any narration of
    how the code came to look this way. Those belong in `docs/journal.md`.
  - Prefer one sharp sentence naming the failure to a paragraph of
    discussion. Where a long comment is genuinely earned, keep it on the
    declaration it protects rather than spreading it through the body.
  - Existing long comments are not a cleanup target — `app.go`,
    `datagrid.go`, `secret.go`, `propsheet/common.go` are 33-50% comments,
    mostly rationale, and that rationale has repeatedly been what stopped a
    regression. Judge only *new* comments by the failure-mode bar above.
- Go 1.26 features in active use: `new(T{...})` composite-literal syntax,
  the `slices` package, `errors.AsType`.
- `core.DisplayWidth(s)`, never `len(s)`, for any column-position math —
  terminal columns aren't byte length.
- **A property-sheet label must fit `propsheet.LabelWidth` (30 columns).**
  Text/Password/Int/Select pad theirs with `core.PadRight` and Static clips its
  own; both hard-clip with no ellipsis, so an over-long label silently renders
  as a shorter, different one — "Auto update statistics asynchronously" became
  "Auto update statistics asynchr", directly under "Auto update statistics".
  `TestNoPropertySheetLabelIsTruncated` fails on any literal over the limit and
  prints what it would render as; shorten the label rather than widening
  `LabelWidth`, which moves the value column on every page. Check/Radio/
  Section/Note draw at full width and are exempt.
- `tuikit` is a strictly one-way dependency graph and knows nothing about
  `tui` (the application layer).
- Every `tuikit` sub-package is organized one file per type or tightly
  related group of types, plus `common.go` for helpers shared across more
  than one file in the package and `doc.go` for the package doc comment.
  `internal/tui` files are organized one-per-purpose (`app.go`, `menu.go`,
  `connect_dialog.go`, …).

## Application rules (not derivable from the code)

- **Seed `QueryPanel.savedText` from `qp.editor.Text()`, never from the string
  you just passed to `SetText`.** `SetText` normalizes what it is given —
  expands tabs, folds CRLF to LF, replaces invalid UTF-8 with U+FFFD — so
  seeding from the source marks the panel dirty the moment it opens. That is
  not cosmetic: a panel that is falsely dirty prompts to save on close, and the
  save rewrites the file in the editor's normalized form. `File > Open` shipped
  that way and converted a UTF-16 script to U+FFFD-laden UTF-8 on disk
  (2026-08-14). Anything that reads a text file also goes through
  `decodeTextFile`/`encodeTextFile` (`text_encoding.go`), which detect the
  encoding **from a BOM only** — a guessed encoding rewrites the user's script
  in one they never chose — and preserve the file's line endings.
- **A widget's `HandleKey`/`HandleMouse` must return `true` only for events
  it actually acted on** — never a blanket "I'm focused, so I consumed it."
  `propsheet.Form` gives the focused row first refusal and falls back to its
  own Tab/Up/Down cycling only on `false`, so a widget that always returns
  `true` becomes a keyboard trap the user can only escape with the mouse.
  Check what a new widget returns for Up/Down at a list boundary and Escape
  with nothing open.
  **A widget that is correct standalone can still trap once wrapped as a
  form row, and then it is the wrapper's job to translate.** `DataGrid`
  answers `true` to every arrow key, which is right when nothing else wants
  them — and made every one of the 21 `NewGridRow` pages swallow Down at the
  last row, Up at the first, and `Left` back to the page list. The fix went
  in `propsheet.GridRow`, which snapshots `SelectedCell`/`ScrollCol` around
  the key and reports what actually moved; changing `DataGrid` itself would
  have reached QueryPanel, DetailBrowser and the Activity Monitor, all of
  which depend on the blanket answer. Detect movement, never predict it, and
  remember that a grid with no cell cursor scrolls without changing
  `SelectedCell`.
  **The other side of that contract: never call `DataGrid.SetData` on a grid
  the user is navigating without putting the cell cursor back — use
  `redrawGrid` (`prop_grid_helpers.go`).** `SetData` resets the selection to
  0,0. From inside the grid's own `OnSelectRow` that is worst, because the
  callback runs *after* the grid has moved: the move is undone, `GridRow`
  correctly reports "not handled", and `Form` moves focus out of the grid on
  the very first arrow key. Six sites shipped that way — three AG Properties
  pages, three New Availability Group pages, plus `owner_transfer_page.go`'s
  dropdown — and on each, every row but the first was unselectable by mouse
  *and* keyboard (found and fixed 2026-08-14; see `docs/journal.md`).
  `wireGridEditor` (`ag_props.go`) packages the whole commit/load/redraw
  wiring for the grid-plus-detail-editor idiom. A redraw after the row *set*
  changes — an add/remove button, a new result — is a different case and may
  reset the cursor deliberately.
  **The cursor is not the only thing `SetData` discards**, so restoring it by
  hand is not enough and shipped as its own bug: the scroll position and any
  dragged column width go too, and `SetSelectedCell` ends in `ensureVisible`,
  which then scrolls to the selection *from zero* and lands the row the user
  was on against the bottom edge. Toggling a State half way down Server
  Properties > Permissions moved the whole list on every click (found and fixed
  2026-08-20; reproduced and A/B'd live). `redrawGrid` is now a wrapper over
  `controls.DataGrid.SetDataPreservingView`, which restores all three in an
  order that matters — widths, then scroll, then selection. Outside
  `internal/tui`, call `SetDataPreservingView` directly; `propsheet.ToggleGridRow`
  had the same defect and could not reach `redrawGrid` at all. Never hand-roll
  the pair again: six pages did, and every one of them dropped the widths.
- **A `Ctrl+Shift+<letter>` chord never arrives as `KeyCtrlX`.** tcell folds a
  Ctrl-modified rune into `KeyCtrlA..KeyCtrlZ` only when Ctrl is the *sole*
  modifier, so Kitty reports Ctrl+Shift+O as `KeyRune "o"` and xterm's
  modifyOtherKeys as `KeyRune "O"`, both with `ModCtrl|ModShift`; legacy
  terminals cannot encode the chord at all and send plain `0x0F`.
  `normalizeCtrlRune` (`app_events.go`, called once at the top of `handleKey`)
  folds it back for the whole app — a binding that tests `KeyCtrlX` *and*
  `ModShift` works only because of it, and only on terminals with a modern
  keyboard protocol. Anything that must work everywhere needs a function key
  or a Ctrl+non-letter as well, the way Connect has F9. Verify a new chord by
  injecting its encodings with `tmux send-keys -H` and reading Key
  Diagnostics; a unit test proves nothing here.

- **Every menu item and toolbar button must be context-gated** — never let a
  click or keypress do nothing, do the wrong thing, or crash because a
  precondition (a connection, an active query panel, an Object Explorer
  selection) wasn't met. `MenuItem`/`ToolbarButton` have an
  `Enabled func() bool`; the reactive fallback is a guard plus
  `setStatus(...)` matching existing wording ("Not connected — use File >
  Connect", "No active query panel").
- **A dialog-level scrollbar goes through `ModalDialog.DrawContentScrollbar`,
  not `core.DrawScrollbar` at `Rect().Right()-1`.** On a terminal too small
  for its requested size a dialog's content is clipped to `InnerRect`
  (`App.drawDialogs` wraps the screen in a `core.ClipScreen`; `DrawBase`
  narrows it when `clamped()`), and the border column the bar sits on is
  outside that clip — a bar drawn directly there is correct at full size and
  silently gone on a clamped one. A scrollbar inside a child widget is on the
  widget's own rect and needs nothing. Same reason `DrawBase` sets the clip
  only when clamped: an overlay a dialog opens may extend past the box.
- **Key Diagnostics (`internal/tui/key_diagnostics_dialog.go`, Help menu) is
  a permanent shipped feature**, not debug scaffolding. It's out of scope for
  any pre-release trimming — it decodes tcell's exact Key/Modifiers/rune per
  keypress, which is what separates a real app bug from a terminal
  limitation.
- **An open dialog owns the clipboard outright — `activeClipboardTarget` must
  never fall past `topDialog()` to a panel underneath.** Ctrl+C/X/V are
  consumed centrally in `App.handleKey` *before* any dialog sees them (the
  Screen clipboard methods only exist in the application layer), so the target
  is resolved by asking the frontmost dialog for its focused field via
  `core.ClipboardHost`. The old form was a switch naming three of thirty
  dialogs with a fall-through for the rest, and Ctrl+X in the Find dialog cut
  the query editor's selection behind it, silently, reported as "Cut to
  clipboard" (2026-08-20). A dialog with no text entry deliberately isn't a
  host, and an inert clipboard is the correct answer there. A new dialog with
  a text field implements `FocusedClipboardTarget` — returning an **explicit**
  nil on every miss, since a typed nil widget in an interface is not a nil
  interface. `TestEveryDialogWithTextEntryIsAClipboardHost` catches a missing
  host but cannot catch a reintroduced fall-through, which is why this is
  written here. A dialog that has to *react* to an edit — re-filter a list,
  refresh a preview — implements `core.ClipboardEditHandler` and checks the
  target it is handed rather than assuming the edit landed in the field it
  watches. The paste itself is applied by `App.pasteInto`, which drops the text
  unless the widget it was aimed at is still the target: every clipboard read
  is asynchronous, so the dialog may be gone by the time it returns.

- **Never give a procedure you install outside `master` an `sp_` prefix.** An
  `sp_`-prefixed name falls back to `master` when the current database has no
  such procedure, which corrupts DDL on exactly the path that installs one:
  `CREATE OR ALTER dbo.sp_x` in tempdb finds master's copy and fails with
  "Invalid object name", and `DROP PROCEDURE IF EXISTS dbo.sp_x` in tempdb
  **deletes master's copy** — it did, on the win10cli test server. Both
  verified live. `internal/activity/block.go` is the worked example: master's
  copy is `sp_block` (the name a hand-installed one already has), tempdb's is
  `usp_block`, and every `EXEC` names its database.
- **A folder's Object Explorer filter has to be applied twice — once for the
  tree, once for the Detail Browser.** The tree's half is `filterChildren` in
  `fetchChildren`; the pane's loaders (`detail_browser_*.go`) query gosmo
  independently and hold gosmo objects, so they go through `filterObjects`
  instead, on the collection and *before* rows are built — a progressive
  loader backfills by index, so filtering the rows afterwards writes each
  count and size into the wrong row. A new detail loader for a filterable
  folder that skips this leaves the pane listing objects the tree next to it
  has filtered away, which is what shipped until 2026-08-15.
- **Never call `rows.Next()` speculatively inside `internal/query/executor.go`'s
  `sqlexp.ReturnMessage` loop.** One extra `Next()` on an exhausted result set
  makes the driver consume the protocol message `retmsg.Message(ctx)` is
  waiting for: the grid comes up empty with no error and no Messages tab. Gate
  any drain on having actually abandoned the set mid-scan (`scanNext` returns
  a bool for exactly this). Unit tests do not catch it; only a live query does.

## Mouse, overlays, and async UI

**Read `ARCHITECTURE.md` § The mouseDragging idiom before touching any
`HandleMouse`** — it has the reasoning and the shipped bug behind each rule.
Five invariants, and where each is already implemented:

1. A gesture belongs to whatever claimed its first press, until the release.
   Routers: `App.gestureOwner` (`app_events.go`), `QueryPanel.dragZone`
   (`query_panel.go`), `propsheet.PropertySheet.dragZone` (`sheet_input.go`)
   — each an `armGesture`/`armDrag` at every branch that claims a press, plus
   a `routeGesture`/`routeDrag` that replays to the owner.
2. `App` also snapshots the modal layer (`gestureOverlay`/`overlaySnapshot`)
   and drops held events across a change.
3. A widget that acts on `Button1` needs a per-widget `mouseDragging` latch,
   set on the press and cleared on the matching `ButtonNone`.
4. A latch must not survive into the widget's next showing —
   `ModalDialog.Show()` clears both.
5. A host that returns early from `HandleMouse` must still forward
   `ButtonNone` to a latch-bearing child.

An overlay drawn last gets **first refusal** of every key/mouse event while
open — `DataGrid.OverlayActive()` at the top of
`QueryPanel.HandleKey`/`HandleMouse`; the focused row before positional
routing in `propsheet.Form.HandleMouse`.

A background goroutine reports its result with **`App.postAndWake(fn)`**,
never its two halves (`postEvent` then `wakeEventLoop`) by hand — getting the
ordering wrong leaves the result queued and invisible until an unrelated
keypress drains it (shipped bug: tree nodes stuck on "Loading..."). See
`ARCHITECTURE.md` § Async result delivery: postAndWake. `QueryPanel`'s
elapsed-timer tick is the one legitimate bare `wakeEventLoop()` caller: it
has no callback to post, only a redraw to ask for.

**A background operation that latches UI state before it starts must use
`App.safegoRepair`, not `App.safego`** — a busy flag, a "loading" placeholder,
a toolbar the flag dims. The latch is released by the callback the goroutine
posts when it finishes, and a panic unwinds straight past that callback, so
the latch survives for the object's lifetime: the Log File Viewer's whole
toolbar stays inert, an Activity Monitor tab sits at "Running..." forever, a
Properties page's button refuses every later click. `safego` alone reports the
panic and leaves the UI stuck; the repair step is the way out. Cover it the
way `TestPageActionLatchClearsWhenTheActionPanics` does — panic the action,
then assert the *next* click still runs.

## Splitting a file that's grown too large

One file per type/group, plus `common.go` and `doc.go`. Then:

1. extract each section by exact line range,
2. never retype by hand,
3. diff the extracted text byte-for-byte against the original,
4. only then delete the source section/file.

## Repo hygiene

- `todo/` is tracked in git but is not source — scratch notes, SSMS
  mockups, and scratch SQL. Don't build from it or act on it unless asked
  to, and leave it out of any cleanup.
- `CHANGELOG.md` and `RELEASE.md` cover the release process. Don't edit
  either as part of a feature or fix unless asked.

## Self learning

Turn mistakes into rules, and never repeat them — but write the rule down
where it will be loaded again:

- A rule about the code or the workflow → this file, or `ARCHITECTURE.md`
  if it needs more than a paragraph.
- Work knowingly left undone, or a bug found and not fixed →
  `docs/open-threads.md`.
- What was built and how a bug was found → `docs/journal.md`.
