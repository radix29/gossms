# Engineering journal

Dated record of the work behind goSSMS and gosmo **since the current tag**:
what was built, what bugs were found and how, and which decisions were made
deliberately. Trimmed at each release — entries for work that has shipped come
out, since `CHANGELOG.md` records what shipped and git history keeps the rest.
Trimmed to `v0.0.7` (2026-08-19) on 2026-08-19.

Nothing here is required reading. `CLAUDE.md` carries the rules that still
apply; `docs/open-threads.md` carries the work that is still open. Newest
entries at the bottom. A `slug` under a heading is a note's name from the
Claude Code memory store this file was migrated out of, kept for older
cross-references.

The v0.0.7 entries are in git history at the `v0.0.7` tag and its parent
commits — Always On in seven phases (the read models and the primary-following
rule behind them, AG Properties, the dashboard, operations, creating a group,
the four gaps that scoped out, and Add Replica/join), Backup and Restore
learning to browse the server's own filesystem, the Restore File Locations
view, the SQL Server / Agent log viewer, the Object Explorer folder filter,
general Delete and Rename, the text-encoding work behind File > Open and Save,
the small-terminal dialog thread (resize, then clipping), the grid-cursor
sweep and its six shipped sites, the busy-latch passes behind `safegoRepair`,
and the cross-repo review passes of 2026-08-12 through 2026-08-18.

## 2026-08-19 — Ctrl+Shift+<letter> never arrived as KeyCtrlX

Reported as "Ctrl+O and Ctrl+Shift+O misbehave on macOS". The Connect binding
at `app_events.go` tested `KeyCtrlO` + `ModShift`, an event tcell never
produces: `newEventKey` folds a Ctrl-modified rune into `KeyCtrlA..KeyCtrlZ`
only when Ctrl is the *sole* modifier (`if mod == ModCtrl && !advanced`,
tcell v3.4.1 `key.go:390`). So Connect was unreachable by keyboard on every
terminal — legacy encodings send `0x0F` for Ctrl+Shift+O and it opened a file
instead, while Kitty/modifyOtherKeys terminals delivered a `KeyRune` that
matched nothing. `Ctrl+Shift+U` (Uppercase Selection) was dead the same way,
and its test passed only because it synthesized the impossible event.

Found by injecting the raw encodings into a running binary with
`tmux send-keys -H` and reading the Key Diagnostics dialog — the decode is
protocol-specific and no unit test would have shown it:

| bytes | decoded |
|---|---|
| `0x0F` (legacy Ctrl+O *and* Ctrl+Shift+O) | `Ctrl+O` |
| `CSI 111;5u` (Kitty Ctrl+O) | `Ctrl+O` |
| `CSI 111;6u` (Kitty Ctrl+Shift+O) | `Shift+Ctrl+Rune[o]` |
| `CSI 27;6;79~` (modifyOtherKeys Ctrl+Shift+O) | `Shift+Ctrl+Rune[O]` |

Fix: `normalizeCtrlRune` in `app_events.go`, applied once in `handleKey` after
`RecordKey` (diagnostics keeps showing the raw truth) so the editor and every
dialog inherit it. It folds Ctrl(+Shift)+ASCII-letter back to `KeyCtrlX` and
lowercases first — Kitty reports the base-layout rune, modifyOtherKeys the
shifted one. Ctrl+Alt is deliberately excluded: that is AltGr on many layouts
and produces text that must stay a `KeyRune`.

Normalization cannot help a legacy terminal, where the chord is physically
indistinguishable from Ctrl+O, so Connect also got **F9** — the binding that
works everywhere. Ctrl+Shift+O stays as the alias. README, the help dialog,
the File menu and the status bar were updated to lead with F9.

## 2026-08-19 — Menu cascades (`MenuItem.Sub`), Phase 1 item 4a

Groundwork for `Script … as ▸` on every Object Explorer node: `tuikit` had no
submenus at all, so `MenuBar` and `ContextMenu` gained them together.

`MenuItem.Sub []MenuItem` makes an item a cascade — it opens a submenu instead
of firing `Action`, and `menuRowSuffix` draws a `▸` where a shortcut would
otherwise sit (paid for in `menuContentWidth`, or the widest label runs into
the marker). The open chain itself lives in `menu_cascade.go`'s `menuCascade`,
shared by both hosts: it stores a *path* of item indexes rather than the item
lists, so the levels are re-derived from the host's current menu every frame
and a rebuilt menu can never be navigated through a stale pointer. Geometry is
recomputed by `draw` and cached into `rects` for hit-testing — the same reason
`ContextMenu` caches `drawnX/drawnY`: only `Draw` sees a `tcell.Screen`, so
only `Draw` knows where the edge clamp actually put the box. `hit` searches
deepest level first, so a submenu overlapping its parent isn't robbed of the
click by the box underneath.

