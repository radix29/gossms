# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. Read this first; it points
to the rest of the docs rather than repeating them.

## What this is

goSSMS is a portable, cross-platform terminal TUI reimplementation of SQL
Server Management Studio, written in Go 1.26. Runs on Linux/macOS/Windows
from a single build — no CGO, no build-tag-split files. `runtime.GOOS`
branching exists in exactly two places: `internal/tui/os_clipboard.go`,
which shells out to `xclip`/`wl-copy`/`pbcopy`/`clip` with an OSC-52
fallback, and `internal/version/version.go` for the version string. Version
is resolved automatically from the pushed git tag — see
`internal/version/version.go` — never hand-edited.

- Module: `github.com/radix29/gossms` — https://github.com/radix29/gossms
- Depends on `github.com/radix29/gosmo`, the author's own companion library
  for SQL Server management objects — https://github.com/radix29/gosmo —
  and `github.com/gdamore/tcell/v3` (`v3.4.1`) for the TUI backend.
- `go.mod`'s `require` pins a gosmo tag, but the `replace
  github.com/radix29/gosmo => ../gosmo` directive is deliberately **active**
  during development, so builds use the sibling checkout rather than the
  pinned tag, and `HEAD` may depend on gosmo code that isn't tagged yet. See
  ARCHITECTURE.md's "Developing against a local gosmo checkout".
- The author's local layout has both repos as siblings: `~/go/gossms` and
  `~/go/gosmo`.

## Read next

Read what the task actually touches — a one-file fix needs none of this.

1. `README.md` — features and keyboard reference, user-facing.
2. `ARCHITECTURE.md` — package map, file tree, the `tuikit`/`tui` split
   rationale, the `mouseDragging`/gesture-ownership and `postAndWake` idioms
   in full, local gosmo dev workflow. Read before a change that spans
   packages, and whenever a convention below points at it.
3. `internal/tuikit/README.md` — the TUI library's package map, dependency
   direction, and design rules (callbacks-only, `core.Rect` geometry,
   `core.DisplayWidth` not `len()`, overlays drawn last). Required reading
   before touching anything under `internal/tuikit`.
4. `docs/open-threads.md` — bugs knowingly left unfixed, scope repeatedly
   deferred, and what blocks the next release. Check before reporting
   something as newly found, and add to it when leaving work undone.
5. `docs/journal.md` — dated archive of what was built and which bugs were
   found how. Not required reading; search it when you need the history
   behind a design.

## The one rule that matters most: verify against real source, don't guess

Guessing at `tcell`/`gosmo` API shapes from training data produces code
that *looks* plausible but doesn't match the real API. Gotchas specific to
these two dependencies: `Screen.PollEvent`/`PostEvent` don't exist in
tcell v3 — replaced by a channel, `EventQ()`; the modifier accessor is
`Modifiers()`, not `Mod()`; `gosmo.Database.Name` / `.State` /
`.RecoveryModel` etc. are *methods*, not fields.

`gosmo.Server` has **both** `Database(name)` and `DatabaseByName(name)`, and
they are not interchangeable — picking the wrong one fails quietly:

- `Database(name) *Database` returns a lightweight handle without querying
  at all. Its `State`/`RecoveryModel`/`Collation`/`CompatibilityLevel` stay
  zero-valued. Every write method needs only the name, so this is the right
  choice for issuing `ALTER`-style calls against a database already known to
  exist — and the *only* one that works under a `WithScript`-derived context,
  where `DatabaseByNameContext`'s lookup is a real read that script mode
  doesn't capture.
- `DatabaseByName(name) (*Database, error)` queries `sys.databases`, so it
  verifies existence and populates those fields. Use it when you need to
  read them or to confirm the database is there.

Before writing any code that calls into `tcell` or `gosmo`, check the real
source first if there's any uncertainty — `go doc`, grep the module cache
(`go env GOMODCACHE`), or read the sibling `~/go/gosmo` checkout directly.
Don't rely on memory of "similar" APIs from other versions or packages.

## Build & verify

```
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
same signature, same results, same errors.

For the rest of the local dev workflow (the `replace` directive and
its release-time handling, and gosmo's own file-layout/method-pair/Seq/
error-wrapping conventions), see the `dev-with-local-gosmo` skill and
ARCHITECTURE.md's "Developing against a local gosmo checkout".

## Coding conventions

