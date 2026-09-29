# Release Notes

What changed in the current goSSMS release. Detail, and every earlier release,
is in [CHANGELOG.md](CHANGELOG.md). Questions and feedback:
[goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

## v0.0.13 — 2026-09-29

### New

- **Certificates, asymmetric and symmetric keys** — listing, details,
  scripting, Delete, Properties and New dialogs.
- Certificate backup, Remove Private Key, and module signatures
  (`ADD` / `DROP SIGNATURE`).
- **Database Master Key** — Properties, Back Up, Regenerate.
- **New User** dialog for every `CREATE USER` form.
- **Word wrap** in the query editor (Alt+Z).
- IntelliSense matches anywhere in a name.
- Unsaved queries are saved on a crash or a closed terminal.
- `GO n` repeats a batch.
- Results to Text column width in Tools > Options.
- `tcp:host,port` server addresses.

### Fixes

- `GO -- step 2` ran the batch twice.
- Script Table as CREATE dropped scales, CHECK constraints, `PERSISTED`,
  `ROWGUIDCOL` and index options.
- Scripted schemas and procedures failed with Msg 111; module scripts lost
  their `SET` options.
- Changing containment and setting advanced server options failed.
- Included Columns rebuilt the index with different options.
- Delete Table with its foreign keys could keep the table.
- Detach, rename, forced drop and Restore raced for single-user access.
- Non-ASCII file paths were mangled.
- Index storage counted rows ×3 on tables with LOB data.
- Renaming an enabled audit could leave it off.

### Changes

- `gosmo` v0.0.14 → v0.0.15; `go-mssqldb` v1.11.2.
- Large Results to Text sets format in the background.
- Server Properties applies one batch per page.
- A failed Apply keeps edits that never reached the server.
- Two running instances no longer overwrite each other's connections.
- Backup and Restore re-read the server's default directories.
- Name checks follow the server's collation.
