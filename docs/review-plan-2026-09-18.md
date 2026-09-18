# Review plan — 2026-09-18

A full read of both repositories (gossms ~95 kLOC, gosmo ~43 kLOC) looking for
bugs, inconsistencies, optimisations, simplifications and architecture. Nothing
here is implemented — this is the plan, in priority order.

**Item IDs in this document are `P1…P14`.** They are local to this plan.
`docs/decisions.md` cites `R2` and `R5` from the retired plan, and
`docs/open-threads.md` owns the permanent `B*`/`V*` series; neither is reused
here. An item that survives to `docs/open-threads.md` takes the next free
number in *that* series at the time it moves.

## What was already clean

Worth recording so the next review does not re-measure it:

- `gofmt -l`, `go vet ./...`, `staticcheck ./...` and `go test ./...` are all
  silent on **both** repositories.
- `internal/tuikit`'s external imports are still exactly the three
  `ARCHITECTURE.md` names — no layering violation.
- gosmo's `rows.Err()` rule holds: every non-test file with a `rows.Next()`
  has at least as many `rows.Err()` calls.
- The `escapeSingle`/`quoteIdent` split holds at every call site checked
  (`OBJECT_ID`, `TYPE_ID`, `DB_ID`, `SUSER_ID`, `SCHEMA_ID`,
  `DATABASE_PRINCIPAL_ID`). The functions that take an *unquoted* name
  (`DB_ID`, `SUSER_ID`, `SCHEMA_ID`, `DATABASE_PRINCIPAL_ID`) correctly get
  `escapeSingle` alone; the ones that take a *quotable* name get
  `escapeSingle(qualifiedName(...))`.
- `*ByNameContext` not-found handling: exactly two methods answer without
  `notFoundf`, and they are the two `docs/decisions.md` names
  (`CertificateByName`, `AsymmetricKeyByName`).
- 154 `setIfApplied`/`setPtrIfApplied` sites; no `Rename`/`Enable` found
  mirroring state onto its receiver by direct assignment.
- gossms has no `context.Background()` in a background load that should be
  connection-scoped, no bare `go func()` outside `safego.go`, and no direct
  `postEvent`+`wakeEventLoop` pair outside `postAndWake`.
- `DataGrid.computeColWidths` is already bounded in both rows and cell width.
- Zero `TODO`/`FIXME`/`HACK` markers in either repository.

---

## Bugs

### P1 — `PropertySheet.Refresh` starts a second page load without stopping the first

`internal/tuikit/propsheet/sheet.go:329` (`Refresh`) calls `startLoad(page)`
unconditionally, with no test on `slot.state`. `startLoad` bumps `p.seq` and
dispatches `OnLoadPage`, and `PropDialog.onLoadPage`
(`internal/tui/prop_dialog.go:253`) opens a **fresh**
`context.WithTimeout(sessionCtx, propFetchTimeout)` whose `cancel` is a
`defer` inside its own goroutine. Nothing cancels the load being superseded.

So F5 on a page whose load is still out gets the staleness half of the
`latest` contract and not the cancel half — which is exactly the failure
`internal/tui/latest.go` and `ARCHITECTURE.md` § Latest-only loads name:

> **Both halves matter, and a copy with only the first is a bug.** … the
> cancel stops the superseded fetch's queries, so they release their pool
> connection now rather than at their timeout.

Failure scenario: open Index Properties on a large index, go to
Fragmentation (`sys.dm_db_index_physical_stats`), press F5 four times while it
loads. Four reads are in flight, three of them wanted by nobody, each holding
a pooled connection for up to `propFetchTimeout` (30 s). The fourth — the one
the user is waiting for — queues behind them. `InvalidateAll` (after a
successful Apply) has the same shape, at lower risk.

Two further facts:

- **`ARCHITECTURE.md` § Latest-only loads lists "`PropDialog`'s page loads"
  among the sites that "own a `latest` rather than their own copy". It does
  not own one.** The document asserts the fix is already in place, which is
  how this survived. Correct the sentence whichever way P1 lands.
