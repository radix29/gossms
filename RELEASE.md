# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.1.2 — 2026-11-03

### New

- Group by Schema in Object Explorer (Alt+G, title button).
- New Schema, New Database Role, New Application Role, New Sequence and
  New Synonym dialogs; application role and sequence properties.
- Double-click in Object Explorer Details navigates into the object.
- Double-click a result cell for Show Value; click a row number to select the row.
- Query Store: drill from a wait category into its queries.

### Fixes

- Disabled toolbar actions in monitor panels now say why.
- Background page actions in create dialogs are latched and recover from panics.

### Changes

- `gosmo` v0.1.2.
- Comment cleanup; `azcore` v1.24.0.
