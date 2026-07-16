# Execution Plan Viewer — Architecture & Implementation Plan

Status: **implemented (2026-07-16)**, all 5 phases below. Scope was the
reusable control only; binding into QueryPanel / menus / plan generation
is still a later step (the API below was designed against it so nothing
needs to change when that step comes).

Decisions settled with the author (2026-07-15): control lives in
`internal/tui/planview` (parser in `internal/showplan`); graph tab uses
SSMS-classic orientation (root/SELECT at the left); `cmd/plandemo` dev
harness is kept checked in; the originally-planned "tree compact" tab was
dropped — the control has **three** tabs (Plan, Tree, XML).

Built as designed, with a few adjustments made during implementation:
- **Focus model for the Tree tab's bottom section**: the design didn't
  originally call out that Enter needs to route to *either* the tree
  (toggle expand) *or* the Summary table (jump to operator) depending on
  which one currently has keyboard focus. Added an explicit
  `bottomFocused` field, toggled by Tab, gating Up/Down/PgUp/PgDn/Enter;
  sort keys (c/r/t) stay reachable regardless of focus.
- **`OnCopyRequest` callback dropped.** The design's Ctrl+C-triggered
  callback would never fire in practice — `App` intercepts Ctrl+C
  globally before any panel sees it (`app_events.go`). `HasSelection`/
  `SelectedText`/`Cut`/`Paste`/`SelectAll` (the real `clipboardTarget`
  integration path, matching `DetailBrowser`'s precedent) do the actual
  work; `OnOpenInPanel` and `OnStatus` remain as designed.
- **`SetIconSet`/`IconSet` dropped.** Nothing in the graph/tree renderers
  ended up needing a swappable glyph table — operator icons weren't
  implemented (tiles/tree rows identify operators by name text only, no
  icon column) to avoid unused API surface; can be added later without
  any other change.
- **Graph tile height is 5 rows, not 4** — the mockup's "~20 cols × 4–5
  rows" range turned out to need the full 5: 3 interior text lines
  (PhysicalOp, object/LogicalOp, cost%+rows) don't fit between the
  borders at height 4 (a real bug caught via `cmd/plandemo`, not by
  tests — see the graph_layout.go comment).
- **`internal/showplan.Indent`** needed a trailing-newline fix beyond
  what was originally scoped as a one-line pretty-printer — a
  genuinely single-line XML document with an ordinary EOF newline was
  being mistaken for "already multi-line" and returned unindented.
- **Post-ship fixes (2026-07-16, found via live use):** the tree|details
  `layout.Splitter`'s `dragging` flag got stuck `true` after any drag —
  `handleTreeTabMouse`'s `switch ev.Buttons()` had no `ButtonNone` case,
  so the release event never reached `treeSplit.HandleMouse`, and the
  next plain click anywhere in the tab kept moving the divider instead of
  selecting a tree row. Fixed by forwarding `ButtonNone` to the splitter
  before any position routing (same pattern `App`/`QueryPanel` already
  used). Separately, the Operator Details pane (right of the splitter)
  was reformatted to match the original mockup: a one-row header
  (`detailsHeaderRect`, "Operator Details" + a right-aligned "Scroll
  ▲/▼" indicator) above a scrollable content rect
  (`detailsContentRect`), an expanded curated field list (`detailKVs` in
  details.go — Physical/Logical Operator, Object, Output Columns,
  Predicate(s), Estimated/Actual Rows, Estimated I/O/CPU, Actual
  CPU/Duration, Memory Grant [root operator only, statement-level in real
  SQL Server], Parallel, Warnings), with every label padded to the
  widest one (`core.PadRight`) so the `:` separators line up in a
  column, and mouse-wheel scrolling (`detailsScroll` on `PlanView`).
- **Plan tab detail strip redesigned (2026-07-16, same follow-up
  session)**, per a second mockup in `todo/todo.txt` ("Plan view: split
  the pane horizontally 70/30…"): the single "Selected Operator"
  strip/`graphStripHeight` was replaced with a fixed 70/30
  canvas/strip split (`graphCanvasRatio`), itself split 65/35
  (`graphStripPropsRatio`) into a **Properties** block (reusing the Tree
  tab's new `detailLines`/`drawDetailsHeader` machinery under the title
  "Properties" instead of "Operator Details") over an **Operator
  Summary** block (the same shared `summarySt` grid the Tree tab uses).
  `drawDetailsHeader`/`detailsHeaderText` gained a `title` parameter and
  `drawSummary` an explicit `rect` parameter so both tabs could share
  them without duplicating the rendering logic; `bottomFocused` (Tab to
  toggle) is now genuinely control-level, not Tree-tab-specific, since
  both tabs' Operator Summary views share the exact same grid instance.
- **Plan tab strip revised again (2026-07-16, same session, second
  follow-up):** the fixed 70/30 ratio became a draggable `graphSplit`
  (`layout.NewHorizontalSplitter`, default ratio 0.7 — same widget/pattern
  as `treeSplit`, including the `ButtonNone`-forwarding fix above so a
  drag can't get stuck), the Operator Summary block was removed from the
  Plan tab entirely (it stayed Tree-tab-only), and the one genuinely new
  piece of information it carried — **Cost %** — was folded into
  `detailKVs` itself, so it now appears in Properties on both tabs
  instead of a second grid duplicating Rows/Time/Operator/Object/Status
  that Properties already covers by other names. The Properties strip is
  now open from first load (`graphSt.detailOpen` defaults `true`); Enter
  still toggles it. `summaryHeaderStyleAndText` dropped its `cycleHint`
  bool parameter (Plan tab was its only other caller).

---

## 1. What we're building

A reusable TUI control that renders a SQL Server execution plan from its
ShowPlanXML text — estimated or actual, the control doesn't care; it
presents whatever the XML contains. Three visualization tabs:

| Tab | Name | Content |
|-----|------|---------|
| 1 (default) | **Plan** | SSMS-style graphical plan: operator tiles connected by elbow edges, scrollable in all directions |
| 2 | **Tree** | Expanded tree per mockup: header metrics + plan tree + operator details pane + properties/summary section |
| 3 | **XML** | The raw plan XML in a read-only `controls.Editor` — line numbers, selection, copy |

The control is host-agnostic: later it will be embedded in QueryPanel's
results area (as an extra "Execution Plan" tab) *and* wrapped in a
standalone `layout.Panel` like Object Explorer Details. It carries an
"Open in Panel" button that fires a callback — the host decides what that
means.

## 2. Package layout — two new packages

### 2.1 `internal/showplan` — parser + domain model (no TUI)

Pure data package: ShowPlanXML in, operator tree out. Zero tcell/tuikit
imports, fully unit-testable against the real example plan.

```
internal/showplan/
├── doc.go        package doc
├── model.go      Plan, Statement, Node, Runtime, Object, KV
├── parse.go      Parse/ParseReader — UTF-16/BOM handling, RelOp tree walker
├── indent.go     Indent(xml) — pretty-printer for single-line plan XML
├── parse_test.go
└── testdata/ExecutionPlan.xml   (UTF-16 fixture, copied from todo/plan)
                + estimated_plan.xml (small synthetic single-line, no runtime)
```

**Model** (all fields resolved at parse time; views never touch XML):

```go
type Plan struct {
    Version, Build string
    Statements     []*Statement
    XML            string        // input decoded to UTF-8 — feeds the XML tab
}

type Statement struct {
    Text, Type       string      // StatementText, SELECT/UPDATE/…
    SubTreeCost      float64     // StatementSubTreeCost — denominator for node cost %
    EstRows          float64
    DOP              int         // QueryPlan DegreeOfParallelism
    QueryHash        string
    TimeStats        *TimeStats  // CpuTime/ElapsedTime ms; nil on estimated plans
    MemoryGrant      *MemoryGrant// granted/max-used KB; nil when absent
    MissingIndexes   []MissingIndex
    Warnings         []string    // statement-level (<Warnings> under QueryPlan)
    Root             *Node       // nil for plan-less statements (SET, USE, …)
    Props            []KV        // every statement/QueryPlan attribute, ordered — Properties pane
}

type Node struct {
    ID                     int      // NodeId
    PhysicalOp, LogicalOp  string
    Object                 Object   // {Database, Schema, Table, Index, Alias, IndexKind}
    EstRows, EstRowsRead   float64
    EstIO, EstCPU          float64
    EstSubtreeCost         float64
    AvgRowSize             float64
    Parallel               bool
    ExecMode               string   // Row / Batch
    Predicate, SeekPredicate string // ScalarString of the significant predicates
    OutputColumns          []string
    Warnings               []string // <Warnings> children: SpillToTempDb, NoJoinPredicate, …
    Runtime                *Runtime // nil = estimated-only node
    Props                  []KV     // all RelOp attrs + op-specific details, ordered
    Children               []*Node
}

type Runtime struct { // aggregated over RunTimeCountersPerThread
    Rows, RowsRead, Executions       int64 // summed across threads
    ElapsedMS, CPUMS                 int64 // max across threads (SSMS semantics)
    LogicalReads, PhysicalReads      int64 // summed
    Threads                          int
}
```

Derived helpers (methods, matching gosmo's methods-not-fields habit):

- `Node.Cost(stmtTotal float64) float64` — SSMS node cost:
  `max(0, EstSubtreeCost − Σ children.EstSubtreeCost) / stmtTotal`.
- `Statement.Nodes() []*Node` — preorder walk; feeds summary table,
  warning jump, and search.
- `Plan.HasActual() bool` — any node carries Runtime.

**Parsing strategy:**

- **Encoding.** SSMS saves `.sqlplan` as UTF-16 (LE or BE) with BOM; plans
  fetched over the wire arrive as UTF-8 strings, optionally with their own
  BOM. `Parse` sniffs the BOM (FF FE / FE FF / EF BB BF) and transcodes
  UTF-16 → UTF-8 using only the standard library (`encoding/binary` +
  `unicode/utf16`) — simpler than originally planned; `x/text`'s
  `encoding/unicode` package turned out not to be needed at all, so no
  dependency promotion was required. The `xml.Decoder` gets a
  `CharsetReader` that accepts the `encoding="utf-16"` declaration left in
  the (already transcoded) stream.
- **RelOp tree.** Children of a `<RelOp>` are nested inside an
  operator-specific wrapper element (`<NestedLoops>`, `<Top>`,
  `<IndexScan>`, `<Hash>`, `<Sort>`, …) — dozens of kinds. Rather than
  modelling the whole showplan XSD, `RelOp` gets a custom `UnmarshalXML`
  token walker: decode the known informational children (`OutputList`,
  `Warnings`, `RunTimeInformation`), then descend generically into the
  one remaining wrapper element collecting nested `<RelOp>`s (in document
  order) as children, plus `<Object>` and the `ScalarString` of
  `Predicate`/`SeekPredicates`-style elements into fields/Props. Unknown
  operators still render — `PhysicalOp`/`LogicalOp` attributes are always
  present — which makes the parser tolerant of SQL Server versions newer
  than whatever we tested against.

### 2.2 `internal/tui/planview` — the control

**Why here and not `internal/tuikit`:** tuikit's README opens with "It
knows nothing about SQL Server" — an execution-plan viewer is intrinsically
showplan-shaped, so putting it there would break the charter (a generic
"metric-annotated DAG viewer" abstraction in tuikit + adapter in tui would
satisfy the letter of the rule but is speculative indirection nothing else
would use). **Why a subpackage and not flat files in `internal/tui`:** the
control must stay reusable and must not reach into `App`; once
`internal/tui` imports `planview` (at binding time), the compiler itself
forbids `planview → tui` imports. Same precedent as `tuikit/propsheet`: a
big composite gets its own package. It composes tuikit
(`core`/`theme`/`controls`/`layout`) + `internal/showplan`, and talks
outward only through callbacks.

```
internal/tui/planview/
├── doc.go
├── planview.go            PlanView root: tab bar, statement selector, routing, public API
├── graph.go               Tab 1 — canvas draw, selection, navigation, detail strip
├── graph_layout.go        Tab 1 — pure tile-placement algorithm (unit-testable, no screen)
├── tree.go                Tab 2 — plan tree, expand/collapse/scroll, bottom-section dispatch
├── details.go             Operator-details / properties key-value pane (shared: graph strip, tree pane)
├── summary.go             Operator summary table — controls.DataGrid, sortable c/r/t
├── search.go              Cross-tab operator search (/, n/N), warning-jump (w), est/actual toggle (p)
├── planview_test.go, graph_test.go, graph_layout_test.go, tree_test.go, search_test.go
```

Colour and cost/warning styling is derived inline from `theme.Active()`
at each draw site (tree.go/graph.go) rather than a separate `style.go` —
it turned out to be a handful of `switch` statements, not enough shared
logic to justify its own file. Operator icons (a planned `icons.go` with
an `IconSet`/`config.IconStyle`-equivalent glyph table) were dropped:
tiles and tree rows identify operators by name text alone; nothing ended
up needing a swappable glyph set, so the API surface wasn't added.

**Public API:**

```go
func New() *PlanView

func (v *PlanView) SetPlanXML(xml string) error // parse + install; error also rendered inline
func (v *PlanView) SetPlan(p *showplan.Plan)    // when the caller already parsed
func (v *PlanView) Plan() *showplan.Plan

// The standard tuikit widget contract:
func (v *PlanView) SetBounds(x, y, w, h int)
func (v *PlanView) Draw(s tcell.Screen)
func (v *PlanView) HandleKey(ev *tcell.EventKey) bool
func (v *PlanView) HandleMouse(ev *tcell.EventMouse) bool
func (v *PlanView) SetActive(active bool)

// clipboardTarget integration (internal/tui/clipboard.go) — the same
// interface DetailBrowser satisfies for its grid, so a host wires this in
// exactly the same way at binding time: XML tab → editor selection; Plan
// and Tree tabs → selected operator's details (see details.go's
// formatDetailsText) as their "selection", since there's no free-form
// text there. Cut returns the same as SelectedText (read-only view,
// matching DataGrid.Cut's convention); Paste is a no-op.
func (v *PlanView) HasSelection() bool
func (v *PlanView) SelectedText() string
func (v *PlanView) Cut() string
func (v *PlanView) Paste(text string)
func (v *PlanView) SelectAll()

// Callbacks (tuikit convention — no upward imports):
v.OnOpenInPanel func()          // "Open in Panel" button; button hidden while nil
v.OnStatus      func(msg string) // one-line status reporting (tab switch, search "no matches", ...)
```

`OnCopyRequest` (Ctrl+C-triggered, in the original design) was dropped —
`App` intercepts Ctrl+C globally before any panel ever sees the key
(`app_events.go`), so a callback firing from PlanView's own `HandleKey`
would never actually run; the `clipboardTarget` methods above are the
real integration path, matching how `DetailBrowser` does it.

`SetPlanXML` never blocks on anything but parsing (a few ms); no
goroutines anywhere in the control — same "async is the host's problem"
stance as propsheet.

## 3. Control layout & behaviour

```
┌ Plan │ Tree │ XML ──────────────────────────────────[⧉ Open in Panel]┐ ← tab bar (1 row)
│ ◀ Statement 1/3 ▶  33%  SELECT TOP (1000) PatientID, PatientName, …  │ ← only when >1 statement
│                                                                      │
│                        (active tab view)                             │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

- **Tab switching:** click, or keys `1`–`3`. Deliberately *not*
  Ctrl+PgUp/PgDn — QueryPanel already uses those for its own results tabs,
  and this control will live inside that results area.
- **Statement selector:** a ShowPlanXML can hold many statements (one per
  statement in the batch). One statement is displayed at a time; `[` / `]`
  (and clicking ◀ ▶) cycle. The row shows the statement's cost share of
  the batch and its truncated text. Hidden for single-statement plans.
- **Tab and selector rows follow the drawTabBar/tabAt pattern from
  QueryPanel** (`core.DisplayWidth` walk shared by draw + hit-test).
- Selected-operator state (`selID`) is shared between the Plan and Tree
  tabs: selecting a node in the graph and switching to Tree keeps the same
  operator selected.

### 3.1 Tab 1 — Plan (graph, default)

SSMS-classic node-and-edge layout, per `exec.png`:

- **Orientation:** root (SELECT) at the **left**, children extending
  right, arrowheads pointing left toward the parent — matching real SSMS
  and the provided screenshot (confirmed with the author over the tab-1
  sketch's root-right flow).
- **Tiles:** fixed-width, 20×5 cells (5, not the mockup's low end of 4 —
  see the "Built as designed" note above):

  ```
  ┌──────────────────┐      ╔══════════════════╗
  │Nested Loops      │      ║Clustered Index Sc║  ← selected = double border
  │Inner Join        │      ║Orders            ║
  │1%  9 rows        │      ║45%  245782 rows ⚠║
  └──────────────────┘      ╚══════════════════╝
  ```

  Line 1: PhysicalOp; line 2: LogicalOp when it differs, else the
  object's table name; line 3: cost % + row count (actual when
  available, unless `p` asks for the estimate — see 3.4) with a `⚠`
  marker at the top-right corner when the node has warnings. No icon
  column (see the package-layout note above).
- **Layout algorithm** (`graph_layout.go`, pure function → tested in
  `graph_layout_test.go` with no screen involved): recursive placement on
  a virtual cell canvas. `place(node, depth, top)` positions a childless
  node's tile directly; a node with children first places each child
  (stacked top-to-bottom with a 1-row gap) and then centers its own tile
  between its first and last child's tile Y. Returns `(bandBottom,
  tileY)`. Edges are computed in a second pass from each node's placed
  rect: parent's right-edge midpoint → a trunk column midway between the
  two columns → child's left-edge midpoint, three straight segments plus
  a `◄` arrowhead at the parent end.
- **Scrolling:** `scrollX`/`scrollY` offset every draw call, clipped
  manually cell-by-cell (`putClipped`/`hlineClipped`/`vlineClipped` in
  graph.go) since `core`'s line/text primitives don't clip to an
  arbitrary sub-rect on their own. A tile draws only when its whole rect
  is within the viewport — partial-glyph clipping wasn't worth the
  complexity for a fixed-size card. Mouse wheel scrolls vertically;
  Shift+wheel scrolls horizontally; both scrollbars via
  `core.DrawScrollbar`.
- **Navigation:** `←` parent, `→` first child, `↑`/`↓` previous/next
  sibling, falling back to the nearest tile in the same column
  (depth) above/below when there's no sibling in that direction; `Home`
  jumps to the root. Selection auto-scrolls into view
  (`ensureTileVisible`) and is shared with the Tree tab (`selectedID`
  on `PlanView`) — selecting a node in either tab keeps it selected in
  the other. A bottom **Properties strip** is visible from first load
  (`graphSt.detailOpen` defaults `true`); `Enter` toggles it. Sized by a
  draggable `graphSplit` (`layout.NewHorizontalSplitter`, same widget
  `treeSplit` uses, default ratio 0.7) between the canvas and the strip —
  drag the bar with the mouse, or `Ctrl+↑`/`Ctrl+↓` while the strip is
  open. The strip holds **Properties** only (the same `detailLines`/
  `detailKVs` aligned key/value list and header+scroll-indicator the Tree
  tab's Operator Details pane uses — see 3.2 and 3.4); there's no
  Operator Summary grid here — that stayed Tree-tab-only, and the one
  field it carried that Properties didn't already have (**Cost %**) was
  added to `detailKVs` directly instead of showing a second grid.
  Properties scrolls independently via mouse wheel (`graphPropsScroll`).

### 3.2 Tab 2 — Tree

`tree.go`, per the big mockup:

```
│ Est Cost: 0.0188  DOP: 1  CPU: 0 ms  Elapsed: 0 ms  Mem: —  Hash: 0x7D98… │ ← header (metrics
├──────────────────────────────────────────┬────────────────────────────────┤    present in XML;
│ ▼ SELECT (0%)                            │ Operator Details    Scroll ▼  │    "—" when absent)
│   ▼ Top (0%)                             │ Physical Operator : Clustered…│
│     ▼ Nested Loops (Left Outer Join) (1%)│ Logical Operator  : Clustered…│
│       ├──► Nested Loops (Inner Join)     │ Object             : dbo.Appt…│
│       │ …                                │ Estimated Rows     : 5        │
│       └──► Clustered Index Scan (19%) ⚠  │ Actual Rows        : 5        │
│             MedicalRecords [PK_Medical…] │ Estimated I/O      : 0.00     │
├──────────────────────────────────────────┴────────────────────────────────┤
│ [Properties ▾]   (o cycles: hidden → properties → summary)                │
│ Physical Operator : Clustered Index Scan                        …         │
```

- **Node rendering:** each operator renders as one line — ancestor
  continuation bars (`│`/blank), this node's own `├─`/`└─` connector, an
  expand/collapse chevron (`▼`/`▶`, only for operators with children),
  `PhysicalOp (LogicalOp) (cost%)`, a `⚠` marker, and the object's short
  name when present. Right of a `layout.Splitter`: the **Operator
  Details** pane (`details.go`) for the selected node — a one-row header
  ("Operator Details" + a right-aligned "Scroll ▲/▼" indicator, shown
  only when the content overflows) over a curated, label-aligned
  key/value list (`detailKVs`/`detailLines`: Physical/Logical Operator,
  Object, Output Columns, Predicate(s), Estimated/Actual Rows, Estimated
  I/O/CPU, Actual CPU/Duration, Memory Grant on the root operator only,
  Parallel, Warnings), mouse-wheel scrollable.
- **Bottom section:** key `o` cycles hidden →
  **Properties** (full ordered `Props` key/value list of the selected
  node, scrollable — the mockup's Properties pane) → **Summary**
  (`summary.go`: one row per operator — cost%, rows, time, operator,
  object, status — in a `controls.DataGrid`; `c`/`r`/`t` re-sort by
  cost/rows/time). Stacking all four mockup panes simultaneously doesn't
  fit 24–30-row terminals; cycling keeps them all reachable.
- **Bottom-section focus:** while Summary is shown, `Tab` toggles
  keyboard focus between the tree and the table (`bottomFocused` on
  `PlanView`) — Up/Down/PgUp/PgDn/Enter drive whichever one currently has
  it (Enter on the table jumps the tree's selection to the activated row
  and returns focus to the tree, k9s/lazygit-style); sort keys (c/r/t)
  work regardless of focus, since they don't collide with anything the
  tree itself binds. This wasn't called out explicitly in the original
  design — seed keys (Up/Down/Enter shared across two navigable regions)
  turned out to need an explicit focus flag; see the "Built as designed"
  note above.
- **Tree mechanics:** expand/collapse (`←`/`→`/`+`/`-`), full keyboard nav
  (↑↓/PgUp/PgDn/Home/End), mouse click/wheel, vertical + horizontal
  scrolling. Implemented in `tree.go` itself rather than reusing
  `controls.TreeView` — TreeView draws single-styled one-line labels
  inside its own "Object Explorer"-titled box; the plan tree needs
  per-span coloring (cost/warning/estimated), multi-line nodes, and
  connector glyphs (`├──►`). The projection/flat-list technique is
  borrowed from it; the widget itself isn't.

### 3.3 Tab 3 — XML

- A `controls.Editor`: `SetReadOnly(true)`, gutter/line numbers on, text =
  `showplan.Indent(plan.XML)` (indents only when the XML arrives as a
  single line — wire-fetched estimated plans do; SSMS-saved files are
  already indented). Exactly the Results-To-Text precedent.
- Full selection + copy: the editor's own selection handling;
  `PlanView.SelectedText()`/`HasSelection()`/etc. forward to it while the
  XML tab is active, for App-level Ctrl+C/Copy at binding time (see the
  public API note above — no `OnCopyRequest`). No highlighter; a small
  `xmlHighlighter` (tags/attributes/values, sibling of
  `sql_highlighter.go`) remains a nice-to-have, not implemented.

### 3.4 Colors and metrics display

- Derived inline from `theme.Active()` at each draw site (tree.go's
  `drawTreePane`, graph.go's `drawTile`) — no new Palette fields:
  expensive node (cost ≥ 30%) → `Error`; warnings → `Warning`; selected →
  `TreeSelected`/`BorderActive` (double border on the graph tab). No
  separate "healthy" or "parallel" color was added — nothing in the
  parsed model currently distinguishes a parallel operator from the DOP
  already shown in the statement header.
- **Estimated vs actual:** a tile's row-count line shows the actual count
  when `Runtime` is present, unless `p` (`showEstimated` on `PlanView`)
  asks for the estimate regardless — toggling always changes what's
  shown, even for a node that has both. The Tree tab's Operator Details
  pane (`details.go`) already shows *both* "Estimated Rows" and "Actual
  Rows" as separate lines when both exist, so `p` only affects the graph
  tiles, which only have room for one number.

### 3.5 Keyboard summary (control-level)

| Key | Action |
|-----|--------|
| `1` `2` `3` | Switch visualization tab |
| `[` / `]` | Previous / next statement |
| `↑↓←→`, `PgUp/PgDn`, `Home/End` | Navigate active view (see per-tab) |
| `Enter` | Graph: toggle Properties strip; Summary (focused): jump to operator; Tree: toggle expand |
| `o` | Tree tab: cycle bottom section (hidden/properties/summary) |
| `Tab` | Tree tab (Summary shown): toggle focus between the tree and the summary table |
| `Ctrl+↑`/`Ctrl+↓` | Graph (strip open): resize the canvas/Properties split (drag the bar with the mouse too) |
| `/`, `n`/`N` | Search operator/object name (Plan/Tree tabs); next/previous match |
| `w` | Jump to next node with a warning (Plan/Tree tabs) |
| `c` `r` `t` | Tree tab's Summary table: sort by cost / rows / time (works regardless of focus) |
| `p` | Toggle estimated/actual emphasis on graph tile row counts |
| `Ctrl+C` | Copy — handled at the App level via `clipboardTarget`, not by PlanView itself |

Deferred (not v1): `f` filter by operator type, `m` memory-grant
breakdown, `s` spills-only — plan-explorer luxuries; cheap to add later on
top of `Statement.Nodes()`.

Every key not consumed returns `false` from `HandleKey` — required for
host focus handling (see the keyboard-conventions rule).

## 4. Future binding (designed-for, out of scope now)

- **QueryPanel:** "Include Actual Execution Plan" runs with
  `SET STATISTICS XML ON`; each plan arrives as an extra one-row result
  set whose single column is the showplan XML. QueryPanel detects those,
  routes them into one PlanView, and grows an "Execution Plan" tab beside
  Results/Messages. Estimated plan (Ctrl+L equivalent) uses
  `SET SHOWPLAN_XML ON` without executing.
- **Standalone panel:** a thin `PlanPanel` in `internal/tui` (~60 lines):
  implements `layout.Panel` + `Activatable`, title "Execution Plan N",
  delegates everything to an embedded PlanView. `OnOpenInPanel` on an
  embedded view constructs one with the same `*showplan.Plan` (no
  re-parse) via `SetPlan`.
- Nothing in gosmo needs to change for the control itself; plan
  *generation* (later step) is plain T-SQL through the existing
  `internal/query` path.

## 5. Verification plan

- `internal/showplan`: parse the real UTF-16 fixture — statement count,
  operator tree shape (12 nodes, root Top→…), spot-check metrics
  (node 7 ActualRows=5, LogicalReads=2), cost-% math sums ≈ 100%,
  UTF-8/UTF-16 both accepted; `Indent` round-trips single-line XML.
- `graph_layout_test.go`: tiles non-overlapping, parent left of children,
  edges connect tile midpoints, canvas size correct — pure data, no
  screen.
- `planview_test.go` / `tree_test.go`: state-level tests driving
  `HandleKey` (tab switching, tree expand/collapse, selection shared
  across tabs, search, warning jump, `SelectedText` per tab), plus a
  `tcell.NewSimulationScreen` smoke-draw of each tab.
- **Manual/visual before binding exists:** a tiny dev harness
  `cmd/plandemo` (main.go, ~50 lines: read plan file from argv, run a
  minimal tcell loop hosting one PlanView) — drivable through the
  established tmux workflow, and handy for eyeballing each tab against
  the mockups. Kept checked in permanently as a dev tool (it opens any
  `.sqlplan` file directly); not added to the release workflow, which
  builds only `cmd/gossms`.
- `gofmt`, `go vet`, `go build`, `go test ./...` throughout.

## 6. Implementation phases

1. **`internal/showplan`** — model, parser (UTF-16 + RelOp walker),
   Indent, tests against the fixture. *(~600–800 lines)*
2. **PlanView shell** — tab bar, statement selector, XML tab, copy
   plumbing, error state, `cmd/plandemo`. *(~400 lines)*
3. **Tree tab** — tree.go, details.go, properties/summary bottom
   section. *(~600 lines)*
4. **Graph tab** — graph_layout.go + graph.go, detail strip. *(~600 lines)*
5. **Cross-cutting polish** — search, warning jump, est/actual toggle,
   colors, doc.go package docs. *(~200 lines)*

Each phase ends green (`go build`, `go vet`, `go test ./...`) and
demo-viewable via `cmd/plandemo`.
