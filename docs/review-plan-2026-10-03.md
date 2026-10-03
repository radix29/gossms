# Review plan — 2026-10-03

A review of both repositories, one day after the previous review plan was
finished and deleted (commit `5314188`). That plan swept both libraries
whole, so this pass concentrated on what it could not have covered: code
written while it was being implemented (CLR module scripting, Database Mail
and job-step transactions, the shared lexer, cross-database and linked-server
completion, address keys), and conventions that can be checked mechanically
across the whole of each repository. gosmo's backward compatibility is
waived, but nothing below needs a breaking change; as always, nothing in gosmo
is removed because gossms doesn't call it.

**Item IDs are `E1…E12`.** They are local to this plan. The `E` prefix appears
in no commit message or document of either repository, so it collides with
none of the `P*`…`U*` or `T*` IDs of earlier plans, nor with the permanent
`B*`/`V*`/`N*` series that `docs/open-threads.md` owns.

**Evidence markers.** *Confirmed live* was reproduced on win10cli (17.0.1135.8).
*Confirmed in code* was traced in source, with the failing input named.
*Plausible* is reasoned and needs the probe the item names. The Managed
Instance was down for the whole review; nothing here was run on it.

---

## What was checked and is clean

- **Build, format and static analysis.** `go build`, `gofmt -l`, `go vet ./...`
  (gosmo also `-tags livedb`) and `staticcheck` with every check enabled
  (minus the ST1000/1003/1020-1022 style checks) are silent on both
  repositories, apart from one ST1023 in gosmo's `dialer_cache_test.go:187`.
- **Tests.** `go test -race -count=1 ./...` is green on both.
- **Dead code.** `deadcode ./cmd/...` in gossms reports only what
  `docs/decisions.md` § By design already keeps (`UseTrackedQueries`,
  `NarrowToDMLStatement`, `StackedHistoryChart.*`, `SetPalette`,
  `SpinnerByName`).
- **Vulnerabilities.** `govulncheck ./...`: none reachable.
- **Error handling idioms.** No `err.Error()` string matching outside message
  tidying, no `context.TODO`, no `time.Local` in gosmo. Builder functions that
  return unprefixed errors (`createAsymmetricKeyStatement`, `checkReorder`, the
  listener and audit builders) are all wrapped "gosmo: …" by their callers.
- **gosmo exported API docs.** Every exported function or method has a doc
  comment except interface implementations and `AttachDatabaseRequest.DataFiles`
  (`database_attach.go:182`).
- **Recent transaction work.** The job-steps page and Database Mail apply each
  run their writes in one `InTransaction` and the `wrote` observer in
  `plannedApply` only fires on COMMIT, so a failed apply keeps its edits and a
  committed one reloads — consistent with the prop-dialog contract. E2 is the
  one gap found in it.

---

## Implementation order

Each step is one unit the user commits, gosmo first, then gossms. At the end
of every step both repositories pass `gofmt -l`, `go vet ./...` (gosmo also
`-tags livedb`), `go test -race ./...`; gossms builds against the gosmo working
tree; the step's docs move with it; and its live or tmux check has run on
win10cli (17), `SQL2017` or `SQL2016` — or, for one that needs the Managed
Instance, is recorded as pending in the item. Mark an item **Done** under its
heading when its step lands.

- **W1 — gossms-only bug fixes.** E3 (search Backspace), E4 (completion keys
  under a case-sensitive collation). tmux check for E3; E4 needs a throwaway
  `_CS_` database on win10cli.
- **W2 — gosmo correctness.** E1 (CLR trigger event groups), E2
  (`atomicBatch` inside `InTransaction`), E5 (FILESTREAM relocation names),
  E7 (receiver mirroring and its meta-test). `livedb` on 17, 14 and 13.
- **W3 — Cross-repo helpers.** E8 (collation and server-path helpers exported
  from gosmo, gossms copies deleted), then E6 (identifier continuation runes)
  on top of it. Behaviour-preserving except where E6 intends a change.
- **W4 — Small consistency items.** E9 (pending the Managed Instance), E10,
  E11, E12.

No step depends on a later one.

---

## Bugs

### E1 — A CLR DDL trigger declared on an event group is scripted with the group's individual events — gosmo — *confirmed live*

