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

## 2026-08-20 — Ctrl+X in the Find dialog cut the query editor's text

Found in a cross-repo review pass and reproduced under tmux before touching
anything: type `SELECT 1 FROM T`, `Ctrl+A`, `Ctrl+F`, `Ctrl+X` — the Find
field is untouched, the editor behind the dialog is emptied, and the status
bar says "Cut to clipboard". Nothing on screen says what was cut or from
where.

The cause is the two halves of the clipboard path disagreeing about who is in
front. `App.handleKey` consumes Ctrl+C/X/V centrally, *before*
`topDialog().HandleKey`, because `SetClipboard`/`GetClipboard` are Screen
methods only the application layer has — so no dialog ever sees the key.
`activeClipboardTarget` then resolved the target from a switch naming
`fileDialog`, `propDialog` and `connectDialog`, and **fell through to
`activeQueryPanel().editor` for the other twenty-seven**. Find/Replace,
Backup, Restore, Options, Filter, the Rename prompt, the typed-confirm box and
all ten New-<object> dialogs were each aiming Ctrl+X at whatever was behind
them. `widgets.InputField` implements the target interface perfectly well; it
simply never got asked.

The fix is `core.ClipboardHost` — `FocusedClipboardTarget() ClipboardTarget`
— asked of the frontmost dialog, with the fall-through deleted:

- The interface pair lives in `tuikit/core` because tuikit's own dialogs
  (`FileDialog`, `PromptDialog`, `TypedConfirmDialog`) have to be able to
  return one, and two identical interfaces declared in two packages are not
  the same type to a method signature. `tui.clipboardTarget` is now an alias
  of `core.ClipboardTarget`, so every existing call site is unchanged.
- **An open dialog owns the clipboard outright**: its focused field, or
  nothing. A dialog with no text entry (Help, About, Confirm, Query List)
  deliberately does not implement the interface, and Ctrl+C there is now inert
  rather than reaching past it. That is the point of the change, not a
  side-effect of it.
- `propsheet.PropertySheet` returns *itself* — its clipboard methods already
  resolve through the focused row — which makes `PropDialog` and all ten
  New-<object> dialogs hosts by embedding, with no per-dialog code.
- The flat-slice dialogs (Connect, Find/Replace, Backup, Restore, Filter)
  share `focusedClipboardTarget(list, idx)` in `dialog_common.go`. Backup and
  Restore gate on `mode`: their progress/inspect/files views drive their own
  focus and answer nil. Every miss arm returns an **explicit** nil — a typed
  nil `*InputField` in an interface is not a nil interface, and every caller
  tests the result against nil.
- Resolving through `topDialog()` also fixes an ordering assumption nobody had
  noticed: the old switch gave `fileDialog` priority by hand, which happened
  to be right, where the stack is right by construction for any nesting.

`TestEveryDialogWithTextEntryIsAClipboardHost` walks each registered dialog's
*type* for a `*widgets.InputField`, `*controls.Editor` or
`*propsheet.PropertySheet` and fails if it isn't a host — type-level, so it
covers dialogs that don't exist yet, and it skips `*App` to break the cycle
every dialog has back to it. Verified to bite by renaming one method away.
Then re-driven under tmux: the same four keystrokes now cut the Find field and
leave the editor alone, paste lands in the field, and the editor's own
Ctrl+A/Ctrl+X with no dialog open still works.

Noticed while there and **not** fixed, since it predates this and is
unrelated: the Connect dialog's Connection String preview doesn't track the
Server field as it is typed — `sqlserver://:1433?...` with `myserver01` in the
box, on the pre-fix binary too.

## 2026-08-20 — The panel drop-down ran off the bottom of the screen

Open more query panels than the terminal has rows and the tab bar's `[v]`
list became unusable. `PanelManager.Draw` drew one row per panel starting at
`contentY()`, with no bound: on a 20-row terminal with 24 panels it painted
Query 1 through Query 16 and stopped where the screen did. Everything past
that was unreachable — nothing was drawn to click, and the list didn't scroll,
so Up/Down moved `active` off the visible window without moving the window.
The *active* panel itself was among the missing ones. Reproduced on the
pre-fix binary side by side with the fix.

The list is now capped to the rows between the tab bar and the bottom of the
panel manager, and scrolls:

- `comboGeom()` returns origin, width and usable height in one call, the way
  `tabSegments()` already does for the tab row. Both `Draw` and `HandleMouse`
  take their math from it, so a click lands on the entry drawn under it —
  `HandleMouse` had its own copy of the `rect.W-30`/`28` literals, and the
  fix only works because there is now one of them. The width is also clamped
  to `rect.W`, which the literal `28` was not.
- `comboScroll` is the first visible row. `scrollComboToActive()` moves it by
  the least that brings `active` into view, called when the combo opens and
  after each Up/Down. `Draw` re-clamps it, which is what survives a resize
  shrinking the list under an offset that was in range when it was set.
- A click maps to `row + comboScroll`; the wheel scrolls the list while the
  pointer is over it; a scrollbar takes the list's last column, and the
  labels give up that column only when there is one.
- `comboHover` went with it — set to -1 in the constructor and read nowhere,
  since before this the list had no hover state to track.

Five tests in `panel_manager_combo_test.go` assert against a recording screen
rather than recomputing the geometry they are checking: the list stops at the
last usable row and paints nothing below it, arrowing to the end scrolls the
window and keeps the active title drawn somewhere in it, a click activates the
panel whose title was captured at that row, the wheel saturates at both ends,
and a short list still draws every panel with no scrollbar column stolen.
Verified to bite by reverting the height to `len(pm.panels)` (first test) and
by dropping `+ pm.comboScroll` from the click (third).

## 2026-08-20 — gosmo: a scripted sequence restart moved the handle anyway

`Sequence.RestartContext` assigned `seq.CurrentValue = value` straight after
the ALTER. Under `WithScript` the ALTER is only captured, so the handle came
back claiming a current value the server would not hand out for another five
thousand calls — exactly the state-mirroring hazard `setIfApplied` exists for,
on the one field a caller reads back immediately. Now
`setIfApplied(ctx, &seq.CurrentValue, value)`.

`NextValueContext` assigns the same field directly and is left alone: that is
the value the server just returned, and reads run against the real server
under `WithScript` too. Said so in a comment, since the two now look
inconsistent side by side.

Swept the rest of gosmo for the same shape — every method that reaches an
`exec`/`execContext` and then writes a receiver field — and this was the only
one. `JobStep.UpdateContext` does it under an explicit `if !Scripting(ctx)`
because it mirrors a whole struct's worth at once, and is correct.

`TestScriptedSequenceRestartDoesNotMoveCurrentValue` pins both halves against
the capture harness (the statement text, with a quote-hostile schema and name,
and the untouched field). `TestLiveScriptedSequenceRestartMirroring`
(`-tags livedb`) does it on a real server in three phases: scripted moves
neither handle nor `sys.sequences`; the captured statement run by hand does
move the server; a real restart moves both and `NEXT VALUE FOR` then returns
the value the handle claims. Both verified to bite by reverting the one line —
the live one failed with "scripted restart moved the handle to 5001, want it
left at 1" on win10cli. Throwaway database dropped; `sys.databases` confirmed
clean afterwards.

## 2026-08-20 — two unlatched Button1 handlers, and a drop-down that lost its place

From the cross-repo review of the same day. Both bugs are invariant 3 of the
`mouseDragging` idiom, at the two sites that never got one, and both were
proven with a throwaway probe before anything was changed.

`PanelManager`'s drop-down list: clicking an entry activated that panel and
closed the list, so from the next resent `Button1` on there was no branch left
to match the still-held press — it fell straight through to the panel the
click had just activated. Probe output was `aGot=[0 0 1]`: a press arriving in
a query editor that never saw it begin, at the coordinates of a list entry,
moving the caret or starting a selection drag. The latch alone is not enough
here, because the branch carrying it stops matching the moment the list
closes; the guard that actually holds is a check just above the fall-through
to the active panel — if `mouseDragging` is set, a press claimed by the tab
bar or the list owns the gesture and the panel does not see it. The click
*outside* the list still falls through on purpose (2026-08-18), and that path
sets no latch.

`planview`'s tree pane: the row click toggles when it lands on the already
selected row, so a held press expanded and collapsed a node once per motion
event, and a press on any other row selected it and then toggled it on the
very next resend — a click that so much as twitched collapsed what it had just
selected. `PlanView.mouseDragging` could not be reused: it is set
unconditionally on the way down to the content area, precisely so the XML
editor and the graph keep receiving the resent events a selection drag is made
of. Hence `treeState.rowDragging`, cleared in the same `ButtonNone` case as
`sbDragging`.

Third, smaller: the drop-down scrolls now (2026-08-19), but only its own
Up/Down kept the active row in view. `Ctrl+N` and `Ctrl+Tab` are consumed by
`App.handleKey` before the combo ever sees them, so the highlight could end up
outside the visible window with nothing on screen saying which panel was
active, and `RemovePanel` could leave the offset past the end of a shrunken
list. `scrollComboToActive` is now a no-op while the list is closed, which is
what lets every exported mutator call it unconditionally.

Each of the three tests was A/B'd by reverting its own fix: the panel got
`[1 1]` from the press that selected it, the tree row toggled under the hold,
and `comboScroll` sat at 20 against a maximum of 0.

## 2026-08-20 — the clipboard's write half, and gosmo's one scripted create that handed back nothing

Items 4-7 of the same review. The 2026-08-20 `ClipboardHost` work rewrote how
the clipboard *resolves* its target; these are the three things it left in the
half that *applies* the edit, plus an unrelated gosmo outlier.

**gosmo: `CreateDatabaseMirroringEndpointContext` returned `(nil, nil)` under
`WithScript`.** Every other scripted create in the library hands back a
name-only handle so the caller's next step can be collected too
(`CreateScheduleContext` has the reasoning). This one was pinned the other way
by `TestScriptedEndpointCreateReturnsNoHandle`, on the grounds that a handle
would have the caller address an endpoint the server does not have — which is
what a scripted caller is *for*. The cost was visible one repo over:
`ensureEndpoint` (`new_endpoint_dialog.go`) had `if ep == nil { return nil //
scripting }`, so Script Changes on the New Endpoint wizard emitted the CREATEs
and silently dropped every `GRANT CONNECT` behind them. The spec's defaults and
validation moved into `EndpointSpec.normalized`, which the statement and the
handle are now both built from, so the two cannot disagree about what was
asked for. `ConnectionAuth` and `Owner` are left empty deliberately: the first
is a server-side `*_desc` keyword rather than the spec's clause text, the
second is decided by whichever connection runs the script.

**Three hardcoded `connectDialog.updateMatches()` calls**, one at each edit
site — the same enumerate-the-dialogs shape the resolver had just been
rewritten to remove, and wrong in the same direction: it re-ran the
saved-connections lookup for a paste into *any* field of that dialog, so
pasting a password could pop the autocomplete list open over a server name
nobody had touched. Now `core.ClipboardEditHandler`, an optional companion to
`ClipboardHost`, handed the target that was actually edited.

**A paste re-resolved its target after the read.** Both clipboard reads are
asynchronous — the native tool is shelled out to on a goroutine, the OSC 52
reply arrives as an `EventClipboard` an unbounded time later — and the target
was resolved again when the text arrived rather than checked. Close the dialog
in between and the paste lands in the query editor behind it: the same failure
the read half had just been fixed for, through the other half of the same
operation. `App.pasteInto` now drops the text unless the intended widget is
still the target, and `App.pendingPaste` carries that widget across the OSC 52
round trip (which also means an unsolicited terminal reply pastes nothing).
The test failed before the fix with exactly the reported symptom: "the paste
landed in the query editor behind the dialog: "ubusql2"".

**`holdsTextEntry` was blind to interface-typed fields**, so a dialog keeping
its widgets in nothing but its `[]focusable` focus ring would have been
approved as owning no text entry, with an inert clipboard and a green test. A
probe confirmed the gap is latent — a value walk that follows interfaces,
slices and maps finds exactly the same set of dialogs today — so this is a trap
closed, not a bug fixed. The walk is now over the built dialogs' values, with
the type walk kept as the fallback for a nil pointer, and each half has a test
asserting what it can and cannot see.

## 2026-08-20 — The panel drop-down's scrollbar, wheel, and paging

Three defects a review pass turned up in the capped combo list, all in the
same unshipped change that gave it a scroll offset. Each was A/B'd twice: the
test fails with the fix reverted, and the behaviour was reproduced against the
built binary under tmux with 31 panels open (Escape past Connect, 30 × Ctrl+N,
then SGR mouse sequences via `tmux send-keys -H`).

**The scrollbar was drawn but inert.** `panel_manager.go` had become the only
`core.DrawScrollbar` call site in either repo with no `HandleScrollbarDrag`
behind it — every other bar in the app (editor, treeview, listbox, datagrid,
propsheet form, planview, activity monitor, and `FileDialog` through
`ModalDialog.ScrollbarDrag`) is draggable. Grabbing this one did nothing
useful: the bar sits in the list's last column, so the press fell to the row
hit-test, selected whatever panel was drawn behind it and dismissed the list.

Two placement decisions came with the fix, and both are load-bearing:

- The drag runs **ahead of the row hit-test**, or grabbing the bar still picks
  the row behind it.
- It also runs **ahead of the `mx < pm.rect.X` bounds check**, because
  `HandleScrollbarDrag`'s latch is meant to own the mouse for the whole
  gesture wherever the cursor drifts to. Verified live: press on the bar at
  row 5, then a motion event at column 40 — far off the bar — still scrolled
  the list, to the bottom.
- The latch is its own field, `comboSbDragging`, **not** `mouseDragging`.
  Sharing it would let the press that opened the combo satisfy
  `HandleScrollbarDrag`'s "already dragging" test, turning the next motion
  event anywhere into a jump to that row. Both clear on `ButtonNone`.

**The wheel fell through an open drop-down to the panel underneath.**
`HandleKey` already gave the overlay first refusal of every key while
`comboOpen`; `HandleMouse` did not, and a wheel event is not `Button1`, so it
skipped the dismiss branch and reached `ActivePanel().HandleMouse` — the query
editor scrolled under a list still floating over it. The wheel is now consumed
anywhere inside the manager while the list is open, and scrolls the list.
Note what did *not* change: a `Button1` outside the list still dismisses and
falls through, which was decided deliberately on 2026-08-18 and matches
`widgets.DropDown`. Only the wheel was along for the ride.

**No PageUp/PageDown/Home/End.** Harmless while the list drew one row per
panel; once it was capped and scrollable, walking 40 panels was 40 keypresses,
and the list swallowed those keys rather than refusing them. `moveComboSelection`
now backs Up/Down/PgUp/PgDn/Home/End alike. It **clamps rather than wraps** —
`Next`/`Prev` wrap because Ctrl+Tab is a cycle, but a list the user is looking
at stops at its ends like every other list in the app — and it moves the active
panel immediately, which is what Up/Down have always done: the drop-down
previews its selection rather than waiting for Enter.

One thing deliberately left alone: the thumb never sits flush at the bottom of
the track when scrolled to the maximum. That is `core.DrawScrollbar`'s thumb
math, shared by every scrollbar in the app, and it is consistent everywhere —
not a defect of this list.

## 2026-08-20 — The second batch of by-name finders

The 2026-08-19 pass replaced five fetch-the-listing-then-scan lookups with
real gosmo finders and left five behind. This is the rest of them:
`findSchema` (`schema_props.go`), `findIndex` (`index_props.go`),
`findStatistic` (`statistics_props.go`), `findForeignKey` (`fk_props.go`) and
the Change Tracking page's table lookup (`table_props.go`). Each fetched every
schema / index / statistic / foreign key / tracked table in scope and compared
names in Go to keep exactly one. Three of the five carried a comment naming
the missing gosmo method as the reason.

gosmo gained five methods, additively, with every bulk listing kept:

- `Database.SchemaByNameContext`
- `Table.IndexByNameContext`
- `Table.StatisticByNameContext`
- `Table.ForeignKeyByNameContext`
- `Database.TableChangeTrackingForContext`

Each listing's SQL moved into a package-level `…Select` const that the listing
and the finder both build on — `schemaSelect`, `statisticSelect`,
`foreignKeySelect`, `tableChangeTrackingSelect` — so the two can only ever
disagree in the predicate. Where the listing already had a row scanner inline
it became a `scanX(scan func(...any) error)` helper the two share. Same shape
as `partitionFunctionSelect`/`scanPartitionFunction` from the first batch.

