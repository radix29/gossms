# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.2.0 — 2026-11-10

### New

- Command line: `gossms <command>` runs goSSMS without the interface, built for
  scripts and AI agents (`gossms help`, `docs/cli.md`).
  - Built for AI agents: `agent guide` prints a manual to paste into a system
    prompt, tailored to what the profile's login can actually do; `agent schema`
    exports the full command catalog as JSON for tool definitions; `agent
    context` reports the server, login and any permissions the agent lacks.
  - Safe by design: every script is classified read, write, DDL or admin and
    refused above `--allow` or the profile's Allowed Operations; `--dry-run`
    prints the exact T-SQL; `--confirm` guards destructive calls.
  - Machine-friendly: json, jsonl, csv, tsv and markdown output, capped stdout,
    exit codes that name the failure, passwords never on the command line.
  - Covers queries, plans, objects, data, monitoring, security, Agent jobs and
    backup/restore.
- Audit log of command-line calls (`audit.jsonl`).
- Generate Scripts... for a database, in the TUI and as `script db --objects`.
- New Server Role dialog.
- SQL format style option; disabled objects dimmed in the tree.

### Fixes

- SQL Agent alerts and operators follow the caller's permissions.
- Application Roles work as principals; sequences accept decimal-sized ranges.
- Theme colors meet contrast limits.

### Changes

- `gosmo` v0.2.0.
- Theme palette now drives dialogs, grids and menus.

### Command line examples

```bash
gossms agent guide --brief -p dev          # manual for an agent's system prompt
gossms agent schema                        # command catalog as JSON
gossms query run -p dev -Q "SELECT TOP 5 name FROM sys.tables" -o json
gossms query run -p dev --file fix.sql --allow write --transaction rollback
gossms object describe -p dev Sales.Customer
gossms plan estimated -p dev --file slow.sql
gossms monitor blocking -p dev
gossms server config set -p dev maxdop 4 --allow admin --dry-run
gossms script db -p dev Sales --objects --include-permissions
```
