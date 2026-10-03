# Review plan — 2026-10-02

A review of both repositories for bugs, inconsistencies, optimisations,
simplifications and architecture. Three parallel read-only passes:
**gosmo** (whole library), **gossms `internal/tui`** (excluding `sqlparse`),
and **gossms `internal/tuikit`, `sqlparse`, the non-UI packages, `cmd/`** plus
the gossms↔gosmo boundary. The plan lists items in priority order within
each section. gosmo's backward compatibility is explicitly waived, so the
items in § gosmo API are breaking by design. As always, nothing in gosmo is
removed because gossms doesn't call it.

**Item IDs are `T1…T73`** (with gaps). They are local to this plan and chosen so they
collide with none of the `P*`, `Q*`, `R*`, `S*` IDs that earlier plans left in
commit messages, and none of the permanent `B*`/`V*`/`N*` series that
`docs/open-threads.md` owns.

**Evidence markers.** *Confirmed* means traced in source, reproduced with a
throwaway overlay test, or following directly from documented SQL Server or
go-mssqldb behaviour. *Plausible* means reasoned but not executed. No item
was probed against a live server: the Azure SQL Managed Instance was down
during the review. Every item therefore names what a live or tmux run must
show before it is called fixed (`docs/testing.md`).

---

## What was checked and is clean

- **Formatting, vet and static analysis.** `gofmt -l`, `go vet ./...`,
  `go vet -tags livedb ./...` (gosmo) and `staticcheck ./...` are silent on both
  repositories, apart from three deprecated `ast.Object` uses in gosmo's
  `write_through_read_test.go`.
- **Tests and dead code.**
  - `go test -race ./...` passes on gosmo and on every gossms package except
    one flaky `internal/tui` test (T18).
  - `deadcode` reports what `docs/decisions.md` already keeps on purpose, plus
    `sqlparse.NarrowToDMLStatement`, `sqlparse.TokenizeRange` (T41) and
    `nameMap.Len` (T63).
- **gosmo quoting.** A go/types scan of every SQL-building `Sprintf` found
  every string argument quoted, or else a validated enum or int.
- **gosmo per-row queries.** No query runs inside a `rows.Next()` loop.
- **gossms UI-goroutine discipline.** AST scans over every `safego`, `fanOut`,
  `runWithProgress` and `runPageAction*` body found no function literal that
  writes UI state off the UI goroutine. The leaks are behind helpers and
  method values (T6, T7).
- **gossms idioms.**
  - There is no bare `go`.
  - `context.Background()` appears only at the documented exceptions.
  - No `len()` is used for column math.
  - No T-SQL is built with `Sprintf` in `internal/tui`.
  - Every grid host calls `DrawOverlay`.
  - `DatabaseRef` is used correctly.
- **tuikit dependencies.** `internal/tuikit` imports nothing under
  `internal/tui`. Its README dependency graph is stale (T43).

---

## Implementation order

This is the only ordering in the plan; the sections below are grouped by
kind, not by order. Steps are `W1…W28`. Each one is a single unit the user
commits: the gosmo commit first, then the gossms one. When a step is done,
both repositories are in a consistent state.

**What "consistent" means at the end of every step:**
- **Both repositories pass the checks.** `gofmt -l` is empty, and
  `go vet ./...` is clean (in gosmo also `go vet -tags livedb ./...`).
  `go test ./...` passes in both.
- **gossms builds against the gosmo working tree.** It goes through the
  active `replace`, so no gosmo change lands without its gossms call sites.