`IndexByNameContext` is the one that is not a one-line predicate.
`IndexesContext` is deliberately two queries — the indexes, then *every* index
column on the object at once — because fetching columns inside the loop cost a
query per index on its own pooled connection. The finder has to keep that
shape rather than collapse it, so `indexListContext` and `indexColumnsContext`
each gained an `extra string, args ...any` predicate appended to the object
filter (parameters starting at `@p2`), and the column-distribution half became
`attachIndexColumns`. The finder narrows the first query by `i.name` and the
second by the `index_id` the first returned. An index that loads without its
columns is not a cosmetic loss: the Index Properties pages script from them, so
it would script as a `CREATE INDEX` with empty parens — which is why the live
test asserts the key *and* included columns, not just the index row.

`TableChangeTrackingForContext` is the odd one. Its listing is a `LEFT JOIN`
onto `sys.change_tracking_tables`, so a table with tracking switched **off**
still produces a row, and the finder has to preserve that: off is a value, not
an absence. Only a table that isn't in `sys.tables` at all is `ErrNotFound`.
The gossms page keeps its old tolerance explicitly — on `ErrNotFound` it falls
back to a zero `TableChangeTracking`, so an ms-shipped table or one dropped
since the tree was populated still shows as "off" rather than failing the page.

As with the first batch, matching moved from Go's `==` into SQL, so it now
happens under the server's collation. Every one of these names comes from a
gosmo listing by way of an Object Explorer node, so it is already byte-exact;
the change only widens what would match, never narrows it.

### How it was verified

Three ways, because green unit tests prove nothing here — none of this code
has a fake to run against.

`gosmo/live_bynamefinders_test.go` gained
`TestLiveSchemaAndTableChildFindersMatchTheirListings`, the same shape as the
first batch's: a throwaway database with a schema, a parent/child pair, an FK
with `ON DELETE CASCADE`, a `NONCLUSTERED … DESC INCLUDE` index, a user
statistic, and change tracking on at database level but on only one of the two
tables. Each finder is compared field by field against the listing that used to
be scanned, and every one answers a missing name with `ErrNotFound`.

`gossms/internal/tui/live_propfinders_test.go` is new, and does the same for
the five gossms helpers themselves — the finders as the property dialogs call
them, through a real `db.ServerConn`. It builds its own schema so the expected
values are known, rather than asserting against whatever HealthClinic happens
to contain. Both files are `-tags livedb` and skip without `-livedb`.

Then the pages themselves, driven under tmux against win10cli: Schema
Properties (`SchemaByNameContext` — owner, principal type and schema ID all
populated), Table Properties > Change Tracking (a table with tracking off,
reading OFF/OFF as before rather than erroring), and Index Properties on
`IX_Appointments_DoctorID` (the General page's Key columns grid populated,
which is the narrowed second query arriving).

Five listing call sites in `internal/tui` still fetch a whole collection, and
all five are correct: they populate a list or a drop-down, or count.

## 2026-08-20 — Schema Properties' Object summary, in one query

The General page's six-row Object summary — Tables, Views, Stored procedures,
Functions, Synonyms, Sequences — was built by fetching every view, procedure,
function, synonym and sequence **in the database** and counting the ones whose
`Schema` matched, plus a schema-scoped table listing for the sixth. Six
listings, five of them database-wide, to produce six integers. On a database
with a few thousand routines that is the page's entire load time, and the
definitions come back with them: `ViewsContext` and `StoredProceduresContext`
both join `sys.sql_modules` and select `m.definition`, so the page was pulling
every view and procedure's full text across the wire to discover how many
there were.

gosmo gained `Schema.ObjectCountsByType`/`Context`, returning a
`SchemaObjectCounts` struct, and the page now makes one round trip for all
six.

It is **not** a `GROUP BY o.type` over `sys.objects`, which was the obvious
shape and the wrong one. The six listings do not differ only in type code:
three join `sys.sql_modules` (which is what keeps a CLR or extended procedure
out of the stored-procedure count), synonyms and sequences have catalog views
of their own and filter no `is_ms_shipped`, and tables do. A grouped query
would have produced numbers that were defensible but different from the ones
the page has always shown. So it is six scalar subqueries in one SELECT, each
reproducing its own listing's predicate — one round trip, same answers by
construction.

It is also deliberately a separate method from the existing
`Schema.ObjectCount`, which is a single `COUNT(*)` over `sys.objects` used by
the Owned Schemas page. The two do not agree and are not meant to: `ObjectCount`
counts constraints and primary keys as objects too. Widening it would have
silently changed that page.

The page still fetches users, roles and schema permissions — the Owner
drop-down needs the full principal list, and the Permission summary needs each
permission's state, not a count. Nine fetches down to four.

### How it was verified

`gosmo/live_schemacounts_test.go` (`-tags livedb`) is the A/B: it builds two
schemas with tables, views, procedures (one `WITH ENCRYPTION`), all three
function flavours, synonyms and a sequence, then asserts
`ObjectCountsByTypeContext` equals what the six listings-and-scan produced, for
`app`, `other` and `dbo`. Two schemas because a count that ignored the schema
filter still looks right against one. It also pins the literal expected numbers
so a bug that broke both paths identically still fails, and checks that a
non-existent schema yields zeros rather than an error — `SCHEMA_ID` returns
NULL and every subquery counts nothing.

Then the page itself, against HealthClinic's `dbo` under tmux: 8 / 3 / 3 / 0 /
0 / 0, matching the same six counts run through `sqlcmd` by hand. That last
step is what catches a struct field wired to the wrong row, which no equality
test on the gosmo side can see.

`TablesBySchemaContext` now has no gossms caller. It stays — gosmo is a
library, and this is exactly the case the no-removal rule is about.

## 2026-08-20 — ScriptDatabase's missing Context sibling, and the bug behind it

`Scripter.ScriptDatabase` was the only one of gosmo's 27 `Script*` methods
with no `Context` variant, which showed up in gossms as the one odd entry in
`scripting.go`'s otherwise uniform dispatch table:

```go
scriptDB scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) { return s.ScriptDatabase() }
```

It had none because it needed none: alone among the `Script*` methods it runs
no query, rendering instead from the `Database`'s own cached `name`,
`collation`, `recoveryModel` and `compatLevel`. Adding a `ctx` it ignored would
have been symmetry for its own sake.

Except that rendering from cached metadata is exactly the problem.
`Server.Database(name)` is documented as a lightweight handle with zero-valued
metadata — the only form that works under a `WithScript` context — and
`ScriptDatabase` rendered it anyway:

```sql
ALTER DATABASE [x] SET RECOVERY ;
GO
ALTER DATABASE [x] SET COMPATIBILITY_LEVEL = 0;
GO
```

Neither line parses. gossms never hit it — `scripting.go`'s `ddl` always goes
through `DatabaseByNameContext`, which fills the metadata in — but gosmo is a
library, and `NewScripter(srv.Database("Sales"), opts).ScriptDatabase()` is the
obvious thing for a consumer to write.

So the `Context` sibling earns its context: `ScriptDatabaseContext` refills a
handle that has no recovery model or compatibility level from `sys.databases`
before rendering. Two further points, each deliberate:

- The emit is *still* guarded per line. A refresh can succeed and come back
  empty — `sys.databases` reports NULL `recovery_model_desc` and
  `compatibility_level` for a database that is OFFLINE or otherwise
  inaccessible — and a script short one setting is recoverable where a script
  that doesn't parse is not.
- Rendering split into an unexported `scriptDatabaseFrom(d)` that reads
  nothing. That is what makes the guard testable without a server, which is
  the whole reason the guard is more than a hopeful `if`.

### How it was verified

Three unit tests (`scripter_database_test.go`) cover rendering from cached
metadata, the two guards, and the double-quoting of a name that reaches the
script twice — `IF DB_ID(N'O''Brien')` as a literal and `[O'Brien]` as an
identifier. The guard test was A/B'd against the unguarded emit and produces
exactly the two broken lines above.

`live_scriptdatabase_test.go` (`-tags livedb`) is the one that matters. It
creates a throwaway database, sets it to `BULK_LOGGED`, then scripts it twice —
once from a fetched `*Database`, once from a bare `srv.Database(name)` — and
requires the two to be byte-identical. Then it drops the database and **replays
the generated script batch by batch**, asserting the database comes back with
`BULK_LOGGED`. A script that "looks right" and a script that runs are different
claims; this makes the second one. A/B'd the same way: with the refresh
disabled the bare-handle script emits `SET RECOVERY ;` and fails.

The gossms side is one line — `scriptDB` now passes its `ctx` like the other
twenty-six. Verified under tmux: Script Database as > CREATE To > New Query
Editor on HealthClinic still produces the same script it always did
(`RECOVERY SIMPLE`, `COMPATIBILITY_LEVEL = 170`), which is the expected
no-change, since that path was never the broken one.

## 2026-08-20 — Dragged column widths survive a redraw

Two of the three items left over from the review pass, both about
`redrawGrid` (`prop_grid_helpers.go`).

**A column the user had dragged snapped back on the next redraw.**
`DataGrid.SetSource` clears `colWidthOverride` along with selection and
scroll, and it is right to: the next result set's column 2 has nothing to do
with this one's. `redrawGrid` exists precisely because that reset is wrong for
a Properties page redrawing its *own* fixed columns, and it already restored
the cell cursor — the widths were the half it missed. So a user who widened
Name on Job Properties > Schedules lost it the moment they toggled a row.

`redrawGrid` now snapshots the overrides and reapplies them, through a new
`DataGrid.ColumnWidthOverrides()` — the inverse of the `SetColumnWidth` that
was already exported. A width restored past the new column count is dropped by
`SetColumnWidth`'s own bounds check, so a redraw that genuinely reshapes the
grid still starts clean; there is a test for that as well as for the
preservation.

**`agent_job_props_schedules.go` was the last site hand-rolling the idiom.**
Its `OnActivateCell` did `SetData` + `SetSelectedCell(row, col)` inline. It was
correct — the same pattern that shipped as a bug on six other sites in August,
but here the cursor really was put back — and it is now `redrawGrid`, which
means it picks up the width fix for free. The swap is behaviour-preserving:
both mouse and keyboard set `selRow`/`selCol` before `activateCell` fires, so
the `SelectedCell()` `redrawGrid` reads back is the `(row, col)` the callback
was handed.

Verified live against win10cli under tmux: Job Properties > Schedules, drag
the Name/Enabled separator right (Name goes 35 → 53 columns), then click a row
to toggle Attached. The toggle takes, the cursor stays, and the width holds.
The unit test was A/B'd against the un-snapshotted `redrawGrid` and fails with
`column 1 override = 0 after the redraw`.

That live run turned up something else, recorded in `docs/open-threads.md` and
not fixed here: on a grid with `SetCellCursor(true)`, a click moves the
selection without ever firing `OnSelectRow`, so every detail panel wired to it
lags a click behind. It is a `DataGrid.HandleMouse` branch that reaches every
grid in the app, which is not something a column-width change should carry.

## 2026-08-20 — A click that moved the highlight and told nobody

Fixing this was the whole reason the previous entry's live run was worth
doing. `DataGrid.HandleMouse`'s `Button1` handler has two exits. The plain-row
one fires `OnSelectRow`; the cell-cursor one set `selRow`/`selCol`, called
`activateCell()` and returned `true` — never firing it at all. So on any grid
with `SetCellCursor(true)`, a click moved the highlight and the page was never
told: the detail panel below went on describing whatever row the *keyboard*
had last left it on. Eleven pages wire `OnSelectRow` onto a cell-cursor grid.

Two decisions in the fix, both load-bearing:

- **Select, then activate** — the order the keyboard already uses (arrow keys
  fire `OnSelectRow`; Enter separately calls `activateCell`). A page syncs its
  detail widgets to the new row, and only then does the toggle run against
  that row. The other order leaves the toggle running while the page still
  believes it is on the old row.
- **Only when `selRow` actually moved**, matching the keyboard path's `moved`
  gate and the callback's own documented contract ("fires whenever the
  selected row changes"). Unconditional firing would re-enter the selection
  callback on every toggle click of the row already selected — and on a
  `wireGridEditor` page that callback commits the detail editor back into the
  row, so a repeat click would fight the toggle.

Three tests: that a click fires it, that select precedes activate, and that a
second click on the same row activates again without re-firing. All three
A/B'd against the pre-fix branch. Verified live on Job Properties > Schedules:
clicking `CollectorSchedule_Every_60min`'s Attached cell now both toggles it
and moves "Selected schedule" onto it, where before the panel stayed on
whatever Down had last reached.

## 2026-08-20 — A paste that followed focus to the next row

`PropertySheet` answers `FocusedClipboardTarget` with **itself** and resolves
the real row on every call. That is what makes the Properties dialog and all
ten New-<object> dialogs clipboard hosts without each saying so, and it is
right for Copy and Cut, which are synchronous.

It defeats the paste guard. `App.pasteInto` protects an asynchronous clipboard
read by checking the target it was handed is still the active one — and
against a self-resolving host that check passes unconditionally, because the
sheet is the target whichever row has focus. A paste aimed at Name and
delivered after the user tabbed to Description landed in Description.

The fix is one optional interface, `core.ClipboardTargetTokener`, alongside the
`ClipboardEditHandler` that was already there:

```go
type ClipboardTargetTokener interface{ ClipboardTargetToken() any }
```

`PropertySheet` returns the focused `ClipboardRow`; `pasteFromClipboard`
captures the token beside the target and `pasteInto` compares both. The token
is opaque — compared with `==`, never interpreted. A host that returns a
distinct target per field needs none of it, and implements nothing.

