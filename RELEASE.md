# Release Notes

What changed in the current goSSMS release. The detail behind each line — and
every earlier release — is in [CHANGELOG.md](CHANGELOG.md).

## v0.0.9 — 2026-09-04

The Query Store release. Also Compare Showplan, Detach/Attach Database, six
more server-level families in the tree, and multi-object Delete. Updates
`gosmo` to v0.0.11.

### New

- **Query Store** — SSMS's seven views per database, as tree leaves and as a
  charted panel with selectable metric, statistic, window and row count.
- **Force / Unforce Plan, Show Plan, Track Query, Compare Plans** from that
  panel.
- **Compare Showplan** — two plans of one query paired over the operator tree.
- **Detach and Attach Database**, browsing the server's own filesystem.
- **Credentials, Audits, Server Audit Specifications, Backup Devices, Server
  Triggers and Endpoints** in Object Explorer, with Properties, Delete, Script
  and Enable/Disable.
- **New Credential, New Audit, New Server Audit Specification, New Backup
  Device.**
- **Delete several objects at once** from Object Explorer Details
  (`Ctrl+click`, `Shift+click` / `Alt+click`).
- **A Script button on every delete confirmation.**
- **New Login creates all five login kinds** — SQL, Windows, Entra,
  certificate- and asymmetric-key-mapped.
- **Column encryption key rotation.**
- **A job step's command is edited in the query editor** — multi-line,
  highlighted, with its own undo.
- **New Availability Group preflights every secondary** before writing.
- **Key Diagnostics logs mouse events.**
- **Supported floor stated: SQL Server 2016 SP1+.** Features an instance is
  too old for are withheld, not offered and refused.

### Fixes

- A query could return an empty grid with no error and no Messages tab.
- A named instance could not be reached by name — the Connect dialog's
  pre-filled port suppressed the SQL Browser lookup.
- Saving a mostly-LF script rewrote every line ending in it.
- Saving a file re-widened its permissions.
- Toolbar buttons that did not fit were deleted, not hidden — Export and
  Recycle, Pause, Compare Plans, Track Query. They collapse into **More ▾** now.
- Read-only Properties pages still drew editable-looking controls, and a
  read-only label ran into its value.
- `Ctrl+Z` in a job step's command box reverted the whole page.
- A Properties page showed stale values after an apply that failed having
  already changed the server, and stuck on "Loading..." after a panicking load.
- A grid went blank on an error when scrolled right, and scrolled past its own
  rows before being laid out.
- An Add or Remove on a Properties grid dropped a dragged column width — 17
  sites.
- A menu-driven drop, rename, offline or failover was abandoned mid-flight on
  the read timeout.
- The permission banner and the menus disagreed; a DENY on an object, column or
  schema is now honoured.
- `xp_cmdshell` could be ticked on Linux, where it does not exist.
- New Database Mirroring Endpoint could import an instance's own certificate.
- The connection-string preview disagreed with the connection it previewed.
- The server filesystem picker showed "0 B" and 0001-01-01 on pre-2017
  instances.

### Changes

- `gosmo` v0.0.10 → v0.0.11.
- The Connect dialog's Port field is blank by default; Connect is greyed until
  a server is entered.
- Object- and schema-scoped rights are asked with their securable, so a grant
  made directly on one object counts.
- Peer connection failures are cached briefly instead of redialled per folder.
- Highlighting and editing a large script got substantially faster.
- The server filesystem listing filters server-side — roughly 8x less waiting.
- Release binaries cover four targets, not six: `windows/amd64`,
  `linux/amd64`, `linux/arm64`, `darwin/arm64`.
- `docs/journal.md` and `docs/permissions-plan.md` removed; `open-threads.md`
  holds open work and settled decisions only.
