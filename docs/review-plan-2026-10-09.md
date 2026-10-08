# Review plan — 2026-10-09

A bug review of both repositories at gossms `4db4d25` / gosmo `c969a23`, the
day after the 2026-10-08 pass (whose `G*` items, e.g. G11, are already in the
code). This pass ran the mechanical checks across both trees, then read by
hand the code most likely to hold a defect earlier sweeps could not see:
behaviour that only shows with T-SQL-shaped input (sigils, non-ASCII names,
map-typed XEvent fields), the newest features (Replication Monitor, full-text
dialogs), and the shared plumbing every feature leans on. gosmo's backward
compatibility is waived for this plan, but nothing below needs a breaking
change; nothing in gosmo is removed because gossms doesn't call it.

**Item IDs are `L1…L9`**, local to this plan. `L` appears in no commit
message or document of either repository, so it collides with none of the
earlier plans' prefixes (`E`, `G`, `P`…`U`, `T`, `W`) nor with the permanent
`B*`/`V*`/`N*` series `docs/open-threads.md` owns.

**Evidence markers.** *Confirmed by repro* was reproduced with a standalone Go
program against the same standard-library calls the code makes. *Confirmed in
code* was traced in source with the failing input named. *Plausible* needs the
probe the item names. Nothing was driven live or in tmux; the Managed
Instance is down.

Like every earlier review plan this file is scratch: delete it once the last
step lands (`docs/decisions.md` § No top-level plan document); anything left
undone moves to `docs/open-threads.md`.

---

## What was checked and is clean

- **Build, format, static analysis.** `gofmt -l`, `go vet ./...` and
  `go vet -tags livedb ./...` are silent on both. `staticcheck` with every
  check but ST1000/1003/1020-1022: gosmo silent, gossms one ST1005 (L8).
  Extra analyzers run from `golang.org/x/tools` v0.51.0 — `nilness`,
  `unusedwrite`, `lostcancel`, `waitgroup` — silent on both; `shadow`'s 57
  hits were read and are all scoped re-declarations with no lost error.
- **Tests.** `go test -race -count=1 ./...` green on both.
- **Dead code.** `deadcode ./cmd/...` lists only what `docs/decisions.md` §
  By design keeps.
- **Vulnerabilities.** `govulncheck ./...`: none reachable.
- **Read by hand and found sound:** gosmo's retry/pool core (`retry.go`,
  `Server.query/queryRow/execScan`, `Database.query/queryRow/withConn`,
  `useBatch`/`recheckUse`, `bound`, `ReleaseIdleConnections`), quoting
  (`quoting.go`, `helpers.go`, `perDatabaseBatch`), script capture
  (`bindScriptArgs`, `redactSecrets`, `atomicBatch`), backup/restore
  planning, `ParseServerAddress`, index creation, the XEvent alter diff,
  replication-monitor scanning, `SpaceUsed`/`DiskUsage`. In gossms: the
  async primitives (`safego`, `fanOut`, `latest`, `postAndWake`/
  `wakeEventLoop`), the batch splitter and lexer, the query executor and
  session, config save/merge and `fileutil`, connection setup, the editor's
  undo stack, the CSV sink, Activity Monitor's delta maths, the Replication
  Monitor loader.

---

## Implementation order

Each step is one unit the user commits, gosmo first. At the end of every
step both repositories pass `gofmt -l`, `go vet ./...` (and `-tags livedb`),
`go test -race ./...`; gossms builds against the gosmo working tree; and the
step's live or tmux check has run (`docs/testing.md`). Mark an item **Done**
under its heading when its step lands.

- **S1 — gosmo.** L4 (`Schedule.Jobs` on a `ScheduleRef`), L6 (client-clock
  start date). `livedb` on 17, 14 and 13. **Done** (uncommitted in gosmo).
- **S2 — gossms editor search.** L1 (whole word), L2 (regex replace). tmux
  check in a query window. **Done** (uncommitted).
- **S3 — gossms XEvent viewer.** L3 (negated filters on map fields), L7
  (group filter vs grouping). tmux check on `system_health`. **Done**
  (uncommitted).
- **S4 — gossms query window.** L5 (database clobbered by a failed dial), L9
  (commit-failure wording on a lost session). tmux check.
- **S5 — lint.** L8.

No step depends on another.

---

## Bugs

