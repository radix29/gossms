# Review plan — 2026-10-26

The fix list from a sixth whole-repo review of gossms and gosmo, after the
2026-10-24 plan was implemented and retired (its A1, S1, B4, C3, D1 and H1
landed or carried; only H1 is carried here). Nothing settled in
`docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is
re-raised. The author's waiver applies again: **gosmo may break backward
compatibility where it helps** (Q2, Q5).

**Baseline (2026-10-26, gossms `HEAD` fde606c / gosmo d862687):** both repos
build; `go test -race ./...` passes in both; `go vet` passes, with `-tags
livedb` in both; `govulncheck` finds nothing reachable. Three things moved
since the 10-24 baseline: `staticcheck -checks all` now reports three doc
comment hits (S1), `ineffassign` one dead store (S1), and `unparam` a handful
of constant or unused parameters (S1).

How it was found:

- **Tools**: staticcheck, vet, deadcode, govulncheck, plus `unparam`,
  `exhaustive -d`, `ineffassign`, `errcheck`, `dupl -t 70`. The last four
  found nothing beyond S1: every `exhaustive` miss has a deliberate default, the
  `errcheck` hits are sticky-error writers and best-effort cleanups, and the
  largest `dupl` group is 30 lines.
- **Two throwaway AST audits**: receiver fields read inside a `safego`/
  `safegoRepair`/`fanOut` body outside its `postAndWake` callback (a race
  `-race` sees only on tested paths), and every SQL-shaped `fmt.Sprintf` in
  gosmo whose argument does not pass through a quoting helper. Both came back
  clean. The first found nothing that isn't immutable or posted. In the
  second, every bare argument is catalog-sourced, allowlisted or a pre-quoted
  fragment.
- **API diff**: the gosmo methods gossms never calls (119), read for
  user-facing operations SSMS has. That found G1–G3.
- **Doc-identifier check**: backticked Go identifiers in both repos' docs that
  no longer exist in code. That found D1.
- **Read in full**: gosmo's `database.go`, `proc_read.go`, `transaction.go`,
  `retry.go`, `connection_retarget.go`; gossms's `prop_apply.go`,
  `login_props.go`, `restore_dialog_ops.go`, `new_ag_dialog.go`'s pipeline,
  `internal/activity`'s rates and counters, `config_save.go`, `address.go`, and
  tuikit's editor undo, `Document`, `strutil.go` and `runecol.go`. Other
  dialogs were sampled, not read whole.
- **No live drive this pass**: it is plan-only. B1 and B2 are argued from code
  and SQL Server's documented behaviour. Confirm each live before fixing it.

This pass found **no data-loss or crash bug**. What it did find: one security
hazard in generated T-SQL, five gaps where gosmo has (or nearly has) a
capability gossms does not expose, three consistency slips, stale docs, and
lint drift.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
`TestNoDanglingDocReference` checks every `.md` path named here, so cite only
paths that exist.

Every gosmo change follows `~/go/gosmo/CLAUDE.md`:

- build and test in gosmo first;
- run `go vet -tags livedb ./...` after any rename;
- every new write gets an offline test of its exact statement in the matching
  `script_*_write_test.go`, mutation-checked;
- run `TestLiveVersionSweep` on the oldest instance (win10cli\SQL2016) after
  any query change.

IDs: **B** bug or hazard, **G** gap, **C** consistency, **S** simplification
or optimisation, **A** architecture, **D** docs, **Q** question for the
author, **H** release hygiene.

---

## Bugs and hazards

### B1 — gossms: Script Database Security plants a working, published password

`internal/tui/database_security_script.go:45` declares
`@password nvarchar(128) = N'ChangeMe123!'`. Every contained user and
application role the generated script recreates gets that password (`:93`,
`:115`).

`ChangeMe123!` passes Windows password complexity: upper, lower, digit and
symbol, 12 characters. Run the output without reading it, and every such
principal is created live, with a password printed in gossms's own source.
The "marked for review" comments do not stop that.

`docs/db-rules.md` § T-SQL and filters has the rule for this case: T-SQL gossms
writes itself carries gosmo's placeholder, by hand. gosmo's own scripter uses
`PasswordPlaceholder` (`<insert password here>`) for the same principals
(`scripter_security.go`). That string has no upper case and no digit, so a
replay is expected to fail the policy check rather than create a principal.

**Fix:** declare `@password` from `gosmo.PasswordPlaceholder` (the constant,
with its single quotes escaped). Reword the header comment.

**Test:**

- A unit test that the generator text carries `gosmo.PasswordPlaceholder` and
  not `ChangeMe`. Mutation-check it.
- Live on 17: in a throwaway contained database, create one contained user
  and one application role, run the generator, and replay its output into a
  second throwaway contained database.
  - The replay should refuse both principals (expected Msg 15118, password
    validation failed).
  - If it does not, choose a placeholder that certainly fails policy, and
    change gosmo's constant too, so both stay one text.
  - Drop both databases afterwards.

### B2 — gossms: Login Properties ▸ Status can count 0 active sessions for a Windows login

`internal/tui/login_props.go:693` counts sessions with
`s.LoginName == *loginName`. A Windows session can report its login name in
a different case from the one `sys.server_principals` holds.
`connectedLoginIndex` (`internal/tui/new_job_pages.go:59`) documents that
and fixed it for B14. Such a login then shows "Active sessions 0" while
connected.

**Fix:** compare with `gosmo.SameName(serverCollation(sc), …)`, as
`connectedLoginIndex` does.

**Optional:** count server-side instead of listing every session. That needs
a gosmo filter, which `ActiveSessions` does not take; skip it unless asked.

**Test:** a fake session row whose `LoginName` differs only in case. Live
confirmation needs a Windows login, which none of the instances here offers;
say so in the commit.

---

## Gaps — gosmo has it (or nearly), gossms does not

### G1 — DML triggers: no Enable/Disable, no disabled marker (gosmo and gossms)

`DatabaseTrigger` and `ServerTrigger` both have `Enable`, `Disable` and
`SetEnabled` (`database_trigger.go:143`, `server_trigger.go:138`). The DML
`Trigger` (`trigger.go:12`) has only `Rename`, `Drop`, `Definition` and the
signature pair. It scans `IsEnabled`, but nothing writes it.

In gossms, a table's or view's trigger node is built from the name alone
(`internal/tui/explorer_objects.go:487`). Server triggers get a
`(Disabled)` label (`internal/tui/explorer_management.go:130`) and an
Enable/Disable toggle. A DML trigger gets neither. SSMS offers
Enable/Disable on both.

**Fix, in gosmo:**

1. Add `Trigger.Enable`, `Trigger.Disable` and `Trigger.SetEnabled(ctx, on)`.
   Each sends `ENABLE|DISABLE TRIGGER [s].[t] ON [s].[parent]` through
   `setIfApplied`.
2. Handle a `TriggerRef` handle, which has no `TableName`. Resolve the parent
   with one by-name lookup and don't cache it, the `Job.id` pattern in
   `~/go/gosmo/CLAUDE.md` § Conventions. Otherwise refuse with
   `ErrHandleNotLoaded` and add a row to `handle_not_loaded_test.go`.
3. Add the offline statement case and a live round trip on 13 and 17.

**Fix, in gossms:**

- Label the node with `disabledLabel`.
- Set `node.data.IsEnabled`.
- Add a toggle beside `toggleDatabaseTrigger`
  (`internal/tui/app_explorer_data.go:296`), over the shared
  `toggleEnabledState`.
- Gate it on ALTER on the parent table or view (`gate.AlterOnObject`, scoped
  to the parent).

Drive it in tmux, and check both the label and the toggle after a Refresh.

### G2 — Activity Monitor: no Kill Process

SSMS's Processes pane offers Kill Process. The Sessions and Block grids here
already have a cell menu (`internal/tui/activity_monitor_proctab.go:102`,
"Show Live Execution Plan"). `selectedSession` (`:117`) already resolves the
row's session id. gosmo has `Server.KillSession` (`server_config.go:551`),
which gossms never calls.

**Fix:**

- Add "Kill Process…" beside Show Live Execution Plan, behind a confirmation
  that names the session, login, host and database.
- Run it through `runWithProgress`, then refresh the tab.
- Gate it on a new `gate.AlterAnyConnection` right (a server-scope
  `HAS_PERMS_BY_NAME` arm). `database_props_options.go:112` already names
  that right in prose, but nothing gates on it.
- Refuse the panel's own session in the dialog, before the server's Msg 6104.

In gosmo, refuse `sessionID <= 0` with `invalidf` and add an
`invalid_request_test.go` row.

**Test:** fake-driver tests for the gate and the own-session refusal. Then
live on 17: open a second `sqlcmd` session, kill it from the panel, and
confirm it is gone.

### G3 — Index node: no Rebuild, Reorganize or Disable

`indexMenuItems` (`internal/tui/explorer_objects.go:558`) is Properties only.
Rebuild and Reorganize exist as buttons on Index Properties ▸ Fragmentation
(`index_props.go:550`). `Index.Disable`/`Enable` are in gosmo, but no gossms
path reaches them. SSMS puts Rebuild, Reorganize and Disable on the index
node, and Reorganize All and Disable All on the Indexes folder.

**Fix:**

- Add the three node items, each confirmed and run through
  `runWithProgress`.
- Disable on a **clustered** index takes the table offline. Say so in the
  confirmation (`~/go/gosmo/OPEN-THREADS.md` § Scripter fidelity notes the
  same effect).
- Rebuild re-enables a disabled index. Label the item "Rebuild (enables it)"
  when `IsDisabled`.
- Gate the items like the existing Rebuild All item, which opens a script
  rather than running it.

### G4 — Restore: no STANDBY, no point-in-time (gosmo already has both)

`gosmo.RestoreOptions` has `RestoreWithStandBy` with `StandByFile`, and
`StopAt` (`restore.go:39-85`, built at `:233` and `:250`). The dialog offers a
two-way radio, `WITH RECOVERY` / `WITH NORECOVERY`
(`internal/tui/restore_dialog.go:209`), and no time field.

The SSMS features beyond those are not built anywhere:

- a restore *plan* (full, then differential, then logs, read from history);
- a tail-log backup first.

**Fix (G4a, small):**

1. Make the recovery radio three-way, with a standby-file path field shown
   only for STANDBY.
2. Add an optional "Restore to point in time" field for a log set. Use the
   server's local wall clock, as `StopAt`'s doc requires.
3. Send both through `restoreRequest`/`restoreOptionsFor`, and through
   `script()`.

**G4b** (the plan and the tail-log backup) is Q1.

### G5 — gosmo `BackupOptions` lacks encryption, tail-log and expiry

`BackupOptions` has no `ENCRYPTION (ALGORITHM, SERVER CERTIFICATE |
ASYMMETRIC KEY)`, no `NO_TRUNCATE`/`NORECOVERY` (the tail-log form G4b
needs), and no `EXPIREDATE`/`RETAINDAYS`.

Each is a library feature with an offline statement test. Encryption also
needs a live run on 17 with a throwaway certificate. Whether to build any of
them is Q5.

---

## Consistency

### C1 — gossms: msdb-held names compared under the server's collation

`name_set.go` says names compare under the collation of the scope they live
in, and gives msdb's for Agent objects. Jobs, schedules, operators and alerts
follow that (`msdbCollation`). Four sets of msdb-held names do not, and use
`serverCollation` instead:

- job steps: `internal/tui/agent_job_props_steps.go:249` and
  `internal/tui/new_job_pages.go:77`;
- Database Mail profiles: `internal/tui/database_mail_props_profiles.go:94`;
- Database Mail accounts: `internal/tui/database_mail_props_accounts.go:217`.

msdb normally shares the server's collation, so this is latent. A restored or
rebuilt msdb can differ, and then the duplicate checks disagree with the
server.

**Fix:**

- In the two Database Mail page loads and the Job Properties steps page,
  call `msdbCollation(ctx, sc)`; each load already has `ctx`.
- New Job already reads it in its prefetch (`new_job_dialog.go:33`). Keep
  that value on `njobPrefetch` and pass it to `buildNewJobStepsPage`.

**Test:** a fake with a `_CS_` msdb under a `_CI_` server, and two step names
that differ only in case, both accepted.

### C2 — gossms: a replication subscription is matched by `EqualFold`

`internal/tui/replication_props.go:46` picks the subscription whose
publisher, publisher database and publication equal the node's, with
`strings.EqualFold`. gosmo's rule for picking one of several catalog rows by
name is exact first, then a single case-insensitive match, and
`ErrAmbiguous` for several (`~/go/gosmo/CLAUDE.md`, the `matchName` bullet).
On a `_CS_` server, two publications differing only in case open the wrong
one.

**Fix:** exact match first, then fall back to `EqualFold` only when exactly
one row matches.

Low priority: replication is read-only here.

---

## Simplification and optimisation

### S1 — lint drift since the 10-24 baseline

Fix each by hand; none changes behaviour.

- **staticcheck:**
  - ST1020 on the `HandleKey` comments at
    `internal/tui/replication_monitor_panel_input.go:12` and
    `internal/tuikit/propsheet/sheet_input.go:23`;
  - ST1021 on `DatabaseTrigger`'s comment, gosmo `database_trigger.go:11`.
- **ineffassign:** `status := "Disabled"` at
  `internal/tui/database_mail_props.go:247` is overwritten on every branch.
  Declare it bare.
- **unparam, gossms:**
  - `masterKeyOpenPassword` (`master_key_dialogs.go:49`) and
    `symKeyEncEdit.encryptor` (`symmetric_key_props.go:133`) never use `ctx`.
    The latter's comment also claims a placeholder substitution it does not
    make, since gosmo redacts. Drop `ctx` from both and fix the comment.
  - Both `runTool`s (`log_viewer_toolbar.go:125`, `xevent_viewer_toolbar.go:188`)
    return a `bool` nobody reads.
  - `buildPermissionsMatrix`'s two height parameters are always 8 and 12.
  - The two `SendTestMailDialog.build` closures always return a nil error.
  - `xevent.cutLowerPrefix`'s first result is unused.
- **unparam, gosmo (unexported only):**
  - `ObjectFilter.clause`'s `nextArg` is always 1;
  - `qsReportSpec.value`'s `rs` is always `"rs"`;
  - `withExtendedPropertiesAt`'s `minorID` is always 0;
  - `clauseList.render`'s `indent` is always four spaces;
  - `Table.exec`'s `sql.Result` is never used.

### S2 — gosmo: msdb procedure calls splice escaped literals (optional)

There are 22 sites like `EXEC msdb.dbo.sp_add_category @class = N'%s', …`
with `escapeSingle`. `Server.exec` takes bound arguments, and
`bindScriptArgs` renders them into captured scripts. `unparam` reports that
no caller has ever passed any. `exec`'s doc comment says such statements
"are not parameterizable"; that is true of DDL, not of a procedure's
arguments.

Binding them would remove 22 hand-escapes and let plans be reused. The risk
is churn in the exact-statement tests. Whether to do it is Q6.

---

## Architecture

### A1 — one gesture router for the panels (optional)

`ARCHITECTURE.md:601` says "Three routers, one shape": `App.gestureOwner`,
`QueryPanel.dragZone` and `PropertySheet.dragZone`. Since then, six more
panels have hand-rolled the same idiom. Each has a zone enum, `armDrag`,
`routeDrag` and a release reset:

- `LogViewer`;
- `XEventViewer`;
- `QueryStorePanel`;
- `ReplicationMonitorPanel`;
- `PlanComparePanel`;
- `ActivityMonitor` (`routeDrag` only).

`DetailBrowser` and the Always On dashboard carry zone enums too.

Each copy is about 25 lines, plus the same release-forwarding block, and each
has its own tests. The invariant is the one ARCHITECTURE.md calls easy to
get wrong.

**Proposal:** a generic `Gesture[Z comparable]` in tuikit `layout` or `core`.
It would hold the claimed zone, `Arm(ev, z)`, `Owner()` and `Release()`.
Each panel keeps only its `switch` over zones.

Do it only with a tmux drive of every panel's splitter, toolbar and grid
drag. Mouse regressions are what this idiom exists to prevent. If it is not
done, fix the "three routers" sentence anyway (D1). Q3.

### A2 — move Script Database Security's generator into gosmo (optional)

`database_security_script.go` is about 200 lines of T-SQL in a Go constant.
It is the only large T-SQL text outside gosmo, it is pinned by string tests,
and it is never replayed live. Its principal, membership and permission logic
parallels gosmo's `ScriptUser`, `ScriptDatabaseRole` and the permission
readers.

A `Scripter.ScriptDatabaseSecurity(ctx)` returning the statements would:

- share that code;
- inherit the placeholder rule (B1) and the comment-safety rule for free;
- get a live round-trip test beside `live_user_kinds_test.go`.

The cost is the "a query the user runs" design: today it works with no
connection-side reads beyond `USE`. Q4. B1 is needed either way.

---

## Docs

### D1 — stale identifiers in the docs

Each of these names something that no longer exists:

| Where | Says | Is now |
|---|---|---|
| `ARCHITECTURE.md:601` | "Three routers" | nine, see A1 |
| `ARCHITECTURE.md:708` | `tickExecuting` | the elapsed-time redraw is `App.animateUntil`'s; name the current function |
| `docs/ui-rules.md:219` | `gosmo.QueryStoreQueryText` | `d.QueryStore().QueryText` (the facet) |
| `docs/db-rules.md:34-56`, `:83` | `rightAlterDatabase`, `dbScopedCredentialRights`, `queueAlterRights`, `queueDropRights`, `classOneTransferRights`, `rightAlterOnObject`, `rightControlOnObject` | `gate.AlterDatabase`, `gate.DBScopedCredentialRights`, `gate.QueueAlterRights`, `gate.QueueDropRights`, `gate.ClassOneTransferRights`, `gate.AlterOnObject`, `gate.ControlOnObject` |
| gosmo `ARCHITECTURE.md:2317` | `AgentJobByName` | `JobByName` |

**Optional guard:** a test that every backticked Go identifier in the five
gossms docs resolves somewhere in either repo, with an allowlist for SQL and
SMO names. It would have caught every row above.

### D2 — F1 help is missing editor shortcuts the menus bind

`CLAUDE.md` calls the F1 help the keyboard reference. Its Query Editor
section (`internal/tui/help_dialog.go:186`) lacks shortcuts that `menu.go`
binds:

- Ctrl+Enter (Execute at Cursor);
- Ctrl+/ (comment);
- Ctrl+D (duplicate line);
- Ctrl+L (delete line);
- Ctrl+Shift+Up/Down (move line);
- Ctrl+Shift+U and Ctrl+U (case);
- Ctrl+A;
- Ctrl+F, Replace, F3 and Shift+F3.

Its Format Document line (`:197`) says "(JSON, XML; …)", leaving out SQL,
which the Standard style formats.

**Fix:** add the lines.

**Test:** a test that every `Shortcut:` in `menu.go` appears in `helpLines`,
so the next binding cannot be missed.

---

## Questions for the author

- **Q1 — G4b:** build the restore *plan* (full, then the latest differential,
  then the log chain from msdb history, with `StopAt` applied to the last
  log, and a tail-log backup first)? Or stop at G4a's STANDBY and
  point-in-time on one chosen set? G4b is the largest item here and needs
  G5's tail-log options.
- **Q2 — Agent per-field setters (gosmo, breaking):**
  - These are about 19 one-line wrappers over `Alter(XChanges)`:
    - `Job`: `SetDescription`, `SetCategory`, `SetOwner`, `SetStartStep`,
      `SetEmailNotify`, `SetDeleteLevel`, `SetEnabled`;
    - `Alert`: `SetTrigger`, `SetDatabase`, `SetDelay`,
      `SetNotificationMessage`, `SetCategory`, `SetJobResponse`;
    - `Operator`: `SetEmailAddress`, `SetCategory`;
    - `Schedule`: `SetFrequency`, `SetActiveRange`, `SetOwner`.
  - `MailAccount`, `MailProfile`, `Route` and `Queue` have `Alter` only.
  - Remove the wrappers, so one write has one spelling? Or keep them as
    documented conveniences? They are a duplicate spelling rather than a
    capability, so `~/go/gosmo/CLAUDE.md` § This is a library does not decide
    it. Keep `Enable`/`Disable`/`SetEnabled`, which every enable-able type has.
- **Q3 — A1:** extract the gesture router, or only fix the doc sentence?
- **Q4 — A2:** move the security generator into gosmo, or only fix B1?
- **Q5 — G5:** add backup encryption, tail-log and expiry to gosmo now, or
  when G4b needs them?
- **Q6 — S2:** bind the msdb procedure arguments as parameters?

---

## Author's answers (2026-10-26)

- **Q1:** the full restore plan — G4a and G4b both.
- **Q2:** remove the Agent per-field setters (breaking; a **Migrating** note
  waits for H1). `Enable`/`Disable`/`SetEnabled` stay.
- **Q3:** yes — extract the gesture router (A1).
- **Q4:** yes — move the security generator into gosmo (A2); B1 still lands
  first, on the gossms constant.
- **Q5:** only what G4b needs: tail-log `NO_TRUNCATE`/`NORECOVERY` in
  `BackupOptions`. No encryption, no expiry.
- **Q6:** yes — bind the msdb procedure arguments as parameters (S2).

---

## Release hygiene

### H1 — carried

gosmo `HEAD` is 26 commits past `v0.1.0`, and gossms 41 past its tag. The
breaking renames of the 10-22 and 10-24 passes have no **Migrating** notes in
gosmo's CHANGELOG yet. Only when a release is asked for.

---

## Recommended implementation order

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | **B1** placeholder password | gossms (+ gosmo constant if the live replay disagrees) | The only hazard; a one-line fix plus one live replay. |
| 2 | **S1** lint drift, **D1** stale identifiers, **D2** help shortcuts and their test | both | Mechanical; restores a zero-output baseline before larger work lands on top. |
| 3 | **C1** msdb collation, **C2** subscription match, **B2** session count | gossms | Small, independent, each with a fake-driver test. |
| 4 | **G1** DML trigger Enable/Disable | gosmo → gossms | New gosmo write (offline statement test, live on 13 and 17), then the node label and toggle; tmux drive. |
| 5 | **G2** Kill Process | gosmo → gossms | Needs the new `gate.AlterAnyConnection` right; live kill of a throwaway `sqlcmd` session. |
| 6 | **G3** index node actions | gossms | gosmo already has every write; menu items, confirmation and gating only. |
| 7 | **G4a** STANDBY and point-in-time | gossms | gosmo already has both; dialog and script only. Drive a restore of a throwaway database on 17. |
| 8 | **S2** parameterised msdb EXECs, then the **Q2** setter removal | gosmo → gossms | Mechanical gosmo churn in the exact-statement tests; do it before A2 adds new scripting. |
| 9 | **A2** `Scripter.ScriptDatabaseSecurity` | gosmo → gossms | Live round trip beside `live_user_kinds_test.go`; gossms's menu item then opens its output. |
| 10 | **G5** (tail-log only), then **G4b** restore plan | gosmo → gossms | The largest item; tail-log options first, then the chain from history with `StopAt` on the last log. Drive on 17 with a throwaway database. |
| 11 | **A1** gesture router | gossms | Last: touches every panel's mouse path; tmux drive of each panel's splitter, toolbar and grid drag. |
| 12 | **H1** changelog and tags | gosmo → gossms | Only when a release is asked for. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet after any gosmo rename);
- every gosmo write it adds has its offline statement test, mutation-checked;
- the live checks named in the item have run on throwaway objects, then been
  dropped;
- for a user-visible change (B2, G1–G4a, D2), the built binary has been driven
  in tmux, per `docs/testing.md`.