Two alternatives were weighed and rejected. Returning the focused *row* from
`FocusedClipboardTarget` is cleaner in principle but `ClipboardRow` and
`core.ClipboardTarget` are different interfaces, and the sheet's
`HasSelection`/`SelectedText` deliberately fall back to row-level copy text (a
static row's value, a checkbox's state, a grid's selected cell) that no single
row implements — it turns a paste bug into a rewrite of the sheet's whole
clipboard path. Re-resolving the target at delivery instead of checking it is
the failure `pasteInto`'s own comment already documents.

`TestASelfTargetingClipboardHostCarriesAToken` is the guard for next time, and
it recognises the shape rather than naming `PropertySheet`: a host whose
`FocusedClipboardTarget` returns something that is itself a `ClipboardHost`
answered with itself, and must carry a token. Removing `PropertySheet`'s
method fails it on all eleven dialogs by name. The paste tests were A/B'd
against the unguarded `pasteInto`, which puts the text in the field focus moved
to.

The race itself cannot be triggered by hand — it needs a clipboard read to
outlast a focus change — so the unit tests are the evidence that it is closed.
What the live run against win10cli settles is the other half: Ctrl+V into Job
Properties > General's Name field still pastes, so the new guard does not
refuse ordinary pastes. Escape discarded it; the job is still `test_job` on the
server.

## 2026-08-20 — A grid redraw put the row the user was on at the bottom

Found by a cross-repo review pass, not by a report. `redrawGrid`
(`prop_grid_helpers.go`) restored the cell cursor and the dragged column
widths across a `SetData`, but not the scroll position — and restoring the
cursor alone does not bring it back. `SetSelectedCell` ends in `ensureVisible`,
which scrolls to the selection from wherever it finds the view; from the zero
`SetSource` had just left, that means scrolling *just far enough to reach* the
selected row, which lands it against the bottom edge.

So every toggle on a scrolled grid moved the list. Reproduced live on
win10cli, Server Properties > Permissions, with the pre-fix binary: with
`ALTER ANY LINKED SERVER` selected at the **top** of the seven-row viewport,
Space set its State to Grant and scrolled six unrelated rows in above it,
leaving the row the user had just acted on at the **bottom**. The same
sequence on the fixed binary does not move the view at all. Both halves of
that A/B are what the change is worth; the unit tests alone would not have
shown it, and `TestSetDataThenSetSelectedCellStillMovesTheView` now pins the
old arithmetic so the difference stays visible.

Two things came out of the fix beyond the scroll itself.

**The restore is one primitive now, on the widget that owns the state.**
`controls.DataGrid.SetDataPreservingView` does the saving and restoring;
`redrawGrid` is a wrapper over it, kept because CLAUDE.md's rule names it and
because a Properties page saying `redrawGrid` reads as the page-level idiom it
is. That mattered immediately: `propsheet.ToggleGridRow.activateCell` had the
same defect and could not call `redrawGrid` at all, since that lives in the
application layer and `ToggleGridRow` is in tuikit. It was the seventh site.
`Revert` there preserves the view too — Ctrl+Z restores the values of rows
already on screen, so the row set has not changed, only what it says. `SetRows`
still resets, because its rows *are* a different set.

**Order is load-bearing, twice.** Widths go back before the scroll, because
`ensureVisibleCol` decides the horizontal offset by walking `colWidths` and
would otherwise decide it against the recomputed defaults — the pre-fix code
restored widths *after* `SetSelectedCell` and had this wrong too, silently.
Scroll goes back before the selection, for the `ensureVisible` reason above.

`DataGrid.SetScroll` is new, and its own clamp had a bug that a test caught
before the code ever ran: bounding `scrollRow` by `rows.Len()-1` rather than by
the last row that can sit at the *top* of the viewport let a redraw that shrank
the list leave a two-row grid scrolled one row down, drawing a blank line above
its only visible row. It now uses the wheel's bound. `ScrollRow`/`ScrollCol`
had both documented "there is deliberately no setter"; that reasoning was about
hosts *driving* the view, which `SetScroll` still says not to do — restoring
what `SetSource` discarded is the other thing, and is the same argument
`ColumnWidthOverrides` already carried.

Six Properties pages had hand-rolled the pair — `SetData` then
`SetSelectedCell(row, col)` from inside `OnActivateCell` — and so lost the
dragged widths on every toggle as well: `securables_matrix.go` (both grids),
`server_permissions_matrix.go` (both), `agent_job_props_alerts.go`,
`new_login_pages.go`. All now call `redrawGrid`.

Also fixed in the same pass: `pasteFromClipboard`'s doc comment still said the
OSC 52 reply is "handled in Run(), which re-resolves `activeClipboardTarget()`
and calls its Paste method". Re-resolving on arrival is precisely the bug the
`ClipboardHost` work removed — `Run` uses `pendingPaste`/`pendingPasteToken`
now — so the comment described the failure as if it were the design.

## 2026-08-20 — the drop-down's scrollbar latch outlived the drop-down

Second finding from the same review pass. `PanelManager.comboSbDragging` was
cleared in exactly one place, the `ButtonNone` arm at the top of `HandleMouse`.
That covers the ordinary end of a drag and nothing else: `Escape` or `Enter`
while the scrollbar is still held closes the list with the latch set, and
`core.HandleScrollbarDrag` then takes the *next* opening's first `Button1` —
anywhere on screen, since a latched drag deliberately runs ahead of the bounds
check — and jumps the scroll to whatever row the pointer is over.

This is CLAUDE.md's invariant 4, "a latch must not survive into the widget's
next showing". `ModalDialog.Show()` enforces it for dialogs; `PanelManager` has
no `Show` to hang it on.

The fix is a `setComboOpen` that owns the transition, the way `setActiveIndex`
already owns the active index in the same file — seven sites assigned
`comboOpen` directly, and clearing the latch at each of them is the version
that goes stale. `mouseDragging` is deliberately *not* cleared there: it
belongs to the press that claimed the gesture, not to the list, and releasing a
still-held press back to the panel underneath is what the catch-all at the end
of `HandleMouse` exists to prevent. `TestComboOpeningKeepsTheGestureLatch`
pins that half.

Worth recording about the tests, because the first attempt was wrong: a
sub-case that forced `comboSbDragging = true` and then clicked a tab failed,
and the failure was correct. While the latch is set the scrollbar branch owns
every `Button1`, so no mouse-driven close is reachable mid-drag at all — the
only two ways to strand the latch are Escape and Enter, and the release that
ends a real drag clears it anyway. Testing a state the code cannot reach is
noise; the two tests now drive the two real paths, and both fail with the
one-line clear removed.

Also corrected in `docs/open-threads.md`, since a file whose job is to be
trusted had drifted: `internal/tuikit/layout` is no longer "the least-covered
tuikit package (46.6%)" — it is 73.7% and second-highest, with `core`,
`dialogs` and `propsheet` now below it — and `activity_monitor_proctab.go`'s
trailing-period status strings are three, not "the only one".

## 2026-08-20 — the schedule form's encoders, and what a round-trip test can't see

The last open item from the review pass was `internal/tui`'s uncovered write
paths. The review had recorded these as reachable already, via the
`gosmo.WithScript` + nil-`Server` harness the New-X dialog tests use. Checking
before writing anything showed that was wrong, and the correction is in
`docs/open-threads.md`: a Properties page's `load` *and* `apply` both open with
a by-name lookup, lookups are reads, and `WithScript` only intercepts writes.
With a nil `Server` they panic rather than script. `gosmo.Server` holds one
unexported `*sql.DB` and is only constructible through `Connect`, so there is
nothing to fake either.

What that leaves reachable is the layer the closures call into: the
form-to-request encoders. `agent_schedule_form.go` is the worked example and
the densest of them — 251 lines, every function at 0%, and it is what New
Schedule and Schedule Properties both encode an msdb schedule through.

`populate` and `readFrequency` are two separate switches over `FreqType`, and
they only agree because the parallel label/code tables they share are inverses.
The obvious test is the round-trip: populate a `gosmo.Schedule`, read it back,
compare. That went in over all seven `FreqType`s. The case worth having is
`FreqMonthlyRelative`, where `FreqInterval` is a relative *day code* and
`FreqRelativeInterval` the occurrence, read from two different Select rows — a
mismatched pair turns "last weekday of the month" into "the 16th" with nothing
reported anywhere, because `FreqInterval`'s meaning is `FreqType`-dependent and
every value is valid for *some* type.

Two others came out of reading the apply closure rather than the encoder.
Schedule Properties gates `SetFrequencyContext` on `frequencyDirty()` and
`SetActiveRangeContext` on `rangeDirty()`, so **`populate` leaving any row
dirty makes every OK rewrite the schedule** — and not harmlessly: on the
FreqTypes where `populate` substitutes an in-range default for a stored field
that doesn't apply, the default gets written back as fact. `weekdaysGrid` is the
one at risk, filled through `SetRows` rather than a `SetValue`, so it needs its
own baseline reset. It has one. Pinned now, along with the two predicates not
overlapping — if either widened to the whole form, editing a start time would
write the frequency too.

**The part worth remembering.** Mutation-testing the round-trip found it has a
blind spot that no amount of extra cases would fix. Swapping two entries in
`weekdayBits` — so the checkbox labelled Monday sets Tuesday's bit — passes.
Both halves read the same table, so the fault cancels: `setWeekdayGrid` writes
through the wrong index and `weekdayMask` reads back through the same wrong
index. A round-trip proves two functions are inverses of each other, never that
either is *correct*. What catches it is naming the pairs — a test asserting
`"Monday"` maps to `gosmo.WeekdayMonday`, for all five dropdown tables, plus a
length check on each parallel pair since a label added to one slice and not the
other is otherwise silently absorbed (a short value slice makes
`readFrequency`'s bounds guards write a zero code; a short label slice hides a
real option). Both mutations fail loudly now.

`TestEveryScheduleFormRowIsReachable` is the last one, and reflective on
purpose: it walks `scheduleFreqForm`'s fields and asserts each is either spliced
in by `rows()` or placed by the caller in its own identity section. A field
added to the struct and wired into `readFrequency` but forgotten in `rows()` is
invisible and uneditable, yet still written on every Apply — pinned to whatever
the constructor defaulted it to. A test listing the rows by hand would have been
updated in the same edit that forgot the row; walking the struct can't be. It
compares by `reflect.Value.Pointer()` rather than `Interface()`, which panics on
the unexported fields.

Every test here was checked by mutation, not just run: six deliberate faults
(the Monthly branch reading the wrong row, the weekday table drifting, `populate`
using an edit setter instead of the load setter, a label added to one slice
only, a row dropped from `rows()`, a caller-placed row spliced in twice), each
confirmed to fail the test that should catch it and only that one.

## 2026-08-20 — the Steps page's encoder, and one extraction to reach the delete order

Same treatment as `agent_schedule_form.go`, on `agent_job_props_steps.go`.
Everything outside the page closure — `jobStepEditFromStep`, `editable`,
`changed`, `request`, `stepNumberText` — went from 0% to 100% with no
production change. `pageJobSteps` itself stays at 0%: it opens with
`findAgentJob`, and that is the read a unit test cannot serve (see
`docs/open-threads.md`).

The `changed()` test is the one that earns its keep, and it is the same
drift-proofing shape as the schedule form's `rows()` test. `changed()` gates
the entire update pass, so a field that `request()` sends but `changed()`
doesn't compare is one the user can edit and never save — silently, page
showing the new value, server keeping the old. The test walks `jobStepEdit`
by reflection, finds every field with an `orig<Name>` mirror, and fails if
one isn't in its edit table. Reflection can't *set* unexported fields, so the
mutations stay hand-written; reflection only decides whether the hand-written
set is still complete. That is the half that drifts.

`OutputFileName` got its own test because gosmo reads two of
`JobStepRequest`'s string fields' empty values in opposite directions, and
gossms depends on both: an empty `Database` means "leave the step's own
alone" (which is what the `(unchanged)` sentinel maps to), while an empty
`OutputFileName` means "null the column" — blanking the field is how a user
clears it. Both documented on the gosmo type; pinned here because a
well-meaning "treat blank as no-op" in `changed()` would break exactly one of
them.

**The one production change.** The three-pass write order in `apply` — updates,
then deletes in descending `step_id`, then adds — is the most consequential
logic in the file and it was sealed inside the load closure, so no test could
reach it. It came out as `planJobStepWrites`, a pure function over
`[]*jobStepEdit` returning the three slices; `apply` now runs three short
loops over the plan. Behaviour is unchanged — verified by reading the
classification back against the original predicates, and the diff is in git.

Why it was worth extracting rather than leaving alone: `sp_delete_jobstep`
renumbers every later step's `step_id` down by one, so ascending deletes are
a live data bug, not a style question. Delete step 2 of {1,2,3,4} and the old
step 4 becomes 3; the next delete aimed at 4 either fails with "not found" or,
once something has slid into that number, **succeeds against a step the user
never selected**. `apply` re-fetches the step list by ID between passes, so
the wrong-step case is reachable rather than hypothetical. Sorting ascending
is a one-character edit that nothing else would catch.

Six mutations, each confirmed to fail only the test that should catch it:
deletes sorted ascending, `changed()` dropping `outputFileName`, `request()`
dropping `OutputFileName`, the `!editable` guard removed from the plan, the
on-action labels reordered, and `request()` rewriting every subsystem to TSQL.
The label reordering is the shared-table blind spot again — `commitCurrent`
writes `Selected()+1` and `syncFieldsFromSelection` reads `SetSelected(action-1)`,
so a reordered list round-trips perfectly while every dropdown reads wrong:
the user picks "quit reporting success" and the step is stored as "quit
reporting failure". Naming the pairs is what catches it, per CLAUDE.md.

## 2026-08-20 — the Files page's encoder, and a silently discarded edit

Third file in the same pass. `fileEditFromInfo`, `growthText` and
`maxSizeText` were already package-level and went to 100% as they stood;
`pageDatabaseFiles` stays at 0%, opening as it does with
`DatabaseByNameContext`.

Three methods came out of the load closure, the same shape as
`planJobStepWrites`: `fileEdit.changed`, `.modify` and `.spec`. `changed()` is
the one that was worth extracting for its own sake rather than for the test —
the six-field comparison existed **twice**, once in `GridRow.DirtyFn` and once
inline in `apply`, and the two must agree. A field added to one and not the
other gives either a page that never reports itself dirty (OK writes nothing)
or one that is always dirty (OK always writes). They did agree; they no longer
can disagree.

`modify()` is the interesting half. Every field is left zero unless it
actually changed, because gosmo reads a zero as "leave this property alone"
and omits it. Resending the unchanged current value looks harmless and is not:
`SIZE` is a grow-to target, and `MODIFY FILE` rejects a value at or below the
file's current size. A user editing only the autogrowth of a file that has
since grown past its recorded size would get "Specified size is less than or
equal to current size" for an edit they never made. That is now pinned from
both sides — a growth-only edit must send no `SIZE`, and a size edit must.

The complement is `TestFileEditIgnoresTheFieldsModifyFileCannotChange`:
`fileType`, `fileGroup` and `path` deliberately have no `orig` mirror, because
`MODIFY FILE` cannot retype a file, move it between filegroups, or move it on
disk. The reflection check in the `changed()` test keys off exactly that —
mirrored fields must be compared, unmirrored ones must not be — so the two
tests state one rule from both ends.

### Found, not fixed: autogrowth cannot be turned off

Reading `gosmo.buildAlterFileStatement` to get `modify()`'s contract right
turned up a real bug, confirmed by calling the builder directly:

    buildAlterFileStatement("db", "f", FileModify{GrowthKB: 0}) == "", nil

`FILEGROWTH = 0` is how SQL Server disables autogrowth, and it is what SSMS
emits when "Enable Autogrowth" is unchecked. `FileModify` cannot express it:
the builder's guards are `GrowthPercent > 0` / `GrowthKB > 0`, so a zero is
indistinguishable from "don't touch the growth". The Files page's Growth
amount spinner has a minimum of 0, so the user *can* ask for it — and when
nothing else on the file changed, the whole statement collapses to the bare
`NAME =` clause, `AlterFileContext` returns nil on the empty string, and the
page reports a successful Apply that did nothing. The grid then shows "0 MB"
until the reload puts the old value back.

Not fixed here: the gap is in `FileModify`, so the fix is a gosmo addition
(an explicit `DisableGrowth bool`, or making the fields pointers) — an API
decision, and one to verify live before shipping. `AddFile` has the same
blind spot, where a zero means "server default" rather than "disabled".
Recorded in `docs/open-threads.md`.

Six mutations, each failing only its own test: `modify()` resending `SIZE`
unconditionally, `changed()` dropping `maxSizeKB`, `changed()` gaining `path`,
`spec()` sending a filegroup for a LOG file, `modify()` sending both growth
halves, and `growthText` choosing by value instead of by the flag.

## 2026-08-20 — the backup and restore encoders close out the series

Last of the form-to-request encoders. No production change was needed here:
everything wanted was already package-level or reachable off a
widget-only dialog.

**Backup.** `currentOptions` reads seven widgets and nothing else, so a
`&BackupDialog{}` with just those constructed the way `show()` builds them is
enough — no App, no screen, no connection. Better still,
`gosmo.BuildBackupStatement` is exported and pure, so the assertions are on
the T-SQL that would actually run rather than on an intermediate struct that
could still build the wrong statement.

Three things there were worth pinning beyond coverage. The Backup Type
radio's *index* is the only record of which `gosmo.BackupAction` it means —
the shared-table blind spot again, so the pairs are named. `Init` is
hardcoded true because the dialog has no "append to media set" option;
dropping it silently turns every backup into an appending one, and a media
set growing without bound is not something a user notices quickly. And an
unchecked box must drop its clause rather than emit a negated one:
`NO_COMPRESSION` is a different instruction from saying nothing and letting
the server default apply, and the dialog offers no third state — which is
why `Compression` is a `*bool` and the nil case is asserted directly.

`sqlStringLiteral` got the security-flavoured treatment it deserves. It is
the only place in the backup files that builds SQL from a value the user did
not type, `backupHistoryQuery` opens the result in a query window, and msdb
runs whatever comes out. Besides the doubling itself, the test asserts the
finished statement has an even number of quotes for every input — an odd
count is what an escape failure looks like from the outside, whatever form
it takes.

**Restore.** The relocation encoder — `relocateFiles`, the thing that builds
the `MOVE` clauses — was already at 100% from earlier work, so what was left
was the accessors around it. `plannedPaths` is the one worth having: it is
the Files view's *preview*, and its whole documented claim is that it cannot
drift from what the restore does, because both go through `relocateFiles`.
The test states that as an equality against `relocateFiles` rather than by
restating expected paths, so a change to the relocation rules updates both
sides at once and only a genuine divergence fails. Mutating `plannedPaths` to
ignore the relocation mode fails it immediately; a test listing expected
paths would have had to be hand-updated for every rule change and would have
rotted instead.

One test-setup trap worth recording: `DropDown.SetSelected` clamps against
the items it holds, so seeding a dropdown with nil items and then selecting
index 1 silently leaves it on 0. The first draft of
`TestDeviceForRestoreFollowsTheSourceRadio` failed for that reason and not
because of any product defect — the fix was to give the dropdown the items
`loadHistory` would have put there.

Nine mutations, each failing only its own test: the Differential/Log radio
indices swapped, `currentOptions` dropping `Init`, a blank destination
becoming one empty device, `sqlStringLiteral` not doubling quotes,
`taskTimes` measuring a finished task to now, `deviceForRestore` always
reading the typed file, `relocation` not trimming the log folder,
`plannedPaths` ignoring the relocation mode, and `autoFillTarget` always
overwriting.

With this, `internal/tui` is at 29.6% (from 28.1% when the pass started).
The number understates it: what moved is the write paths, and every
form-to-request encoder in the application is now covered. What remains
uncovered is dominated by the `propPage` load closures, which are blocked on
a server, and by draw/layout code.

## 2026-08-20 — autogrowth could not be turned off, and the seam that proves it now can

Two findings from the encoder pass were reported rather than fixed, because
both needed a gosmo API decision. Both are now done. They turned out to be one
piece of work: the first is a bug that only shows itself at the page level, and
the second is what makes the page level testable.

### FILEGROWTH = 0 was inexpressible

`FILEGROWTH = 0` is how SQL Server records autogrowth-off, and what SSMS emits
when "Enable Autogrowth" is cleared. `gosmo.FileModify` could not ask for it.
The struct's zero value means "leave this property alone" —
`buildAlterFileStatement` guards on `GrowthKB > 0` / `GrowthPercent > 0` — so
the value that disables growth and the value that omits the clause are the same
value, and the omission won.

What made it worse than a missing clause: the Files page's Growth amount
spinner bottoms out at 0, so a user can ask for it, and when growth was the
only thing they changed the whole statement collapsed to the bare identifying
`NAME =`. gosmo returns `""` for that by design, `AlterFileContext` returns nil
on `""`, and the page reported a successful Apply. The grid went on showing the
value the user typed until the next reload put the old one back.

The fix is additive on both sides, since nothing about the existing behaviour
was wrong — only incomplete. `FileModify` and `DatabaseFileSpec` each grew a
`DisableGrowth bool` that emits `FILEGROWTH = 0` and takes precedence over the
amount fields; `writeFileSizeClauses` is shared with CREATE DATABASE's file
definitions, so the ADD FILE and CREATE halves came along for free. In gossms,
`fileEdit.growthOff()` names the condition once and both `modify()` and
`spec()` branch on it. `growthText` now renders a zero growth as "None": after
the fix, zero is a state the user can choose deliberately, and "0 MB" next to
six columns of real values reads as a field nobody filled in.

`TestDisableGrowthIsNotTheZeroValue` in gosmo pins the distinction from the
other side — collapsing the two, by emitting `FILEGROWTH = 0` whenever growth
is zero, would switch autogrowth off on every ALTER that only meant to resize.

### gosmo.NewServer, and the Properties pages becoming reachable

The `propPage` load and apply closures had been the largest untested surface in
`internal/tui` and the one where a mistake writes to a production database. The
`gosmo.WithScript` harness the New-X dialogs use cannot reach them: both halves
open with a by-name read, `WithScript` intercepts writes only, and with a nil
`Server` the read panics. There was no seam either — `gosmo.Server`'s only
field is an unexported `*sql.DB` and the only constructors built a real
go-mssqldb connector.

`gosmo.NewServer(ctx, *sql.DB)` is that seam: it wraps a pool the caller
opened, loading the same metadata `ConnectContext` does. It is the inverse of
the `DB()` accessor that was already there, and useful beyond tests — a pool
shared with the rest of an application, or a driver wrapped for tracing.

`internal/tui/fakedb_test.go` is the gossms half: a `database/sql` driver that
answers queries by substring match from a scripted table and records every
statement executed, keyed by DSN so each test gets its own instance. Around it,
`newFakeConn` (hands back a `*db.ServerConn`), `loadPage` (fails naming the
queries the script missed, which is what a half-scripted page actually does),
and `textRow` (finds a row by label, so a test drives a page the way a user
does rather than by an index into `Rows()` that moves whenever a field is
added). `TextRow.Label()` and `InputField.Label()` were added for that last
one.

`database_props_files_page_test.go` is the worked example, and it is the test
the autogrowth bug needed: it runs the real load and apply closures and reads
back what reached the server. Every encoder-level assertion around
`fileEdit.modify` had passed while the page as a whole did nothing.

Be clear about what it proves. Queries are matched by substring and answered
with whatever was scripted, so a test there shows the page asked for the right
things and built the right request — never that the T-SQL is valid or that the
server would accept it. Statement text belongs to gosmo's own tests, which
build the string and assert it; acceptance belongs to a live run. An assertion
in the fake that reaches for server semantics is asserting the fake.

### Mutations

Six, five caught by exactly one test each: gosmo dropping the `DisableGrowth`
case from MODIFY FILE, `growthOff` never firing, `modify` resending the
unchanged size, `apply` addressing a renamed file by its new name, and
`growthOff` always firing.

The sixth is worth recording because it *missed*, and the reason is a real
property rather than a gap. Making `changed()` always true does not make an
untouched page write: `modify()` still returns an empty `FileModify`, gosmo
builds no statement from one carrying only the identifying `NAME`, and nothing
is executed. Two independent guards have to fail together, which is why either
one alone leaves `TestFilesPageWritesNothingWhenNothingChanged` green —
mutating both does fail it, with the two files resending their own current
`SIZE`, which MODIFY FILE reads as a grow-to target and rejects. The note is on
the test so nobody "fixes" it later.

## 2026-08-20 — five Properties pages driven end to end, and what the harness had to grow first

With the fake-driver seam in place, the pages worth pointing it at are the ones
whose apply can destroy something or can do the wrong thing without saying so.
Five, in that order of risk: Database > Options, Login > Status, Login > Server
Roles, Securables (the server permission matrix), and Database > Filegroups.
Files was already done.

### The harness needed a way to make a row dirty, and there wasn't one

The first attempt looked like it worked and proved nothing. Every apply closure
gates its write on `Dirty()`, and `SetValue`/`SetSelected` move the baseline
along with the value — so a row set that way is *clean*, apply skips it, and a
test asserting "the right statement was written" fails while a test asserting
"nothing was written" passes for entirely the wrong reason. The only ways to
dirty a row were a keystroke and a paste, both of which need a focused widget:
focus reaches a widget only through `Draw`, tcell v3 has no simulation screen
(which is why this codebase's widget tests are state-only), and the mouse path
needs hit geometry the dropdown keeps unexported.

So `propsheet` grew `Edit` on `TextRow`, `SelectRow` and `RadioRow` — set the
value the way a keystroke does: value changes, row goes dirty, OnChange fires.
It is a real gap rather than a test hook: "set the value" and "the user changed
the value" are different operations in this package and neither could stand in
for the other. `rows_edit_test.go` pins the pair in both directions, including
that `SetSelected` does *not* fire OnChange (it is programmatic and would
re-enter whatever set it) and that an out-of-range `Edit` cannot leave a row
permanently dirty.

Around it: `Label()` on `InputField`, `DropDown`, `RadioBox` and their rows, so
a test addresses a page by label instead of by an index into `Rows()` that moves
whenever a field is added; `ToggleGridRow.Text()` and `.Toggle()`, so a
checkbox grid can be driven by row name and through the real toggle path
including `OnToggle`; and `typeInto` for the filter boxes, which are
deliberately `SetDirtyTracked(false)` and must stay clean.

One harness bug worth recording: `Statements()` dropped anything starting with
`USE `, to hide the plumbing gosmo issues before a database-scoped read. That
also hid every server-scoped grant, because SQL Server only accepts those from
master and gosmo prefixes them `USE master;` in the same batch. The filter now
anchors on a bare `USE [db]` and nothing else.

### What the tests are actually for

Two shapes recur across these pages, and both are invisible to a test written
the obvious way.

**Index-parallel read-back.** Every one of these pages builds a grid from a
slice and reads it back positionally — `for i, v := range grid.Values()` against
`roles[i]`. The checkbox and the thing it acts on are related by nothing but
position. So every assertion here toggles *by name* and asserts on the name in
the statement: a test that also worked in indices would agree with a page that
had them misaligned. On Server Roles that is the difference between ticking
sysadmin and granting sysadmin.

**A subset that diverges from the full list.** Securables keeps `visible`, a
filtered slice, while its write loop walks the whole catalog; Filegroups keeps
one that shrinks as rows are marked for removal. The hazard only exists once
the two lists differ, which means a single-item test passes on the broken
version — mutating `vis[row].pendingRemove` to `edits[row].pendingRemove`
survived every Filegroups test until one removed a *second* filegroup after the
first was already pending. That case is now
`TestRemovingTwoFilegroupsRemovesBothTheOnesSelected`.

Page-specific things worth naming: Options' twenty-one
label/`DatabaseOption`/items triples are asserted one by one, plus a count check
so a row added to the page and not to the table cannot slip through untested;
Restrict access is pinned as staying *out* of that tracked table, because the
generic option path would drop `WITH ROLLBACK IMMEDIATE` and leave
`SET SINGLE_USER` blocking on other connections — a dialog that appears to hang,
on a database nobody can reach; Login > Status pins `GRANT_WITH_GRANT_OPTION`
reading back as Grant, since falling through to Default would show a granted
login as unset and then REVOKE it on the next Apply; Securables pins that
another principal's grant is not read onto this page.

### Mutations

Fifteen, each failing only its own test: two Options labels swapped against
their options; Restrict access moved into the tracked table; the Grant/Deny
arms swapped; Enable/Disable inverted; `GRANT_WITH_GRANT_OPTION` falling
through to Default; server-role membership matched by substring instead of by
element; the role grid read back off by one; permission cell edits applied
against the unfiltered index; the state map ignoring which principal holds the
permission; Grant With Grant losing its option; REVOKE losing CASCADE; the two
filegroup toggle columns swapped; Remove resolved against the wrong list;
`syncToggles` walking `edits` instead of `visible`; and filegroup state written
for every row rather than the changed ones.

`internal/tui` is at 31.9%, from 30.3%. The pages themselves: Options 82.1%,
Server Roles 83.0%, Securables 82.5%, Filegroups 75.0%, Login Status 72.3%.

### Two findings fell out of it, and both are now fixed

**Silently truncated labels.** `propsheet` pads every Text/Password/Int/Select
label with `core.PadRight(label, LabelWidth)` and clips Static's with
`DrawTextClipped` at the same 30 columns. Both hard-clip with no ellipsis, so
an over-long label renders as a shorter, different label and the page looks
finished. Found while trying to address the Options page by label from a test:
"Auto update statistics asynchronously" renders as "Auto update statistics
asynchr", immediately below "Auto update statistics" — two rows that set
different options, reading as the same one.

Seven call sites, five distinct labels, all shortened to fit: "Recurs every
(day/week/month)", "Every N (per Daily frequency)", "Auto update statistics
async", "Custom: stale threshold", "Required sync secondaries". The AG one lost
"to commit", which its own page Note already spells out.

The rewording is the smaller half. `TestNoPropertySheetLabelIsTruncated` is the
fix: it parses every non-test file in `internal/tui` with `go/ast`, finds calls
to the five width-constrained constructors and the three application wrappers
that pass a label straight through, and fails on any string literal wider than
`LabelWidth`, printing what the label will actually render as. Static analysis
rather than built forms, because most pages need a live server to construct and
the labels are literals — so the question is answerable for every page at once.
`Check`, `Radio`, `Section`, `Note` and `Hint` are deliberately excluded: their
text is drawn on its own line at full width.

It also guards itself. A test that walks the AST looking for names can pass by
finding nothing, so it fails if it checked fewer than a hundred labels —
renaming `propsheet.Int` out from under it is caught, which was the second
mutation.

Neither `PadRight` nor `DrawTextClipped` grew an ellipsis. `PadRight` promises
exactly *n* columns and every fixed-width grid cell in the app depends on the
hard clip; the guard removes the need. What stays unguarded is a label built at
run time, of which there are none near the limit today.

**gosmo's deprecated filegroup keywords.** `SetFileGroupReadOnlyContext` emitted
`MODIFY FILEGROUP ... READONLY`/`READWRITE`. SQL Server accepts those only for
backward compatibility and documents them as slated for removal; `READ_ONLY`/
`READ_WRITE` are the current spellings. One `mode :=` pair in
`database_files.go`, with the reason on the function so it is not "simplified"
back. Not run against win10cli — the underscored forms are the documented
primary spelling, but this particular statement has only been exercised through
the script collector and the fake driver.

## 2026-08-21 — the next nine write pages, and two silent no-ops they found

Continuing the fake-driver page tests from 2026-08-20, in the order
`docs/open-threads.md` named: Login > User Mapping, Database Scoped
Configurations, Server Properties Memory / Processors / Advanced, Database and
Server Role Members, User Membership, Change Tracking. Nine pages, six new test
files, 24 mutations run and all of them caught after the two described below
were closed.

**What the harness needed to reach them, and why each was a real gap.**

A `fakeResponse` can now be scoped to the database a `USE` has pinned the
connection to (`db:`) and to a query parameter (`arg:`). Both came out of
wrong answers, not tidiness. Without `db:`, every database on a page gets the
identical answer to the per-database query gosmo runs in each of them, so the
misalignment these tests exist to catch is unreachable — appdb and salesdb have
to be able to differ. Without `arg:`, a by-name read is served the *list*
read's answer, because `DatabaseByName`'s query contains `FROM sys.databases`
too: every database on the Processors and User Mapping pages resolved to
whichever row sorted first, and the affinity tests failed asserting on
`min server memory (MB)`. Responses are tried in order, so the by-name one goes
first; the same ordering rule settles the role list against the role-members
list, whose query embeds the other as a subquery.

`fakeConn` also implements `driver.SessionResetter`, clearing the pinned
database when the pool hands the connection back out. A real server would keep
the last `USE`, but gosmo issues its own ahead of every database-scoped read,
so the only thing carrying it over could do here is let a database-scoped
answer serve a server-scoped query.

`StatementsIn(db)` is the other half. `Statements()` strips the bare `USE`,
which is right — it is not a write — but that also strips the only record of
*where* a write went, and `DROP USER [appuser]` is correct in one database and
data loss in the next. Every User Mapping assertion is `assertOneStatementIn`,
paired with `assertNoStatementsIn` on the databases that should not have been
touched: a test that names only the intended database cannot see the other
half of a per-database write.

Pages whose grid is a `controls.DataGrid` rather than a `propsheet.ToggleGridRow`
— User Mapping's database list, both Role Members pages — have no `Toggle` to
call, and `SetSelectedRow`/`SetSelectedCell` deliberately do not fire
`OnSelectRow`. `plainGrid`, `selectGridRow` and `activateGridCell` drive one
with the keys a user sends, which is also the only way to exercise the
commit-and-redraw wiring the page hangs off `OnSelectRow`.

`propsheet.CheckRow` gained `Edit`/`Label`, for the reason `TextRow` and
`SelectRow` gained theirs: `SetChecked` moves the dirty baseline with the value,
so a row set that way is clean and apply skips it.

**A half-scripted fake produces an empty page, not a failure.** `eachDatabase`
(`db_scan.go`) drops a database whose per-database read fails rather than
failing the page, and gosmo's `userMappingsIn` skips one the same way — both
deliberate, both documented on themselves. The consequence for a test is that
an unscripted query yields a User Mapping page with zero rows and a passing
apply. `loadMappingPage` therefore starts by insisting the grid has three rows.
Anything built on `eachDatabase` needs the same guard.

**Finding: a scoped-configuration or sp_configure option this server does not
have got a live control that did nothing.** Both `newScopedConfigBoolEditor`
and `newConfigBoolEditor` returned an ordinary dropdown/checkbox when
`findScopedConfig`/`findConfig` came back nil, and left it out of the page's
tracked-rows slice. The Int editors beside them already rendered a disabled
"N/A" row for the same case. So on any instance missing the option — half the
scoped-configuration options postdate SQL Server 2019, and `sys.configurations`
is edition-dependent — a user could switch `PARAMETER_SNIFFING` on or tick
`xp_cmdshell`, press OK, be told it succeeded, and have nothing sent. That is
the "never let a control silently do nothing" rule one level down from the
menus it is usually stated about. Both now return a `propsheet.Row` and render
the same disabled N/A row as their Int counterparts.

Pinned by `TestScopedConfigOffersNoControlForAnOptionTheServerDoesNotHave` and
`TestSpConfigureOffersNoControlForAnOptionTheServerDoesNotHave` — both assert
through the *widget*, not the statement log, because the old behaviour wrote
nothing either and "no statement" passes on the bug.

**A missed mutant, and the rule it produced.** Rewiring User Membership's apply
to `roles[0].Name` — grant the first role whatever row was ticked — passed.
The test toggled `db_owner`, which was the first row. Every one of these tests
now acts on an object that is *not* first in its list, and Change Tracking's
fixture has three tables in two schemas for the same reason. The rule is in
`docs/open-threads.md` beside the two index hazards it belongs with.

The affinity grid is the one pairing on these pages a round-trip test cannot
reach: `affinityBits` and `bitsToAffinity` are inverses and are unit-tested as
such, which says nothing about which CPU a checkbox sits over. Shifting the
grid by one row leaves the round trip intact and pins SQL Server to the wrong
processors —
`TestProcessorAffinityBitFollowsTheProcessorItIsLabelled` names the row and
asserts the bit, and catches exactly that mutation. The two toggle columns feed
two different sp_configure options, so they get their own test.

`internal/tui` coverage 31.9% → 34.5%.

## 2026-08-21 — Go 1.27: background goroutines name themselves in a traceback

Two changes out of a survey of what Go 1.27 offers the two repos (the survey
itself is not repeated here — the short answer was that most of what it adds
lands on packages neither repo uses, and the bulk of `go fix`'s 74-file output
is pre-1.27 modernizers that had simply never been run).

Go 1.27 prints a goroutine's `runtime/pprof` labels in the header of every
traceback it appears in, for modules declaring `go 1.27`. `App.safego` and
`safegoRepair` already take a `what` string naming the operation, so both now
set it as a label on their goroutine, and the stack `reportPanic` writes to the
log is headed `goroutine 42 [running] {op: "loading the thing"}:` instead of an
anonymous func literal that names nothing. Labels are inherited, so a goroutine
the operation starts for itself carries it too, and the same label shows up in
the `goroutineleak` profile 1.27 promoted to GA.

The label is set with `pprof.SetGoroutineLabels` rather than `pprof.Do`, and
that is the whole subtlety: `Do`'s `defer SetGoroutineLabels(ctx)` restores the
old (empty) label set *while the panic is still unwinding*, which is before the
recover — so wrapping `fn` in `pprof.Do` labels every traceback except the one
that matters. `TestSafegoLabelsItsGoroutineWithTheOperation` takes its stack
from inside a deferred function during the unwind for that reason; it fails
with the header `"goroutine 42 [running]:"` when `labelGoroutine` is dropped,
which is how it was checked.

In gosmo, `declTypeByName`'s eleven `reflect.TypeOf(T{})` keys and the
`time.Time` comparison in `scriptDeclType` became `reflect.TypeFor[T]()`
(`go fix -reflecttypefor`) — no composite literal to allocate, and the type is
named rather than derived from a value.

## 2026-08-21 — The Status History ring was written from a loader goroutine

Recorded in `docs/open-threads.md` on 2026-08-19 and fixed now.
`App.logStatus` reaches `StatusHistoryDialog.Record`, and `fetchChildren`
(`explorer_loaders.go`) calls `logStatus` on the Object Explorer's loader
goroutine — so a failed folder expand appended to `d.lines` while the UI
goroutine read the same slice to draw the dialog. Undefined behaviour, not a
stale read, and `-race` was quiet because no test drove both sides.

The slice was only half of it. `Record` also called `SetText` on the dialog's
`controls.Editor` whenever the dialog was visible, which is a background
goroutine writing the very widget the UI goroutine is drawing. So the fix is
two-sided: a `sync.Mutex` over `lines`/`dirty`, and `Record` reduced to
recording — the editor rebuild moved onto the UI goroutine, into `Show` (as
before) and now `Draw`, both through `syncIfDirty`. `Draw` reads the line count
through `lineCount()` for the same reason, and `syncIfDirty` drops the lock
before `SetText` so a background `Record` never blocks behind the expensive
half. Behaviour is unchanged from the user's side: a message recorded while the
dialog is open still appears without reopening it, one frame later, and a frame
is exactly what the `postAndWake` that produced the message already asks for.

Audited the other callers rather than assuming, since the thread said to:
`options_dialog.go:236` and `app_connections.go:44` are both on the UI
goroutine (the latter inside a `postAndWake` callback), and a scan for
`setStatus`/`logStatus` inside `safego`/`safegoRepair` closures turned up only
`scripting.go:274`, whose `setStatus` is the *repair* argument —
`safegoRepair` posts that through `postAndWake` too. `fetchChildren` was the
only background caller.

Two tests: `TestStatusHistoryDrawSyncsWhatWasRecordedWhileVisible` pins the
rebuild onto `Draw`, and `TestStatusHistoryRecordIsSafeFromBackgroundGoroutines`
runs four recorders against a drawing loop and fails under `-race` when the
mutex is removed from `Record` — checked by removing it. `fakeSizedScreen`
grew `Get`/`Put`, which `DrawBase`'s shadow needs; it had only `Size` and
`SetContent`, so no test in `internal/tui` had ever driven a dialog's `Draw`.
Verified live too: a failed connect, then a click on the status row, shows both
messages newest-first.

## 2026-08-21 — A scripted partitioned table came back unpartitioned

`ScriptTable` emitted no `ON` clause at all, so a partitioned table's script
recreated it on the target database's default filegroup. Nothing reports that:
the script runs clean, the table is there, and it is a single-partition heap or
clustered index on PRIMARY. Found 2026-08-19 while scripting the item-6 probe
database, whose `dbo.Parted` came back on `[PRIMARY]`.

gosmo carries the data space now. `DataSpace` (`table.go`) holds the filegroup
or partition scheme name, whether it *is* a scheme, whether it is the default
filegroup, and — for a scheme — the partitioning column, since `ON
[scheme]([column])` is the whole clause and half of it is not one. `Index`
gained a `DataSpace` field, filled by the index list from `sys.data_spaces`
plus `sys.index_columns`'s `partition_ordinal`, and `Table.DataSpace`/
`DataSpaceContext` reads the table's own from index_id 0 or 1 — a query of its
own precisely because `indexListContext` filters `i.type > 0` and a partitioned
*heap* has no row in it. Additive throughout: no existing method changed shape.

Two details worth keeping. The partitioning-column subquery is aliased `pic`,
not `ic`: two tests count round trips by looking for `sys.index_columns ic`,
and an `OUTER APPLY` mentioning the same table under the same alias made a
one-query read look like two. And `dataSpaceClause` emits a partition scheme
always but a filegroup only when it is *not* the default — `ON [PRIMARY]` is
what the server does anyway, and naming a filegroup the target database may not
have turns a script that would have worked into one that fails. A scheme with
no partitioning column emits nothing rather than half a clause.

Every object that has an `ON` clause now gets one: the table, the inline
primary key, unique constraints, both columnstore forms and the B-tree indexes.

Verified live on win10cli, and this is the part unit tests could not do:
`live_scripttable_partition_test.go` builds two throwaway databases with the
same filegroup, partition function and scheme, scripts `dbo.Parted` (clustered,
partitioned), `dbo.PartedHeap` (a heap on the scheme) and `dbo.OnArchive` (a
non-default filegroup) out of one, replays each script batch into the other,
and reads `sys.data_spaces` back — the assertion is where the rows actually
landed, not what the text says, and it deliberately does not go through gosmo's
own reader. A/B'd by stubbing `dataSpaceClause` to return `""`: all four
assertions fail with the replayed objects on `PRIMARY (FG)`, which is the
reported bug exactly. The unit tests were checked the same way — dropping the
partitioning column from the clause, dropping the table's clause, and narrowing
the data-space read to index_id 1 each fail a different test.

## 2026-08-21 — The coverage holes from the 2026-08-18 review

Three separate pieces of the same open thread.

**`internal/activity` 65.2% → 97.4%.** The thread called it 54.8%; it had
drifted up since. What was actually uncovered was not the collector — its
backoff, its non-positive-rate normalization and its stop latch were all
pinned already — but every DMV *reader*: `collectCPUUsage`,
`collectSchedulerLoad`, `collectFileIO`, `collectMemory`, `collectSchedulers`,
`collectSessions`, `collectWaits` and the whole of tempdb, all at 0%. Each is a
query and a scan, and a scan reading a column into the wrong field produces a
plausible number and no error at all.

`internal/activity/fakedb_test.go` is a scripted `database/sql` driver keyed by
the *query constant* — `cpuUsageQuery`, `schedQuery`, `tempdbFileQuery` — so a
test says which read it is answering rather than counting queries in order, and
an unscripted query is an error naming itself rather than an empty result. On
top of it: `Collect` and `collectTempDB` driven end to end with a distinct
value in every column, a failing-read subtest per query (a DMV read that fails
must fail the tick, not leave a zero-valued part that draws as an idle server),
`Proc.Find`/`Install`, the `TempDBStore` retention and detail windows, and the
collector's pause/resume and stop-while-paused. Checked by mutation: swapping
`RunnableRequests`/`SuspendedTasks` in the sessions scan, dropping the clamp in
`nonNegativeMB` and making `Find` prefer tempdb each fail a different test.

**gosmo's four untested backup reads** — `BackupHeaders`, `BackupHistory`,
`BackupFileList`, `BackupFileListForSet` — now have `live_backupreads_test.go`
(`-tags livedb`). They are reads, so `WithScript` cannot capture them, and
their column sets are the server's own, which is what `newNamedRow` exists to
absorb: only a real backup file settles them. The test backs two throwaway
databases up to one device in three sets and asserts the header positions and
types, the file list of set 1, and — separately — the file list of set *2*,
since with no FILE clause the read returns set 1 and a `FileListForSet` that
ignored its argument would still look right. Verified by making it ignore the
argument: the set-two subtest fails naming the other database's files.

That mutant run also exposed a fault in the test itself, worth writing down:
**msdb's backup history outlives the database it describes**, so the history
assertion counted the previous run's rows too and the test passed only the
first time. It now clears the history with `sp_delete_database_backuphistory`
before backing up, and again on the way out. Run twice in a row to confirm, and
the server is left as it was found — history rows gone, `.bak` removed with
`xp_delete_files`, both databases dropped.

**Five more Properties pages on the `fakedb_test.go` harness**, leaving about
thirty. Server Properties' Connections, Database Settings and Security joined
Memory/Processors/Advanced in `TestEverySpConfigureRowWritesTheOptionItIs
Labelled` — the same label-to-`sp_configure`-name table, now 44 rows — plus
FILESTREAM, the one control on those six pages that is not a configRow but a
Select whose *index* is the value written (level 2 grants file-system access to
the share, so arriving there by accident matters). Query Store's thirteen
editors leave through one `QueryStoreOptions` struct and one statement, so they
are set to thirteen distinct values in a single pass and read back clause by
clause — a per-field test cannot tell a crossed pair from a correct one when
every apply rewrites every option — with `OFF` covered separately for its own
statement shape, and Flush/Clear pinned to run only when ticked. Login
Properties > General pins the four things that fail silently in the direction
that looks like success: blank password fields mean *keep the current
password*, the rename is the last write, changing the mapped credential unmaps
the old one first, and a Windows login has no password to change while a
built-in `##` login has no name to change.

Both page suites were mutation-checked. Crossing `FlushIntervalSec` with
`IntervalMinutes` and swapping two Connections labels each fail; moving the
rename to the front of Login General's apply produces
`ALTER LOGIN [appuser] WITH NAME = [appuser2]` followed by two statements aimed
at a login that no longer exists; dropping the `passwordRow.Value() != ""` gate
writes `ALTER LOGIN [appuser] WITH PASSWORD = N''` on a page nobody typed a
password into.

`propsheet.StaticRow` gained a `Label()` accessor to make a read-only row
addressable by name, matching every other row type.

## 2026-08-21 — The write-path Properties pages, phase 1 (Tier 1)

Continuing the seam opened on 2026-08-20 (`gosmo.NewServer` +
`internal/tui/fakedb_test.go`). An audit of every `propPage` constructor found
52 with a real `apply`, 17 of them covered; this pass took the eight whose
apply reissues an object or rewrites a permission graph.

**Phase 0, the harness gap.** Two of the eight take a `*PropDialog` — Job
Steps and the database Securables page — and `PropDialog.runPageAction` goes
through `App.safego`/`postAndWake`, so the page could not be built at all
without an `App`. `newFakeDialog` in `fakedb_test.go` pairs `newTestApp`'s
screenless App with a bare `PropDialog{app, ctx}`, and `drainDialog` runs the
callbacks the action posts (`wakeEventLoop` is a no-op with no screen, so
without the drain the assertions run before the work does). No production
change was needed. `fakeExec` also learned to record a statement's
**parameters**: a write through a system procedure — `sp_rename` here, every
`sp_add_job*` later — carries the name it acts on as `@p1`, so the statement
text alone reads `EXEC sp_rename @objname = @p1` and says nothing about what
was renamed to what. `argsFor`/`assertArgs` read them back.

**Eight pages.** Index Options and Included Columns
(`index_props_page_test.go`), Key General and Key Options
(`key_props_page_test.go`), the four permission matrices — Server Properties,
Database Properties, Schema and Table Properties — in one table-driven file
(`permissions_pages_test.go`), the database Securables page including its
column editor (`securables_page_test.go`), and Database Properties > General
(`database_props_general_page_test.go`).

Every one was checked by mutation, and nine mutants died: the constraint guard
that keeps `IGNORE_DUP_KEY` out of a PK-backing index's SET; rebuilding
whenever anything is dirty; naming a compression nobody chose; an off-by-one
reading the included-columns grid back; key columns not excluded from it;
`commitRename` dropped, and `sp_rename`'s second argument; Table Permissions
wired to the database scope and Schema Permissions to the wrong schema;
`buildPermissionsMatrix` ignoring the principal selection; `applySecurable`
sending a SCHEMA down the object path; the column editor cycling row 0; and the
recovery-model write reading the wrong row.

**Two things the mutation pass taught, both worth keeping.** Reordering
`recoveryItems` — the Recovery model dropdown — does *not* fail any test, and
correctly so: the items **are** the T-SQL keywords, so display and write index
the same slice and there is no label/value pair to fall out of step. The
comment on that test now says what it actually catches rather than claiming a
reordering it cannot see; this is the round-trip trap from `CLAUDE.md` in its
benign form. Reordering `databasePermPrincipals`' two lists is likewise
invisible, and also correctly: the matrix keys its edits by principal *name*,
so list order carries no meaning. A surviving mutant is not automatically a
missing test.

**One fake-scripting trap, found the hard way.** `Table.ColumnsContext`'s query
joins `sys.index_columns ic2` to find the primary key, so a response matched on
`"sys.index_columns ic"` — meant for the index-column read — was served to a
scan wanting seventeen columns. The index-column response is matched on
`ic.key_ordinal` instead. Substring matching is ordered and greedy; a response
meant for one query has to be matched on something the other cannot contain.

## 2026-08-21 — The write-path Properties pages, phase 2 (Tier 2, SQL Agent)

The nine SQL Server Agent write pages: Job Properties' General, Steps,
Schedules, Alerts and Notifications, Alert Properties' General and Response,
Operator Properties > General, and Schedule Properties > General. Four new
files plus a shared fixture file (`agent_fakedb_test.go`); 33 tests.

**The plan said "msdb-scoped, so `db:msdb` scoping throughout". It is not.**
Every Agent write gosmo issues is three-part — `EXEC msdb.dbo.sp_update_job` —
and no `USE` is ever sent, so every statement lands on a connection pinned to
no database. `StatementsIn("msdb")` returns nothing on all nine pages;
`Statements()` is the assertion. Stated at the top of `agent_fakedb_test.go`
so the next Agent test does not start by scripting a database that never
appears.

`assertArgs`, built in phase 1 for `sp_rename`, went unused after all: the
`sp_add_job*` family takes no parameters either — gosmo interpolates the names
into the statement text with `escapeSingle`. It stays for the pages that do.

**Twenty mutants killed.** The order of Steps' three passes, both ways (deletes
ascending; adds moved ahead of updates); the `(unchanged)` database sentinel
sent as a real database name; the grid toggling row 0 rather than the selected
row on Schedules; `commitRename` dropped on Job, Alert and Schedule; the
enable/disable branches swapped; `sp_attach_schedule` and `sp_detach_schedule`
swapped; the Job Alerts page dropping its WMI filter, which shifts every edit
past the WMI row onto its neighbour; `SetTrigger`'s error-number and severity
arguments swapped; each of the four `<none>`/`<all databases>` sentinels sent
to the server as a literal name; Notifications' e-mail write gated on the page
instead of its own rows; an operator's e-mail written from the name field.

**The weekday table is now pinned end to end.** `CLAUDE.md`'s standing example
of the round-trip trap is `weekdayBits`: swapping Monday and Tuesday leaves the
checkbox labelled Monday setting Tuesday's bit, and `populate`→`readFrequency`
still agrees, so `agent_schedule_form_test.go` cannot see it. The Schedule
Properties page test ticks days *by name* and asserts the number that reaches
`sp_update_schedule` (`@freq_interval = 10` for Monday and Wednesday) — the
swap now fails, on that test alone.

**Two survivors, both correct.** A non-T-SQL step is kept out of the write path
by two independent guards — `commitCurrent` refuses to copy the form onto it,
and `planJobStepWrites` tests `editable()` again — so removing either one alone
leaves the test passing. That redundancy is deliberate and documented on
`planJobStepWrites`; the test kills the pair, and its comment now says so. As
in phase 1: a surviving mutant is not automatically a missing test.

Coverage on the nine pages' load/apply closures: 74.4%–91.5%, from zero.
The four read-only Agent pages (Job Targets, Job History, Operator
Notifications, Schedule Jobs) stay uncovered — no `apply`, nothing to get
wrong that a test here would see.

## 2026-08-21 — The write-path Properties pages, phase 3 (Tiers 3-4)

The principal and ownership pages: Database Role, Server Role, Database User
and Schema Properties' General pages; the three owner-transfer pages (Owned
Schemas, shared by Role and User Properties, and Owned Roles at both database
and server scope); the shared Extended Properties page; and Table Properties >
Change Tracking. Five new files over one shared fixture
(`principal_fakedb_test.go`); 32 tests.

