# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.2.1 — 2026-11-17

### New

- Microsoft Fabric Data Warehouse: own Object Explorer tree, menus, statistics
  and property pages.
- Fabric T-SQL in the editor: `QUALIFY`, FROM-first queries, `WINDOW`, `MEDIAN`
  and `QUANTILE`.
- Right-click Cut/Copy/Paste menu on every text field.
- Picture viewer for binary result cells; result export to CSV, TSV, JSON, XML.
- Profile name on the Object Explorer root; database security nodes.

### Fixes

- Service Broker statements with `FROM` run whole on F5.
- Ctrl+C and paste work in plan, Query Store and Log Viewer panels.
- Line endings and text encodings survive open, save and format.
- Backup planning refuses invalid cases clearly; unknown encryption modes refused.
- Config tests no longer touch the real config on Windows and macOS.

### Changes

- `gosmo` v0.3.0, `tuikit` v0.0.3.
- Connection dialog **Reset** is now **New**.