### L1 — "Match whole word" never matches `@var`, `#temp`, `@@ROWCOUNT` or a word ending in a non-ASCII letter — gossms — *confirmed by repro* — **Done**

`Editor.SetSearch` (`internal/tuikit/controls/editor_search.go:79`) wraps the
pattern as `\b(?:pat)\b`. RE2's `\b` is an ASCII word boundary, so:

- a term that starts or ends with a non-word character needs a *word*
  character beside it to match: `@id` in `WHERE x = @id`, `#tmp` in
  `FROM #tmp t` — both find nothing (repro: `FindAllStringIndex` → `[]`);
- a term that starts or ends with a non-ASCII letter never has a boundary
  there: `café` in `SELECT café FROM t` finds nothing.

T-SQL variables and temp tables are exactly what a whole-word search is used
for, and Ctrl+F3 (`findWordAtCursor`, `find_replace_dialog.go:531`) always
searches whole-word on `WordAtCursor`, which takes Unicode letters
(`core.IsWordRune`) — so Ctrl+F3 on `Société` reports no match for a word
under the cursor.

- **Fix.** Stop wrapping in `\b`. Keep the plain (or user regexp) pattern and,
  when `WholeWord` is set, filter in `appendLineMatches`: reject a match whose
  rune before `start` or rune at `end` is `core.IsWordRune` — the editor's own
  word definition, so Find, Ctrl+F3 and word motion agree. A rejected match
  must not hide an overlapping valid one (`aa` in `aaa a`): on rejection,
  resume the search one rune after the rejected start instead of after its
  end (loop `FindStringIndex` over `text[pos:]` rather than `FindAll…`).
- **Pins.** `editor_search_test.go`: whole word finds `@id`, `#tmp`,
  `@@ROWCOUNT` beside spaces/operators/line ends; does not find `id` inside
  `identity`; finds `café`/`Société`; overlapping-candidate case; whole word +
  regexp. `find_replace_dialog_test.go`: Ctrl+F3 on a non-ASCII word selects
  the next occurrence.
- **Done (S2).** `SetSearch` no longer wraps in `\b`; `lineMatchLocs` filters
  on `isWholeWord` (`core.IsWordRune` on the runes outside the match), and
  `wholeWordLocs` resumes one rune past a rejected candidate's start. The tail
  search `text[pos:]` the plan proposed would satisfy `^`/`\A` at `pos`, so a
  pattern holding a begin anchor (`hasBeginAnchor`, a `regexp/syntax` walk)
  takes FindAll + filter instead — losing only overlap recovery, which `^`
  makes near-moot; `\b`/`\B` at `pos` are harmless (comment on
  `wholeWordLocs`). The rule is the plan's: a word rune *outside* either edge
  rejects, so `@abc` in `x@abc` is rejected while `abc` inside it is accepted.
  Pinned by `TestSearchWholeWordSigilsAndNonASCII`,
  `TestSearchWholeWordRejectionResumesInsideTheCandidate` and
  `TestFindWordAtCursorOnANonASCIIWord` (Ctrl+F3 selects the word under the
  caret, F3 the next). Mutations caught: resume at the candidate's end, no
  begin-anchor guard. tmux A/B: pre-fix binary "No matches for "@id"" on
  `x = @id AND y = @identity OR z = @id`; fixed binary Match 1 of 2 / 2 of 2
  (cols 11, 40), `@identity` skipped.

### L2 — Regexp Replace re-runs the pattern on the matched text alone, so context-dependent patterns replace wrongly or not at all — gossms — *confirmed by repro* — **Done**

`replaceMatch` (`editor_search.go:405`) does
`re.ReplaceAllString(old, repl)` where `old` is the matched substring cut out
of its line. Any assertion that looked at the surrounding text is now
evaluated at the substring's edges:

- `\Bing` matches `ing` in `running`; against `"ing"` alone `\B` fails, so
  `ReplaceAllString` returns `"ing"` unchanged — the Replace "succeeds" (the
  count goes up, an undo step is pushed) and nothing changes (repro).
- `\b`, `^`, `$` at a match edge behave the same way; with the L1 fix in
  place `WholeWord` no longer adds `\b`, but a user's own pattern can.
- A pattern that matches again *inside* `old` (a shorter alternative) is
  substituted twice.