- The propsheet side cannot hold the cancel itself: `tuikit` knows nothing
  about `context`-scoped application fetches, and it must not learn.

**Fix (recommended, both halves):**

1. `internal/tuikit/propsheet/sheet.go` — `Refresh` returns early when
   `slot.state == PageLoading`. It is the same judgement `SelectPage` already
   makes ("Navigating to an already-loaded or still-loading page is always
   allowed; it is starting a *new* load while applying that isn't"), and it
   removes the duplicate dispatch at source.
2. `internal/tui/prop_dialog.go` — give `PropDialog` a per-page cancel
   (`pageRuns map[int]context.CancelFunc`, or a `[]latest` sized to
   `d.pages`) so a load *is* cancellable, and cancel page *i*'s previous run
   at the top of `onLoadPage`. `InvalidateAll` then also stops what it
   replaces. A `latest` per page is the closer fit to the existing idiom, but
   its seq would duplicate propsheet's — a bare `map[int]context.CancelFunc`
   keyed by page, cancelled and re-armed in `onLoadPage` and drained in
   `onClose`/`show`, is the smaller change. Take the map.

**Pin it:** a `fakedb_test.go` page whose load blocks on a channel; call
`Refresh` twice and assert the first load's context is `Done` and that
`OnLoadPage` dispatched once (step 1) / twice with the first cancelled
(step 2). Mutating either half must fail it.

### P2 — two drop entries bypass `dbOf`, costing a `sys.databases` round trip each

**Done (2026-09-18)**, with P5, P6 and P7. `TestDropsThroughDbOfReadNoCatalog`
(`explorer_object_ops_write_test.go`) pins it, and covers the sibling trigger
entry as the third case.

`internal/tui/explorer_object_ops.go` defines `dbOf` (line 64) precisely so
the per-NodeType drop/rename table never resolves a database it does not read
anything off. 24 entries use `dbOf(sc, n)`. Two do not:

- line 479, `NodeDatabaseScopedCredential`'s `drop`
- line 519, `NodeDatabaseAuditSpecification`'s `drop`

Both do `sc.Server.DatabaseByNameContext(ctx, n.DBName)` and then address the
child with a `…Ref(n.Name).DropContext(ctx)` — a lookup-free handle hung off a
looked-up parent. The sibling `NodeDatabaseTrigger` entry (line 549) is the
identical shape done the other way, with the rule written out at the call.

Impact is one wasted round trip per delete, plus a delete that fails with
"database not found" where the sibling would have emitted the DROP. Not a
re-raise of `docs/decisions.md` § "The 137 `DatabaseByNameContext` calls in
`internal/tui` stay as they are": that closure is about *loaders* resolving a
database for a read, and turns on `IsSystem()`/`IsSnapshot()` going wrong on a
handle. Neither drop reads a field of the `*Database`, and this file already
made the call for its other 24 entries.

**Fix:** `return dbOf(sc, n).DatabaseScopedCredentialRef(n.Name).DropContext(ctx)`
and the matching line for the audit specification. Delete the two now-unused
error branches.

**Pin it:** the drop table is already exercised; add a case asserting these
two node types' `drop` issues no `sys.databases` read (`assertNoStatementsIn`
plus a `Statements()` check against the fake driver).

### P3 — `Sequence.DataType` loses the type's schema, so `ScriptSequence` is not re-runnable

`~/go/gosmo/sequence_synonym.go`'s `SequencesContext` joins `sys.types` and
selects `tp.name` alone. `buildSequenceScript`
(`~/go/gosmo/scripter_objects.go:207`) then emits
`CREATE SEQUENCE … AS [<name>]`.

`CREATE SEQUENCE … AS <type>` accepts a user-defined **alias** type over an
integer base type. For such a sequence the emitted script carries the type's
name with no schema, so re-running it resolves the alias against the executing
principal's default schema — a different type, or none.

