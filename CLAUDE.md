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
- A background goroutine that reports its result via `App.postEvent(fn)`
  must send the `a.screen.EventQ() <- tcell.NewEventInterrupt(nil)` wakeup
  (or call `a.wakeEventLoop()`, the named helper for it) **outside** the
  `postEvent` closure — right after the `postEvent(...)` call, still on the
  background goroutine. `Run()`'s event loop only drains queued callbacks
  (`drainPending()`) when it wakes up for *some* event on `EventQ()`; if the
  wakeup send is nested inside the very closure that's waiting to be
  drained, nothing will ever wake the loop up to drain it; the result sits
  queued, invisible, until an unrelated keypress happens to arrive and
  drains it as a side effect. This was a real, shipped bug — expanding an
  Object Explorer tree node showed "Loading..." forever until any other key
  was pressed — that turned out to be present in every async operation in
  `internal/tui` (`connectServer`, `connectForQueryPanel`, `loadChildren`,
  `scriptObject`, `QueryPanel.runQuery`, `DetailBrowser.ShowNodeDetails`,
  `PropertiesDialog.ShowDependencies`) except the ones already using
  `wakeEventLoop()` (`clipboard.go`) or the equivalent inline pattern
  (`tasks.go`'s `postProgress`/`postTaskDone`, later consolidated onto
  `wakeEventLoop()` itself once it grew a nil-`a.screen` guard for
  screen-less test `App`s) — see `wakeEventLoop`'s doc comment in `app.go`
  and `PropDialog.post`'s in `prop_dialog.go`, which already explained the
  trap correctly but wasn't followed everywhere.

When splitting a file that's grown too large: one file per type/group,
`common.go`, `doc.go`, and extract each section by exact line range and
diff it byte-for-byte against the original before deleting the source
file — don't retype by hand.