**Done** (W2). Probe: `event_group_type_desc` names the *declared* group, not
the innermost (`FOR DDL_DATABASE_LEVEL_EVENTS` → 158 rows, all naming it), so
no `parent_type` walk. Live pins on 17/14/13: a database trigger `FOR
DDL_TABLE_EVENTS, CREATE_VIEW` and a server trigger `FOR DDL_LOGIN_EVENTS`.

`clrEvents` (`scripter_module_clr.go`) reads `type_desc` from
`sys.trigger_events` / `sys.server_trigger_events`. For a trigger created
`FOR DDL_TABLE_EVENTS` that view holds one row per member event, each with
`event_group_type_desc = 'DDL_TABLE_EVENTS'` (probed on win10cli: three rows,
CREATE_TABLE/ALTER_TABLE/DROP_TABLE). The rebuilt CREATE therefore says `AFTER
CREATE_TABLE, ALTER_TABLE, DROP_TABLE`: it runs, but the recreated trigger no
longer fires for events later added to the group, and a broad group
(`DDL_DATABASE_LEVEL_EVENTS`) scripts as a very long list. A T-SQL trigger is
unaffected — its stored definition is replayed.

- **Fix.** Select `COALESCE(te.event_group_type_desc, te.type_desc)`, distinct,
  ordered by the minimum `type` in each group, so a mix such as `FOR
  DDL_TABLE_EVENTS, CREATE_VIEW` survives. Leave `DatabaseTrigger.Events` /
  `ServerTrigger.Events` (the listing) as they are — their doc already says
  they hold individual events.
- **Probe first.** For a nested group (`DDL_DATABASE_LEVEL_EVENTS` contains
  `DDL_TABLE_VIEW_EVENTS` contains `DDL_TABLE_EVENTS`), check whether
  `event_group_type_desc` names the declared group or the innermost one. If the
  innermost, walk `sys.event_notification_event_types` (`parent_type`) up to the
  outermost group whose members are all present.
- **Pins.** A golden test of `clrEvents`' rendering; extend
  `live_clr_function_test.go` with a database trigger `FOR DDL_TABLE_EVENTS`
  and a server trigger `FOR DDL_LOGIN_EVENTS`, on 17, 14 and 13.

### E2 — `atomicBatch` inside `InTransaction` ends the caller's transaction on failure and leaves XACT_ABORT on — gosmo — *confirmed live*

**Done** (W2). Inside a transaction the statements go as `atomicTxBatch` —
`BEGIN TRY … END TRY BEGIN CATCH THROW; END CATCH`, no transaction statements
or SET — so it stops at the first failure with the server's message. A failed
atomic write marks the transaction (`serverTx.fail`) and `InTransaction`
refuses to COMMIT even if `fn` swallowed the error: the statements before the
failing one are in the transaction, and committing them is the half-done
write. Deviation: six callers, not five — `Job.AddSchedule` with an owner was
missed by the review. gossms's job-steps page test now asserts the
in-transaction form.

`atomicBatch` (`script.go`) renders `SET XACT_ABORT ON; BEGIN TRY BEGIN
TRANSACTION … COMMIT … END TRY BEGIN CATCH IF @@TRANCOUNT > 0 ROLLBACK
TRANSACTION; THROW; END CATCH`. Five writes use it (`CreateJob`, job-step
reorder, `MailProfile.SetAccounts`, `CreateLogin`, `Table.Drop` with cascade), and
gossms now calls two of them inside `InTransaction` (job steps,
Database Mail). Probed on win10cli with an outer `BEGIN TRANSACTION`, one
insert, then a failing atomic batch:

- `@@TRANCOUNT` afterwards is **0**: the CATCH's bare `ROLLBACK` rolled back
  the caller's transaction too, including the insert made before the batch.
- A statement run afterwards **autocommits** (`@@TRANCOUNT` stays 0, its row
  stays).
- `XACT_ABORT` stays ON for the rest of the session's transaction.

gossms is safe today only because every `fn` returns at the first error, so
`InTransaction` rolls back (a no-op) and reports. A caller that tolerates an
error inside `fn` — "drop if exists", "already a member" — would carry on
outside any transaction, its later writes committed one by one, and then get
`COMMIT` failing with Msg 3902 while those writes stay.