Second half of the same defect: the field is declared `DataType`, a closed
vocabulary of built-in names (`types.go:107…`), and `CreateSequence` enforces
that vocabulary through `validDataType`. The *read* path puts a
user-defined type name into it, so the declared type lies about the field's
domain — the same "unguessable shape" objection `docs/decisions.md` records
against `Database`'s accessors.

**Fix (breaking, authorised):** add `Sequence.DataTypeSchema string`, scanned
from `SCHEMA_NAME(tp.schema_id)`, and qualify in the scripter whenever it is
neither empty nor `sys`. Leave `DataType` as it is rather than widening it to
`string`; document on the field that a sequence over an alias type carries
that type's name, and that `DataTypeSchema` is the half that disambiguates it.

**Pin it:** a scripter unit test over a `Sequence` with
`DataTypeSchema: "app"`, asserting `AS [app].[bigid]`; and a `sys` one
asserting the bare `AS [bigint]` is unchanged. Live: create an alias type in a
non-`dbo` schema, a sequence over it, and round-trip the script.

### P4 — `Editor.SetWrapMode` leaves the departing mode's state behind

`internal/tuikit/controls/editor.go:234` is a bare assignment. Wrap mode and
plain mode disagree about three pieces of state:

- `selBlock` — `editor.go:112` says the rectangular selection reinterprets
  the anchor/cursor pair, and that wrap mode "breaks the fixed rune columns it
  assumes". `editor_input.go:73` refuses the keyboard combo in wrap mode
  (`blockCombo := … && !e.wrapMode`), and the non-wrap mouse press sets it
  explicitly (`editor_input.go:410`). **`handleMouseWrapped`
  (`editor_wrap.go:148`) never reads, sets or clears it** — grep confirms
  `selBlock` does not appear in that file. So a `selBlock` that is already
  true when wrap mode is entered can be cleared by no click.
- `scrollCol` — `editor_input.go:526` records it as "meaningful only outside
  `wrapMode`".
- `scrollRow` — a *visual* row index in wrap mode and a logical one outside
  it; switching reinterprets it.

Not reachable in the shipped binary: the three callers
(`connect_dialog.go:199`, `:203`, `datagrid_overlay.go:124`) all set it at
construction and never flip it. It is reachable through the published
`tuikit` API, which is the whole point of the package being extractable.

**Fix:** make `SetWrapMode` a no-op when `v == e.wrapMode`, and otherwise
reset `selBlock`, `selecting`, `mouseDragging`, `scrollRow` and `scrollCol`
before assigning — the state `editor.go:305` already resets together, minus
the document. Add the one-line `selBlock` clear to `handleMouseWrapped`'s
fresh-press branch too, so the invariant does not depend on the setter alone.

**Pin it:** a test that arms a block selection, calls `SetWrapMode(true)`, and
asserts `SelectedText()` is the linear reading; plus one asserting
`SetWrapMode` with the current value does not disturb the cursor.

---

## Inconsistencies and documentation accuracy

`docs/decisions.md` records which comment drift recurs — "a doc naming the
wrong caller after a helper moved … an inverted sentence that reads fluently
either way". Three current instances, all load-bearing.

### P5 — "the only form that works under a `WithScript`-derived context" is over-broad

**Done (2026-09-18)**, for the four sites named below. The same claim on ~20
gosmo `Ref` *method* doc comments is left, as `docs/open-threads.md` B7.

Stated unconditionally in four places:

- `CLAUDE.md` § The one rule that matters most — "`DatabaseRef` … is the only
  form that works under a `WithScript`-derived context"
- `~/go/gosmo/CLAUDE.md` § Conventions, the `Ref` suffix bullet
- `docs/decisions.md` § gosmo: deliberate API decisions — "The lightweight
  form itself is not removable — it is the only one that works under a
  `WithScript`-derived context"
- `internal/tui/explorer_object_ops.go:64`, `dbOf`'s doc — and this one is the
  stated justification for the helper existing

gosmo's own `Scripting` doc (`~/go/gosmo/script.go:60`) says the opposite:

