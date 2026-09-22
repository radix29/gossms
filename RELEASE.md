# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.0.12 — 2026-09-22

### New

- **Service Broker** — message types, contracts, queues, services, routes,
  remote service bindings and broker priorities: listing, details, Properties,
  scripting and Delete.
- **Editable Queue and Route Properties**; queue message counts in the listing.
- **Redesigned Connect dialog** — History pane, Connection Properties /
  Connection String tabs, **Reset** and **Delete**.
- **Remember Password** checkbox — off by default for new connections.
- **Configurable indent size** (Tools > Options) and smart indentation.
- **Smarter IntelliSense** — columns from CTEs, derived tables, sub-SELECTs,
  `UNION` chains and `PIVOT`; temp tables and table variables; `#` and `@`
  open the list.
- **Linux desktop launcher and icons**, installed by Homebrew and APT.

### Fixes

- Move to Schema asked for the wrong permission; it now checks `CONTROL`.
- Deleting a database-scoped credential or audit specification made an extra
  lookup and could fail with "database not found".
- F5 on a loading Properties page left the previous load running.
- A reused Properties dialog could show the previous object's dependencies.
- A scripted sequence over an alias type lost the type's schema.
- Switching the editor's wrap mode kept stale selection and scroll state.

### Changes

- `gosmo` v0.0.13 → v0.0.14.
- SQL Server Agent Properties save in one statement instead of one per field.
- Connect dialog labels match SSMS; **Server Name** takes the port
  (`host,1500`).
- IntelliSense stays fast in long scripts.
- Superseded Properties, query and Query Store reads are cancelled.
