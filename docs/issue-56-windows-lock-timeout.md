# Issue #56 — Windows audit-lock timeout in `TestAppendParallelProcesses`

Investigation of 2026-11-22 (branch `review/bugfix-2026-11-22`, `HEAD`
8d156fb; the issue was filed against a408538 — `internal/fileutil/lock.go`
is the same in both). Nothing in the tree was changed; all probes ran on a
throw-away copy on win10cli and were removed afterwards.

## Outcome (2026-11-22)

Fixed as § A, C, D and E below, with three changes found by re-running the
recipe under load:

- **A lock seen absent is progress on every poll**, not only when it changes:
  a waiter that polls slowly under load saw it absent twice, with holders in
  between, and timed out.
- **The clock is read after the stat:** a stat stalled for 300–800 ms dated
  what it saw too early.
- **Release retries for up to `lockWait`, not once (§ C):** under load the
  antivirus held a fresh lock open past a 5 ms retry, and the leftover lock
  failed every waiter as stuck.

Result on win10cli under full-suite load: 80/80 runs of the three failing
tests plus the new moving-queue test passed, and so did the full suite
alongside them. What is left is in `docs/open-threads.md` § File lock.

## Summary

- **Reproduced.** The error from the issue shows up reliably on win10cli
  (Windows 10, Go 1.27.4 windows/amd64) once the machine is busy — here,
  while `go test ./...` runs alongside. On an idle machine it does not.