> Reads are unaffected by WithScript and go to the real server either way.

`WithScript` only intercepts *writes* (`docs/testing.md` says the same about
the `fakedb` harness), so `DatabaseByNameContext` under a script context
issues its query against the live server and succeeds — which is why P2 is a
wasted round trip rather than a broken Script-as-DROP.

The accurate rule is narrower, and still a real reason the handle cannot go:
the by-name read fails under `WithScript` when the object the script names
**does not exist yet** — a New-X dialog's Script Changes, scripting a CREATE
for a database or child that has not been created — and when there is no live
connection to read with at all. For a Script-as-DROP of an existing object
both forms work.

**Fix:** state the narrow rule once, in `~/go/gosmo/CLAUDE.md` beside the
`Ref` bullet (gosmo owns the semantics), and reduce the other three to a
sentence that links there. `dbOf`'s comment keeps its *first* reason — the
round trip buys nothing — and drops the second.

### P6 — `loadDatabaseTrigger`'s comment justifies the wrong thing

**Done (2026-09-18)**. The call was switched to `DatabaseRef` rather than
re-justified, matching the two sibling sites; `ref_comment_test.go`'s doc
comment now states the gap it leaves.

`internal/tui/database_trigger_props.go:73`:

> `DatabaseByName`, not `DatabaseRef`: the pages show `CreateDate` and the
> definition, which the lightweight handle leaves zero-valued.

`CreateDate` and `Definition` are fields of the **trigger**, scanned by
`DatabaseTriggerByNameContext`, not of the database. That method reads nothing
off its `*Database` but `Name` (for `useBatch` and the error text), so
`DatabaseRef(dbName)` is behaviourally identical here and is what
`explorer_object_ops.go:549` and `app_explorer_data.go:292` already use for
the same object.

Note what this says about the new `internal/tui/ref_comment_test.go` in the
working tree: it pins the *names* a comment may use and cannot see a comment
whose names are now right and whose claim is wrong. That is the gap it should
say it leaves, in its own doc comment.

**Fix:** correct the comment. Changing the call to `DatabaseRef` is P2's
argument and is optional here — but if it is left as `DatabaseByName`, the
comment must give the real reason or none.

### P7 — `ARCHITECTURE.md` understates `gate`'s imports

**Done (2026-09-18)**.

§ Package map: "`gate` … imports `internal/db` and `gosmo`". The package also
imports `internal/tuikit/controls` (`gate/menu.go`'s `controls.MenuItem`
parameters). `internal/tui/gate/doc.go:13` has it right — "internal/db and
tuikit/controls". One-line fix; the invariant the paragraph exists to state
(`gate` never imports `tui`) is intact.

---

## gosmo API consistency — breaking changes authorised

All three follow the same reasoning `docs/decisions.md` records for the
2026-09-18 `Database` field change: the defect was the shape being
*unguessable* from the type.

### P8 — the parent back-pointer is exposed by half the types

54 gosmo types carry a `db *Database` or `server *Server` field. **26 expose
it, 28 do not** — close enough to a coin flip that no caller can guess.
Exposed: `Table`, `Assembly`, `PlanGuide`, `Rule`, `Default`, `Route`, the
five user-type families, the Service Broker families, `DatabaseTrigger`,
`DatabaseScopedCredential`, `DatabaseAuditSpecification`, `ExternalDataSource`
and friends, `Database`, `DatabaseSnapshot`, `AvailabilityGroup`,
`DatabaseMirroringEndpoint`. Not exposed, among others: `Login`, `Job`,
`Schedule`, `Alert`, `Operator`, `Credential`, `ServerRole`, `ServerAudit`,
`ServerAuditSpecification`, `ServerTrigger`, `Endpoint`, `ConfigurationOption`,
`User`, `Schema`, `DatabaseRole`, `Certificate`, `AsymmetricKey`,
`ColumnMasterKey`, `ColumnEncryptionKey`, `PartitionFunction`,
`PartitionScheme`, `SecurityPolicy`, `Sequence`, `Synonym`.

