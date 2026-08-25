# Least-privilege plan — behaving correctly for users who aren't sysadmin

Status: **complete — P0 to P4 shipped 2026-08-25.** F1 to F6 below, the
`README.md` § Required rights section, the capability probe of §3.1, all four
presentation rules of §3.2, the error→right mapping of §3.3 and the
silent-empty guard of §3.4. What was deliberately left ungated, and the two
places the mapping cannot reach, are listed in `docs/open-threads.md`
§ Permission gating.

Written 2026-08-25 from a live audit against `win10cli` (SQL Server 17.0.1125.2,
Windows) and `ubusql1` (SQL Server 17.0.4065.4, Ubuntu) using the two supplied
test logins plus one disposable one, since created and dropped.

---

## 1. What the audit actually measured

Measured rights, not assumed ones — `HAS_PERMS_BY_NAME` / `IS_SRVROLEMEMBER` /
`IS_ROLEMEMBER` on both servers:

| Login | Server | sysadmin | VIEW SERVER STATE | VIEW ANY DEFINITION | Database role | msdb |
|---|---|---|---|---|---|---|
| `user_dbo` | win10cli | no | **no** | no | `db_owner` on HealthClinic | guest only |
| `user_dbo` | ubusql1  | no | **no** | no | `db_owner` on HealthClinic | guest only |
| `user_dr`  | win10cli | no | **yes** | no | `db_datareader` on HealthClinic | guest only |
| `user_dr`  | ubusql1  | no | **no** | no | `db_datareader` on HealthClinic | guest only |

Two corrections to the brief worth recording:

- **`user_dr` has `VIEW SERVER STATE` on win10cli only.** On `ubusql1` it does
  not, so the two servers are not the mirror pair they were described as. The
  ubusql1 grant is what would need adding to reproduce the intended matrix.
- Neither login can see any database but `HealthClinic`, `master`, `msdb` and
  `tempdb` (`HAS_DBACCESS` = 0 for `backup_test`, `model`, and ubusql1's
  `testdb_1`), and neither is in any `SQLAgent*` role.

A third, disposable login — `zz_dbo_vss` (`db_owner` on HealthClinic **plus**
`VIEW SERVER STATE`) — was created on win10cli to see what the product looks
like once finding F1 below is fixed, then dropped. `HealthClinic` was left
unmodified: the one write attempted (recovery model `SIMPLE` → `FULL` as
`user_dr`) was correctly refused by the server.

## 2. Findings

Ordered by severity. Every one was reproduced by driving the built binary, not
inferred from the code.

### F1 — goSSMS cannot connect at all without `VIEW SERVER STATE` *(blocker)* — **FIXED (P0)**

Connecting as `user_dbo` to either server, or as `user_dr` to `ubusql1`, failed
outright:

```
Could not connect to win10cli: gosmo: load server info:
mssql: The user does not have permission to perform this action.
```

`gosmo.Server.loadInfo` read fifteen values in one statement. Thirteen are
`SERVERPROPERTY`/`@@VERSION` calls available to `public`; the other two are
`osi.physical_memory_kb` and `osi.cpu_count`, and the `FROM sys.dm_os_sys_info`
they need failed the **whole** statement. On SQL Server 2022+/17 the actual
denial is on the narrower `VIEW SERVER PERFORMANCE STATE`.

**Fixed in P0.** The two halves are separate statements now: the
`SERVERPROPERTY` one is still fatal, the DMV one degrades to
`ServerInfo.SysInfoUnavailable` and the connection succeeds. gossms renders the
two fields it leaves at zero as `N/A` (`sysInfoInt`/`sysInfoMB`) rather than
reporting a machine with no CPUs. Verified live: `user_dbo` connects on both
servers and gets a working session on its own database.
`TestServerInfoLoadsWithoutViewServerState` (gosmo) and
`TestConnectSurvivesADeniedSysInfoRead` / `TestUnreadableSysInfoRendersAsNA`
(gossms) all fail if the split is undone.

### F2 — an unreadable value renders as a wrong value, not as "unknown" — **FIXED (P2)**