- **Cause: the waiters starve, the lock never sticks.** Every holder
  released its lock: no failed remove, no "access denied" from a
  delete-pending lock file. The waiters that failed spent almost all of
  their 2 s standing back from *another* waiter's priority marker
  (`.lock.wait`) and never won it. The marker holds one waiter at a time, and
  each time it frees up the waiters race for it again with no ageing. With
  five waiters and a busy machine (each hold-and-hand-off takes 100–300 ms),
  a waiter can lose often enough to pass `lockWait` while the queue keeps
  moving. The error message ("locked by another gossms instance — remove
  … if none is running") is therefore wrong in this case: the holder is
  alive and the queue is moving.
- **Not only this test.** The same full-suite run also failed
  `TestAppendRotationLosesNothing` (`internal/cli/audit`, 8 goroutines) and
  `TestWithLockSerializesReadModifyWrite` (`internal/fileutil`, 4
  goroutines) for the same reason. Real gossms users can hit it too: several
  CLI agents writing audit lines at once on a busy Windows host, or
  `config.Save` / `TrackedQueries.Save` racing with them.
- **The impact is a lost audit line, not just a red test.** `cli.go` calls
  `audit.Append` *after* the command has run. When the append fails, all the
  caller gets is a warning ("The audit log could not be written: …"). So when
  several CLI agents run write-class commands at once on a busy Windows host,
  some executed DML/DDL is never recorded. That cuts against the log's
  contract (`docs/cli.md` § Audit log).
- **The TempDir cleanup error is a side effect,** as the issue suspected: the
  test calls `t.Fatalf` on the first failed child and leaves the others
  running, still creating and removing `audit.jsonl.lock`.

## What was tested

### 1. Isolated run on Windows, idle machine

```
scripts/wintest.sh test ./internal/cli/audit -run '^TestAppendParallelProcesses$' -count=40 -v
```

40/40 PASS, 174.8 s, about **4.4 s per run** for 240 appends. That is about
18 ms per locked hold even with no other load (lock-file create/write/close,
token read-back, remove, log open/append 8 KB/close; Defender probably
accounts for a large share). Linux takes a fraction of that.

### 2. Latency probe, idle machine

A scratch test (`TestZZRepro56`, pushed only into `C:\src\gossms`) runs the
same six-child × 40-append workload. Each child times every `Append`. The
parent waits for **all** children and reports every failure (the real test
stops at the first).

3 runs, 0 failures. Per-`Append` latency per child: median 50–110 ms, p90
about 140 ms, **max 144–276 ms**. That is 7× or more inside `lockWait`
(2 s).

### 3. Latency probe under load (full suite running alongside)

`go test ./... -count=1` was started on win10cli; 20 s later the probe ran
`-count=8`.

| run | elapsed | failed children | slowest successful Append |
|---|---|---|---|
| 1 | 3.6 s | 0 | — |
| 2 | 3.7 s | 0 | — |
| 3 | 13.5 s | 3 | 1.93 s |
| 4 | 27.7 s | 3 | 3.13 s |
| 5 | 6.7 s | 3 | 2.09 s |
| 6 | 7.7 s | 0 | 1.29 s |
| 7 | 9.8 s | 3 | 2.21 s |
| 8 | 5.4 s | 0 | — |

**4 of 8 runs failed**, all with the issue's exact error
(`…\audit.jsonl is locked by another gossms instance (remove
…\audit.jsonl.lock if none is running)`), after 2.01–2.41 s. Some failed on
their very first `Append` (`i=0`). Under load, successful appends had median
35–450 ms and p90 0.3–1.8 s: the queue was moving the whole time while
individual waiters timed out.

### 4. Instrumented lock under load

Same as test 3, but the Windows copy of `lock.go` was patched to count, per
`WithLock` call:

- `blocked` — polls spent standing back from someone else's marker
- `exist` — create failed because the lock file exists
- `denied` — create failed with `ERROR_ACCESS_DENIED` (delete-pending)
- `claimTry` / `claimWon` — tries to take the marker, and wins
- `prioAt` — when this caller got the marker

It also logged any failed `os.Remove` / read-back in `releaseLockFile`.
Probe `-count=8` under full-suite load: 3 of 8 runs failed (2–4 children
each). Representative failing calls:

```
blocked=13 exist=2 denied=0 claimTry=2 claimWon=0 prioAt=0s removeFails=0
blocked=41 exist=5 denied=0 claimTry=5 claimWon=0 prioAt=0s removeFails=0
blocked=35 exist=4 denied=0 claimTry=3 claimWon=0 prioAt=0s removeFails=0
```

The full suite running alongside (also on the instrumented lock) failed:

```
TestAppendRotationLosesNothing        blocked=49 exist=6  denied=0 claimTry=4 claimWon=0 prioAt=0s
TestWithLockSerializesReadModifyWrite blocked=64 exist=24 denied=0 claimTry=3 claimWon=1 prioAt=1.749s
```

What this shows:

- `denied=0` everywhere. The delete-pending / `ERROR_ACCESS_DENIED` path
  played no part.
- `removeFails=0` in every failing call. No holder left its lock behind, so
  nobody waited on a stale lock (which would take `lockStale`, 10 s).
- The failing waiter spent nearly all of its polls `blocked` by another
  waiter's marker. It tried for the marker only 2–5 times in 2 s and won it
  0 times (once, at 1.75 s, too late). The single-slot marker picks a random
  next owner each round, and a loser has no better chance next round.

One stray `release remove: … The process cannot access the file because it
is being used by another process` appeared in
`TestAppendRotationLosesNothing`, **after** that test had already failed.
Its goroutines were still running while `TempDir`'s `RemoveAll` cleanup
walked the directory. It is consistent with that cleanup race, not with the
failures above. It does show one real gap, though: `releaseLockFile` ignores
a failed `os.Remove`. On Windows, any process holding a handle without
`FILE_SHARE_DELETE` (an AV scanner, an indexer) would leave the lock to
expire as stale, and every saver would fail for up to 10 s.

### 5. Hypotheses from the issue

| hypothesis | verdict |
|---|---|
| `WithLock` returns the error when its 2 s deadline expires | confirmed (it is the `blockedByPriority` or post-create deadline branch, not the `denied` one) |
| Windows delete-pending behaviour | ruled out for these failures (`denied=0`) |
| Contention | confirmed — specifically unfair hand-off among ≥2 waiters, made worse by slow holds under load |
| Cleanup error is secondary (children left running) | confirmed by reading the test; same pattern in `TestAppendRotationLosesNothing` (goroutines) |

## Proposed fix (not implemented)

`docs/decisions.md` records that the priority marker was chosen over a
backoff (made barging worse) and over a FIFO ticket queue (more crash cases
for the same result). The fix below keeps the marker and does not re-open
either choice.

This section is the second revision. The first draft is critiqued in
§ Review of the first draft below, which is what changed and why.

### A. Time out on "no progress", with a cap — recommended

`lockWait` exists to detect a holder that is stuck: the error text says so,
and callers keep pending changes for the next save. But it measures how long
*this caller* has waited, and with N waiters that grows with N even while
every hold is short. Change what it measures:

1. **Progress clock.** Keep `lastProgress` (initially `start`) and the last
   observed state of the lock file: absent, or present with a given
   `ModTime`. On every pass of the loop, including the `blockedByPriority`
   branch, which today stats only the marker, `os.Stat` the lock. If the
   state differs from the last pass (it appeared, disappeared, or has a new
   `ModTime`), set `lastProgress = now`. Every new holder creates a fresh
   file with a fresh `ModTime`, so a moving queue always reads as progress.
   A stat error other than not-exist (a delete-pending lock on Windows)
   counts as a change too: that lock is on its way out.
2. **Stuck-holder failure, unchanged.** Return today's "locked by another
   gossms instance (remove … if none is running)" only when
   `time.Since(lastProgress) > lockWait` (2 s). A crashed or wedged holder
   is reported exactly as fast as now.
3. **Busy cap.** New `lockMaxWait = 10 * time.Second` (equal to
   `lockStale`). Past it, return a distinct error: "… is busy: waited 10 s
   behind other gossms instances". The message no longer tells the user to
   delete a lock that a live process holds.
4. **The `denied` branch** keeps returning `denied` itself, but on the same
   two clocks: no progress for `lockWait`, or the cap.

**Stat only, never read the token, to detect progress.** I checked this in
the Go 1.27 source:

- `syscall.Open` on Windows opens with `FILE_SHARE_READ | FILE_SHARE_WRITE`
  and no `FILE_SHARE_DELETE` (`syscall_windows.go:395`).
- `os.Stat` uses `GetFileAttributesEx` and opens no handle
  (`os/stat_windows.go`).

The obvious way to detect a new holder is to compare the token, which means
`os.ReadFile(lock)`. That would let five waiters polling every 5 ms keep
handles open on the lock, and the holder's `os.Remove` would fail with a
sharing violation. The lock would then sit until it went stale (10 s), with
every saver failing in the meantime. Metadata only.

**If timestamps are coarse.** On a filesystem with coarse timestamps (FAT,
or some redirected `%AppData%` shares), two holders inside one tick can look
identical. Even then, the gap between holders is usually seen as "absent".
When it is missed, the waiter is simply back to today's behaviour (fail
after `lockWait`). It is never less safe, because mutual exclusion is still
the O_EXCL create alone.