Two things the existing hosts forced:

- `ContextMenu` needed a `mouseDragging` latch it never had. It was safe
  without one only because every click closed the menu; a click on a cascade
  row now leaves it open, and tcell's all-motion tracking resends `Button1` on
  every motion while the button is held.
- A click on a disabled or divider row still has to dismiss the menu. Both
  hosts' tests pin that, and the first cut of the new routing returned early
  before reaching it, leaving a disabled item stuck open.

Verified by driving a probe build under tmux with a three-level cascade
temporarily added to the File menu: keyboard (Right/Left/Escape/Enter, disabled
rows skipped), mouse via injected SGR sequences (`tmux send-keys -H`) for
hover-open, sibling-hover closing the deeper level, and click-to-run, then the
left-flip at the screen edge by resizing the window to 44 columns. The probe
was reverted afterwards; nothing in the app ships a submenu yet.

## 2026-08-19 — gosmo's Scripter grew the rest of the object families, item 4b

`Script … as ▸` needs more than the five things `Scripter` could script
(table, view, procedure, function, database), so gosmo gained the rest —
`ScriptTrigger`, `ScriptIndex`, `ScriptCheckConstraint`, `ScriptForeignKey`,
`ScriptSequence`, `ScriptSynonym`, `ScriptSchema`, `ScriptUser`,
`ScriptDatabaseRole`, plus a `ServerScripter` for the two principals that
belong to no database (`ScriptLogin`, `ScriptServerRole`) — and the DML
templates (`ScriptSelect/Insert/Update/Delete/Execute/FunctionCall`), which
needed `Database.Parameters` over `sys.parameters` since gosmo had no
parameter metadata at all. New files: `scripter_objects.go`,
`scripter_security.go`, `scripter_server.go`, `scripter_dml.go`.

`ScriptOptions.Verb` replaces the `ScriptDrops` bool as the general knob —
CREATE / DROP / DROP-and-CREATE / ALTER. `ScriptDrops` is still honoured
(it means `ScriptDrop` while `Verb` is unset), so no caller broke. The three
module scripters, which were near-identical copies, now share one
`scriptModule` behind a `moduleKind` table; the trigger scripter is the
fourth entry rather than a fourth copy.

Every builder is a pure function over metadata already read
(`buildSequenceScript`, `buildUserScript`, …), the pattern `buildTableScript`
already used, so the assembly is unit-tested without a server.

Decisions worth keeping:

- **A sequence is scripted from its *current* value, not its start value.**
  Restarting at `StartValue` hands out numbers the original already gave away.
- **A user's CREATE form comes from `AuthType`, not `UserType`.** An orphaned
  user — created `FOR LOGIN`, login later dropped — still reports
  `INSTANCE` with an empty `LoginName`, and scripting that as a contained or
  login-less user creates a different kind of principal.
- **`DROP LOGIN` and `DROP SERVER ROLE` have no `IF EXISTS` form**, unlike
  `DROP USER`/`DROP ROLE`/`DROP SCHEMA`; both drops are guarded with
  `SUSER_ID(...) IS NOT NULL` instead.
- **`CREATE SCHEMA` must be first in its batch**, so its existence guard is a
  dynamic `EXEC(N'CREATE SCHEMA …')` rather than an `IF` around the statement.
- **A disabled CHECK constraint is scripted `WITH NOCHECK` and then
  `NOCHECK CONSTRAINT`** — the first skips the check of existing rows, the
  second is what leaves it untrusted, as it was.

Verified live on win10cli, not just by unit test: a throwaway database with
one of every object, scripted at all four verbs, then the generated scripts
run back through `sqlcmd` — every DROP-and-CREATE executed, every CREATE ran
twice unchanged (the guards work), every ALTER applied, and the DML templates
ran once their placeholders were filled. That is what caught the one real
bug: `DECLARE [total] decimal(10,2)` in the EXEC template. A `DECLARE` takes
an `@`-prefixed name and nothing else, so bracket-quoting the stripped name
parsed as a *cursor* declaration and failed with "'decimal' is not a
recognized CURSOR option". No unit test asserting "contains DECLARE" would
have noticed. Everything was dropped afterwards.

