# Database and permission rules

Rules for the code between the UI and gosmo — permission gating, emitted T-SQL,
Object Explorer filters, query execution. Each is a bug that shipped.

## Permission gating

- **Every writable Properties page declares the rights its writes need — per
  page, not per dialog.** `withRequires` on the `[]propPage` constructor, or
  `withRequiresOn` when any right is schema- or object-scoped: an object right
  asked without a securable answers for nobody, so `gate.ObjectWriteRights()`
  under plain `withRequires` shows read-only to the one principal who could
  write. The securable for an index, statistic or key is the **table**. (Login
  General = ALTER ANY LOGIN, Server Roles = ALTER ANY SERVER ROLE, Securables =
  CONTROL SERVER — one list per dialog is wrong on two of three.)
  `prop_page_requires_test.go` fails on a page declaring nothing (unless in
  `pagesThatOnlyRead`, for pages with no `apply`), a stale exemption, an
  object-scoped page with no securable, and an unlisted `[]propPage`
  constructor.
- **The banner's check and the menus' gate are one function,
  `gate.RightsAllow` (`internal/tui/gate`) — never a second copy.** The copies
  diverged: the banner knew only server/database scope, so object rights
  showed a false read-only and msdb Agent memberships never fired. Callers
  differ only in how database capabilities are reached (cached on the UI
  goroutine; probing in a page's `load`).
- **Object-scope DENY is asked first and separately** (`gate.ObjectDenial`,
  before the loop): rights in a set are alternatives that only add, but a DENY
  on the object beats all of them. It exempts sysadmin — the probe reads
  through `public`, and a DENY to public never applies to sysadmin. **A DENY on
  one column is asked next**, via gosmo's `DeniedOnAnyColumn` (column rows are
  a separate map): every gated action touches the whole object, so one denied
  column withholds it.
- **A right set is what the server checks, not the family's usual shape.**
  Don't add `rightAlterDatabase` "for symmetry": database-scoped credentials
  work under `GRANT CONTROL ON DATABASE` and are refused under `GRANT ALTER`,
  so `dbScopedCredentialRights` is `CONTROL` alone. Probe with a `WITHOUT LOGIN`
  user before writing a set. A *server*-scope permission asked of a database
  via `HAS_PERMS_BY_NAME` returns **NULL, not 0** (`ALTER ANY CREDENTIAL`),
  which gates as `CapabilityUnknown` forever.
- **A schemaless node never falls to `gate.ObjectWriteRights()`** — only ALTER
  ANY SCHEMA is left, which permits no schemaless DROP. Ten families shipped
  so; `TestSchemalessDatabaseOpsAreGated` refuses an eleventh.
- **Fixed-role contents vary by version**: db_ddladmin has ALTER ANY EXTERNAL
  DATA SOURCE / FILE FORMAT on major 17, not 13/14. A right's `role` must
  confer it on every supported major; one version's probe doesn't settle it.
- **ALTER and DROP can take different rights — then they get different
  entries.** A Service Broker queue (majors 13, 14, 17): `ALTER ON
  OBJECT::<queue>` alters but can't drop (Msg 15151; drop needs CONTROL on the
  queue or ALTER on its schema), so `queueAlterRights` gates Properties and
  `queueDropRights` Delete; Move to Schema is a third set
  (`classOneTransferRights`). Probe per verb, not per family.
- **`rightAlterOnObject` means ALTER *or* CONTROL; `rightControlOnObject` means
  CONTROL** (object owner included). gosmo's object block matches `CONTROL`
  alongside the named permission, so the `O:ALTER` map reads 1 for either —
  right for rename/drop, wrong for `ALTER SCHEMA ... TRANSFER`. Gate transfers
  and anything else needing CONTROL on `rightControlOnObject`.
- **Probing trap: a transfer or ownership change deletes the object's explicit
  permissions.** A script that grants CONTROL to several users then transfers
  once finds every later grantee refused, which looks like "CONTROL is not
  enough" (a wrong answer on 2026-09-16). Recreate or re-grant between cases.
