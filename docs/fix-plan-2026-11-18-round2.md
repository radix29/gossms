# Fix plan — 2026-11-18, round 2

Known issues in gossms, gosmo, tuikit and gotuipic that can be fixed or
closed from this machine. Round 1 (F1–F5: mapped-user scripting, File > Open
of pictures, the Windows clipboard owner, Fabric detection in gosmo, the
prefetch `sys.filetables` gate) is done, committed (`96c0d85`, `2021314`;
gosmo `edc99e4`, `184480e`) and gone from the trackers. Delete this file when every
step is closed, and delete or trim each source entry as its step lands.

## How the list was built

- **Trackers read:** `docs/open-threads.md` and `~/go/gosmo/OPEN-THREADS.md`.
  tuikit and gotuipic have no tracker.
- **Code sweep:** non-test Go source in all four repos has no
  `TODO`/`FIXME`/`XXX`/`HACK`.
- **Checks, 2026-11-18 (after round 1 was committed):** `go vet`,
  `staticcheck` and `go test -short ./...` are clean in all four repos, and
  `deadcode -test` is clean in gossms.
- **Left out:** anything that needs an environment we don't have. That covers
  Fabric (edition 11 or 12), Azure SQL Database, Managed Instance (unavailable
  since 2026-10-08), Standard/Web/Express, 15/16, a multi-group host, EKM,
  Entra principals, PolyBase on-premises, a URL backup credential, a
  case-sensitive server collation, and the clipboard hang, which does not
  reproduce. It also leaves out work blocked upstream (go-mssqldb bulk-copy
  COLLATE), watch-only items (azidentity removing ROPC), and anything an entry
  or `docs/decisions.md` marks as by design. Examples: a disabled clustered
  index, the TDE protector named rather than scripted, `float16` split around a
  variable, and the DDL-trigger twins.

The result is **3 fixes and 2 closable verification gaps, not 20.** The
trackers hold nothing else that code or this estate can close.

## Fixes

### R1 — gosmo: the `IsFabricDW` breaking change has no release note — DONE

**Done 2026-11-18:** `~/go/gosmo/CHANGELOG.md` has a `v0.4.0 (unreleased)`
entry covering everything since `v0.3.0` (`2373b1b`..`184480e`), not only
round 1: three breaking changes with Migrating bullets (`IsFabricDW()`, the
typed `EngineEdition`, `ErrNotFound` from the optional singletons), plus
Added, Changed and Fixed (F1's mapped users, F5's `sys.filetables` gate).
`RELEASE.md` is untouched until the tag. Uncommitted in gosmo.

**Source:** round 1 F4 ("the release notes must say so"); gosmo `184480e`.

**Issue:** `Server.IsFabricDW` went from an exported field to a read-only
method, and `ConnectionOptions.FabricDW` is new. `~/go/gosmo/CHANGELOG.md`
tops out at `v0.3.0` and has no entry for either change. The next tag would
ship a breaking change with nothing to migrate from.

**Fix:** add the next version's entry with **Breaking** and a **Migrating**
bullet (`srv.IsFabricDW` → `srv.IsFabricDW()`). Under Added, cover
`ConnectionOptions.FabricDW` and the EngineEdition-based detection. Under
Fixed, cover the round 1 F1 mapped-user scripting fix and F5's `sys.filetables`
gate. Follow the existing entry style. **`CLAUDE.md` says not to edit
`CHANGELOG.md` unless asked, so this step runs only on the user's go-ahead.**

**Done when:** the user has reviewed the entry. No code changes.

### R2 — gosmo: `TestLiveStartTime` and `TestLiveReplicationMonitor` fail only in the full suite

**Source:** `docs/open-threads.md` § Environment (two gosmo live tests).

**Issue:** both tests failed inside the 20-minute `-short` run on win10cli
and passed when run alone. The failure text was lost. A test that is only
order-dependent hides real regressions, so it gets ignored.

**Steps:**

1. Reproduce with `-v` and no output filter. Run the full suite on
   win10cli 17, then on `SQL2016`, and keep the whole log.
2. **Leads for `TestLiveStartTime`:** it checks `start + uptime` against
   `SYSDATETIME()` within 10 s, and requires the difference to be at least
   0. Check where `StartTime` gets each value (`sqlserver_start_time` vs.
   tick counts). Under suite load, or after an earlier test changes the
   clock or time zone, those sources can drift apart. If it is drift, widen
   the window and allow a small negative difference, and write down why.
   Do not delete the check.
