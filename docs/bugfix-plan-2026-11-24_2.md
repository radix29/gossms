# Bug-fix plan, 2026-11-24 (second pass: gossms, tuikit)

Three defects found in a read-through of goSSMS, `~/go/tuikit` and
`~/go/gosmo`. Each was reproduced, not only read: B1 and B2 by a failing
scratch test against the current tree (since deleted), B3 by a scratch test
of the classifier plus a live run on `win10cli` inside a rolled-back
transaction in tempdb. None is in `docs/open-threads.md` or
`docs/decisions.md`. gosmo was read (index scripter, table/constraint
scripter, restore plan, retry, quoting/`tsql` scanning, agent schedules,
performance counters, Browser dialer) and nothing was found worth fixing.
Close an item by deleting it; delete this file with the last one. Nobody
commits but the user.

Breaking tuikit API is allowed here; B1 (done) used that.

| # | Where | User-visible in goSSMS? |
|---|---|---|
| B2 | goSSMS `internal/tui/planview` (graph and tree tabs) | Yes: Execution Plan tab |
| B3 | goSSMS `internal/cli/safety` (`classifyBatch`) | Yes: `gossms query run` with `--allow ddl` |

## Recommended implementation order

1. **B3.** A safety guardrail under-classifies: the most serious defect,
   entirely inside goSSMS, small, and independent of the other two.
2. **B2.** The PlanView gesture router. It uses the `fresh` argument B1
   added to `core.HandleScrollbarDrag` and `Viewport.HandleScrollbars`;
   B1 left PlanView's three calls passing `true` (old behavior) for B2 to
   replace.

Each tuikit step: `go build ./... && go vet ./... && gofmt -l . &&
go test ./... && staticcheck ./...` in `~/go/tuikit`. Each goSSMS step:
`go build -o gossms ./cmd/gossms && go test ./... && staticcheck ./... &&
deadcode -test ./...`, then drive the built binary with the repro
(`docs/testing.md`, tmux harness). Ask before touching tuikit's
`CHANGELOG.md`/`RELEASE.md`.

---

## B3: a GRANT after a view inside CREATE SCHEMA is hidden from the classifier

**Cause.** `classifyBatch` (`internal/cli/safety/classify.go:158`) treats
any statement for which `isModule` holds (CREATE/ALTER of PROCEDURE,
FUNCTION, TRIGGER or VIEW) as a module whose body is the rest of the batch,
and `break`s, so nothing after it is classified. Inside `CREATE SCHEMA`,
though, `CREATE VIEW` is a *schema element*: its body ends at the next
element, and the elements that can follow are `CREATE TABLE`,
`CREATE VIEW`, `GRANT`, `REVOKE` and `DENY`, all of which execute. The
statement cutter does cut at the `GRANT`, but the `break` drops it.

**Repro (verified).** `Classify` on

```sql
CREATE SCHEMA s AUTHORIZATION dbo
    CREATE VIEW v AS SELECT 1 AS x
    GRANT CONTROL ON SCHEMA::s TO public
```

returns `ddl` (statements `CREATE SCHEMA`, `CREATE VIEW`). Without the view,
the same `GRANT` is `admin`. Live on 17 (`win10cli`, tempdb, rolled back):
the batch creates the view and `sys.database_permissions` shows
`CONTROL GRANT SCHEMA` for the grantee. So `gossms query run --allow ddl`
runs an `admin` statement. A statement after the whole `CREATE SCHEMA`
(`…; CREATE TABLE …`) is refused by the server (Msg 156, the batch does not
run), so schema elements are the only exposure.

**Fix.** In `classifyBatch`, stop treating a statement as a module when the
batch's first statement is `CREATE SCHEMA`
(`createAlterObject(toks[cuts[0]:]) == ("CREATE", "SCHEMA")`). Each cut is
then classified by `classifyStatement` and floored, as `CREATE TABLE`
already is there. Only `CREATE VIEW` can be a module inside `CREATE SCHEMA`;
the server refuses the others. Don't narrow modules to `ci == 0` instead:
`TestClassifyModuleBatch` (`classify_test.go:456`, `PRINT 1` then
`CREATE PROCEDURE`) pins module treatment past the first statement on
purpose.

