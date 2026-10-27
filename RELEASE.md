# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.1.1 — 2026-10-27

### New

- Format Document (SQL, JSON, XML) and Execute at Cursor; more editor keys.
- Light color theme.
- Shrink Database and Shrink Files dialogs.
- Restore: standby and point-in-time options.
- Enable/disable DML triggers; rebuild, reorganize, disable indexes.
- Column sorting in Object Explorer details and query results.
- Max fetch rows option.
- Indexed views in index and statistics dialogs; database security script.
- Warning for unsupported SQL Server versions.

### Fixes

- Certificates are validated by default.
- Peer credentials are pinned to registered servers; Always On actions
  prompt to connect.
- Last grid column stays visible when scrolling.
- Cancel is gated during COMMIT; panics and stale connections cleaned up.
- Long values in read-only properties; Windows-safe file names.

### Changes

- `gosmo` v0.1.1.
- New logo; docs wiki split into pages.
