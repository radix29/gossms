# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.2.2 — 2026-11-24

### New

- Hide and restore Object Explorer.
- IntelliSense: user-defined types, DDL positions, more built-ins and `OPENROWSET(BULK)` options.
- Picture viewer brightness and contrast.
- CSV/TSV formula neutralization.
- Stricter CLI `--max-rows-affected` and parameter checks.

### Fixes

- Plan cost badges and columnstore scans; no panic on plan-less statements.
- Safer CLI output: control characters, JSON integers, Markdown cells.
- Windows file, lock and clipboard issues.

### Changes

- `gosmo` v0.4.0, `tuikit` v0.1.0.
- Fabric DW detected by engine edition.
