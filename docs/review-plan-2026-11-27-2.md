# Review plan, 2026-11-27 (second pass)

A review of gossms, `~/go/gosmo` and `~/go/tuikit` at gossms `52ecafe`, gosmo
`b52e8a2`, tuikit `14b91e7`. Three bugs; B1 and B2 are closed. B3 is
gossms only. tuikit needs none.
Close an item by deleting it, and delete this file with the last one.

**Method.** `go test ./...` is green in all three repos. I read the last week
of commits (the Object Explorer active database, schema-qualified completion,
the Nov-26 refactor of User Mapping, Resource Governor and Database Mail
pages, tuikit's keyed DataGrid column map, TreeView mouse, LogFeed) and then
swept two rules from `docs/db-rules.md` across every repo: "compare names
under the scope's collation, never `strings.EqualFold`" (every `EqualFold` in
`internal/cli`, `internal/tui` and gosmo) and "a name the server holds may
contain anything" (every place a server file path is built from a name).

**What came out clean.** The refactors are faithful moves. tuikit's editor
search/replace, LogFeed row accounting, keyed column map, viewport and
scrollbar math, file-dialog path rules; gosmo's `SplitName`, `UnquoteName`,
`CommentSafe`, `NameKey`, `ObjectFilter`, search, sequence scripting, kill
batch and capability scan; gossms's `textenc`, `textutil`, the import record
reader, JSON/XML export, the argument parser, `query.Session`, Live Query
Statistics merge and the Activity Monitor counter decoding. No finding there.

Severity is ordered by what a user can lose.

## Recommended implementation order

1. **B3** — the backup/restore commands compare database names under the
   server collation (or by id), then the guard test that stops the rule
   being broken again.
2. **Docs** — add the new case-sensitive checks to `docs/open-threads.md` §
   Environment (the CS-server list), point `docs/db-rules.md`'s collation
   rule at the guard test, then delete this file.

Each step: `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Live steps use disposable objects only,
and only with your go-ahead. Nobody commits but the user.

---

## B3: gossms CLI: backup and restore compare database names case-blind

**Where:** `internal/cli/cmd/maint_backup.go` and `maint_restore.go`. Each
site breaks `docs/db-rules.md`'s rule (server-scoped names under the server
collation). B1 broke the same rule in another command family; the two are
listed apart because their consequences and tests differ.

| Line | What goes wrong on a case-sensitive server |
|---|---|
| `maint_backup.go:534` (`lastBackupRows`) | `maint backup --last sales` reports the row of whichever of `Sales`/`sales` `LastBackups` lists first: another database's last backup |
| `maint_backup.go:354` (backup read-back) | a file holding sets of `Sales` and `sales` reads back the other database's set as this backup |
| `maint_backup.go:497` (`backupHistoryRows`) | a user database named `TempDB` is left out of the all-database history |
| `maint_restore.go:163` (system-database refusal) | `maint restore MASTER` (a user database) is refused as master: the CLI twin of gosmo's `BackupRefusal` bug (review 2026-11-22 B4) |
| `maint_restore.go:375` (`restoreOverChecks`) | restoring over `Sales` while connected to `sales` is refused as "the connection's own database" |
| `maint_restore.go:453` (`pathCollisions`) | restoring over `sales` skips the collision check against `Sales`'s files |
| `maint_restore.go:475` (`verifyBackup`) | no "the set is a backup of …" warning when `Sales`'s set is verified for `sales` |
| `db_files.go:263` (`--filegroup`) | in a `_CS_` database `--filegroup fg` also lists `FG`'s files |

The first two give wrong data with no error. The others refuse wrongly or
skip a check the server repeats. Found by reading; nothing here has a case-
sensitive server to run on (`docs/open-threads.md` § Environment).

**Fix:**

- `lastBackupRows`: exact match first, then a unique
  `gosmo.SameName(serverCollation(srv), …)` match (the `matchName` shape).
- Backup read-back: `x.DatabaseName == opts.Database`. Both are catalog
  spelling.
- `backupHistoryRows`: skip tempdb by `d.ID == 2`, not by name.
- System-database refusal: move it after connect and compare under
  `serverCollation(srv)`. A database that already exists can be checked by
  id instead (1–4).
- `restoreOverChecks`: `current == target.Name`. Both come from the catalog.
- `pathCollisions`: pass the target's database id (0 for a new database) and
  skip `held` rows by `h.DatabaseID`.
- `verifyBackup`: `gosmo.SameName(serverCollation(srv), …)`.
- `--filegroup`: exact first, then a unique fold under
  `catalogCollation(d)`.

**Guard:** add `TestCLIComparesNamesUnderCollation` in `internal/cli/cmd`.
It parses each non-test file and fails on any `strings.EqualFold` call not
on an allowlist. Each allowlist entry is the enclosing function plus a
reason: "matchName convenience: exact first, unique fold", "configuration
name, case-insensitive by definition", "object list filter is
case-insensitive by design". This mirrors `TestApplyClosuresDoNotWritePageState`.
The rule has now been broken in this package three reviews running
(2026-11-23 B4, 2026-11-27 B3, this one).

**Tests:** a fake-driver `Latin1_General_CS_AS` server holding `Sales` and
`sales`, as `TestDBDropCaseSensitive` does. Run `--last sales`, `restore
MASTER` (not refused), `restore Sales` connected to `sales` (not refused),
and a collision against `Sales`'s file (refused). Plus a `_CS_` database
case for `--filegroup`.

**Verify:** `--filegroup` live on win10cli's `_CS_` contained database (add
a disposable filegroup `FG` beside `fg`, then remove both). The server-
collation rows join the CS-server list in `docs/open-threads.md`
§ Environment; they need an instance that does not exist here.
