# Review plan — 2026-11-12

Whole-tree review of gossms (`HEAD` b019e40, branch `fix/review_20261110`) and
gosmo (`HEAD` 380b061, 5 commits past `v0.2.0`). Plan only: nothing below is
implemented. Breaking gosmo's API is allowed for this round (author waiver,
like the 2026-09-22 one); every break lands with its gossms caller update in
the same change.

Delete this file once every item is closed or moved to `docs/open-threads.md`.

## What was run, and what it found

| Check | gossms | gosmo |
|---|---|---|
| `go build`, `go test ./...` | clean | clean |
| `go vet` (also `GOOS=windows`, `GOOS=darwin`) | clean | clean, `-tags livedb` too |
| `staticcheck ./...` | clean | clean |
| `deadcode -test ./...` | clean | 63 offline-unreached funcs; 9 with `-tags livedb` (T1) |
| `go test -race -count=1` | `./internal/...` clean | `./...` clean |
| `dupl -t 70` | 56 small clone groups, none over ~20 lines (S3) | 8 groups, all in scripters |
| Safety classifier probed with 70 edge cases | every surprising answer is documented in `docs/cli.md` § Safety | — |

Also read: the threading model, `postAndWake`/`latest`, config save and merge,
`query.Execute`, gosmo's retry, `AcquireConn`, `withConn`/`withSessionIn`,
every hand-quoted `'%s'`/`[%s]` T-SQL format string in both repos. All of those
held up (no finding).

**Coverage is sampled, not exhaustive.** 168k + 70k non-test lines were
searched by pattern (duplicated helpers across packages, rule violations from
both `CLAUDE.md`s, catalog-name comparisons, quoting) and read in depth where
the risk is highest. Dialog-by-dialog behaviour was not driven in tmux.
Settled questions in `docs/decisions.md` and `~/go/gosmo/OPEN-THREADS.md` were
not re-raised (e.g. splitting `internal/tui` or gosmo into packages, Agent
staying unprefixed, the `Ref` naming, `ScriptTable`'s round trips).

**Not run, for the author:** `govulncheck ./...` in both repos (it queries
vuln.go.dev, so it was left to you).

Severity: **M** medium (wrong behaviour a user can hit), **L** low
(inconsistency, latent, or hint text). Status: **verified** = traced in source;
**plausible** = needs a live check before fixing.

---

## Bugs and inconsistencies

### B1 — TUI and CLI disagree on when a connection is missing its password (M, verified)

**Done (2026-11-12).** `config.PasswordRequired(m)` reads a `passwordRequired`
column of `authMethods` (kept out of `AuthFields`, which is the dialog's
greyed-field set); `Connection.NeedsPassword()` = required and empty. Both
`missingPassword`s use it (the TUI helper is gone). Tests: `TestNeedsPassword`,
and the Windows-with-user row in `TestPeerCredentialsSkipASavedEntryWithNoPassword`.
Not driven live: the estate has no Kerberos sign-in to save.

- `internal/tui/app_peer_creds.go:141` `missingPassword` uses
  `config.FieldsFor(m).Password` and so treats **Windows Authentication with a
  user and no password** as certain to fail.
- `internal/cli/connect/connect.go:376` `missingPassword` lists SQL Server,
  Entra Password and Entra Service Principal by hand, so it **accepts** that
  case.
- gosmo agrees with the CLI: `connection_dsn.go` (the `AuthWindows` arm) sends
  a bare user for Kerberos keytab / credential-cache sign-in, which needs no
  password.

Effect: `loadPeerCredentials` never seeds a saved Windows-with-user connection,
so Peer (AG replicas, endpoints) offers the Connect prompt for an instance the
saved entry could reach. And a password method added to `authMethods` later
reaches the CLI's hand list only if someone remembers it.

Fix: one rule in `config`, e.g. `func (c Connection) NeedsPassword() bool`,
true only for methods where a password is the credential itself (SQL Server,
Entra Password, Entra Service Principal). Add it as a column of the
`authMethods` table (`AuthFields` gains `PasswordRequired`), rather than a
second list. Both `missingPassword`s call it; the CLI keeps its message
building. Tests: a table test over `AllAuthMethods()` × user set/unset; the
peer-seeding test gains the Windows-with-user row.

### B2 — `jobIsRunning` exists twice, verbatim (L, verified)

**Done (2026-11-12).** gosmo `JobState.Running`; both copies deleted, the
`docs/decisions.md` bullet names it.

`internal/tui/agent_menu.go:159` and `internal/cli/cmd/job.go:179` are the same
function; the CLI copy's comment says "the TUI's rule". The rule is about
gosmo's own `JobState` encoding (`docs/decisions.md` § gosmo: deliberate API
decisions).