- **Server Properties › Permissions** lists a principal's explicit permissions
  as `(none)` when the rows are merely invisible. As `user_dr`,
  `sys.server_permissions` returns 2 rows where `sa` sees 3 — `user_dbo`'s
  `CONNECT SQL` grant is filtered out, and the page states positively that it
  does not exist.
- **Database Properties › General** reported `Number of users` as **5** for
  `user_dr` and **9** for `zz_dbo_vss` on the same database, with no indication
  that the first number is a visibility artefact.
- Object Explorer Details already does this right: sizes for an inaccessible
  database show `N/A`. That is the pattern to generalise.

**Fixed in P2.** A count taken over a visibility-filtered catalog view is now
gated on the right that lifts the filter: `valueOrUnreadable`
(`permission_display.go`) renders `N/A` instead. Where the filtering hides
*rows* rather than a value — the two permissions matrices — there is no cell to
blank, so the page carries a `propsheet.Note` at the top instead, prepended
rather than appended because a Note is not focusable and Tab never scrolls one
into view (`propsheet.Form.Prepend`). Verified live on win10cli: HealthClinic's
user count reads 8 for `user_dbo` and `N/A` for `user_dr`, where it read 8 and
5 before.

### F3 — raw server errors leak into the tree, the detail pane, and the log viewer — **FIXED (P2)**

Expanding any folder of a database the login cannot open puts the driver string
into the tree as a node:

```
⚠ gosmo: list tables in "backup_test": gosmo: USE backup_test:
  mssql: The server principal "user_dr" is not able to access the database …
```

Same shape for `Agent › Jobs › User Jobs` (`The SELECT permission was denied on
the object 'sysjobservers'`) and for the Log File Viewer, which renders
`The EXECUTE permission was denied on the object 'xp_readerrorlog'` as its only
grid row while still offering Refresh / Search / Recycle / Export.

Note the database node itself expands happily — the folder skeleton is static —
so the user gets nine folders that each fail individually.

**Fixed in P2.** Two halves. The database node now asks
`DatabaseCapabilities.Accessible` before building the folder skeleton and
short-circuits to one leaf, the same shape the offline case has used all along
— nine identical Msg 916s become "Access denied — CONNECT permission on this
database is required." And any read refused on permission grounds is rendered
as the server's own sentence about it rather than the wrapped driver string,
through `permissionDeniedMessage`/`displayError` (`permission_error.go`), at
the three places an error is shown where content would go: the tree
(`errExplorerNode`), the detail pane, and the Log File Viewer's grid. Only a
refusal — anything else keeps its full text, because reporting a truncated
network read as a permissions problem sends the user after the wrong thing.

### F4 — every action is offered regardless of rights — **FIXED (P3)**

`grep -c 'Enabled:' internal/tui/*.go` → 52 gates, **none** permission-based.
Confirmed live:

- Right-clicking a database the login only reads offers Back Up, Restore, Take
  Offline, Rename, Delete.
- The Activity Monitor's Blocking tab offers **Install in master**; clicking it
  through its confirmation returns
  `install master.dbo.sp_block: mssql: Cannot alter the procedure 'sp_block',
  because it does not exist or you do not have permission.`
- Server Properties › Memory / Processors / Connections / Advanced present
  fully editable fields to a login without `ALTER SETTINGS`.
- Database Properties opens on an **inaccessible** database showing
  `Error: gosmo: space used: … is not able to access the database` and still
  offers OK / Apply / Script Changes.

**Fixed in P3.** Two mechanisms, both built on the fail-open rule: an action is
withheld only when the server denied *every* right that would permit it.

`gate` (`permission_gate.go`) extends a `MenuItem`'s existing `Enabled`
predicate rather than replacing it, so "no rights" and "nothing selected" both
still withhold. It is wired into the database node's Back Up / Restore / Take
Offline, Rename / Move / Delete on every object family, New Database, New
Login, Recycle Log, Activity Monitor (menu and toolbar), every Always On
action, and the Activity Monitor's Install in master. A withheld item names the
right it wants in the shortcut column — `MenuItem.Note`, shown only while the
item is disabled — because a disabled item cannot be clicked and so has no
reactive path left to answer on.

