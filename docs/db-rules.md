# Database and permission rules

Rules for the code between the UI and gosmo — permission gating, the T-SQL a
page emits, Object Explorer filters, and query execution. Each is a bug that
shipped.

## Permission gating

- **Every Properties page that can write declares the rights its writes need, and
  the rights are the *page's*, not the dialog's.** `withRequires` at the
  `[]propPage` constructor, or `withRequiresOn` when any right is schema- or
  object-scoped — an object-scoped right asked without a securable answers for
  nobody, so a page on `objectWriteRights()` wrapped in plain `withRequires`
  compiles, looks right, and shows a read-only banner to the one principal who
  could actually write it. The securable for an index, a statistic or a key is the
  **table**: that is what SQL Server checks and what gosmo's probe records.
  Login General takes ALTER ANY LOGIN, Login Server Roles ALTER ANY SERVER ROLE
  and Login Securables CONTROL SERVER — one list per dialog would have been wrong
  on two of the three. `prop_page_requires_test.go` fails on a page that declares
  nothing (unless named in `pagesThatOnlyRead`, only for pages with no `apply`),
  on a stale exemption, on an object-scoped page with no securable, and on a new
  `[]propPage` constructor absent from its list.
  **The banner's check and the menus' gate are one function, `rightsAllow`
  (`permission_gate.go`) — never add a second copy.** They had diverged: the
  banner's half knew only server and database scope, so it showed a false
  read-only for every object-scoped right and never fired at all for SQL Agent's
  msdb memberships, which fail open when read as server permissions. The callers
  differ in one thing only — how a database's capabilities are reached, cached for
  the UI goroutine, probing for a page's `load`.
  **Inside it, the object-scope DENY is asked first and separately.** Every right
  in a set is an alternative that can only *add* permission, but SQL Server
  resolves a DENY on the object over all of them, so `objectDenial` runs before
  the loop rather than as another case in it — and it exempts sysadmin, because
  the probe reads permissions through `public` and a DENY to public is recorded
  for the one login the server never applies it to. **A DENY on one *column* is
  asked right after, through gosmo's `DeniedOnAnyColumn`**, and it is a separate
  question because gosmo keeps column rows in their own map: every gated action
  touches the whole object, so one denied column withholds it, but recording the
  row on the table would make it a denial of every column.

## T-SQL and filters

- **Never give a procedure you install outside `master` an `sp_` prefix.** An
  `sp_` name falls back to `master` when the current database has no such
  procedure, corrupting DDL on the very path that installs one: `CREATE OR ALTER
  dbo.sp_x` in tempdb finds master's copy and fails "Invalid object name", and
  `DROP PROCEDURE IF EXISTS dbo.sp_x` in tempdb **deletes master's copy** — it did,
  live. `internal/activity/block.go` is the worked example: master's copy is
  `sp_block`, tempdb's is `usp_block`, and every `EXEC` names its database.
- **A folder's Object Explorer filter has to be applied twice — once for the tree,
  once for the Detail Browser.** The tree's half is `filterChildren` in
  `fetchChildren`; the pane's loaders (`detail_browser_*.go`) query gosmo
  independently and hold gosmo objects, so they use `filterObjects` on the
  collection *before* rows are built — a progressive loader backfills by index, so
  filtering rows afterwards writes each count and size into the wrong row. A loader
  that skips this leaves the pane listing objects the tree has filtered away.
  **Both halves also push the filter into gosmo's `...FilteredContext` listing — that
  push-down is an optimisation, never the meaning of the filter.**
  `filterChildren`/`filterObjects` still run over whatever comes back and stay the
  authority, so `nodeFilter.pushdown` must either reproduce their comparison exactly
  (values trimmed as `matchText` trims them, whole calendar days as `matchDate`
  compares them) or refuse the filter, which `serverFilter` turns into "read the
  whole folder". Two rules in gosmo's clause builder are the ones a plausible
  simplification removes: `LOWER(col) LIKE LOWER(@p)`, because a bare LIKE follows
  the database collation and drops rows on a case-sensitive one; and `likeEscape`
  plus `ESCAPE`, because `%`, `_` and `[` are legal in an identifier — unescaped, a
  filter for `pct_1` also matches `pct1100`.

## Query execution

- **Never call `rows.Next()` speculatively inside `internal/query/executor.go`'s
  `sqlexp.ReturnMessage` loop.** One extra `Next()` on an exhausted result set makes
  the driver consume the protocol message `retmsg.Message(ctx)` is waiting for: the
  grid comes up empty, with no error and no Messages tab. Gate any drain on having
  actually abandoned the set mid-scan (`scanNext` returns a bool for exactly this).
  Unit tests do not catch it; only a live query does.

