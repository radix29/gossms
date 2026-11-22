# Windows test findings

Run on win10cli (Windows 10 19045, Go 1.27.4 amd64) with `scripts/wintest.sh`.
Progress log, newest last. Started 2026-11-22 after user reports of failing
Windows tests and intermittent Windows build errors.

## Run 1 — 2026-11-22, `go test -count=1 ./...` (4m25s)

Cross-builds from Linux: `GOOS=windows` amd64 and arm64 build clean; `go vet`
clean. Remote run: 3 packages fail, the rest pass.

| Package | Failing tests | Class |
|---|---|---|
| `internal/fileutil` | `TestWithLockSerializesReadModifyWrite`, `TestWaitersOnAStaleLockTakeTurns` | product bug (A) |
| `internal/config` | `TestConcurrentSavesDoNotLoseEachOther`, `TestConcurrentTrackedSavesDoNotLoseEachOther` | product bug (A) |
| `internal/cli/audit` | `TestAppendRotationLosesNothing` | product bug (A) |
| `internal/config` | `TestSaveLoadRoundTrip` (dir 0777), `TestSaveIsAtomic` (file 0666), `TestOpenLogFileRotatesOnlyPastTheLimit` (0666) | test assumes POSIX modes (B) |
| `internal/config` | `TestLoadRefusesToSaveOverAnUnreadableConfig` | test assumes chmod 000 makes a file unreadable (B) |

**A. `WithLock` fails with `open …\*.lock: Access is denied` under contention.**
`createLockFile` maps only `fs.ErrExist` to "held". On Windows an
`O_CREATE|O_EXCL` create against a lock file that is delete-pending (just
removed by the releasing holder, or still open elsewhere) returns
`ERROR_ACCESS_DENIED`, so a waiter aborts instead of retrying. In the field
this is a Save that fails with "Access is denied" when two instances (or a
save and a background tracked-queries write) overlap.

**B. POSIX permission assertions.** Windows has no 0600/0700 mode bits; Go
reports 0777/0666. The tests must skip those assertions there.

### Fixes after run 1

- **A (product).** `fileutil.WithLock` treats `fs.ErrPermission` from the lock
  create as contention and retries until `lockWait`; a denial that outlasts it
  (an unwritable directory) is returned as itself, after 2 s.
- **C (product, found by run 2).** `config.json` save failed with
  `rename …tmp…: Access is denied` when another process had the target open
  (a second instance in `Load`, antivirus, an indexer): Windows refuses to
  replace a file held open without `FILE_SHARE_DELETE`. New
  `fileutil.Rename` retries a permission denial for up to 500 ms; used by
  `WriteAtomic`/`Stream.Commit`, config log rotation and audit rotation.
  `TestRenameReturnsAPersistentDenial` covers the give-up path (POSIX only).
- **B (tests).** Mode and chmod-000 assertions skip on Windows, as sibling
  tests already did.
- **D (tests).** `TestWithLockSerializesReadModifyWrite` raises `lockWait`;
  `TestAppendRotationLosesNothing` retries an `Append` that lost the lock.
  Cause: the lock is unfair and each hold costs two fsyncs, so a waiter can
  starve past 2 s. Not a product change — `lockWait` also bounds exit time
  (`internal/tui/app_saves.go`).

## Timing note

On win10cli (i5-2500K) an uncontended `WithLock`+`WriteAtomic` costs ~22 ms,
contended ~52 ms (`BenchmarkWithLock*`), versus well under 1 ms on Linux: it is
fsync, not a bug. The lock/config/audit tests therefore take 10-45 s there
and are the first to flake under full-suite load.

## Run history

| Run | Result |
|---|---|
| 1 | 3 packages failed (A, B) |
| 2 | after A, B: 2 lock tests + a rename denial (C) failed under full-suite load |
| 3 | after C: audit rotation starved the lock (D) |
| 4 | after D: `go test -count=1 ./...` all 28 packages pass; three consecutive config/fileutil/audit runs pass; `staticcheck`, `deadcode -test` clean; amd64 and arm64 cross-build clean; built `gossms version` runs |

## Not covered (so far)

- `-race` on Windows: no gcc on win10cli (`CGO_ENABLED=0`).
- Git Bash inside mintty, PowerShell 7 (`docs/open-threads.md` § Command line).
- Windows TUI driven interactively beyond the earlier RDP pass; clipboard
  manager hang (`docs/open-threads.md` § Windows clipboard).
- Other Windows versions (only 10 19045 here); Windows ARM64 only cross-built.