A caller holding a `*Login` cannot reach the `*Server` it came from and must
thread one alongside it; a caller holding a `*Rule` can. The library already
decided this is worth exposing — it just did it 26 times out of 54.

**Fix (additive, non-breaking):** add the missing 28, following the existing
shape exactly — `Database() *Database` for a `db` field, `Server() *Server`
for a `server` field, one line, no context, documented as a back-pointer.
Nothing is removed, so the "never narrow a capability" rule is untouched.

**Pin it:** a source-reading test in the style of
`method_pair_wiring_test.go` / `gate/names_test.go`: enumerate every struct
with a `db *Database` or `server *Server` field and require the matching
accessor, with an explicit exception list carrying a reason per entry. That is
what stops the ratio drifting back.

### P9 — `Table.DB()` is the only `DB()`-named parent accessor, and the name is taken

Twenty types name the parent-database back-pointer `Database()`. `Table`
(`~/go/gosmo/table.go:62`) names it `DB()`. In the same package
`Server.DB()` returns `*sql.DB`, so `DB()` already means "the pool" — and
`Table.DB()` means "the database object". Both `CLAUDE.md`s and
`ARCHITECTURE.md` cite `Table.DB()` as *the* example of a back-pointer, so the
outlier is currently taught as the pattern.

`Table.DB()` has **zero callers** in either repository (the 13 `.DB()` call
sites in gossms and the one in gosmo are all `Server.DB()`).

**Fix:** rename to `Table.Database()`. Keeping `DB()` as a deprecated alias is
the non-breaking option; given the authorisation and the zero call sites, take
the clean rename and update the three documents that cite it. Run
`go vet -tags livedb ./...` after, per `~/go/gosmo/CLAUDE.md`.

### P10 — eight collections have no `*Seq`, and nothing pins iterator coverage

`iter_wiring_test.go` is thorough about *direction* — every declared `*Seq` is
ranged against a recording driver and must issue the same SQL as its
`…Context` partner, and a new `*Seq` fails until it is in the table. It says
nothing about *coverage*, so a new collection method silently joins without an
iterator. Eight have:

| Method | Element |
|---|---|
| `Database.AsymmetricKeysContext` | `*AsymmetricKey` |
| `Database.DatabaseAuditSpecificationsContext` | `*DatabaseAuditSpecification` |
| `Assembly.FilesContext` | `*AssemblyFile` |
| `Assembly.ModulesContext` | `*AssemblyModule` |
| `UserDefinedTableType.ColumnsContext` | `*Column` |
| `Server.CryptographicProvidersContext` | `*CryptographicProvider` |
| `Server.DatabaseRecoveryStatusesContext` | `*DatabaseRecoveryStatus` |
| `BackupDevice.HeadersContext` | `*BackupHeader` |

The three `[]string` returns (`Server.AuditActionGroupsContext`,
`Server.DatabaseAuditActionsContext`,
`Server.DatabaseAuditActionGroupsContext`) and `Server.FixedDrivesContext` are
the plausible deliberate omissions — a `Seq2[string, error]` over a fixed
vocabulary buys nothing.

**Fix:** add the eight, wire each into `iter_wiring_test.go`'s table, then add
the symmetric guard: enumerate every exported zero-extra-argument
`…Context(ctx) ([]*T, error)` method and require either a `*Seq` or an entry
in an exception list with a reason. The four `[]string`/`FixedDrive` cases are
the list's first entries.

---

## Simplifications

The working tree is already mid-pass on this (`credentialGeneralPage`,
`definitionPage`, `sqlBodyRow`, `indexKeyColumnGrid`,
`forwardReleaseToFocusedField`). What follows is the rest of what a
window-hash duplicate scan over both `internal/` trees turns up, ranked by how
much a divergence between the copies would cost. **`docs/decisions.md` §
"Merging the two user-mapping page builders is a deliberate non-goal" is the
standing counter-argument, and P13 is where it applies — do not merge for the
count.**

### P11 — the two audit-specification pages duplicate two reasoned blocks

