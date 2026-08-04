# goSSMS Plan

## Working style

This is a spare-time project — no deadlines, no sprints, no committed
velocity. Work happens in whatever order priorities and available time
allow; this document tracks *what's next*, not *when*.

Three companion documents carry the detail this one deliberately doesn't:

- `docs/open-threads.md` — work found, decided, or deferred but not
  finished, including the decisions that must not be re-raised.
- `docs/journal.md` — why a design is the way it is, and how a bug was
  found.
- `CHANGELOG.md` / `RELEASE.md` — what actually shipped, per tag.

## Release target

**First usable version: 2026.**

"Usable" means the core SSMS workflows (connect, browse objects, run
queries, view/edit properties) are solid across all three supported
platforms and authentication modes, not that every SSMS feature is
covered — see [Feature backlog](#feature-backlog) for what can wait.

`v0.0.5` (2026-08-04) is the current tag. The last few releases have been
feature-led; that one was mostly hardening — result-set memory, display
width, paste, panic containment, and saved-password integrity.

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

1. **Database Restore** dialog needs a complete rework — remote server
   directory browsing, moving files, error messages, text trimming.

2. **SQL Agent** needs a complete rework.

3. **Find / Replace** in the query editor — `Ctrl+F`, `Ctrl+H`, `F3`.

4. **Database Reports** — the useful ones at server and database level:
   disk usage, top tables.

5. **Activity Monitor** — currently a reachable stub with three entry
   points (see `docs/open-threads.md`). Tabbed: dashboard, history,
   `sp_whoisactive` (all/active), blocking, Azure/Managed Instance stats.
   History in memory only, refresh interval and window configurable.

6. **Availability Groups** — viewing and managing AG topology and health.

7. **Light / white theme** — selectable in Tools > Options, dark staying
   the default.

8. **Authentication testing** — no infrastructure currently available;
   blocked until access exists, not a code problem: Entra ID against
   Managed Instances and Azure SQL Database.

9. **Platform testing** — build and exercise on macOS (no Mac available
   yet; blocked on hardware/CI access, same as above).

## Feature backlog (later, no particular order)

- Keyboard shortcut consistency audit across terminals — several terminal
  emulators eat or mangle specific chords; see `internal/tuikit/README.md`'s
  notes and the Known Issues below. Needs a documented fallback/remap story
  rather than one-off fixes per report.
- `sp_blitz`-style diagnostics and an index reference, built in.
- Splitting `internal/tui` into sub-packages — it is 140+ files in one flat
  package while `tuikit` is cleanly layered. The `agent_*`,
  `database_props_*`, and `new_*` families are the natural seams. Judged
  not worth doing yet, and recorded in `docs/open-threads.md` as the one
  structural thing that keeps getting slowly worse.
- The deferred Properties scope listed in `docs/open-threads.md`: Windows
  and Entra authentication in Login Properties / New Login, `WITH GRANT
  OPTION`, effective permissions, filter boxes on the long securables
  grids, and column-level permissions.

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