The fixture is one database with a deliberately tangled ownership graph — the
role `app_admin` owns two other roles and two of the four schemas — so every
page has more than one row to get wrong and nothing acted on is first in its
list.

**Seventeen mutants killed, none survived.** The built-in guard on each of the
four General pages (`isSystemDatabaseRole`, `isSystemServerRole`,
`isSystemSchema`, `isSystemUser`): forcing `builtin = false` gives the page an
apply closure and editable rows, and the statement it would then build —
`ALTER ROLE public WITH NAME = …` — is a syntax error, not a permission
failure, so the page is the only thing standing between the user and it. The
rename ordering on Role, Server Role and User (moved first: killed);
`commitRename` dropped. The owner dropdown built from users only, dropping the
roles. `(None)` sent to `ALTER USER … WITH LOGIN`. All three owner-transfer
pages with their `Owner == this principal` filter removed — the failure worth
naming, since an object that leaks into that list gets handed away on a page
the user thinks is about something else — plus the shared widget committing
onto row 0. Extended Properties: `sp_add` where `sp_update` was meant, the drop
addressing property 0, and a property added *and* removed in one sitting still
being added. Table Change Tracking: `TRACK_COLUMNS_UPDATED` hard-wired ON, the
dirty gate removed (an `ENABLE CHANGE_TRACKING` on a table that already has it
resets the tracking baseline), and an unlisted table loading as tracked.