Two failures during that run were SQL Server's own rules, not script defects,
and are expected: dropping a role that still has members, and `ScriptTable`
of a child table carrying its foreign key to a parent that doesn't exist in
the target database (SSMS scripts one table the same way).

## 2026-08-19 — `Script <Noun> as ▸` on every node, items 4c/4d

The gossms half of Phase 1 item 4: `internal/tui/scripting.go` is one table
saying which verbs each `NodeType` offers and how each is generated, plus the
three destinations SSMS gives them (New Query Editor Window / File... /
Clipboard). Sixteen node types script; a type absent from the table shows no
Script item at all, the same way `objectOps` decides who offers Delete and
Rename. `contextMenuItemsForNode` splices the cascade in above Rename/Delete
with the existing `insertBeforeRefresh`, so no per-type menu branch mentions
scripting — the three hardcoded "Script Table as CREATE"/"as DROP"/"Script
View as CREATE"/"Script Proc as CREATE" items and `scriptObject` are gone.

Each generator takes a `nodeData` **by value**, not the `*explorerNode`: it
runs on a background goroutine and the UI goroutine writes `node.data` (see
`applyNodeFilter`) — the same rule `objectOp` follows. Every destination
generates first and commits second, so a failure reports itself without
having prompted for a filename or overwritten the clipboard.

Two things the tree itself was missing:

- **A NodeCheck node carried no `TableName`.** `loadConstraintsChildren` never
  set it, so `ScriptCheckConstraint` had no table to name — and neither did
  Delete, whose `dropConstraint` has been emitting
  `ALTER TABLE [schema].[] DROP CONSTRAINT` for as long as it has existed.
  One line in the loader fixes both.
- **`nodeData.FuncType`**, because "Script Function as SELECT" produces a
  different template for a scalar function than for a table-valued one, and
  nothing downstream can recover that from the label. It needs one field more
  than `loadSchemaScoped`'s generic three, so functions got their own
  `loadFunctionNodes` rather than a fourth field bolted onto the shared shape.

**A system object offers no Script item, but a system database does.** A
catalog view's or system procedure's definition lives in the resource
database, which `sys.sql_modules` in a user database doesn't expose:
`ScriptView("sys","objects")` can only ever return "not found" (verified
live), so offering the item would be a menu entry that always fails. A system
database is assembled from metadata gossms can read, so `master` scripts like
any other database.

Verified live on win10cli against a throwaway `gossms_4c_probe` holding one
of every family plus a probe login and server role. The check that mattered
walked the **real Object Explorer loaders** from the server root down and ran
every verb the registry offers on every node they produced — 82 nodes, 200-odd
scripts, all non-empty and error-free. That is what proves the wiring rather
than the generation: a loader that forgets a field and a registry entry that
passes the wrong one both look fine in a unit test. The menu itself was driven
under tmux: the three-level cascade opens keyboard and mouse alike, and all
three destinations were exercised end to end — a file written to disk and read
back, a query window opened connected to the right database, and the clipboard
copy acknowledged. Everything was dropped afterwards.

## 2026-08-19 — Missing-index banner and `.sqlplan` files, Phase 1 item 5

Three pieces, all gossms-side — `internal/showplan` is gossms's own parser,
so gosmo isn't involved.

