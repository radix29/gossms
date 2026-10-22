# Review plan — 2026-10-22 (third pass)

The fix list from a third whole-repo review of gossms and gosmo, the same day
the second plan (also dated 2026-10-22) was implemented and retired. Everything
that plan listed is done except its H1 (the release), which is carried over
below. Nothing settled in `docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md`
or either `CLAUDE.md` is re-raised.

**Baseline (`HEAD` f3ff81b / gosmo c4fc44f):** both repos build; `go test
./...` passes in both; `go vet` and gosmo's `go vet -tags livedb` pass;
`staticcheck` (both, and gosmo with `-tags livedb`) reports nothing;
`go test -race ./...` passes in both. `deadcode ./cmd/gossms` reports the six
known hits, all settled or deliberate. `dupl -t 120` finds eight small clone
pairs, all parallel families (dark/light palettes, the two DDL-trigger
scripters, the pool/group stats readers, the stoplist/property-list pages);
none has diverged into a bug and none is worth a shared helper.

So this pass found **one shipped bug** (B1, gosmo, real servers only), two
stale documentation statements, one simplification, one operational
optimisation, one hardening item, and one API restructuring proposal that is
allowed to break gosmo.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
`TestNoDanglingDocReference` checks every path named here, so cite only paths
that exist.

Every gosmo change follows `~/go/gosmo/CLAUDE.md`: build and test in gosmo
first; every write keeps an offline test of its exact statement
(`script_*_write_test.go`, mutation-checked); `go vet -tags livedb ./...`
after any rename; `TestLiveVersionSweep` on win10cli\SQL2016 after any query
change.

IDs: **B** bug, **C** consistency/docs, **S** simplification, **O**
optimisation, **H** hardening/hygiene, **A** architecture.

---

## Bugs

### B1 — gosmo: exclusive database operations fail when the connection's initial database is the target

**Where:** `Database.Drop` (`database.go`), `Database.Detach`
(`database_attach.go`), `Database.Rename` (`database.go`), `Server.Restore`
(`restore.go`), `Server.RestoreFromSnapshot` (`database_snapshot.go`); to
confirm live: `Database.SetOffline`, `Database.SetReadOnly` and the
`NeedsExclusiveAccess` options of `Database.SetOption`
(`database_settings.go`, `database_options.go`).

Every one of these runs through `Server.exec` on a pooled connection. A pooled
connection is reset on checkout to the DSN's `database`, which is
`ConnectionOptions.Database` (`connection_dsn.go`'s `commonDSNValues`; master
only when it is empty). gossms passes the Connect dialog's "Connect to
database" straight through for Object Explorer (`db.Connect` →
`toGosmoOptions`), and saved connections commonly carry one.

So with a saved connection whose database is `Sales`, Object Explorer ▸
`Sales` ▸ Delete / Detach / Restore / Rename runs the statement from a session
that is *in* `Sales`:

- `DROP DATABASE [Sales]` → Msg 3702, "currently in use";
- `RESTORE DATABASE [Sales] …` → Msg 3102, "in use by this session. It is
  recommended that the master database be used";
- `sp_detach_db` → Msg 3703.

`releaseIdle` does not help: it closes idle connections, and the statement's
own connection (fresh or reset) lands back in `Sales`.
`killDatabaseSessionsBatch` deliberately skips `@@SPID`, and SET SINGLE_USER
makes the executing session the single user. The forced paths therefore fail
the same way, after briefly holding the database single-user.

The scripts these writes capture under `WithScript` (each dialog's Script
action) have the same gap. SSMS writes `USE [master]` ahead of each of these
statements, as gosmo's own `ScriptDatabase` DROP does (`scripter.go`).

**Fix (gosmo):** one helper, e.g. `s.execFromMaster(ctx, stmt)`, that sends
`"USE [master];\n" + stmt` in the same batch. Use it for every statement
above, including the `exclusiveBatch`, `renameExclusiveBatch`, restore and
kill batches.

- The batch-level `USE` lasts only until the pool resets the connection on
  its next checkout, so nothing else is affected.