**A page can refuse to write, and that refusal is testable.** Four of these
pages return a nil `apply` for a built-in principal and render the same fields
as `StaticRow`s. The assertion is therefore "the row is not editable", not "the
row is absent" — `hasEditableRow` in
`principal_props_general_pages_test.go`.

**One fake-scripting trap, the same shape as phase 1's.** gosmo's database-role
and server-role queries both end `WHERE r.type = 'R' AND r.name = @p1`, and the
list forms both contain `r.type = 'R'`; the only thing separating them is that
the server-side query is indented with tabs. The two are never scripted
together, and the fixture says so where the responses are declared. Matching on
whitespace is fragile — if it ever breaks, the fix is a more specific `FROM`
clause (`FROM   sys.database_principals r` vs `FROM sys.server_principals r`),
which is what the *list* responses already use.

Coverage on the ten closures: 74.8%–98.9%, from zero.

## 2026-08-21 — The write-path Properties pages, phase 4 (Tier 5, Always On)

The last three write pages: Availability Group Properties' General, Backup
Preferences and Read-Only Routing. Two new files (`ag_fakedb_test.go`,
`ag_props_pages_test.go`); 22 tests. With this the write-path sweep that began
on 2026-08-20 is done — every `propPage` with a real `apply` outside the New-X
dialogs now has a page test.