- **Fix (recommended).** Make the wrapper transaction-aware: an
  `execAtomic(ctx, stmts)` (and `execPasswordsAtomic` for `CreateLogin`) on
  `*Server` that, when `txFrom(ctx, s) != nil`, sends the statements plain —
  the outer transaction already makes them all-or-nothing — and otherwise the
  current `atomicBatch` text. Under `Scripting(ctx)` keep today's wrapper (a
  script has no outer transaction). The alternative, `SAVE TRANSACTION` plus
  `ROLLBACK TRANSACTION <savepoint>` in the CATCH, keeps one text but cannot
  undo a doomed transaction under XACT_ABORT, so it is not recommended.
- **Pins.** A unit test of the rendered text with and without a transaction in
  ctx; `live_transaction_test.go`: `InTransaction` running a successful insert
  then a `CreateJob` that fails (duplicate name) leaves neither, `@@TRANCOUNT`
  0, and an `fn` that swallows that error and writes again gets the COMMIT
  error *and* no committed rows.
- **Docs.** `InTransaction`'s doc gains a line on `atomicBatch` writes; the
  `atomicBatch` comment's XACT_ABORT paragraph says it applies outside a
  transaction only.

### E3 — Execution-plan search Backspace cuts a multi-byte character in half — gossms — *confirmed in code*

**Done** (W1). `core.TrimLastGrapheme`; tmux-checked on win10cli (the search
key is `/`, not Ctrl+F).

`PlanView.handleSearchKey` (`internal/tui/planview/search.go:41`) deletes
`query[:n-1]` — one byte. After typing a non-ASCII character (`é`, any CJK),
one Backspace leaves an invalid UTF-8 byte in the query: the search box draws a
replacement glyph and the search matches nothing. Every other text field goes
through `widgets.InputField`.

- **Fix.** Trim the last grapheme cluster — the input arrived as `ev.Str()`,
  which may itself be a cluster. `core` already walks graphemes with
  `displaywidth.StringGraphemes` (`splitGraphemeWidth`, `strutil.go`); add a
  `core.TrimLastGrapheme` beside it rather than a second segmenter.
- **Pins.** A test typing `é` then Backspace and asserting an empty, valid
  query. tmux: Ctrl+F in a plan, type `é`, Backspace, type `x`, Enter.

### E4 — IntelliSense keys objects by lowercased name whatever the database's collation — gossms — *confirmed in code*

**Done** (W1). Deviation: the collation is read in the inventory's own fetch
(`DatabaseByName`, beside `CallerDefaultSchema`) rather than taken from the
directory, which may not have loaded when a panel's own inventory does — so
`directoryEntry` is unchanged. The sys-schema and linked inventories fold.
Contained databases' catalog collation is left as `docs/open-threads.md` N2.
Live on win10cli in a `Latin1_General_CS_AS` database: `Orders`/`orders` each
list their own columns, `Id`/`ID` both offered, `ORDERS.` answers nothing.

`completionInventory` (`completion_inventory.go:102-113`) keys
`byQualifiedName`, `bySchema` and `fnByQualifiedName` by `strings.ToLower`, and
`findCatalogObject`/`chainCandidates` (`completion_candidates.go:38-60`,
`completion_crossdb.go`) compare and de-duplicate the same way. In a
case-sensitive database holding `dbo.Orders` and `dbo.orders` — two tables —
the second overwrites the first, so `Orders.` offers `orders`' columns. The
database directory beside it (`completionDirectory`) was fixed for exactly
this shadowing with a collation-aware `nameMap`; the inventory was not.

- **Fix.** Key the three maps with `nameMap` under the database's collation
  (add `collation` to `directoryEntry` — `gosmo.Database` already carries it —
  and read the panel's own database's from the same list). Prefix *filtering*
  stays case-insensitive: that is a matching convenience, not identity.
  Column de-duplication (`completion_candidates.go:261`,
  `completion_relations.go:512`) follows the same rule.
- **Pins.** A unit test with a fake CS catalog holding both tables. Live: a
  throwaway `Latin1_General_CS_AS` database on win10cli with `Orders`/`orders`
  of different columns; tmux check that each qualifier lists its own columns.

### E5 — A renamed restore gives FILESTREAM containers a `.ndf` extension — gosmo — *confirmed in code*

