# Review plan — 2026-11-11

Review of gossms (~168k non-test lines) and gosmo (~70k), branch
`fix/review_20261110`. Plan only: nothing here is implemented. Delete this file
when its items are done or moved (`docs/open-threads.md` for anything left
undone, `docs/decisions.md` for anything settled "won't do").

## How the review ran, and what it did not cover

- Tools, both repos: `go vet`, `staticcheck`, `deadcode -test`, `dupl -t 100`,
  `govulncheck`, `go test -race ./...`.
- Hand-read: `internal/query` (executor, arena), `internal/tui` safego/tasks/
  app_saves, the permission-matrix pages and their prefetches, the
  explorer-loader and dialog clone groups, `internal/format/xml.go`, the CLI
  `Catalog` seam; gosmo `retry.go`, `helpers.go` (`scanRows`, per-database
  scans), `search.go`, `statistics.go`, the create/rename clone groups.
- **Not covered**: most of `internal/tui` (691 files), the CLI command bodies,
  gosmo's scripters. The codebase is mature and already self-audited; a
  further pass should target one family at a time rather than a sweep.
- Findings already settled were filtered out against `docs/decisions.md`
  (§ Code and dependency hygiene: `StackedHistoryChart.Draw`,
  `SpinnerByName`, `UseTrackedQueries`; § gosmo API shape) and
  `~/go/gosmo/OPEN-THREADS.md` (the DDL-trigger twins). gosmo's exported
  functions `deadcode` reports are library surface, kept deliberately.

Each item: **ID — title** · repo · kind · effort (S < 1 h, M ≤ ½ day, L > ½ day).

---

## A. Bugs

### A1 — Permissions pages never list application roles · gossms · bug · M · **DONE 2026-11-11**

`databasePermPrincipals(users, roles)` (`server_permissions_matrix.go`) builds
the grantee list for every database-scope Permissions page and New dialog —
Database, Schema, Table, Sequence Properties; New Schema, New Sequence, New
Synonym — from users and database roles only. Application roles are valid
grantees (`GRANT SELECT ON … TO [approle]`; SSMS lists them), so:

- an application role cannot be granted, denied or revoked anything from any
  of these pages;
- an **existing** grant to an application role is read (`objectPermEntries`)
  but silently dropped: `buildPermissionsMatrix` only shows principals in the
  list, so the page claims the role has nothing. The same holds for any
  principal holding a grant but missing from the list.

Same dialog, opposite answer: New Schema already reads `ApplicationRoles` and
offers them as *owners* (`new_schema_dialog.go`), just not as grantees.

Fix:
1. `databasePermPrincipals(users, roles, appRoles)`; the type column shows
   `gosmo.PrincipalApplicationRole`.
2. In `buildPermissionsMatrix`, append any principal named by `entries` but
   absent from `principals` (type from `PermissionEntry.PrincipalType`), so a
   grant can never be invisible again.
3. Tests: a matrix test with an app-role grant in `entries` and not in
   `principals`; one per page that the app role appears. Live: grant SELECT
   to a throwaway app role on 13 and 17, open Table Properties ▸ Permissions,
   revoke from the page, confirm with `sys.database_permissions`.

Depends on nothing; C1/C2 make it a one-site change if done first.

### A2 — `query.runScript` is quadratic in messages · gossms · bug (perf) · S · **DONE 2026-11-11**

`runScript` calls `res.errorCount()` twice per batch execution, and it scans
all of `res.Messages` (`internal/query/executor.go`). `GO 100000` over a
statement printing `(1 row affected)` is 10⁵ executions × a growing slice:
~10¹⁰ comparisons, seconds-to-minutes of CPU on the executing goroutine after
the server is done. Fix: keep an `errors int` on `Result`, bumped in
`addError`/`addBatchError` and the `MsgError` case (every place an
`IsError` message is appended — grep `IsError: true` in the package,
`cancelledMessage` included), and have `errorCount`/`HasErrors` read it.
Test: a fake-driver run of `GO 5000` with one error stays linear (assert the
count, and a benchmark in `executor_test.go`).

### A3 — Test-only helpers left dead · gossms · hygiene · S

`staticcheck`/`deadcode -test` report: `dbServer.writeCount`
(`internal/cli/cmd/db_server_test.go`), `objKey` and a never-read `res`
(`maint_index_test.go`), `instanceServer.reads` (`server_state_test.go`),
`clitest.JobStateRow` (`internal/cli/clitest/clitest.go`). Either a test lost
an assertion it meant to make (check `git log -S` for each: `writeCount` and
`reads` look like counters a test stopped asserting — restore the assertion
if so) or delete them. `staticcheck ./...` then runs clean.