3. **Leads for `TestLiveReplicationMonitor`:** it depends on
   `liveReplicationFixture` state (`LastDistSync` non-zero,
   `SubscriptionCount == 1`, `MergeRunDuration != nil`). Look for an earlier
   test that drops or re-creates the distribution database, a publication or
   a subscription. Also look for a case where the merge agent hasn't run yet
   when the fixture is reused. Fix isolation in the fixture by making it
   reset or wait for its agents. Don't fix it by reordering tests.
4. Run the full suite twice, green both times, then trim the open-threads
   bullet.

### R3 — gosmo: live tests leave non-backup files on 13 and 14 — DONE

**Done 2026-11-18:** `liveRemoveFile` falls back to `ssh $GOSMO_LIVE_SSH_HOST
del /q "<file>"` for a non-backup file when the server has neither
`xp_delete_files` nor `xp_cmdshell`, gated on the host's first label matching
`@@SERVERNAME`'s machine part (new helper `liveRemoveFileSSH`). Live on 13
(port 50110) and 14 (55253): without the variable, the restore-close,
forced-drop and XE lifecycle tests log the unchanged "left behind" line; with
`GOSMO_LIVE_SSH_HOST=win10cli` they pass with no cleanup output, and `dir` on
win10cli shows no `gossms_test_xe_*` `.xel`, STANDBY `.tuf` or undo `.dat`
left (the XE template and deadlock tests swept the older leftovers). The
`~/go/gosmo/OPEN-THREADS.md` entry is trimmed to "closed when set", and
`~/go/gosmo/CLAUDE.md` test block names the variable. gosmo `gofmt`, `go vet` (with and
without `-tags livedb`), `staticcheck` and `go test -short ./...` are clean;
`deadcode` is not a gate in gosmo (library surface). Uncommitted in gosmo.

**Source:** `~/go/gosmo/OPEN-THREADS.md` § Live tests leave non-backup files
on 13 and 14.

**Issue:** `liveRemoveFile` (`live_remove_file_test.go`) can't delete STANDBY
undo files or `.xel` files on `SQL2016`/`SQL2017`. Those instances have no
`xp_delete_files` (2019+), and `xp_cmdshell` is off. The leftovers build up on
win10cli.

**Fix (test-only):** add a third fallback that the test runs from this
machine. It runs only when `GOSMO_LIVE_SSH_HOST` is set (for example
`win10cli`), and it uses `ssh <host> del /q "<path>"`. The SQL instance
reports a server-local path, so this holds only when the instance and the ssh
host are the same box. Gate it on the configured host matching the instance's
`@@SERVERNAME` machine part. Keep the current log line when no fallback
applies. Do **not** turn on `xp_cmdshell` (the entry's deliberate rule).

**Tests:** run a STANDBY restore test and an XE `event_file` test on 13 and
14 with the variable set. Then confirm on win10cli with `dir` that the files
are gone. Run once without the variable and confirm the log line is
unchanged. Then trim the entry to "closed when `GOSMO_LIVE_SSH_HOST` is
set", or delete it.

## Closable verification gaps

These are open-threads entries that this estate can now close. Nothing is
known to be broken, but each one stays open until it is run.

### R4 — gossms CLI: Windows never driven (phases 0 and 1)

**Source:** `docs/open-threads.md` § Command line: what is left.

The entry says win10cli has no interactive shell. It does now: ssh opens
`cmd`, and the Remmina RDP profile with `schtasks /it` reaches a real console
session (round 1 F3 used it). Build `GOOS=windows`, copy the binary over (see
`scripts/wintest.sh`'s sync), and run the phase 0/1 commands from
`docs/cli.md`. Run them in cmd and PowerShell over ssh, and in Git Bash if it
is installed. In the RDP session, check the output that depends on the
terminal: colour, the width of a non-tty pipe, and the password prompt.
Fix anything that fails, and narrow the entry to whatever is still not run.

### R5 — gossms CLI: `--application-intent readonly` routing unverified

**Source:** `docs/open-threads.md` § Command line: what is left.

AAG1 has no read-only routing list, so both intents land on the primary.
**This changes a shared cluster, so ask the user before doing it.** First
set `SECONDARY_ROLE (ALLOW_CONNECTIONS = READ_ONLY,
READ_ONLY_ROUTING_URL = …)` and `PRIMARY_ROLE (READ_ONLY_ROUTING_LIST = …)`
on ubusql1/ubusql2. Then connect through `ubuaag` with each intent and check
`@@SERVERNAME`. Then put the routing settings back exactly as they were;
record the before values first.
Delete the bullet, or write in it what routing did.

## Done means

For each code step (R2, R3): `gofmt`, `go vet`, `staticcheck`,
`deadcode -test` and `go test -short ./...` are clean in gosmo, and the live
runs above are green. gossms `go test ./...` stays green (the `replace`
directives stay active). For R4 and R5, the runs are recorded in the entry
they close. Then hand back for the user to commit.