**Done** (W2). A renamed `S`/`F` file also drops whatever followed a dot in the
original directory name. `TestLiveRestorePlanFilestreamContainer` on 17 (14
and 13 have no FILESTREAM and skip); tmux Restore dialog to a new name on
win10cli put the container at `…\gossms_e5_fs_copy_e5fs`.

`RestoreRelocation.Moves` (`restore_plan.go`) names every non-log file
`<target>_<logical><ext>` and supplies `.ndf` when the source has no extension.
A FILESTREAM container (`Type "S"`) — and a legacy full-text catalog (`"F"`) —
is a directory and never has one, so a database restored under a new name gets
its container at `<DataDir>\<target>_<logical>.ndf`: a directory with a data
file's extension. The restore succeeds; the name misleads anyone looking at the
disk, and a later "delete *.ndf" clean-up hits it.

- **Fix.** For `S` and `F`, no extension: `<target>_<logical>`.
- **Pins.** A `restore_plan_test.go` case per type. Live on win10cli (FILESTREAM
  is enabled there): back up a throwaway FILESTREAM database, restore it under a
  new name with Relocate all files, check `sys.database_files.physical_name`,
  drop both. Then the same through gossms's Restore dialog (tmux).

### E6 — The lexer ends a word at `$`, `#` and `@`, which T-SQL allows inside an identifier — gossms — *confirmed in code*

**Done** (W3). The editor's backward word scans (auto-open trigger,
`currentTokenStart`) go through a new `sqltext.WordStart`, which walks back
over continuation runes and then forward to the first rune that can start a
word — so a `$` never starts one and a `#`/`@` sigil stays before it, as
before. One intended change beyond the item: `#@x` is now one word (a valid
temp-table name), where the old lexer test pinned it as two. The `sqlparse`
golden sweep and `PrefixCache` tests pass unchanged (the corpus holds none of
these runes). tmux on win10cli, column `Price$`: `i.Pri` offers it, the popup
stays open through `ce$`, `i.Qty#` offers `Qty#1`, and the highlighter draws
`Price$` as one word with `$5` unchanged.

A regular identifier's later characters may be letters, digits, `_`, `@`, `#`
or `$`. gosmo's `isRegularIdentifier` (`quoting.go:49`) follows that rule, so
`QuoteNameIfNeeded("Price$")` returns `Price$` bare and completion inserts it
bare. gossms's lexer (`sqltext.IsWordRune`, `internal/tuikit/sqltext/lexer.go`)
does not: it lexes `Price$` as `Price` then `$`. Effects: the highlighter breaks
the word; completion's prefix restarts after the `$`, so the popup offers
nothing; `sqlparse` resolves `t.Price` instead of `t.Price$`.

- **Fix.** A separate `sqltext.IsWordContinue(r)` = `IsWordRune(r) || r == '$' ||
  r == '#' || r == '@'`, used by `wordEnd` and the completion trigger. Keep
  `IsWordRune` for a word's first rune, so `$5.00` (money) and `@@ROWCOUNT` lex
  as today. `core.IsWordRune` (editor word motion) is deliberately separate
  (`docs/decisions.md` § T-SQL lexing) and stays.
- **Pins.** Lexer cases (`Price$`, `t#1`, `a@b`, `$5`, `@x@y` as one variable);
  re-run the `sqlparse` golden sweep and the `PrefixCache` equivalence tests.
- **Decision 2** asks whether `#`/`@` mid-word are in scope or `$` alone.

---

## Consistency

### E7 — Five setters don't mirror their write onto the receiver — gosmo — *confirmed in code*

**Done** (W2). `SetAccounts` reads the links back after a write, since a newly
linked account's id is the server's. The meta-test follows calls to receiver
methods and package functions (`setReplicaKeyword` for the three AG
setters); the exceptions map is empty. `TestLiveLoginSettersMirror` compares
the handle with `LoginByName` after each login setter.

gosmo's convention (`CLAUDE.md` § Script mode) is that a write changing a
scanned field mirrors it through `setIfApplied`; 151 sites do. An AST scan of
every exported `Set*` method on a type with the matching field found five that
neither mirror nor delegate to an `Alter` that does:

| Method | Field left stale |
|---|---|
| `Login.SetDefaultLanguage` (`login.go:227`) | `DefaultLanguage` |
| `Login.SetPasswordPolicy` (`login.go:238`) | `IsPolicyChecked`, `IsExpirationChecked` |
| `Login.ChangePassword` with `MustChange` (`login.go:265`) | `IsExpirationChecked` (MUST_CHANGE turns it on) |
| `Index.SetIncludedColumns` (`index_management.go:262`) | `IncludedColumns` |
| `MailProfile.SetAccounts` (`database_mail_write.go:507`) | `Accounts` (`[]*MailProfileAccount`) |

Its sibling `SetDefaultDatabase` does mirror, so a caller reading the handle
after both sees one value updated and one not.

- **Fix.** `setIfApplied` in each. `Accounts` holds ids as well as names: build
  the new list from the links `SetAccounts` already read (`current`) plus the
  names it added, or leave it out with an exceptions-map entry saying why.
- **Pin.** Turn the scan into a test (`receiver_mirror_test.go`, beside
  `parent_accessor_wiring_test.go`): every exported `Set<Field>` on a type with a
  field `<Field>` or `Is<Field>` calls `setIfApplied`, or calls a method that
  does (`Alter`), or is listed in an exceptions map with a reason. The scan
  script used for this review found 13 candidates, 8 of them delegating
  correctly.

### E8 — Collation-folding and server-path helpers are written twice, and the copies differ — both — *confirmed in code*

**Done** (W3). gosmo `collation.go` and `server_path.go` (with
`serverPathSeparator` moved there). Beyond the listed helpers, gosmo's
`pathExt` (`database_attach.go`) and `splitServerPath` were further copies and
now use them; `pathExt` counted a lone ".ldf" as an extension, `ServerPathExt`
does not. `ServerPathDir` keeps a root's separator (`C:\a.bak` → `C:\`), so
`EventFiles` on a pattern at a drive root now lists `C:\` rather than `C:`.
gossms's join differed only for a dir ending in two separators. gossms's
`xeBlobFilename` also had an inline base and uses `ServerPathBase`. Live:
gosmo's restore-plan, attach, create-database and Extended Events tests on 17,
14 and 13.

- **Collation.** gosmo's `sameDatabaseName` (`restore_plan.go:170`) and
  gossms's `collationFoldsCase`/`sameName` (`internal/tui/name_set.go`) are the
  same token rule (`CS`, `BIN`, `BIN2`), maintained separately.
- **Server paths.** gosmo has `joinServerPath` (`server.go:738`),
  `serverPathBase`, `serverPathExt` (`restore_plan.go`), `splitServerPath`
  (`extended_events_read.go:377`) and `serverPathSeparator`
  (`filesystem.go:230`); gossms has its own `joinServerPath` and
  `serverPathBase` (`internal/tui/backup_common.go`). The two joins differ:
  gossms trims every trailing separator, gosmo keeps one.

- **Fix.** Export from gosmo, additively: `CollationIgnoresCase(collation)
  bool`, `SameName(collation, a, b) bool`, and `JoinServerPath`,
  `ServerPathBase`, `ServerPathDir`, `ServerPathExt` — gosmo's internal users
  switch to them and keep one implementation. gossms deletes its copies;
  `nameSet`/`nameMap` keep their types and call the gosmo rule. Pick one join
  behaviour (gosmo's, which preserves a root like `C:\` exactly) and pin the
  edge cases (`C:\`, `/`, `\\host\share\`, empty dir) in gosmo.
- Not a removal: every gosmo helper stays, under an exported name.

### E9 — XE Advanced page gates MAX_DURATION by version number, gosmo by "Azure is newest" — gossms — *confirmed in code; MI pending*

**Pending** (W4). Blocked on the Managed Instance; gossms's gate kept, the
probe recorded under V3 in `docs/open-threads.md`.

`newXEOptionRows` (`internal/tui/xevent_session_advanced.go:59`) shows Maximum
duration only when `major >= 17 && !azure`. gosmo reads `max_duration` with
`colSince(major, SQLServer2025, …)`, and `serverMajorVersion` reports 0
(newest) on Azure, so on a Managed Instance gosmo reads the column and gossms
hides it. The hidden row preserves the value (`apply` skips it), so nothing is
lost — the two layers simply disagree about what the instance supports.

- **Fix.** One answer in gosmo — `ServerInfo.SupportsXEMaxDuration()` or a
  capability key alongside the existing ones — and gossms asks it.
- **Pending V3/V4** (`docs/open-threads.md`): whether MI accepts `MAX_DURATION`
  decides the answer for EngineEdition 8. Until then, keep gossms's gate.

### E10 — `Login.MapToDatabase` is documented by a section banner only — gosmo — *confirmed in code*

**Done** (W4). By the time W4 ran `MapToDatabase` had a one-line doc; it now
says what it creates, that it reads the database by name first, and that
`defaultSchema` "" omits DEFAULT_SCHEMA. The undocumented `DataFiles` is the
method `DetachedDatabase.DataFiles` (it shared `LogFiles`' comment, which `go
doc` attaches to `LogFiles` only), not a field of `AttachDatabaseRequest`;
each now has its own.

`login.go:474`'s only comment is `// -- User mapping ---…`, which `go doc`
shows as its doc. Give it a real one (what it creates, that it reads the
database by name first, that `defaultSchema` "" means none). Also
`AttachDatabaseRequest.DataFiles` (`database_attach.go:182`).