**`MissingIndex.CreateStatement()` / `.Script()`** (`internal/showplan/
missing_index.go`). The parser was folding INEQUALITY columns in with the
EQUALITY ones, which reads harmlessly and generates a wrong index: every
equality column has to come first or the index can't seek on them, and an
INEQUALITY column landing among them silently costs the seek the suggestion
existed to buy. `Inequality` is now its own field and `Keys()` is what orders
them. `Script()` follows SSMS: the impact as a comment, the database in a
`USE` (never as a three-part name in the CREATE, which doesn't parse), and
the DDL left commented out — the index name is still a `<Name of Missing
Index, sysname,>` placeholder, so it cannot run as it stands.

**The banner** (`internal/tui/planview/missing_index.go`). One green row
between the statement selector and the plan, showing the highest-impact
suggestion's CREATE statement and a `(+N more)` count. `m` or a click hands
every suggestion on the statement to the host via `OnMissingIndex`;
QueryPanel opens it in a query window on its own connection, PlanPanel on
whatever the tree points at (the script names its own database, so the panel
only decides where it would run). `m`, not Enter: Enter already toggles the
Properties strip in the Plan tab and collapses a subtree in the Tree tab.
The banner belongs to *one statement*, so `stepStatement` now re-runs
`layout` rather than just its `ensure*` tail — otherwise a statement with no
suggestion keeps the row reserved and the content below stays one row short.
A statement with no suggestion refuses `m` rather than swallowing it.

**`.sqlplan` open and save.** `File > Open` branches on the extension and
shows the plan in its own `PlanPanel` instead of a query panel full of XML;
it goes through `decodeTextFile` because SSMS writes `.sqlplan` as UTF-16 and
the BOM is what says so. `File > Save` on a plan panel writes the plan's
source XML — back to the file it came from without prompting, exactly as a
query panel's Save does — and `File > Save Execution Plan As...` covers a
query panel's Execution Plan tab as well, where plain Save still means the
script. The XML is written as UTF-8 rather than SSMS's UTF-16: it is the
document as decoded, and re-encoding it would need the `<?xml ... encoding?>`
declaration rewritten to match or the file would announce an encoding it
isn't in.

Verified live: a throwaway 7.5M-row `gossms_plan_probe` table queried on an
unindexed column produced a real 99.7%-impact suggestion; the banner rendered
it, the click opened the full script, and the generated
`CREATE NONCLUSTERED INDEX ... ([CustomerID],[Amount]) INCLUDE ([Note])` was
run for real against the table to prove it parses and applies. The plan was
then expanded to a panel, saved with Ctrl+S, reopened through File > Open as
a plan panel with its banner intact, and saved again in place. Probe database
dropped afterwards.

## 2026-08-19 — Partition functions/schemes, security policies, Always Encrypted keys, Phase 1 item 6

Four families gosmo could already read but the tree never showed. A database
gained a **Storage** folder (Partition Functions, Partition Schemes) and its
**Security** folder gained **Security Policies** and **Always Encrypted Keys**
(Column Master Keys, Column Encryption Keys) — eleven node types, their
loaders, icons, name-filter entries, Delete, read-only Properties, a Detail
Browser view each, and entries in item 4's Script registry. A security policy
also gets Enable/Disable; disabling one is confirmed first, since it stops
filtering and every row of the tables it protects becomes visible.

The Script entries needed gosmo scripters that didn't exist:
`ScriptPartitionFunction`, `ScriptPartitionScheme`, `ScriptSecurityPolicy`,
`ScriptColumnMasterKey`, `ScriptColumnEncryptionKey`. Three gosmo model gaps
came with them, all of them things a script cannot be correct without:

- **`ColumnMasterKey.Signature`.** `ENCLAVE_COMPUTATIONS (SIGNATURE = 0x…)`
  takes the key's own signature verbatim; nothing can recompute it, so an
  enclave-enabled key was unscriptable.
- **`ColumnEncryptionKey.Values`.** The old query returned one row *per
  encrypted value*, so a key mid-rotation (encrypted under two master keys)
  appeared twice and would have shown twice in the tree. The rows are now
  folded into one key with its values, and `MasterKeyName`/
  `EncryptionAlgorithm` still describe the first, so no caller broke.
- **`SecurityPolicy.IsSchemaBound`.** Part of the CREATE; a policy recreated
  without it binds nothing and lets the tables underneath change.

Three defects only running the generated SQL could show, all found that way:

- **`sys.partition_range_values` hands a date boundary back as
  `Jan  1 2026`** under the default `CAST(... AS NVARCHAR)`, which loses any
  time part. Now `CONVERT(..., 126)` — ISO 8601, ignored for non-date types.
- **A predicate definition is parenthesised in the catalog** —
  `([sec].[fn]([TenantID]))` — and `ADD FILTER PREDICATE` takes a function
  call, not an expression: it fails with "Incorrect syntax near '('".
  `unwrapPredicate` strips exactly one wrapping pair, and only when the first
  `(` is the one the last `)` closes.
- A boundary value of a non-numeric type has to be re-quoted, or
  `FOR VALUES (2026-01-01)` is arithmetic.

Verified live on win10cli against a throwaway `gossms_p6_probe` holding two
partition functions (int and date), a partition scheme across two filegroups,
a partitioned table, a row-level security policy with a FILTER and a BLOCK
predicate, and an Always Encrypted CMK/CEK pair. Every node was walked
through the **real loaders**, scripted at all three verbs, and the generated
CREATEs were then run into a second empty database — every family came back
with the same shape, the policy included, predicates and all. The tree itself
was driven under tmux: all five Properties dialogs opened, Detail Browser
rows checked against the catalog, a Script cascade run to a query window, and
Disable/Enable round-tripped against `sys.security_policies`. Both databases
dropped afterwards.

Known gap, unchanged by this work: `ScriptTable` doesn't emit a partitioned
table's `ON <scheme>(<column>)` clause, so scripting one puts it back on the
default filegroup. Recorded in `docs/open-threads.md`.

## 2026-08-19 — Loaders read a snapshot, not the live tree node

A cross-repo review pass. Most of what it turned up was already right; four
things were not, and only two of those were real bugs.

**The one that mattered: `node.data` was being read on loader goroutines.**
The hazard was already known and defended in three places — `deleteObject` and
`runRename` copy `nodeData` before handing it off, `scripting.go` says in its
header comment that generators take a `nodeData` by value "for the same reason
objectOp's do", and `savedFilters` has `filterMu` precisely because
`fetchChildren` restores from it off the UI goroutine. The gap was the loaders
themselves: `App.loadChildren` passed the live `*explorerNode` into
`safegoRepair` and `fetchChildren` read `node.data.Type` and
`node.data.Filter` from it, and `DetailBrowser.fetch` did the same through
`fetchNodeDetails`, `loadTablesFolderDetails`, `loadDatabasesFolderDetails` and
`loadLoginsDetails`. Meanwhile `applyNodeFilter` writes `node.data.Filter` on
the UI goroutine. Apply a filter while an expand or a detail fetch is in
flight and that is a data race, not a stale read.

The fix is `explorerNode.snapshot()` — a detached copy carrying `label` and
`data`, taken on the UI goroutine and handed to the goroutine in the live
node's place. The live node still travels alongside as the identity the posted
callback keys off (`endLoad`, `SetChildren`, `DetailBrowser.pending`), but
nothing dereferences it off the UI goroutine any more. `id`, `parent` and
`children` are deliberately dropped from the copy: no loader reads them, and
leaving them out is what stops a snapshot being mistaken for a tree node.
Checked first that no `childLoader` touches anything but `.data` and `.label` —
every `node.parent` read in the package is inside a `postAndWake` callback.

`filterChildren` and `filterObjects` now take the `*nodeFilter` rather than the
folder node, which is what makes the remaining call sites honest about where
they are reading it from.

**How it was verified, since green tests would have proved nothing here.** The
three new tests in `explorer_snapshot_test.go` pass under `-race`, but so did
the broken code — no test drove both goroutines. So the pre-fix shape was
reconstructed in a scratch test (`filterChildren(node.data.Filter, …)` from a
goroutine while the main one writes it) and confirmed to trip the detector:
three `WARNING: DATA RACE`, `FAIL`. The shipped shape does not. That A/B is
the actual evidence; the assertions are what keeps it from regressing.
Then driven live against win10cli end to end: expand, filter the Databases
folder on `Name contains Health`, and watch *both* halves narrow — the tree to
`Databases (filtered)` and the Detail Browser to the one row, sizes still
backfilling into the right rows. Script Table as ▸ CREATE To ▸ New Query
Editor Window still produces the table's DDL through all three cascade levels.

**`Peer` read the racy `closed` flag it documents as unreadable.** `peer.go`
explains, at the point where it checks `sc.Context().Err()`, that `Peer` runs
on loader goroutines and `closed` is written by `Close` on the UI goroutine —
and then two lines above, `p.IsOpen()` reads exactly that flag on a cached
peer. `closePeers` calls `p.Close()` outside `peerMu`, so nothing synchronises
the two. Now `peerLive(p)`, which asks the context instead. Deliberately a
function and not a method: `IsOpen` remains the right answer for UI-goroutine
callers, and `peerLive` exists only because that answer cannot be read from
where `Peer` runs. Functionally this changed nothing today — `closePeers` nils
the map, so the `Context().Err()` check downstream already caught it — which is
why it is filed as UB rather than as a shipped bug.

**Two smaller ones.** `MenuBar.HandleMouse`'s "on the bar row but off every
label" branch set `openMenu = -1` directly instead of calling `Close()`,
leaving `cascade` populated behind a closed dropdown; latent, since every
reopen path resets it first, but it was the one place in the new cascade work
that skipped the reset. And `showplan`'s missing-index script hand-rolled
`"[" + name + "]"` in three places without doubling an embedded `]` — now one
`bracket` helper. Not routed through `gosmo.QuoteName` on purpose:
`internal/showplan` has no dependencies at all today, and reaching for
`QuoteName` would pull the mssql driver in behind it to quote a table name.

Deferred with reasons in `docs/open-threads.md`: `logStatus` writing the Status
History ring from background goroutines (same class, separate fix), the five
`findX` helpers that scan a whole collection because gosmo never got their
`ByName` finders, and the dual dialog registration in `buildUI`.

## 2026-08-19 — The five missing gosmo finders, and `registerDialog`

The two items the correctness pass deferred, picked up in order.

**Five families were resolved by scanning their own listing.**
`findPartitionFunction`, `findPartitionScheme`, `findSecurityPolicy`,
`findColumnMasterKey` and `findColumnEncryptionKey` each fetched every object
of their kind and linear-scanned for one name — on every Properties open and
every Detail Browser selection. Each said so in its own doc comment, and that
is the workaround CLAUDE.md § Changing gosmo says not to settle for: gosmo
already had thirteen `XByNameContext` finders and these five families were
simply the ones that never got one.

Added to gosmo, additively — every bulk listing stays: `PartitionFunctionBy
Name{,Context}`, `PartitionSchemeByName{,Context}` (`partition.go`),
`ColumnMasterKeyByName{,Context}`, `ColumnEncryptionKeyByName{,Context}`,
`SecurityPolicyByName{,Context}` (`security.go`). Each listing's SQL was split
into a shared `xSelect` const the listing finishes with `ORDER BY` and the
finder with a `WHERE`, plus a `scanX` the two share, so the two reads cannot
drift apart in what they populate. The `==` name comparison the gossms helpers
used is gone with them: matching happens in SQL now, under the server's own
collation, which is the semantics a name comparison should have had.

Two shapes needed more than the `certificate.go` template:

- **Column encryption keys** are one row per encrypted value — a key mid
  master-key-rotation is two rows — so the finder uses `query` and the shared
  fold, not `queryRow`, and its not-found answer is an empty fold rather than
  `sql.ErrNoRows`.
- **Security policies** need a second query for their predicates. That load
  moved out of the listing's row loop (where it ran a query while the outer
  rows were still open) into a pass over the scanned policies, and the finder
  calls the same `loadSecurityPredicates`. A policy loaded without them
  scripts as an empty `CREATE SECURITY POLICY`, so the live test asserts the
  predicate specifically.