**The fixture had one constraint worth writing down.** Every page here reads
and writes through the group's *primary* replica, and `agOnPrimary` treats an
unreachable primary as a hard error rather than degrading — a Properties page
loaded from a secondary would show editable rows whose Apply the server
rejects. `IsLocalPrimary` compares the group's `primary_replica` against the
connected server's own name, so the fixture's primary replica has to be called
what `serverInfoResponse` says the instance is called: there is a replica named
`FAKE\SQL` in `agReplicasResponse` alongside `ubusql2` and `ubusql3`. Without
it the pages open a peer connection the fake cannot serve. The per-replica
routing-list read is scoped with `arg:` on the replica id for the usual reason
— unscoped, all three replicas report the primary's list.

**Seventeen mutants killed.** The failure-condition level written as the row
index rather than the level (the labels read plausibly either way round); the
required-sync ceiling — replica count less the primary — removed; the
replica-by-name lookup replaced by `replicas[0]` on all three pages; each of
the six General detail rows wired to its neighbour's setter; the session
timeout rewritten whenever anything else on the replica changed; two entries of
`agBackupPreferenceItems` swapped, and the radio read as item 0; the Excluded
column reporting the loaded priority rather than the edited one; the routing
URL and list committed onto replica 0; `planAGRoutingOps` no longer holding
cleared URLs back to the end; the routing-list validator dropped.

**The routing order is the one that is only visible end to end.** SQL Server
refuses a routing list naming a replica with no routing URL, and refuses to
clear a URL a list still references, so the writes go out as URLs-being-set,
then every list, then URLs-being-cleared. `ag_props_test.go` already pinned
`planAGRoutingOps` directly; the page test now shows the three statements
arriving in that order from a real edit session, which is what a server error
would otherwise be the only way to discover.

**`agBackupPreferenceItems` is the third label/keyword pair this sweep has
pinned by name** (after the schedule weekday bits and the permission-state
tables). The test walks the whole table rather than one entry — a rotation only
shows up on the entries a single-value test does not visit — and the one label
the fixture already has selected is asserted as *selected* rather than edited,
since a row that is already on the value cannot be made dirty.

Coverage on the three pages: 80.8%–82.0%, and their apply helpers 74.2%–85.0%,
from zero. `wireGridEditor`, the commit/load/redraw wiring all three share, is
at 100%.

## 2026-08-21 — Status strings with a trailing period

Cosmetic, closed off `docs/open-threads.md`. The count there said three
literals in `activity_monitor_proctab.go`; a repo-wide grep for the
concatenated forms found six across two files — `"Not connected."`,
`"Stopped unexpectedly — see the log for details."`, `MasterName + " is not
available."`, `qualified + " failed."`, `"No result returned."`, and
`log_viewer.go`'s `"Read stopped unexpectedly — see the log for details."`.
All six now end without one, matching every other `setStatus` in the app.
`TestActivityMonitorProcTabWithoutConnection` asserted the old wording and was
updated with it.

## 2026-08-21 — CREATE COLUMN MASTER KEY's enclave clause was never valid syntax

`Database.CreateColumnMasterKeyContext` took an `enclaveComputations bool` and
emitted `ENCLAVE_COMPUTATIONS = YES`. There is no boolean spelling: the clause
is `ENCLAVE_COMPUTATIONS (SIGNATURE = 0x...)`, which gosmo's own scripter had
right all along (`buildColumnMasterKeyScript`). Proved live on win10cli — the
old form dies with `Msg 102, Level 15 ... Incorrect syntax near '='`, a parse
error, so it cannot even be caught by TRY/CATCH. The bool signature was
un-implementable, not just misspelled: the signature is computed client-side
from the master key's private key, so nothing inside the library can supply
one.

`CreateColumnMasterKeyWithSignature`/`...Context` now take the signature —
the same bytes `ColumnMasterKey.Signature` reads back — and both forms share
`createColumnMasterKey`, which omits the clause entirely when there is no
signature. The old bool method stays (published API, no-removal rule) and
returns an error naming the new one when passed `true`, rather than shipping a
statement the server will reject.

Two tests, because the statement test cannot see this class of bug at all — it
compares text against text, and the text it was comparing against was the
error. `script_security_write_test.go` pins the clause and the two refusals;
`live_columnmasterkey_test.go` (`-tags livedb`) creates both forms against a
throwaway database, reads `allow_enclave_computations` and `signature` back out
of `sys.column_master_keys`, and asserts the refused key was never created.
A/B'd: reintroducing `= YES` fails the live test on the CREATE and leaves the
unit tests' verdict unchanged unless the text is updated with it.

No gossms caller — there is still no New Column Master Key dialog; see
`docs/open-threads.md`.

## 2026-08-21 — Five unreachable functions, and what "unreachable" was hiding

The 2026-08-18 audit's list was three different things, not one.

`charts.HistoryChart.Plot`, `StackedHistoryChart.Plot` and `historySpec.plotRect`
looked dead because nothing called them — but `TimeRow`/`timeRowRect`, built
the same way in the same files, were reachable only because
`TestDrawFrameAgreesWithDrawAndTimeRow` used `TimeRow` as its oracle for
`DrawFrame`'s second rect. `Plot` had been left out of the same test. Deleting
it would have removed the only way to ask where a chart's plot landed without
drawing the chart, which is what a mouse hit-test needs (the dashboard records
its `ChartHit`s from `DrawFrame` because it happens to be drawing; a hit-test
outside a draw pass has nothing to call). The test now asserts all three agree
and is renamed `TestDrawFrameAgreesWithDrawPlotAndTimeRow`. Mutant-checked by
shortening `plotRect`'s rect by a row: eight failures across the four rects it
runs.

`core.ClearRect` was genuinely dead — `FillRect(s, r, ' ', style)` with a name
— and is gone.

`theme.SetPalette` stays: `theme/doc.go` advertises it as the extension point a
host application swaps its palette through, so it is API, not residue. New
`theme/palette_test.go` pins that `Active()`'s pointer keeps reading the
palette that was set (a version returning a copy would leave a caller on the
old colours) and that `StyleMenuBar` follows. Mutant-checked by making
`SetPalette` a no-op.

Worth carrying forward: `internal/tuikit` is described in its own README as an
embeddable, application-agnostic library. It is not gosmo — removal there is
allowed — but "no callers in the app" still asks a question about the test
suite first, and in three of these five cases that was the whole answer.

## 2026-08-21 — WriteAtomic and ownership: documented, not changed

Closed off `docs/open-threads.md` as a decision rather than a fix. The rename
leaves the replacement file owned by whoever is running, where a write-in-place
would have kept the original's uid/gid. Fixing it means a Stat plus a Chown
that only root can make succeed and that has to be a no-op on Windows — new
`runtime.GOOS` branching, in a codebase that has it in exactly two files — to
serve a case gossms does not support (running as root over someone else's
file). The note now lives on `modeFor`'s doc comment, where the next person
reading "this preserves the mode" will see what it does not preserve.

## 2026-08-21 - The two Always On writes that had never run live

`SetAvailabilityMode` and `SetFailoverMode` were the last replica setters
without live coverage, and for a good reason: the only availability group
standing is AAG1, `cluster_type = EXTERNAL`, and dropping its last synchronous
replica leaves Pacemaker unable to promote anything - recoverable only by
recreating the group. The way in was already in the repo:
`TestLiveAvailabilityGroupCreate` builds a throwaway `CLUSTER_TYPE = NONE`
group across ubusql1/ubusql2, joins the secondary, and drops it. Nothing
watches that group, so the modes can be exercised there for real, between the
join and the drop.

`AVAILABILITY_MODE` round-trips: SYNCHRONOUS_COMMIT to ASYNCHRONOUS_COMMIT and
back, each half re-read from `sys.availability_replicas` rather than from the
setter's own mirror.

`FAILOVER_MODE` cannot be round-tripped anywhere available here, and the test
pins what is true instead of skipping. Probed live first: a NONE group accepts
MANUAL and refuses both others with "The cluster type of availability group
'gosmo_agtmp' only supports MANUAL failover mode." AUTOMATIC needs a WSFC;
EXTERNAL needs a group created EXTERNAL, which is AAG1. The refusal is still
worth an assertion - it is a *semantic* rejection, so the statement parsed,
where a malformed one would come back `Msg 102`. The test also pins that
neither refusal moves `r.FailoverMode`: `setReplicaKeyword` calls
`setIfApplied` only after `modifyReplica` returns nil, and an optimistic mirror
would report a mode the server never took.

A/B'd by pointing `SetAvailabilityModeContext` at the `SEEDING_MODE` keyword -
"Invalid usage of the option SEEDING_MODE in the ALTER AVAILABILITY GROUP
statement", so the test is reading the real write. Cluster left as found: AAG1
only, PRIMARY/HEALTHY on ubusql1, SECONDARY/HEALTHY on ubusql2, no throwaway
group behind.

## 2026-08-21 — Key Diagnostics can copy its log, and the sync rule that took

The last of the `ClipboardHost` work of 2026-08-20. Key Diagnostics was the one
dialog whose inert clipboard was worth revisiting rather than accepting: its
whole purpose is reporting what a terminal actually sent, which is exactly the
text someone pastes into a bug report, and it drew its log directly instead of
holding a widget, so there was no `core.ClipboardTarget` to hand back.

The log now lives in a read-only `controls.Editor` with the gutter hidden,
built the way `StatusHistoryDialog` has always been built — `SetReadOnly(true)`,
`SetActive(true)`, a dirty flag, a `syncIfDirty` called from `Show` and `Draw`,
`FocusedClipboardTarget` returning the editor. That buys mouse drag-selection,
Shift+arrows, Ctrl+A and the editor's own scrollbar, all of which the
hand-rolled list had none of. A `Copy` button sits beside `Close` for the mouse
route.

**The one thing that is not a copy of Status History, and the reason a straight
copy fails.** This dialog records the very keys used to copy from it.
`Editor.SetText` resets cursor, scroll *and* selection, so with a plain
sync-in-`Draw` the sequence Ctrl+A, Ctrl+C reliably copies nothing: the Ctrl+A
is itself recorded, marking the log dirty, and the frame drawn between the two
keystrokes rebuilds the editor and drops the selection the user just made. So
`syncIfDirty` refuses to rebuild while `d.editor.HasSelection()`; `dirty` stays
set and the deferred lines flush the moment the selection clears. `Show` writes
the empty text directly rather than going through `syncIfDirty`, which would
otherwise let a selection left over from the previous showing pin the last
session's text on screen.

Confirmed live under tmux rather than assumed: with three lines logged, Ctrl+A
highlighted all three, the two keys pressed *during* the selection stayed out of
the editor, Ctrl+C put the exact log on the system clipboard, and pressing Right
flushed the deferred `Ctrl+A`/`Ctrl+C` lines into view — proving nothing was
lost, only deferred. Note `Ctrl+C` appears in the log at all because
`App.handleKey` calls `RecordKey` *before* it consumes the clipboard shortcuts;
that ordering was already right and needed no change. A mouse drag across two
rows then Ctrl+C copied exactly the dragged range, ending mid-line.

Three mutants killed: dropping the `HasSelection` guard fails
`TestKeyDiagnosticsKeepsASelectionWhileMoreKeysArrive` with the reported
symptom; routing `Show` through `syncIfDirty` fails
`TestKeyDiagnosticsShowResetsTheLog` with the previous session's text still on
screen; returning nil from `FocusedClipboardTarget` fails the copy test.
`TestEveryDialogWithTextEntryIsAClipboardHost` picks the dialog up on its own
now that it owns an `*controls.Editor`.

`TestKeyDiagnosticsDialogScrollbarDragScrolls` is deleted rather than rewritten:
it drove the `d.scroll` field the editor replaces, the editor's scrollbar drag
is pinned in `internal/tuikit/controls/scrollbar_drag_test.go`, and Status
History — the same shape — has never had a dialog-level case either. A note in
`scrollbar_dialogs_test.go` says so, so the absence reads as a decision rather
than an oversight.

One false alarm worth recording, since it cost a detour: the Copy button
appeared not to work under tmux. It did. A malformed `send-keys -H` sequence
injected earlier in the session had left `ModalDialog.mouseDragging` latched, so
the next press was correctly ignored as a drag continuation, and the sentinel
was read back before the asynchronous `osClipboardWrite` had landed. A clean
run — fresh dialog, fresh sentinel, 1.5s wait — showed both the selection and
the clipboard. When a tmux mouse test disagrees with the code, suspect the
injected sequence before the code.

## 2026-08-21 — Recycle, and why the open viewer has to Refresh