### A4 — Go toolchain 1.27.3 carries GO-2026-6859 · both · security · S · **DONE 2026-11-11**

`govulncheck` flags `net/http` (MaxBytesReader race, HTTP/1 *server*). Both
binaries are clients, so exposure is nil, but the release workflow builds
with whatever `go.mod` pins. Bump `go 1.27.3` → `go 1.27.4` in both `go.mod`s
(and the CI setup-go version if pinned) so the next tag is clean.

---

## B. Inconsistencies

### B1 — A raw `go func` contradicts the "every goroutine via safego" rule · gossms · S · **DONE 2026-11-11**

`safego`'s doc says every background operation goes through it, and
`fanOut`'s says it is "the only place that spawns a goroutine outside
safego/safegoRepair". `App.FlushSaves` (`app_saves.go`, the
`savesInFlight.Wait()` waiter) is a third. It cannot panic, so the fix is the
words, not the code: name it in `fanOut`'s comment (or move the
wait-with-deadline into a `fileutil.WaitTimeout(wg, d)` helper and say so).
Also: the same function can overrun its deadline — `a.cfg.Save()` after the
first `select` is not bounded by `deadline`. Bound it or document that it is
accepted at exit.

### B2 — Grantee and owner pickers read principals three different ways · gossms · S · **DONE 2026-11-11**

Thirteen prefetches read `Users` + `Roles` (+ sometimes `ApplicationRoles`)
and assemble `permPrincipal`s or owner names by hand; which kinds each
includes is per site (A1 is the visible symptom). Fold into C2's helper so
the rule — grantees and owners are users, roles and application roles, minus
the per-dialog exclusions — lives once.

### B3 — Encrypt mode and gotuipic (already tracked) · gossms

Not new, listed so the order below accounts for them:
`docs/open-threads.md` § Connection config (non-canonical `encrypt_mode`
must be refused) and § Picture viewer (gotuipic untagged — **release
blocker**).

---

## C. Simplifications / refactors

### C1 — One `pageObjectPermissions` for schema-scoped securables · gossms · S · **DONE 2026-11-11**

`pageTablePermissions` (`table_props.go`) and `pageSequencePermissions`
(`sequence_props.go`) are the same 27 lines differing only in
`gosmo.SecurableTable`/`SecurableSequence`. Replace both with
`pageObjectPermissions(sc, dbName, class gosmo.SecurableClass, schema, name)`
and use it from every future object page (views, procedures, functions,
synonyms — SSMS has a Permissions page on each; gossms currently has none
there, so this also makes adding them one line each).

### C2 — `loadDatabasePrincipals(ctx, d)` prefetch helper · gossms · S

Returns `{users, roles, appRoles}` plus `.grantees()` and `.owners(exclude)`.
Replaces the hand-rolled triple reads in the 13 prefetches (`new_schema`,
`new_sequence`, `new_synonym`, `new_role`, `new_application_role`,
`new_user`, `schema_props`, `role_props` ×2, `fulltext_props`,
`key_actions`, `owner_transfer_page`, the permission pages). With D1 it is
one round trip.

### C3 — Parallelise independent prefetch reads · gossms · optimisation · M

Prefetches run 4–6 independent catalog reads serially
(`fetchNewSequencePrefetch`: database, schemas, sequences, users, roles).
Each is a round trip; on a WAN link (the MI, the Linux AG) that is the
dialog's open latency. Add a small `prefetchAll(ctx, fns...)` (error-group
semantics, first error cancels, labelled + recovered like `fanOut`) and use
it where reads don't depend on each other. Measure on ubusql1 before/after;
keep serial where a later read needs an earlier one's result.

### C4 — Separate run state from `query.Result` · gossms · refactor · M

`Result` carries public output and private per-run state (`progress`,
`lineOffset`, `maxRows`, `cut`, `stopOnError`, `noSinkNotices`, `sinkSets`);
`Option` is `func(*Result)`. Move the private fields to an unexported
`run` struct that holds `*Result` and is what `runBatch`/`scanNext` take;
`Option` becomes `func(*runOptions)`. Exported API unchanged except
`Option`'s underlying type. Do after A2 (same lines). Pure refactor — the
executor tests must pass unchanged.