All five report a missing name with `notFoundf`, matching the thirteen that
predate them, not `certificate.go`'s documented `(nil, nil)` exception.

The five gossms helpers survive as four-line wrappers — they exist only to
save the `DatabaseByNameContext` step at their eleven call sites. One
behaviour narrowed on purpose: `findSecurityPolicy` used to treat an empty
schema as "match any", and gosmo's finder requires the schema. Every security
policy node carries one, since `loadSecurityPoliciesChildren` reads it from
`SCHEMA_NAME`.

**Verification.** `live_bynamefinders_test.go` (`-tags livedb`) creates a
throwaway database with one object of each of the five kinds and asserts each
finder returns what its listing returns, field by field — the whole point
being that they must not disagree — plus `errors.Is(err, ErrNotFound)` for a
missing name in all five. Passes against win10cli. Then driven through the
built binary on the same objects: all five render in the Detail Browser
(partition function boundaries `100, 200, 300`; the policy's `FILTER`
predicate; the CEK's encrypted value byte-identical to the one created), and
Column Encryption Key Properties opens on its grid. Database dropped after.

**Every dialog was registered twice.** `buildUI` constructed 30 dialogs into
30 named `App` fields and then hand-listed all 30 again in `allDialogs`. A
dialog in one list and not the other is constructed but never drawn or given
input, which had already happened once and is why
`TestEveryAppDialogFieldIsRegisteredInAllDialogs` exists. Now
`a.x = registerDialog(a, NewX(a))` — one expression that assigns and appends,
so the class is gone rather than tested for. The test stays: the helper makes
skipping registration awkward, not impossible. Registration order is
byte-identical to the old list (three constructions were reordered to keep it
so), which matters only as the same-tick tie-break `syncDialogStack` uses.

**Three small ones alongside.** `detail_browser_storage.go` said "four
families" over a list of five. `scripting.go` named a foreign key "Key" in the
script menu where the drop/rename table calls it "Foreign Key" — now "Script
Foreign Key as", verified in the live tree. And `generateScript` latched the
status line to "Scripting…" before its goroutine and cleared it only from the
posted callback, so a panic left the app claiming to still be working:
`safegoRepair` now, per the rule in CLAUDE.md § Mouse, overlays, and async UI.
