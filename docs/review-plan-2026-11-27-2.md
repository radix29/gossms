# Review plan, 2026-11-27 (second pass)

A review of gossms, `~/go/gosmo` and `~/go/tuikit` at gossms `52ecafe`, gosmo
`b52e8a2`, tuikit `14b91e7`. Three bugs. B1 and B3 are gossms only; B2 needs
one gosmo change (an exported helper, so no caller breaks). tuikit needs none.
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

1. **B2, gosmo half** — export `gosmo.FileNameSafe`, apply it to the snapshot
   name in `snapshotFilePath`. Build and test in `~/go/gosmo` first
   (`dev-with-local-gosmo` skill; the `replace` is active).
2. **B2, gossms half** — Back Up's default destination, Restore's tail-log
   device and standby file, and the CLI's `backupFileName` all use it.
3. **B3** — the backup/restore commands compare database names under the
   server collation (or by id), then the guard test that stops the rule
   being broken again.
4. **Docs** — add the new case-sensitive checks to `docs/open-threads.md` §
   Environment (the CS-server list), point `docs/db-rules.md`'s collation
   rule at the guard test, then delete this file.

Each step: `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...` (and the same in `~/go/gosmo` for step 1), then drive
the built binary (`docs/testing.md`). Live steps use disposable objects only,
and only with your go-ahead. Nobody commits but the user.

---

## B2: gosmo + gossms: server file names are built from raw database and snapshot names

**Where:**

- gosmo `database_snapshot.go:212` and `:214` (`snapshotFilePath`): the sparse
  file is `<stem>_<snapshot name>.ss`, the snapshot name unchanged. The
  logical name beside it already goes through `fileNameSafe`.
- gossms `internal/tui/backup_dialog.go:294` (`autoDest`): Back Up's default
  destination is `<db>_FULL_<stamp>.bak`.
- gossms `internal/tui/restore_dialog_plan.go:85` (`tailLogDevice`): the
  tail-log backup a restore plan takes first is `<db>_LogBackup_<stamp>.bak`.
  The form has no field for it; the user cannot correct it.
- gossms `internal/tui/restore_dialog.go:446` (`autoFillStandby`): the standby
  file default is `ROLLBACK_UNDO_<target>.BAK`.

**What:** a database or snapshot name is a sysname and may hold `\ / : * ? "
< > |`. gosmo documents what that does to a path in `RestoreRelocation.Moves`
(`restore_plan.go:131`): a separator makes a subdirectory and a colon an NTFS
stream. Restore relocation and the CLI's `maint backup` (`backupFileName`,
`maint_backup.go:204`) already replace those characters with `_`. These four
places do not.

The likely trigger is a time in a snapshot name. New Database Snapshot
offers `<source>_snapshot` and says SSMS adds a timestamp. A user who types
`Sales_snapshot_2026-11-27 10:30` gets the default sparse file
`…\Sales_Sales_snapshot_2026-11-27 10:30.ss`. On Windows, `10:30.ss` is then
a stream name, so the CREATE DATABASE … AS SNAPSHOT fails. With `/` in the
name (`2026/11/27`), the path names directories that do not exist. For a
database whose name holds one of these characters, Back Up's default
destination fails the same way, and so does every restore with "Take
tail-log backup" checked, before the restore starts.

The exact server message is not recorded yet; the verify step captures it.
Whether Windows instead accepts `name:stream` and writes the tail log into
an alternate data stream (hidden from `dir`) is open. If it does, it is worse
than an error, so the verify step checks that too.

**Fix:**

- gosmo: rename `fileNameSafe` to exported `FileNameSafe` and move it to
  `server_path.go`, beside `JoinServerPath`. It is the rule for "a name that
  becomes one path element on the server". Apply it to `snapshotName` in
  both `snapshotFilePath` branches. `RestoreRelocation.Moves` keeps calling
  it. CHANGELOG entry under Unreleased.
- gossms: `autoDest`, `tailLogDevice` and `autoFillStandby` wrap the name in
  `gosmo.FileNameSafe`. The "is it still the generated value" checks compare
  whole generated strings, so they keep working.
- gossms CLI: `backupFileName` calls `gosmo.FileNameSafe` and drops its own
  copy of the same map.

**Tests:**

- gosmo `snapshotFilePath`: `Sales/2026:1` gives `…_Sales_2026_1.ss`, and two
  names that differ only by an unsafe character (`a:b`, `a/b`) still get
  distinct paths through the `taken` fallback.
- gossms: `autoDest` with database `a:b/c`, `tailLogDevice` with `a\b`, and
  `autoFillStandby` with target `x?y` each give one path element under the
  backup directory (`gosmo.ServerPathDir` is the directory passed in).
- The CLI's existing `backupFileName` tests pass unchanged.

**Verify (live, win10cli, disposable objects, with your go-ahead):**

- Before the fix, capture the failure: on a disposable source database, run
  New Database Snapshot with name `gossms_b2 10:30` and Default File Paths.
  Record the server's message, and whether a file named `…gossms_b2 10` was
  left in the data folder (`dir /r` shows streams).
- After the fix, the same dialog creates the snapshot. Back Up of a
  disposable database created with an explicit FILENAME and named
  `gossms_b2:x` succeeds to the default destination. Restore with tail-log
  over it succeeds. Drop everything, and delete the files.

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