**Tests** (`classify_test.go`):

- the repro → `admin`, with a `GRANT` statement;
- `DENY` and `REVOKE` in the same position → their standalone class;
- `CREATE SCHEMA s CREATE VIEW v AS SELECT 1 AS x CREATE TABLE t (a int)`
  → `ddl`, three statements;
- the existing module cases unchanged.

Check `docs/cli.md` § Safety: if it describes module bodies, add the
schema-element exception in one sentence.

---

## B2: PlanView has no gesture router: a drag reselects tiles and grabs the bars

**Cause.** `PlanView.HandleMouse` (`planview_input.go:142`) sets
`mouseDragging` on any `Button1` and passes every held resend to the tab's
handler. Neither tab handler can tell a resend from a press, so each motion
event is handled as a new click:

- graph tab (`graph.go:405-470`): a press on a tile, dragged onto the inside
  horizontal or vertical bar, latches `Viewport.HandleScrollbars` and jumps
  the canvas; dragged across other tiles, it selects each tile it passes
  (each `selectNode` relays out the properties pane);
- tree tab (`tree.go:539-600`): a press on a row, dragged onto the bar column
  (`treePaneRect.Right()-1`), starts `HandleScrollbarDrag` and jumps the
  scroll. `rowDragging` stops re-toggling the pressed row but not this, and
  a drag that began in the bottom summary grid selects tree rows on entry.

This is what `~/go/tuikit/ARCHITECTURE.md` § One owner per press inside a
widget rules out; `PicturePanel` (`picture_panel_input.go:114-176`) and
`ActivityMonitor` already follow it with a `core.Gesture`.

**Repro (failing tests, verified):**

```go
v := New(); v.SetBounds(0, 0, 40, 14); v.SetPlan(loadTestPlan(t))
// press on a visible tile at (px, py), then held onto the horizontal bar:
v.HandleMouse(tcell.NewEventMouse(px, py, tcell.Button1, tcell.ModNone))
v.HandleMouse(tcell.NewEventMouse(r.Right()-3, r.Bottom()-1, tcell.Button1, tcell.ModNone))
// graphView().X went 5 -> 145, Dragging() true

v.SetBounds(0, 0, 120, 8); v.setActiveTab(TabTree)
v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+1, tcell.Button1, tcell.ModNone)) // a row
v.HandleMouse(tcell.NewEventMouse(r.Right()-1, r.Y+r.H-1, tcell.Button1, tcell.ModNone))
// treeSt.scroll went 0 -> 6
```

**Fix.** Give `PlanView` one `core.Gesture[planZone]` for the content
area, as `PicturePanel` does: `Release` on `ButtonNone`, `Route(ev,
routeDrag)` ahead of the bounds tests, `Arm` at every claiming branch of a
fresh press. Zones: graph tile (route: nothing), graph bars (route:
`HandleScrollbars(ev, false)`), graph/tree splitters (route: the
splitter), graph properties pane, tree row (route: nothing), tree bar
(route: `HandleScrollbarDrag(…, false, …)`), bottom summary grid (route:
the grid, which extends its selection), and a `none` zone for an inert
press. `rowDragging` and `sbDragging` go: the gesture replaces them. The
`mouseDragging` latch for the tab/banner/statement rows stays, but should
come from the same fresh verdict rather than being set on every `Button1`.

Keep the existing guarantees: a release reaches the summary popup and the
XML editor wherever it lands (the comments at `planview_input.go:70-89`),
and the splitters still get their own release.

**Tests** (`planview`): both repros; a press on a bar then a drag across
tiles and off the canvas still scrolls (`TestGraphScrollbarDragScrolls`
extended); a press on a tile then a drag over other tiles keeps the first
selected; a drag from the summary grid into the tree selects no row; the
existing splitter and scrollbar tests unchanged. Then drive it in the binary:
open an estimated plan large enough to scroll and do each drag by hand.

Out of scope: drag-to-pan on the graph canvas (Picture Panel has it). With
the gesture in place it is one more route, if wanted.
