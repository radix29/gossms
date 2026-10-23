# Review plan — 2026-10-23 (second pass)

The fix list from a fourth whole-repo review of gossms and gosmo, run the same
day the first 2026-10-23 plan was implemented and deleted. Everything that plan
listed is done except its A1 (pool-camping spike, now half-done — see A1 below)
and H1 (release hygiene), which are carried forward. Nothing settled in
`docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is
re-raised. Two items (C1, C2) apply the naming rule the last pass wrote into
gosmo's `CLAUDE.md` to types that pass left alone.

**Baseline (2026-10-23, gossms `HEAD` db30a05 / gosmo 225fb48):** both repos
build; `go test ./...` passes in both; `go vet` (and gosmo `-tags livedb`) and
`staticcheck` (plain and `-tags livedb`) report nothing; `gofmt -l` is empty;
`deadcode ./cmd/gossms` reports the six settled hits. New this pass: an
`x/tools` multichecker (`nilness`, `unusedwrite`, `unusedresult`, `sortslice`,
`defers`, `waitgroup`, `sqlrowserr`, `reflectvaluecompare`, `deepequalerrors`,
`hostport`) is clean in both repos; the `modernize` suite reports six small
items (S1). The new exact decimal sort key was fuzzed for 20 s (1M inputs)
against a `math/big.Rat` oracle: no disagreement.

Not done in this pass: no live server, no tmux drive. B1 and B2 were found by
reading and need the live checks named in each item before and after the fix.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
The doc-link test (`TestNoDanglingDocReference`) checks every `.md` path named
here, so cite only paths that exist.

Every gosmo change still follows `~/go/gosmo/CLAUDE.md`: build and test in
gosmo first; `go vet -tags livedb ./...` after any rename; `TestLiveVersionSweep`
on the oldest instance (win10cli\SQL2016) after any query change.

IDs: **B** bug or hazard, **C** consistency, **S** simplification, **A**
architecture, **D** docs, **Q** question for the author, **H** release
hygiene.

---

## Bugs and hazards

### B1 — gosmo: a write can still leave the pool's login database unopenable

**DONE 2026-10-23.** Both halves, by the author's choice: once `Connect` has
returned (never at the first ping, so a misspelled database still fails
Connect; never on Azure SQL Database), a dial the login database refuses moves
the pool to master for good and dials again; and NORECOVERY `Restore`,
SINGLE_USER `SetUserAccess` and `UnjoinDatabase` call `leftLoginDatabase`
(which also releases idle sessions). Probed: 17, 14 and 13 refuse with **Msg
4063** alone ("…Using the user default database master instead"), not 4060,
for every cause. Offline: `TestRefusedDialMovesThePoolToMaster`,
`TestRefusedDialThatStaysPut`, three new `TestWriteOnLoginDatabaseMovesThePool`
cases, `TestWriteThatKeepsLoginDatabaseOpenable`. Live:
`TestLiveLoginRefusal` (own NORECOVERY, own SINGLE_USER, another session's
OFFLINE / NORECOVERY / SINGLE_USER held, refused at Connect) passes on 17, 14
and 13; five of six fail with the fix disabled. tmux on 17: Connect to
database set, Restrict access → SINGLE_USER, 70 s, expand, MULTI_USER, New
Query lands in master. One MULTI_USER attempt failed Msg 5064 and did not
reproduce — `docs/open-threads.md` B28. `UnjoinDatabase` is covered offline
only (no AG run).

`leftLoginDatabase` (`~/go/gosmo/connection_retarget.go:100`) moves the pool
to master when this `Server` drops, detaches, takes offline or renames the
database its connections log in to (`ConnectionOptions.Database`). Three
writes that leave that database just as unopenable are not covered:

- **`Server.Restore` with `RestoreWithNoRecovery`** (`restore.go:99`): the
  database is left RESTORING. Every connection the pool dials afterwards fails
  Msg 4060 at login, and a pooled connection's reset re-enters the login
  database and fails the same way, so the whole `Server` is dead until the
  user reconnects. A log-shipping restore chain on a connection opened "to
  database X" hits it on the first step.
- **`Database.SetUserAccess(UserAccessSingle)`**
  (`database_settings.go:110`): the session that ran the ALTER becomes the one
  user. Every other new login fails Msg 924. Reads pass while the pool happens
  to hand back that one connection and fail under any fan-out; after
  `connMaxIdleTime` (60 s, gossms `internal/db/connection.go`) closes it, every
  read fails, including the ALTER back to MULTI_USER that would repair it. In
  gossms this is Database Properties ▸ Options ▸ Restrict access
  (`internal/tui/database_props_options.go:148`).
- **`ALTER DATABASE … SET HADR OFF` / `REMOVE DATABASE` on a secondary**
  (`availability_database.go:246`, `:262`): the local copy goes RESTORING, as
  with NORECOVERY.

Nor can the current design cover another session doing any of these, or
`SET OFFLINE`, to the login database.

**Fix (recommended):** make the retarget react to the refusal instead of
predicting it. In `retargetingConnector.Connect`, when a dial fails with Msg
4060, 924, 942, 952 or 976 *and* the login database is not master, retarget to
master and dial once more. Keep `leftLoginDatabase` for the cases it already
handles: it saves the failed dial. This covers the three writes above and every
external cause. `InitialDatabase` already tells gossms's `CloneOpts` where the
pool went.

**Alternative (narrower):** call `leftLoginDatabase(ctx, name, "master")`
after a successful NORECOVERY restore, a SINGLE_USER ALTER and a SET HADR OFF
or REMOVE DATABASE on a secondary.

**Verify:**

- Offline, with a fake connector that fails the first dial with
  `mssql.Error{Number: 4060}`: one retry with `Database = "master"`, and
  `InitialDatabase()` reports master afterwards.
- Live on 17: connect a `Server` with `Database` set to a throwaway database;
  restore it WITH NORECOVERY, then read `Databases`; set it SINGLE_USER, wait
  past `ConnMaxIdleTime`, read again, then set it MULTI_USER. Every step must
  pass. Before the fix, each step reproduces the failure.
- Then drive gossms in tmux: Connect with "Connect to database" set, Restrict
  access → SINGLE_USER, wait 70 s, expand Databases, set it back.

### B2 — gosmo: per-database batches swallow every error, so the version sweep is blind there

**DONE 2026-10-23 (with D3).** The CATCH is now `IF ERROR_NUMBER() NOT IN
(perDatabaseSkipErrors) THROW;`. Probed live on 13, 14 and 17, identical:
942 OFFLINE, 924 SINGLE_USER held, 927 RESTORING, 916 no access (also
RESTRICTED_USER and EMERGENCY for a non-sysadmin), and **911** (dropped or
renamed since listed — not in the list below). 922, 926, 945, 952, 976, 978
and 40613 are kept from the documentation, unprobed (the author chose not to
probe 976/978 on the AG cluster). Offline: `TestPerDatabaseExecReraisesQueryErrors`.
Live: `TestLivePerDatabaseBatchSkipsOnlyUnenterable` (every skip state, as sa
and as a login with no user; Msg 207/208/468 at compile and 8134 after the
first database's rows; `UserMappings` through a broken query) passes on 13, 14
and 17 and fails on the old empty CATCH. `TestLiveVersionSweep` and the
lean-listing, replication and ref-read live tests pass on 13, 14 and 17.
No gossms change: User Mapping already shows a load error as an error.

`perDatabaseExec` (`~/go/gosmo/helpers.go:411`) wraps each database's step in
`BEGIN TRY … END TRY BEGIN CATCH END CATCH`. The intent is to skip a database
that cannot be entered (offline, no access, SINGLE_USER held, a non-readable
AG secondary). But the empty CATCH skips *any* error, and a compile error
inside `sp_executesql` is caught by the caller's TRY. So these all come back as
"no rows in that database", never as an error:

- a column missing on an older major (Msg 207);
- a collation conflict (Msg 468);
- a typo in the query text (Msg 102, 208).

Three reads go through it:

- `Login.UserMappings` (`login_mapping.go:78`);
- `Server.LocalPublications` (`replication.go:563`);
- `Server.LocalSubscriptions` (`replication_subscription.go:336`).

`TestLiveVersionSweep` calls all three, and it would report zero failures for
a broken query: the gate the open-threads policy relies on cannot see this
path.

The user-visible cost is in Login Properties ▸ User Mapping. A database whose
read failed for any reason shows the login as unmapped. Ticking it then fails
at CREATE USER with "already exists", which points the user at the wrong
problem.

**Fix:** re-raise everything that is not a can't-enter-this-database error.
In the CATCH, put `IF ERROR_NUMBER() NOT IN (<list>) THROW;`, and probe the
list live rather than writing it from the documentation:

- OFFLINE: 942;
- SINGLE_USER held by another session: 924;
- RESTORING or recovering: 927, 922;
- no access: 916;
- non-readable secondary: 976, 978;
- in transition: 952;
- SUSPECT, inaccessible files: 926, 945;
- Azure: 40613.

Keep the CATCH-and-skip for those. A THROW ends the batch with the real error,
which `perDatabaseBatch` already wraps.

**Verify:**

- Offline: a test that checks the batch text (every `perDatabaseExec` CATCH
  carries the guard).
- Live: inject a bad column into a copy of `userMappingsQuery` behind a test
  hook. `UserMappings` must fail naming Msg 207, where today it returns an
  empty list.
- Live on 13 and 17: the probe that settles the list. One database each OFFLINE,
  SINGLE_USER held by another session, RESTORING, and one the login has no user
  in. All four are still skipped.
- Then `TestLiveVersionSweep` on 13.

---

## Consistency (gosmo, breaking, mechanical)

gosmo's `CLAUDE.md` § Conventions (written by the last pass) says:

- a full description that `Create` and `Alter` both take is an
  `XDefinition`;
- an argument whose unset fields mean "leave as stored" is `XChanges`.

Three types still break the rule. Do C1–C3 in one sitting with one tag, as the
last pass did, then run `go vet -tags livedb ./...` and update gosmo
`ARCHITECTURE.md`'s feature map.

### C1 — `MailConfigurationOptions` is a patch

`database_mail_service.go:22`: "a change to the system parameters. A nil
field is left as stored", taken by `Mail.SetConfiguration`. The last pass
renamed its siblings `MailAccountChanges` and `MailProfileChanges` and missed
this one.

**Fix:** rename it `MailConfigurationChanges`. One gossms call site.

### C2 — `EventSessionSpec` and `JobStepRequest` are definitions

- `EventSessionSpec` (`extended_events_script.go:48`) is what both
  `XEvents.CreateSession` and `EventSession.Alter` take, and
  `EventSession.Spec()` returns it. The last plan kept the name because it
  "doesn't collide". The rule written in the same pass now names it
  `EventSessionDefinition`. Rename the accessor to `Definition()` with it.
  It takes no context, so it can't be confused with the modules'
  `Definition(ctx)`.
- `JobStepRequest` (`agent_job_step.go:546`) is what `Job.AddStep`,
  `Job.InsertStep` and `JobStep.Alter` all take ("a step to add to, or replace
  the definition of"). Rename it `JobStepDefinition`.

gossms call sites: 23 + 5 (`EventSessionSpec`, `.Spec()`) and 7
(`JobStepRequest`).

### C3 — `AttachDatabase` is the one create-shaped call off the `Create*` shape (optional)

`Server.AttachDatabase(ctx, AttachSpec) error` (`database_attach.go:129`)
creates a database but returns no handle, and its argument is a `Spec`.

**Fix:** match `CreateDatabase`:
`AttachDatabase(ctx, AttachDatabaseRequest) (*Database, error)` through
`createdObject`. One gossms call site. Skip it if C1–C2 are not being done.

Not raised:

- `AvailabilityReplicaSpec`, `AvailabilityListenerSpec`,
  `DatabaseFileSpec`, `SnapshotFileSpec` and `FullTextIndexColumnSpec` are
  components of a create request, not the request itself.
- `CredentialOptions` is the documented exception.
- `SetAgentMailSettings(ctx, AgentMailChanges)` reads oddly, but it pairs with
  the `AgentMailSettings` read.

---

## Simplification

### S1 — the `modernize` findings (both repos, no behaviour change)

gossms:

- `internal/format/xml.go:285` (redundant embedded type in a literal);
- `xml.go:376` (`strings.Cut`);
- `internal/fileutil/atomic.go:227` (`strings.CutLast`);
- `internal/tuikit/controls/editor_completion.go:412` (`min`);
- `internal/tui/replication_monitor_panel_draw.go:132` (`slices.Clip`).

gosmo:

- `scripter_extended_properties.go:273` (`+=` in a loop → `strings.Builder`);
- 15 `reflect.TypeOf` → `reflect.TypeFor` and a `NumMethod` loop →
  `Type.Methods` in `versionsweep_reflect_test.go`.

To reproduce: build a multichecker over
`golang.org/x/tools/go/analysis/passes/modernize.Suite` (x/tools v0.51.0 is
in the module cache).

### S2 — two files past the ~900-line split prompt (only when a change lands)

`internal/tuikit/controls/datagrid.go` (950) and `internal/config/config.go`
(923). Per `CLAUDE.md` this is a prompt, not a defect. Split along their own
section comments, extracting by line range and diffing byte for byte. A new
`internal/tui` file needs no package-map row here: both are outside
`internal/tui`. Do it only alongside a change to either file.

---

## Architecture

### A1 — gosmo: finish the pool-camping spike (carried, now half-done)

**DONE 2026-10-23 (with D1): go.** Step 1 held on 13, 14 and 17: the five
`live_proc_read_test.go` tests, `TestLiveVersionSweep`, the collation tests
and the whole `livedb` suite with `-procreads`. The only differences were
expected: the `TestLiveExclusiveAccess*` precondition (no session left behind
any more) and the lost `gosmo: USE db:` prefix. Running it found two test
bugs: every `gosmo_a1_*` database leaked (`defer done()` closed the pool
before `t.Cleanup(drop)`), and so the id-swap test never swapped. Both are
fixed; the ids now swap, but its control still does not reproduce the
plan-cache trap.

By the author's choice: `procRead` is the only path for a pooled
`Database.query`/`queryRow` (the switch and the fallback are deleted);
`procParamType` declares `time.Time` (`datetimeoffset(7)`, the only
undeclared type seen in a full run) and the uints; an undeclarable type fails
the read (`ErrUnsupported`). `recheckEntry` keeps the `gosmo: USE db:` prefix
on a can't-enter error with one empty call of `db`'s `sp_executesql`, since
the RPC refusal and the same Msg raised inside the query are byte-identical
(probed). `cannotEnterErrors` is now shared with `perDatabaseSkipErrors`.
`useBatch`/`recheckUse` remain for reads inside `InTransaction`. A failed
read there loses its server error to the driver's "no active transaction"
(found by the widened `TestLiveUseBatch`, older than this work), and
`recheckUse` now keeps the server's error. gossms keeps `connMaxIdleTime` at
60 s, because writes (`withConn`) still `USE` and camp; the comment says so.
`leaveTwoIdleIn` now parks sessions through writes.

Verified: unit tests in both repos (the gosmo and gossms fake drivers unwrap
the call); `TestLiveProcRead*`, `TestLiveUseBatch`,
`TestLiveExclusiveAccess*`, `TestLiveVersionSweep`, the collation,
login-refusal, per-database and in-transaction live tests pass on 13, 14 and
17. Full `livedb` on 13 with the final code: 1394 pass, and the only failures
are two that also fail on a clean `HEAD` (a third, Msg 701, comes and goes
with free memory), recorded in `~/go/gosmo/OPEN-THREADS.md`. tmux on 17: expand
a database, its tables and columns; no gossms session is left in it (9 open
elsewhere); `SET OFFLINE` with no ROLLBACK succeeds at once; F5 then shows
`gosmo: USE …: … offline`.

The spike the last plan asked for has landed, in gosmo 225fb48:

- `procReads` and `procRead` (`proc_read.go`) send a database-scoped read
  as an RPC of `[db].sys.sp_executesql`, behind an unexported switch that is
  off;
- `live_proc_read_test.go` holds five live tests, one per rule the open thread
  names: session stays home, RESTORE not blocked, can't-enter refusal, id swap,
  parameter collation.

What is left:

1. Run the five live tests and `TestLiveVersionSweep` with `procReads = true`
   on 13, 14 and 17. Record the result in `~/go/gosmo/OPEN-THREADS.md` (D1).
2. If they hold, turn it on by default. Then simplify behind it:
   - `useBatch` and `recheckUse` stay only for an argument type
     `procParamType` cannot declare. Consider adding `time.Time`, unsigned
     ints and `sql.NamedArg` so the fallback becomes unreachable, and delete it;
   - gossms's `connMaxIdleTime` comment and value return to the default. Keep
     60 s if it is still wanted for `AUTO_CLOSE`.
3. If any rule fails, record which and delete the switch.

B1's fix is independent of this. procReads keeps the session out of the
database it reads, not out of the one it logs in to.

---

## Docs

- **D1 (DONE 2026-10-23, with A1) — `~/go/gosmo/OPEN-THREADS.md` § Idle pooled connections camp…** is
  headed "not investigated", but the spike code and its live tests are in. Say
  what exists, what is unrun (A1 step 1), and the go/no-go gate.
  `~/go/gosmo/ARCHITECTURE.md` doesn't mention `procReads` at all. Add one
  line beside `useBatch`.
- **D2 — the wiki lags two shipped features.** Neither the column sort
  (Object Explorer Details pane and result grid: header click, Ctrl+Space ▸
  Sort Ascending/Descending, NULLs and blanks last) nor Max fetch rows (Tools
  ▸ Options; cancels the rest of the script) appears on its pages. Add them to
  the wiki's Object Explorer, Query Editor, Keys and Mouse, and Options and
  Terminal pages. `README.md` already covers Max fetch rows, and the F1 help
  covers the sort.
- **D3 — with B2 (DONE 2026-10-23):** gosmo `CLAUDE.md` § Conventions gains one line. A
  per-database batch skips only can't-enter errors; anything else is the
  read's failure.

---

## Questions for the author

- **Q1 — Results To File throws the whole file away on any error.**
  `csvSink.finish` (`internal/tui/query_panel_export.go`) aborts when
  `res.HasErrors()`. So a two-hour export whose last batch fails (or whose
  script has one failing `DROP` among its statements) leaves the destination
  untouched and every row lost. SSMS keeps what was written. Keeping the rows
  is one condition (`cancelled` only). Is discarding deliberate? If so, it
  belongs in `docs/decisions.md`. If not, keep the file and say "written with
  errors — see Messages".

---

## Release hygiene

### H1 — carried, unchanged

gosmo `HEAD` is past `v0.1.0` with no `CHANGELOG.md` entry, and `TableKindAll`
is still `iota`-based (`~/go/gosmo/table_kind.go`). The last pass's renames and
C1–C3 here all go under **Migrating**. Do this only when a release is asked
for.

---

## Recommended implementation order

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | ~~**B2** re-raise non-entry errors in per-database batches, + **D3**~~ **DONE** | gosmo → gossms | The version sweep is blind on this path until it's done, and every later query change relies on that sweep. Small change, but the error list must be probed live first. |
| 2 | ~~**B1** self-healing login-database retarget~~ **DONE** | gosmo → gossms | The one bug that kills a whole connection. An offline fake-connector test, then live on 17, then a tmux drive. |
| 3 | ~~**A1** steps 1–3, + **D1**~~ **DONE** | gosmo → gossms | Needs 13/14/17 and the sweep, which item 1 has just made trustworthy. If the go/no-go is go, the `useBatch` removal is the biggest simplification on offer. |
| 4 | **C1, C2** (+ optional **C3**) renames | gosmo → gossms | Breaking and mechanical. One sitting, after the behaviour changes, so one tag carries the renames together. |
| 5 | **S1** modernize fixes | both | Trivial; do it in any commit that touches the files, or as one tidy-up. |
| 6 | **D2** wiki pages | wiki | Independent; do it any time. |
| 7 | **Q1** | gossms | Wait for the author's answer; either a one-line change plus a test, or a `docs/decisions.md` bullet. |
| 8 | **S2** file splits | gossms | Only when a change lands in `datagrid.go` or `config.go`. |
| 9 | **H1** changelog, tags | gosmo → gossms | Only when a release is asked for. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet in gosmo after any rename);
- the live checks named in the item have run;
- for a user-visible change (B1, Q1 if changed), the built binary has been
  driven in tmux, per `docs/testing.md`.