### C5 — Explorer "disabled-label" loaders · gossms · S

`loadServerTriggersChildren`, `loadAuditsChildren`,
`loadServerAuditSpecificationsChildren` (`explorer_management.go`,
`explorer_security.go`) build the same node from `(Name, IsEnabled,
CreateDate)`. A generic `enabledNodes[T](l, kind, list, name, enabled,
created)` removes three copies; worth it only if a fourth appears — otherwise
cross-reference them as twins (the gosmo DDL-trigger precedent). Author's
call.

### C6 — New Sequence / New Synonym prefetch twins · gossms · S

`fetchNewSequencePrefetch` and `fetchNewSynonymPrefetch` differ only in
`d.Sequences` vs `d.Synonyms`. After C2/C3 the remainder is ~8 lines; make it
`fetchSchemaScopedPrefetch(ctx, sc, db, list func(*gosmo.Database) ([]schemaName, error))`
or leave as twins with a cross-reference. Same shape:
`new_fulltext_property_list_dialog.go` / `new_fulltext_stoplist_dialog.go`.

### C7 — Files over the ~900-line split prompt · both · S each

Split along existing section comments when next touched (CLAUDE.md
procedure: exact line ranges, byte-diff, Package map row):
gossms `internal/tui/explorer_databases.go` (952),
`internal/cli/cmd/db_storage.go` (934), `internal/cli/cmd/object.go` (929);
gosmo `database.go` (929), `scripter_table.go` (928). Not a defect — do only
alongside other edits to those files.

### C8 — Finish moving the CLI off the `Catalog` seam · gossms · refactor · L

`docs/open-threads.md` § Command line leaves it as "the author's call". The
seam now serves name resolution, database listing/sizes/options, server
info/capabilities, storage reads and `server log`; every other group calls
gosmo directly with `gosmotest` fakes. Two mechanisms for one job is the
cost. Recommendation: finish it — move each remaining read to gosmo direct
calls group by group (storage first, it is the largest), convert their tests
to `gosmotest`, delete `Catalog` last. Needs E3 if `gosmotest` lacks a canned
shape for a read.

---

## D. gosmo changes (breaking allowed, per this review's brief)

### D1 — `Database.Principals(ctx, kinds...)` · gosmo · feature · M

One read of `sys.database_principals` returning users, database roles and
application roles (typed), instead of three. gossms (C2) then does one round
trip per dialog instead of three. Keep `Users`/`Roles`/`ApplicationRoles`
(not narrowing). Add to `TestLiveVersionSweep`; run on 13.

### D2 — `Statistic.Columns` should bind the object name · gosmo · S

`statistics.go` splices `escapeSingle(FullName())` into `OBJECT_ID(N'…')`
though the query is already parameterised (`@p1` is the stat name). Pass it
as `@p2`: one plan for every table instead of one per table in the plan
cache, and one less escaping site. (`DBCC SHOW_STATISTICS` genuinely can't
take parameters; leave those.)

### D3 — `SearchDefinitions` lowers whole module texts · gosmo · optimisation · M

`LOWER(m.definition) LIKE '%' + LOWER(@p1) + '%'` copies every
`nvarchar(max)` definition in the database. A `COLLATE
Latin1_General_100_CI_AS` comparison avoids the copy, but changes semantics
for non-Latin case folding (Turkish İ, Greek final sigma) — which is what the
current comment chose `LOWER` to get. **Measure first** on a database with a
few thousand modules; act only if it is seconds. Otherwise record as settled
in `~/go/gosmo/OPEN-THREADS.md`.

### D4 — Create-with-owner twins · gosmo · S

`CreateSchema` (`database.go`), `CreateRole` (`database_role.go`) and the
server-role create (`server_role.go`) are one shape: name check, `CREATE X
[name] AUTHORIZATION [owner]`, `createdObject` read-back. A private
`createOwned[T](ctx, exec, kind, name, owner, ref, byName)` keeps the three
exported methods and their error texts byte-identical. Offline write tests
in `script_*_write_test.go` must stay green unchanged (that is the proof).

### D5 — Small clone groups worth a look · gosmo · S

`dupl` also pairs `asymmetric_key.go`/`certificate.go` (private-key
handling), `external_resource.go`/`user_defined_type.go`, the two
`replication.go` blocks, and `resource_governor.go`'s two pool readers. Read
each pair; unify only where the twins must change together, else add the
twin cross-reference comment.