- **The step's docs move with it.**
  - `docs/open-threads.md` and `docs/decisions.md` (and gosmo's own open-threads file).
  - An `ARCHITECTURE.md` § Package map row for any new `internal/tui` file.
  - Doc comments the change makes wrong.
- **This plan is updated.** Each item the step finishes is marked **Done**
  under its heading, as earlier plans did.
- **The step's live or tmux check has been run, or recorded as pending.**
  - A check that needs the Managed Instance is recorded as pending in the
    item.
  - A check that can run on win10cli 17, `SQL2016` or `SQL2017` is not
    deferred.

No step depends on a later one. Where an item has an interim and a final fix,
the step says which it ships.

### Stage 1 — baseline and the dangerous bugs (gossms first, small)

- **W1 — Green baseline.**
  - Items: T18 (the flaky `-race` test) and T15 (startup errors to stderr).
  - Done when: `go test -race ./...` is green, so every later step can
    require it.
  - **Done.** `go test -race -count=1 ./...` green in three runs in
    gossms, one in gosmo. The full run surfaced a second flake of the same
    class, `TestRGNewClassifierClosesTheDialog` ("General did not load"),
    fixed the same way; `docs/testing.md` now carries the rule.
- **W2 — T1, the Detail Browser wrong-target Delete.**
  - gossms only.
  - Includes a pin and the tmux two-server check.
  - **Done.** `go test -race -count=1 ./...` green; tmux two-server check
    run on win10cli (A) and `SQL2017` (B). See T1.
- **W3 — T2 and T3, the interim Ctrl+Enter fix.**
  - Port sqlparse's ender set and the "WITH (" and EXEC rules into
    `sql_statement.go`, and fix its "mirrors exactly" comment.
  - The shared lexer (W22) replaces this later. The interim fix ships now
    because the bug runs DDL.
  - **Done.** `go test -race -count=1 ./...` green; tmux check run (no
    server needed: the selection is what F5 runs). See T2 and T3. The plan
    names W22 here; the shared lexer is W23.
- **W4 — T4, `CreateLoginRequest`.**
  - gosmo: add the new fields (additive), with script golden tests.
  - gossms: fill the new fields in `new_login_pages.go` and drop the two
    follow-up ALTERs.
  - Live check: on 17 (Linux) and 13.
  - **Done.** `go test -race -count=1 ./...` green in both repositories;
    the new `livedb` test passes on 17 (Windows and Linux), 14 and 13, and
    the tmux check ran on ubusql1 (17, Linux) and `SQL2016`. See T4.

### Stage 2 — gosmo correctness with no gossms call-site change

Each of these steps is gosmo only, and gossms only needs to rebuild. Any
breaking signature has no gossms caller.

- **W5 — DMV reads.**
  - Items: T5 (`CROSS APPLY` from `sys.indexes`), T11 (filter IN_ROW_DATA and
    aggregate per index), T25 (`OUTER APPLY` for stats).
  - If T11 adds `PartitionNumber` instead of aggregating, `index_props.go:424`
    changes in this same step.
  - **Done.** gosmo only: T11 aggregates, so `index_props.go` is unchanged.
    `go test -race -count=1 ./...` green in both repositories; the new
    `livedb` test passes on 17, 14 and 13 (and fails 4 of 6 against the old
    code); tmux check on `SQL2017`. See T5, T11 and T25.
- **W6 — Write-path correctness.**
  - Items: T21 (gate Restore's MULTI_USER repair on `batchCutShort`), T22
    (`BulkInsert` under `WithScript` and the observer), T30 (`atomicBatch` for
    `CreateJob` and `CreateLogin`), T24 (`ObjectFilter` day in UTC).
  - **Done.** gosmo only; gossms just rebuilds. `go test -race -count=1
    ./...` green in both repositories; the affected `livedb` tests (restore
    close, bulk insert, object filter, jobs, agent, logins, atomic batch,
    single-user) pass on 17, 14 and 13, and the new T24 subtest fails against
    the old code. tmux check on `SQL2017`: New Job creates and enlists the
    job through the one batch. That run found B14 (New Job's Owner default),
    recorded in `docs/open-threads.md`. See T21, T22, T24 and T30.
- **W7 — Scripting fidelity and the small API fixes with no gossms callers.**
  - Items: T12 (table types), T31 (`FuncType`, CLR functions), T23
    (`AddSchedule` takes `CreateScheduleRequest`), T26 (capability key
    separator).
  - For T26, check first that gossms never builds a key literal; the review
    found only helper calls.
  - Also: the gosmo docs drift from § Consistency (`errors.go` doc splice,
    `%w` in `schedulerGroupSizes`, `CurrentDatabase` doc, Ref count).
  - **Done.** `go test -race -count=1 ./...` green in both repositories; gofmt
    and vet (gosmo also `-tags livedb`) clean. New `livedb` tests pass on 17,
    14 and 13: `live_table_type_script_test.go` (T12),
    `live_clr_function_test.go` (T31), `live_add_schedule_test.go` (T23) and
    `live_dotted_capability_keys_test.go` (T26, which fails against the old
    separator). gossms is not a rebuild alone: T31 makes
    `nodeData.FuncType` a `gosmo.FunctionType`, and T26 moved the test
    fixtures' fake probe rows onto `probeKey`. tmux check on
    `SQL2017`: Functions lists two CLR functions, SELECT To on the CLR scalar
    one writes `SELECT [dbo].[clr_twice](…)`, CREATE To on it reports the
    refusal in the status bar, and a table type's CREATE To carries its key,
    unique, check, filtered index and collation. See T12, T23, T26, T31 and
    § Consistency.

### Stage 3 — gossms UI-thread, tree and cache correctness

- **W8 — Apply runs no UI code off-thread.**
  - The order inside the step matters:
    1. T6 (commit in preflight; capture the request).
    2. The page commit hook in `PropDialog` and `newObjectDialog`.
    3. Move the 14 `commitCurrent()` calls into it.
    4. Retire `commitApplied`, after the live partial-failure check.
    5. Finally, tighten `TestApplyClosuresDoNotWritePageState` (T7).
  - The test goes last because it is red until the code it checks is fixed.
  - **Done.** `go test -race -count=1 ./...` green; gofmt and vet clean
    (gossms only — gosmo is untouched). The tightened test found a fifteenth
    commit-in-apply the review missed (`syncToggles`, New Database's
    Filegroups page), and fails on the old tree with the T6 and T7 leaks.
    Live on `SQL2017`: the partial-failure check first (below, T7), then a
    Files-page edit left in the editor saved by OK, and New Index's Script
    Changes and OK carrying a sort order left in the editor. On ubusql1, New
    Database Mirroring Endpoint's and New Availability Group's Script Changes
    (nothing written). See T6 and T7.
- **W9 — One tree-refresh path.**
  - T8 first (`Reload` in both `Refresh*Folder` functions).
  - Then T27 (every post-create, attach and restore refresh goes through
    `ReloadFolders(sc, folderOf(…))`).
  - Then delete `RefreshDatabasesFolder` and `RefreshLoginsFolder` if no
    callers are left.
  - **Done.** `go test ./...` green; gofmt and vet clean (gossms only — gosmo
    is untouched). All three hand-rolled refreshers (`RefreshDatabasesFolder`,
    `RefreshLoginsFolder`, `RefreshFolderByType`) are gone; every refresh
    after a write is `ReloadFolders` with `folderOf` or the new `sameNodeAs`,
    and the async completions that held `node.parent` (delete, rename, move
    to schema, enable/disable, endpoint state, AG ops, failover, log recycle,
    Detail Browser delete) moved with the dialogs. Live on `SQL2017`: New
    Database, New Login, New User in it, delete of the login, Take Offline /
    Bring Online, rename and delete of the database — each showed in the tree
    with no manual Refresh. The retired-node race itself is not reproducible
    by hand (every path to it is modal); it is pinned. See T8 and T27.
- **W10 — Identity-aware connection keys: T9 and T60.**
  - **The fold function moves below both packages.** `internal/config` cannot
    import `internal/db` (db imports config). So `InstanceKey` and
    `ConnectionAddress` move to `internal/config`, or to a new leaf package,
    and `db` re-exports or calls them.
  - **The tracked-query file needs migrating.**
    - `tracked.json` is keyed by `serverKey` today, and that key changes.
    - So `loadTracked` re-folds every stored key through the new function
      and merges sets that collide.
    - Pin: an old-format file that loads into the new keys.
  - IntelliSense caches and saved OE filters are in-memory or re-derived, so
    they need no migration.
  - **Done.** `go test ./...` green; gofmt and vet clean (gossms only — gosmo
    is untouched). `ResolveServer`, `InstanceKey` and `ConnectionAddress`
    moved verbatim from `db` to the new `internal/config/address.go`, with
    their tests; `db` and `tui` call them there, with no re-export. One change
    to `InstanceKey`: it trims the whole address before parsing, so
    `" host,1433 "` loses its port as `"host,1433"` does (the old tracked fold
    trimmed too). Live on `SQL2017`: a scratch `tracked_queries.json` holding
    `win10cli\sql2017,55253` → [3] and `win10cli\sql2017` → [2] for a
    throwaway Query Store database; connected as `WIN10CLI\SQL2017`, the
    Tracked Queries leaf listed both 3 and 2 (the old fold showed only 2), and
    `SELECT * FROM dbo.` completed `dbo.t` on the new keys. The split between
    Windows and Entra identities could not be run here (neither auth works
    from this host), so it is pinned only. See T9 and T60.
- **W11 — Async dialog plumbing.**
  - T19: one `latest` per load kind in Backup, Restore, Attach and New
    Snapshot, abandoned on show and on close.
  - Also T69, T70, T28, and the T61 items (`BeginTimeout`, the "cancelled"
    wording, `applyNow` without `InvalidateAll`).
  - Plus the F1-cycles-buttons change, once decided.
  - **Done.** `go test -race -count=1 ./...` green; gofmt and vet clean
    (gossms only — gosmo is untouched). T70 needed no change (see its row).
    Decision 4 went further than either option: F1 was the only keyboard
    route to the form-view buttons of all three dialogs, not just a
    progress-view duplicate of Tab, so the author chose to make the button row
    a Tab stop instead (`buttonRowKey`; `docs/decisions.md` § Connect dialog).
    Live on `SQL2017`, in a scratch config: Connect reached its row by Tab and
    Backtab, stepped over a gated Delete, swallowed typed letters, and
    connected from it; Back Up's Validate and Start Backup fired from the row,
    and Cancel Backup on a 650 MB database read "Backup cancelled." and
    "Backup w11_bk cancelled"; Restore from history went form row → Files →
    Files row → Restore, through the target check, to a completed restore;
    New Snapshot's name edit after Default File Paths cleared the grid with
    the new hint; a Query Store report ran on `BeginTimeout` (the plan pane
    and series reads, same change, were not driven). Not reproducible by hand, so pinned only: a history load
    during the target check (T19), Hide abandoning loads, the Dependencies
    fetch stopped on close (T69), the refused-job repair (T28), OK reloading
    nothing (T61). See T19, T28, T61, T69 and T70.
- **W12 — Remaining small gossms bugs.**
  - Items: T10 (REAL), T29 (rename gate), T32 (gutter), T33 (Right expands),
    T64 (scrollbar thumb), T66 (composed keys), T67 (control characters in
    cells), T68 (bad key file), T71 (clipboard order), T72 (empty rename),
    T73 (filter pushdown), T62 (`nameMap` for the cross-db directory).
  - Each is independent, so this step can be split freely. Each carries its
    own pin.
  - **Done.** `go test ./...` green; gofmt and vet clean (gossms only — gosmo
    is untouched). Every new pin was checked failing against the old code. Two
    items needed no code change: T29 (the old name is the right one) and T72
    (already refused). T64 went one step further than its row: the drag
    mapping could not reach the last offset of a long list either. Live on
    `SQL2017` (tmux, scratch config): `CAST(0.1 AS real)` showed `0.1` and
    `CAST(3.4e38 AS real)` `3.4e+38` (T10); a CR/LF/TAB cell read
    `line1 line2 end`, its column sized to it (T67); Right and `+` on the
    expanded server node left it expanded, `-` then Right collapsed and
    reopened it (T33); a 30-row result's thumb sat on the track's last five
    rows at Ctrl+End and its first five at Ctrl+Home, and a click on the last
    track row scrolled to row 30 (T64); a 10,001-line file drew `10000` in a
    7-column gutter clear of the border (T32); an `e` + U+0301 typed into the
    editor reached the server whole (`LEN` 4). Not reproducible by hand, so
    pinned only: T62 (no case-sensitive instance here), T68, T71 and T73.
    Found on the way, not fixed: B15 (a large paste redraws per key) and B16
    (the editor draws no combining marks), both in `docs/open-threads.md`.
- **W13 — T20, the Recycle error log gate.**
  - Probe first, with a CONTROL SERVER login that is not sysadmin, on 13 and
    17.
  - If the probe shows CONTROL SERVER suffices, close it in
    `docs/decisions.md` with no code change.

### Stage 4 — gosmo API (breaking); each step lands gosmo and gossms together

- **W14 — T36, identifier helpers.**
  - gosmo: add `IsReservedKeyword`, `QuoteNameIfNeeded`, `UnquoteName` and
    `QuoteAnsiLiteral`.
  - gossms:
    - T17: completion quoting.
    - T34: showplan unquoting.
    - The XE predicate refs and `securables_matrix.go` labels.
    - Delete sqlparse's partial keyword list, or reduce it to the
      completion-only set.
  - **Done.** `go test ./...` and `go test -race -count=1 ./...` green in
    gossms, `go test ./...` in gosmo; gofmt and vet clean (gosmo also
    `-tags livedb`). gosmo `quoting.go` gained the four helpers; the reserved
    list is Microsoft's, probed on 13 and 17: the server refuses 179 of its 184
    words bare (DISK, DUMP, LOAD, PRECISION and SECURITYAUDIT are tolerated,
    kept as documented), no ODBC or future keyword is refused, and USER,
    CURRENT_USER, SESSION_USER, SYSTEM_USER, CURRENT_TIMESTAMP, CURRENT_DATE
    and NULL are accepted bare *as themselves* — the silent T17 class. Pins:
    `quoting_test.go`; live `TestLiveReservedKeywords` on 17, 14 and 13 (fails
    with USER dropped from the list). gossms: completion calls
    `gosmo.QuoteNameIfNeeded` (`bracketIfNeeded` gone); `sqlparse.IsKeyword`
    is deleted, but `sqlKeywordList` stays — it is the tokenizer's clause-word
    set (CAST, APPLY), not a quoting list, and its comment now says so. The XE
    refs use `QuoteName` and the ansi `LIKE` literal `QuoteAnsiLiteral` (same
    output for every real name); securable labels quote each part. **One
    deviation:** T34 does not import gosmo — `internal/showplan` is driver-free
    by design (`missing_index.go`'s `bracket` comment), so it has its own
    `unbracket`, the inverse of `bracket`; the StmtUseDb statement text keeps
    the server's bracketed `USE [db]`. Live on 17: plan XML for
    `[w14_odd]]db].dbo.[Odd]]Name]` writes the names doubled, and the parsed
    Missing Index script reads `USE [w14_odd]]db]` / `ON [dbo].[Odd]]Name]
    ([[Doc]]])`. tmux on `SQL2017`: `SELECT a.` completed `a.[User]`,
    `a.[Odd]]Col]`, `a.Cast`; an unqualified `Us` completed `[User]`, and both
    queries returned the row's `alice`, not `dbo`. Pins:
    `TestSQLCompletionBracketsReservedColumnNames`,
    `TestMissingIndexScriptKeepsABracketInAName`, `TestUnbracket`,
    `TestSecurableLabelQuotesEachPart`. See T17, T34 and T36.
- **W15 — T35 and T14: `ConnectionOptions.Port`.**
  - gosmo adds the field; `ResolveServer`'s comma and colon folding is removed.
  - gossms `toGosmoOptions` sets `Port`, and `retargetAt` keeps the saved
    port.
  - Live check: AG peer retarget on `SQL2017` (dynamic port, no Browser).
  - **Done.** `go test ./...` green in both repos; gofmt and vet clean (gosmo
    also `-tags livedb`). gosmo: `ConnectionOptions.Port`; a port written in
    `Server` wins over it (unchanged gossms behaviour, so no saved connection
    changes target); out of 0-65535 is an error. Every reader of the dial
    target — the DSN host, the Browser-dialer choice, the pool's Browser-cache
    eviction, the Entra sign-in cache key — goes through
    `ConnectionOptions.address`. gossms: `toGosmoOptions` passes `Server` and
    `Port` apart, through the new `config.DialPort` (1433 is unspecified, the
    old SQL Browser rule). `ResolveServer` is not deleted: it stays the fold
    for keys (`ConnectionAddress`) and the Connect dialog's display, now off
    the dial path. `retargetAt` moves a port written in the saved `Server` to
    `Port` before replacing `Server`. Live on 17, via `Peer` with a resolver
    answering `win10cli\SQL2017,55253`: the peer dials port 55253. SQL2017's
    Browser answers now, so the control was a resolver entry for a
    nonexistent `win10cli\NOSUCHW15,55253`: it reached SQL2017 directly with
    the fix and failed "no instance matching 'NOSUCHW15'" (a Browser lookup)
    without it. tmux: Connect to `win10cli\SQL2017,55253` previews
    `win10cli:55253/SQL2017`, connects to 14.0.2130.4 and saves Server and
    Port apart. Pins: `TestPortReachesTheDriver` (gosmo),
    `TestPeerOptionsKeepAPortWrittenInTheSavedServer`.
- **W16 — The connection lifecycle.**
  - Items: T37 (`Server` lifetime context; `ServerConn.Context()` delegates,
    then is removed), T50 (`ServerInfo.Login`), T57 (no capability probe for
    non-Explorer roles).
  - All three touch `internal/db/connection.go`, so one step.
  - The ARCHITECTURE § How a query runs text about `ServerConn.Context()` is
    rewritten in this step.
  - **Done.** gofmt and vet clean in both repos (gosmo also `-tags livedb`);
    `go test -race -count=1 ./...` green in both. gosmo: `Server` carries its
    lifetime (`newServer`, the one constructor); `Close` cancels it before
    closing the pool; `Server.Context()` is exported, never nil (Background
    for a nil or literal Server). Every statement gosmo runs is bounded by it
    through `Server.bound` — `Server.query`/`queryRow`/`execScan`/`exec`/
    `execSecret`, `Database.withConn`/`query`/`queryRow`, `BulkInsert`,
    `EffectiveServerPermissions` and the progress path of Backup/Restore
    (`execWithProgress`, now a method). A rows-returning read releases the
    link when its rows close: `Server.query` now returns the `dbRows` wrapper
    (conn nil there), and `withConn`'s callback takes the bounded ctx. Only a
    statement a caller runs on `DB()` is not bounded, and its doc says to
    derive from `Context()`. `ServerInfo.Login` is read in `loadInfo`'s first
    statement; `CurrentLogin` stays. gossms: `ServerConn.ctx`/`cancel`/`Login`
    and `Context()` are gone — every site (54) calls `sc.Server.Context()`;
    `Close` closes the `Server` before `closePeers`, the old cancel order.
    `loadChildren` no longer dereferences a missing connection (the old
    fallback hid it) and shows "not connected" directly. The capability probe
    runs only for `RoleExplorer` (peers inherit it). Live:
    `TestLiveCloseEndsAStatementInFlight` (gosmo) holds a 40 s `WAITFOR`
    through `sp_executesql`, then `Close`s: the request leaves
    `sys.dm_exec_requests` within 6 ms on 17, 14 and 13; with `bound`
    disabled the statement outlives `Close`. The full gosmo `livedb` suite
    passes on 17 (704 s). tmux on `SQL2017` (port 55253):
    connect, expand Databases, a query panel runs `SELECT ... APP_NAME()` as
    `goSSMS - Query`; File > Disconnect drops all four `goSSMS` sessions
    server-side (the panel's own stays, as it owns it); reconnecting typed as
    `SA` labels the root `(sa, ...)`, the server's answer. Pins:
    `TestCloseCancelsAReadInFlight`, `TestContextOfABareServerIsNeverNil`,
    `TestNewServerWrapsACallerSuppliedPool` (Login) in gosmo;
    `TestOnlyExplorerConnectionsProbeServerCapabilities`, `TestServerConnLabel`,
    `TestLoadChildrenWithoutAConnectionShowsNotConnected`. Found on the way:
    B17 (tui's `livedb` tests do not compile since 0a14b06).
- **W17 — Error predicates and catalog derivations.**
  - T39 replaces `refusalNumbers`, `isAlreadyExists` and the mail error
    switch.
  - T40 moves the system, mapped and expired derivations into gosmo, with
    their tests.
  - **Done.** See T39 and T40. `go test -race -count=1 ./...` green in
    gossms, `go test ./...` and `go vet -tags livedb ./...` in gosmo. Live:
    `TestLiveErrorPredicates` (gosmo) on win10cli 17 — duplicate CREATE LOGIN
    and CREATE USER, DROP of an absent login, a refused DMV read under
    EXECUTE AS, and `SendMail` with an unknown profile. The tmux run reached
    Send Test E-Mail, but its gates (no profile; Database Mail stopped) stop
    every mapped error short of the server, so the dialog's `errors.Is`
    switch is covered by the gosmo live test only.
- **W18 — Handle-centric writes: T42 and T45.**
  - gosmo: role and server-role `AddMember`/`RemoveMember`, file and
    filegroup handles, the category handle, `Drop` everywhere,
    `RemoveNotification`, `AddStep` returning `*JobStep`, `Credential.Alter`
    options, `ScriptCollector.Len()`.
  - The parent-side forms are removed in the same step, not deprecated: one
    user, one tree.
  - gossms: about 15 call sites, listed under T42.
  - Then T46 (`Server.BuildBackupStatement`).
  - **Done.** See T42, T45 and T46. `go test -race -count=1 ./...` green in
    gossms; `go test ./...`, `go vet ./...` and `go vet -tags livedb ./...`
    in gosmo. Live: `TestLiveHandleWrites` (gosmo, new) on win10cli 17 runs
    every moved write through its handle and reads the catalog back —
    role and server-role membership, file Alter (rename and size) and Drop,
    filegroup SetReadOnly, SetDefault and Drop, the category drop, AddStep
    and InsertStep reading their step back through a `JobRef`, JobStep.Drop
    and RemoveNotification; `TestLiveJobReorder*` now checks AddStep's
    returned step, and the credential, create-returns and filegroup
    exclusive-access live tests pass on the new signatures. tmux on win10cli
    17 against a throwaway database: Role Properties > Members scripts and
    applies `ALTER ROLE … ADD MEMBER`; Database Properties > Filegroups
    scripts and applies READ_ONLY and DEFAULT on a filegroup (first refused
    with Msg 5070 while two query panels sat in the database, as
    TerminationNone should); Back Up Database > Script opens the BACKUP.
    Found on the way: B18 (an empty filegroup is not listed).
- **W19 — T13, the secret policy.** Blocked on decision 1.
  - gosmo: a `Database.execSecret`, every password and secret routed
    through it, and an opt-in if one is chosen.
  - gossms: the Script button of New Login, Credential and Key behaves as
    decided.
  - **Done.** See T13. Decision 1 taken: redact by default, with a
    `WithScriptSecrets` opt-in for captures only. `go test ./...`,
    `go vet ./...` and `go vet -tags livedb ./...` in gosmo;
    `go test -race -count=1 ./...` green in gossms. Live on win10cli 17: the
    16 gosmo livedb tests over credentials, logins, users, Database Mail, key
    ops, and asymmetric, symmetric and master keys pass on the redacting
    chokepoints. tmux against a throwaway login: New Login > Script Changes
    opens `CREATE LOGIN … WITH PASSWORD = N'<insert password here>'`, and OK
    creates the login with the typed password (`PWDCOMPARE` 1 for it, 0 for
    the placeholder). Login Properties > Script Changes after a password
    change gives the same, and OK sets the new password. Found on the way: New
    Login, Login Properties and the endpoint wizard's CREATE LOGIN never went
    through gossms's `scriptSafePassword`, so their Script Changes carried
    the password in clear until this step.
- **W20 — T49 and T48.**
  - **T49, the `ErrHandleNotLoaded` guard.** Before landing it in gosmo, grep
    every `TableRef`/`IndexRef`/`StatisticRef` read in gossms, because one
    that relied on an empty result now errors.
  - **T48, restore rules in gosmo.** `BackupHeader.SetNumber` and
    `RestoreSpec.FromHeader` replace `backupSetNumber`, `restorableHistory`
    and `relocateFiles` in `restore_dialog_ops.go`.
  - Live check: a restore with MOVE and WITH FILE on 17.
  - **Done.** See T48 and T49. `go test ./...`, `go vet ./...` and
    `go vet -tags livedb ./...` in gosmo; `go test -race -count=1 ./...`
    green in gossms. Live on win10cli 17: `TestLiveRestorePlanWithFileAndMove`
    (gosmo, new) backs a throwaway database up twice to one file, adding a
    data file between, and restores set 2 as a copy through `BackupSetAt` and
    `FromHeader` — `WITH FILE = 2` and three MOVEs, the extra file at its
    planned path; it also runs the T49 refusal, a `CreateIndex` on a TableRef
    and `Statistic.Columns` from handles. `TestLiveAPIPass` passes on its fixed
    subtest. tmux on the same kind of two-set file: Restore Database >
    Analyze, → to set 2 (Files Included lists `extra`), target `W20Copy`,
    Script opens `RESTORE … WITH FILE = 2` with three MOVEs, and F5 restores
    it with the three files at `W20Copy_*`. Found on the way:
    `TestLiveAPIPass`'s datetime2/decimal subtest read `Columns` off a
    TableRef, so it checked nothing — the trap T49 closes; and
    `Statistic.Columns` read by ObjectID and StatID although its doc promised
    it worked from a handle.
- **W21 — T47, `Server.InTransaction`.** Blocked on decision 2.
  - gosmo first, with a livedb test that rolls back.
  - Then make Resource Governor Apply atomic, and drop the comment at
    `resource_governor_props.go:37-42`.
  - **Done.** See T47. Decision 2 taken as drafted. `go test ./...`,
    `go vet ./...` and `go vet -tags livedb ./...` in gosmo;
    `go test -race -count=1 ./...` green in gossms. Live on win10cli 17:
    `TestLiveInTransaction` (gosmo, new) creates a pool, a group in it and a
    schema in a throwaway database in one transaction — the group's
    read-back and a server-scoped read after the database-scoped write run on
    it — and rolls back (nothing left, observer silent), then commits (all
    three, observer told in order); RECONFIGURE inside fails Msg 574 without
    dooming the transaction, and BulkInsert refuses. tmux: Resource Governor
    Properties with pool `w21p` and group `w21g2` added, `w21g2` created
    behind the dialog's back: OK fails "already exists", `w21p` is not on
    the server and the pages keep their edits; after dropping the outside
    `w21g2`, OK commits both and RECONFIGURE leaves nothing pending. Script
    Changes carries no BEGIN TRANSACTION. Found on the way: a rolled-back
    `ALTER RESOURCE GOVERNOR WITH (CLASSIFIER_FUNCTION …)` still sets
    `is_reconfiguration_pending` (pool and group DDL does not); the next
    RECONFIGURE clears it unchanged.
- **W22 — Lean listings: T52 and T53.** T52 is blocked on decision 3.
  - gosmo drops `definition` from listings and adds `Definition(ctx)`. In the
    same step, the gossms rule and default sites switch to it.
  - The T53 round-trip cuts follow: `Catalog`, `SecurityPolicies`,
    `UserMappings`.
  - **Done.** See T52 and T53; decision 3 taken as drafted, the field
    removed outright (a field and a method cannot share the name).
    `go test ./...`, `go vet ./...` and `go vet -tags livedb ./...` in gosmo;
    `go test -race -count=1 ./...` green in gossms. New pins:
    `TestModuleListingsDoNotReadTheDefinition` and
    `TestModuleDefinitionReadsByName` (gosmo, unit), and
    `TestLiveLeanListings` (gosmo, livedb), green on win10cli 17 and the
    SQL2016 floor: every `Definition` by name including `sys.sp_who`, an
    encrypted view `""`, a table read as a view and a view read as a
    procedure `ErrNotFound`; `Catalog`/`SystemCatalog` as one four-result
    batch; three policies' predicates grouped from one query; and
    `UserMappings` as one batch that, with a database held SINGLE_USER by
    another session (ONLINE, Msg 924 on entry), skips it and reads the rest.
    `TestLiveAPIPass`, `TestLiveTreeFamiliesReadTheirCatalog`,
    `TestLiveVersionSweep`, `TestLiveScriptedFamiliesRecreateTheirObjects`
    and `TestLiveByNameFinders*` also pass on both. tmux on 17: the Rules and
    Defaults folders and a rule leaf show the text, Rule Properties' editor
    holds it, Script Rule as CREATE writes it, completion lists a view and
    its columns, a security policy's leaf shows its predicate, and Login
    Properties › User Mapping ticks the one mapped database with its user and
    schema. Found on the way: the procedure and DML-trigger listings
    inner-joined `sys.sql_modules`, so a CLR procedure or CLR trigger never
    appeared in them; without the join they do (the scripter already refuses
    one with `ErrUnsupported`).

### Stage 5 — the lexer, rendering and the data model

- **W23 — T54, one T-SQL lexer in `sqltext`.**
  - The order inside the step:
    1. Build the lexer and the statement splitter, ported with the existing
       `SplitBatches` and sqlparse tests.
    2. Switch `controls.sqlStatementAt`. This retires W3's interim leader
       list.
    3. Switch `sqlparse.lexSQL`.
    4. Switch the highlighter. This fixes T44 and T55.
    5. Move the word-rune rules out of `tuikit/core`.
  - Also: T41 (the test-only oracle moves to `_test.go`) and the T56 byte
    scan in `SplitBatches`.
  - Split `sqlparse/scope.go` here, since the step rewrites it anyway.
  - **Done.** Decided on the way: IntelliSense shares the lexer, not the
    splitter. `sqlparse` keeps its DML splitter, so completion's statement
    bounds, its golden sweep and the two caches' resume rules are unchanged;
    both splitters now read one set of verb tables in `sqltext`. See T54,
    T44, T55, T56 and T41. `go test -race -count=1 ./...` green. Benchmarks:
    the cold completion prefix scan went from 6.0 to 4.2 ms at 1,000
    statements, the highlighted 10k-line draw from 389 to 246 allocations and
    about 10% faster; the cached typing paths are unchanged. tmux, A/B against
    a pre-change binary: `SELECT [a/*b], [from] FROM t` above four more lines
    — the old highlighter colours everything after `[a/*` as a comment to the
    end of the document; the new one leaves both identifiers plain, colours
    `FROM`, and carries a `'x`⏎`GO`⏎`y'` literal across its three lines.
    Ctrl+Enter selects `SELECT 1 FROM t` alone above a DROP, a hinted DELETE
    with its WHERE line, and `SELECT [select`⏎`GO]` as one statement. Live on
    win10cli 17, F5 on a script with GO inside a literal, GO inside a block
    comment and a `GO 2` returned exactly four result sets.
- **W24 — The grid and the draw hot path.**
  - T16 and T38 together: the `errorMode` flag, plus `SetBounds` returning
    early on an unchanged rect. First audit the hosts that rely on the
    per-frame recompute, and give them `RefreshColumnWidths`.
  - Then T65 (`ClipScreen`/`Canvas` `FillArea`).
  - Then T51 (`Put`/`FillArea` in `core/drawing.go`), with a frame
    benchmark committed beside it.
  - Plus the T56 XEvent filter.
  - **Done.** Decided on the way: `FillRect` goes through `Put`, not
    `FillArea`. Measured on a real tcell v3.5.0 screen at 200×50,
    `FillArea` converts its rune to a string per cell — 10,000 allocations a
    fill, where one prebuilt string through `Put` costs none. T65 shipped
    anyway, as the latent bug it is. The per-cell `SetContent` calls outside
    `drawing.go` (editor, grid, input field, menus, charts) moved to the new
    `core.PutRune` too, and the rule is in `docs/ui-rules.md`. The host
    audit found every in-place mutation already behind
    `RefreshColumnWidths`; only `SetMaxCellWidth` and `SetRowNumbers` relied
    on the relayout, and both now defer to `Draw`. `go test -race -count=1
    ./...`, `gofmt` and `go vet` clean. New `BenchmarkFrameDataGrid` and
    `BenchmarkFrameEditor` (`controls/frame_bench_test.go`, real tcell
    screen on a mock terminal): grid 25,028 → 393 allocations and about
    4.8 → 2.6 ms a frame; editor 20,606 → 155 and about 4.4 → 2.8 ms. tmux
    on win10cli 17, 200 columns: a database held SINGLE_USER by another
    session, its Tables folder's Detail Browser error (Msg 924) shows all
    136 columns, follows a resize to 160 (ellipsis at the edge) and back out
    to 240. A results grid with its row gutter clamps at the configured 24
    characters. system_health live data, 38,943 events: free text
    `SCHEDULER_Monitor` matches 13,808 case-insensitively, `name contains
    'DIAGNOSTICS'` and `name > 'sp_' and name < 'sq'` both 11,064, and
    `id >= 0` vs `id > 0` with a name prefix 13,809 vs 0. Borders, the
    toolbar's wide emoji and dialogs render unchanged.
- **W25 — T58, config and tracked-query saves off the UI goroutine.** This
  comes after W10, which changes the tracked file.
  - **Done.** `Config.Save` is now `BeginSave` (snapshot, UI goroutine),
    `SaveJob.Run` (lock, key, merge, fsync — touches nothing in the Config)
    and `EndSave` (adopt, replaying any AddOrUpdate/RemoveConnection made
    meanwhile, which stay pending). `App.saveConfig` (`app_saves.go`) runs one
    job at a time and queues later asks behind it, each caller's callback
    reporting on the UI goroutine; all six call sites use it.
    `TrackedQueries.Toggle` is memory-only, and `Save` no longer holds the
    set's mutex across the file work — before, a save waiting on another
    instance's lock blocked `IsTracked`, which the action row draws every
    frame, so moving the save alone would not have helped. Track Query stales
    the tree leaf at once and reports only a failed save. `FlushSaves`, called
    by `cmd/gossms` once `Run` returns, writes a save still out at quit.
    Rule in `docs/ui-rules.md`. Pins: `TestEndSaveKeepsChangesMadeWhileTheJobRan`,
    `TestEndSaveOfAFailedJobKeepsTheChangesPending`,
    `TestTrackedReadersDoNotWaitOnASave`, `TestConfigSaveDoesNotBlockTheCaller`,
    `TestConfigSavesAskedForMeanwhileRunAfterIt`,
    `TestFlushSavesWritesWhatTheLoopLeftOwing`, `TestTrackedSaveDoesNotBlockTheUI`.
    `go test -race ./...`, `gofmt` and `go vet` clean. tmux, scratch config:
    with `config.json.lock` held as another instance would, Options' OK closed
    the dialog and F10 opened the menu 0.1 s later; the two-second lock failure
    then reached the log. An indent change made just before Ctrl+Q was on disk
    after exit. A connect to win10cli 17 saved its History entry.
- **W26 — T59, the null-aware result model and `TreeView.SetTitle`.**
  - `query.ResultSet` gains the null bitmap first, then `RowSource.IsNull`.
  - Then tuikit stops matching the `"NULL"` string.
  - Copy and CSV export use the bitmap in the same step.
  - **Done.** `query.ResultSet` carries a null bitmap (`MarkNull`/`IsNull`,
    grown only as far as the last NULL) and is itself the grid's source
    (`Len`/`Row`), so the query results and Activity Monitor grids get it
    without an adapter. `RowSink.Row` takes the row's `isNull` beside its
    cells. tuikit's new optional `controls.NullSource` replaces the
    `"NULL"` text match in `drawRow` and `drawCellSelection`, and a NULL never
    reaches `OnShowValue` (an xml column's NULL opens the plain popup), so
    `classifyCellKind` lost its text guard. Decided on the way (the user's
    call, recorded in `docs/decisions.md`): copy keeps "NULL", as SSMS; the
    Results To File CSV writes NULL as an empty field, `''` as `""` and the
    string 'NULL' as `NULL`, through its own field writer — encoding/csv
    never quotes an empty field — that otherwise quotes byte-for-byte as
    encoding/csv did. `TreeView.SetTitle`; Object Explorer sets its own.
    Pins: `TestScanAndStreamMarkOnlyRealNulls`, `TestResultSetNullBitmap`,
    `TestGridDimsOnlyCellsTheSourceMarksNull`,
    `TestShowValueOfANullSkipsTheHost`, `TestResultsGridKnowsWhichCellsAreNull`,
    `TestCSVSinkKeepsNullEmptyAndTheStringNULLApart`,
    `TestTreeViewDrawsItsTitle`. `go test -race ./...`, `gofmt` and `go vet`
    clean. tmux, live on win10cli 17: `SELECT NULL, 'NULL', '', CAST(NULL AS
    xml), CAST('<a/>' AS xml) UNION ALL SELECT 1, NULL, 'z', NULL, NULL` — the
    four NULLs draw dim, the string 'NULL' does not; Show Value on the xml NULL
    opens the popup, on `<a/>` a `y.xml` tab; Results To File wrote
    `n,s,e,x,y` / `,NULL,"",,<a/>` / `1,,z,,`.

### Stage 6 — simplification and file splits (no behaviour change)

- **W27 — Deduplication.**
  - gosmo: `readByName[T]` and the remaining hand-rolled `ErrNoRows`
    checks.
  - gossms: `dbFolderTarget`, `startFeed[T]`, table-driven menus, T63, the
    tuikit README dependency graph and `SetBounds` exceptions (T43), and the
    shared plan-capture helper.
  - Then the Activity Monitor SQL move or live sweep, once decision 5 is made.
  - The `App` dialog registry stays optional.
  - **Done, except the optional registry.** Decision 5 was made and moved
    the Activity Monitor SQL into gosmo (below, after the deduplication).
    - gosmo: `readByName` (`helpers.go`) is a whole by-name read — `q` on the
      `*Server` or `*Database`, the family's `scanX(owner, scan)`, then
      `foundRow`'s tail; 42 reads use it. `rowErr` is `foundRow`'s error half,
      for a read that fills in more after the row; 16 reads end in it. Every
      hand-rolled `ErrNoRows` check that meant not-found is now one of the two. Four that hand-roll it stay: there, no row
      is an answer, not an error (`AgentInfo`, `mirroringCertificateName`,
      `Table.DataSpace`, `BrokerQueue.MessageCount`). `TableByName`, the three
      `isEnabled` reads and `scriptModule` used to return a driver error bare
      and now wrap it "gosmo: …". gosmo CLAUDE.md § Row iteration says which
      helper applies. Net −128 lines.
    - Plan capture: gosmo's exported `StartPlanCapture(ctx, conn, PlanMode)`
      switches SHOWPLAN_XML / STATISTICS XML on and returns `stop`, which
      switches it off detached from ctx's cancellation and bounded at 5 s.
      `capturePlan` and gossms's `runScript` both use it; `planCapture` is
      now `gosmo.PlanMode`. The Messages pane's capture errors gain the
      "gosmo: " prefix. Pins: `TestStartPlanCaptureSwitchesTheOptionOnAndOff`,
      `TestStartPlanCaptureReportsBothFailures`.
    - `dbFolderDialog[P]` (`new_object_dialog.go`) is the plan's
      `dbFolderTarget`, made generic so it carries `show` and the folder
      refresh as well as `dbName`/`node`: eight New dialogs embed it (asymmetric
      key, certificate, symmetric key, database scoped credential, database
      audit specification, column master key, column encryption key, user).
      Statistics and Index stay as they are: they are table-scoped.
    - `startFeed[S]` (`activity_monitor.go`) starts any of the three feeds;
      each `amFeed` holds its `amRunner` (Collector, TempDBCollector or
      Poller), so one `runFeed`, `feedStopped` and `feedError` replace three
      of each, and `applyRate`/`applyPaused` are gone.
    - Menus and loaders: `folderMenu` (31 folder menus: New Query, items,
      Refresh), `agentToggleLeafMenu` (schedule, alert, operator), and
      `listDatabaseChildren` (30 loaders that read the database, then list).
    - T63 and T43 are done (see their items).
    - `go test -race ./...`, `gofmt` and `go vet` (gosmo also `-tags livedb`)
      clean; gosmo's live suite passes on win10cli 17 (812 s). tmux on win10cli 17: Estimated plan drew its graph, and the plain
      F5 after it returned rows; Actual plan added its tab, and F5 with it off
      again did not. Activity Monitor's activity and TempDB feeds collected,
      paused and resumed, and the panel closed cleanly. In a throwaway
      database: the User-Defined Data Types and Message Types folders listed
      their objects, Message Type and Certificate Properties read theirs, and
      New Certificate showed "Database: W27Scratch", saw the master key,
      created `W27Cert` and reloaded Certificates. Schedules' folder menu
      offered New Schedule; a throwaway schedule's menu went Disable → Enable
      → Disable with the matching status. All dropped afterwards.
    - **Activity Monitor SQL → gosmo** (decision 5). New `*Server` reads in
      `server_activity.go` — `HasViewServerState`, `PerformanceCounters`
      (filtered by counter and instance as parameters, instance prefix
      stripped, `CounterType` exported), `WaitStats` (every wait),
      `FileIOStats`, `MemoryClerks`, `RequestActivity`, `HostCPU` (zero with
      no record yet) — and `tempdb_usage.go` — `TempDBSpace`, `TempDBFiles`,
      `TempDBObjects`, `TempDBSessions`. `Schedulers` gained each scheduler's
      load, so one read now gives the Activity Monitor both its pressure sums
      and its load factors (two queries before). The tempdb core count is
      `Info().LogicalCPUCount`, a query fewer each tick.
      `internal/activity` reads through `Source`, which `*gosmo.Server`
      satisfies; it keeps the counter list, the benign-wait filter (now in
      Go: `isBenignWait`), the wait categories, the clerk groups, the rates
      and the stores, and its tempdb types are aliases of gosmo's. The
      collectors take a `Source`, not a `*sql.DB`. The Block and Sessions
      procedures stay in gossms (sp_WhoIsActive is GPL-3.0). Recorded in
      `docs/decisions.md`.
    - Tests: gosmo's `server_activity_test.go` pins every read's column
      mapping, the counter filter's SQL and the no-record HostCPU; the reads
      are in `sweepMustCall`, with `PerformanceCounters` driven by hand
      (filtered and not). gossms's collect and tempdb tests run against
      `fakeSource`. The sweep's `QueryStoreQueryText` call now tolerates
      not-found for its fallback query id: it failed on 17 when the scratch
      store was still empty, which the earlier full run had not hit.
    - Live: gosmo's `TestLiveVersionSweep` passes on SQL2016 (13), SQL2017
      (14) and win10cli (17); gossms's `internal/activity` live tests
      (`-tags livedb`, including the throwaway VIEW SERVER STATE-only login
      and the procedure install) pass on all three. Managed Instance
      pending.
- **W28 — Remaining file splits.**
  - Split along the section comments, by exact line range. Diff byte-for-byte
    against the original, then delete the source.
  - One file per commit-sized change, each with its Package map row.
  - The gossms and gosmo lists are under § Simplification. Skip any file an
    earlier step already split.
  - **Done.** Every range was moved by script and checked byte-for-byte
    against the original before the source lost it; the only edits after
    that are import blocks, one banner, and comments and tests that named
    the old file.
    - Skipped: `activity_monitor.go` (857) and `explorer_databases.go` (826)
      are under the threshold since W27.
    - gossms:
      - `detail_browser.go` 1008 → 520: `detail_browser_runs.go` (495) takes
        `detailRuns` and the fetch path (`fetch`, the `post*` and cache
        writers, `fetchNodeDetails`, `fetchChildObjectsDetail`).
        `readTimeoutSites` now counts one site in each file.
      - `prop_dialog.go` 929 → 455: `prop_apply.go` (483) takes the write
        path (`commitRename`, `applyRun`, `runApplySteps`, `applyPlan`,
        `plannedApply`, and `validateDirty` through `runScript`).
      - `propsheet/rows.go` 1024 → 253 (Section, Note, Hint, Static):
        `textrow.go` (266), `choicerows.go` (411: Check, Select, Radio),
        `buttonsrow.go` (124). The tuikit README file list is updated.
    - gosmo:
      - `backup.go` 1160 → 399 (Backup, Log backup chain): `restore.go`
        (367: Restore and BackupHistory, its section) and
        `backup_headers.go` (412: Backup device inspection).
      - `query_store_reports.go` 1043 → 645 (result types, the reports,
        wait statistics, plan forcing): `query_store_report_spec.go` (411:
        the metric and statistic tables, options, `qsReportSpec` and the
        shared SQL fragments).
      - `index.go` 1049 → 450: `index_management.go` (608), its § Index
        management.
      - `connection.go` 1034 → 464 (options, `Connect`, address parsing):
        `connection_dsn.go` (581: DSN and connector building, the extra
        parameters, `ConnectionString`).
      - `table.go` 1017 → 600: `table_columns.go` (425: § Columns and
        § Column type builder).
      - `scripter_table.go` 934 → 830: `column_type.go` (110), its
        § Column type formatting.
    - Live: none needed, no behaviour change; the full test suites and a
      build of the binary are the check.

---

## Bugs — high

### T1 — Detail Browser runs Delete with the previous node's objects on the new node's connection — gossms — *confirmed*

**Where:** `internal/tui/detail_browser.go:253-255` (the loading branch of
`ShowNodeDetails`), `detail_browser_ops.go:59,94`.

**What happens.**
- When a node that isn't cached is selected, the pane only calls
  `grid.SetStatus("Loading...")`.
- The previous node's rows, `rowObjs`, charts and tooltip stay live until the
  fetch lands, which can take up to `childFetchTimeout`.
- `detailMenuItems` pairs the old `rowObjs` with
  `resolveConn(db.currentNode)`, which already resolves to the *new* node. So
  Delete drops the old node's objects on the new node's connection. Across two
  servers with matching schemas (prod and test), that drops same-named objects
  on the wrong server.
- `showQueryStoreValue` has the same flaw.
- B1 fixed only the "Not connected" branch, a few lines above.

**Fix.**
- Call `db.resetForNewNode()` before `SetStatus` in the loading branch.
- Defence in depth: derive `sc` from the selected objects' own
  `nodeData.conn`, and refuse when it differs from `currentNode`'s.
- Pin: a twin of `TestShowNodeDetailsNotConnectedDropsThePreviousNode` for
  the loading branch.

**Live check.** In tmux with two connections, select a folder on server A,
then a slow folder on B, and right-click during "Loading…". There must be no
Delete on A's rows.

**Done.**
- The loading branch calls `resetForNewNode`, and for a different node also
  empties the grid, so Show Value can't read A's query id against B either. A
  Refresh of the node on screen keeps its rows but drops their objects.
- Defence in depth is `rowObjsNode`, not the objects' own `conn`: row objects
  carry no connection (only the server node does), so the pane records which
  node its `rowObjs` were installed for, and `selectedRowObjects` returns nil
  when that is not `currentNode`.
- Pins: `TestShowNodeDetailsLoadingDropsThePreviousNode`,
  `TestRefreshKeepsRowsButDropsTheirObjects`,
  `TestDetailMenuRefusesObjectsOfAnotherNode`. All three fail on the old code.
- Live check: B's Tables folder was held in "Loading..." by an open
  `CREATE TABLE` transaction in a throwaway database. The grid was empty and a
  right-click on A's old row position opened no menu. Once the lock was gone,
  B's own row offered Delete as usual.

### T2 — Ctrl+Enter selects across non-DML statements, so the next F5 runs DDL — gossms — *confirmed*

**Where:** `internal/tuikit/controls/sql_statement.go:57-60, 186-207`.

**What happens.**
- `dmlStatementLeaders` holds only SELECT, INSERT, UPDATE, DELETE, MERGE and
  WITH.
- So with the cursor on line 1 of
  `SELECT … / DROP TABLE dbo.Staging / EXEC dbo.Purge`, all three lines are
  selected, and the F5 that follows runs the DROP and the EXEC.
- The comment claims the list mirrors sqlparse's map "exactly", but
  sqlparse's `forwardStatementEnders` (`scope.go:302`) already stops at
  DECLARE, CREATE, ALTER, DROP and TRUNCATE.

**Fix.**
- Interim: copy sqlparse's ender set, and add EXEC, PRINT, IF, BEGIN, COMMIT,
  ROLLBACK, USE, GRANT, DENY and REVOKE, with the usual exceptions (SET after
  UPDATE, EXEC after INSERT).
- Final: T54.

**Live check.** Ctrl+Enter on that three-line script selects line 1 only.

**Done.**
- `sql_statement.go` now feeds tokens to a `stmtSplitter`. `statementLeaders`
  is sqlparse's six DML leaders, its `forwardStatementEnders`, the listed
  additions, and SET, WHILE, RAISERROR, SAVE, BACKUP, RESTORE, DBCC, KILL,
  SHUTDOWN, RECONFIGURE, CHECKPOINT, WAITFOR, OPEN, CLOSE, FETCH, DEALLOCATE
  and BULK. Each one would do harm if it ran by accident.
- More leaders meant more ways to cut a statement short, so the step also
  added continuation rules:
  - INSERT ... EXEC continues the INSERT.
  - GRANT/DENY/REVOKE: the permission list up to TO/FROM holds no leaders.
  - Inside an ALTER, ALTER/DROP is the ALTER's own clause unless an object
    type follows it, so ALTER COLUMN and DROP CONSTRAINT don't split.
  - SET continues inside UPDATE, ALTER and MERGE, and after ON DELETE/UPDATE.
  - UPDATE(col), ON DELETE CASCADE / SET NULL, and MERGE RANGE/JOIN are not
    statements.
  - A leader after OR, AS, THEN, FOR, AFTER, OF, BULK, INNER/OUTER/LEFT/
    RIGHT/FULL, `,` or `.` continues the statement. This covers CREATE OR
    ALTER, cursor FOR SELECT, trigger event lists and MERGE actions.
  - DROP ... IF EXISTS continues the DROP, and FETCH after ROWS continues the
    SELECT (OFFSET ... FETCH).
  - A CTE's main statement can be UPDATE/INSERT/DELETE/MERGE, not only
    SELECT.
  - `@var` and `#tmp` are one word, so `@Delete` no longer splits (this was
    an old bug).
- Interim, as planned: module bodies still split from their header, and an
  IF splits from its body (the same as `IF x SELECT` before). Where a rule had
  to choose, it over-splits into a fragment that fails to parse rather than a
  shorter statement that still runs.
- Pin: `TestSelectStatementAtCursorLeaderBoundaries`, 34 shapes; 26 fail on
  the old code.
- Live check (tmux): Ctrl+Enter (LF) on line 1 of the three-line script
  selects line 1 only.

### T3 — Ctrl+Enter splits at a table hint, so a DELETE loses its WHERE — gossms — *confirmed*

**Where:** the same file as T2.

**What happens.**
- `DELETE FROM dbo.Orders WITH (ROWLOCK) WHERE OrderID = 42` selects only
  `DELETE FROM dbo.Orders`. Run, it deletes every row.
- `OPENJSON(@j) WITH (…)` is cut the same way.
- The tuikit copy lacks sqlparse's "WITH followed by `(` is a hint or column
  list" rule (`scope.go:242-295`), and its EXEC rule.

**Fix.** Port both rules with T2. Pin both shapes in
`sql_statement_test.go`.

**Done** with T2.
- A WITH followed by `(` is a hint or column list. A WITH followed by a name
  is a CTE only when AS or `(` comes next. So `RESTORE ... WITH MOVE`,
  `WITH ROLLBACK IMMEDIATE`, `WITH NOWAIT` and `GROUP BY ... WITH ROLLUP` no
  longer split either.
- Pinned: the table hint on the same line and on its own line, and
  OPENJSON's column list.
- Live check (tmux): with `DELETE FROM dbo.Orders WITH (ROWLOCK)` on one line
  and `WHERE OrderID = 42` on the next, Ctrl+Enter selects both lines.

### T4 — `CreateLoginRequest` cannot create a login with the policy off and a weak password — gosmo — *confirmed*

**Where:** gosmo `login.go:591-628` and `704-735`; gossms
`internal/tui/new_login_pages.go:169-201`.

**What happens.**
- `CREATE LOGIN … WITH PASSWORD` always runs with CHECK_POLICY = ON.
- gossms applies `SetPasswordPolicy` afterwards, so with "Enforce password
  policy" unticked the CREATE itself is refused with Msg 15118. That includes
  Linux, where complexity is enforced by default.
- `DEFAULT_LANGUAGE` is also issued as a follow-up ALTER.
- `SID`, `CREDENTIAL` and `HASHED` are missing, so a login can't be
  re-created with the same SID on an AG secondary.

**Fix.**
- gosmo: add `CheckPolicy` and `CheckExpiration *bool`, `DefaultLanguage`,
  `SID []byte` and `Credential` to the request, emitted in the one WITH list.
  This is additive.
- gossms: fill them in, and drop the two follow-up calls.
- Pin: script golden tests.

**Live check.** On 17/Linux and 13, New Login with the policy unticked and
password `a` succeeds.

**Done.**
- gosmo: `CreateLoginRequest` gains `CheckPolicy`/`CheckExpiration *bool`
  (nil = server default), `DefaultLanguage`, `SID`, `Credential` and
  `PasswordHash` (emitted as `PASSWORD = 0x… HASHED` — without it a SID-only
  re-create could not keep the password). All go in the one WITH list; an
  Entra login's language joins its default database on the one follow-up
  ALTER.
- Refused before anything runs, each verified live on 17: expiration on with
  the policy off (Msg 15122, which MUST_CHANGE's implied expiration hits
  too), MUST_CHANGE with HASHED (Msg 33010), and SID or CREDENTIAL on a
  Windows login (a syntax error). Policy, SID, credential and hash are
  refused on every non-SQL source, the language on a mapped login.
- gossms: the General page sets the policy pointers only for a dirty row and
  sends the language in the request; both follow-up calls are gone.
- Pins: golden tests in gosmo `login_write_test.go`;
  `TestNewLoginPolicyOffGoesInTheCreate` and the reworked Windows-defaults
  test in gossms. `live_createlogin_options_test.go` creates a policy-off
  login with password `a`, a SID, a language and a credential, then clones it
  from `LOGINPROPERTY(…, 'PasswordHash')` and checks `PWDCOMPARE`.
- Live check (tmux): New Login with the policy unticked and password `a`
  succeeds on ubusql1 (17, Linux) and `SQL2016`; `sys.sql_logins` shows
  policy and expiration off.

### T5 — `Table.FragmentationStats` on an unresolvable table scans the whole database — gosmo — *confirmed*

**Where:** gosmo `index.go:994-1012`.

**What happens.**
- When `OBJECT_ID(N'…')` is NULL (wrong schema, or the table was dropped),
  `dm_db_index_physical_stats` walks every object, possibly in DETAILED mode.
- It then returns other tables' indexes as this table's.
- `quoting.go:44-47` names this hazard, and `Index.Fragmentation` already
  avoids it with `CROSS APPLY` from `sys.indexes`.

**Fix.** Use the same `CROSS APPLY` shape, together with T11.

**Done.** Both fragmentation reads now share `fragmentationSelect`, which
reaches the DMV through `sys.indexes`; a table that resolves to nothing
returns no rows. Pin: `live_dmv_reads_test.go` ("FragmentationStats of a
missing table is empty"). The old query also failed outright on any table
with a LOB column: `fragment_count` is NULL on LOB rows and was scanned into
an `int64`.

---

## Bugs — medium

### T6 — `NewEndpointDialog` writes widgets and fields from the apply goroutine — gossms — *confirmed*

**Where:** `internal/tui/new_endpoint_dialog.go:256,379,466,471,515`.

**What happens.**
- `d.applyFns[0] = d.configure` is a method value, so it runs on the pipeline
  goroutine.
- `configure` calls `commitInputs` → `commitInstances` →
  `DataGrid.SetDataPreservingView`, while `animateUntil` redraws the dialog.
- It also writes `endpointName`, `algorithm`, `masterKeyPass`, `port` and
  `scriptedGroups`.
- The call is redundant: `preflight()` already commits on the UI goroutine.
- The same shape, writing model fields only, appears in
  `NewAGDialog.createGroup` → `request()` (`new_ag_dialog.go:236,239`) and
  `NewIndexDialog.createIndex` (`new_index_dialog.go:226`).

**Fix.**
- Commit only in preflight.
- Build the request on the UI goroutine and capture it in the step.
- Return `scriptedGroups` through the success callback.

**Done.**
- New Endpoint: preflight builds an `endpointRequest` (name, port, algorithm,
  password, a copy of the instance list) and the step captures it;
  `configure(ctx, req)` returns the scripted groups, which the step hands to
  a destination `runScript` puts in the run's context
  (`withEndpointScript`). The `endpointName`/`port`/`algorithm`/
  `masterKeyPass`/`commitInputs`/`scriptedGroups` fields are gone.
- The same shape for New AG (`createGroup(ctx, req)`; the secondaries come
  from `req.Replicas[1:]`, so the run no longer reads `d.replicas`), New
  Index, New Statistics (both read their widgets in `request()`) and Add
  Replica (a copy of `d.resolved`).
- Pins: `TestEndpointStepDeliversTheScriptThroughTheRun` (also: an instance
  removed after preflight does not reach the run in flight), and the
  existing endpoint/AG/index/replica tests moved to the new signatures.
- Live: on ubusql1 with win10cli as peer, the endpoint script came out
  grouped per instance; New AG's script carried a Readable secondary left in
  the replica editor.

### T7 — The meta-test for "an apply closure never writes page state" misses helpers and method values — gossms — *confirmed*

**Where:** `internal/tui/apply_closure_state_test.go`, `perm_state.go:124`.

**What it misses.**
- `commitCurrent()`, called inside 14 apply closures, for example
  `login_props.go:529`, `ag_props.go:360`, `database_props_files.go:540`,
  `extended_properties_form.go:206` and `new_login_pages.go:430`.
- `commitApplied` (7 uses), which writes `*orig`.
- `commitRename` (9 sites).
- The method-value applies in T6.

**Fix.**
- Add a page commit hook that `PropDialog` and `newObjectDialog` run on the UI
  goroutine before `StartApplying`.
- Retire `commitApplied`. The `applyProgress`/`applyFailed` reload now
  re-reads every page that reached the server. Live-check a partial failure
  first.
- Teach the test to follow local closures and `d.<method>` values.
- Keep `commitRename` as a named exemption.

**Done.**
- `propsheet.Form.SetCommit`/`Commit` and `PropertySheet.Commit`. PropDialog
  commits every loaded page at the top of `runApply` and `runScript`, before
  the confirmations and the dirty check; `newObjectDialog.runPipeline`
  commits every `d.forms` entry (opened or not) before `preflight`. The 14
  `commitCurrent()` calls, and New Database's `syncToggles()`, are form
  hooks now.
- `commitApplied` and `applyPermEdit` are gone; `applyPermChange`'s comment
  carries the reasoning. The two matrix tests that pinned the in-place
  baseline move are replaced by `TestPermissionsMatrixPartlyAppliedReloads`
  (a real `PropDialog` over the fake driver: the page reloads clean).
- The test follows captured closures, func fields
  (`d.commitInputs()`), methods of captured values resolved by receiver type,
  method values of the `func(ctx) error` shape, and package functions handed
  captured state (writes through those parameters). `commitRename` is in
  `applyHelperExemptions`. Blind spot, written into the test and
  `docs/ui-rules.md`: a local that aliases page state (a range variable,
  `r := d.rows`) — tried, and it flags value copies (`cloneXEEvents`, a
  decryptor option) that only types could tell apart; `commitApplied` was
  that shape.
- Test helpers that call a page's apply directly (`loadPage`, the New
  Database/Job `page` helpers) go through `hostApply`, which commits first
  as the dialog does.
- Pins: `TestPropDialogCommitsPagesBeforeApplyAndScript`,
  `TestNewObjectDialogCommitsBeforePreflight`,
  `TestSheetCommitRunsEveryLoadedFormsHook`, `TestFormCommitRunsTheHook`.
- Live, `SQL2017`, Server Properties ▸ Permissions: `a_w8` dropped from
  Grant With Grant to Grant, then a grant to `sysadmin` (Msg 4617). The
  REVOKE GRANT OPTION FOR landed, the message stood, and the page reloaded
  showing Grant. Putting `a_w8` back to Grant With Grant and pressing Apply
  re-granted it (`GRANT_WITH_GRANT_OPTION` in `sys.server_permissions`).
  (A grant to `sa` is no refusal: a silent no-op for a sysadmin.)

### T8 — `RefreshDatabasesFolder` and `RefreshLoginsFolder` bypass `Reload` and leak the old subtree — gossms — *confirmed*

**Where:** `internal/tui/object_explorer.go:143-191`.

**What happens.**
- Both functions set `c.children = nil` and `Loaded = false` by hand.
- Old nodes stay in `oe.byID`, `DetailBrowser.Forget` never runs, and an
  in-flight child load lands and registers more orphans.
- The leak lasts until disconnect, through 13 callers: New Database, New
  Login, Attach, Detach, Snapshot, Restore and Offline/Online.

**Fix.** Make both bodies `oe.Reload(c)`. Then do T27.

**Done** (W9): both functions are deleted rather than fixed — their callers
use `ReloadFolders(sc, folderOf("", NodeDatabases|NodeLogins))`, which goes
through `Reload`. Pin: `TestPostWriteRefreshReleasesTheOldSubtree`.

### T9 — IntelliSense caches and saved OE filters are shared across identities on one server — gossms — *confirmed*

**Where:** `internal/tui/completion_inventory.go:122-124,262-264`,
`explorer_filter.go:372`.

**What happens.**
- These caches are keyed by
  `config.ConnectionName(server, port, db, opts.User)`.
- `User` is empty for Windows auth, Entra Default/MSI and service principals.
- So two identities on one server share a catalog, filtered by metadata
  visibility, and disconnecting one purges the other's.

**Fix.** Add one `connectionIdentityKey(opts)`, built on
`Connection.GeneratedName` with the database blanked, and use it at every
such site.

**Done** (W10): `config.Connection.IdentityKey()` — `InstanceKey(ConnectionAddress(c))`,
then `GeneratedName`'s identity and auth tag (factored out as
`signInIdentity`), NUL-separated, because a login name or an address may hold a
comma. Its instance part is folded, unlike `GeneratedName`, so `HOST` and
`host,1433` share one cache. `sysCompletionInventoryKey` is it, and
`completionInventoryKey` appends the database, so the database and
linked-server directories and `filterKey` follow. Pins:
`TestIdentityKeySeparatesIdentitiesOnOneServer`,
`TestIdentityKeyFoldsTheAddress`, `TestTwoIdentitiesOnOneServerKeepTheirOwnCaches`
(Windows and Entra Default on one server: the purge and the filter restore
each fail on the old key). Live: pinned only. Windows and Entra auth are
unavailable from this host.

### T10 — The REAL column type displays float32 noise — gossms — *confirmed*

**Where:** `internal/query/executor.go:411-424, 684-687`.

**What happens.**
- go-mssqldb decodes REAL as `float64(float32)`, and `appendFloat(…, 64)`
  then prints `0.10000000149011612`.

**Fix.**
- Record `isReal` per column when `DatabaseTypeName()=="REAL"`, and format
  with bitSize 32.
- Pin: an executor test.
- Live: `SELECT CAST(0.1 AS real)` shows `0.1`.

**Done** (W12): `rowScanner.isReal`, set from `DatabaseTypeName()`, formats
the cell with bitSize 32. Pin: `TestScanRendersRealAtItsOwnPrecision`. Live as
above on `SQL2017`.

### T11 — Fragmentation and storage reads pick an arbitrary allocation-unit or partition row — gosmo — *confirmed*

**Where:** gosmo `index.go:826-834, 883-896, 994-1012`.

**What happens.**
- None of these reads filters `alloc_unit_type_desc = 'IN_ROW_DATA'`.
- On a table with `(max)` columns, or a partitioned index,
  `Index.Fragmentation` can return the LOB row's 0%.
- `FragmentationStats` returns duplicate rows.

**Fix.**
- Filter IN_ROW_DATA, and aggregate across partitions weighted by
  `page_count`.
- Or expose `PartitionNumber` instead.

**Done.**
- `fragmentationSelect` keeps leaf (`index_level = 0`) IN_ROW_DATA rows and
  folds the partitions into one row per index: pages and fragments summed,
  fragmentation and density weighted by `page_count`. It is an `OUTER APPLY`
  so a columnstore index with no delta store still reports, as zeros, rather
  than erroring on Index Properties. `FragmentationStats` now also fills
  `AvgPageSpaceUsedPct` in SAMPLED/DETAILED mode.
- `StorageInfo`'s average record size is the leaf in-row one, weighted by
  `record_count`, instead of `TOP 1` of any row.
- Probed on 17: a two-partition clustered index with an `nvarchar(max)`
  column returns four leaf rows, the LOB ones at 0% over ~670 pages each.
- Pins: `live_dmv_reads_test.go` (partitioned + LOB, all three modes,
  columnstore, record size).
- Live check (tmux, `SQL2017`): Index Properties on that index shows 25.0%,
  81.8% density, 8 pages (both partitions) and a 51.0-byte record size.

### T12 — `ScriptUserDefinedTableType` drops constraints, indexes and collation — gosmo — *confirmed*

**Where:** gosmo `scripter_programmability.go:118-169`.

**What happens.**
- PRIMARY KEY, UNIQUE and CHECK constraints, indexes and a non-default
  `COLLATE` are all lost.
- The memory-optimized index comment that the doc promises is never written,
  so the script can't run.
- `type_table_object_id` is already read (`user_defined_type.go:177`).

**Fix.**
- Read constraints and indexes by that id.
- Reuse `tableColumnDefinition`.
- gossms `scripting.go:141` picks up the fix with no change.

**Done** (W7). The type's internal table is read as a `*Table`
(`typeTable`, unexported — an index handle on it would name the type where
DDL expects a table), so `Indexes` and `CheckConstraints` serve it. The
CREATE writes PRIMARY KEY, UNIQUE and CHECK bare (CREATE TYPE takes no
constraint names, Msg 156), named INDEXes with UNIQUE, INCLUDE, a filter and
IGNORE_DUP_KEY (every other index option is Msg 155), a memory-optimized
type's hash and range indexes, `COLLATE` against the database's, a computed
column's PERSISTED. Each group is sorted by text: the catalog orders by
generated names and index ids, and a replay otherwise scripted differently.
Probed: INCLUDE in a table type's index parses on 17, not on 13 or 14. Pins:
`TestBuildUserDefinedTableTypeScriptKeepsConstraintsAndIndexes`; live
`TestLiveScriptTableTypeShape` replays a disk and a memory-optimized type
and requires the recreated type to script identically (17, 14, 13).

### T13 — Most secrets reach statement observers and script captures in clear — gosmo — *confirmed*

**Where.** `execSecret` (`script.go:298-313`) is used only by Database Mail.

**Still sent through plain `exec`:**
- login passwords (`login.go:280,613`)
- credential secrets (`credential.go`, `database_credential.go`)
- master, certificate, asymmetric and symmetric key passwords
  (`certificate.go`, `master_key.go`, `asymmetric_key.go`,
  `symmetric_key.go`, `signature.go`)
- contained-user passwords (`database_users.go:246`)

**Fix.**
- Route all of them through `execSecret`, and add a `Database.execSecret`.
- **Decision needed:** should a `WithScript` capture keep the real secret?
  The proposal is redaction by default plus a `WithScriptSecrets(ctx)` opt-in.
- gossms: check the Script button of New Login, Credential and Key against the
  chosen policy.

**Done** (W19).
- gosmo: `Server.execSecret` and the new `Database.execSecret` take the
  statement to run and the one to show. `execPasswords` builds the shown form
  with `redactSecrets`, which replaces whole string literals, so a secret that
  is a prefix of another literal does not tear it.
- Every site listed above is routed through it, plus a symmetric key's
  KEY_SOURCE and IDENTITY_VALUE, each with its own placeholder (gossms already
  redacted both).
- The placeholders are exported: `gosmo.PasswordPlaceholder`,
  `SecretPlaceholder`, `KeySourcePlaceholder` and `IdentityValuePlaceholder`.
  The scripters share them, and Database Mail's `<password>` became
  `<insert password here>`.
- `WithScriptSecrets(ctx)` keeps the real values in a capture. Observers are
  always redacted.
- `secret_redaction_test.go` lists every secret write.
- gossms: `scriptSafePassword`, `scriptSafeSecret` and `scriptSafe` are
  removed, and every dialog passes the real value. The endpoint script's
  header now tells the user to replace the login password as well as the
  master key's.

### T14 — An AG peer saved as `host\inst,port` loses its port when retargeted — gossms — *confirmed in code*

**Where:** `internal/db/peer.go:234-255`.

**What happens.**
- `InstanceKey` drops the port for a named instance, so `retargetAt` replaces
  `Server` with the catalog name.
- The dial then needs SQL Browser. win10cli\SQL2017 has none.

**Fix.**
- Keep the saved `Server` when the instance keys match.
- The clean fix is T35.
- Live check on SQL2017.

**Done** (W15). Only a port *written in* `Server` was lost: a Port-field
port already survived. `retargetAt` moves it to `Port`, which gosmo now
dials. Pin: `TestPeerOptionsKeepAPortWrittenInTheSavedServer`.

### T15 — Startup failures print nothing — gossms — *confirmed*

**Where:** `cmd/gossms/main.go:31-39`.

**What happens.** `log.SetOutput(logFile)` runs before `run()`, so for
example "init screen" errors go only to `gossms.log`.

**Fix.** Also print the error to stderr, as the panic path does.

**Done.** `main` prints `gossms error: …` to stderr as well as the log; a
recovered panic (`errPanicked`) is skipped there because `run` already told
stderr. Checked on the built binary with stdin from `/dev/null`: it prints
`gossms error: init screen: open /dev/tty: …` and exits 1.

### T16 — DataGrid error text is clipped to 40 columns after any same-rect `SetBounds` — gossms — *confirmed*

**Where:** `internal/tuikit/controls/datagrid.go:363-375`.

**What happens.**
- `computeColWidths` re-clamps the error column that `SetError` sized to
  `rect.W-2`.
- Property-sheet grids hit this every frame.

**Fix.** Add an `errorMode` flag that `computeColWidths` honours. Ship it
with T38.

**Done** (W24). `SetError` sets `errorMode`, `SetSource` clears it, and in
it `computeColWidths` spans the rect (a dragged width still wins). The column
now also tracks a resize, and a grid errored before its first layout no
longer keeps a negative width. Pin: `TestSetErrorColumnTracksAResize`.

### T17 — Completion inserts reserved words unquoted — gossms — *confirmed*

**Where:** `internal/tui/completion_candidates.go:401-406`; the
`sqlparse/token.go:489` list.

**What happens.**
- The list has about 70 words and lacks DESC, ASC, USER, FILE, PLAN,
  PERCENT, OPEN, CURRENT, SCHEMA, DATABASE, OPTION and others.
- A column named `User` completes to bare `User`, which is the USER function:
  a silent wrong result.

**Fix.** T36. Then gossms uses `gosmo.QuoteNameIfNeeded`.

**Done** (W14). Completion commits every name through
`gosmo.QuoteNameIfNeeded`, whose reserved list is the documented one, checked
against the parser on 13 and 17. Pin:
`TestSQLCompletionBracketsReservedColumnNames` (User, Desc, Plan bracketed;
Cast bare).

### T18 — `TestSessionPropertiesApplyReloadsTheSessionsTargets/Apply` is flaky under `-race` — gossms — *confirmed*

It failed in 8 of 10 runs with `-race` and 0 of 30 without.

**Where:** `internal/tui/explorer_xevents_test.go:420-445`.

**What happens.** `waitAndDrain` returns after the first callback, but the
page reloads often land before the folder reload.

**Fix.**
- `drainUntil(… len(session.children) == 2 …)`.
- Correct `docs/testing.md`'s "race clean" note when it lands.

**Done.** `drainUntil` on `len(session.children) == 2`. 0 failures in 30
`-race` runs; reverting to `waitAndDrain` fails again. `docs/testing.md`'s
"clean as of 2026-10-02" note is now true and stays.

### T19 — Restore and Backup dialogs hand-roll a single `loadSeq` token for every load — gossms — *confirmed*

**Where:** `internal/tui/restore_dialog.go:155,208`,
`restore_dialog_ops.go:17,62,250,356,416,686`, `backup_dialog.go:320`.

**What happens.**
- Nothing is cancelled: holding ←/→ runs every RESTORE FILELISTONLY to
  completion.
- Unrelated loads share the one token. A history load that starts during
  "Checking target database…" drops `startRestore`'s check, and the restore
  never starts.
- Attach (`attach_database_dialog.go:146`) and New Snapshot
  (`new_snapshot_dialog.go:186`) use a third pattern.

**Fix.** One `latest` per load kind, all of them abandoned on show and on
close.

**Done** (W11). Restore: `dbListRun`, `historyRun`, `infoRun`, `fileRun`,
`checkRun`, `scriptRun` (plus the existing `defPaths`); Analyze abandons
`fileRun`, whose list is for the device it replaces. Back Up: `dbListRun`.
Both gained a `Hide` that abandons every load (a running backup or restore is
a Task and carries on). Attach and New Snapshot: the `reading` latch and
session-context compare became `fileRead`/`defaultsRead`, begun from `d.ctx`
and abandoned by `show` and `OnClose`; a second press now supersedes instead
of being refused. New Snapshot's source or name change abandons its read too —
a read landing after the edit used to fill the grid with paths for the old
name. Pins: `TestRestoreHistoryLoadDoesNotDropTheTargetCheck`,
`TestRestoreHideAbandonsItsLoads`, `TestBackupHideAbandonsTheDatabaseList`,
`TestSnapshotNameChangeAbandonsTheDefaultsRead` (`dialog_loads_test.go`).

### T20 — Recycle error log is gated on CONTROL SERVER — gossms — *plausible, needs a probe*

**Where:** `internal/tui/explorer_management.go:252-253`,
`log_viewer_toolbar.go:72-77`.

**What happens.** `sp_cycle_errorlog` and `sp_cycle_agent_errorlog` check
sysadmin (Msg 15247).

**Fix.**
- Probe on 13 and 17 with a login that has CONTROL SERVER but is not
  sysadmin.
- If the server refuses, gate on `gate.Sysadmin`.
- Leave the mail-log arm as it is.

**Done** (W13). Probed on 17 (17.0.1135.8) and 13 (13.0.6500.1) with a
throwaway login holding CONTROL SERVER and outside sysadmin:
`sp_cycle_errorlog` refuses it Msg 15247, `msdb.dbo.sp_cycle_agent_errorlog`
Msg 14260 (from `sp_sqlagent_notify`, as Agent Properties' reload). Both the
Explorer item and the viewer's Recycle cell (`recycleRights`) now gate on
`gate.Sysadmin`; the mail arm is unchanged. Pins:
`TestExplorerRecycleIsGatedOnSysadmin`,
`TestLogViewerRecycleIsWithheldFromALoginThatCannotCycle`,
`TestTheLogViewerOverflowMenuKeepsTheGate` (now a CONTROL SERVER login).

### T21 — Restore repairs MULTI_USER after any error — gosmo — *confirmed*

**Where:** gosmo `backup.go:418-425`.

**What happens.** Drop and Detach gate this repair on `batchCutShort(err)`,
per its documented contract (`server.go:222-224`). Restore doesn't, so a
refused restore can flip a deliberately RESTRICTED_USER database to
MULTI_USER.

**Fix.** Gate on `batchCutShort` as well.

**Done** (W6). Probed first on 17 and 13: a refused RESTORE (Msg 3201/3013,
severity 16) is statement-level, so the batch goes on to its own MULTI_USER —
the premise the gate relies on, now pinned live in
`live_restore_close_test.go` ("a refused restore is released by its own
batch"). The batch's `SET @closed = 1` is also gated on `@@ERROR = 0`, as
`exclusiveBatch` already was, so a refused SINGLE_USER no longer releases a
mode the batch never set. Unit pin: the "server error" case of
`TestAFailedRestoreLeavesAnAccessModeItDidNotSet`.

### T22 — `BulkInsert` ignores `WithScript` and the statement observer — gosmo — *confirmed*

**Where:** gosmo `bulkcopy.go:105-170`.

**Fix.**
- Under `Scripting(ctx)`, return `ErrUnsupported`.
- Observe an `INSERT BULK … -- n rows` entry.

**Done** (W6). Refuses before acquiring a connection; the entry carries the
server's row count and quoted column list. gosmo `ARCHITECTURE.md` § Scripting
pending writes names BulkInsert as the one write outside the chokepoints.
Pins: `TestBulkInsertRefusesUnderWithScript`, live
`TestLiveBulkInsertObservedAndScripted` (17).

### T23 — `Job.AddSchedule` can't express weekly or monthly schedules — gosmo — *plausible, high confidence*

**Where:** gosmo `agent_job.go:420-460`.

**What happens.**
- The request has no recurrence factor, relative interval or dates.
- `sp_verify_schedule` rejects freq_type 8, 16 or 32 when the factor is 0
  (Msg 14266).

**Fix.**
- Take `CreateScheduleRequest` and return `*Schedule`. This is breaking.
- gossms has no call sites.

**Done** (W7). *Confirmed* on 17: `@freq_type = 8` with no recurrence factor
is Msg 14278 (not 14266). `AddSchedule(ctx, CreateScheduleRequest)
(*Schedule, error)` sends every field through `frequencyArgs`, which
`CreateSchedule` now shares. sp_add_jobschedule has no owner parameter, so an
`OwnerLoginName` is applied by `sp_update_schedule` in one `atomicBatch`. The
result is the job's newest schedule of that name (names are not unique).
`JobScheduleRequest` is gone; `examples/jobs` moved to the new request. Pins:
the `Job AddSchedule` row of `script_agent_write_test.go`; live
`TestLiveJobAddScheduleRecurrences` (weekly every 2 weeks, last Friday
monthly, an owner, and a refused owner leaving no schedule) on 17, 14 and 13.

---

## Bugs — low

| ID | Repo | Where | Defect → fix |
|---|---|---|---|
| T24 | gosmo | `object_filter.go:139-153` | The date criterion keeps the caller's zone, and go-mssqldb sends `datetimeoffset`, so a non-UTC `Day` shifts the window. Build `day` in UTC. gossms already passes UTC. **Done** (W6): the calendar date in `Day`'s own zone, sent at +00:00; live subtests at +14:00 and −12:00 in `live_objectfilter_test.go` (one of the two fails against the old code at any time of day — +14:00 did on 14). |
| T25 | gosmo | `statistics.go:36-45` | `CROSS APPLY dm_db_stats_properties` hides statistics the caller can't read → `OUTER APPLY`. *Confirmed on 17.* **Done** (W5): a login with only VIEW DEFINITION on the table saw 0 of 2 statistics, now 2; pin in `live_dmv_reads_test.go`. |
| T26 | gosmo | `capabilities_database.go:294,352`, `capabilities.go:456` | Keys are `.`-joined, so `ObjectKey("a.b","c") == ObjectKey("a","b.c")` and a DENY lands on the wrong object → `\x00` separator. **Done** (W7): `keySep` in Go, `NCHAR(0)` in the probe query; SQL Server refuses NUL in an identifier (Msg 1055) and go-mssqldb returns it intact (13, 17). gossms production code builds no key literal, but its test fixtures did: the fake probe rows spelled keys `"sales.IdleQueue"` and 13 gate tests went red. They now go through `probeKey` (`fakedb_test.go`), which calls gosmo's helpers. Pins: `TestCapabilityKeysKeepDottedNamesApart`; live `live_dotted_capability_keys_test.go` on 17, 14, 13 — fails against the old separator for both objects and types. |
| T27 | gossms | 15 `Reload`, 4 `RefreshDatabasesFolder`, 7 `RefreshFolderByType`, … | Five ways to refresh after a create, and `Reload` is a no-op on a retired node → standardise on `ReloadFolders(sc, folderOf(…))`. After T8. **Done** (W9): `folderOf` where the folder is server-scoped or named by type and database; `sameNodeAs(n)` (type, database, schema, name, table, AG, XE session, pool) where a dialog or progress job holds the node it started from, so a Refresh above it in the meantime no longer swallows the write's refresh. `ReloadFolders` also matches a root. Take Offline's success path reloads the Databases folder when its node was retired. The Agent enable/disable success path still writes `IsEnabled` into the held node without a reload — left as it was. Pins: `TestPostWriteRefreshFindsTheReplacementFolder`, `TestSameNodeAsKeepsTableScopedFoldersApart`; `TestTheDetailPaneDeleteRefreshesTheFolder` now puts its folder in the tree. |
| T28 | gossms | `progress_job.go:64-70` | A refused `runWithProgress` never runs `repair`/`done`, so the callers' `busy` latches (Query Store, Log Viewer, Activity Monitor) stay set → call `job.repair()` on refusal. *Plausible.* **Done** (W11): the refusal runs `repair`. Pin: `TestRefusedProgressJobRunsRepair` (fails without it). |
| T29 | gossms | `login_props.go:35,52` | `withRequiresOn` captures the login name by value, so after a rename the gate asks about the old name → read it through `namePtr`. *Plausible.* **No change needed** (W12): the gate reads the capability probe, which recorded the old name and re-runs only on a server-node Refresh (unreachable while the modal dialog is open). A DENY on a login or user also refuses its rename, so the new name can't carry one the old didn't; for a role or server role the DENY leaves the rename alone, and reading the box would ask the stale probe about a name it never saw and open Members editable. Settled in `docs/decisions.md` § Permission gating, The rest, with a comment in `login_props.go`. |
| T30 | gosmo | `agent_job.go:400-415`, `login.go:570-579` | `CreateJob` and `CreateLogin` are two statements, not atomic → `atomicBatch`. **Done** (W6): rollback of both shapes probed on 17 and 13. CreateLogin's batch applies only to the external-provider CREATE + ALTER; on Azure SQL Database, where CREATE LOGIN must be alone in its batch, they stay two statements (not run live: no MI or SQL Database available — pending). |
| T31 | gosmo | `scripter_dml.go:232`, `function.go:37-43` | CLR scalar functions (FS) are scripted as `SELECT * FROM f()`, and CLR functions are missing from listings → typed `FuncType`, `LEFT JOIN sql_modules`. **Done** (W7): `FunctionType` (`IsScalar`, `IsCLR`); the listing takes FS/FT; a CLR module's CREATE/ALTER is `ErrUnsupported`, not "not found" (functions, procedures, triggers). Live: `live_clr_function_test.go` loads `testdata/clr/w7clr.dll` (trusted for the test on 14+), on 17, 14, 13; `SELECT * FROM` a CLR scalar function is Msg 208. Still open (gosmo OPEN-THREADS § CLR modules): scripting the CLR CREATE, and CLR procedures and triggers in their listings. |
| T32 | gossms | `editor_draw.go:16,49,403` | Line numbers ≥ 10,000 draw over the border → `gutterWidth = max(5, digits+2)`. **Done** (W12), in `Editor.gutterWidth`; the wrap cache is keyed by width, so a growing gutter re-wraps. Pin: `TestEditorGutterWidensPastLine9999` (plain and wrapped). |
| T33 | gossms | `treeview.go:299-301,326` | Right and `+` collapse an expanded node, against the comment and F1 help → `expandSelected()`. **Done** (W12); Enter still toggles. Pin: `TestTreeViewRightAndPlusNeverCollapse`. |
| T34 | gossms | `showplan/parse.go:103,279,305,418` | `Trim(x,"[]")` doesn't un-double `]]`, so the Missing Index script names the wrong object → `gosmo.UnquoteName` (T36). **Done** (W14), with a local `unbracket` rather than gosmo: the package stays driver-free. Confirmed live on 17 that plan XML doubles `]`. Pins: `TestMissingIndexScriptKeepsABracketInAName`, `TestUnbracket`. |
| T64 | gossms | `core/drawing.go:240-251` | The scrollbar thumb never reaches the bottom → `offset*(h-thumbH)/(total-visible)`. **Done** (W12): `core.scrollThumb`, shared by `DrawScrollbar` and `DrawScrollbarH`. `ScrollOffsetForDrag` had the twin defect — `y*total/h` topped out at 900 of 995 on a 10-row track over 1000 rows — and is now linear from the first row (0) to the last (`total-visible`). Pins: `TestScrollbarThumbSpansTheWholeTrack`; `TestScrollOffsetForDrag` unchanged. |
| T65 | gossms | `core/clip_screen.go` | `FillArea` isn't clipped, and `charts.Canvas` panics on it. Latent, but blocks T51. **Done** (W24): `ClipScreen.FillArea` intersects with the clip, `Canvas.FillArea` writes in bounds. Pins: `TestClipScreenFillAreaCoversOnlyTheClip`, `TestCanvasFillAreaFillsOnlyTheArea`. |
| T66 | gossms | `core/strutil.go:356` | `EvRune` keeps only the first rune of a composed key (IME, ZWJ) → insert `[]rune(ev.Str())`. **Done** (W12): `core.EvText`, used by `Editor` (plain and block), `InputField` and the plan view's search; `EvRune` stays for key matching. Pins: `TestEditorInsertsAComposedKeyWhole`, `TestInputFieldInsertsAComposedKeyWhole`. |
| T67 | gossms | `datagrid_draw.go` | CR, LF and TAB in a cell render glued together → map them to a space. **Done** (W12): `core.TruncateLine` (CR, LF, CRLF and TAB each one space, no copy without them) in every `datagrid_draw.go` cell path and in `computeColWidths`, so the column is sized as drawn. Pins: `TestTruncateLine`, `TestDataGridDrawsLineBreaksInACellAsSpaces`. |
| T68 | gossms | `config/config.go:631` | A bad `gossms.key` blocks every save → write the sealed blobs back and refuse only new passwords. **Done** (W12): `Save` carries the key error into `mergeAndWrite`, which keeps every ciphertext as Load does (`keepSealed`), writes settings and connections, leaves out only passwords that would need the key (a re-entered one keeps its entry's old ciphertext), and then returns an error naming those connections. Pin: `TestABadKeyFileStillSavesEverythingButNewPasswords`. |
| T69 | gossms | `properties_dialog.go:71-96` | The Object Dependencies fetch isn't cancelled on close → `OnClose` calls `Abandon`. **Done** (W11): `dialogs.PropertiesDialog` gained an `OnClose` hook (Escape, Enter, Close), wired to `run.Abandon`. Pin: `TestDependenciesCloseAbandonsTheFetch`. |
| T70 | gossms | `app_explorer_data.go:159-161` | `primeDatabaseCapabilities` has no deadline → `WithTimeout(childFetchTimeout)`. **No change needed** (W11): the review missed `db.ServerConn.probeDatabase`, which bounds every probe with `capabilityProbeTimeout` (10 s); a caller waiting on another's probe gets its answer or, if that one was abandoned, becomes the prober under the same bound. A 30 s outer deadline would bound nothing more. |
| T71 | gossms | `clipboard.go:129-136` | Two quick copies race for the last owner → one worker goroutine. *Plausible.* **Done** (W12): `App.clipWriteMu` serialises the writes and `clipWriteSeq` drops one a newer copy has superseded, OSC 52 fallback included — the worker's ordering with no long-lived goroutine. Pin: `TestQuickCopiesLeaveTheLastOnTheClipboard` (old code: `C B A`). |
| T72 | gossms | `explorer_object_actions.go:338-361` | Rename sends `""` to the server → refuse it in the prompt. **No change needed** (W12): `PromptDialog.accept` has refused an empty or whitespace-only value ("Enter a value.") since August, pinned by `TestPromptDialogRefusesEmptyAndInvalidValues`. |
| T73 | gossms | `explorer_filter.go:480-488` | The `default:` arm pushes any text criterion down as a Name → match `fpName` explicitly. Latent. **Done** (W12): any other text property refuses the pushdown, so the folder is read whole and filtered client-side. Pin: a `TestNodeFilterPushdown` case. |

---

## gosmo API (breaking by design)

Each item is one gosmo change followed by the gossms call sites in the same
working session (the `dev-with-local-gosmo` skill).

- **T35 — `ConnectionOptions.Port int`.**
  - Removes `ResolveServer`'s comma and colon folding
    (`internal/db/connection.go:352-369`).
  - Fixes T14 properly.
  - **Done** (W15). The dial path no longer folds; `ResolveServer` remains
    for keys and display only.
- **T36 — Identifier helpers: `IsReservedKeyword`, `QuoteNameIfNeeded`,
  `UnquoteName`, `QuoteAnsiLiteral`.**
  - Replaces sqlparse's partial keyword list (T17), showplan's `bracket()`
    (T34), and the hand-built XE predicate refs
    (`xevent_session_events.go:64,441-447`).
  - **Done** (W14): all four in gosmo `quoting.go`; see W14 for the call
    sites and the showplan exception.
- **T37 — `Server` lifetime context.**
  - `Close` cancels it, and every read is bounded by it.
  - Replaces `ServerConn.ctx`/`Context()` and retires the "load rooted at
    `context.Background` outlives disconnect" bug class.
  - The largest boundary item, at about 1 day.
  - **Done** (W16): writes are bounded too, not only reads; see W16.
- **T39 — Exported error predicates: `IsPermissionDenied`, `IsAlreadyExists`,
  `IsObjectMissing`, Database Mail errors.**
  - Replaces `refusalNumbers` (`permission_error.go:152-176`),
    `isAlreadyExists` (`new_endpoint_dialog.go:825`) and
    `send_test_mail_dialog.go:276-298`.
  - **Done** (W17), with two departures from the names above:
    - `IsObjectMissing` shipped as `IsMissingOrDenied`, beside
      `ClassifyRefusal` (kind plus the message that says so) and
      `IsPermissionDenied`. The numbers it covers are the ambiguous
      "does not exist or you do not have permission" ones, and a name
      claiming "missing" would invite exactly the narrowing the server
      withholds on purpose.
    - `IsAlreadyExists` is numbers only (1801, 1913, 2714, 15023, 15025).
      The English-text fallback for a non-SQL-Server error is gone;
      `docs/decisions.md` updated.
    - The mail errors are sentinels `SendMail` wraps:
      `ErrMailProfileInvalid`, `ErrMailNoDefaultProfile`, `ErrMailStopped`,
      `ErrMailXPsDisabled`.
- **T40 — Catalog derivations as methods.**
  - `Login.IsSystem`, `Login.IsSQLLogin`, `Job.IsSystem`, `User.IsMapped` and
    `User.IsExternal`, `Certificate.IsExpired(now)`, `ServerInfo.IsWindows`,
    `QueryStoreInfo.IsReadable`.
  - Moved from `system_principals.go:108`, `agent_explorer.go:62`,
    `user_props.go:251-260`, `explorer_databases.go:410`,
    `server_filesystem.go:97` and `query_store_reports.go:401`. The system-object
    rules gate DROPs.
  - **Done** (W17). The gossms tests for the moved helpers moved with them
    (`catalog_derivations_test.go` in gosmo); gosmo's own scripter now uses
    `Login.IsSQLLogin`.
- **T42 — Handle-centric writes.**
  - Add `Role.AddMember`/`RemoveMember` and
    `ServerRole.AddMember`/`RemoveMember`.
  - Put file and filegroup writes on their handles.
  - Delete a category through its handle, and use `Drop` everywhere
    (`JobStep.Delete`, `Server.DeleteCategory`).
  - Rename `Alert.RemoveNotify` → `RemoveNotification`.
  - `Job.AddStep` returns `*JobStep`.
  - `Credential.Alter` takes an options struct.
  - Remove the parent-side forms.
  - gossms call sites: `login_props.go:584,588`, `new_login_pages.go:463`,
    `new_user_dialog.go:455`, `user_props.go:347,351`,
    `role_props.go:193,196`, `server_role_props.go:169,172`,
    `database_props_files.go:548,562`, `database_props_filegroups.go:163`,
    `agent_alert_props.go:213`, `agent_job_props_steps.go:447`,
    `new_job_pages.go:139`.
  - **Done** (W18). The shapes that shipped:
    - `DatabaseRole.AddMember`/`RemoveMember` (the type is `DatabaseRole`,
      reached by `RoleRef`) and `ServerRole.AddMember`/`RemoveMember`
      replace `Database.AddRoleMember`/`RemoveRoleMember` and
      `Server.AddServerRoleMember`/`RemoveServerRoleMember`.
      `Login.AddServerRoleMember` and `User.AddToRole` stay: they are
      member-side handle writes, not parent-side ones, and now delegate.
    - Files: `Database.FileRef(name)` returns a `*DatabaseFileInfo`, which
      gained its database back-pointer and `Alter(m)` (mirrors a NEWNAME) and
      `Drop`; `Files` and `Server.DatabaseFiles` hand back wired handles.
      Filegroups: `Database.FileGroupRef(name)`, and `FileGroup` gained
      `Drop`, `SetDefault`, `SetReadOnly(ro, term)`, each mirroring onto the
      handle. `AddFile`/`AddFileGroup` stay on `Database` under their names:
      a create returning a handle would need a by-name read neither family
      has yet, which is outside this step.
    - `Server.CategoryRef(class, name)` and `Category.Drop`; `Category`
      gained its server back-pointer.
    - `JobStep.Drop` (was `Delete`). `AddStep` and `InsertStep` both return
      `*JobStep`, read back by job and step name (msdb keeps step names
      unique per job), so a `JobRef` works; under `Scripting(ctx)` the
      handle carries the name and InsertStep's position.
    - `Credential.Alter` and `DatabaseScopedCredential.Alter` both take
      `CredentialOptions{Identity, Secret}` — the two had the same
      positional signature, and changing one alone would have split them.
    - gossms: every site above plus `new_database_pages.go` (filegroup
      read-only and default) and `credential_props.go`/
      `database_credential_props.go`. The page tests' job fakes gained
      `jobStepReadBack`, since the step read-back says
      `WHERE  j.name = @p1` like the job by-name read.
- **T45 — `ScriptCollector.Entries` becomes `Entries()`/`Len()`.** The field is
  guarded by an unexported mutex. Call sites: gossms
  `new_object_dialog.go:478`, `prop_dialog.go:883`.
  - **Done** (W18). `Entries()` returns a copy, `Len()` the count; the
    field is now unexported `entries`.
- **T46 — `BuildBackupStatement` becomes a `Server` method,** matching
  `BuildRestoreStatement`. That makes room for future gating.
  - **Done** (W18). The Back Up dialog calls it on its connection's
    `Server`; the tui tests use a zero `gosmo.Server`, which the builder
    never reads.
- **T47 — `Server.InTransaction(ctx, fn)`.**
  - Binds one connection and transaction into ctx, the way `WithScript`
    threads a collector.
  - Makes Resource Governor Apply atomic (`resource_governor_props.go:37-42`),
    and helps multi-step Database Mail applies and step reorders.
  - About 2 days. The design needs agreeing first.
  - **Done** (W21), in gosmo's new `transaction.go`. The shape:
    - `Server.InTransaction(ctx, fn func(ctx) error) error`: `BeginTx` on
      the pool, bounded by `Server.Context`; the `*sql.Tx` rides in ctx and
      `txFrom(ctx, s)` hands it to every chokepoint for the same Server.
      `withConn`'s callback and `Database.use` now take a `sqlConn`
      (`*sql.Conn` or `*sql.Tx`).
    - Reads go through it, unretried. A database-scoped statement leaves the
      session in its database; the next server-scoped one switches back to
      the database the transaction began in (`serverTx.home`).
    - `observe` takes the Server and holds a transaction's reports until
      COMMIT.
    - Refused inside (`ErrUnsupported`): Backup/Restore with progress,
      `BulkInsert`, `EffectivePermissions` and
      `EffectiveServerPermissions`.
    - gossms: `runResourceGovernorPlan` runs the plan in it.
      `fakedb_test.go`'s driver gained transactions (`TxLog`,
      `StatementsInTx`). The Database Mail apply and step reorders are not
      converted — their `sysmail_*`/`sp_update_jobstep` calls are unchecked
      inside a user transaction.
- **T48 — Restore domain rules into gosmo:** `BackupHeader.SetNumber()` and
  `RestoreSpec.FromHeader`.
  - Moved from `restore_dialog_ops.go`'s `backupSetNumber`,
    `restorableHistory` and `relocateFiles`, so the WITH FILE / MOVE agreement
    lives in one place.
  - **Done** (W20), in gosmo's new `restore_plan.go`. The shapes that shipped:
    - There is no `RestoreSpec`; `RestoreOptions` is the spec, so the method
      is `RestoreOptions.FromHeader(h, files, reloc)`, setting `FileNumber`
      and `RelocateFiles` from one set.
    - `BackupHeader.SetNumber()` and `BackupInfo.SetNumber()`: the Position,
      0 (no clause) for set 1. That replaces the slice-based
      `backupSetNumber`, whose lone-set and index+1 cases both reduce to it.
    - `BackupSetAt(headers, n)` finds the set being restored and returns nil
      when the device no longer holds it. `buildRestoreOptions` used to fall
      back to the first set's name there; it now refuses with "backup set n
      is no longer on …", since restoring set 1 instead restores a different
      backup.
    - `RestoreRelocation{Mode, DataDir, LogDir, DefaultDataDir,
      DefaultLogDir, Collation}` with `NeedsFileList` and `Moves` replaces
      `relocPlan`/`relocateFiles`; the dialog's radio maps onto
      `RelocateIfRenamed`/`RelocateNone`/`RelocateToFolders`.
    - `BackupInfo.Restorable()` is the deviceless predicate;
      `restorableHistory` stays in gossms as the filter around it, since
      BackupHistory's other readers want every row.
    - gosmo's `joinServerPath` now leaves a file bare for an empty
      directory, as gossms's did. The case-fold rule is duplicated
      (`sameDatabaseName` in gosmo, `collationFoldsCase` in gossms's
      `name_set.go`); folding them into one exported gosmo helper is W27
      material.
- **T49 — Name-only handle guard.** `TableRef`'s ~22 ObjectID-keyed reads
  return `ErrHandleNotLoaded` instead of an empty result. This is not the
  `Load(ctx)` handle type that decisions.md rejected. It only turns the
  documented trap into an error.
  - **Done** (W20). The gossms grep found only name-keyed uses (Drop,
    DropColumn, RenameColumn, DropConstraint, RenameConstraint). Guarded:
    `Detail`, `Columns`, `ForeignKeys`, `ForeignKeyByName`,
    `CheckConstraints`, `Triggers`, `RowCount`, `Indexes`, `IndexByName`,
    `DataSpace`, `XMLIndexes`, `Partitions`, `SpaceUsed`, `Statistics`,
    `StatisticByName`, `EdgeConstraints` (16, through `Table.requireLoaded`);
    `Index.IncludedColumnsSupported` now wraps the sentinel too. The
    scripter's private reads are reached only through `TableByName` and are
    left unguarded. `createdObject` treats the refusal like not-found, so
    `CreateIndex`/`CreateStatistic` on a TableRef return the new handle
    instead of failing after the create. `Statistic.Columns` now finds its
    statistic by name, which its doc already claimed. Pin:
    `TestTableRefReadsAreRefused`, `TestCreateOnATableRefReturnsTheHandle`.
- **T50 — `ServerInfo.Login` from `loadInfo`.** Drops the separate
  `SUSER_NAME()` round trip on every connection
  (`internal/db/connection.go:150`). **Done** (W16).

---

## Performance

| ID | Repo | Where | Change |
|---|---|---|---|
| T38 | gossms | `datagrid.go:276-279`, `propsheet/gridrow.go:38` | `DataGrid.SetBounds` re-samples 200 rows and allocates every frame, against ui-rules ("SetBounds does nothing when the rect hasn't changed") → return early on an unchanged rect. Audit hosts that rely on the per-frame recompute. Includes T16. **Done** (W24); see the step. Pins: `TestSetBoundsOnAnUnchangedRectDoesNotRescan`, `TestSettersThatChangeWidthsApplyWithoutARelayout`. |
| T51 | gossms | `core/drawing.go:102-115` | `putGrapheme`'s `[]rune` plus tcell's `SetContent` re-pack cost about 2 allocations per cell per frame → `s.Put`, plus `FillArea` in `FillRect`. Needs T65. Add a frame benchmark. **Done** (W24), with `Put` in `FillRect` rather than `FillArea` (see the step), and `core.PutRune` (a static string table for ASCII and U+2500–U+259F) for every single-rune cell. `Canvas.Put` gained the single-rune fast path its `SetContent` had. Pin: `TestRuneStringIsStringOfRune`. |
| T52 | gosmo | `procedure.go:190,280`, `view.go:46,86`, `function.go:40,83`, `trigger.go:35`, `rule_default.go:74` | Listings pull every module's `definition` (about 1,400 system procs) → drop it from listings, and keep it on `*ByName` / `Definition(ctx)`. **Breaking.** gossms rule/default sites at `detail_browser_programmability.go:140,155,306,318` switch to the by-name read. **Done** (W22): the field is gone from `*ByName` too; `Definition(ctx)` on all six types. |
| T53 | gosmo | `catalog.go:119-150`, `security_policy.go:70-77`, `login.go:337-359` | `Catalog()` costs 4 round trips → 1 multi-result batch. `SecurityPolicies` runs N+1 → one grouped query. `UserMappings` does one per database → one batch with per-database TRY/CATCH (*plausible*; the parallel fan-out was already rejected). **Done** (W22): all three, live on 17 and 13. |
| T55 | gossms | `sql_highlighter.go:234` | `ToUpper(string(…))` per word per frame → sqlparse's stack-scratch fold. **Done** (W23): `isSQLKeyword`. Pins: `TestSQLHighlighterDoesNotAllocatePerWord` (11 allocations per line before, 2 now: the run slice and one growth) and `TestSQLKeywordsFitTheFoldBuffer`. |
| T56 | gossms | `sqltext/split.go:32`, `xevent/filter.go:110` | `[]rune` of the whole script → a byte scan. `ToLower` per value per event → an allocation-free case-insensitive contains. **`SplitBatches` done** (W23), as a line scan rather than a byte scan: each line is decoded into one reused buffer and lexed with the carried `State`, so the cost is the longest line, and the rule stays `sqltext.Next`'s. The XEvent half is W24's. **XEvent half done** (W24): `containsLower`, `cutLowerPrefix` and `compareLower` fold as they read, and `parseNumber` turns away text before `ParseFloat` can allocate its error. Pins: `TestFoldedComparisonsMatchToLower`, `TestParseNumberMatchesParseFloat`, `TestFilterMatchDoesNotAllocate`. |
| T57 | gossms | `internal/db/connection.go:153` | The capability UNION runs for Query, Activity Monitor and XEvent connections, which never read it → skip it for non-Explorer roles. **Done** (W16): `newServerConn` probes for `RoleExplorer` only; completion's per-database probe is lazy and unaffected. Pin: `TestOnlyExplorerConnectionsProbeServerCapabilities`. |
| T58 | gossms | `options_dialog.go:321`, `query_store_panel_load.go:93`, `connect_dialog.go:494` | `config.Save` (lock, crypto, fsync) runs on the UI goroutine → save off-thread and post the result back. *Plausible stall.* **Done** (W25); see the step. |

---

## Consistency

- **T54 — One T-SQL lexer in `sqltext`.** A state machine for nested
  comments, quotes, brackets, `GO`, and a token-level statement splitter.
  - `SplitBatches`, `sqlparse.lexSQL`, `controls.sqlStatementAt` and the
    highlighter's line step all use it.
  - Fixes T2, T3 and T44, and ends the hand-sync drift.
  - sqlparse stops importing `tuikit/core` (word-rune rules move to
    `sqltext`).
  - About 2 days.
  - **Done** (W23).
    - `sqltext.Next` (`lexer.go`) is the lexer: one token per call, a
      `State` (mode plus block-comment depth) carried between calls, so a
      caller lexes a flat buffer or line by line and gets the same answer
      (`TestLineByLineLexingMatchesFlat`).
    - `SplitBatches`, `StatementAt` (the Ctrl+Enter splitter, moved from
      `controls` with its tests unchanged), the highlighter and
      `sqlparse.lexSQL` all walk text with it.
    - `sqlparse` keeps its own DML splitter (the user's call): IntelliSense
      scopes to DML statements, Ctrl+Enter to every statement verb. Both read
      `sqltext`'s verb tables (`IsDMLLeader`, `EndsDML`, and
      `statementLeaders` built from them), so the lists can't drift again.
    - One word rule everywhere: `sqltext.IsWordRune`; a `#`/`@` sigil is one
      or two of the same rune; a word starting with a digit is a number, which
      now runs through `.` (`1.5` is one token, where sqlparse had three — no
      golden output changed). The editor's completion trigger uses the same
      rule. `core.IsWordRune` stays for Ctrl+arrow word navigation in any
      text field, which is not a T-SQL rule.
    - sqlparse no longer imports `tuikit/core`. `scope.go` is split along its
      sections into `scope.go` (cursor context, FROM scope, clause),
      `statement.go` (DML statement bounds) and `query.go` (the Query tree and
      its parser), byte-for-byte apart from the headers.
- **T44.** The highlighter ignores `[…]` and `"…"`: keywords inside them are
  coloured, and `/*` inside `[a/*b]` comments out the rest of the document.
  Fixed by T54. **Done** (W23): the highlighter colours `sqltext.Next`'s tokens, and its
  line cache holds a `sqltext.State`, so a literal or identifier left open at
  a line's end also carries onto the next (it used to end at the line).
  Pin: `TestSQLHighlighterLexesQuotedIdentifiersAndCarriesLiterals`, 7 of 11
  cases fail on the old code.
- **T59 — Null-aware result model.** A per-set null bitmap in
  `query.ResultSet` and a `RowSource.IsNull(row,col)` capability.
  - tuikit stops dimming the literal `"NULL"` (`datagrid_draw.go:168,242,266`).
  - Grid, copy and CSV can tell NULL from `'NULL'`.
  - Also: `TreeView`'s hard-coded "Object Explorer" title
    (`treeview.go:197`) becomes `SetTitle`.
  - **Done** (W26); see the step.
- **T60 — One connection key.** Tracked queries key on
  `ToLower(Opts.Server)` (`config/tracked.go:120`) → `db.InstanceKey`. Fold
  this into T9's helper.
  - **Done** (W10). `serverKey` is `InstanceKey`, and every caller passes
    `config.ConnectionAddress(opts)`, so a Port-field port is no longer
    dropped. `readTrackedFile` re-folds each stored key and merges sets that
    collide (it used to assign them), and the next Save writes the new keys.
    One loss can't be recovered: a pin saved before this from a connection
    whose port was in the Port field went under the bare host, so it now
    shows under the default instance. Pin:
    `TestTrackedQueriesLoadAnOldFormatFile` (fails on the old fold). Live
    check under W10.
- **T61 — gossms async UI consistency.**
  - `query_store_panel_load.go:129,143,321` → `BeginTimeout`.
  - Backup and Restore task errors on cancel → map to "cancelled" in
    `markTaskDone` (`backup_dialog.go:475`, `restore_dialog_ops.go:491`).
  - `applyNow` skips `InvalidateAll()` when hiding (`prop_dialog.go:831`).
  - F1 cycles buttons in Connect, Backup and Restore (`dialog_common.go:137`)
    → **decision:** drop it, or document it for all three.
  - **Done** (W11). The three Query Store reads begin with `BeginTimeout`
    (the inner `WithTimeout` is gone). A task whose Cancel was asked for and
    that then failed is `Task.Cancelled`: "<Label> — cancelled" in Tasks,
    "<Verb> cancelled." in the progress view, "<Label> cancelled" in the status
    bar; a panic is still a failure and a run that finished anyway keeps its
    success. `applyNow` calls `InvalidateAll` only when the dialog stays open.
    F1: replaced, not dropped or documented — the form views' only keyboard
    route to their buttons was F1, so the button row became a Tab stop
    (`buttonRowKey`), crossed with Left/Right, in Connect, Back Up and Restore
    (form and File Locations; Backup Information already cycled on Tab). Pins:
    `TestCancelledTaskReadsCancelled`,
    `TestPropDialogOKDoesNotReloadThePageItCloses`,
    `TestConnectTabReachesTheButtonRow`, `TestConnectButtonRowSkipsGatedButtons`,
    `TestBackupAndRestoreTabReachTheButtonRow`,
    `TestProgressModeKeyRotatesAndHides`.
- **T62 — Case folding.**
  - The `completion_crossdb.go:88-90,125` database directory → the
    collation-aware `nameMap`. **Done** (W12): `completionDirectory.byName`
    is a `nameMap` under the server collation (`nameMap.Values` added for the
    database list), and the own-database check is `sameName`. Pin:
    `TestCompletionDirectoryFollowsServerCollation` (Sales ONLINE beside sales
    OFFLINE on a CS server).
  - gosmo: the remaining `.`-joined keys (T26).
- **Docs drift.**
  - gosmo `errors.go:60-67`: the `ErrSchemaRequired` doc is spliced into
    `notFoundf`'s.
  - `ErrUnsupported`'s doc is too narrow, `schedulerGroupSizes` wraps with
    `%v`, and `unsupportedError` can't carry a cause.
  - The `Server.CurrentDatabase` doc contradicts `applyDefaults` (`"master"`).
  - gosmo ARCHITECTURE lists 10 Ref handles, while CLAUDE.md counts 61.
  - decisions.md says "55 families".
  - **The five gosmo items above are done** (W7). `unsupportedf` keeps a `%w`
    cause reachable (`TestUnsupportedKeepsAWrappedCause`), and
    `schedulerGroupSizes` uses it. There are 64 `…Ref` methods, so no count
    survives: gosmo CLAUDE.md's `Ref` bullet lists the families (plus the
    `MasterKeyRef` and `ResourceGovernorRef` singletons), and ARCHITECTURE and
    decisions.md point to it.
  - tuikit README § Dependency direction: `layout` imports `controls`,
    `dialogs` doesn't import `layout` (**T43**).
  - The README promises `SetBounds(x,y,w,h)`, but `Widgets`, `MenuBar` and
    `Toolbar` differ → document the exceptions.
  - **Both done** (W27). The graph is now a per-package import list, checked
    against `go list`; `layout → controls` is the tab strip's geometry, and
    nothing imports `layout`. § Design principles names the two other
    `SetBounds` shapes: `(x, y)` for the five leaf inputs, `(x, y, w)` for
    `MenuBar` and `Toolbar`.
  - The false "mirrors exactly" comment in `sql_statement.go:55-57`.
  - `completion_relations_test.go:18` names the wrong narrowing function.

---

## Simplification and file splits

- **T41 — Test-only production code.**
  - `sqlparse.NarrowToDMLStatement` and `TokenizeRange` → move to `_test.go`,
    or label them as the oracle.
  - `TokenizeRangeFrom`'s always-`LexNormal` parameter → drop it.
  - **Done** (W23). `TokenizeRangeFrom` is gone, and `TokenizeRange` is what
    `PrefixCache` calls, so it is production code again. `NarrowToDMLStatement`
    stays where it is, labelled as the oracle: `internal/tui`'s completion
    tests call it too, and they can't see a `sqlparse` `_test.go`. `lexSQL`
    lost its `initial` parameter the same way.
- **T63 — `nameMap.Len`.** Only tests use it, so move it to `_test.go`.
  **Done** (W27): it lives in `name_set_test.go`.
- **gosmo generic by-name reader.**
  - About 60 `XByName` bodies are the same 8 lines, and about 25 sites
    hand-roll `errors.Is(err, sql.ErrNoRows)` instead of `foundRow`.
  - A `readByName[T]` saves about 400 lines. Not breaking.
  - **Done** (W27); see the step.
- **gossms dialog boilerplate.**
  - The five new-key/cert/spec/credential dialogs repeat the same
    `dbName`/`node`/`show`/refresh lines → embed a `dbFolderTarget`.
  - Activity Monitor's three feed starters (`activity_monitor.go:394-484`) →
    `startFeed[T]`.
  - The repeated menu and loader shapes (`agent_menu.go`, `alwayson_menu.go`,
    `explorer_programmability.go`, `explorer_service_broker.go`) → table-driven.
  - **All three done** (W27); see the step.
- **Optional:** an `App` dialog registry (`app.go:105-160, 504-571`). This
  stays inside `App`, since decisions.md closes package restructuring.
- **Plan capture twice.** gosmo `capturePlan` and gossms `query.runScript`
  each have their own SET SHOWPLAN handling → an exported gosmo helper over a
  `*sql.Conn`. **Done** (W27): `gosmo.StartPlanCapture`.
- **Activity Monitor DMV SQL is outside the version sweep.** About 15 raw
  queries in `internal/activity` never run under `TestLiveVersionSweep` on
  major 13 → move them to a gosmo monitor package, or add a gossms live sweep.
  **Done** (W27): moved into gosmo's root package (`server_activity.go`,
  `tempdb_usage.go`), where the sweep reaches them; see the step.
- **File splits** (a prompt to split, not a defect). Split along the
  existing section comments when a change lands. Each new `internal/tui` file
  needs its Package map row.
  - **Done** (W28); see the step.
  - gossms:
    - `detail_browser.go` (990) → `detail_browser_runs.go`
    - `activity_monitor.go` (930) → collectors and feeds
    - `prop_dialog.go` (919) → `prop_apply.go`
    - `explorer_databases.go` (900) → `explorer_databases_menu.go`
    - `propsheet/rows.go` (1024)
    - ~~`sqlparse/scope.go` (1063, after T54)~~ — done in W23
  - gosmo:
    - `backup.go` (1138) → backup, restore and backup_headers
    - `query_store_reports.go` (1046)
    - `index.go` (1014)
    - `connection.go` (1011)
    - `table.go` (985)
    - `scripter_table.go` (934)

---

## Decisions needed before implementing

1. **T13:** should script captures keep secrets? The proposal is to redact by
   default, with a `WithScriptSecrets` opt-in.
   **Decided** (W19): as proposed. Observers are always redacted, and
   gossms dropped its own substitution.
2. **T47:** go ahead with `Server.InTransaction`? It is the largest new gosmo
   surface.
   **Decided** (W21): yes, as drafted — observers buffered until COMMIT, no
   BEGIN TRANSACTION in a WithScript capture.
3. **T52:** drop `definition` from listings? It changes listing results for
   other gosmo users.
   **Decided** (W22): yes — the field goes, and each of the six types gains
   `Definition(ctx)`, read by name (see `docs/decisions.md`).
4. **T61:** F1 in Connect, Backup and Restore: remove it, or document it?
   **Decided** (W11): replaced by Tab reaching the button row.
5. **Activity Monitor SQL:** move it into gosmo, or add a gossms-side live
   sweep?
   **Decided** (W27): move it into gosmo.
