[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Extended Events

**Management ▸ Extended Events**: the event sessions on the instance, their targets,
New Session and Session Properties, the live data viewer, and **XEvent Profiler** — one
command from nothing to a live trace.

On **Azure SQL Database** there are no server-scoped sessions: sessions there belong to a
database (`ON DATABASE`), so the folder hangs off each **database** node instead of
Management, exactly as SSMS hangs it. The same screens serve both, and a session's scope
is shown where you would otherwise wonder which of two same-named sessions you just
started.

![extended events profiler](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/15_xevent_profiler.png)

## XEvent Profiler

**Tools ▸ XEvent Profiler** — the menu's Standard and TSQL entries, **Alt+P** — or the
two leaves of the XEvent Profiler folder in Object Explorer (Enter works too).

One command from nothing to a live trace: **Launch Session** creates the session if the
server does not have it, starts it if it is stopped, and opens **Watch Live Data** on it.
Nothing to configure, nothing to save, no trace file, and reopening the app re-runs the
same command against the same session.

Two deliberate choices, both stated in the code and both visible to you:

- The sessions are named **`gossms_QuickSession…`**, not `QuickSession…`, and they carry
  **a target**. SSMS creates its copies with *no* target because it reads the live
  stream; goSSMS polls a target instead, so reusing SSMS's sessions would leave nothing
  to read on any server where SSMS's Profiler has run — and writing to them would change
  a session that is not ours. An existing `gossms_` copy is reused as it stands.
- The live data is **polled from the session's `event_file` (or `ring_buffer`) target**
  rather than streamed from the session, which is why **a target is not optional**.

## Watching live data

**Watch Live Data** on a session, or **View Target Data** on one of its targets, opens
the same viewer: a grid of events over a details pane with the selected event's fields.

| | |
|---|---|
| **Start Data Feed** / **Stop Data Feed** | The reader on the target — live views only. A stopped feed is a frozen grid that will not fill until you start it again |
| **Pause** / **Resume** | Freezes the **grid** while the reader keeps reading, so nothing is lost: **Resume** brings the view up to date. Pause means nothing while no feed runs |
| **Auto Scroll** | Follow the events, or hold the view where it is and keep collecting |
| **Clear Data** | Empty the grid without touching the session |
| **Filter:** box | Instant narrowing of what has arrived; **Edit Filter…** for the fielded form, **Clear Filter**, **Show All** |
| **Grouping ▾** / **Aggregation ▾** | Collapse the grid to one row per group, with a count and summed columns — what to look at when the interesting event is a hundred of one event |
| **Columns ▾** | Which columns are shown |
| **Settings ▾** | SSMS's Save/Open Display Settings: the hidden columns, filter, grouping and aggregation saved **under a name** and applied to any viewer later (kept in goSSMS's own settings rather than `.viewsetting` files). A saved filter that no longer parses, or a column this build cannot read, is left out **and said** rather than silently applied |
| **Find… ▾** / **Bookmarks ▾** | Search the rows and jump between marks |
| **Export ▾** | Write the events (or the view: filter, grouping and column choice) out to a file |
| **Refresh** (**F5**) | Re-read the target now |

The status line always says what the grid holds: `no events`, `reading`, `all shown`,
`Filter (on)`, `Grouping (on)`, `the data feed is stopped`. **A filtered, grouped view is
not the session's state**, and the panel keeps that visible rather than letting an
unlabeled grid be read as an idle server.

## Merge Extended Event Files

**Merge Extended Event Files** (on the Extended Events folder and the File menu) reads
**every `.xel` on the server** matching a wildcard pattern through
`fn_xe_file_target_read_file` and shows them in one viewer, in time order: SSMS's
*File ▸ Open ▸ Merge Extended Event Files* except that the files are the **server's**, not
the client's — a local `.xel` cannot be read, because the format has no public decoder.

The pattern starts on `*.xel`, the error-log directory, which is where `system_health`,
`AlwaysOn_health` and any session given a bare file name write their files.

## New Session / Session Properties

Four pages, and the whole session is written by **one `CREATE EVENT SESSION`** — every
event, target and option at once — which is why Apply is one statement.

- **General** — the name, a **Template** (or `(none)`, which starts the session empty; a
  template fills the other pages and everything it sets stays changeable), when the
  session starts (`STARTUP_STATE`), and **causality tracking**.
- **Events** — the event library on the left (Selected, Event, Package, Category, Channel,
  Description), the session's events on the right, and **Configure** per event for its
  actions, predicate and event fields. **The filter is the catalog's text, and that text
  is what is written**: the clause builder only appends, so what a filter grid cannot
  express — parentheses, a comparison operator not on its list — stays typed and is never
  silently lost.
- **Data Storage** — the session's targets and each target's parameters read from the
  catalog, with their defaults and the ones the target cannot do without. On an **Azure**
  engine edition an `event_file` filename cannot be a path (message 40538): it is a
  **blob URL**, written and read with the SAS of the credential named after the container
  — a server credential on a Managed Instance, a database-scoped one on Azure SQL
  Database. The page offers those credentials as the containers to write to, and refuses a
  filename that is not an `https://` URL before anything is sent.
- **Advanced** — retention (**Single event loss / Multiple event loss / No event loss**),
  dispatch latency, maximum event size, buffer memory and memory partitioning, and on SQL
  Server 2025 the session's **maximum duration**.

**Script Session as** and **Delete** come from the shared scripting and object-operations
tables like every other object, so a session definition is copyable and a drop can be
reviewed as a script.

## Details panes

The Sessions folder lists every session of its scope — with the Object Explorer folder
filter applied to the same set the tree uses, rather than to a second reading of the
catalog — and a session's own rows are its **events** and its **targets**; a target's rows
are its settings beside its **runtime counts**. That is the reading that answers "is this
session actually writing, and where did its events go".

## See also

- [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer) — the folder's nodes and filters
- [Scripting](https://github.com/radix29/gossms/wiki/Scripting) — Script Session as
- [Query Editor](https://github.com/radix29/gossms/wiki/Query-Editor) — the legacy **SQL Profiler** (Server Trace, not Extended Events) at `Ctrl+Alt+P`

[← Home](https://github.com/radix29/gossms/wiki)