- `permission_options.go` already does this for server-scope GRANT/DENY, and
  `docs/decisions.md` (§ By design, "`USE master;` prefix doesn't strand the
  pooled connection") settled that the pool copes.
- A contained-database user can't `USE master`, but can't run any of these
  statements either, so they lose nothing.
- Under `WithScript` the captured text then starts with `USE [master];`,
  which is what makes it runnable from a query window sitting in the database.

**Tests:**

- Offline: update the expected statements in the `script_*_write_test.go` and
  `drop_rename_test.go` cases. Mutation-check by dropping the prefix.
- Live, on 13 and 17, against a throwaway database: connect with
  `ConnectionOptions.Database` set to it, then Drop, Detach + attach back,
  Rename and back, and Restore over it from a fresh backup, each with and
  without `TerminationRollbackImmediate` / `CloseExistingConnections`. Then
  try `SetOffline` and `SetReadOnly` the same way. Add the prefix wherever
  they fail too, and record the ones that don't need it in the helper's
  comment.
- gossms: drive the binary in tmux with a saved connection whose database is
  a throwaway, and run Delete and Restore on that database.

---

## Consistency and docs

### C1 — gossms: `peerFields.creds`' comment contradicts `peerOptions`

`internal/db/peer.go`: the field's comment says "nil means every peer uses
this connection's settings". `peerOptions` does the opposite: with no
resolver, every peer is refused with `PeerNotRegisteredError`. That is the
deliberate rule, since retrying with the parent's login could disclose
credentials to another server (the comment above `peerOptions` says so).
Reword the field comment to "nil refuses every peer (PeerNotRegisteredError)".

### C2 — gossms: `ARCHITECTURE.md` § Dependencies credits go-mssqldb with GO splitting

The go-mssqldb row reads "plus `batch` for `GO` splitting — `internal/query`".
Nothing imports `go-mssqldb/batch`. GO splitting is
`sqltext.SplitBatches`, as § How a query runs already says. Rewrite the row:
the driver itself, imported by `internal/query` for its types
(`NullUniqueIdentifier`, `mssql.Error`) and by gosmo.

---

## Simplifications

### S1 — tuikit: DataGrid's data-row height and vertical-bar test are written out at every use

`g.rect.H - 3` appears eight times across `datagrid.go`, `datagrid_draw.go`
and `datagrid_input.go`. The "vertical bar shown" predicate (`rows.Len() >
dataH && dataH > 0`) is written out twice: in `Draw`, and in `hScrollbar`,
where 515aba3 had to copy it to fix the last-column bug. The next layout
change (a second header row, a footer) has to find every copy.

**Fix:** two methods, `dataRows() int` and `showsVScrollbar() bool`, used at
every site. Behaviour-neutral, and the existing scrollbar tests pin it. Run
one tmux pass over a wide, tall result grid.

---

## Optimisations

### O1 — pooled connections stay in the last database they read, holding it open for other people

`Database.query`/`queryRow` send `USE [db]; …` on a pooled connection
(`database.go`'s `useBatch`). The connection goes back to the pool still in
`db`: the reset happens only on the next checkout. An idle connection sits
there for up to `ConnMaxIdleTime` (gosmo's default is 5 minutes), and gossms
keeps up to 10 of them (`internal/db/connection.go`). Each one holds a shared
database lock.

`ReleaseIdleConnections` covers gossms's *own* exclusive writes. It does not
help anyone else. A colleague's `RESTORE`, `SET OFFLINE` (without ROLLBACK)
or `SET SINGLE_USER` on a database a gossms user merely expanded fails with
"Exclusive access could not be obtained" for up to five minutes. An
`AUTO_CLOSE` database never closes while gossms is browsing it.

**Options, in order of cost:**

1. **gossms:** set `ConnMaxIdleTime` to about 60 s in `toGosmoOptions`. That
   cuts the window five-fold. The cost is a re-login after a minute idle,
   which the Object Explorer already pays at first expand. Measure a folder
   expand after 2 minutes idle, before and after.
2. **gosmo (investigate first):** run database-scoped reads through
   `EXEC [db].sys.sp_executesql` so the session never leaves its initial
   database. That removes the camping entirely, and with it most of
   `releaseIdle`'s reason to exist. It has to be proved against three things
   `~/go/gosmo/CLAUDE.md` records:
   - the Msg 924 compile-time resolution of a literal `[db].sys.sp_executesql`
     (`perDatabaseExec`);
   - the plan-cache database-id trap ("Read another database's catalog under
     `USE [db]`");
   - the parameter-collation rule `useBatch` depends on.

   Write it up as an open thread rather than doing it blind.

Recommend 1 now and 2 only as a write-up.

---

## Hardening and hygiene

### H1 — carried over: release state

gosmo `HEAD` is 12 commits past `v0.1.0`, gossms 22. The breaking changes and
the `TableKind` renumbering listed in the previous plan's H1 still have no
`CHANGELOG.md` entry. **Only when a release is asked for** (`CLAUDE.md`):

1. Write the gosmo entry with a Migrating note per break.
2. Tag and push gosmo.
3. Bump gossms's `require`, comment out `replace`, and tag gossms.

A2 below adds breaks of its own, so if both are wanted, do A2 before the tag.

### H2 — gossms: passwords sealed under a legacy binding stay that way until the next Save

`internal/config/secret.go` still opens three older formats: unprefixed (no
AAD), `v2:` and `v3:`. Each is bound to less of the connection than `v4:`.
`Save` re-seals them, but a config that is loaded and never saved keeps them
indefinitely. An unprefixed ciphertext is exactly the transplantable blob the
AAD exists to prevent: write access to `config.json` can copy it onto an entry
pointing elsewhere.

**Fix:** when `Load` opened any password through a legacy path, queue one
`saveConfig` once the UI is up. That rewrites everything under `v4:`. Leave the
readers themselves in place; a downgrade still needs them. Test: a fixture
config with one entry in each format comes back all `v4:` after Load plus one
save.

---

## Architecture and refactoring

### A1 — not proposed: splitting gosmo into sub-packages

Considered, rejected. gosmo is one package of 158 non-test files (59 k lines).
Families such as `agent/`, `mail/` and `xevents/` would need `exec`, `query`,
`Scripting`, `createdObject` and the retry/observer plumbing exported through
an `internal/` package. Every family's tests would also have to move away from
the shared fakes. A2 gets the discoverability benefit without that. Recorded
here so the next review doesn't propose it again; if A2 is done, add the
reason to `docs/decisions.md` § gosmo API shape.

### A2 — gosmo: feature facets on `Server` and `Database` (breaking)

`go doc` counts **254 methods on `Database` and 205 on `Server`**. Whole
features live there behind a shared name prefix:

| Feature | Today | Count |
|---|---|---|
| Query Store | `d.QueryStore(ctx)`, `d.QueryStoreTopResourceQueries`, `…ForcePlan`, `…WaitCategories`, `…Metrics`, … | 16 |
| Database Mail | `s.MailAccounts`, `s.MailProfiles`, `s.MailItems`, `s.MailStatus`, … plus writes | 14 reads |
| Extended Events | `s.XEPackages`/`XEObjects`/`XEMapValues`/`XEObjectColumns` beside `s.EventSessions`/`EventSessionByName`/`EventFiles` | two prefixes for one feature |

**Proposal:** a value handle per feature, such as `d.QueryStore()`
(no I/O; this frees the name, so today's `QueryStore(ctx)` becomes
`.Options(ctx)`). The methods lose their prefix: `.TopResourceQueries(ctx,
opts)`, `.ForcePlan(ctx, q, p)`, `.Plans(ctx, id, opts)`. Then `s.Mail()`, and
`s.XEvents()` with both prefixes unified under it.

The handle carries only the parent pointer, so nothing about lookups, `Ref`
semantics or `WithScript` changes. Types such as `QSQueryStat` keep their
names.

**Scope it as a pilot:** Query Store first. It is self-contained (one gossms
area, `query_store_*`, about 47 call sites, plus about 96 references inside
gosmo). Decide Mail and XEvents on how the pilot reads. Agent (`Jobs`,
`Alerts`, `Operators`, `Schedules`) already reads well unprefixed; leave it.

Also, while Query Store is open: `QueryStoreWaitingQueries` takes
`category string`. Make it a typed constant set like the other R2 enums.

**Checks:**

- `go vet -tags livedb` in gosmo;
- update gosmo `ARCHITECTURE.md`'s feature-map rows;
- one tmux pass over the Query Store panel;
- record the facet rule in gosmo `CLAUDE.md` § Conventions, so new features
  follow it.

---

## Verification baseline

| Check | gossms | gosmo |
|---|---|---|
| `go build ./...` | clean | clean |
| `go test ./...` | pass | pass |
| `go vet ./...` | clean | clean (also `-tags livedb`) |
| `staticcheck ./...` | clean | clean (also `-tags livedb`) |
| `deadcode ./cmd/gossms` | 6 hits, all deliberate | — |
| `dupl -t 120` | 8 small parallel pairs | 6 small parallel pairs |
| `go test -race ./...` | pass | pass |
| coverage (`go test -cover`) | `internal/tui` 69.7 %, others 72–97 % | 68.6 % (live tests excluded) |

---

## Recommended implementation order

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | **B1** `USE [master]` for exclusive database operations | gosmo → gossms | The only shipped bug. Offline statement tests, then the live run on 13 and 17, then one tmux pass. |
| 2 | **C1, C2** | gossms | Comment and doc lines; minutes. Do them alongside 1. |
| 3 | **S1** DataGrid helpers | gossms (tuikit) | Behaviour-neutral, pinned by existing tests; one tmux look at a wide grid. |
| 4 | **H2** re-seal legacy passwords on load | gossms | Small, self-contained, offline-testable. |
| 5 | **O1** option 1 (`ConnMaxIdleTime` ≈ 60 s) | gossms | Measure the idle-expand cost first; record option 2 as an open thread instead of doing it. |
| 6 | **A2** Query Store facet pilot | gosmo → gossms | Breaking; do it in one sitting with `replace` active, and only if the pilot reads better, extend to Mail/XEvents. |
| 7 | **A1** record the rejection | gossms docs | With A2, or now if A2 is declined. |
| 8 | **H1** changelog, tags | gosmo → gossms | Only when a release is asked for, and after A2 if A2 is wanted. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet in gosmo after any rename);
- the checks named in the item have run;
- for a user-visible change, the built binary has been driven in tmux, per
  `docs/testing.md`.
