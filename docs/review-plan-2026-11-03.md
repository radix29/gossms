# Review plan — 2026-11-03

The fix list from a seventh whole-repo review of gossms and gosmo, after the
2026-10-26 plan was implemented and retired. Nothing settled in
`docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is
re-raised. The author's waiver applies again: **gosmo may break backward
compatibility where it helps** (G1, C2, S2, S3).

**Baseline (2026-11-03, gossms `HEAD` 04f6ce8 / gosmo d3ec363):** both repos
build; `go test -race ./...` passes in both; `go vet ./...` passes in both,
and `go vet -tags livedb ./...` in gosmo; `staticcheck -checks all` is clean
in both (style checks ST1000/1003/1020–1022 aside); `govulncheck` finds
nothing reachable (H1 is module-level only).

How it was found:

- **Tools**: vet, staticcheck, deadcode, govulncheck, `unparam`,
  `ineffassign`, `dupl -t 100/120`. `deadcode ./cmd/gossms` lists only what
  `docs/decisions.md` § Code and dependency hygiene keeps, plus
  `sqlparse.NarrowToDMLStatement` and `core.FillAreaByPut`, both documented
  test helpers. `dupl` found no group above 30 lines; the ones worth acting on
  are C1, S2 and S3.
- **Pattern sweeps**: raw `go` statements outside `safego` (three, all
  justified in place); `context.Background()` in UI code (all detached on
  purpose: clipboard, format, sort, update check); ignored errors (all
  best-effort cleanups); `len()` in column math (none); timers and tickers
  (every one stopped); `==` on login/owner names (all catalog-to-catalog,
  which gosmo's rule wants); every `'%s'`/`N'%s'` literal in gosmo (all go
  through `escapeSingle`, an allowlisted enum or a `normalize`d mode).
- **API diff**: gosmo's exported functions that gossms never references (149
  names). Read for operations SSMS has. That found G2–G4.
- **Doc-identifier check**: backticked Go identifiers in both repos' docs
  that no longer exist in code. That found D1. SMO names in gosmo's
  `ARCHITECTURE.md` mapping table (`Server.JobServer`, …) are deliberate.
- **Read in full**: both new dialogs and their diffs (`new_role_dialog.go`,
  `new_schema_dialog.go`, the `securables_matrix.go`/`new_object_dialog.go`
  changes), gosmo's `CreateRole`/`CreateSchema`, `helpers.go`,
  `login.go`'s writes, `rule_default.go`, the trigger scripters;
  gossms's `internal/query/executor.go` (Execute/runScript/runBatch),
  `query_panel_sort.go`, `explorer_group_schema.go`, `config.go`'s
  log/config paths, the four tool-row panels' `runTool`s. Other files were
  sampled, not read whole.
- **No live drive this pass**: it is plan-only. B1 is argued from the
  catalog queries; confirm it live before fixing it.

This pass found **no data-loss, crash or security bug**. What it found: one
low-severity validation gap, four places where gosmo has (or should have) a
capability gossms does not expose, two consistency slips, three
simplifications, stale doc names, and lint drift.

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
- a new read joins `TestLiveVersionSweep`, run on the oldest instance
  (win10cli\SQL2016) after any query change;
- a new `Create*` returns through `createdObject`, and its family gets its
  `Ref` and writes together.

Every new gossms dialog follows `docs/ui-rules.md` and `docs/db-rules.md`, gets
an `ARCHITECTURE.md` § Package map row (`TestPackageMapListsEveryTUIFile`), and
is driven in the built binary (`docs/testing.md`) before it is called done.

IDs: **B** bug or hazard, **G** gap, **C** consistency, **S** simplification
or optimisation, **A** architecture, **D** docs, **H** release hygiene, **Q**
question for the author.

---

## Bugs and hazards

### B1 — gossms: New Database Role and New User miss application roles in their name check

`internal/tui/new_role_dialog.go` (`nrolePrefetch.existingNames`, "holds every
database principal's name") and `internal/tui/new_user_dialog.go`
(`nuserPrefetch.existingNames`, the same claim) are filled from `d.Users`
and `d.Roles`. gosmo's `Users` reads `type IN ('S','U','G','E','X','C','K')`
(`database_users.go`, `userTypes`) and `Roles` reads `type = 'R'`
(`database_role.go`). An application role (`type = 'A'`) is in neither, and
it shares the principal namespace.

**Effect:** typing an application role's name passes the preflight, and the
server refuses the CREATE with Msg 15023. The CREATE is the first apply, so
nothing is left half-done. The cost is a late error instead of an inline one,
and two comments that overstate what they hold.

**Fix:** lands with G1. Add the application roles from gosmo's new
`Database.ApplicationRoles` to both `existingNames`. Until then, correct the
two comments.

**Test:** extend `new_role_dialog_test.go`'s and the New User tests'
prefetch fixtures with an application-role name. Assert that the preflight
refuses it. Mutation-check by dropping the new loop.

---

## Gaps

### G1 — gosmo + gossms: application roles have no family and no tree node

SSMS lists Security ▸ Roles ▸ **Database Roles** and **Application Roles**,
and has New Application Role and Application Role Properties. gossms's Roles
folder lists database roles only (`explorer_databases.go`), and
`role_props.go` says application roles "have no tree node of their own".
gosmo reads `type = 'A'` only privately, for Script Database Security
(`scripter_database_security.go`, `applicationRole`).

**gosmo:**

- `ApplicationRole` with fields `Name`, `DefaultSchema`, `PrincipalID`,
  `CreateDate`, `ModifyDate`.
- `Database.ApplicationRoles`, `ApplicationRoleByName` (via `readByName`) and
  `ApplicationRoleRef`.
- `CreateApplicationRole(ctx, CreateApplicationRoleRequest{Name, Password,
  DefaultSchema})`. The password goes through `execSecret`, with a row in
  `secret_redaction_test.go`.
- Writes on the handle: `Rename`, `SetDefaultSchema`, `ChangePassword`,
  `Drop`.
- Move `scripter_database_security.go`'s private read onto the public one.

**gossms:**

- Roles gets two sub-folders, Database Roles and Application Roles, as SSMS
  has them.
- New Application Role dialog: General (name, default schema, password and
  confirm), Owned Schemas (reuse `buildNewPrincipalOwnedSchemasPage`),
  Securables, Extended Properties.
- Application Role Properties with the same pages.
- Gate: `ALTER ANY APPLICATION ROLE`. Probe it live on 13 and 17 the way
  `gate.CreateRole` was probed, and record the result beside it in
  `gate/gate.go`.

**Test:** gosmo offline statement tests for every write, plus a live
create/rename/drop round trip. gossms: the usual dialog tests, then a tmux
drive on 17 and 13.

### G2 — gossms: Sequences have no New Sequence and no Properties

gosmo has `CreateSequence` and `Sequence.Restart`
(`sequence_synonym.go`), and gossms calls neither. A sequence node offers
Script, Delete, Rename and Move only (`explorer_object_ops.go`,
`schemaObjectOp("Sequence", …)`). SSMS has New Sequence and Sequence
Properties: General has start, increment, min/max, cycle, cache and the
"restart with" box; there are also Permissions and Extended Properties pages.

**Fix:** add a `new_sequence_dialog.go` on `newObjectDialog`, and add
`sequence_props.go`.

- **gosmo:** if the properties' edits need an ALTER that gosmo lacks
  (increment, min/max, cycle, cache), add `Sequence.Alter(ctx,
  SequenceChanges)`. It is an `XChanges` (nil = leave as stored), and
  `Restart` folds into it or stays beside it.
- **Gating:** CREATE SEQUENCE plus ALTER on the schema, and ALTER on the
  object for Properties. Add a `gate` entry if one is missing.

### G3 — gossms: Synonyms have no New Synonym

gosmo has `CreateSynonym` (`sequence_synonym.go`), and gossms never calls it.
SSMS's New Synonym dialog takes the synonym's schema and name, then the base
object's server, database, schema and name. A synonym has no ALTER, so this
needs no Properties beyond the read-only one gossms may already show.

**Fix:** add a `new_synonym_dialog.go` on `newObjectDialog`, gated on CREATE
SYNONYM plus ALTER on the schema.

### G4 — gossms: Query Wait Statistics has no drill-down

`queryWaitStatisticsReport` (`internal/tui/query_store_reports.go`) shows
`QueryStore.WaitCategories` only. gosmo's `QueryStore.WaitingQueries`, which
its doc calls "the drill-down half of SSMS's Query Wait Statistics view", is
never called. In SSMS, picking a category lists the queries waiting in it,
and each has a plan.

**Fix:** Enter or double-click on a category row opens the ranked queries
for that category, reusing the Top Resource Consuming Queries result shape
(rows → plan pane). Only Avg and Total are valid for
`QSWaitAllCategories` (the gosmo doc says so), so the statistic selector
greys the other three there.

---

## Consistency

### C1 — gossms: the four tool-row panels refuse a withheld tool four different ways

`ActivityMonitor`, `LogViewer`, `XEventViewer` and
`ReplicationMonitorPanel` each have their own `runTool`:

| Panel | Disabled from | When refused, says |
|---|---|---|
| Activity Monitor (`activity_monitor.go`) | `Cell.Disabled`/`Cell.Reason` fields | the bare reason, or nothing |
| Log File Viewer (`log_viewer_toolbar.go`) | `toolsEnabled`/`recycleDenied` | nothing while busy; the gate text for Recycle |
| XEvent viewer (`xevent_viewer_toolbar.go`) | `toolReason(i)` | "Label: reason" |
| Replication Monitor (`replication_monitor_panel_draw.go`) | `toolDisabled(i)` | nothing |

`LogViewer.runTool` also returns a `bool` that no caller reads (`unparam`).

**Fix:** add one helper to `controls.ToolRow` (tuikit, which knows nothing of
`tui`):

```go
Run(i int, disabled func(int) bool, reason func(int) string, status func(string))
```

It bounds-checks `i`, runs the action when allowed, and otherwise reports
`Label: reason`, or nothing when the reason is empty. Each panel passes the
same `disabled`/`reason` pair it already gives `OverflowItems`, so clicks, keys
and the More menu share one gate. Drop `LogViewer.runTool`'s result.

### C2 — gosmo: `RuleByName`/`DefaultByName` bypass `readByName`

`rule_default.go` repeats each family's scan column list three times: the
listing, the by-name read (a hand-rolled `queryRow` + `foundRow`), and the
SELECT. Every other family's by-name read goes through `readByName` with a
`scanX(owner, scan)` that the listing shares. That is the rule in
`~/go/gosmo/CLAUDE.md` § Conventions, Row iteration.

**Fix:** add `scanRule`/`scanDefault`, used by both `Rules`/`Defaults` (via
`scanRows`) and `RuleByName`/`DefaultByName` (via `readByName`). The behaviour
and errors stay the same; the existing `module_byname_test.go` and live
by-name tests pin it.

---

## Simplifications and optimisations

### S1 — both: lint drift since the last baseline

- `unparam`: `LogViewer.runTool`'s unused `bool` (folded into C1), and
  `cmd/amdemo`'s `canvasSize` always returning 150. That one is a demo; make
  it a constant or leave it.
- `ineffassign`: `~/go/gosmo/tsql/scan_test.go:123`, a dead `j := i` store in
  `legacySkipped`.

### S2 — gosmo: one builder for the two DDL-trigger scripts

`buildDatabaseTriggerScript` (`scripter_module.go`) and
`buildServerTriggerScript` (`scripter_server.go`) are line-for-line the same
except for the scope clause (`ON DATABASE` / `ON ALL SERVER`). The
deliberate-twin note in `server_trigger.go` covers the *scan* and the
Enable/Disable block, because unifying those would change two exported types.
These builders take plain fields and change no type.

**Fix:** `buildDDLTriggerScript(kind, name, scope string, def string, isCLR,
ansiNulls, quotedIdent, enabled bool, opts ScriptOptions)`, with both callers
one line. Keep the per-kind error text ("script database trigger" / "script
server trigger"). `scripter_module_clr_test.go` and the trigger script tests
pin the output.

### S3 — gosmo: server and database audit specifications share their write mechanics

`ServerAuditSpecification.SetAudit` (`audit_specification.go`) and
`DatabaseAuditSpecification.SetAudit` (`database_audit_specification.go`) are
identical but for the keyword and the exec target. The same holds for their
`withSpecificationDisabled` pair, which the database half's comment already
calls a twin.

**Fix:** an unexported `auditSpecScope{keyword string; exec func(ctx, q)
error; …}` that each type builds, with the shared bodies taking it. The
exported types and signatures stay the same. Their write tests pin the
statements.

**Optional:** do this only alongside another change in those files. It
removes about 60 lines and no bug.

---

## Architecture

### A1 — gossms: the `App` struct's 60-odd dialog fields (question for the author)

`App` holds one field per dialog (`app.go`), each built in `buildUI` and
appended by hand to `allDialogs` through `registerDialog`. A new dialog costs
a field, a construction line, a `registerDialog` and a `show*` method. It
works: `registerDialog` hands the dialog back, so forgetting the append is
visible. But the struct is now 103 fields, two thirds of them dialogs.

**Option:** a typed lazy registry, `dialogOf[T Dialog](a *App, build func(*App)
T) T`, keyed by type and constructed on first use. Its benefits:

- New dialogs need no field.
- Startup builds only what is opened.
- `allDialogs` becomes the registry's values.

Its costs:

- Draw order is today registration order, the tie-break `app.go` documents.
  It would become first-use order, so overlapping same-layer dialogs need an
  explicit layer.
- Every `a.xDialog` call site changes (hundreds).
- Tests that reach dialogs by field change too.

**Q1:** worth doing? The plan recommends **no** unless startup time or
memory is measured to matter. The gain is tidiness, and the churn touches
every dialog.

---

## Docs

### D1 — gossms: `docs/decisions.md` names rights by their pre-`gate` identifiers

§ Permission gating cites `rightAlterOnSchema`, `rightAlterAnyDBRole`/
`rightAlterAnyDBRoleMembers` and `rightAlterAnyServerRole`/
`rightAlterAnyServerRoleMembers`. Those now live in the `gate` package as
`gate.AlterOnSchema`, `gate.AlterAnyDBRole`, `gate.AlterAnyDBRoleMembers`,
`gate.AlterAnyServerRole` and `gate.AlterAnyServerRoleMembers`.
`conjoinedOpRights` and `objectOpRights` are still correct.

**Fix:** rename the five in place. The doc-identifier check that found them
is a shell loop over backticked names. It is cheap enough to rerun before
any release; no test is proposed (most hits are T-SQL and env names, not Go).

### D2 — gossms: the B1 comments

These are covered by B1, and listed here so the docs pass sees them.

---

## Release hygiene

### H1 — both: `golang.org/x/crypto` v0.58.0 carries GO-2026-5932

The advisory covers the unmaintained `openpgp` package. Neither repo imports
it (govulncheck: "doesn't appear to call"), and the fix exists only as a
pseudo-version (`v0.58.1-0.20261009181718-2f1f834330b2`).

**Fix:** bump both `go.mod`s when x/crypto tags a release past it. Don't pin a
pseudo-version for an unreachable package.

### H2 — gossms: New Schema and New Database Role are not in `CHANGELOG.md`

Commits 005f575 and 04f6ce8 added both. `CLAUDE.md` keeps feature work out of
`CHANGELOG.md`, so this is a release-time step, not part of any fix here.

---

## Recommended implementation order

Cheapest and most certain first, then the gosmo groundwork the larger gaps
need, then the gaps by user value. Each step builds, tests and is driven
before the next starts.

1. ✅ **DONE: S1 + D1**: lint drift and stale right names. Mechanical, about 10
   minutes, and resets the baseline. *Done 2026-11-03:* the five names
   renamed in `docs/decisions.md`; gosmo `tsql/scan_test.go`'s dead
   `j := i` became `var j int`; `cmd/amdemo`'s `canvasSize` left as is (the
   two widths are separate constants that happen to be equal).
2. ✅ **DONE: C2** (gosmo): `scanRule`/`scanDefault`. Small, and pinned by existing
   tests. *Done 2026-11-03:* both listings and both by-name reads share the
   scanners, the by-name reads go through `readByName`; messages unchanged.
   Offline suite `-race` green; `TestLiveVersionSweep`,
   `TestLiveTreeFamiliesReadTheirCatalog` and
   `TestLiveScriptedFamiliesRecreateTheirObjects` green on 17 and 13; a
   Name/Schema swap in `scanRule` fails the tree-families test.
3. ✅ **DONE: S2** (gosmo): the shared DDL-trigger script builder. Small, and
   pinned by the existing script tests. *Done 2026-11-03:*
   `buildDDLTriggerScript` takes an unexported `ddlTriggerScript` struct
   rather than the planned four positional bools, so an ANSI_NULLS/
   QUOTED_IDENTIFIER swap can't slip past the compiler.
   `buildDatabaseTriggerScript`/`buildServerTriggerScript` remain as one-line
   adapters, so their tests are unchanged. Output and error text are
   unchanged. Offline suite `-race` and `go vet -tags livedb` green; the
   three live trigger tests and `TestLiveScriptedFamiliesRecreateTheirObjects`
   green on 17. Mutation check: changing the server scope to `SERVER` fails
   the trigger tests.
4. ✅ **DONE: C1** (tuikit + gossms): `ToolRow.Run` and the four panels. One
   helper, four call sites; drive each panel's refused tool in tmux. *Done
   2026-11-03:* `Run(i, disabled, reason, status)` as planned. It names a cell
   by `runLabel`, which drops a trailing `...`/`…`/`▾`: the drive showed
   "Recycle...: Requires …". Each panel's `runTool` is one line, and
   `LogViewer.runTool` lost its `bool`. The tests that read it now assert the
   effect instead (dialog shown, statements run). Query Store's `runSel`/`runAct`
   are left as they are, and `docs/ui-rules.md` says why. The drive found two
   Activity Monitor bugs, both fixed and pinned by
   `TestActivityMonitorProcTabInstallRefusalNamesTheRightOnlyWhenDenied`:
   - Install/Update gated on the tab's own cloned connection. Nothing probes
     that connection, so the gate failed open and offered Install to every
     login. It now asks the panel's probed connection, as Kill does.
   - Install's reason named CONTROL SERVER even while greyed for a running
     read.

   Both mutation-checked, as is `Run`'s label prefix. Driven on 17 as a
   throwaway securityadmin login (dropped afterwards):
   - Activity Monitor: "Install in master: Requires CONTROL SERVER (sysadmin)."
   - Log Viewer: "Recycle: Requires membership of the sysadmin server role", on
     the cell and as the More-menu note.
   - XEvent: "Aggregation: group the events first".
   - Replication Monitor: selectors still act, and Refresh while busy stays
     silent.
5. ✅ **DONE: G1 gosmo half**: the `ApplicationRole` family, offline and live
   tests, and the version sweep on 13. *Done 2026-11-03:* new
   `application_role.go`, as planned, with one shared select
   (`applicationRoleSelect`/`scanApplicationRole`). `CreateApplicationRole`
   and `ChangePassword` go through `execPasswords` and refuse an empty
   password (`ErrInvalidRequest`). Both have rows in
   `secret_redaction_test.go`. `ScriptDatabaseSecurity` now reads the public
   `ApplicationRoles`, and its private type is gone. Also added `ALTER ANY
   APPLICATION ROLE` to `ProbedDatabasePermissions` for step 7's gate.
   `TestLiveEveryProbedPermissionNameIsOneTheServerDefines` passes on 13
   and 17, but the gate's semantics are still step 7's probe. Offline suite
   `-race`, `go vet` and `go vet -tags livedb` are green. Every statement
   case was mutation-checked. Live on 13 and 17, both green:
   - `TestLiveApplicationRoleRoundTrip` (new): create, list, rename, schema,
     password, drop. It proves the password change by `sp_setapprole`
     taking the new password and refusing the old one, and was
     mutation-checked.
   - `TestLiveCreateReturnsTheCreatedObject`.
   - `TestLiveScriptDatabaseSecurityRoundTrips`.
   - The version sweep: 406 calls / 0 failed on 13, 462 / 0 on 17.

   `sp_setapprole` runs only at the ad hoc level (Msg 15422), so it cannot
   be called with bound parameters.
6. ✅ **DONE: B1**: application roles into both `existingNames`. It depends on
   step 5. *Done 2026-11-03:* both prefetches read `Database.ApplicationRoles`.
   A failure there is fatal, as it is for `Users`/`Roles`, since all three
   read the same view. `newRolePrefetchFrom` takes the application roles,
   which go into the name set only: no picker offers them. Both comments now
   name all three kinds.
   - Tests: `TestNewRolePreflight` refuses an application-role name, and the
     new `TestNewUserPrefetchHoldsEveryPrincipalName` drives
     `fetchNewUserPrefetch` over the fake connection. Dropping either loop
     fails its test.
   - Driven on 17 against a throwaway `gossms_b1` holding application role
     `b1_app` (dropped afterwards). New Database Role refused `B1_APP` inline
     (CI collation): "a user or role named "B1_APP" already exists in
     gossms_b1". New User refused `b1_app` the same way.
7. ✅ **DONE: G1 gossms half**: the Application Roles folder, New Application Role and
   Properties, with the gate probe live on 13 and 17. *Done 2026-11-03:*
   Security ▸ Roles is now a folder of two, Database Roles (the old
   `NodeDatabaseRoles`, relabelled) and Application Roles (`NodeRoles`,
   `NodeApplicationRoles`, `NodeApplicationRole`). New files:
   `new_application_role_dialog.go` (General with name, default schema,
   password and confirm; Owned Schemas; Securables; Extended Properties) and
   `application_role_props.go` (the same pages; General renames last, and a
   blank password keeps the current one). The leaf has Rename and Delete, and
   Properties opens with `showReloading`.
   - **Gate:** `gate.AlterAnyApplicationRole`. There is no CREATE
     APPLICATION ROLE permission. Probed with WITHOUT LOGIN users on 13 and
     17, with identical results: ALTER ANY APPLICATION ROLE,
     db_securityadmin, db_owner, and ALTER or CONTROL on the database all
     allow create, rename, schema, password and drop. ALTER ANY ROLE,
     db_accessadmin and db_ddladmin are refused (Msg 15247/15151). A DENY
     ALTER on one role refuses only DEFAULT_SCHEMA, so there is no class-4
     twin, the same as for `AlterAnyDBRole`.
   - **Tests:** new `new_application_role_dialog_test.go` and
     `application_role_props_test.go`. Six mutations each fail a test:
     password write dropped, app-role names dropped from the name set, the
     menu gate's right swapped, the Delete gate's right swapped, the schema
     clause dropped, the confirm check dropped.
   - **Driven on 17** against a throwaway `gossms_g1` (dropped afterwards):
     - The inline refusal of `EXISTING_APP` (CI collation).
     - Created `pay]roll` with schema, an owned schema and an extended
       property; the USER level works for an application role.
       `sp_setapprole` took the password.
     - Script Changes shows the password placeholder.
     - Properties renamed, re-schemaed and re-passworded in one Apply; the
       folder reloaded, the new password works and the old one is refused.
     - Delete while owning a schema gets the server's Msg 15138, and
       succeeds after the transfer.
     - As a throwaway db_ddladmin login, New Application Role shows "needs
       ALTER ANY APPLICATION ROLE".
   - **Driven on 13:** create, then all four Properties pages load.
   - **Left over:**
     - No Script menu on the leaf. gosmo has no `ScriptApplicationRole`;
       Script Database Security emits the one-role form privately.
     - The owner pickers never offer an application role, though one can
       own a schema: Owned Schemas' transfer picker, New Schema, and Schema
       Properties all use `principalNames(users, roles)`.
     - Database Role Properties renames without `showReloading`, so the tree
       keeps the old label. It is missing from
       `TestPropertiesApplyReloadsTheFolderListingTheObject`, a ui-rules
       miss that predates this step.
     - A login with no right on an application role doesn't see it listed
       (catalog visibility), so the folder reads empty for it.
8. **G2**: New Sequence and Sequence Properties, plus a gosmo
   `Sequence.Alter` if needed (gosmo first).
9. **G3**: New Synonym. Small once G2's dialog pattern is fresh.
10. **G4**: the Query Wait Statistics drill-down.
11. **S3** (gosmo): only if steps 1–10 touched the audit-specification files
    anyway; otherwise carry it to `~/go/gosmo/OPEN-THREADS.md`.
12. **H1**: when x/crypto tags. **H2**: at the next release.
13. **Q1/A1**: only on the author's say-so.

When done: delete this file. Move anything unfinished to
`docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