- **Fix.** Expand the template against the match in its line:
  `re.FindAllStringSubmatchIndex(lineText, -1)`, pick the entry whose
  `[0],[1]` (byte offsets, mapped through `byteRuneIndex`) equal the match's
  rune bounds, then `re.ExpandString(nil, repl, lineText, loc)`. `ReplaceAll`
  rewrites a line right to left, so take `lineText` once per row before its
  first replacement and expand every match of that row against it. A literal
  (non-regexp) replace is unchanged.
- **Pins.** `\Bing`→`ed` in `running`; `(\w+)@(\w+)` with `$2.$1`; `^\s+`
  replace on an indented line; Replace All with groups over two matches on one
  line; a no-op regexp replace is reported as not replaced.
- **Done (S2).** `replacementFor` expands the template with `ExpandString`
  against the match located in its line's `FindStringSubmatchIndex` results
  (`submatchesOf`, built on the same `lineMatchLocs` as the scan, so a
  whole-word overlap match is found too). Replace All computes every
  replacement from the untouched rows before pushing undo and rewriting right
  to left; a span that is no longer a match of its line is skipped and not
  counted, and ReplaceCurrent returns false without an undo step. The draw-time
  scan still asks for bounds only (no submatches). "No-op reported as not
  replaced" is read as that stale-span case, pinned directly on
  `replacementFor` — no editor path produces one, since the scan is current.
  Pinned by `TestSearchRegexpReplaceUsesLineContext` (`\Bing`, `$2.$1`,
  `^\s+`, groups ×2 on one line, `ab$|ab` seeing the original tail,
  `aXa|a` not substituted twice), `…ReplaceCurrentUsesLineContext`,
  `…ReplaceOfANonMatchIsNotReplaced`; mutation (back to `ReplaceAllString` on
  the cut) caught. tmux A/B: pre-fix `\Bing`→`ed` on `running jumping`
  reported "Replaced 2 occurrence(s)" with the text unchanged; fixed gave
  `runned jumped`, one Ctrl+Z restored it.

### L3 — XEvent filter: `<>` and `not contains` on a map-typed field keep every event — gossms — *confirmed in code* — **Done**

`term.match` (`internal/xevent/filter.go:150`) compares a map field
(`wait_type`: key `66`, text `PAGEIOLATCH_SH`) against its text, and on
failure against its key, accepting if *either* comparison passes. For the
positive operators that is the intended "text or key" rule
(`TestFilterExpressions`). For the negated ones it inverts: `wait_type <>
PAGEIOLATCH_SH` fails on the text, then `66 <> PAGEIOLATCH_SH` passes, so the
event the user asked to exclude is shown; `wait_type not contains LATCH`
likewise. Ordering operators mix the two as well: `wait_type > 100` compares
the text `PAGEIOLATCH_SH` with `100` as text first, which passes for every
named wait.

- **Fix.** In `term.match`, for a value with a distinct key: for `OpNe` and
  `OpNotContains` require the comparison to pass against *both* text and key
  (De Morgan of the positive rule); for `<`, `<=`, `>`, `>=` compare the key
  when the filter value is a number and the text otherwise; `=`, `contains`,
  `starts with` keep "either".
- **Pins.** Add to `TestFilterExpressions`: `wait_type <> PAGEIOLATCH_SH`,
  `wait_type <> 66`, `wait_type !~ latch` all exclude `wait_info`;
  `wait_type <> CXPACKET` keeps it; `wait_type > 100` / `< 100` decided by
  the key.
- **Done (S3).** `term.match` as planned: a value without a distinct key
  compares once; with one, `<>`/`not contains` need both sides, ordering
  takes the key against a number (`parseNumber`) and the text otherwise,
  the rest keep "either". Pinned by eleven new `TestFilterExpressions` rows
  (`<>`/`!~` by text and by key exclude, `<> CXPACKET`/`!~ cxpacket` keep,
  `> 100`/`< 100`/`>= 66` by key, `> PAGEIOLATCH`/`< PAGEIOLATCH` by text);
  mutations (negation back to "either", ordering back to "either") caught.
  tmux A/B on win10cli's `system_health` ring_buffer, map field
  `connectivity_ring_buffer_recorded.type` (232 events: 230 `LoginTimers`
  key 2, 2 `ConnClose`): pre-fix `type <> LoginTimers`, `<> 2`, `!~ login`
  and `> 1` each matched all 232; fixed, the first three match the 2
  `ConnClose` and `> 1` the 230 `LoginTimers`.