A page declares the rights its *writes* need (`propPage.requires`), evaluated
on the load goroutine where a database probe is affordable. When every one is
denied the page still loads and still reads, and comes up read-only: a note at
the top naming the rights, a form that refuses focus and clicks
(`propsheet.Form.SetReadOnly`), and a button row collapsed from
OK/Cancel/Apply/Script Changes to **Close / Script Changes**. Nothing on such a
page can become dirty, which is what makes Apply and Script Changes safe rather
than merely discouraged — and Script Changes is exactly what a login without
the rights to run the statements wants.

Database-scope gates read the cache only (`CachedDatabaseCapabilities`): an
`Enabled` predicate runs while a menu is being drawn, and a probe there would
block the application on a slow server. `App.onNodeSelected` primes the cache
off the UI goroutine, so the answer is there by the time the menu opens, and a
database nobody has touched yet simply fails open.

### F5 — the informative half of a SQL Server error is discarded — **FIXED (P0)**

Changing the recovery model as `user_dr` and pressing OK showed:

```
gosmo: set recovery model: mssql: ALTER DATABASE statement failed.
```

The server sent **two** messages, and the first is the one that matters:

```
Msg 5011 … User does not have permission to alter database 'HealthClinic' …
Msg 5069 … ALTER DATABASE statement failed.
```

`database/sql` surfaces only the last. `mssql.Error` carries `All []Error`
(first to last) and nothing read it.

**Fixed in P0.** gosmo's `withAllMessages` (`errors.go`) rewrites a
multi-message driver error so its text carries every message of severity 11 and
above, first to last — informational ones, such as the class-0 "Changed
database context" a `USE` produces, are dropped. It is hooked in at `withRetry`
(every read), `Server.execContext` (every server-scoped write) and
`Database.withConn` (every database-scoped one), so no display site had to
change. The same apply now reads *"gosmo: set recovery model: mssql: User does
not have permission to alter database 'HealthClinic', … ALTER DATABASE
statement failed."* Verified live on ubusql1 as `user_dr`.

### F6 — things that already degrade correctly (do not regress these)

- `internal/activity` probes `VIEW SERVER STATE` once
  (`snapshot.go:62`) and returns `ErrNoPermission` rather than drawing an idle
  server. This is the model the rest of the app should copy.
- Object Explorer Details shows `N/A`, not `0`, for a database it cannot size.
- The Logins folder lists only the principals the login can see, without error.
- The filesystem browser is **not** affected: `sys.dm_os_enumerate_filesystem`
  and `sys.dm_os_enumerate_fixed_drives` both return rows without
  `VIEW SERVER STATE` (verified as `user_dbo`). Only gosmo's pre-2017
  `xp_dirtree` fallback is sysadmin-only, and that one returns **0 rows with no
  error** — a silent empty directory.

## 3. Proposed solution

### 3.1 One capability probe, in gosmo, cached per connection — **SHIPPED (P1)**