**What the cap buys.** The failures come from the marker race having no
memory: the marker is not a queue. So with k waiters racing for each
hand-off, the chance one waiter loses r rounds in a row is about
((k−1)/k)^r. A hand-off takes about 0.1–0.3 s under load (measured above).
This crude model, with rounds assumed independent, matches what I observed
at today's 2 s:

| per-round time | contenders | P(fail) per run of 240 appends, 2 s | same, 10 s cap |
|---|---|---|---|
| 0.1 s | 4 | 53 % | ≈0 |
| 0.2 s | 4 | ≈100 % | ≈0 (6e-7 per append) |
| 0.3 s | 5 | ≈100 % | 13 % |

So A turns a failure that is near-certain under load into a rare one, but
not zero. The last row is a heavy-load, five-way-contention worst case,
well past real CLI use (and past the test, where a writer that just
released does not join the marker race for `lockPriorityAfter`, so k is
nearer 4). If that tail ever matters in practice, see § F.

**What it costs.** It is about 20 lines in `WithLock`, with no new files, no
new crash cases and no GOOS branch. Every caller of `WithLock` can now wait
up to 10 s instead of 2 s, but only while the queue is actually moving:

- TUI saves (`App.saveConfig` / `saveTracked`) already run off the UI
  goroutine. Nothing user-visible changes.
- The CLI audit append is synchronous. It waits longer instead of losing the
  line, which is the right trade given the impact noted in § Summary.
- `App.FlushSaves`'s doc comment (`internal/tui/app_saves.go`) states that
  the exit-time synchronous save overruns by "up to the file lock's wait
  (fileutil.lockWait)". It becomes `lockMaxWait`; update that comment in the
  same change.
- `WithLock`'s doc comment (the "If a live holder keeps the lock past
  lockWait" paragraph) and a `docs/decisions.md` entry change too: the
  meaning of `lockWait` changes from "my wait" to "the lock's idle time".

**Rejected alternative: just raise `lockWait` to around 12 s, above
`lockStale`.** That also covers the tail, and a crashed holder would even
heal itself (its lock goes stale and is broken). But a live holder that is
wedged, for example on a hung disk or a suspended process, would stall
every saver for 12 s with nothing to tell it apart from a busy queue. A
keeps the 2 s answer for "nobody is moving" and spends the long wait only
when someone is.

### B. Drop "make the marker prefer the oldest waiter"

The first draft proposed it as an optional follow-up. As written, it is
unsafe on Windows. Comparing ages means every claimant *reads* the marker's
contents, which opens handles that block deletion (see above). The marker
owner's `releaseLockFile(wait, …)` would then fail now and then, the marker
would stay for `2*lockWait` (4 s) before `blockedByPriority` dropped it as
abandoned, and every other caller would stand back for that whole time. The
same data could go into the marker's `ModTime` via `os.Chtimes` instead, but
that changes how staleness is judged and starts to rebuild the ticket queue
that `docs/decisions.md` declined. Leave it out unless § F shows a need.