`internal/tui/audit_specification_props.go:53-69` and
`internal/tui/database_audit_specification_props.go:101-115` hold the same two
decisions twice:

- the **orphaned-specification select**: `slices.Index(auditNames, spec.AuditName)`,
  and on `< 0` prepend `missingAuditItem` and select it, so a stray Apply
  cannot silently rebind the specification to whichever audit sorts first.
  Both copies carry a comment; the database copy's is already a back-reference
  to "the server half", which is the shape that goes stale.
- the **pick-list union**: append any `spec.ActionGroups` entry the server's
  list does not contain, then `slices.Sort`, so a group the instance no longer
  defines does not vanish from a page that still records it.

This is the class the working tree's `credentialGeneralPage` change was
written for — two copies of one rule, each with its own wording.

**Fix:** two helpers in `prop_grid_helpers.go` beside the ones just added —
`auditSelectRow(names []string, current string) (propsheet.Row, []string)`
carrying the orphan rule and its comment once, and `unionSorted(list, extra
[]string) []string` carrying the pick-list rule. Do **not** merge the two page
builders: they differ in the audit list they start from (the database page
filters audits already bound elsewhere via `taken`), in their grid heights,
and the database page has a second per-securable grid.

**Pin it:** a page test per scope asserting an orphaned specification's audit
name is present and selected, and that a recorded group absent from the
server's list still renders — the assertions that die if the shared helper
drifts.

### P12 — the editor's two mouse-press branches duplicate the fresh-press body

`internal/tuikit/controls/editor_input.go:379-412` (unwrapped) and
`editor_wrap.go:150-186` (wrapped) differ only in **how `(row, col)` is
derived** — a clamp plus `runeColAtScreenX` against a physical row, versus a
visual-line lookup plus `RuneIndexAtColumn` against a segment. Everything
after that is the same twenty lines: the `mouseDragging` latch, the
`pressIsDouble` word-select, the Shift-extend anchor rule, `selecting`,
cursor placement, the continued-drag branch and `desiredCol`. Both carry a
comment; the wrapped one's is already "see the identical Shift+Click handling
in HandleMouse … for the rationale".

P4 is the bug this duplication produced: the shared body's `selBlock`
assignment exists in one copy only.

**Fix:** extract `applyMousePress(row, col int, ev *tcell.EventMouse) bool`
holding the shared twenty lines, and leave each branch its own `(row, col)`
derivation and its own `desiredCol` computation. Do it as part of P4, not
separately — the extraction is what makes P4 impossible to reintroduce.

**Pin it:** the existing `editor_block_test.go` and the mouse tests; add one
that drives the *wrapped* path through a Shift+Click and a double-click, which
nothing currently does.

### P13 — three lower-value duplicate pairs, listed so they are not rediscovered

Real, shallow, and each with a reason to leave it alone. Recorded with a
recommendation rather than a task:

- **`internal/tui/tasks_dialog.go:75-86` / `query_list_dialog.go:90-101`** —
  the same `dataH := d.InnerRect().H - 2`, Escape/Up/Down/`ensureVisible`
  list-dialog skeleton. Twelve lines, no shared rule, and the two selection
  bounds read different slices. **Leave.** If a third list dialog appears,
  extract then.
- **`internal/tui/ag_props_backup.go:163-178` / `ag_props_routing.go:155-170`**
  — the same "nothing dirty → return nil, else fetch replicas and index them
  by name" prologue. The `agReplicasByName` half is already shared; what is
  left is a four-line dirty scan over two different edit types. A generic
  `anyDirty[T any](edits []T, dirty func(T) bool) bool` would collapse it.
  **Optional**, and only worth taking if a third AG apply arrives.
- **`internal/tui/effective_perms_page.go:122-134` and `:166-178`**, and
  **`query_store_panel_plans.go:103-114` and `:126-140`** — self-duplication
  inside one file, which is the cheapest kind to fix and the least risky.
  **Take these two** when a change lands in either file; they need no new
  shared surface, only a local helper.