- **Comments: describe what the code does. Add the "why" only when getting
  it wrong reintroduces a bug, and then say so concretely.** The bar is
  whether the next person to touch this code would break something without
  the note — not whether the reasoning was interesting.
  - Worth writing, and load-bearing here: the invariant a `mouseDragging`
    latch protects, why `postAndWake`'s two halves can't be called by hand,
    why `SelectRow.orig` is read back from the widget, why `escapeSingle`
    goes on top of bracket-quoting. Each of those is a shipped bug that a
    plausible "simplification" would bring straight back. The comment is the
    only thing standing between the invariant and the next refactor.
  - Not worth writing: alternatives considered and rejected for no lasting
    reason, restatements of what the line plainly says, and any narration of
    how the code came to look this way. Those belong in `docs/journal.md`.
  - Prefer one sharp sentence naming the failure to a paragraph of
    discussion. Where a long comment is genuinely earned, keep it on the
    declaration it protects rather than spreading it through the body.

  This rule was rewritten 2026-07-30. It previously banned "why" comments
  outright, which contradicted the codebase — `app.go`, `datagrid.go`,
  `secret.go`, `propsheet/common.go` are 33-50% comments, mostly rationale,
  and that rationale has repeatedly been what stopped a regression. The old
  wording would have had a session strip exactly the notes worth keeping.
  Existing long comments are not a cleanup target; judge new ones by the
  failure-mode bar above.
- Go 1.26 features in active use: `new(T{...})` composite-literal syntax,
  the `slices` package, `errors.AsType`.
- `core.DisplayWidth(s)`, never `len(s)`, for any column-position math —
  terminal columns aren't byte length. See `internal/tuikit/README.md`.
- `tuikit` is a strictly one-way dependency graph and knows nothing about
  `tui` (the application layer) — see that README before adding anything
  there.
- Every `tuikit` sub-package is organized one file per type or tightly
  related group of types, plus `common.go` for helpers shared across more
  than one file in the package and `doc.go` for the package doc comment.
  `internal/tui` files are organized one-per-purpose (`app.go`, `menu.go`,
  `connect_dialog.go`, …); the largest now are `restore_dialog.go`,
  `planview/planview.go`, `query_panel.go` and `backup_dialog.go`, any of
  which would benefit from the same treatment.
- **Mouse and overlay handling: read ARCHITECTURE.md's "The mouseDragging
  idiom" before touching any `HandleMouse`.** Every rule there came from a
  reproduced, shipped bug. The enforceable summary:
  - A gesture belongs to whatever claimed its first press, start to finish.
    tcell resends `Button1` on every motion event while the button is held,
    so a router that dispatches by screen position must record the region
    that took the fresh press and replay every later event to it until the
    release. Three routers do this: `App.gestureOwner` (`app_events.go`),
    `QueryPanel.dragZone` (`query_panel.go`), and
    `propsheet.PropertySheet.dragZone` (`sheet_input.go`) — each with an
    `armGesture`/`armDrag` at every branch that claims a press and a
    `routeGesture`/`routeDrag` that replays to the owner. `App` also
    snapshots the modal layer (`gestureOverlay`/`overlaySnapshot`) and drops
    held events across a change, since a dialog opened mid-gesture sees the
    held button as a fresh press. A new router, or a new region inside an
    existing one, needs all of it.
  - A widget that fires an action on `Button1` needs a per-widget
    `mouseDragging` latch, set on the triggering press and cleared on the
    matching `ButtonNone` release.
  - A latch must not survive into the next showing of the same widget: a
    dialog button closes its dialog on the *press*, so the matching release
    never clears it. `ModalDialog.Show()` clears both latches. Any widget
    that can be hidden mid-gesture by its own click handler needs the same.
  - A host with an early `return` in its own `HandleMouse` must forward a
    `ButtonNone` release to a latch-bearing child before returning, or the
    child's latch sticks and swallows its next press.
  - An overlay drawn last (`internal/tuikit/README.md`'s "overlays drawn
    last" rule) gets **first refusal** of every key/mouse event while open,
    in whatever host lays it out beside another focusable widget — see
    `DataGrid.OverlayActive()` checked at the top of
    `QueryPanel.HandleKey`/`HandleMouse`, and the focused row tried before
    positional routing in `propsheet.Form.HandleMouse`.
- A background goroutine reports its result with **`App.postAndWake(fn)`**,
  never its two halves (`postEvent` then `wakeEventLoop`) by hand — the
  ordering is subtle, and getting it wrong leaves the result queued and
  invisible until an unrelated keypress drains it as a side effect (a real,
  shipped bug: tree nodes stuck on "Loading..."). `QueryPanel`'s
  elapsed-timer tick is the one legitimate bare `wakeEventLoop()` caller: it
  has no callback to post, only a redraw to ask for. See ARCHITECTURE.md's
  "Async result delivery: postAndWake" and the doc comments in `app.go`.

When splitting a file that's grown too large: one file per type/group,
`common.go`, `doc.go`, and extract each section by exact line range and
diff it byte-for-byte against the original before deleting the source
file — don't retype by hand.

## Repo hygiene

- `todo/` is tracked in git but is not source — scratch notes, SSMS
  mockups, and scratch SQL. Don't build from it or act on it unless asked
  to, and leave it out of any cleanup.
- `CHANGELOG.md` and `RELEASE.md` cover the release process. Don't edit
  either as part of a feature or fix unless asked.