- **Move to Schema is not the Rename/Delete set.** `TRANSFER` needs CONTROL on
  the securable; ALTER on the database, db_ddladmin, ALTER ANY SCHEMA and
  ALTER on the source schema all permit drop and are refused the move (Msg
  15151; 13, 14, 17). Two sets, one per class: types and XML schema
  collections (class 6/10) use `securableTransferRights`; every other
  transferable family — table, view, procedure, function, sequence, synonym,
  rule, default, queue — is a class-1 `sys.objects` row sharing
  `classOneTransferRights`. **A DENY of ALTER does not withhold the move**
  (CONTROL granted + ALTER denied reads `O:ALTER` 0, `O:CONTROL` 1 and
  transfers; probed 2026-09-17), a DENY of CONTROL does. ALTER-denial tests
  exempt Move to Schema, pointing at
  `TestTheClassOneTransferGateMatchesWhatTheServerAllowed`, which holds the
  live table.
- **"Edition doesn't implement this" is separate from "login may not" —
  `edition_gate.go`.** Same shape (disabled item + note), composed outside the
  permission gate: `gateAzure(gate(item, …), sc)`, edition's note winning. No
  third mechanism. A page-level refusal is a read-only row plus a
  `propsheet.Note`, not a blocked dialog (Azure `CREATE DATABASE` works without
  file/filegroup clauses). The note names the engine edition, never "Azure".
- **Gate an edition refusal only when it's compile-time.** `CREATE REMOTE
  SERVICE BINDING` on MI (Msg 41906) aborts the whole batch before anything
  runs, hence `azureRefusedScriptVerbs`. `CREATE`/`ALTER ROUTE` with
  `ADDRESS = 'TRANSPORT'` or a same-instance `MIRROR_ADDRESS` (Msg 41943) is
  runtime — earlier statements have run, and the server's own message is what a
  gate would say anyway. Gate it only if something else in the batch must
  survive. (Both probed on `t-qmi-01`, 2026-09-16/17.)

## T-SQL and filters

- **A typed secret never reaches Script Changes in clear.** Every password or
  secret goes through `scriptSafePassword`/`scriptSafeSecret`
  (`new_object_dialog.go`): the value on a real Apply, `<insert password
  here>`/`<insert secret here>` under `gosmo.WithScript` (matching gosmo's
  Script as).
- **Never give a procedure installed outside `master` an `sp_` prefix** — `sp_`
  falls back to `master`: `CREATE OR ALTER dbo.sp_x` in tempdb fails "Invalid
  object name", and `DROP PROCEDURE IF EXISTS dbo.sp_x` in tempdb **deleted
  master's copy**, live. `internal/activity/block.go`: `sp_block` in master,
  `usp_block` in tempdb, every `EXEC` names its database.
- **An Object Explorer filter applies twice — tree and Detail Browser.** Tree:
  `filterChildren` in `fetchChildren`. Pane loaders (`detail_browser_*.go`) use
  `filterObjects` on the gosmo collection *before* building rows — a
  progressive loader backfills by index, so filtering rows later misaligns
  counts and sizes. **Both also push the filter into gosmo's
  `...FilteredContext` listing — an optimisation, never the meaning.**
  `filterChildren`/`filterObjects` stay authoritative, so `nodeFilter.pushdown`
  either reproduces their comparison exactly (trimmed like `matchText`, whole
  days like `matchDate`) or refuses (`serverFilter` then reads the whole
  folder). gosmo's clause builder keeps `LOWER(col) LIKE LOWER(@p)` (bare LIKE
  follows a case-sensitive collation) and `likeEscape` + `ESCAPE` (`%`, `_`,
  `[` are legal in identifiers — `pct_1` would match `pct1100`).
- **`sys.database_audit_specification_details.class_desc` isn't the ADD
  keyword.** Object rows say `OBJECT_OR_COLUMN`, which `ADD (SELECT ON
  OBJECT_OR_COLUMN::x BY y)` rejects and a `WHEN 'OBJECT'` arm never matches.
  gosmo translates out (`database_audit_specification.go`) and accepts it as
  `OBJECT` in. Found live on major 17 with unit tests green.

## Query execution

- **Never call `rows.Next()` speculatively in `internal/query/executor.go`'s
  `sqlexp.ReturnMessage` loop** — one extra `Next()` on an exhausted set
  consumes the message `retmsg.Message(ctx)` waits for: empty grid, no error,
  no Messages tab. Drain only after abandoning a set mid-scan (`scanNext`'s
  bool). Only a live query catches it.
- **A query window's SQL runs on its `query.Session`, never a pool.** A pooled
  `*sql.Conn` is reset on next checkout (temp tables, SET options, open
  transaction gone) and the pool reuses the latest-returned connection — the
  user's own (BUG-1: `#t` vanished between F5s, `COMMIT` failed Msg 3902).
  Ending a session discards its connection (`Session.Close`), never pools it.
