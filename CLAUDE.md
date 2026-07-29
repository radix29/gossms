# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. Read this first; it points
to the rest of the docs rather than repeating them.

## What this is

goSSMS is a portable, cross-platform terminal TUI reimplementation of SQL
Server Management Studio, written in Go 1.26. Runs on Linux/macOS/Windows
with no OS-specific code, no CGO. Version is resolved automatically from
the pushed git tag — see `internal/version/version.go` — never hand-edited.

- Module: `github.com/radix29/gossms` — https://github.com/radix29/gossms
- Depends on `github.com/radix29/gosmo` (`v0.0.5`), the author's own
  companion library for SQL Server management objects —
  https://github.com/radix29/gosmo — and `github.com/gdamore/tcell/v3`
  (`v3.4.1`) for the TUI backend.
- The author's local layout has both repos as siblings: `~/go/gossms` and
  `~/go/gosmo`.

## Read next

1. `README.md` — features and keyboard reference, user-facing.
2. `ARCHITECTURE.md` — package map, file tree, the `tuikit`/`tui` split
   rationale, the `mouseDragging` and `postEvent`/`wakeEventLoop` idioms,
   local gosmo dev workflow. Developer-facing.
3. `internal/tuikit/README.md` — the TUI library's package map, dependency
   direction, and design rules (callbacks-only, `core.Rect` geometry,
   `core.DisplayWidth` not `len()`, overlays drawn last). Required reading
   before touching anything under `internal/tuikit`.

## The one rule that matters most: verify against real source, don't guess

Guessing at `tcell`/`gosmo` API shapes from training data produces code
that *looks* plausible but doesn't match the real API. Gotchas specific to
these two dependencies: `Screen.PollEvent`/`PostEvent` don't exist in
tcell v3 — replaced by a channel, `EventQ()`; the modifier accessor is
`Modifiers()`, not `Mod()`; `gosmo.Server` has `DatabaseByName(name)`, not
`Database(name)`; `gosmo.Database.Name` / `.State` / `.RecoveryModel` etc.
are *methods*, not fields.

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

Ignore the folder todo and its content.

## Changing gosmo

`gosmo` is the author's own library, not a third-party dependency — add
or change functionality in it when gossms needs something it doesn't
have yet, rather than working around a missing capability inside gossms.
For the local dev workflow (the `replace` directive, build/test-in-gosmo
steps, and gosmo's own file-layout/method-pair/Seq/error-wrapping
conventions), see the `dev-with-local-gosmo` skill.

## Coding conventions

- **Comments: short, describe what the code does — not why a decision
  was made, what alternatives were rejected, or what trade-offs were
  discussed.** That kind of explanation doesn't belong in the code.
  (Some existing comments are more verbose than this; that's not the
  target style.)
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
  `connect_dialog.go`, …); some — `app.go` in particular — are large
  enough that splitting further the same way would help.
- An overlay that's drawn last (see `internal/tuikit/README.md`'s
  "overlays drawn last" rule — `DataGrid`'s right-click menu and "Show
  Value" popup, `DropDown`'s open list, …) must also get **first refusal**
  of every key/mouse event while it's open, in whatever host lays that
  widget out alongside another focusable one. `propsheet.Form.HandleMouse`
  does this correctly (the focused row is tried before any position-based
  routing); `QueryPanel` didn't for its results grid — the popup floats
  centred on the whole screen, so its coordinates land inside the SQL
  editor's rect too, and Shift+arrows/Escape/clicks meant for the popup
  silently went to the editor instead. Fixed via `DataGrid.OverlayActive()`
  checked at the top of `QueryPanel.HandleKey`/`HandleMouse`. Any future
  host that lays an overlay-owning widget next to another focusable one
  needs the same check.
- **A mouse gesture belongs to whatever claimed its first press, start to
  finish.** tcell resends `Button1` on every motion event while the button
  is held, so any router that dispatches by screen position must record the
  region that took the fresh press and replay every later event to it until
  the release — otherwise a drag that merely wanders somewhere else is acted
  on there. All three routers do this the same way: `App.gestureOwner`
  (`app_events.go`), `QueryPanel.dragZone` (`query_panel.go`), and
  `propsheet.PropertySheet.dragZone` (`sheet_input.go`), each with an
  `armGesture`/`armDrag` at every branch that claims a press and a
  `routeGesture`/`routeDrag` that replays to the owner. Every one of these
  was a real, reproduced bug: a selection drag out of the SQL editor resized
  the panes and flipped the results tab, one leftward out of the editor
  armed an Object Explorer drag-and-drop, and a scrollbar drag in a
  Properties dialog fired Script Changes. Also snapshot the modal layer
  (`App.gestureOverlay`) — a dialog opened mid-gesture sees the held button
  as a fresh press, since it saw nothing before it existed. A new router, or
  a new region inside an existing one, needs the same treatment.
- **A latch must not survive into the next showing of the same widget.** The
  flip side of the rule above: a dialog button closes its dialog on the
  *press*, so `ConsumeOutsideClick`'s reset never runs for the matching
  release — `HandleMouse` bails on `!visible`, and `App` has already dropped
  the dialog from `dialogStack`. `ModalDialog.mouseDragging` therefore stayed
  latched, and `ButtonClicked` silently refused the first click of every
  *re*-opening; the dialog looked frozen until an unrelated release cleared
  it. Fixed by clearing both latches in `ModalDialog.Show()`. Any widget that
  can be hidden mid-gesture by its own click handler needs the same.
- A background goroutine reports its result with **`App.postAndWake(fn)`**.
  Don't hand-write its two halves (`a.postEvent(fn)` then
  `a.wakeEventLoop()`) — the helper exists so the ordering can't be got
  wrong. The wakeup has to be sent **outside** the `postEvent` closure,
  right after the `postEvent(...)` call, still on the background goroutine:
  `Run()`'s event loop only drains queued callbacks (`drainPending()`) when
  it wakes up for *some* event on `EventQ()`, so a wakeup nested inside the
  very closure that's waiting to be drained can never fire, and the result
  sits queued, invisible, until an unrelated keypress drains it as a side
  effect. This was a real, shipped bug — expanding an Object Explorer tree
  node showed "Loading..." forever until any other key was pressed — and it
  was present in every async operation in `internal/tui` at the time.
  `QueryPanel`'s elapsed-timer tick is the one legitimate bare
  `wakeEventLoop()` caller: it has no callback to post, only a redraw to
  ask for. See `postAndWake`'s and `wakeEventLoop`'s doc comments in
  `app.go`.

When splitting a file that's grown too large: one file per type/group,
`common.go`, `doc.go`, and extract each section by exact line range and
diff it byte-for-byte against the original before deleting the source
file — don't retype by hand.