Item 6 of the open-issues list: SSMS's "Recycle" was the one Log File Viewer
action with no counterpart here. gosmo already had `CycleErrorLog`, fixed to
the SQL Server family and called by nothing.

**gosmo.** `CycleLog(logType)` / `CycleLogContext(ctx, logType)`, symmetric with
the `ReadLog`/`EnumErrorLogs` pairs that already take an `ErrorLogType`, over a
`cycleLogStatements` table: `EXEC sp_cycle_errorlog` for the SQL Server family,
`EXEC msdb.dbo.sp_cycle_agent_errorlog` for the Agent's. The Agent's is
three-part on purpose — the procedure is in msdb and the connection may sit in
any database, whereas `sp_cycle_errorlog` resolves from anywhere through the
`sp_` prefix's master fallback. `CycleErrorLog`/`Context` stay and delegate; a
published method is never removed or narrowed.

That table is exactly the shape `CLAUDE.md` warns a round-trip test cannot see —
swap its two entries and every "cycle then check it cycled" assertion still
passes while the user's Recycle hits the wrong log family. So
`TestCycleLogStatements` names both statements outright and asserts the table
has no more entries than the test names, and `TestCycleErrorLogIsTheSQLServerFamily`
pins the delegate's family separately. Swapping the entries fails both;
pointing the delegate at the Agent fails the second.

`live_cyclelog_test.go` is the other half, because `WithScript` only proves the
statement was *built*. It cycles each family on a real instance and asserts the
archives renumbered. Both passed against win10cli. It is the rare live test that
mutates the instance rather than a throwaway object — there is nothing smaller
to cycle — so its doc comment says so.

**gossms.** A `Recycle...` cell on the viewer's toolbar and a `Recycle` item on
both log folders' Object Explorer menus, sharing `cycleLogMessage`, which names
what is lost (the archives renumber; the oldest goes once the instance is at its
configured maximum).

Two things went in as comments because getting either wrong is silent:

- **The busy latch is taken before the question, not in the answer.** The
  confirm dialog takes input but does not stop F5 reaching the panel, so a Load
  begun while the question was up would clear `busy` from under a cycle it knows
  nothing about. Declining releases it — a latch leaked there dims the entire
  toolbar for the panel's lifetime, which is also why the goroutine is
  `safegoRepair` and not `safego`.
- **An open viewer is refreshed through `Refresh`, never `Load`.** This is the
  one that a plausible simplification breaks. For the family on screen the two
  are equivalent — Load overwrites that family's cached enumeration anyway — so
  the first test written for it (`...RefreshesRatherThanReloads`) passed with
  the mutant in place. The real case is cross-family: recycle the Agent log from
  the tree while the viewer sits on SQL Server, and Load re-enumerates only SQL
  Server, leaving the Agent's pre-cycle numbering cached to be handed over the
  moment the user flips the selector. `TestExplorerRecycleRefreshesTheOtherFamilyToo`
  is that case, and it does kill the mutant. The comment on
  `refreshOpenLogViewer` says why rather than saying "use Refresh".

Seven mutants killed in gossms: the cycle ignoring `lv.logType`, the No answer
leaking the latch, no repair on panic, a `logTool*` const added without its
cell, no latch while the question is up, the open viewer put through `Load`, and
the open viewer not refreshed at all. The Refresh-vs-Load mutant surviving the
first attempt is the reason the test was retargeted rather than the comment
being trusted — the test that only exercised the family on screen was asserting
something that was true either way.

Live, on win10cli, both paths and both families: the tree's Recycle on SQL
Server Logs (server list renumbered, folder refreshed), the toolbar's on the
Agent family (declined once — toolbar stayed live and nothing was written — then
confirmed), and the cross-family case above, where the viewer's SQL Server
archive list came back matching the server's post-cycle numbering exactly while
it had been sitting on Agent throughout.

## 2026-08-21 — A peer is reached with its own credentials

Item 5. `db.ServerConn.peerOptions` copied the parent connection's options
wholesale, so all nine `Peer` call sites reached every instance with whatever
login the user registered the tree with. A topology with per-instance
credentials surfaced as a connect error naming the instance, and on the Object
Explorer's follow-the-primary path as a silent
`(partial — primary X unreachable)`.

The seam is `internal/db`, not the UI: `peerOptions` is the one place
credentials are derived, and the Object Explorer's loader — the caller that
matters most — has no `*App` handle and cannot prompt. So `ServerConn` gained a
`PeerCredentials` resolver installed with `SetPeerCredentials`, consulted before
the fallback to the parent's own settings. `Peer` installs it on every peer it
opens, so the second hop of follow-the-primary resolves through the same table
rather than inheriting the primary's login.

