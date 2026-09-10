# goSSMS status

A spare-time project — no deadlines, no sprints. This document tracks where
the project actually stands; it does not schedule anything.

Two companion documents carry the detail this one deliberately doesn't:

- `docs/open-threads.md` — work found, decided, or deferred but not
  finished, including the decisions that must not be re-raised.
- `CHANGELOG.md` / `RELEASE.md` — what actually shipped, per tag.

## Current state

`v0.0.10` (2026-09-09) is the current tag. Unreleased since it: the Resource
Governance database page, query-execution progress, user-defined type and XML
schema collection properties, the Homebrew first-release fix, and (uncommitted)
Log File Viewer and release-workflow work. `CHANGELOG.md` records them when
they ship.

The core SSMS workflows — connect, browse objects, run queries, view and edit
properties — work on Linux and Windows. goSSMS is installed from a package
manager on the platforms that have one: a Homebrew tap for macOS and Linux and
a GPG-signed APT repository for Debian and Ubuntu, both published by
`.github/workflows/release.yml` from the same archives the GitHub release
carries.

`v0.0.10` made Azure SQL Managed Instance a supported target rather than an
incidentally reachable one (engine-edition version gating, backup and restore
to Azure Storage, `internal/tui/edition_gate.go` for the operations the
edition refuses, and an Activity Monitor "Instance" tab), and added four
Object Explorer families at database and server scope (database DDL triggers,
database audit specifications, database-scoped credentials, cryptographic
providers), a merged Log File Viewer, and Query Store's Plot History.

`v0.0.9` added Query Store (SSMS's seven views, as Object Explorer leaves and
as a charting panel, with Force/Unforce Plan, Track Query and Compare Plans),
Compare Showplan, Detach and Attach Database, six server-level families in the
tree, multi-object Delete from Object Explorer Details, and all five login
kinds in New Login. `v0.0.8` closed the least-privilege pass (P0-P4),
scripting for every object family the tree shows, and New Index / New
Statistics. `v0.0.7` closed the Database Restore rework and Always On.

What each of those deliberately left out is in `docs/open-threads.md`, not
here.

## Version support

The floor is SQL Server 2016 SP1, stated in `README.md`. The nine
version-specific defects the first runs against majors 13 and 14 found are
closed; `TestLiveVersionSweep` reports 0 failures on all three instances
(call counts vary by what each instance has, so compare failures, not
totals). The gates holding them,
and the rule that a query change must be swept on the oldest instance
available, are in `docs/open-threads.md` § Version support.

Azure engine editions are gated on `EngineEdition`, not on the version they
report: a Managed Instance answers `12.0.2000.8` while running an 18.x engine,
which put it below every version gate. Run the sweep against the MI too when
changing a query it can reach.

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
- Backup and restore to Azure Storage were built and validated live, but never
  executed: the test Managed Instance has no shared access signature
  credential on a container. `WITH INIT` on a URL device and `RESTORE ... WITH
  MOVE` on MI are the first things to check when one exists — see
  `docs/open-threads.md`.
- macOS is untested — no Mac available, so the Homebrew formula has never been
  through `brew install` / `brew test` / `brew audit` on real hardware.
- The Homebrew and APT jobs have never run on a real tag. Both were verified
  by driving their extracted scripts against the `v0.0.9` assets; `dpkg -i` on
  a clean container, arm64 execution and `lintian` are still unrun. See
  `docs/open-threads.md`.
- Released binaries are built by GitHub and cosign-signed, but not
  platform-signed; checksums are published. The `.deb` packages are covered by
  the repository's GPG-signed `Release` file; the Homebrew formula by the
  release checksums it is rendered from.

## Non-goals

- **A row cap on query results.** Removed deliberately in v0.0.5: a result
  set is retained in full, so a large enough query can exhaust memory.
  SSMS parity was preferred to a silent cap. Do not add one back — see
  `docs/open-threads.md` § By design.
