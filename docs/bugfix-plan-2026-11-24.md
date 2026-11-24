# Bug-fix plan, 2026-11-24 (tuikit)

Defects found in a read-through of `~/go/tuikit`. Each was reproduced
by a failing test against the current tree (a scratch module with `replace
github.com/radix29/tuikit => ../tuikit`), not only read. None is in
`docs/open-threads.md` or `docs/decisions.md`. T1 (ValueViewer Close on a
drag) and T2 (Viewport `BarsInside` scroll range) are fixed. Close an item by
deleting it; delete this file with the last one. Nobody commits but the user.

Breaking tuikit API is allowed for these fixes; the remaining item doesn't need it.

| # | Where | User-visible in goSSMS? |
|---|---|---|
| T3 | `controls/toolbar.go` | No (App's row-0 router masks it); any other host |

Each step, in tuikit: `go build ./... && go vet ./... && gofmt -l . &&
go test ./... && staticcheck ./...`. Then in goSSMS (the `replace` is
active): `go build -o gossms ./cmd/gossms && go test ./... && staticcheck
./... && deadcode -test ./...`, then drive the built binary with the repro
below (`docs/testing.md`; tmux harness). tuikit's `CHANGELOG.md`/`RELEASE.md`:
ask before touching them.

---

## T3 — Toolbar fires a button that a press on a divider or gap is dragged onto

**Cause.** `Toolbar.HandleMouse` calls `tb.press.Fresh(ev)` only when
`buttonAt` found a button (`idx >= 0`). A press on a divider (or the padding a
host leaves inside the rect) consumes the event but claims no latch, so the
held `Button1` resent when the pointer moves onto a button is fresh and runs
its `Action` on what was never a click on it. `layout.Splitter` already
claims on every `Button1` for this reason (`~/go/tuikit/ARCHITECTURE.md` §
Press latches / Routers).

goSSMS is not affected today: `App.handleMouse` arms `ownerMenuRow` for any
row-0 press and its route swallows the resends. Any host routing the toolbar
row by position is.

**Repro (failing test, verified):**

```go
tb := controls.NewToolbar()
tb.SetButtons([]controls.ToolbarButton{{Icon: "A", Action: inc}, {Icon: "|", Divider: true}, {Icon: "B", Action: inc}})
tb.SetBounds(0, 0, 9)                     // A 0-2, | 3-5, B 6-8
tb.HandleMouse(tuikittest.Press(4, 0))    // press on the divider
tb.HandleMouse(tuikittest.Press(7, 0))    // held, onto B: Action runs
```

**Fix.** Claim the latch for every `Button1` the toolbar sees, before the
bounds test (as `Splitter` does): `fresh := tb.press.Fresh(ev)` right after
`Release`, then `if idx >= 0 && fresh { ... }`. A press outside the rect that
drifts in is then not fresh either; the release outside still clears it
(`Release` already runs first). Return values unchanged.

**Tests** (`controls/toolbar_test.go`): the repro; press outside the rect,
drag onto a button → no action; an ordinary press on a button still fires
once and its resends don't.

**Out of scope, same pattern:** `widgets.Button`, `CheckBox` and `RadioBox`
also call `Fresh` only on a hit. They sit in dialogs whose
`dialogs.FieldGesture` routes drags to the field that took the press, so no
repro was found; if a host is found driving one by position, apply the same
one-line change. Not a T3 blocker; record in `docs/open-threads.md` if left.

---

## Reviewed and clean

`loop` (Latest, Poster, Runner, Tasks), `clipboard.Dispatcher`,
`core/runecol.go`, `core/strutil.go` (Truncate*, ParseFormattedNumber,
SafeFileName), `dialogs.Stack`/`Lazy`, `controls.TreeModel`, `TreeView`
(title layout, close button, gestures), `ListBox`, `DropDown`, `InputField`,
`layout.Splitter`, `PanelManager` (panel add/remove, tab strip),
`menuCascade`/`MenuBar`/`ContextMenu` (left-press menus are covered by
`core.OverlayGuard` in the host), editor search/replace/undo,
`DataGrid.SetSourceReordered`, `charts` scale/axis, `ProgressDialog.Run`,
`propsheet` `FilterIndex`/`PendingEdits`/`PreservingSelectRow`/`Form` focus.