---

## Simplification and performance

### E11 — `ToggleGridRow.Validate` restates the embedded `GridRow.Validate` — gossms — *confirmed in code*

**Done** (W4).

Both return nil (`internal/tuikit/propsheet/togglegrid.go:203`,
`gridrow.go:142`). After `5314188` made `Dirty`/`Revert` defer to the embedded
row, the explicit `Validate` is the last shadowing method and suggests a
difference there isn't. Delete it; fix the comment above `Dirty` ("Dirty and
the Revert and Validate beside it").

### E12 — Object Explorer recomputes every label's display width on each rebuild — gossms — *plausible, measure first*

**Closed, under budget** (W4). `BenchmarkTreeViewSetNodes50k`
(`treeview_bench_test.go`: 50k labels at depth 4 with an icon, selection on
the last node): 10.1–12.6 ms/op over five runs, 50,000 allocs/op. Profile:
~75% in `lineWidth` (`DisplayWidth` of the label ~58%, `string(n.Icon)`'s
per-node allocation ~13%), `indexOf` ~3%. No structural fix, per the rule
below. The icon is now measured with `core.RuneWidth(n.Icon)` (in `lineWidth`
and `TreeNode.onText`; equal to `DisplayWidth(string(r))` for every rune
U+0020–U+10FFFF, checked exhaustively): 6.4–7.0 ms/op, 0 allocs/op — the
non-ASCII icon string was sending every row through the grapheme iterator.

`ObjectExplorer.rebuild` flattens the whole forest and
`TreeView.SetNodes` (`internal/tuikit/controls/treeview.go:124`) calls
`core.DisplayWidth` on every visible label to find `contentW`, then
`indexOf` scans linearly. Every expand, collapse, load or refresh pays this
for all expanded nodes. With a 50,000-table folder open, expanding one table's
Columns re-measures 50,000 labels.

- **Measure first.** A benchmark: 50k-node flat list, `SetNodes` cost. If it is
  under a frame (16 ms) at 50k, record the number here and close the item.
- **Fix, if it is not.** Cache the label width on `TreeNode` (set when the
  node is built; labels change only on rename), and keep `contentW` as a
  running max the host can reset.

---

## Decisions (settled 2026-10-03)

1. **E2** — plain statements inside a transaction (`execAtomic` /
   `execPasswordsAtomic`); today's wrapper outside one and under `Scripting`.
2. **E6** — all three continuation runes: `$`, `#`, `@`.
3. **E8** — free functions: `CollationIgnoresCase`, `SameName`,
   `JoinServerPath`, `ServerPathBase`, `ServerPathDir`, `ServerPathExt`.
4. **E12** — measure first; fix only if `SetNodes` at 50k exceeds 16 ms.

---

## Implementation list

### W1 — gossms bug fixes
- [x] E3 — `core.TrimLastGrapheme` beside `splitGraphemeWidth` (`strutil.go`) + test
- [x] E3 — `planview/search.go` Backspace uses it; test `é`+Backspace → empty valid query
- [x] E3 — tmux: `/` (not Ctrl+F) in a plan, `é`, Backspace, `x`, Enter
- [x] E4 — collation read in the inventory fetch (`DatabaseByName`) instead of `directoryEntry` — see the item
- [x] E4 — key `byQualifiedName`/`bySchema`/`fnByQualifiedName` with `nameMap` under that collation
- [x] E4 — `findCatalogObject`, `chainCandidates`, column de-dup (`completion_candidates.go:261`, `completion_relations.go:512`) follow the same rule; prefix filtering stays case-insensitive
- [x] E4 — unit test: fake CS catalog with `dbo.Orders` + `dbo.orders`
- [x] E4 — live: throwaway `Latin1_General_CS_AS` db on win10cli, tmux check each qualifier's columns, drop db