Fix (gosmo, additive): `func (s JobState) Running() (running, known bool)`
beside `JobState`, with the doc comment from `agent_menu.go`. Delete both
copies. Update the `jobIsRunning` bullet in `docs/decisions.md` to name the
gosmo method.

### B3 — `permissionName` exists twice and the copies differ (L, verified)

**Done (2026-11-12).** gosmo `SecurableClass.Permission`; both copies deleted
(the TUI now types a server-scoped name `ServerPermission`).

- `internal/tui/perm_state.go:127`: database → `DatabasePermission`, anything
  else → `ObjectPermission` (including `SecurableServer`).
- `internal/cli/cmd/security_permissions.go:281`: adds the
  `SecurableServer` → `ServerPermission` case.

Harmless today only because `ApplyPermission` checks the name against the
class's allowlist and ignores the Go type. The TUI copy is the latent bug.

Fix (gosmo, additive): `func (c SecurableClass) Permission(name string)
PermissionName` in `permission_options.go`, next to `PermissionNames()`.
Delete both copies. Offline test: every class's `PermissionNames()` round-trips
through `Permission` to the scope's type.

### B4 — gosmo: columnstore ORDER validation mixes exact and case-blind matching (L, verified)

**Done (2026-11-12).** The key-column check is `==`; the doc comment says
both checks are exact. Test row "NCCI ORDER column cased unlike its key
column"; the version-gate test's NCCI no longer relies on a cased match.

`~/go/gosmo/index_create.go` (`validateColumnstoreOrder` area, ~line 433–437):
the duplicate check uses `slices.Contains` (exact) while the "is one of the
index's columns" check uses `strings.EqualFold`. In a `_CS_` database an ORDER
column `A` passes against key column `a`, and the server refuses later with a
less useful error. Breaks gosmo's "a name from the catalog is matched exactly"
rule.

Fix: compare with `==` (the request's names are the caller's spelling of the
same request, not catalog rows). Add the `_CS_` case to the request-validation
test.

### B5 — gosmo: connection retargeting compares database names case-blind (L, plausible)

`~/go/gosmo/connection_retarget.go:89, 157, 200` use `strings.EqualFold` on
database names. Database names follow the **server** collation; on a
case-sensitive server `Sales` and `sales` are two databases, and `retarget`
would fire for the wrong one (dropped or renamed database → pool re-pointed).

Fix: exact comparison, or a `NameKey` under the server collation if gosmo
already holds it (`collation.go`). Needs a case-sensitive *server* to confirm;
none is in the estate (`docs/open-threads.md` § Format Document: what is left
names the same gap). If it can't be run, fix by reasoning and pin with an
offline test; record the unrun live check in `docs/open-threads.md`.

### B6 — CLI hints hand-bracket an event-session name (L, verified)

**Done (2026-11-12).** Both hints use `tsql.QuoteName`.
`TestXEventCaptureDropHintQuotesTheName` (the fake's `applyWrite` now
un-doubles `]]`). Moot in practice — the name is generated — but correct.

`internal/cli/cmd/xevent_capture.go:430, 444` print
`DROP EVENT SESSION [%s] ON SERVER` with the raw name. A name containing `]`
gives a hint that doesn't parse. Use `tsql.QuoteName(name)` (as
`internal/showplan/missing_index.go` already does for its database).

### B7 — Two `isSystemSchema` functions with different meanings (L, verified)

**Done (2026-11-12).** gosmo `Schema.IsSystem`; the CLI calls it, the TUI's
name-based predicate is `isFixedSchema` (comment and tests unchanged but for
the name).