### P14 — `forwardReleaseToFocusedField`'s `InputField` arm: verify or narrow the doc

The helper added in the working tree (`internal/tui/dialog_common.go`) says it
"covers the field that has focus without one" — without a claimed press.
In all three callers (`backup_dialog_input.go:96`,
`restore_dialog_input.go:138`, `connect_dialog_input.go:172`) the only path
that forwards a press to a `*widgets.InputField` is `dialogs.FieldGesture.Claim`
(`backup_dialog_input.go:145`, `restore_dialog_input.go:210`,
`connect_dialog_input.go:280`), and `drag.Release` runs above
`ConsumeOutsideClick` in each. So a focused `InputField` holding a latch
without a held gesture looks unreachable, and the `*controls.Editor` arm — an
Editor is deliberately outside `FieldGesture` — is the live one.

This is not a defect: the arm is cheap and the claim may be describing a path
the three dialogs do not currently take. But the comment is the kind
`docs/decisions.md` flags, so it should be settled rather than left ambiguous.

**Fix:** either find the reachable path and name it in the comment, or narrow
the comment to the Editor and keep the `InputField` arm as stated
belt-and-braces. Half an hour, and it closes a doc claim nothing can check.

---

## Considered and rejected

Recorded so a later review does not spend the time again.

- **`Peer`'s liveness check.** `peerLive` (`internal/db/peer.go:275`) tests the
  cached peer's context, so a peer whose TCP connection died but whose context
  is live is handed out forever, and `ForgetPeerFailures` clears failures, not
  stale-but-open peers. Left alone: every read through it errors and names the
  instance, and Refresh reopens. Adding a ping per cache hit costs a round trip
  on the hot path to fix a case the user already recovers from.
- **`Peer`'s `fallback == opts` struct comparison** (`peer.go:70`) compares
  every field of `config.Connection`, so a resolver answer differing only in
  `Name` costs a redundant second dial against an instance that is really down.
  Left alone: it is one extra attempt in an already-failing path, and
  comparing a chosen subset of fields is the more fragile form.
- **Splitting `internal/tui`.** `docs/decisions.md` closed this on measured
  numbers. Nothing found here reopens it. The three files over the ~900-line
  prompt (`detail_browser.go` 954, `activity_monitor.go` 930,
  `propsheet/rows.go` 920) are all within a rounding error of it and none has
  a seam its own section comments already draw.
- **gosmo's six files over 900 lines** (`query_store_reports.go` 1139,
  `table.go` 1112, `backup.go` 1025, `database.go` 1007, `index.go` 968,
  `connection.go` 958). Same answer: a prompt to look, and nothing in them is
  landing a change now.
- **The `DatabaseByNameContext`-for-a-single-child-read pattern**, 38 sites
  where the resolved `*Database` is used exactly once. `docs/decisions.md`
  closed this unmeasured and asked for a timing from a real expand. P2 and P6
  are carved out of it on different grounds (an in-file inconsistency, and a
  false comment), not on the round-trip argument.

---

## Suggested order

1. **P1** — the only item with a measurable runtime cost to a user today.
2. **P4 + P12 together** — the fix and the extraction that prevents its return.
3. **P2, P6, P5, P7** — one commit of corrections; P5 first, since P6's and
   P2's comments both lean on it.
4. **P8, P9, P10** — the gosmo consistency pass. One breaking change (P9) plus
   two additive ones, all three with a source-reading test that stops the
   convention drifting again. Tag gosmo after, per
   `docs/open-threads.md` § Release workflow.
5. **P11, P3** — the audit-page helpers, and the sequence schema.
6. **P14, then P13's two self-duplications** — opportunistic, when a change
   lands in the file anyway.

Anything from here that is knowingly left undone moves to
`docs/open-threads.md` under the next free `B*`/`V*` number; anything argued
and declined moves to `docs/decisions.md`. This file is deleted once its items
are closed, and its `doc_links_test.go` exemption — if one is ever added for
it — goes with it.