### W2 — gosmo correctness
- [x] E1 — probe nested groups (`DDL_DATABASE_LEVEL_EVENTS`) for which group `event_group_type_desc` names
- [x] E1 — `clrEvents`: `COALESCE(event_group_type_desc, type_desc)`, distinct, ordered by min `type` (walk `parent_type` if the probe says innermost)
- [x] E1 — golden test of rendering; `live_clr_function_test.go` db trigger `FOR DDL_TABLE_EVENTS` + server trigger `FOR DDL_LOGIN_EVENTS` on 17/14/13
- [x] E2 — `execAtomic(ctx, stmts)` + `execPasswordsAtomic` on `*Server`; plain when `txFrom(ctx, s) != nil` and not scripting
- [x] E2 — switch the five callers (`CreateJob`, job-step reorder, `MailProfile.SetAccounts`, `CreateLogin`, `Table.Drop` cascade)
- [x] E2 — unit test of rendered text with/without tx; `live_transaction_test.go` cases (failing `CreateJob` rolls back the prior insert, `@@TRANCOUNT` 0; swallowed error → COMMIT error, no rows)
- [x] E2 — docs: `InTransaction` line on atomic writes; `atomicBatch` XACT_ABORT paragraph scoped to outside a transaction
- [x] E5 — `RestoreRelocation.Moves`: no extension for types `S` and `F`; `restore_plan_test.go` case per type
- [x] E5 — live: FILESTREAM db backup → restore renamed, check `physical_name`, drop both; then via gossms Restore dialog (tmux)
- [x] E7 — `setIfApplied` in `SetDefaultLanguage`, `SetPasswordPolicy`, `ChangePassword` (MustChange), `SetIncludedColumns`, `SetAccounts` (or exception with reason)
- [x] E7 — `receiver_mirror_test.go` AST meta-test with exceptions map
- [x] W2 — `livedb` on 17, 14, 13 (2026-10-03, after W4). Two test-only
  failures, both fixed and re-run green on all three:
  `TestLiveEveryProbedPermissionNameIsOneTheServerDefines` (13, 14) had not
  exempted the nine 2022-only event-session names added 2026-09-30;
  `TestLiveDatabaseMailReads` (17, once in three) read the failed item before
  DatabaseMail.exe logged its error event — the event now gets its own wait.

### W3 — cross-repo helpers
- [x] E8 — gosmo: export `CollationIgnoresCase`, `SameName`, `JoinServerPath`, `ServerPathBase`, `ServerPathDir`, `ServerPathExt`; internal users switch, one implementation each
- [x] E8 — gosmo: pin join edges (`C:\`, `/`, `\\host\share\`, empty dir) with gosmo's keep-one-separator behaviour
- [x] E8 — gossms: delete `collationFoldsCase`/`sameName` (`name_set.go`) and `joinServerPath`/`serverPathBase` (`backup_common.go`); `nameSet`/`nameMap` call gosmo; check callers for the join-behaviour change
- [x] E6 — `sqltext.IsWordContinue` (`$`, `#`, `@`); used by `wordEnd` and the completion trigger; `IsWordRune` unchanged for first rune
- [x] E6 — lexer cases (`Price$`, `t#1`, `a@b`, `$5`, `@x@y`); re-run `sqlparse` golden sweep and `PrefixCache` equivalence tests
- [x] E6 — tmux: highlighter + completion on a `Price$` column

### W4 — small consistency items
- [ ] E9 — **pending**: blocked on V3/V4 (MI down); keep gossms's gate, record as pending
- [x] E10 — doc comments for `Login.MapToDatabase` and `AttachDatabaseRequest.DataFiles`
- [x] E11 — delete `ToggleGridRow.Validate`; fix the comment above `Dirty`
- [x] E12 — benchmark `TreeView.SetNodes` at 50k nodes; record result here; fix (cached width, running-max `contentW`) only if > 16 ms
