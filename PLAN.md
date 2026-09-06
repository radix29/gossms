# goSSMS status

A spare-time project — no deadlines, no sprints. This document tracks where
the project actually stands; it does not schedule anything.

Two companion documents carry the detail this one deliberately doesn't:

- `docs/open-threads.md` — work found, decided, or deferred but not
  finished, including the decisions that must not be re-raised.
- `CHANGELOG.md` / `RELEASE.md` — what actually shipped, per tag.

## Current state

`v0.0.9` (2026-09-04) is the current tag. Nothing is unreleased as of it.

The core SSMS workflows — connect, browse objects, run queries, view and edit
properties — work on Linux and Windows. `v0.0.9` added Query Store (SSMS's
seven views, as Object Explorer leaves and as a charting panel, with
Force/Unforce Plan, Track Query and Compare Plans), Compare Showplan, Detach
and Attach Database, six server-level families in the tree (credentials,
audits, audit specifications, backup devices, server triggers, endpoints),
multi-object Delete from Object Explorer Details, a Script button on every
delete confirmation, and all five login kinds in New Login.

`v0.0.8` closed the least-privilege pass (P0-P4), scripting for every object
family the tree shows, and New Index / New Statistics. `v0.0.7` closed the
Database Restore rework and Always On (viewing and managing AG topology and
health).

What each of those deliberately left out is in `docs/open-threads.md`, not
here.

## Version support

The floor is SQL Server 2016 SP1, stated in `README.md`. The nine
version-specific defects the first runs against majors 13 and 14 found are
closed, verified 2026-09-04 by `TestLiveVersionSweep` on all three instances
(219 calls / 0 failures on 13 and 14, 233 / 0 on 17). The gates holding them,
and the rule that a query change must be swept on the oldest instance
available, are in `docs/open-threads.md` § Version support.

## Ongoing practices

- Bug fixing, optimizing, and refactoring as issues turn up.
- Keep `README.md`, `ARCHITECTURE.md`, and `internal/tuikit/README.md` in
  sync with the code — stale docs are worse than no docs.
- Close items out of `docs/open-threads.md` rather than letting them
  accumulate.

## Known issues

- Some terminals (e.g. xfce4-terminal) eat specific key shortcuts. Several
  emulators mangle particular chords; `internal/tuikit/README.md` has the
  notes.
- Entra ID authentication is untested — no infrastructure available, against
  either Managed Instances or Azure SQL Database.
- macOS is untested — no Mac available.
- Released binaries are built by GitHub and cosign-signed, but not
  platform-signed; checksums are published.

## Non-goals

- **A row cap on query results.** Removed deliberately in v0.0.5: a result
  set is retained in full, so a large enough query can exhaust memory.
  SSMS parity was preferred to a silent cap. Do not add one back — see
  `docs/open-threads.md` § By design.
