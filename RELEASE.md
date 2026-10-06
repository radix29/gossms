# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.0.14 — 2026-10-06

### New

- **Resource Governor** — pools, workload groups, live counters, Properties.
- **Send Test E-Mail** for Database Mail.
- Cross-database and linked-server IntelliSense.
- Extended Events grouping and aggregation.
- `PIVOT`, `OPENJSON`, `OPENROWSET`, `OPENXML` in the SQL parser.

### Fixes

- Agent schedules with the same name hit the wrong one.
- Name checks ignored the database collation.
- Failed Database Mail Apply left `Database Mail XPs` changed.
- Instances on one host shared credentials; ports in server addresses.
- Identifiers with `$`, `#`, `@`; combining marks, tabs and line breaks.
- Cancelled tasks reported as errors.

### Changes

- `gosmo` v0.0.16.
- Faster typing in large scripts (lexer cache).
