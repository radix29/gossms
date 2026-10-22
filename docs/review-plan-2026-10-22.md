# Review plan — 2026-10-22

The fix list from a second whole-repo review of gossms and gosmo, the day after
the 2026-10-21 plan was implemented and retired. Everything that plan listed is
done or deliberately parked (R4 lazy dialogs, R5 frontend-neutral layer), and
none of it is re-raised. Nothing settled in `docs/decisions.md`,
`~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is re-raised either. Two
candidates were dropped on checking: the `EqualFold` key-column match in
gosmo's `validateColumnstoreOrder`, which its doc comment documents as
deliberate, and `NarrowToDMLStatement`, which `deadcode` reports but which is
the test oracle its doc comment says it is.

**Baseline (2026-10-22, `HEAD` 4700ca4 / gosmo 8caff56):** both repos build;
`go test ./...` passes in both; `go vet` and `go vet -tags livedb` pass;
`staticcheck -checks all` reports nothing. `deadcode` reports six hits, all
settled or deliberate. `dupl` at 80 to 120 tokens finds only small clones of
5 to 20 lines, none diverged into a bug. `go test -race ./...` passes in both
with no data race reported.

So this pass found **no shipped user-visible bug**. What it did find: one
latent hazard against real servers, a set of naming and API inconsistencies
(some breaking, which is allowed this pass), stale documentation left behind
by yesterday's R3, and a release blocker.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
The doc-link test (`TestNoDanglingDocReference`) checks every `.md` path named
here, so cite only paths that exist.

Every gosmo change still follows `~/go/gosmo/CLAUDE.md`:

- build and test in gosmo first;
- run `go vet -tags livedb ./...` after any rename;
- run `TestLiveVersionSweep` on the oldest instance (win10cli\SQL2016) after
  any query change. None is planned here.

IDs: **B** bug or hazard, **C** consistency, **S** simplification, **H**
release hygiene, **D** docs.

---

## Bugs and hazards

### B1 — gosmo: the live version sweep would run any new ctx-only write on a real server

**Done 2026-10-22, with D2.** The reflective half moved, byte-for-byte, to the
untagged `versionsweep_reflect_test.go`, and each call runs under its own
`WithScript`. `TestReflectSweepRefusesWrites` pins it offline: a test-only
ctx-only write, and `Table.Truncate` with its entry removed, both fail the
sweep and never reach the driver (mutation-checked by dropping the wrapping).
`TestSweepSkipNamesRealMethods` runs `checkSkipList` offline. Live on
win10cli\SQL2016: 399 calls, 0 failed, 13 refused — unchanged.

`live_versionsweep_test.go` `reflectSweep` calls every exported method on
`Server`, `Database`, `Table`, `Index`, `Statistic`, `Login`, `Job` and
`ResourcePool` that takes only `ctx`, or `ctx` plus a zero `ObjectFilter`/
`TableQuery`, and returns an error. Writes are kept out only by a hand-kept
deny-list, `sweepSkip` (`Table.Truncate`, `Database.SetOffline`,
`Server.CycleErrorLog`, …).

`checkSkipList` catches a stale entry but not a missing one. Today all 199
such methods outside the list are reads; I checked them by name. But the next
ctx-only write — say a `Database.Checkpoint(ctx)` or a `Table.Rebuild(ctx)` —
gets executed the first time someone runs the sweep. That could be on
win10cli, or on ubusql1, the AG cluster. The test exists to be run after every
query change, so it is the test most likely to hit such a write.

**Fix:** run the reflective half under `gosmo.WithScript(ctx)`.

- `WithScript` intercepts writes only (`~/go/gosmo/CLAUDE.md` § Script mode),
  so the reads still hit the server and the sweep measures exactly what it
  does today.
- After each call, fail the test if the capture holds any statement: "X wrote
  under the sweep; add it to `sweepSkip`".
- The deny-list becomes a detector: a missing entry now fails in script mode
  instead of executing.
- Keep `sweepSkip` for the existing entries. They are still writes and should
  not be swept.

**Test:** add a fake ctx-only write to a test-only type the sweep is pointed
at, or temporarily delete `Table.Truncate` from `sweepSkip`, and confirm the
sweep fails offline. A fake driver will do; no live run is needed for the
mutation check. Then run the sweep once on win10cli\SQL2016 and confirm the
call count (399) and failure count are unchanged.

---

## Consistency

### C1 — gosmo: `Ref` suffix on two row types, and `SecurableRef` duplicates `Securable` (breaking)

`~/go/gosmo/CLAUDE.md` says the `Ref` suffix marks a lookup-free handle. Two
types break that rule:

- `AlertNotificationRef` and `JobNotificationRef` (`agent_operator.go:162`,
  `:193`) are result rows of `Operator.NotifyingAlerts`/`NotifyingJobs`. They
  have no method and nothing to look up.
- `SecurableRef` (`securable_search.go:20`) is `{Type, Schema, Name}`. It
  duplicates `Securable` (`permission_options.go:95`), which is
  `{Class, Schema, Name, Columns}` and is what `ApplyPermission` takes. So a
  picker result can't be passed straight to the write, and the same field is
  called `Type` in one and `Class` in the other.

**Fix:**

1. Rename the two row types to `OperatorAlert` and `OperatorJob`. Field names
   stay.
2. Make `FindSecurables` return `[]Securable`, with `Columns` nil, and delete
   `SecurableRef`.

gossms call sites to update: `agent_operator_props.go:120-124`,
`agent_detail.go:191-196`, `securables_matrix.go:146`, plus any conversion
from `SecurableRef` to `Securable` they do. Also update the gosmo
`ARCHITECTURE.md` feature-map row (line 768). Then run `go vet -tags livedb`.
No SQL changes.

### C2 — gossms: `ConnectContext` and the `ProbeCapabilities` pair break the one-form rule

gosmo has one form of each call, context first (`gosmo.Connect(ctx, …)`).
gossms's `internal/db` still has the old shapes:

- `ConnectContext(ctx, opts, role)`, with no plain `Connect` beside it
  (`connection.go:113`);
- `ProbeCapabilities()`, a wrapper over
  `ProbeCapabilitiesContext(sc.Server.Context())` (`capabilities.go:193-201`).
  Only tests call the wrapper.

**Fix:**

- Rename `ConnectContext` to `Connect`.
- Fold the pair into `ProbeCapabilities(ctx)`. Test callers pass
  `sc.Server.Context()`.
- Update `ARCHITECTURE.md:47` (§ How a query runs) and the comments that name
  either function.

This is mechanical. Driving the binary isn't needed beyond one connect in tmux.

### C3 — gossms: duplicated bracket-qualified name helpers

gosmo's `qualifiedName(schema, name)` is unexported, so gossms builds the same
string by hand: `fqn` (`explorer_loaders.go:426`) and `securables_matrix.go:41`
both write `QuoteName(schema) + "." + QuoteName(name)`, with the
empty-schema special case.

**Fix:** export `tsql.QualifiedName`, with a root forwarder like
`QuoteName`'s, and have gosmo's `qualifiedName` call it. Then use it from both
gossms sites. This adds gosmo surface without narrowing any; pin it with a
case in `identifier_quoting_test.go`. Low value. Do it with C1, while gosmo is
open.

---

## Simplifications

### S1 — gosmo: three near-identical "who notifies whom" readers

`Alert.Notifications` (`agent_alert.go:315`), `Operator.NotifyingAlerts`
(`agent_operator.go:168`) and `Operator.NotifyingJobs` (`:201`) are a three-way
`dupl` clone. Each one does a `requireID`, a two-column query on one msdb id,
and a `scanRows` that converts the second column to an enum.

**Option:** one unexported generic,
`notificationRows[T](ctx, s, what, q, id, build func(name string, code int) T)`.
It saves about 30 lines and is behaviour-neutral, since the existing tests pin
the statements.

**Only if C1 is done:** the rename touches the same lines. Otherwise skip it;
it's below the bar on its own.

---

## Release hygiene

### H1 — gosmo `HEAD` is 10 commits past `v0.1.0`, with breaking changes and no changelog entry

`CLAUDE.md` says gossms can't release while gosmo's `HEAD` is untagged.
Yesterday's work landed these breaking changes after `v0.1.0`:

- R3 list signatures: `X(ctx)` → `X(ctx, ObjectFilter{})`, the `…Filtered`
  methods removed, `Tables(ctx, TableQuery)`, `ReadLog(…, LogSearch)`;
- **`TableKindAll` inserted as the zero `TableKind`**, which shifts
  `TableKindUser` and the rest up by one;
- S2 `Index.Partitions`;
- the R2 typed enums;
- S1 one `DatabaseFileInfo.Shrink`;
- C2 `Server.exec` (internal only).

`CHANGELOG.md` has no entry above `v0.1.0`.

The `TableKind` shift is the dangerous one. Code that uses the constant names
recompiles fine. Code that stored or transmitted the integer gets the wrong
kind silently. gossms is clean: I found no persisted `TableKind`.

**Fix, when the release is asked for** (per `CLAUDE.md`, the changelog is not
edited in a fix):

- Write the next gosmo entry with a **Migrating** note per break, and call out
  the `TableKind` renumbering explicitly.
- Tag gosmo, then comment out gossms's `replace` to tag gossms.
- **Optional (breaking-safe now, not later):** give `TableKind` explicit
  values, or make it a string type like the R2 enums, so a future insertion
  can't renumber it again.

---

## Docs

- **D1 — stale `…FilteredContext` references after R3.** These name listings
  that no longer exist:
  - `internal/tui/explorer_filter.go:522` (`serverFilter`'s comment);
  - `docs/db-rules.md:144`;
  - `docs/decisions.md:600`.

  Reword each to "a listing taking an `ObjectFilter`". Don't edit
  `CHANGELOG.md:1515`/`:1528`; they are history.
- **D2 — gosmo sweep comments name the removed `XxxContext(ctx)` form.**
  `live_versionsweep_test.go:124` and `:205`. The code matches on the
  parameter type, not the name, so only the comments are wrong. Do it with B1,
  which rewrites that comment anyway.
- **D3 — one-line rules for where they will load again**
  (`CLAUDE.md` § Self learning):
  - gosmo `CLAUDE.md` § Conventions, the `Ref` bullet: "a result row is never
    named `…Ref`" (C1).
  - gossms `CLAUDE.md`: "`internal/db` follows gosmo's one-form rule: no
    `Foo`/`FooContext` pairs" (C2).

---

## Verification baseline

| Check | gossms | gosmo |
|---|---|---|
| `go build ./...` | clean | clean |
| `go test ./...` | pass | pass |
| `go vet ./...` | clean | clean (also `-tags livedb`) |
| `staticcheck -checks all` | clean | clean |
| `deadcode` | 6 hits, all deliberate | — |
| `dupl` (80–120 tokens) | small clones only | small clones only |
| `go test -race ./...` | pass, no races | pass, no races |

---

## Recommended implementation order

Hazard first, then the gosmo breaks in one sitting (each leaves both repos
building, with `replace` active), then gossms-only renames, and docs with the
item they describe. Release last, and only on request.

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | **B1** sweep under `WithScript` + **D2** | gosmo | Only item that can damage a real server. Offline mutation check, then one sweep on SQL2016. |
| 2 | **C1** row-type renames, `FindSecurables` → `[]Securable` | gosmo → gossms | Breaking; batch it with the other gosmo edits so the next tag carries it. `-tags livedb` vet after. |
| 3 | **C3** export `tsql.QualifiedName` | gosmo → gossms | Same sitting as C1; additive. |
| 4 | **S1** notification reader | gosmo | Only if doing C1. Otherwise skip. |
| 5 | **C2** `Connect` / `ProbeCapabilities(ctx)` | gossms | Mechanical rename; one tmux connect. |
| 6 | **D1, D3** | both | With or right after the items above. |
| 7 | **H1** changelog, optional `TableKind` hardening, tags | gosmo → gossms | Only when a release is asked for. If `TableKind` is hardened, do it before the tag, since afterwards it is another break. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet in gosmo after any rename);
- the checks named in the item have run;
- for a user-visible change, the built binary has been driven in tmux, per
  `docs/testing.md`.
