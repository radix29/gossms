[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Keys and Mouse

**F1 in the app is the keyboard reference**, and this page is its transcription — the copy
that survives a terminal scrollback, plus the keys a panel documents on its own toolbar. If
the two ever disagree, F1 is what is running.

A terminal is a claim on some keys before any application sees them: a window manager may
keep `Alt+1..9`, tmux keeps `Ctrl+b`, many terminals keep `Ctrl+Shift+C/V` for their own copy
and paste and `Shift+click` for their own text selection, and a ConPTY profile keeps
`Alt+Enter`. Where a binding does nothing, first check the terminal handed it over —
**Help ▸ Key Diagnostics** logs every key and mouse event with its modifiers and timing,
which is how "this binding is wrong" is told apart from "my terminal kept that modifier".

The app's keys are fixed rather than configurable; [Options and
terminal](https://github.com/radix29/gossms/wiki/Options-and-Terminal) covers the few
bindings that are.

## Global

| | |
|---|---|
| **F1** | This help |
| **F10** | Activate the menu bar — then `Left/Right` switch menus, `Up/Down` select, `Enter` activates, `Esc`/`F10` closes |
| **Ctrl+Q** | Quit |
| **F9** / **Ctrl+Shift+O** | Connect to server (`Ctrl+Shift+O` on terminals that encode it distinctly) |
| **Ctrl+O** | Open a `.sql` file as a new query, or a `.sqlplan` as a plan |
| **Ctrl+N** | New query panel |
| **Ctrl+W** | Close current query |
| **Ctrl+S** | Save query (a plan panel saves its `.sqlplan`) |
| **Ctrl+C / X / V** | Copy / cut / paste (editor and dialog fields) |
| **Ctrl+Space** | Open the context menu — the keyboard right-click |
| **Alt+P** | XEvent Profiler: start the Standard trace and watch it (Tools ▸ XEvent Profiler also has TSQL) |
| **Click the status bar** | Show the message history |

## Object Explorer

| | |
|---|---|
| Arrow keys | Navigate the tree |
| **Enter** / **+** | Expand node |
| **-** / **Backspace** | Collapse |
| **F5** | Refresh node |
| Right-click | Context menu — also **Shift+F10**, the **Menu** key, **Ctrl+Space** |
| **Ctrl+Left** / **Ctrl+Right** | Shrink / grow the explorer |
| **Ctrl+Space** on a node | The same menu a right-click would open at it |
| Drag a node into an editor | Inserts its qualified name (see [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer)) |

## Query editor

| | |
|---|---|
| **F5** | Execute (runs only the selection, if any) |
| **Shift+Arrow** | Select text with the keyboard; **click+drag** with the mouse |
| **Ctrl+Z** / **Ctrl+Y** | Undo / redo |
| **Ctrl+Up** / **Ctrl+Down** | Grow the editor and shrink results, and back |
| **Ctrl+PgUp** / **Ctrl+PgDn** | Previous / next result tab (grids and Messages) |
| **Enter** or double-click on an error in Messages | Go to its line |
| **Alt+Z** | Word wrap on/off (display only) |
| **Alt+Shift+F** / **Alt+F** | Format Document (SQL, JSON, XML): the selection's lines, or the whole text — one **Ctrl+Z** undoes it |
| **Ctrl+F** / **F3** / **Shift+F3** | Find, find next, find previous (Edit ▸ Replace… to replace) |
| **Ctrl+D** / **Ctrl+L** | Duplicate line / delete line |
| **Ctrl+Shift+U** / **Ctrl+U** | Uppercase / lowercase the selection |
| **Ctrl+/** | Comment / uncomment |
| **Ctrl+Space** | Clipboard menu at the cursor when no completion is installed; Cut/Copy/Paste from it |
| Query ▸ **Execute at Cursor** | **Ctrl+Enter** — run the statement under the caret |
| Query ▸ **Results To Grid / Text / File** | Where the next execution sends its rows |
| Query ▸ **Cancel Executing Query** | Stop the run, from the menu |

## IntelliSense

| | |
|---|---|
| *(as you type)* | Suggests schemas/tables/views/columns, including `sys.` — the list opens once a word **starts** (a letter or `[`); a space, a `.` or a digit never opens it |
| **Ctrl+Space** | Open or force suggestions: auto-completes a started word that has one match; with nothing typed, always shows the list |
| *(filtering)* | Matches anywhere in a name, names *starting* with it first; a list of only mid-name matches opens **unselected**, because the first row may not be the one you meant — `Down` picks |
| **Tab** / **Enter** | Accept the selected suggestion |
| **Up/Down**, **PgUp/PgDn** | Move the selection |
| **Esc** | Dismiss — and it will not reopen for that word until you move on |
| **Ctrl+R** | Refresh the cached table/column list for this database |
| *(Tools ▸ Options)* | There is an on/off toggle for the whole feature |

The cache is per connected session and per database — the same catalogue
`sys.dm_exec_describe_first_result_set` reads — and a name you accept is quoted only when it
needs to be, so a schema you did not type cannot turn into `.[order details]`.

## Results grid and Messages

| | |
|---|---|
| **Ctrl+PgUp** / **Ctrl+PgDn** | Next / previous **result tab** — a multi-statement batch gets one per result set, plus Messages |
| **Up/Down** | Move the row cursor |
| **Left** / **Right** | Scroll one column |
| **PgUp** / **PgDn**, **Home** / **End** | Page, first row, last row |
| **Enter** / **Space** | **Show Value** on the selected cell: the value on its own, at width |
| Wheel | Scroll down, and sideways with the horizontal wheel (**Shift+Wheel** as the alias) |
| Click / **Ctrl+click** / **Shift+click** | Select a row / mark one more row / extend the selection |
| Right-click | **Copy**, **Copy All**, **Copy All with Headers**, **Show Value**, plus the panel's own items under the divider |
| **Enter** / double-click on an error in Messages | Jump to that line in the editor |
| **Click the status bar** | The session's message history |

Right-click on the grid offers **Copy**, **Copy All** and **Copy All with Headers** beside
**Show Value**, and the panel hosting it appends its own items under a divider.

To write a result set out as a file, run the batch again with Query ▸ **Results To File** — it
streams **CSV**, one row at a time, to the path you choose before the run starts.

## Grids generally

Every grid in the app — results, Details pane, a report, a grid editor in a dialog — takes:

| | |
|---|---|
| **Up/Down** | One row; on a read-only grid with a cell cursor, **Shift+Arrow** extends a **block** selection from the cell |
| **PgUp** / **PgDn** | A page of rows |
| **Home** / **End** | First / last row |
| **Left** / **Right** | Move the cell cursor where there is one; otherwise pan one column |
| **Enter** / **Space** | Activate the selected cell (results and Messages) |
| Click | Select a row (or the cell, where the grid has a cell cursor) |
| **Ctrl+click** | Add or remove **one** row from a marked set — and any cursor move drops it, the file manager's rule |
| **Shift+click** or **Alt+click** | Extend the selection from the anchor — `Alt` because a VTE terminal keeps `Shift` for its own text selection whenever mouse reporting is on, so the app never sees that click |
| Right-click | The grid's menu, at the row under the cursor |

In a property page's grid the page's own keys apply instead: see Properties dialogs below.

## Execution plan

| | |
|---|---|
| **1 / 2 / 3** | Plan graph / operator tree / raw XML |
| **[** / **]** | Previous / next statement of the batch |
| **m** | Missing-index details, when the banner is showing |
| **/**, **n** / **N** | Search operators, next / previous match |
| **File ▸ Save Execution Plan As…** | Write the `.sqlplan` |
| Query menu ▸ **Live Query Statistics** | Execute shows the running statement's plan live — rows of estimate, elapsed, progress — and it becomes the actual plan at the end. Turns Actual Execution Plan on; needs `VIEW SERVER STATE` |

The graph and the tree are the **same selection seen twice**, and a right-click in either
offers **Compare Showplan…** against a `.sqlplan` file or **Compare with ▸** another open plan.

## Query Store and plan compare

| | |
|---|---|
| **Tab** | Walk the report grid → the plan grid → out of the panel (out means back to Object Explorer) |
| **F5** | Re-read the report with the current metric, statistic and window |
| **Ctrl+Up/Down** | Resize the panes — offered before the grid, which has its own `Ctrl+Arrow` use |
| Toolbar | Metric, statistic, top-N, filters, **Force/Unforce Plan**, **Show Plan**, **Script**, **Track Query**, **Compare Plans**, **Plot History** — mouse-only, and a toolbar row too narrow puts them under **More** |
| Grid keys | Arrows, `PgUp`/`PgDn`, `Home`/`End`, column panning, `Enter` for **Show Value** on a statement cell |
| **[** / **]**, **{** / **}** | In plan compare: plan A's and plan B's previous / next statement |
| **Enter** | In plan compare: open the operator's plan at it |

## Extended Events viewer

| | |
|---|---|
| **F5** | Start the data feed (live) / re-read the target |
| **F6** | Pause / resume the grid — **the feed keeps reading** |
| **F7** | Edit the filter: `column op value [AND|OR …]`, or plain text |
| **F8** | Choose columns (remembered per session) |
| **Ctrl+F**, then **F3** / **Shift+F3** | Find an event containing text; next / previous |
| **Ctrl+F2**, then **F2** / **Shift+F2** | Bookmark the event; next / previous bookmark |
| **Enter** / **Space** | Open or close a group row (**Grouping ▾**); **Right** opens, **Left** closes |
| **Alt+Up/Down** | Scroll the event details pane |
| Right-click | Open SQL in a query window, Show Plan, open XML, Filter by This Value, Group by This Column, Bookmark, Copy Event Details, Copy Rows with Headers; on a group row, **Filter by This Group** (its whole path of groups) |
| Toolbar | Grouping, Aggregation (on group rows, under its column), Settings (save / apply a layout), Export (CSV, tab-separated, `INSERT` script) |

## Replication Monitor

| | |
|---|---|
| **Tab** | Move focus: publications, subscriptions and agents → the agent's sessions → the session's actions |
| **F5** | Re-read everything now |
| **Ctrl+Up/Down** | Resize the panes |
| Toolbar | Auto refresh rate (or Off), History window, Failed sessions only |
| **Show Value** | On an action's **Error details** cell: the whole error |

## Activity Monitor

| | |
|---|---|
| **Tab** / **Shift+Tab** | Cycle the tabs (`Ctrl+Tab` belongs to the app, so plain `Tab` is free here) |
| **p** | Pause / resume the sampling feed (canvas tabs) |
| **+** / **-** | Refresh faster / slower (canvas tabs) |
| **r** | Refresh once (the Sessions and Block grids) |
| Arrows, **PgUp/PgDn**, **Home/End** | Scroll the canvas — a page of overlap on `PgUp`/`PgDn` |
| Grid keys | On Sessions and Block the grid answers first, including its right-click menu |
| Toolbar | Refresh, Pause/Resume, the wait-statistic category, auto-refresh rate — mouse-only |

## Log Viewer

**F5** refreshes what the panel is pointed at, **Tab** walks grid → filter box → out, and
**Ctrl+Up/Down** resizes the split. **Alt+Up/Down** scrolls the details pane — `PgUp`/`PgDn`
stay with the grid, where the cursor is while the pane is being read — and the selected row's
full text is below the grid. The log type, the file, **Search…**, **Recycle…** and
**Export…** are on the toolbar, which is the only way to them.

## Connect dialog

| | |
|---|---|
| **Tab** | Move focus: History → the visible tab's controls → buttons |
| **Up/Down** | Pick a saved connection in History (fills the form) |
| **Enter** | In History, connect to the highlighted connection |
| **Ctrl+PgUp/PgDn** | Connection Properties / Connection String tab |
| **Left/Right** | On the buttons, move along Delete, Reset, Connect, Cancel — Reset clears the form; Delete, set apart at the left, removes the highlighted saved connection after confirming |
| **Esc** | Close; while connecting, cancel the attempt |

## Back Up / Restore Database

| | |
|---|---|
| **Tab** | Move focus: fields → buttons |
| **Left/Right** | On the buttons; **Enter** presses one |
| **Enter** | In a field, Start Backup / Analyze |
| **Esc** | Close — on Restore's Backup Information and File Locations views, back to the form |

## Properties dialogs

| | |
|---|---|
| **Tab** | Move focus: page list → page → buttons |
| **F5** | Reload the current page from the server |
| **Ctrl+Z** | Revert this page to its loaded values |
| **Ctrl+C** | Copy the focused row's value |
| **Esc** | Close — while OK/Apply/Script Changes is running, **stop it** instead (as the Cancel button does) |
| Read-only page | `Tab` reaches only its grids: they scroll and select (the rows beside them follow) but edit nothing |

**Script Changes** sits beside OK and Apply on every page that writes, and writes a script
instead of running it — which is the only way a page that writes a hundred rows becomes
reviewable.

## Progress dialog

A confirmed Delete, Rename, Offline, failover…:

- **Esc** / **Enter** cancel the statement running, and the dialog stays up until the server
  has stopped it.
- Cancel is **greyed on a failover or a revert to a snapshot**, which cannot be safely
  interrupted — a dialog that offers a cancel it must not honour is worse than one that does
  not offer it.

## Focus and panel tabs

| | |
|---|---|
| **Ctrl+Tab** | Cycle focus: Explorer → Query Editor → Results |
| **Ctrl+Shift+Tab** | Cycle in reverse |
| **Ctrl+Shift+Right** / **Ctrl+Shift+Left** | Next / previous panel or tab (Query N, Object Explorer Details, …) |
| **Ctrl+0..9** | Jump to panel N, counted from the left (0 = Object Explorer Details) — while a panel has focus |

Some terminals reserve `Ctrl+Tab`/`Ctrl+Shift+Tab` for their own tab switching and never
forward them: `Ctrl+Shift+Left/Right` and `Ctrl+0..9` are the reliable fallback there. The
currently focused pane's title bar is highlighted, which is how you know where `Tab` will go
next.

## Mouse

| | |
|---|---|
| Click | Select / focus |
| Double-click | Open / expand |
| Right-click | Context menu |
| **Ctrl+click** | Add / remove one grid row from the selection |
| **Shift+click** | Extend the grid selection (**Alt+click** does the same on terminals that keep `Shift` for their own text selection) |
| Drag a splitter | Resize panels |
| Scroll wheel | Scroll trees, grids, results and details panes — including the horizontal wheel |
| Drag an Object Explorer node into an editor | Inserts its qualified name |

**Help ▸ Key Diagnostics logs mouse events too**, which is how a modifier the terminal kept
for itself is told from a wrong binding.

## See also

- [Options and terminal](https://github.com/radix29/gossms/wiki/Options-and-Terminal) — terminal capabilities and the settings that exist
- [Query Editor](https://github.com/radix29/gossms/wiki/Query-Editor)
- [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer)

[← Home](https://github.com/radix29/gossms/wiki)