---

## E. Verification / test infrastructure

### E1 — `go test -race` in the standing check · both · S

Add `go test -race ./...` to the documented verify list in
`docs/testing.md` (and CI if there is a test job), since nearly every UI
result crosses goroutines via `postAndWake`. Result of this review's run:
see the note appended at the end of this file.

### E2 — `staticcheck` / `deadcode -test` clean as a gate · both · S

Both are near-clean today (A3 is the whole list for gossms; gosmo is clean).
Document them in `docs/testing.md` as part of "calling anything done" so new
dead test helpers are caught when written.

### E3 — `gosmotest` coverage for the storage reads · gosmo · M

Prerequisite for C8: canned row helpers for files, volumes, space and log
usage, matching `clitest`'s row builders, so the storage commands' tests move
without hand-written fakes.

---

## Recommended implementation order

Bugs first, then the helpers that make later items one-line changes, then
the larger refactors. Each step is independently committable.

1. ~~**A4** — toolchain bump (both repos). Trivial, unblocks a clean tag.~~ **Done 2026-11-11**: gossms `go 1.27.4`, gosmo `toolchain go1.27.4`; build, vet, tests green; govulncheck clean of GO-2026-6859 (CI uses `go-version-file`, nothing else pinned).
2. ~~**A2** — quadratic `errorCount`. Contained, testable offline.~~ **Done 2026-11-11**: `runScript` scans only the batch's own messages (`Messages[len-before:]`, append-only during a run) and `errorCount` is gone. Not the planned `errors int` counter: `Messages` is exported and callers outside the package append, replace and filter it, so a counter behind `HasErrors` would go stale; `HasErrors` stays a scan (once per run). `TestGoRepeatCountsFailedExecutions` (GO 5000, one failure) and `BenchmarkGoRepeat`: 2000→8000 executions went 7→99 ms before, 5→24 ms after.
3. ~~**A3 + E2** — clear the static findings, then document the gate.~~ **Done 2026-11-11**: `writeCount`, `reads`, `JobStateRow` were born unused (fb52b6b), `objKey` a leftover of 2d6ea22's fake conversion — all deleted, no lost assertion. The never-read `res` was one: the view-reorganize case in `TestMaintIndexRebuild` now asserts its rows (7.00 → 0.50). `staticcheck`/`deadcode -test` clean. Gate documented in `docs/testing.md` (§ Static checks), `CLAUDE.md` and gosmo's `CLAUDE.md` § Build & verify. gosmo gates on `staticcheck` only: `deadcode` there lists library surface plus unexported helpers only it reaches (`schedulerGroupSizes`), so it is not a clean-able gate.
4. ~~**D1** (gosmo) — `Database.Principals`; live sweep on 13.~~ **Done 2026-11-11**: `d.Principals(ctx, kinds ...PrincipalKind) (*DatabasePrincipals, error)` (`database_principals.go`) — one `sys.database_principals` read built on `userSelect` (now taking extra columns), dispatched by `type_desc` into `Users`/`Roles`/`ApplicationRoles`; no kinds = all, an unknown kind is `ErrInvalidRequest`. Each family is filled as its own read fills it; roles as `RoleByName` does (SID and dates too, which `Roles` leaves out). Offline tests compare field-by-field with `UserByName`/`RoleByName`/`ApplicationRoleByName` (mutation-checked); `TestLivePrincipalsMatchesTheFamilyReads` green on 13 and 17; `TestLiveVersionSweep` green on 13 (441 calls, 0 failed). The reflective sweep now calls a variadic read with no arguments only when its type is in `sweepEmptyVariadics` (an open-ended rule also reached `Server.BackupHeaders`, which refuses no targets); `Database.Principals` is in `sweepMustCall`. Feature map and `diagram/05-database-handle.mmd` updated.
5. ~~**C2** — `loadDatabasePrincipals` on top of D1.~~ **Done 2026-11-11**: `internal/tui/database_principals.go` — `loadDatabasePrincipals(ctx, d)` wraps `Database.Principals` (one round trip) with `.grantees()` (users and roles, unchanged: A1 adds application roles there), `.names()` and `.owners(exclude)`. All 13 sites converted: New Schema/Sequence/Synonym/Role/Application Role/User prefetches, Database/Schema/Table/Sequence Permissions, Schema General, Role General/Members/Owned Roles, Owned Schemas, `keyOwnerRow` (drops `Roles` when `!withRoles`), `fullTextOwnerItems`. Single-family reads (`database_props` users, `user_props`/`login_props`/`new_login` roles, the msdb mail users) stay as they are. Fake-driver fixtures answer the new read through `principalsResponse(families...)`, derived from each test's existing user/role/app-role fakes; `TestLoadDatabasePrincipalsIsOneRead` asserts no family read runs beside it. Gate clean (vet, staticcheck, deadcode, `go test ./...`). Live: a throwaway-database scratch test ran every converted loader on 17 and 13 — `names()`/`grantees()` equal to the three family reads, every page loads; the TUI itself was not driven.
6. ~~**A1** — application roles in the permission matrices (now a change to
   one helper plus `buildPermissionsMatrix`); live check on 13 and 17.~~ **Done 2026-11-11**: `databasePermPrincipals(users, roles, appRoles)` lists application roles last, typed `APPLICATION_ROLE`; `databasePrincipals.grantees()` passes them, so all seven pages/dialogs (Database/Schema/Table/Sequence Permissions, New Schema/Sequence/Synonym) offer them. `buildPermissionsMatrix` now runs `withEntryPrincipals`, appending any principal an entry names but the list lacks (server page included), so an existing grant can no longer read as "(none)". Tests: the page suite gained a sequence scope and an application role — `TestPermissionsMatrixGrantsToAnApplicationRole` (all four database scopes write `TO [billing_app]`), `TestPermissionsMatrixShowsAGranteeTheListLacks` (an unlisted grantee's GRANT shows and revokes); `grantees()` asserted in `TestLoadDatabasePrincipalsIsOneRead`; both fixes mutation-checked. Gate clean. Live (scratch test, throwaway database, 17 and 13): Table Properties ▸ Permissions listed both app roles, showed `billing_app`'s existing SELECT as Grant, revoked it and granted UPDATE to the other from the page — confirmed in `sys.database_permissions`. TUI not driven.
7. ~~**C1** — `pageObjectPermissions`.~~ **Done 2026-11-11**: `pageObjectPermissions(sc, dbName, class, schema, name)` (`server_permissions_matrix.go`, beside `objectPermEntries`) replaces `pageTablePermissions` and `pageSequencePermissions`; Table and Sequence Properties call it with `gosmo.SecurableTable`/`SecurableSequence`. The page suite already wrote at each scope, but nothing pinned the class: hard-coding `SecurableTable` in the helper (catalog or securable) passed every test, since both classes write `ON [schema].[name]`. New `TestPermissionsMatrixOffersItsOwnCatalog` (each scope's grid lists exactly its gosmo catalog) catches the catalog mutation. Gate clean, `go test ./...` green. Not live-tested: a pure extraction of two identical bodies.
8. ~~**B1, B2** — comment/consistency fixes (B2 is closed by 5–6 — done).~~ **Done 2026-11-11**: B1 fixed in words, not code. `fanOut`'s doc now says it is one of two places spawning outside safego and names `FlushSaves`' `savesInFlight` waiter as the other (cannot panic, runs after the event loop is gone); `safego`'s "every background operation" points there; `ARCHITECTURE.md` § Starting the goroutine and the `safego.go` Package map row say the same. The deadline overrun is documented as accepted in `FlushSaves`' doc: `wait` bounds the waits, not the synchronous save of a change asked for after the in-flight one, which can overrun by up to `fileutil.lockWait` (2 s) plus the write — it is the user's only copy of that change. No `fileutil.WaitTimeout` helper (one caller). B2 closed by steps 5–6.
9. **C3** — parallel prefetch; measure on ubusql1.
10. **C4** — split run state out of `query.Result` (after A2).
11. **D2, D4, D5** — gosmo small cleanups; offline write tests prove them.
12. **B3** — the two tracked threads: encrypt-mode refusal, then gotuipic tag
    (the release blocker — must land before the next tag regardless).
13. **E1** — race detector in the standing check.
14. **E3 → C8** — `gosmotest` storage helpers, then retire the CLI `Catalog`
    seam group by group.
15. **D3** — measure `SearchDefinitions`; act or record as settled.
16. **C5, C6, C7** — opportunistic, only with other edits to those files.

## Race-detector run (2026-11-11)

`go test -race -count=1 ./...` passed in both repos: no failures and no data
races reported. Only the offline tests ran (no `livedb` tag), so this covers
the fake-driver paths, not live UI timing. That makes E1 a guard against
future races, not a fix for a known one.