- `internal/tui/system_principals.go:62` — by name: dbo, guest, sys,
  INFORMATION_SCHEMA (the schemas that can't be dropped or re-owned).
- `internal/cli/cmd/object.go:198` — by id: guest/INFORMATION_SCHEMA/sys plus
  the fixed roles' schemas, **not** dbo (the schemas SQL Server created).

Both are correct for their caller; the shared name invites the next caller to
pick the wrong one. gosmo has `IsSystem()` on `Database`, `Login`, `Job`,
`ResourcePool`, `WorkloadGroup` but not on `Schema`.

Fix: gosmo `func (s *Schema) IsSystem() bool` (the CLI's id rule — a
derivation, allowed by gosmo's field-vs-method rule). The CLI calls it; the TUI
function is renamed `isFixedSchema` (or similar) with its comment unchanged.

---

## gosmo API consistency (breaking allowed)

### A1 — Service Broker reads are half-prefixed (L, verified)

On `*Database`: `BrokerQueues`/`BrokerQueueByName`/`BrokerQueueRef`,
`BrokerServices…`, `BrokerPriorities…` carry the prefix; `MessageTypes`,
`Contracts` (returning `ServiceContract`), `Routes`, `RemoteServiceBindings`
don't. Under gosmo's own facet rule (`~/go/gosmo/CLAUDE.md` § Conventions, "a
whole feature behind a shared name prefix is a facet") this is one feature.

Proposal: `d.ServiceBroker()` facet with `Queues`, `QueueByName`, `QueueRef`,
`Services…`, `Priorities…`, `MessageTypes…`, `Contracts…`, `Routes…`,
`RemoteServiceBindings…`; result types keep their names. Mechanics, per the
existing facets: facet type holds the `*Database`, exposes `Database()`
(`parent_accessor_wiring_test.go`), is pointed at by `reflectSweep` in
`live_versionsweep_test.go`, its writes go in `sweepSkip`. Then run
`TestLiveVersionSweep` on 13. gossms callers: the `explorer_service_broker.go`,
`detail_browser_*`, broker props/new dialogs and `internal/cli/cmd` object
listing — compile errors will find them all.

### A2 — Other prefix families: decide per family (author decision)

Same rule, weaker cases — list only, decide before A1 lands so all renames
ship in one gosmo tag:

- Full-text on `*Database`: `FullTextCatalogs`, `FullTextStoplists`,
  `FullTextIndexes`, `SearchPropertyLists` (+ByName/Ref), and on `*Server`
  `FullTextInfo` → `d.FullText()` / `s.FullText()`.
- Resource Governor on `*Server`: `ResourcePools…`, `ResourcePoolStats`,
  `ResourceGovernorStatus`, `ExternalResourcePools…`. Note `ResourceGovernor`
  and `ResourceGovernorRef` already exist as the singleton read/handle, so a
  facet would need another name — probably leave as is.
- Backup on `*Server` (`Backup`, `BackupHeaders`, `BackupFileList`,
  `BackupHistory`, `BackupDevices…`): devices are an object family, the rest
  are operations — probably leave as is.

Recommendation: do A1; do Full-text only if the facet reads naturally at the
gossms call sites; leave RG and Backup.

---

## Simplifications and optimisations

### S1 — `query.Execute`'s `database` argument has no production user (L, verified)

The only non-test caller, `internal/tui/activity_monitor_proctab.go:427`,
passes `""`. The switched-database branch (`AcquireConn` with a name, then
always `discardConn`) exists for tests only. Either drop the parameter
(and its tests) or, if a caller is planned, switch back to the start database
before releasing — as gosmo's `withSessionIn` does — instead of discarding the
pooled connection, which costs a fresh login per run.

### S2 — Size formatting is spread over a dozen helpers (L)

`formatMB` (`detail_browser_databases.go`), `shrinkMB`, `fileSizeText`/
`maxSizeText` (`database_props_files.go`), `formatBytes` (`backup_common.go`),
`auditFileSizeText`, `governedMB`, `instanceOptionalMB`, `sysInfoMB`,
`snapshotFileSize*`, plus the CLI's `maxSizeText`. Several differ on purpose
(SSMS's own formats; `fileSizeText`'s comment names a shipped bug). One visible
inconsistency: `auditFileSizeText` prints `10240 MB`, the others
`10,240 MB` via `core.FormatThousands`.

Fix: make `auditFileSizeText` use `formatMB`; otherwise leave them where they
are — consolidating into one file would trade local "why" comments for
indirection. Not worth a new file.

### S3 — Small clones, fix only when touching the file (L)

From `dupl -t 70`: `alwayson_menu.go` (Remove Database / Remove Replica confirm
+ `runAGOperation`, 262–282 vs 536–556); `explorer_database_security.go` (five
7-line folder loaders, 309–439); `explorer_programmability.go` (four);
`fulltext_props.go` 400 vs 643; `new_fulltext_property_list_dialog.go` vs
`new_fulltext_stoplist_dialog.go`; `new_sequence_dialog.go` vs
`new_synonym_dialog.go`; gosmo `scripter_programmability.go` (four). None is
large enough to justify a change on its own.

### S4 — Files at the split threshold (L)

`internal/tui/explorer_object_ops.go` (891), `internal/cli/cmd/security_permissions.go`
(885), gosmo `scripter.go` (885). B3 shrinks `security_permissions.go`; split
the others only if a change here lands in them (`CLAUDE.md` § Coding
conventions; a new `internal/tui` file needs its `ARCHITECTURE.md` § Package
map row).

---

## Tests

### T1 — gosmo exported functions no test reaches (L, verified)

`deadcode -test -tags livedb ./...` in gosmo lists functions neither offline
nor live tests call:

- `Scripter.ScriptDatabaseObjects`, `Scripter.ScriptDatabaseAuditSpecification`,
  `Scripter.ScriptDatabaseScopedCredential`
- `ServerScripter.ScriptServerRole`, `ServerScripter.ScriptBackupDevice`
- accessors `Scripter.Database`, `ServerScripter.Server`; `QSMetricUnit`;
  `version.Runtime`

Offline-only (`deadcode -test` without the tag) a further ~54 `Script*`
wrappers are reached only by live tests. Two carry logic of their own and
deserve an offline test: `schedulerGroupSizes`
(`scripter_resource_governor.go`, maps Msg 297/300 to `ErrUnsupported`) and
`Server.clrServerTrigger` (`scripter_module_clr.go`).

Fix: offline tests with the row-serving fakes for the five scripters and the
two logic helpers; the accessors join `parent_accessor_wiring_test.go` if they
aren't there by design. Library surface — **add tests, don't delete**.

---

## Recommended implementation order

Each step builds, tests and lints clean on its own (`CLAUDE.md` § Build &
verify; `docs/testing.md` before calling it done).

1. ✅ **B1** (done 2026-11-12) — `config` `NeedsPassword` + both callers. gossms only; the one
   user-visible fix. Drive it: a saved Windows-with-user connection seeds Peer.
2. ✅ **B2, B3, B7 (gosmo half)** (done 2026-11-12) — one additive gosmo change: `JobState.Running`,
   `SecurableClass.Permission`, `Schema.IsSystem`. Build and test in gosmo
   first.
3. ✅ **B2, B3, B7 (gossms half)** (done 2026-11-12) — delete the copies, call gosmo; rename the
   TUI's `isSystemSchema`; update `docs/decisions.md`'s `jobIsRunning` bullet.
4. ✅ **B4, B6** (done 2026-11-12) — two one-line fixes with their test rows.
5. **B5** — fix by reasoning with an offline test; note the unrun CS-server
   check in `docs/open-threads.md`.
6. **T1** — gosmo tests for the never-reached scripters and the two helpers.
7. **S1, S2 (audit size only)** — small gossms cleanups.
8. **A2 decision, then A1** — the breaking gosmo rename(s) with every gossms
   caller in the same pair of changes; `go vet -tags livedb ./...` in gosmo
   (renames break live tests silently), then `TestLiveVersionSweep` on 13.
9. **Release prep** — gosmo is 5 commits past `v0.2.0` and gossms `HEAD`
   needs them (`replace` active); after A1 the next gosmo tag is a breaking
   one (`v0.3.0`). Tag gosmo, then gossms per `docs/decisions.md` § Release
   workflow.
10. **S3, S4** — opportunistic, only alongside other work in those files.

Not scheduled: run `govulncheck ./...` in both repos before the release
(step 9).