### C. Make a failed release visible (unchanged in intent, narrowed)

Within gossms, only the holder ever opens the lock file (the `ReadFile` in
`releaseLockFile`). Waiters only create (`CREATE_NEW` fails without leaving
a handle) and stat. So a failed `os.Remove` there means an outside process
has a handle open, such as an antivirus scanner or an indexer. Log it to
`gossms.log` and retry once after `lockPoll`. Without that, the result is a
silent stall of up to `lockStale` for every saver. It was not seen to cause
#56: the only occurrence came after a test failure, while `TempDir` cleanup
was running, so its cause is unknown. This is defensive only.

### D. Test hygiene (independent of A)

- `TestAppendParallelProcesses`: `Wait()` every child, gather all errors,
  and only then fail. That removes the misleading `TempDir RemoveAll
  cleanup: unlinkat …audit.jsonl.lock` noise.
- `TestAppendRotationLosesNothing`: it reads `errs` only until the first
  error, then `t.Fatal`s while the other goroutines are still writing. Read
  all `writers` results before failing.

### E. Regression tests for A (`internal/fileutil`)

- **A moving queue succeeds.** Set `lockWait` to 100 ms and `lockMaxWait` to
  a few seconds, and run 6 goroutines that each take the lock 20 times and
  hold it for 30 ms. Each waiter sits behind about 5 × 30 ms > `lockWait`,
  so this fails on today's code every time on every OS, with no load
  needed. That makes it a deterministic stand-in for the Windows-under-load
  failure.
- **A stuck holder still fails fast.** The existing
  `TestWithLockGivesUpOnALiveHolder` must keep failing at about `lockWait`
  even with `lockMaxWait` large. Assert the elapsed time is below
  `lockMaxWait`, so a broken progress clock that counts the holder's
  unchanged lock as progress is caught.
- **The cap gives the busy error.** A helper creates the `.wait` marker
  itself, so the waiter under test always stands back. It then loops: create
  the lock with `createLockFile`, sleep 20 ms, release it with
  `releaseLockFile`, and repeat. Each new lock file counts as progress, so
  the waiter never hits the stuck-holder clock. With `lockWait` = 100 ms and
  `lockMaxWait` = 500 ms, the waiter must return the *busy* error after
  about 500 ms. Recreate the marker if the test runs longer than
  `2*lockWait`, or `blockedByPriority` drops it as abandoned.
- Per `docs/testing.md`, the real proof is on win10cli **under load**: the
  full suite in parallel with `-count=8` of the three failing tests. The
  isolated run passes even today, so it proves nothing.

### F. If the residual tail ever matters

Measure first: run the § Reproduction recipe with A in place, count
failures, and record each failing waiter's `claimTry`/`claimWon`. Only if
starvation past 10 s actually shows up should the decision against a ticket
queue be revisited. That is a decision for the user, recorded in
`docs/decisions.md`, not something to slip in here.

### Review of the first draft

| first-draft point | verdict | change |
|---|---|---|
| A: progress via `ModTime` of lock **and** marker | sound, but under-specified | progress from the lock only (every contended hand-off already changes it); blocked waiters must stat the lock too, since today they stat only the marker; "do not read the token" spelled out, with the Go source as evidence |
| A: cap = 10 s | kept | quantified the remaining tail; noted the doc comments and the `decisions.md` entry the change makes stale (`FlushSaves`, `WithLock`) |
| "callers are fine waiting 10 s" | checked | TUI saves run in the background; the CLI audit append is synchronous, and a failure there loses an audit line for a change already made, which raises the priority of the fix |
| B: oldest waiter replaces the marker | **withdrawn** | reading the marker's contents opens handles without `FILE_SHARE_DELETE`, which can stick the marker for 4 s; it also drifts back toward the declined ticket queue |
| C: log a failed release | kept, narrowed | gossms itself never opens another process's lock, so this guards only against outside processes |
| D: tests | split and sharpened | added a deterministic test that fails today on every OS (§ E), and a timing assertion so a broken progress clock cannot pass the stuck-holder test |
| — | added | rejected the alternative of just raising `lockWait`, with the reason

## Reproduction recipe

```sh
# terminal 1 — load
ssh win10cli 'cd /d C:\src\gossms && go test ./... -count=1 -timeout=10m'
# terminal 2 — ~20 s later
ssh win10cli 'cd /d C:\src\gossms && go test ./internal/cli/audit -run "^TestAppendParallelProcesses$" -count=8 -v'
```

(`scripts/wintest.sh test ./internal/cli/audit` first, to sync the tree.)
Expect one or more FAILs with the issue's message within 8 runs. On an idle
machine expect none.
