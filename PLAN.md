# goSSMS Plan

## Working style

This is a spare-time project — no deadlines, no sprints, no committed
velocity. Work happens in whatever order priorities and available time
allow; this document tracks *what's next*, not *when*.

Two companion documents carry the detail this one deliberately doesn't:

- `docs/open-threads.md` — work found, decided, or deferred but not
  finished, including the decisions that must not be re-raised.
- `CHANGELOG.md` / `RELEASE.md` — what actually shipped, per tag.

## Release target

**First usable version: 2026.**

"Usable" means the core SSMS workflows (connect, browse objects, run
queries, view/edit properties) are solid across all three supported
platforms and authentication modes, not that every SSMS feature is
covered — see [Feature backlog](#feature-backlog) for what can wait.

`v0.0.9` (2026-09-04) is the current tag — Query Store (SSMS's seven views, as
Object Explorer leaves and as a panel that charts them, with Force/Unforce
Plan, Track Query and Compare Plans), Compare Showplan, Detach and Attach
Database, six more server-level families in the tree (credentials, audits,
audit specifications, backup devices, server triggers, endpoints), multi-object
Delete from Object Explorer Details, a Script button on every delete
confirmation, and all five login kinds in New Login. Nothing is unreleased as
of this tag.

## Ongoing practices (no end date)

These continue for the life of the project, release or not:

- Bug fixing, optimizing, and refactoring as issues turn up.
- Triage incoming issues and re-prioritize the list below as they land —
  this file should stay roughly in priority order, top to bottom, but
  gets reshuffled as real-world bug reports come in.
- Keep `README.md`, `ARCHITECTURE.md`, and `internal/tuikit/README.md` in
  sync with the code as features land — stale docs are worse than no docs.
- Close items out of `docs/open-threads.md` rather than letting them
  accumulate.

## Next up (working priority order)

1. **Version support.** The floor is SQL Server 2016 SP1, and `README.md`
   now states it, but gosmo is not version-aware: the first runs against
   majors 13 and 14 found reads that select catalog columns which do not
   exist before 2019/2022, and `STRING_AGG` (a 2017 function) in seven
   queries. See `docs/open-threads.md` § Version support — this is the
   resume point, and nothing in it is fixed yet.

2. **SQL Agent** needs a complete rework. Job step add/remove/reorder, step
   fidelity, state-aware Start/Stop and a real editor for a step's command
   landed in `v0.0.8`–`v0.0.9`; the rework itself — proxies, categories,
   targets, and the New Job flow — has not.

3. **Database Reports** — the useful ones at server and database level:
   disk usage, top tables.

4. **Light / white theme** — selectable in Tools > Options, dark staying
   the default.

5. **Authentication testing** — no infrastructure currently available;
   blocked until access exists, not a code problem: Entra ID against
   Managed Instances and Azure SQL Database.

6. **Platform testing** — build and exercise on macOS (no Mac available
   yet; blocked on hardware/CI access, same as above).

Closed in `v0.0.9`: **Query Store**, **Compare Showplan**, **Detach/Attach
Database**, the **server-level families** (credentials, audits, audit
specifications, backup devices, server triggers, endpoints), **multi-object
Delete** from Object Explorer Details, and **all five login kinds** in New
Login. What each deliberately left out is in `docs/open-threads.md` —
§ Query Store, § Server-level families and § Delete/Rename — rather than here.

Closed in `v0.0.8`: the **least-privilege pass** (P0-P4), **scripting** every
object family the tree shows, **New Index / New Statistics**, and the SQL Agent
scope notes that were blocking step reordering.

Closed in `v0.0.7`: the **Database Restore** rework (server-side browsing,
the File Locations view, wrapped error messages, left-clipped paths) and
**Availability Groups** (viewing and managing AG topology and health).

## Feature backlog (later, no particular order)

- Keyboard shortcut consistency audit across terminals — several terminal
  emulators eat or mangle specific chords; see `internal/tuikit/README.md`'s
  notes and the Known Issues below. Needs a documented fallback/remap story
  rather than one-off fixes per report.
- `sp_blitz`-style diagnostics and an index reference, built in.
- Windows and Microsoft Entra authentication in Login Properties / New Login,
  and the External Provider login type generally — gosmo-side work needed
  first. The standing deferral in `docs/open-threads.md`.

## Known issues to close out before/around release

Carried from `README.md`'s Known Issues section — resolving these (or at
least confirming root cause) is part of getting to a genuinely usable v1,
not just documentation:

- Some terminals (e.g. xfce4-terminal) eat specific key shortcuts.
- Entra ID authentication untested (see Authentication testing above).
- macOS untested (see Platform testing above).
- Released binaries are built by GitHub and cosign-signed, but not
  platform-signed; checksums are published.

## Non-goals for v1

- **A row cap on query results.** Removed deliberately in v0.0.5: a result
  set is retained in full, so a large enough query can exhaust memory.
  SSMS parity was preferred to a silent cap. Do not add one back — see
  `docs/open-threads.md` § By design.