`gosmo/capabilities.go` adds `Server.Capabilities`/`CapabilitiesContext` and
`Database.Capabilities`/`CapabilitiesContext`, returning `*Capabilities` and
`*DatabaseCapabilities`. `internal/db/capabilities.go` caches them on
`ServerConn`: the server scope is probed inside `Connect`, each database on
first use, and `ClearCapabilityCache` (wired to the server node's Refresh)
drops the per-database answers.

What the design turned on, all of it verified live rather than assumed:

- **Three states, not two.** `HAS_PERMS_BY_NAME` answers 1, 0 or **NULL**, and
  NULL — a permission this instance does not define — is not a denial.
  `CapabilityState` keeps the three apart, and the pair `Has` (known granted;
  use it to *offer*) / `Allows` (not known denied; use it to *withhold*) is
  what makes the difference actionable. Everything unknown makes `Allows`
  answer yes, so a failed probe leaves today's behaviour rather than locking
  the user out. Gating a menu item on `Has` would hide the application from a
  sysadmin whose probe timed out.
- **Never nil, never fatal, at the gossms boundary.** `ServerConn.Capabilities`
  and `DatabaseCapabilities` always return a usable value and no error. A
  *failed* database probe comes back `Accessible` with nothing known and is
  **not cached** — caching it would disable every gate for the session over one
  dropped connection. Only an answer the server actually gave reports
  `Accessible` false.
- **Accessibility is settled at the server scope first.** The role and
  permission probe runs *inside* the database, and `Database.query` opens with
  a `USE` — the very statement that fails for a login that cannot connect
  there. So `HAS_DBACCESS` is read server-scoped first, and an inaccessible
  database returns early, with no error. That early return is the predicate the
  tree needs for F3.
- **Database-scope permissions must be probed against the `DATABASE`
  securable.** `HAS_PERMS_BY_NAME(NULL, NULL, 'ALTER')` asks about the
  *server*, answers NULL, and so disappears without an error;
  `HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'ALTER')` is the question meant.
  Confirmed by the earlier probe of `VIEW DATABASE STATE`, which reads NULL at
  server scope and 1 at database scope for the same login.
- **sysadmin implies every server *permission* but no role membership.**
  `HAS_PERMS_BY_NAME` answers 1 for `sa` on all twenty probed permissions, so
  nothing needs folding — but `IS_SRVROLEMEMBER('SQLAgentUserRole')` is 0 for
  `sa`, so any Agent gate has to test `IsSysadmin() || InRole(...)`.
- **Read the answers by name.** The probe returns one `(kind, name, answer)`
  row per name through a `VALUES` constructor with each name bound as a
  parameter, so adding a name to a list cannot shift the answers after it —
  which scanning N booleans positionally would.

Cost, measured against both servers: **~3 ms** for the server probe, against a
connect that takes hundreds. Two round trips per database, on first touch only.

Live results (the four probed logins): `sa` — all 20 permissions granted, 8
roles; `user_dbo` — `public` only, `VIEW ANY DATABASE` granted and 19 denied,
`db_owner` with all 21 database permissions on HealthClinic, `backup_test`
**inaccessible**; `user_dr` — the three `VIEW SERVER STATE` forms granted on
win10cli and denied on ubusql1, `db_datareader` with `SELECT` alone. Zero
`unknown` on either instance, which is what says all 41 probed names are real.

### 3.2 Four presentation rules

The brief asked for hide / default text / state the minimum right. Concretely,
each surface picks exactly one:

1. **Placeholder** — a value that could not be read renders as `N/A` (never
   `0`, `False`, `Never`, or `(none)`), with the required right in the status
   bar or an adjacent note. Applies to F2. **`N/A` is settled**, not open: it
   was already the Object Explorer Details convention and P0 adopted it in
   `unreadableValue` (`prop_grid_helpers.go`), which every later site should
   use rather than inventing a second spelling.
2. **Read-only page** — a Properties page whose *reads* work but whose *writes*
   cannot loads normally, renders its editable rows as `propsheet.Static`, and
   carries one `propsheet.Note` at the top: *"Read-only: changing these
   settings requires ALTER SETTINGS (serveradmin)."* Apply/OK/Script Changes
   collapse to Script Changes plus Close. Applies to Server Properties'
   configuration pages and to Database Properties for a non-`db_owner`.
3. **Disabled action with a reason** — every `MenuItem`/`ToolbarButton`/page
   button gets its existing `Enabled func() bool` extended to consult the
   capability set, and the reactive fallback `setStatus` names the right:
   *"Requires ALTER ANY LOGIN (securityadmin)."* This is the existing
   context-gating rule in `CLAUDE.md` § Application rules, applied to a second
   axis. Applies to F4.
4. **Hidden** — reserved for the case where the object genuinely is not there
   for this login. A database with `HAS_DBACCESS` = 0 keeps its node (SQL
   Server itself still lists it) but loses its expander and gets a single
   greyed child: *"Access denied — CONNECT permission on this database is
   required."* That replaces nine separate raw errors with one honest line, and
   is the fix for F3's first shape.

The folder-level shape of F3 gets the same treatment one level down: a folder
whose load failed **on a permission error** shows one italic child, *"Requires
SELECT on msdb.dbo.sysjobs (SQLAgentUserRole)."* A folder that failed for any
other reason keeps today's raw error — the distinction is worth making, because
a truncated network read must not be reported as a permissions problem.

### 3.3 An error → required-right mapping — **SHIPPED (P4)**

One table, `internal/db/permerror.go`, keyed on SQL Server error number, that
turns a failure into a sentence naming the missing right:

| Msg | Meaning | Rendered as |
|---|---|---|
| 229 / 230 | SELECT/EXECUTE denied on an object | "Requires SELECT on *obj* …" |
| 262 | CREATE permission denied | "Requires CREATE *type* in *db*" |
| 297 / 300 | VIEW SERVER STATE (or its 2022+ narrower forms) | "Requires VIEW SERVER STATE" |
| 916 | cannot access the database | "Requires CONNECT on *db*" |
| 5011 / 5069 | ALTER DATABASE refused | "Requires ALTER on *db* (db_owner)" |
| 15151 / 15247 | object missing *or* no permission | "Not found, or requires *right*" |

Two rules for it. It must read `mssql.Error.All` and prefer the **first**
message, which is where the cause lives (F5) — today's last-message-only
behaviour is what turns Msg 5011 into "ALTER DATABASE statement failed". And
15151/15247 must keep their ambiguity in the wording: SQL Server deliberately
does not distinguish "missing" from "invisible", and neither should the UI.

**Shipped in P4**, in `internal/tui/permission_error.go` beside P2's
classifier rather than the `internal/db/permerror.go` this plan first named —
every consumer is in the presentation layer, and splitting classification from
wording across two packages bought nothing.

Four things the build turned on, each measured rather than assumed:

- **The table needed two more numbers and a second kind.** Msg **3701**
  ("Cannot drop the table 'Patients', because it does not exist or you do not
  have permission") has the ambiguous shape and was not in the list above; and
  **5011** turns out to have it too — "User does not have permission to alter
  database 'HealthClinic', *the database does not exist*, or …". So the
  classifier has two kinds. A *certain* refusal gets P2's "Access denied — "
  prefix; an *ambiguous* one must not, because "Access denied" is a claim the
  server did not make, and it sends someone hunting a permission for a table
  that was dropped last week.
- **Msg 262 is the workhorse, and it is not only CREATE.** A refused BACKUP
  sends "BACKUP DATABASE permission denied in database 'HealthClinic'" as Msg
  262, followed by the contentless Msg 3013 that `database/sql` surfaces. Same
  shape as CREATE TABLE / CREATE VIEW / CREATE PROCEDURE / CREATE DATABASE, all
  captured live.
- **First message, again, and for a second reason.** P2 established it for the
  DMV pair (Msg 300 then 297); the BACKUP pair (262 then 3013) is the same
  trap. The mutation that takes the last qualifying message turns "Requires
  VIEW SERVER PERFORMANCE STATE" into the generic "Requires VIEW SERVER STATE".
- **Wording is localized; numbers are not.** Classification is keyed on the
  number, and only the *identifiers* are read out of the message text with
  English patterns. A pattern that does not match yields no advice, and the
  caller falls back to the server's own sentence verbatim — which on a German
  instance is the only correct thing left to say. That fallback is a test, not
  a hope.

Where it is used: the tree node, the detail pane and the Log File Viewer's grid
show the advice in place of content (`displayError`); a Properties or New-object
page that failed to load shows it in place of the wrapped driver string; and a
*failed action* keeps its own full text with the advice appended
(`withPermissionAdvice`) at Apply, at Delete/Rename/Move, at Recycle, and at
every background task's completion.

### 3.4 Silent-empty is the dangerous case — **SHIPPED (P4)**

Two paths return **zero rows and no error** to an unprivileged login and so
cannot be handled by any of the above:

- `xp_dirtree` (gosmo's pre-2017 filesystem fallback) — an empty directory.
- Any `sys.*` catalog view whose rows are metadata-visibility filtered.

Both need a positive check *before* trusting an empty result: for the first,
`Capabilities.IsSysadmin` before offering the pre-2017 Browse path at all; for
the second, the placeholder rule (3.2.1) rather than a confident zero.

**Shipped in P4.** The catalog-view half is P2's placeholder and visibility
note. The `xp_dirtree` half is `legacyListingRefusal`
(`server_filesystem.go`): an empty listing is reported as a refusal only when
all three facts hold — the instance takes the legacy path
(`gosmo.Server.EnumFileSystemIsLegacy`, added for this), the result is empty,
and the probe actually ran *and* said the login is not a sysadmin.

That last conjunct is the one that matters, and it needed a new gosmo method.
`Capabilities.InServerRole` answers false for a role it was never asked about,
exactly as it does for one the login is not in — so a role test, unlike every
permission accessor, does not fail open on its own. `Capabilities.Probed()`
separates the two, and without it every unprobed connection would report an
empty directory as a permissions problem.

Not verifiable live: both test servers are 2017+, so nothing here takes the
xp_dirtree path. It is unit-tested on all five combinations.

## 4. Phasing

| Phase | Work | Why this order |
|---|---|---|
| ~~**P0**~~ | ~~Split `loadInfo` (F1); surface `mssql.Error.All` (F5)~~ — **done 2026-08-25** | Two small changes in gosmo; without P0 nothing else is reachable by the users this is for |
| ~~**P1**~~ | ~~`Server.Capabilities` + `ServerConn` cache (3.1)~~ — **done 2026-08-25** | Everything below reads it |
| ~~**P2**~~ | ~~Read paths: placeholder rule, inaccessible-database node, folder "requires…" child (F2, F3)~~ — **done 2026-08-25** | Pure display; no write-path risk |
| ~~**P3**~~ | ~~Write paths: `Enabled` gates, read-only Properties pages (F4)~~ — **done 2026-08-25** | Largest surface; needs P1 |
| ~~**P4**~~ | ~~Error mapping table (3.3), silent-empty guards (3.4), docs~~ — **done 2026-08-25** | Catches what static gating cannot |

All five phases are done.

P2 also closed the gap P0 left open: Server Properties' General, Memory and
Processors pages each returned the error from
`MemoryStatsContext`/`ProcessorInfoContext` and so failed their whole load,
which is why a `db_owner` who could now connect still could not open Server
Properties. All three degrade now — the values that read from the refused DMV
render as `N/A` and the page carries a note naming `VIEW SERVER STATE`, while
every configuration row on them stays editable. Verified live as `user_dbo` on
win10cli, which cannot open any of the three on the pre-P2 binary.

P2 classified refusals but did not translate them — the sentence shown was
always the server's own. P4 added the translation, and P2's
`permissionErrorNumbers` grew into `refusalNumbers`, which also records whether
a number's message is a *certain* refusal or an ambiguous one.

P3 closed the write path; P4 closed the gap between a refusal and the right it
names. What remains open is listed in `docs/open-threads.md` § Permission
gating — the ungated actions, and P3's are listed in `docs/open-threads.md` § Permission gating —
principally that a read-only page's rows still *look* editable, that a
schema-scoped Delete is gated on database-wide rights because the exact
permission is per-schema, and that SQL Agent's New * actions are deliberately
left ungated because what permits them is msdb role membership rather than any
permission the probe reads.

## 5. How to re-verify

The live matrix that produced this document:

- `win10cli` × {`user_dbo`, `user_dr`} and `ubusql1` × {`user_dbo`, `user_dr`}.
- To get the intended four-way matrix, `GRANT VIEW SERVER STATE TO user_dr` on
  `ubusql1` (currently missing) and add a `db_owner`-plus-`VIEW SERVER STATE`
  login on both — the disposable `zz_dbo_vss` recipe is in §1.
- Worth adding for P3: a login with `ALTER SETTINGS` but no `sysadmin`
  (serveradmin), and one in `SQLAgentReaderRole` but not `SQLAgentOperatorRole`
  — those two draw the read-only-page and disabled-action lines respectively.

Drive the binary under tmux per `CLAUDE.md` § Green tests are not verification.
Unit-testable parts: the capability struct's decode (including the `NULL`
case), the error-number mapping, and each `Enabled` predicate — the last via
the existing `fakedb_test.go` harness, which can script the probe's response.