**A saved connection is taken whole, not field by field.** The plan listed the
fields to copy — User, Password, AuthMethod, Port, tenant, client, TLS — and a
hand-copied list is exactly the thing that silently stops carrying whatever
`config.Connection` gains next. Only `Server` and `Database` are overridden:
`Server` so the catalog's spelling is what the peer is opened against, and
`Database` for the reason it was already blanked (2026-08-14 — a database named
in the connection string has to be openable, and a secondary's may not be).

App answers the resolver from the connections the user has already made, keyed
by `db.InstanceKey` — lowercased host plus `\instance`, port discarded — which
is now also `Peer`'s own cache key, so a hit in one and a hit in the other
always agree about what "that instance" means.

**The FQDN tier was not in the plan and turned out to be most of the value.**
The first cross-spelling test failed: `@@SERVERNAME`, which is what
`sys.availability_replicas` reports, is the short machine name, while what
people type into Connect on a domain network is the FQDN — so a connection
saved as `ubusql2.fritz.box` would never answer a peer read for `ubusql2`, which
is the common case rather than an edge one. Collapsing the key to the short host
was the wrong fix: `sql.a.example` and `sql.b.example` are different instances
and would have handed each other their logins. Instead a saved connection is
registered a second time under its short host in a separate, strictly
lower-priority map, consulted only when the exact host misses. Worst case is the
connect error that was the behaviour before any of this.
`TestPeerCredentialsPreferExactHostOverAShortAlias` pins the tiering with three
instances that share a short name.

Seven mutants killed: `peerOptions` ignoring the resolver, the saved
connection's Database surviving, `InstanceKey` reduced to a plain lowercase, the
App map keyed by the raw spelling, `loadPeerCredentials` letting the oldest
entry win, the alias tier consulted first, and an already-short host also
registering an alias.

One test could not be written honestly: `Peer`'s own propagation line sits
behind `Connect`, which needs a real instance, so the unit test pins the two
halves it composes and the wiring itself is live-verified.

Live on the ubusql1/ubusql2 cluster, and the A/B is what makes it worth
recording. A throwaway `peer_probe` login on ubusql1 (the primary), saved in
gossms as ubusql1's connection; the tree then connected to **ubusql2** as `sa`
and its availability group expanded. `sys.dm_exec_sessions` on ubusql1 showed
the follow-the-primary reads arriving as `peer_probe`. Deleting that one saved
connection from the config and repeating the identical expansion showed them
arriving as `sa` — the old behaviour, back. The login was dropped and the run
used a scratch `XDG_CONFIG_HOME`, so neither the cluster nor the author's own
saved connections were touched.

## 2026-08-21 — New Endpoint's Script Changes, and two statements that had to go

Item 4, and the framing in `open-threads.md` was out of date in the worse
direction. It said the dialog "does not script". In fact
`newObjectDialog.init` sets `OnScript` unconditionally, so the button was live
and running the shell's version: every collected statement in one batch, with
nothing saying that they belong to two or more instances. Run whole against the
one you are connected to, the certificate imports fail and the endpoint GRANTs
land on the wrong endpoint.

**One collector per instance, not one for the pipeline.** `endpointPeer` gained
a `ctx` and a `script`, and each phase writes through `p.ctx`. That is what
makes the grouping possible at all: the run is three phases over N instances
with a skip possible at every step, so there is no positional mapping from a
flat statement list back to the instance each statement belongs to.
`NewAGDialog.annotateScript` keeps one and its own comment admits how fragile it
is ("a MANUAL-seeding replica, whose GRANT is skipped, shifts every label after
it"); here it would not work at all. `TestEndpointConfigureCollectsPerInstance`
asserts the pipeline-wide collector stays empty and that no instance's group
creates another's certificate — run the phases against the flat `ctx` and all
six statements land in one place.

Reads deliberately still hit the real servers. That is not a leak in the
scripting model, it is the only thing that makes the script useful: a
certificate's public key can only be read from the instance that holds it, so a
certificate that already exists scripts as a complete `FROM BINARY` import.
`WithScript` intercepts only the two exec chokepoints, so this needs no special
handling.

**Everything a partial script contains has to be runnable.** Two statements
failed that, and neither was visible from the unit tests as first written.

The first was found by reading the rendered output: `ensureEndpoint` emitted
`GRANT CONNECT ON ENDPOINT::[…] TO [X_login]` even when `importPeerCertificate`
had skipped creating `X_login` for want of a certificate. Run as one batch the
script fails on a login that does not exist — and fails *before* the endpoint
statements above it take effect. The grant is now skipped for a peer in
`p.certSkipped`, and the note already says the grant is missing along with the
login.

The second was found live, which is the point of running it live. Against
ubusql1/ubusql2 the script opened with `CREATE USER [ubusql2_user] FOR LOGIN
[ubusql2_login]` — for a user that already existed on both instances. The
pipeline creates the user unconditionally and tolerates the already-exists
error, a strategy that works when the statement actually runs and cannot under
`WithScript`, where the write short-circuits and gosmo never sees the error.
`importPeerCertificate` now looks the user up first, exactly as it already did
the login and for the reason already written there. That is one extra read per
pair per instance on the real path too.

`peerServerFor` is a seam like the `certificateName` above it: the default goes
through `db.ServerConn.Peer`, and a test supplies its own. It is what lets the
whole pipeline run against two `fakedb` instances, which is where the pending
case is covered — a live run on already-configured instances never produces it.

Nine mutants killed, including two the first round missed:
`TestEndpointDialogOverridesTheShellScript` compares `OnScript`'s code pointer
against both candidates, because removing the override is the exact bug this
item started from and nothing else failed when it was absent.

Live, writing nothing to either server, twice:

- **Complete path**, stock binary on ubusql1/ubusql2, whose certificates and
  endpoints already exist: a correctly grouped script, no INCOMPLETE warning,
  and — after the `CREATE USER` fix — only the two idempotent re-`GRANT`s. Both
  were executed against their instances and accepted; the AG stayed HEALTHY.
- **Pending path**, a scratch binary whose `certificateName` returns
  `_ScriptProbeCert` so the certificates look absent. Real servers, real reads,
  nothing written: the INCOMPLETE header, per-instance `CREATE CERTIFICATE`, the
  pending note naming the instance, the "Missing here" note naming the peer, and
  no GRANT. Confirmed afterwards that neither instance had gained a certificate
  and that both replicas were still HEALTHY.

## 2026-08-21 — Always Encrypted: both keys can now be created from the tree

Closes the last item of Phase 1 item 6. gossms gained New Column Master Key and
New Column Encryption Key on the two folders under a database's Security >
Always Encrypted Keys; gosmo gained `Database.CreateColumnEncryptionKey`, the
one write of the pair that was missing. The scope was set with the user: both
dialogs, key material pasted.

**What the dialogs cannot do, and why that is the design.** Neither the
master key's enclave signature nor the encryption key's `ENCRYPTED_VALUE` can
be computed here — both are produced client-side from the master key's private
key, which lives in a Windows certificate store, a CSP/CNG provider or a Key
Vault, none of which a portable no-CGO build can reach. Both dialogs therefore
take the `0x…` blob SSMS or the PowerShell cmdlets print, through one shared
`parseHexBytes`.

One refusal is worth keeping: a signature pasted with "Allow enclave
computations" *unticked* is rejected rather than ignored. Ignoring it creates a
key that succeeds, reads back fine, and is not the key the user was setting
up — the failure only surfaces later, when an enclave query refuses to run.

**The two create dialogs are the first database-scoped ones.** Every earlier
`newObjectDialog` is server-scoped, so `show(sc)` was enough; these carry the
database and the folder node, set before the embedded `show` runs the prefetch
that reads them, and refresh through `refreshExplorerNode(node)` rather than
`RefreshFolderByType` — which finds the *first* folder of a type anywhere in
the tree and would refresh whichever database sorted first. Apply goes through
`Server.Database(name)`, not `DatabaseByName`: the statement names everything
it needs, and only the lightweight handle works under Script Changes.

**Two traps in the fake-DB tests, both of which produced a wrong answer first.**
The listing query is `FROM   sys.column_master_keys` with the whitespace gosmo
formats its SQL with, so a `fakeResponse` matching `"FROM sys.column_master_keys"`
never fires and the page loads as an error — the substring is matched against
the raw query text. And the column *encryption* key read joins
`sys.column_master_keys`, so behind the master-key answer it is served six
columns and fails in the scanner rather than missing; its response has to come
first. Both are the same shape as the `arg:`/`db:` scoping rules already in
`CLAUDE.md`: the fake answers by substring, in order, and order is meaning.

Mutants killed: swapping provider and key path in the CMK apply, and using the
first master key instead of the selected one in the CEK apply — the second is
the "act on something that is not row 0" rule, and the dropdown's selection is
the only thing that made it fail.

Live on win10cli against a throwaway database, driven through the TUI under
tmux: both dialogs created their key, both folders refreshed to show it without
a manual refresh, and `sys.column_encryption_key_values` read the encrypted
value back byte-for-byte through the hex field. gosmo's own live test
(`live_columnmasterkey_test.go`, `-tags livedb`) covers the two-value
mid-rotation form, which the dialog does not offer.

Also this session, unrelated and asked for directly: **a new connection's
server node is expanded on connect** (`ObjectExplorer.ExpandNode`, called from
`connectServer`), the way SSMS opens one. The expansion goes through
`handleExpand`, so it is the same path a click takes — including the
"Loading..." latch and the background fetch. It is deliberately not inside
`AddRoot`, which the unit tests call with a connection that has no server
behind it.

## 2026-08-21 — Delete a column, drop with cascade, move to another schema

Three of the four gaps § Delete/Rename recorded, closed together. gosmo gained
`Table.DropColumn` and `Database.TransferObject`; gossms gained a `NodeColumn`
entry in `objectOps`, a checkbox on the table delete confirmation, and a
"Move to Schema..." item that lists the database's other schemas.

**The cascade is a checkbox on the question, not a second question.**
`dialogs.ConfirmDialog` grew `ShowConfirmOption`, which draws one
`widgets.CheckBox` above the separator and leads the Tab cycle with it. Two
things it has to get right, both covered:

- `CheckBox.HandleKey` refuses every key while unfocused, and focus was only
  applied in `Draw`. That works in the app, where a frame is drawn before any
  key arrives, and made every headless test's Space a no-op. Focus is now set
  where it changes (`setOptFocused`), and Draw only draws.
- The checkbox must not survive into the next showing — `ConfirmDialog` is one
  shared instance, so a Delete Table followed by "Discard changes?" would
  otherwise draw a stray box and put the keyboard on it. `ShowConfirm` and
  `ShowConfirmCancel` clear it, and a test answers a plain confirm after an
  option one to prove the focus cycle went back to two stops.

The option is unticked on every showing, deliberately: it widens what the drop
touches — the foreign keys it drops are on *other* tables — so it is asked for
each time rather than remembered.

**The column node did not know its own table.** `loadColumnsChildren` set
Schema and Name (the column's) but never `TableName`, unlike the Keys and
Indexes loaders. Nothing needed it until a column could be dropped; without it
`ALTER TABLE ... DROP COLUMN` names the *column* as the table. The fake-DB test
asserts the whole statement for exactly that reason, and the mutant that builds
the table handle from `n.Name` fails it.

**What the server will not tell you about a blocked column drop.** Verified
live: dropping a column with one default constraint on it fails with "ALTER
TABLE DROP COLUMN note failed because one or more objects access this column."
— the constraint is not named. The first version of the live test asserted the
constraint's name and failed, which is how this was found. SSMS never shows it
because SSMS drops the default first. gossms does not: the confirmation names
the classes of blocker ("a constraint, index or statistic ... without naming
which") and the server's refusal is passed through.

**Move to Schema is not a rename.** `sp_rename` takes a bare name and cannot
cross schemas; `ALTER SCHEMA ... TRANSFER` can, and drops every permission
granted directly on the object as it goes — which the confirmation says,
because nothing afterwards does. The schemas are read before the menu opens
rather than typed into a prompt, and the current schema is filtered out of the
list (gosmo refuses a same-schema transfer as well, on both spellings of the
case). `TreeView.SelectionAnchor` was extracted from
`openContextMenuAtSelection` so the submenu opens where the node is.

Live on win10cli against a throwaway database, all A/B'd: dropping a referenced
table refused with the box unticked and succeeded with it ticked, leaving the
referencing table and no foreign keys; a plain column dropped and one with a
default constraint was refused; a view moved from dbo to arch and the tree
came back showing `arch.vParent`. gosmo's own live test asserts the transferred
object keeps its `object_id` — a re-create would be a different object with the
same name.

## 2026-08-21 — Log File Viewer: Enter on a leaf, and a search the server runs

Two of the three things § Log File Viewer recorded as deliberately out. The
third — merging several logs into one grid — stays out, at the user's call.

**`TreeView.OnActivate`.** The tree had no activation callback at all: Enter
was `toggleExpand`, which means nothing on a leaf, so a log file could only be
opened from its context menu. The hook returns a bool and Enter falls back to
expand/collapse on false, which is what keeps every folder behaving as before.
Double-click is the mouse spelling of the same thing, timed with the Editor's
`doubleClickInterval` so the app has one double-click speed.

Two details that a plausible simplification would get wrong, both pinned:
`Right` must *not* activate — it is the arrow-key half of navigation, and
overloading it would make the two keys different on leaves and identical on
folders; and a double-click on the `[+]`/`[-]` glyph must only toggle, because
that region already means toggle and activating from it fires on the second
half of an ordinary open-and-close.

Only `NodeSQLServerLog`/`NodeAgentErrorLog` claim it. An object node's menu
offers several actions and none is obviously the default; guessing would make
Enter unpredictable from node to node.

**Server-side search.** gosmo gained `LogSearch` and
`Server.ReadLogFiltered(Context)` — `xp_readerrorlog`'s arguments 3-6 — with
`ReadLog` now a call with an empty search. Two findings the statement test
could not have produced, both from the live run:

- **The date bounds have to be sent as text.** A typed datetime parameter is
  rejected: "The format for the date filter is incorrect. Please use a valid
  SQL Server date or datetime format." The procedure parses the string itself,
  so `readErrorLogCall` formats `YYYY-MM-DD HH:MM:SS` into an `sql.NullString`.
  Seconds are included deliberately — a bound rounded to the day widens the
  range the caller asked for.
- **An unset argument that precedes a set one has to be a typed NULL**, not
  omitted: the arguments are positional, so dropping one shifts the rest and
  the date silently arrives as a search string. `sql.NullString{}`/`NullTime{}`
  carry a type where a bare Go nil does not.

The two search strings are AND-ed by the server, not OR-ed — the live test
pins it by pairing a real string with an impossible one and asserting zero
rows, which an OR would answer with the whole first set.

In the panel, Search is a toolbar button and its own small dialog
(`log_search_dialog.go`, modelled on Find/Replace including the drag-latch and
`ClipboardHost` rules). The client-side Filter box is untouched and still runs
over whatever the read returned, so the two compose. The status line names an
active search: without it, "no entries" on a searched read is indistinguishable
from an empty log.

Live on win10cli: Enter on an archive opened it, a double-click on another
switched to it, a text search cut 1035 entries to 147, a `From` bound alone
cut it to 16 with nothing older than the bound, and an unparseable date was
refused in the dialog rather than sent. That run also showed the hint line
clipped at the dialog's width — `DrawTextClipped` truncates silently, and the
half it dropped was the date format itself, so the hint is now two short lines.

## 2026-08-21 — The Object Explorer filter now runs at the server

Closes the last open item of § Object Explorer folder filter (Owner and
Durability Type on Tables stay out, at the user's call). gosmo gained
`ObjectFilter` and seven `…FilteredContext` listings; gossms translates a
folder's `nodeFilter` into one and passes it to the loader.

**The safety property that made this a small change.** The client-side pass is
untouched and still runs over whatever comes back, so the push-down is an
optimisation and can only be wrong by narrowing *further* than the client
would. `nodeFilter.pushdown` therefore either reproduces the client comparison
exactly or refuses the whole filter, and `serverFilter` turns a refusal into an
empty filter — the folder is read whole and narrowed in Go, exactly as before.
That is also why the value is trimmed here: `matchText` trims what the user
typed, and a pattern built from the untrimmed value searches for something
narrower.

**Two rules in the clause builder, either of which silently changes results.**
Both are stated on `ObjectFilter.clause` and pinned by
`TestObjectFilterClause`:

- `LOWER(col) LIKE LOWER(@p)`. A bare LIKE inherits the database collation, and
  on a case-sensitive one it drops rows a case-insensitive filter box is
  expected to keep.
- The pattern goes through gosmo's existing `likeEscape` and the LIKE carries
  `ESCAPE '\'`. `%`, `_` and `[` are all legal in an identifier: unescaped,
  filtering on `pct_1` also matches `pct1100`.

Dates are half-open day ranges (`>= day AND < day+1`), matching `matchDate`'s
whole-calendar-day comparison; "after the 20th" starts on the 21st.

**What the live test asserts, and why it is the only one that could.** The
statement test says what SQL is generated; only a server says whether that SQL
selects the same objects the caller would have. `live_objectfilter_test.go`
builds a fixture of deliberately awkward names — `CustOrders`/`custarchive`,
`pct_100` next to `pct1100`, `100%done`, `br[ackets` — and asserts every
filtered read equals its own unfiltered read narrowed in Go, per operator and
per family. All six families plus tables agree, and the date cases pass with
the parameter sent as a `time.Time`, which is what says the driver is not
shifting the zone underneath the comparison.

Live in the TUI against a fixture database: filtering Tables on `pct_1` shows
`dbo.pct_100` and not `pct1100`, in the tree and in the Details pane, which is
the escape rule end to end.

## 2026-08-22 — Review pass over both trees: three fixes

A read of every uncommitted non-test change in gossms and gosmo, plus the new
files, against `go vet`, `go test`, and `go test -race` in both repos (all
green before and after). `staticcheck` is no longer usable here — the installed
build cannot decode Go 1.27 export data — so this was reading, not tooling.

**A release outside the dialog stranded ConfirmDialog's new checkbox.**
`ShowConfirmOption` (2026-08-21, Delete Table's cascade) put a `widgets.CheckBox`
in a dialog whose `HandleMouse` opens with `ConsumeOutsideClick`, which answers
true for *every* event outside the rect — `ButtonNone` included. So the widget
never saw the release, its `mouseDragging` latch survived the gesture, and the
next press on the box was swallowed as a continuation of it: press the box,
drift off the dialog, release, and the following click does nothing. Invariant 5
of § Mouse, overlays, and async UI, and the two dialogs written the same week
(`key_diagnostics_dialog.go`, `log_search_dialog.go`) both forward the release
first — this one was the odd man out. The regression test drives the three
events (press the box, release outside, press again) and fails on the pre-fix
code.

**Key Diagnostics stopped recording after its own Copy button.** `copyAll`
selects the whole log and leaves the selection standing, and `syncIfDirty`
deliberately refuses to rebuild the editor while one is live (that rule is what
keeps Ctrl+A→Ctrl+C from copying nothing). A read-only `controls.Editor` also
rejects every key that is not a movement key *without* clearing its selection
(`readOnlySafeKey`), so after pressing Copy the log froze on exactly the keys
the dialog exists to decode, until the user happened to press an arrow. The fix
is one line — clear the selection once the text has been read, which is safe
because `copySelection` reads `SelectedText()` synchronously — and the comment
on `copyAll` now says why it is there.

**A peer credential resolver must never make an instance less reachable.**
Yesterday's resolver (above) prefers a saved connection over the parent's own
settings unconditionally, and is seeded at startup from disk rather than from
connections proven to work. Two consequences, neither of which the tests could
see: `config.Load` blanks the password of every entry it cannot decrypt — a
replaced config key blanks the whole file — so a peer read that used to work
would sign in with an empty password; and a saved entry that fails for any
other reason had no second chance, where before the resolver existed the
parent's credentials were simply used.

Fixed in two halves. `peerOptions` is now only the *preferred* derivation:
`Peer` keeps `parentPeerOptions` — the pre-resolver form — and retries with it
once when the resolver's answer will not connect, skipping the retry when the
two are equal so an agreeing resolver costs nothing. When both fail the first
error is reported, since it names the credentials the user deliberately
registered for that instance. And `loadPeerCredentials` no longer seeds an
entry whose password this session cannot read, which is what
`config.Connection.PasswordUnreadable` exists to say: `Password` alone cannot,
because Load blanks it both for "no password saved" and for "saved and
unopenable". Nothing else is filtered — the fallback is the general answer and
does not need the filter to be exhaustive.

What the resolver still does on purpose: a saved *low-privilege* login for a
replica keeps winning over the parent's. That is the feature's premise —
connecting to an instance once is how it is given different credentials — and
only the unusable and the failing cases changed.

`Peer` itself is still not unit-testable (it is behind `Connect`, which needs a
real instance), so the retry is pinned only through the option derivation:
`TestParentPeerOptionsIgnoreTheResolver` and its sibling assert what the two
sets are and that they differ, which is the condition the retry turns on.
`config.Connection` has to stay comparable for that, and the test says so.
**The wiring itself is unverified live** — the run that would close it is
expanding Always On on ubusql1/ubusql2, then breaking a saved replica password
and watching the read still succeed.

Every test added here was checked by mutating the code it covers and confirming
it fails.

## 2026-08-22 — win10cli as a third instance: the endpoint dialog, the thumbprint check and Add Replica's Connect, all driven live

Three coverage entries in `docs/open-threads.md` had been waiting on hardware:
the endpoint dialog had never been driven end to end, the peer-certificate
thumbprint check had never fired, and Add Replica's Connect had never filled
its endpoint URL row. All three said "needs a third instance". The question was
whether `win10cli` could be one.

**Answered by probing, not by reasoning.** It cannot be a third *replica* —
`IsHadrEnabled` 0, `IsClustered` 0, no rows in `sys.dm_os_cluster_nodes`, and
the host is Windows 10 Pro, which has no Failover Clustering feature for SQL
Server to require. It can be a third *instance*, because nothing in the
certificate exchange needs Always On and gossms checks HADR only on the
instance the dialog is opened from. That asymmetry decided the direction of
every run below: local ubusql1, peer win10cli, never the reverse.

**The endpoint dialog, scripted first.** win10cli had no master key, no
certificate and no endpoint, which is the state ubusql1/ubusql2 can no longer
produce and the reason the scripted path had never been seen doing its real
job. It came out headed `THIS SCRIPT IS INCOMPLETE`, with ubusql1's block
reduced to a note naming exactly what was missing there and win10cli's block
complete and runnable, `CREATE CERTIFICATE [ubusql1_Cert] ... FROM BINARY =
0x3082...` included — ubusql1's genuine public key, read off the live server
while the writes were being collected rather than run. Both suppressions the
design calls for held.

**Then for real.** Every phase landed and every one was checked in the
catalogs rather than believed from the status line. The assertion that matters
is the thumbprints: `win10cli_Cert` on ubusql1 and `ubusql1_Cert` on win10cli
each equal the presenting instance's own, which is what separates a public key
that crossed from a second key pair generated at the far end. ubusql1's own
endpoint was left exactly as it was, and AAG1 was verified afterwards as two
HEALTHY replicas with both databases SYNCHRONIZED.

**The thumbprint check cost far less than the entry claimed.** It said
reproducing needed "two instances and one of them reinstalled". The comparison
is by certificate *name*, so one `CREATE CERTIFICATE [ubusql2_Cert]` on
win10cli with its own material is the entire setup. What made the test leave
nothing behind was ordering the instance list ubusql1 → win10cli → ubusql2:
`configure`'s exchange runs `for p, for other` in list order and aborts on the
first error, so `p=win10cli, other=ubusql2` raises before
`p=ubusql2, other=win10cli` can write a login, user and certificate onto
ubusql2 — verified, ubusql2 held no `win10cli_*` principal afterwards. The
reverse order is equally correct and a dirtier test.

**A refusal on a dialog whose OK would mutate production is testable through
Script Changes.** `newObjectDialog.runPipeline` calls `preflight()` before the
first apply function on both paths, so Script Changes runs `validateAddReplica`
in full with no write path behind it. That is how `validateEndpointURL`'s
refusal was seen — `endpoint URL "win10cli.fritz.box:5022" has to start with
tcp:// — the form is tcp://host:port` — on the primary of a real availability
group, with nothing sent. Connect itself had already filled the row with
`tcp://win10cli:5022` and reported `Connected to win10cli
(tcp://win10cli:5022).`, the first live sighting of either.

win10cli is left configured as ubusql1's mirroring peer on purpose: a third
instance with a STARTED endpoint is what Add Replica's Connect needs, and it is
inert otherwise. The one thing it still cannot supply is a **named** instance,
so `endpointPrincipalBase`'s `HOST$INSTANCE` spelling remains unexercised live.

## 2026-08-22 — New Index, New Statistics, and index maintenance as a script

The Indexes folder gained a "New Index ▸" cascade — Clustered, Non-Clustered,
Clustered Columnstore, Non-Clustered Columnstore, XML and Spatial — the
Statistics folder a "New Statistics...", an index's Script cascade three
maintenance verbs (REBUILD, REORGANIZE, UPDATE STATISTICS), and a statistics
object its own "Update Statistics", which opens the script rather than running
it.

**The maintenance verbs generate nothing of their own.** `indexMaintenance`
(`scripting.go`) runs gosmo's `Index.RebuildContext`/`ReorganizeContext`/
`UpdateStatisticsContext` under `gosmo.WithScript` and returns what they would
have executed. The alternative — a second copy of the statement in gossms —
drifts from the one the app runs the moment either side changes, and there is
nothing to make the drift visible. Same for the statistic's UPDATE.

**gosmo's `CreateIndexRequest` grew the rest of the index types** rather than
gossms assembling DDL: clustered columnstore (it was refused outright before),
a columnstore column list without the `ASC`/`DESC` a rowstore one carries, XML
primary/secondary, spatial tessellation with its bounding box and grid, plus
`PAD_INDEX`, `DROP_EXISTING`, `DATA_COMPRESSION`, `COMPRESSION_DELAY`, the
filegroup and the partition scheme. `validate` refuses the combinations SQL
Server refuses; `TestLiveCreateIndexRefusalsMatchTheServer` executes each of
those combinations raw against a real instance to prove the refusal is the
server's and not an invention. `Table.XMLIndexes` is new for the same reason —
`sys.indexes` cannot say which XML index is the primary one, and a secondary
index has to name it. `CreateStatisticWithOptions` is the statistics half
(FULLSCAN, filter, NORECOMPUTE, INCREMENTAL).

**Which pages a New Index dialog has depends on the type it was opened for**
(`nidxPagesFor`). A single page set would be mostly rows that do not apply, and
each of those is a statement the server rejects rather than ignores: no
`INCLUDE` on a clustered index, no fill factor on a columnstore one, no
`DATA_COMPRESSION` on an XML index, no key columns at all on a clustered
columnstore index.

**The bug the tests found: the Sort order dropdown never reached the
request.** The key column grid commits the dropdown onto the selected column
in its `OnSelectRow`, so the last edit before OK — the usual one — had no
selection change after it and was dropped. Setting a column to Descending and
pressing OK created it ascending, with the grid still showing what was asked
for. `request()` now calls the same commit first (`nidxRows.commitKeyColumn`).
Live-confirmed afterwards through Script Changes:
`CREATE UNIQUE NONCLUSTERED INDEX [IX_Orders_Customer] ON [dbo].[Orders]
([Customer] DESC, [Placed] ASC)`.

Verified live on win10cli against a throwaway database, driving the real
binary: the nonclustered index above (unique, DESC on the second column, read
back from `sys.index_columns`), a nonclustered columnstore index with
`COLUMNSTORE_ARCHIVE`, a primary XML index and then a secondary one `FOR PATH`
over it, a spatial `GEOMETRY_GRID` index with `LEVEL_1 = LOW` and
`CELLS_PER_OBJECT = 16` (both read back from
`sys.spatial_index_tessellations`), a statistics object with `WITH FULLSCAN`,
and the statistic's generated `UPDATE STATISTICS`, executed in the query panel
it opened in. The failure path was exercised too: a clustered columnstore index
on a table with an `xml` column comes back as the server's own message on the
dialog, which stays open.

Two wording nits came out of that run and were fixed: an "A xml index
requires..." note built by lowercasing the type's noun, and a columnstore
Options page explaining a fill factor it has no row for.