### L4 — `Schedule.Jobs` on a `ScheduleRef` queries `schedule_id = 0` and answers "no jobs" — gosmo — *confirmed in code* — **Done**

`Schedule.Jobs` (`agent_schedule.go:350`) binds `sch.ID` directly. A
`ScheduleRef(name)` has ID 0, so the read silently returns an empty list —
the exact failure `~/go/gosmo/CLAUDE.md` § Conventions forbids ("Never query
with the zero id: it answers 'no children'"). Every sibling handle read is
guarded (`Alert.Notifications`, `Operator.NotifyingJobs`, … in
`TestRefChildReadsAreRefused`; `Job.Steps` looks its id up by name). gossms
is not affected today — `findAgentSchedule` reads by id — but any caller
holding a Ref is.

- **Fix.** Resolve the id the way `Job.id` does: when `sch.ID == 0`, look it
  up by name with `ScheduleByName`, which already returns `ErrAmbiguous` for
  a shared name and `ErrNotFound` for none (schedules are addressed by id —
  `~/go/gosmo/OPEN-THREADS.md` § Agent schedules — so a Ref resolving by name is only
  ever the fallback). Don't cache the id on the handle.
- **Pins.** A row in `handle_not_loaded_test.go` (shape of
  `TestJobRefReadsLookUpTheJobID`): the recorded SQL binds the looked-up id;
  a shared name is `ErrAmbiguous`. Extend the live Agent schedule test with
  `ScheduleRef(name).Jobs` on 17.
- **Done (S1).** `Schedule.id` resolves a zero ID through `ScheduleByName`
  (uncached, its `ErrNotFound`/`ErrAmbiguous` returned as is); `Jobs` uses it.
  Pinned by `TestScheduleRefJobsLooksUpTheScheduleID` and in
  `TestLiveSharedScheduleNames` (shared name ambiguous, unique name and
  handle read the attached job, missing name not found); a line added to
  gosmo `ARCHITECTURE.md` § Shared schedules. Live green on 17, 14 and 13.

### L5 — A failed or cancelled query-window connect leaves the window pointing at the attempt's database — gossms — *confirmed in code*

`dialQueryPanel` (`internal/tui/app_connections.go:200`) sets `qp.database =
opts.Database` before dialling and only overwrites it on success. On failure,
cancel, or a panel closed meanwhile, the panel keeps its previous (closed)
`qp.conn` with the *attempt's* database: the info bar
(`query_panel_draw.go:68`) shows it, IntelliSense keys its inventory by it
(`completion_provider.go:120`), Reconnect redials the old server in it
(`query_panel_exec.go:81`), and the next Connect prompt pre-fills the old
server with it (`ShowForQueryPanel`, `connect_dialog.go:553`). The window
reaches this path only when disconnected (`runRefused`), so the damage is a
wrong pairing rather than a wrong run — but Reconnect into a database that
does not exist on that server fails with a confusing error.

- **Fix.** Don't write `qp.database` before the dial; the success branch
  already sets it from `state.Database`. If the info bar should show the
  target while connecting, render it from `qp.connectingTo` plus a new
  `connectingDB`, cleared with it.
- **Pins.** `app_connections` test with a failing dial: `qp.database` is
  unchanged after the callback; same for cancel (`done` → false) and a panel
  closed during the dial.

### L6 — A schedule created with no start date starts on the *client's* today — gosmo — *confirmed live* — **Done**

`CreateScheduleRequest.frequencyArgs` (`agent_schedule.go:226`) fills a zero
`ActiveStartDate` with `timeToYYYYMMDD(time.Now())`, the client's local date;
`Job.AddSchedule` shares it. A client east of the server shortly after its
midnight writes tomorrow's date for the server, and a schedule meant to run
today first runs tomorrow; west of it, yesterday. gossms always passes the
date its form shows (SSMS's own client-date default), so only library callers
leaving it zero are affected.

- **Probe first.** On 17 and 13: `sp_add_schedule` and `sp_add_jobschedule`
  with `@active_start_date` omitted store the *server's* date (documented
  default NULL → today).
- **Fix.** Omit `@active_start_date` when the field is zero, in both
  procedures; update the field's doc comment ("defaults to the server's
  today").
- **Pins.** A unit test on `frequencyArgs`' text for a zero date; the live
  schedule test reads `active_start_date` back and compares it with
  `CAST(GETDATE() AS date)`.
- **Done (S1).** Probe confirmed on 17, 14 and 13: both procedures with
  `@active_start_date` omitted store the server's date. `frequencyArgs` now
  omits it for a zero date; the field's doc says "the server's today".
  Pinned by `TestFrequencyArgsOmitsAZeroStartDate`, and the live
  `TestLiveSharedScheduleNames` (`CreateSchedule`) and
  `TestLiveAddScheduleSharedName` (`AddSchedule`, both branches) compare the
  read-back date with the server's `GETDATE()`. Live green on 17, 14 and 13.

### L7 — XEvent "filter to this group" can match events of a sibling group — gossms — *confirmed in code* — **Done**

`groupLevel` (`internal/xevent/group.go:122`) groups by the exact displayed
value, so `App` and `app` (or `1` and `1.0`) are two groups; `Group.Terms`
turns a group into `column = value`, and `compare` is case-insensitive and
numeric — the filter for group `App` also shows `app`'s events, and its count
no longer matches the group's.

- **Fix.** Make the two agree on one equality: group by the same key
  `compare` uses for `=` (numeric value when it parses, else the lowered
  text), showing the first value seen as the group's label. That also merges
  groups SQL Server's own case-insensitive comparison would treat as one.
- **Pins.** `group_test.go`: events with `App`/`app` form one group, and
  `Terms` over it matches exactly the group's events; `1`/`1.0` likewise.
- **Done (S3).** `groupLevel` keys groups by `equalityKey`: `n` + the
  `parseNumber` value formatted (`-0` folded to `0`), else `t` + the lowered
  text; `Group.Value` is the first spelling seen, `Group.Key` (the expanded
  set's key, in memory only) uses the equality key so expansion survives a
  different spelling arriving first. Pinned by
  `TestGroupsMergeWhatTheFilterFindsEqual` (`App`/`app`/`APP`,
  `master`/`Master`, `1`/`1.0`/` 1`, `-0`/`0`; every group's `Terms` match
  exactly its events); mutation (key back to the raw text) caught. tmux A/B
  with a disposable `user_event` session on win10cli fed `App`, `app`, `APP`,
  `master`, `x` by `sp_trace_generateevent`: pre-fix, grouping by
  `user_info` gave `App`/`app`/`APP` one event each and Filter by This Group
  on `App` showed 3 events in 3 groups; fixed, one `App (3 events)` group
  whose filter shows exactly those 3. Session dropped. Not changed: `compare`
  finds `NaN` equal to every number (neither `<` nor `>`), so `x = 5` matches
  a `NaN` value — not seen in XEvent data, left as is.

### L8 — `staticcheck` ST1005 in `new_fulltext_copy_source.go:106` — gossms — *confirmed*

The one finding that breaks "staticcheck is silent". The text is a
user-facing sentence shown through `accessDeniedLabel`. Either end it without
the period and add it where it is displayed, or keep the sentence and add
`//lint:ignore ST1005 shown to the user as a sentence` — the second matches
how the rest of the package words such errors; check `displayError`'s other
callers before choosing.

### L9 — After a COMMIT that failed because the session died, the alert says the transaction is still there — gossms — *confirmed in code*

`QueryPanel.endTransactions` (`query_panel_exec.go:147`): on a failed commit
it keeps the window open and says "the transaction is still there to deal
with". When the failure was the connection dropping, `EndTransactions` has
already marked the session lost — the server rolled the transaction back —
so the message is wrong and the user is invited to retry a commit that can
no longer happen (the next run then reports the lost session).

- **Fix.** Check `sess.Lost()` in the callback: if lost, run the
  `noteSessionState` path (close the connection, the standard lost-session
  message) and say the transaction was rolled back by the server; keep the
  current alert only for a commit refused on a live session.
- **Pins.** A scripted-driver test where `EndTransactions` fails and the
  connection reports invalid: the alert wording and that the panel shows as
  disconnected.

---

## Not raised (checked, by design)

So nobody re-checks them: editor tab expansion, the uncapped redo stack,
`fileutil.WithLock`'s stale-lock race, retryable `mssql.ServerError`, grid
copy writing raw tabs/newlines (SSMS parity), English-only parsing in
`permission_error.go` (gosmo's own numbers drive gating), and the
`fmt.Errorf` refusals on `invalid_request_test.go`'s allowlist
(`Login.UnmapFromDatabase` among them) — all settled in `docs/decisions.md`
or the allowlist itself.
