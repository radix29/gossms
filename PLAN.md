# goSSMS Plan

## Working style

This is a spare-time project — no deadlines, no sprints, no committed
velocity. Work happens in whatever order priorities and available time
allow; this document tracks *what's next*, not *when*.

Three companion documents carry the detail this one deliberately doesn't:

- `docs/open-threads.md` — work found, decided, or deferred but not
  finished, including the decisions that must not be re-raised.
- `docs/journal.md` — why a design is the way it is, and how a bug was
  found, for the work since the current tag. Trimmed at each release.
- `CHANGELOG.md` / `RELEASE.md` — what actually shipped, per tag.

## Release target

**First usable version: 2026.**

"Usable" means the core SSMS workflows (connect, browse objects, run
queries, view/edit properties) are solid across all three supported
platforms and authentication modes, not that every SSMS feature is
covered — see [Feature backlog](#feature-backlog) for what can wait.

`v0.0.8` (2026-08-25) is the current tag — permission awareness end to end
(goSSMS works as a login that is not `sysadmin`, and says which right is
missing rather than showing a zero or a driver error), `Script <object> as ▸`
on every family the tree shows, New Index and New Statistics, partition
functions and schemes, security policies and Always Encrypted keys in the
tree, a missing-index banner and `.sqlplan` files on execution plans, Log File
Viewer search and Recycle, and job-step reordering. Nothing is unreleased as
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

1. **SQL Agent** needs a complete rework. Job step add/remove/reorder,
   step fidelity and state-aware Start/Stop landed in `v0.0.8`; the rework
   itself — proxies, categories, targets, and the New Job flow — has not.

2. **Database Reports** — the useful ones at server and database level:
   disk usage, top tables.

3. **Light / white theme** — selectable in Tools > Options, dark staying
   the default.

4. **Authentication testing** — no infrastructure currently available;
   blocked until access exists, not a code problem: Entra ID against
   Managed Instances and Azure SQL Database.

5. **Platform testing** — build and exercise on macOS (no Mac available
   yet; blocked on hardware/CI access, same as above).

Closed in `v0.0.8`: the **least-privilege pass** (P0-P4, `docs/permissions-
plan.md`), **scripting** every object family the tree shows, **New Index /
New Statistics**, and the SQL Agent scope notes that were blocking step
reordering. What each deliberately left out is in `docs/open-threads.md`
§ Permission gating and § Deferred scope rather than here.

Closed in `v0.0.7`: the **Database Restore** rework (server-side browsing,
the File Locations view, wrapped error messages, left-clipped paths — see
`docs/open-threads.md` § Reworks named in README's Known Issues) and
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
