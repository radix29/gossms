# Review plan — 2026-11-17 (gossms, gosmo, tuikit)

Scope: all three repos at `fix/review_20261117` (gossms), gosmo `v0.3.0` +
HEAD, tuikit `v0.0.3` + 1. Breaking gosmo/tuikit API changes are allowed for
this plan (author's waiver, this review only). Plan only: nothing below is
implemented. Delete this file when its items are done or moved to
`docs/open-threads.md`.

## Baseline (what is already clean)

Ran before looking for anything by hand, so the items below are what tooling
does not catch:

- `go build`, `go vet` (also `GOOS=windows`/`darwin`, also `-tags livedb`),
  `staticcheck` (also `-tags livedb`), `govulncheck`: clean in all three.
- `go test ./...` green in all three; `go test -race` green for tuikit and
  gossms `internal/tui`, `internal/query`.
- `staticcheck -checks all` (minus doc-style checks): one hit, see Q1.
- `deadcode -test`: clean in gossms; gosmo/tuikit hits are exported library
  surface, kept deliberately (`CLAUDE.md` § Changing gosmo or tuikit).
- `go mod tidy -diff`: only the gosmo/tuikit `go.sum` lines a release build
  needs when the `replace` lines are commented out — not a finding.

Checked by hand and found sound (don't re-review without a new reason):
`latest`, `safego`/`postAndWake` usage (no stray goroutines, every
`context.Background()` is documented), `query.Session` lifetime and
`EndTransactions`, gosmo `withRetry`/`AcquireConn`/`bindScriptArgs`,
`config` snapshot/merge/trim, CSV/XML export quoting, the XEvent filter
lexer vs `Quote`, `datetime` display (go-mssqldb already rounds to whole ms),
completion item ordering, tuikit `strutil`, `DataGrid.drawRow`, editor
undo/redo bookkeeping, the two `isHexLiteral`s (different on purpose: an LCID
needs a digit, a binary parameter may be an empty `0x`).

## Findings

IDs are this plan's only. Severity: **H** wrong behaviour a user can hit,
**M** inconsistency or guardrail gap, **L** cleanup/optimisation.

### Command-line safety classifier (gossms `internal/cli/safety`)

Found by running `Classify` over ~110 adversarial statements. Each is a
guardrail gap (`--allow` lets through more than its class says), not a
security boundary (the login's permissions are), but `docs/cli.md` §
Safety promises these classes.

- **C1 (M) — `sp_rename` is `write`, not `ddl`.** `EXEC sp_rename
  'dbo.t','t2'` → `write` ("not on the read allow-list"), so `--allow write`
  renames tables, columns and indexes. Same class problem, to be probed one
  by one: `sp_refreshview`, `sp_refreshsqlmodule`, `sp_recompile`,
  `sp_addextendedproperty`/`sp_updateextendedproperty`/
  `sp_dropextendedproperty`, `sp_bindrule`/`sp_unbindrule`,
  `sp_bindefault`/`sp_unbindefault`, `sp_settriggerorder`, `sp_tableoption`,
  `sp_changeobjectowner`, `sp_addtype`/`sp_droptype`.
  **Fix:** add them to `ddlProcs` (`tables.go`), widen its doc comment
  ("changes statistics" reason in `execTarget` becomes per-procedure or
  generic "changes the schema"), add rows to the classifier test table, update
  `docs/cli.md`'s class table (`ddl` row).
- **C2 (M) — an msdb procedure's class depends on how it is qualified.**
  `EXEC msdb.dbo.sp_send_dbmail …` → `admin`; `EXEC sp_send_dbmail …` →
  `write`. After `USE msdb` (classed `read`) the unqualified call is msdb's
  own procedure, so `USE msdb; EXEC sp_send_dbmail …` sends mail under
  `--allow write`. `adminProcs` lists some Agent procedures by bare name
  (`sp_add_job`, `sp_start_job`, …) so they are already admin either way;
  everything else in msdb (`sp_send_dbmail`, `sp_purge_jobhistory`,
  `sp_delete_backuphistory`, `sp_update_alert`, `sp_add_operator`,
  `sysmail_*`, …) is not.
  **Fix (fail closed):** an `sp_`/`sysmail_` name that is an msdb procedure
  is `admin` whatever its qualification. Either a bare-name set of msdb
  procedures in `tables.go` (generated once from `msdb.sys.procedures` on 17,
  checked into the table with the version noted), or track `USE` within the
  script and treat "current database is msdb" like `dbName == "msdb"`. The
  set is simpler and does not depend on the connection's default database.
- **C3 (M) — `RAISERROR … WITH LOG` is `read`.** It writes the SQL Server
  error log and the Windows application log, needs `ALTER TRACE` or
  sysadmin, and at severity 20+ ends the connection — while `xp_logevent`,
  which does the same write, is `admin`.
  **Fix:** in `leaderRule`, raise RAISERROR to `admin` when a `LOG` option
  follows `WITH` at depth 0; plain RAISERROR stays `read`. `docs/cli.md`'s
  `read` row lists RAISERROR — add "(not `WITH LOG`)".

### gosmo API consistency (breaking allowed)

- **G1 (M) — optional singletons answer `(nil, nil)` for "none".**
  `Database.MasterKey`, `Server.DatabaseMirroringEndpoint`,
  `Endpoint.MirroringDetail`, `Endpoint.ServiceBrokerDetail`. Every by-name
  read returns `ErrNotFound` for absence (`docs/decisions.md` § gosmo:
  deliberate API decisions), so a caller forgetting the nil check
  dereferences nil instead of reading a typed error.
  **Fix:** return `notFoundf(...)` (`ErrNotFound`) from all four; keep the
  doc comments' visibility caveats. Update the gossms callers (8 sites:
  `grep -rn "MasterKey(ctx\|DatabaseMirroringEndpoint(ctx\|MirroringDetail(ctx\|ServiceBrokerDetail(ctx" internal`)
  to `errors.Is(err, gosmo.ErrNotFound)`, and `CreateMasterKey`'s `m == nil`
  branch. `go vet -tags livedb` and the live tests that read these.
- **G2 (L) — the master-key surface is split across files, and the
  "ensure" step lives in gossms.** `HasMasterKey` and `CreateMasterKey` are in
  gosmo's `certificate.go`, everything else in `master_key.go`; gossms
  carries `ensureMasterKey` (`internal/tui/master_key.go`), which every other
  gosmo user would rewrite.
  **Fix:** move both functions to `master_key.go` (exact line-range move);
  add `Database.EnsureMasterKey(ctx, password) (created bool, err error)`
  with the Script Changes semantics `ensureMasterKey` documents (read is
  real, CREATE is captured), an offline statement test and a
  `secret_redaction_test.go` row; delete gossms's helper. Also fold
  `MasterKey`'s second round trip (`is_master_key_encrypted_by_server`) into
  the `symmetricKeys` read as a subquery column.
- **G3 (L) — `withRetry` drops the last transient error on cancel.** A
  cancel during the back-off returns bare `ctx.Err()`; the error that caused
  the retry (the useful part when a timeout ends a flapping read) is lost.
  **Fix:** `return zero, errors.Join(ctx.Err(), withAllMessages(err))` —
  `errors.Is(err, context.Canceled)` still holds. Check
  `TestNoWrapOfAnAlreadyPrefixedError` and the retry tests.

### tuikit

- **T1 (L) — `dialogs.Stack` can only hold dialogs registered up front.**
  `Register` + `Sync` (poll every registered dialog's `Visible()` each tick)
  is why gossms constructs all its dialogs at startup (A1).
  **Fix:** add `Stack.Open(d Dialog)` (push if not already open) and let
  `Sync` drop closed ones as now; `Register` stays for hosts that prefer it.
  Ordering becomes show order, which is what the tie-break approximates.
- **T2 (L) — `ModalDialog` keeps the `tcell.Screen` only to call `Size()`.**
  Every `NewXDialog(s tcell.Screen)` and `NewPropertySheet(s, …)` threads a
  screen through construction for `clampToScreen`/`Relayout`; `Draw` already
  receives one.
  **Fix (breaking):** `Relayout(w, h int)` (the host knows the size), or a
  `ScreenSize func() (int, int)` set once on the `Stack`; constructors drop
  the screen. ~25 construction sites in gossms. Do after T1, same tuikit
  change set, only if A1 goes ahead — on its own it is churn for little.

### gossms architecture

- **A1 (L) — `App` holds 69 dialog singletons, built eagerly.** 69
  `registerDialog` calls in `NewApp` and 69 fields in `App` (out of 108);
  each dialog's only caller is its `showX` function.
  **Fix (needs T1):** a generic lazy slot — `func dialogOf[T Dialog](a *App,
  slot *T, mk func(*App) T) T` that builds on first use and `Stack.Open`s on
  show — then collapse the fields into one `dialogs struct{…}` or drop them
  where the dialog holds no state between showings. Mechanical, file by file;
  `TestPackageMapListsEveryTUIFile` unaffected. Four tests enumerate dialogs
  through `a.modals.Registered()` after `buildUI` —
  `dialog_registration_test.go` (every dialog field registered),
  `clipboard_host_test.go`, `clipboard_token_test.go`,
  `dialog_gesture_test.go` — so they need a constructor list to walk
  instead, and the registration test's purpose (no dialog left out of the
  stack) becomes "every lazy slot opens through `Stack.Open`".
- **A2 (L) — Format Document cannot be cancelled.** `format_document.go`
  discards the `latest` context (`_, token := qp.formatRun.Begin(…)`) because
  `internal/format` takes none, so a superseded multi-MB format runs to the
  end. **Fix:** `ctx` first on the formatters' entry points, checked per
  statement/line; pass the `latest` context.
- **A3 (L) — two T-SQL lexers, unguarded.** `docs/decisions.md` § T-SQL
  lexing settles "one lexer" inside gossms, but gosmo's `tsql.Spans` is a
  second, and `internal/cli/safety` uses both (`sqltext` to lex, `tsql` to
  unquote). A differential run over 28 edge cases (nested comments,
  unterminated literal/bracket/comment, doubled closers, `--` in brackets,
  `/*` in literals, non-ASCII) agreed everywhere except the `N` of `N'…'`
  (in the span for `tsql`, outside for `sqltext` — representational).
  **Fix:** a `FuzzLexersAgree` in `internal/sqltext` comparing their
  comment/literal/identifier byte ranges (normalising the `N` prefix). Not
  a merge: the editor needs `sqltext`'s line-resumable state.

### Docs and hygiene

- **D1 (L) — stale tuikit version note.** `CLAUDE.md` § What this is says
  tuikit "has none yet: its `require` is the `v0.0.0-…` placeholder until its
  first tag"; `.claude/skills/dev-with-local-gosmo/SKILL.md` repeats it.
  tuikit is tagged `v0.0.3` and `go.mod` requires it. **Fix:** reword both to
  "released like gosmo: comment out the `replace` against pushed tags of both".
- **Q1 (L) — ST1023** at `internal/tui/agent_operator_props.go:126`
  (redundant `propsheet.Row` in a `var` with an initializer).

### Verification gap

- **W1 (L) — Win32 clipboard opens with a NULL owner.**
  `os_clipboard_windows.go` calls `OpenClipboard(0)` then `EmptyClipboard`;
  the Win32 docs say that leaves the owner NULL and makes `SetClipboardData`
  fail. It works in practice (the common Go clipboard libraries do the same),
  but it has not been tried with Windows clipboard history (Win+V) on, over
  RDP, or with a clipboard manager running. **Fix:** drive those three on
  win10cli; only if one fails, open with a message-only window
  (`CreateWindowExW` with `HWND_MESSAGE`) as owner. Add to
  `docs/open-threads.md` if it cannot be driven.

## Recommended implementation order

Cheap and independent first; the guardrail before API work; library changes
in one gosmo change set and one tuikit change set, each built and tested in
the library before gossms.

1. **D1, Q1** — doc drift and the staticcheck nit (minutes, no risk).
2. **C1, C2, C3** — classifier tables and `leaderRule`, with test rows and
   the `docs/cli.md` class table in the same change. Highest user-facing
   value: they are guardrail promises.
3. **G1 + G2 + G3** — one gosmo change set (`go test`, `go vet -tags
   livedb`, `staticcheck`, then the live tests for master key, mirroring
   endpoint and Service Broker endpoint on 13 and 17), then the gossms
   callers. Drive New Certificate/New Asymmetric Key with and without an
   existing master key, and Script Changes for both, in the tmux harness.
4. **A3** — the differential fuzz test (guards later lexer edits; no
   behaviour change).
5. **A2** — context through `internal/format`; drive Format Document on a
   large file and type during the format.
6. **W1** — Windows clipboard drive on win10cli (needs an interactive
   session there).
7. **T1 → A1** — `Stack.Open` in tuikit, then lazy dialogs in gossms. Optional:
   a structural cleanup with no user-visible change; do it when touching
   `app.go` anyway.
8. **T2** — only together with A1, else skip.
