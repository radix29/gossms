# Engineering journal

Dated record of the work behind goSSMS and gosmo **since the current tag**:
what was built, what bugs were found and how, and which decisions were made
deliberately. Trimmed at each release — entries for work that has shipped come
out, since `CHANGELOG.md` records what shipped and git history keeps the rest.
Trimmed to `v0.0.5` (2026-08-04) on 2026-08-10.

Nothing here is required reading. `CLAUDE.md` carries the rules that still
apply; `docs/open-threads.md` carries the work that is still open. Newest
entries at the bottom. A `slug` under a heading is a note's name from the
Claude Code memory store this file was migrated out of, kept for older
cross-references.


---

## 2026-08-05 — Permissions gap-fill: WITH GRANT OPTION, effective, column-level

`permissions-gapfill-2026-08-05`

Five items sat under `open-threads.md`'s "Deferred scope (repeatedly,
deliberately)" heading — re-deferred on every properties pass since the
dialogs were built. All five shipped in one sitting, plus two smaller
carried-forward threads.

### gosmo

Three new files, no existing signature touched (the library rule):

- `permission_options.go` — `PermissionOptions{WithGrantOption, Cascade,
  GrantOptionOnly}` and twelve `...WithOptions` method pairs across object,
  schema, database and server scope, all rendering through one
  `permissionStmt.render`. The zero value renders byte-identically to the
  plain trio, which is what lets the UI take one path for every state; a test
  pins that equivalence at four scopes. A modifier the verb has no form for
  (WITH GRANT OPTION on a DENY) is an error rather than a silently dropped
  field.
- `column_permission.go` — `ColumnPermissions`, `ColumnPermissionsForPrincipal`,
  the grant/deny/revoke trio taking a `[]string` of columns, and
  `ColumnPermissionNames()` (SELECT/UPDATE/REFERENCES only — `GRANT DELETE
  (col)` is a syntax error). An empty column list is refused rather than
  quietly widening into an object-level grant.
- `effective_permission.go` — `fn_my_permissions` under `EXECUTE AS` for
  database, object and schema scope, plus `EXECUTE AS LOGIN` for server
  scope. The impersonation has to be in the same batch as the SELECT
  (`fn_my_permissions` only ever answers for the current execution context),
  which `Database.query`'s pinned connection makes safe.

Six matching `*Seq` iterators, and the README feature map + class diagram +
byte-identical `gosmo.mermaid` copy updated.

### gossms

- `internal/tui/perm_state.go` is the new shared layer: a four-state cycle
  (none → Grant → Grant With Grant → Deny), and `permTransition(orig,
  current)` deciding the modifiers from the *pair* of states. That pairing is
  the load-bearing part — SQL Server refuses to revoke or deny a permission
  granted WITH GRANT OPTION unless CASCADE is present, so every transition out
  of Grant With Grant carries it, and the Grant With Grant → Grant step is a
  `REVOKE GRANT OPTION FOR ... CASCADE` rather than a re-GRANT (a re-GRANT
  leaves the grant option standing and changes nothing).
- Both permission matrices took a single `permApplyFn` in place of the
  grant/deny/revoke triple — a three-way split has nowhere to put a decision
  that depends on two states.
- Filter boxes on every securables/permissions grid, live as you type. Needed
  `TextRow.SetOnChange` in tuikit, and `SetDirtyTracked(false)`: a filter box
  is a view control, and left dirty-tracked it made read-only pages report
  unsaved changes they had no way to save.
- Column permissions are edited inline on the Securables page rather than
  behind SSMS's modal, with an explicit "Load Columns" button through
  `PropDialog.runPageAction` — auto-loading on selection would be a query per
  arrow-key press.
- A new Effective Permissions page on all four principal dialogs.
- The create-dialog picker gap turned out to be New Login's "Default schema",
  a free-text box whose typo only surfaced as a failed CREATE USER at apply
  time; now a picker over the selected database's own schemas
  (`DropDown.SetItems`/`SelectRow.SetItems`). The owner-change warning is a
  `HintRow` on the owner-transfer pages rather than a modal — the change is
  staged until Apply, so there is no moment where a blocking "do it now?"
  prompt would be answering anything.

### Verified live against ubudock

Unit tests do not prove any of the modifier logic. Against a throwaway
`ClaudeTmpDB` with a `tmpuser` holding `GRANT SELECT ... WITH GRANT OPTION`
and `GRANT UPDATE (Salary)`:

- the object grant read back as "Grant With Grant" in the grid;
- cycling it to Deny and hitting Apply succeeded — that is the statement that
  fails outright without CASCADE, so it is the real test of `permTransition`;
- Load Columns listed the table's three columns, and cycling one to Grant
  produced a genuine `sys.database_permissions` row (`UPDATE`/`GRANT`/`Id`);
- Effective Permissions at object scope returned exactly the two column-level
  UPDATEs with their column in the Subentity column, and *no* SELECT — the
  DENY correctly absent rather than listed and overridden.

Database dropped afterward. One harness note worth keeping: the app's own
pooled connections hold a database open, so `DROP DATABASE` from a query
panel in the same session fails with "currently in use" — drop it from a
separate process (`Server.DropDatabase(name, true)`).

### Also closed this sitting

- **Object Explorer Details refresh.** The title-bar button ran
  `App.refreshSelected` (the *explorer's* selection); it now runs
  `DetailBrowser.RefreshCurrent`, which re-fetches the node the panel is
  actually showing. Same node today, correct if the panel ever drills on its
  own.
- **`ExecuteToSink` end-to-end coverage.** The old note claimed a fake driver
  could not reproduce sqlexp's `ReturnMessage` protocol. It can: implement
  `driver.NamedValueChecker`, intercept the `*ReturnMessage`, and drive it
  with `ReturnMessageInit`/`ReturnMessageEnqueue` exactly as the sqlexp docs
  specify. `internal/query/executor_sink_test.go` now pins `Result.Sets`
  staying empty, `RowsWritten`, the per-set notice, and the `sinkSets`
  success-notice decision — that last one A/B-verified by reverting it to
  read `RowsWritten` and watching the test fail. What a fake still cannot
  reach is `runBatch`'s drain gate (deleting the drain loop still passes), so
  that one stays a live check and remains recorded in `open-threads.md`.

## 2026-08-05 — `internal/tui/sqlparse`, and the measurement that killed the rest of the split

`sqlparse-extraction-2026-08-05`. Asked to review the costed three-step
`internal/tui` restructuring proposal in detail before acting on it. The review rejected two of its three steps and
shipped a fourth thing it had not proposed.

### Re-measuring with types instead of grep

The proposal's numbers all came from `grep -E '\bApp\b'` over the package.
Re-deriving them from a real cross-file reference graph — `go/types`, package
loaded with `Defs`/`Uses`, every use edge attributed to its declaring file,
549 edges — changed the conclusion:

- **The headline "56% of lines never mention `App`" is wrong.** `\bApp\b`
  does not match `p.app`/`d.app`, the lowercase field. The real number is
  48% (58 files, 12,618 lines). Nine files are misclassified by it, two of
  them inside the proposal's own Step 1.
- **Step 1 was not the "pure lift with no interface needed" it claimed.**
  The four completion files carry 30 outbound references to 11 symbols —
  `QueryPanel` ×11 (`completion_candidates.go` is mostly `*QueryPanel`
  methods), `nodeIcon`/`nodeData`/`Node*`, `p.app.cfg.IconStyle` ×3,
  `isConnected`, `completionInventory` ×6. And its "densest test suite,
  travelling unchanged" is `newTestApp()`-based for 811 of its 1,407 lines.
- **Step 2's "one interface, five methods" is off by ~8×.** 110 outbound
  references, 44 symbols, 16 files. Six are `App` services; the rest are
  shared helpers (`formatSQLDate` ×19, `fqn` ×10, `formatHMS`, `dashIfZero`,
  `intRowValue0`, the agent formatters) sitting in files the rest of `tui`
  references 146-221 times, which therefore cannot move. It needs a fourth
  package.
- **Step 2's benefit already existed.** Five props tests run with zero `App`
  references today.

The generalisable lesson, and the reason this is written down: **a grep for a
type name does not measure coupling to that type.** It misses field access,
it misses methods on types that themselves hold the dependency, and it misses
transitive reach entirely. Two minutes of `go/types` gave a different answer
to every number in a document that had already been reviewed and parked.

The other lesson is about the precedent the proposal cited for itself.
`planview` has no `Host` interface and no callbacks — it is a pure leaf, and
that is *why* it worked. A proposal that quotes an existing success as
precedent should be checked for having the same shape as it; Step 2 had the
opposite shape.

### What actually shipped

The reference graph found one set with **zero** outbound edges — the only one
in the package: the tokenizer and the scope scanner. Those became
`internal/tui/sqlparse` (`token.go`, `scope.go`, `doc.go`), 786 lines plus
596 lines of tests, 16 symbols exported. Worth doing on its own merits, not
as a probe: a T-SQL lexer has no business in the application shell.

Done to `CLAUDE.md`'s file-split rule — `git mv` first, `sha1sum` all five
files against their pre-move hashes (all identical), *then* rename
identifiers with an explicit sed map, and let the compiler find the rest.
`sqlToken`→`Token`, `scanCompletionPrefix`→`ScanPrefix`,
`completionTokenContext`→`TokenContext`, and so on; `lexSQL`, `goScan`,
`lexResult`, `sqlKeywordCanon` stay unexported.

The proof it was behaviour-preserving is the golden file. `TestScanCompletionPrefixGolden`
sweeps every cursor position in a script corpus; after the move its diff was
**exactly two lines**, both header text (`scanCompletionPrefix` → `ScanPrefix`
in the title, and the regeneration command's package path). All 1,560 lines of
actual scan output were byte-identical. Benchmarks unchanged at 1 alloc/op.

### Live verification

Green tests are not verification, so all of it was driven against ubudock
under tmux, on the real IntelliSense path:

- `SELECT * FROM dbo.` → schema members (tables and views, correctly iconed)
- `... FROM dbo.Patients AS p WHERE p.` → alias resolution backwards through
  `ParseFromScope`
- `SELECT d. FROM dbo.Doctors AS d`, cursor before the FROM → the forward
  scan (`StatementEndOffset` + forward tokenize) still resolves the alias
- `SELECT * FROM dbo.Invoices AS inv` / `-- GO` / `WHERE inv.` → columns
  offered: a commented-out GO still does not split the batch (this is the
  shipped bug the lexer's `goScan` comment names)
- the same with a real `GO` → no popup, alias correctly out of scope
- `dbo.[Pat` → bracket-identifier completion (the `LexBracket`/`QuoteStart`
  branch), and `sys.objec` → the sys inventory

### Rejected, and why it is written down rather than left open

Steps 2 and 3 are closed in `open-threads.md` with their measurements, not
just their verdict — a future review that only sees "120 files, one package"
will re-propose exactly this, and the four negative results are what stop it
costing another day. The proposal's own best contribution is one of them:
the `agent_*`/`new_*`/`*_props_*` name families are not a seam, because they
cut through the `App` dependency rather than along it.

---

## 2026-08-05 — Find and Replace in the query editor

Ctrl+F / F3 / Shift+F3 / Ctrl+F3, plus Edit > Find.../Replace..., built in
four layers: a search engine on `controls.Editor`
(`internal/tuikit/controls/editor_search.go`), a highlighting pass in
`editor_draw.go`, a modal `FindReplaceDialog`
(`internal/tui/find_replace_dialog.go`) in two modes, and the app-side key
and menu wiring.

### Decisions worth keeping

**One engine, not two.** A literal term goes through `regexp.QuoteMeta`,
whole word wraps it in `\b(?:…)\b`, case-insensitivity prefixes `(?i)`. So
match iteration, replacement, and the group-reference path have exactly one
implementation regardless of which options are ticked.

**Matches are per line, in rune indices.** The pattern is applied one
logical line at a time, so a match can never span a line break — which keeps
the per-line match list directly usable by the drawing path, and keeps every
position in the same units (`cursorCol`, selection bounds, `ColorRun.Start`)
the rest of the editor already uses. `byteRuneIndex` converts the regexp
engine's byte offsets once per line; a byte offset that reached a selection
bound would land mid-character on the first non-ASCII line.

**Zero-width matches are dropped at scan time.** `x*`, `^`, `\b` have
nothing to select or replace, and Find Next would stall on one forever.

**Replace All is one undo step, applied right-to-left per line.** Both are
pinned by tests: replacing left-to-right with a longer replacement shifts
the offsets of the matches still to come on the same line, and a per-match
`pushUndo` would take one Ctrl+Z per occurrence to undo.

**"In selection only" captures the selection at `SetSearch` time**, not at
`ReplaceAll` time — the first replacement moves the selection, so a range
read later is already wrong. With the option ticked and nothing selected it
replaces *nothing* rather than falling back to the whole document.

**The current match is the selection.** The drawing path paints all matches
in `Palette.EditorMatch` and lets the selection style win in `styleForRune`,
so the current hit stays distinct without a second "current match" colour or
a special case in the row renderer.

### Ctrl+H is deliberately unbound

tcell decodes the byte a legacy terminal sends for Ctrl+H (`0x08`) as plain
`KeyBackspace` with no Ctrl bit — indistinguishable from the Backspace key
on terminals that send `0x08` for it. Binding it would break Backspace
there, so Replace is reached through Edit > Replace... or the Find dialog's
own Replace mode. This was the user's call after the trade-off was laid out.

### Live verification

Driven against ubudock under tmux, not just unit tests: literal and regex
finds with the match counter tracking (`Match 3 of 4 — line 2, col 24`),
wrap-around at both ends, F3/Shift+F3 with the dialog closed, Ctrl+F3
switching the search to the word under the caret (verified by clicking onto
a *different* word first — plain F3 would have looked identical otherwise),
Replace then Replace All, both undone by exactly one Ctrl+Z each, "in
selection only" leaving the unselected line untouched, an invalid regex
reported in the dialog, and mouse clicks on the checkboxes and buttons.

Two layout bugs only the live run showed: Replace mode's four-button row
drew over the dialog's own left border at width 56 (now 62), and both modes
carried a spare blank row.

### Harness note

The Edit menu's Down-count is not stable — `Find Next`/`Find Previous` are
`Enabled`-gated on there being a search to repeat, and disabled items are
skipped during menu navigation. Counting Downs from a previous run activated
`Delete Line` instead of `Replace...`. Verify the highlight per keystroke
(the `48;2;0;122;204` match) rather than trusting a count, exactly as the
`ButtonsRow` note in the tmux-testing memory already says.


---

## 2026-08-05 — Effective Permissions: roles can't be impersonated, and the server scope needed USE master

`effective-permissions-role-fix-2026-08-05`

Raised by a two-repo review as item A1 of a plan, then confirmed live. The
Effective Permissions page shipped the sitting before onto "all four principal
dialogs" (see `permissions-gapfill-2026-08-05`). Two of those four could never
have worked.

### A role cannot be impersonated, so it has no effective permissions to show

Everything under `gosmo/effective_permission.go` resolves permissions by
impersonating the principal and asking `fn_my_permissions`, because
`fn_my_permissions` only ever answers for the *current* execution context —
there is no principal argument to pass instead. SQL Server refuses to
impersonate a role, at either scope:

| statement | result |
|---|---|
| `EXECUTE AS USER = N'<database user>'` | 3 permissions |
| `EXECUTE AS USER = N'<database role>'` | **Msg 15517** — "this type of principal cannot be impersonated" |
| `EXECUTE AS LOGIN = N'<login>'` | 5 permissions |
| `EXECUTE AS LOGIN = N'<server role>'` | **Msg 15406** — same, server-principal wording |

The error names three possible causes ("does not exist, this type of principal
cannot be impersonated, or you do not have permission"), which is what makes it
easy to misread as a missing principal. It isn't: the roles in the A/B plainly
existed and the connection was `sa`.

So the page is now gone from Database Role Properties and Server Role
Properties, and both gosmo doc comments — which promised roles outright — say
user/login. It is not a gap to fill later; there is nothing to fill it with
short of reimplementing SQL Server's own permission resolution in Go. SSMS
doesn't offer it for roles either. `rolePropPages` and `serverRolePropPages`
each carry a comment saying why, so it doesn't get "restored" as an oversight.

Dropping the page left `serverRolePropPages`' `*PropDialog` parameter unused —
it existed only for that page's `runPageAction` — so it went too.

### The server-scope query needed the USE master prefix

Found while building the A/B, not predicted by the review. `EXECUTE AS LOGIN`
keeps the session's current database, so `EffectiveServerPermissionsContext`
failed with **Msg 916** ("The server principal %q is not able to access the
database %q under the current security context") whenever the pooled
connection sat in a database the target login has no user in — which is the
normal case for exactly the restricted logins the page is most useful on.

Reproduced through gosmo itself, same binary, one line changed:

```
[conn db=master   ] LOGIN -> 5 permissions          [conn db=master   ] LOGIN -> 5 permissions
[conn db=gossms_a1] LOGIN -> ERROR: mssql: ... Msg  [conn db=gossms_a1] LOGIN -> 5 permissions
        916 ... not able to access the database
   (without USE master)                                  (with USE master)
```

`Server.GrantServerPermissionContext` and every other server-scoped statement
already carried this prefix for the same reason; the effective-permissions one
was simply missed. Worth noting the prefix works fine at the head of a *query*
batch and not just an exec — the `USE` produces an ENVCHANGE, not a rowset, and
go-mssqldb returns the SELECT's result set unchanged. That was checked rather
than assumed.

### Verified live against ubudock

Throwaway `gossms_a1` database, `gossms_a1_login`, `gossms_a1_srvrole`,
`gossms_a1_user`, `gossms_a1_dbrole`; all dropped afterward. Driving the built
binary under tmux, **connected to `gossms_a1`** so the Msg 916 path was live:

- Server Role Properties — pages are General, Members, Owned Roles,
  Securables. No Effective Permissions.
- Database Role Properties — General, Members, Owned Schemas, Owned Roles,
  Securables, Extended Properties. No Effective Permissions.
- Login Properties — Effective Permissions still present, and Show reported
  "5 effective permission(s) for gossms_a1_login" with the rows listed. Before
  the prefix, that same click was a Msg 916 error.


---

## 2026-08-05 — Column permissions were broken end-to-end for views

`column-permissions-views-2026-08-05`

Item A2 of the same two-repo review as `effective-permissions-role-fix-2026-08-05`.
A view carries column permissions exactly like a table — `GRANT UPDATE (Name)
ON dbo.SomeView` is legal and `sys.database_permissions` records it as an
`OBJECT_OR_COLUMN` row with a non-zero `minor_id`, indistinguishable from a
table's. Nothing on the Securables page handled that.

Two separate failures, one root cause: `ColumnPermissionEntry` reported no
object type, so gossms had nothing to key on and hardcoded `"TABLE"` in the two
places it built a `securable` from a column entry.

1. **Existing grants on a view showed as "(none)".** The seed keyed the entry
   under `TABLE` while the securable list gave the same object `VIEW`, so
   `columnEditKey` never matched. The grant was there, the grid said it wasn't
   — and cycling the cell would then have issued a fresh GRANT computed from
   the wrong `orig`.
2. **"Load Columns" hard-errored on any view.** It went through
   `TableByNameContext`, whose query reads `sys.tables`, so it returned
   `gosmo: table [dbo].[X] not found` for every view.

### gosmo side

- `ColumnPermissionEntry.ObjectType`, selected from `obj.type_desc` in both
  column-permission queries and mapped through the **existing**
  `securableObjectTypeNames` (security.go) — deliberately reusing that map
  rather than adding a second one, so `ObjectType` and
  `PrincipalSecurable.SecurableType` cannot drift apart. They are keyed
  against each other by callers.
- `Database.ObjectColumns`/`ObjectColumnsContext`, resolving through
  `OBJECT_ID` so it reaches a view. `Table.ColumnsContext`'s query body was
  extracted to a shared `columnSelect` const plus a `scanColumns` helper;
  each caller appends its own WHERE, because a `Table` already holds an
  `object_id` while the Database-scoped form has only a name.
  `Table.ColumnsContext` keeps its exact signature, results and error string.
- Zero rows means the object doesn't exist rather than "has no columns" —
  every table and view has at least one — so that case returns a not-found
  error instead of an empty slice.
- `Database.ObjectColumnSeq` in iter.go, keeping the one-Seq-per-listing
  convention.

The joins that supply identity/computed/default/primary-key data simply do not
match for a view, so those fields come back zero-valued. Name, ordinal, type,
length/precision/scale, nullability and collation are all real. Said so on the
method rather than leaving it to be discovered.

### Verified live against ubudock

Throwaway `gossms_a2` with `dbo.Employee`, `dbo.EmployeeView` over it, and a
non-owner `gossms_a2_user` (an owner's GRANT is a silent no-op — the trap
already recorded in the live-test-server notes). One column grant on each:
`SELECT (Salary)` on the table, `UPDATE (Name)` on the view.

Through gosmo directly:

```
ColumnPermissionsForPrincipal:  TABLE dbo.Employee     col=Salary SELECT GRANT
                                VIEW  dbo.EmployeeView col=Name   UPDATE GRANT
ObjectColumns Employee:      3 columns  Id(int) Name(nvarchar) Salary(money)
ObjectColumns EmployeeView:  3 columns  Id(int) Name(nvarchar) Salary(money)
ObjectColumns NoSuchThing:   gosmo: table or view [dbo].[NoSuchThing] not found
TableByName  EmployeeView:   gosmo: table [dbo].[EmployeeView] not found   <- the old path
```

That last line is the bug reproducing: it is exactly what Load Columns called.

Then the built binary under tmux, Database User Properties > Securables. Both
objects are listed off column grants alone (neither has an object-level entry),
which is the reconstruction path that was hardcoded:

- `[dbo].[Employee] | TABLE` and `[dbo].[EmployeeView] | VIEW` — the view typed
  correctly rather than as TABLE.
- Load Columns on the **view** succeeded ("3 columns"), where it previously
  errored.
- With UPDATE selected, the view's columns read `Id (none)` / `Name Grant` /
  `Salary (none)` — the grant found, not "(none)".
- Regression check on the table: with SELECT selected, `Id (none)` /
  `Name (none)` / `Salary Grant`.

Database dropped afterward.

## permissions-apply-and-drag-fixes-2026-08-05

Items A3–A6 of the 2026-08-05 two-repo review, in one pass. No commits.

### A3 — apply order and the stale baseline

Both permissions editors ranged a Go map to issue their statements
(`editsByPrincipal`, `editsBySecurable`, `colEdits`), and neither moved a
cell's `orig` after the statement succeeded.

Ordering is now a walk of the `principals` / `securables` slice, plus
`slices.Sorted(maps.Keys(colEdits))` for the column edits. Every map key comes
from `editsFor(<member of that slice>)`, so the walk is exhaustive.

`commitApplied` (perm_state.go) moves `orig` onto `current` after a successful
statement, guarded by `!gosmo.Scripting(ctx)` — committing under Script Changes
would mark the page clean and leave the following real Apply with nothing to
do, the same trap `commitRename` documents. `applyPermEdit` pairs the two so a
caller can't do one without the other.

**The plan's stated rationale for A3 was wrong and is corrected here.** It
claimed re-issuing an already-applied transition is "wrong, not merely
redundant" because `permTransition` derives `REVOKE GRANT OPTION FOR` and
`CASCADE` from `orig`. Checked against the live server, every one of those
statements is idempotent:

```
seed                                        GRANT_WITH_GRANT_OPTION
REVOKE GRANT OPTION FOR ... CASCADE   x1    GRANT
REVOKE GRANT OPTION FOR ... CASCADE   x2    GRANT      <- replay is a no-op
DENY ... CASCADE                      x1    DENY
DENY ... CASCADE                      x2    DENY
REVOKE ... CASCADE                    x2    (no row)
```

So the replay costs round trips, not correctness. The defect a stale baseline
*does* cause is the page misreporting server state, and it bites when the user
undoes an edit that already landed: cell downgraded from Grant With Grant to
Grant, Apply issues the REVOKE GRANT OPTION FOR and then fails on a later
cell, user puts the cell back to Grant With Grant and presses Apply — with a
stale `orig` the cell reads clean, nothing is issued, and the grid claims a
grant option the server lost. `Dirty()` and `Revert()` are wrong for the same
reason, both being `orig`-versus-`current` comparisons. That is what
`TestPermissionsMatrixUndoOfAnAppliedEditIsReissued` pins.

### A4 — a filter matching nothing left the lower grids live

`loadSecurable`/`loadPrincipal` returned early on an out-of-range row, so
filtering to zero rows left the previous selection's grid on screen and
editable. Both now call a `clearSelection` that empties the lower grid(s),
resets the section titles and drops `selectedEdits`. Edits already made are
kept — only the display is cleared.

Live A/B, Server Properties > Permissions, filter `zzz`:

```
pre-fix   top grid 0 rows | "Explicit permissions for ##MS_PolicyEventProcessingLogin##", 35 rows
post-fix  top grid 0 rows | "Explicit permissions", 0 rows
```

### A5 — TextRow.Revert contradicted its own doc

`SetDirtyTracked(false)` promised "Revert leaves it alone"; `Revert` reverted
anyway and never fired `onChange`, so `Form.Revert` blanked a filter box while
the grid it filters stayed narrowed on the old term. Fixed on `TextRow` and on
`SelectRow`, which had the identical defect and whose `SetDirtyTracked` doc
points at `TextRow`'s.

### A6 — a drag that leaves the field stops selecting

Two halves. `FindReplaceDialog` hit-tested every `Button1`, so motion outside
the Find field's rect never reached it; it now has a `dragField` press-owner
(cleared on the release and on `Show`, invariants 1 and 4). That alone changed
nothing, because `InputField.HandleMouse` hit-tests too — its own
`mouseDragging` latch now takes priority, which is the actual mechanism.

Live A/B, press on the field's first cell and drag five rows below it:

```
pre-fix   [a]bcdefghij     — caret only, no selection; the drag never arrived
post-fix  [abcde]fghij     — selection extended, SGR bg 48;2;0;122;204
```

The widget half is general but only reaches hosts that forward off-rect
motion; every other dialog still hit-tests first. Recorded in
`docs/open-threads.md` rather than widened here.

### Verification

Unit tests were A/B'd against the pre-fix code rather than just written green.
Each failed first with the message it was written to produce —
`TestPermissionsMatrixApplyOrderIsStable` caught map order on run 4 of 20,
`TestPermissionsMatrixEmptyFilterClearsSelection` caught the stale grid *and*
the cycle reaching the hidden edit ("issued `GRANT ... [WITH GRANT OPTION]`",
i.e. it had cycled a cell the page no longer showed).

`gofmt -l`, `go build ./...`, `go vet ./...`, `go test ./...` clean in both
repos. gosmo untouched this pass. Throwaway database `gossms_a3` dropped,
confirmed at zero.

## gosmo-legacy-permission-delegation-2026-08-05

Item C1 of the 2026-08-05 review. gosmo had two renderers for the same
statement: `permissionStmt.render` (permission_options.go) and a hand-rolled
`fmt.Sprintf` inside each of the twelve plain
`Grant/Deny/RevokePermission*Context` methods — three each at object, schema,
database and server scope. `PermissionOptions`' doc already claimed the zero
value "renders exactly the statement those trios render"; nothing enforced it.

Each of the twelve is now a one-line delegation to its `…WithOptionsContext`
counterpart with a zero `PermissionOptions`. 84 net lines gone, one renderer,
and the doc claim is true by construction. No exported signature changed, so
the gosmo library rule is satisfied — this removes duplication, not
capability.

### Proving it behaviour-preserving

Two harnesses, because neither alone reaches the whole surface.

`TestLegacyPermissionMethodsRenderAndReject` pins, for all twelve, the exact
rendered statement (via `gosmo.WithScript`, which captures writes without a
server) and the exact validation error for an unrecognized permission name.
Written and run green against the **pre-delegation** code first, so it pins
existing behaviour rather than the refactor's.

Scripting can't reach the exec-error wrap — under `WithScript` the write
succeeds. Those needed a real server, calling each scope against an object
that doesn't exist, captured before and after:

```
object grant    gosmo: grant SELECT on [dbo].[NoSuchTable] to "NoSuchUser": mssql: Cannot find the object ...
object revoke   gosmo: revoke SELECT on [dbo].[NoSuchTable] from "NoSuchUser": ...
schema deny     gosmo: deny UPDATE on schema "NoSuchSchema" to "NoSuchUser": ...
database grant  gosmo: grant CREATE TABLE to "NoSuchUser" in "master": ...
server grant    gosmo: grant VIEW SERVER STATE to "NoSuchLogin": ...
server revoke   gosmo: revoke VIEW SERVER STATE from "NoSuchLogin": ...
```

`diff before.txt after.txt` — identical. `fromOrTo` reproduces the to/from
split exactly, and `lower := strings.ToLower(verb)` reproduces each verb's
lower-cased prefix.

Nothing was created on the server: every call names an object that does not
exist, so all six failed by design and wrote nothing.

### A note on the older test

`TestZeroPermissionOptionsMatchesPlainStatement` compared the plain and
WithOptions statements for four pairs. After the delegation both sides run the
same code, so it can no longer detect two renderers drifting — it now asserts
the delegation is still in place. Its comment says so, and the literal
statements moved to the new table test.

## editor-search-draw-optimizations-2026-08-05

Items B1 and B2 from the 2026-08-05 review — the two find/replace hot paths,
both in `internal/tuikit/controls`.

### B1 — `styleForRune` scanned the whole match list per drawn column

`lineRow.styleForRune` ran `for _, m := range r.matches` for every rune it
styled, so a Draw pass cost `visible columns × matches on that line` per row.
Searching a common short string in a wide script is exactly that shape.

Replaced with `lineRow.inMatch`, an advancing cursor: `drawLineRow` only ever
asks for a non-decreasing `i`, and `matches` is sorted and non-overlapping, so
each match is stepped past once per row. The first lookup binary-searches
rather than walking from index 0 — a horizontally scrolled row, and every wrap
segment after the first, starts part-way along the line, and under wrap mode
everything left of the segment is most of the list. `matchCur`/`matchPrimed`
are scratch, not caller input, which is why the zero value has to mean *not
primed*: `lineRow` is built as a composite literal at two call sites and
neither should have to remember to initialise them.

### B2 — `byteRuneIndex` allocated for every line, ASCII included

`scanMatches` built a `len(line)+1` `[]int` per line per scan to convert the
regexp engine's byte offsets to rune indices. For a pure-ASCII line — the
overwhelmingly common case in T-SQL — that map is the identity. It now returns
`nil` there and the caller uses the byte offset directly. An edit invalidates
the scan, so this ran once per line of the whole document on the next Draw.

### Measured, `-count 6`, i5-2500K

| benchmark | before | after |
|---|---|---|
| `EditorDrawManyMatchesWideLine` | 131 µs/op | 37 µs/op |
| `EditorDrawManyMatchesWrapped` | 1.31 ms/op | 147 µs/op |
| `SearchScanASCII` | 36.3 ms, 3.84 MB, 22010 allocs | 32.5 ms, 1.43 MB, 17004 allocs |
| `SearchScanNonASCII` | 36.4 ms, 4.18 MB, 22009 allocs | 36.5 ms, 4.05 MB, 21509 allocs |

The 5006 allocations `SearchScanASCII` loses are one per line of the 5000-line
fixture. `SearchScanNonASCII` is unchanged by design; it drops a few hundred
because some lines of that fixture are still pure ASCII.

### How it was checked

Five style-per-column tests in `editor_matchstyle_test.go`, run green against
the pre-change code first so they pin existing behaviour: every match on a
line painted (not just the first), the current match in the selection style
while the rest stay in the match style, matches under horizontal scroll, every
segment in wrap mode, and a match spanning wide runes. A `styleScreen` records
the style of each cell and reads a row back as `m`/`s`/`.`, so these assert the
resolved style rather than that Draw survived.

B2 splits ASCII and non-ASCII into different code paths, which had no test at
all before: three added in `editor_search_test.go` cover rune-index bounds on a
non-ASCII line, a document mixing the two kinds of line (one line's mapping
must not carry into the next), and a replacement on a non-ASCII line, where
bounds off by the multi-byte prefix corrupt the text rather than merely
mis-highlighting it.

Live: connected to ubudock, `SELECT col, col FROM dbo.T; -- col héllo col
wörld col`, Ctrl+F `col`, Enter, Escape. `capture-pane -p -e` shows the first
hit on the selection background (`48;2;0;122;204`) and the other four on the
match background (`48;2;88;76;22`), including both hits that follow a
multi-byte rune.

## page-action-latch-and-scope-rows-2026-08-06

Items A7 and C2 from the 2026-08-05 review.

### A7 — a second click put two round trips in flight

`runPageAction` launches a goroutine and reports back through `d.post`; no
caller guarded against being clicked again while the first was still out. Two
goroutines then both write the captured result variable and both fill the same
grid, so the picture that survives is whichever finished last — which can be
the older request.

Added `PropDialog.runPageActionOnce(&inFlight, fn, onDone)`, which sets the
latch on the way in and clears it in the callback. `inFlight` is a plain bool
because both halves run on the UI goroutine: the click handler, and `onDone`
via `d.post`. Wired into the two Effective Permissions Show buttons, the
Securables page's Load Columns, and `asyncStatusButton` — the last had the same
defect and covers Check Syntax, Rebuild, Reorganize and Update Statistics in
one place. A blocked click is not a silent no-op: the hint or status row
already reads "Resolving..." / the busy text from the first one.

Pinned by `prop_dialog_action_test.go`, which plays the event loop's part
itself — with no screen the wake half of `postAndWake` is a no-op, so the test
drains `pending` in a loop. It asserts the second click launches nothing, the
latch clears afterwards, a later click runs, and the latch clears on the error
path too. Stubbing out the guard fails it with "a second click ran while the
first was still in flight".

### C2 — the header literal, and rows that do nothing

`effectivePermsCols` hoisted: `SetData` takes the header with the rows, so two
literals were two things to keep in step.

Schema and Table-or-view are now enabled only for the scope that resolves
against them, re-synced from the picker's `OnChange`. The `Show` guards stay —
a row can be enabled and still blank, and dropping live validation to save two
branches would let a blanked Schema box reach the query.

**That change exposed a real bug and could not ship without fixing it.**
`TextRow.SetEnabled`'s doc claims a disabled row is "drawn dim", but
`InputField` had no notion of being disabled: the row merely became
unfocusable, so it rendered *identically* to a live field while ignoring every
click — the "click does nothing" failure `CLAUDE.md` § Application rules is
about. Three shipped callers were already in that state
(`database_props_scoped_config.go`, `login_props.go`, `server_props.go`), so
this was not new, but adding a fourth knowingly was not an option.

`InputField` now has `SetEnabled`/`Enabled`. A disabled field drops the input
background entirely (`theme.StyleInputDisabled`, dialog background + `TextDim`)
rather than only dimming its text, keeps the unfocused border, paints no
caret, and refuses keys and clicks — the mouse guard sits *above* the
`ButtonNone` branch, since a disabled field never latched a press and so has no
drag to end. `TextRow.SetEnabled` forwards to it, which makes its own doc true
and fixes the three existing pages as a side effect.

### How it was checked

`TestDisabledInputFieldRefusesInput` covers keys, press, release and
re-enabling; `TestDisabledInputFieldDrawsDifferently` asserts the two states do
not render identically, which is the half that was missing before.
`fieldScreen` now records styles as well as runes.

Live, against ubudock: Database User Properties for `clinic_app_user` →
Effective Permissions. Under Database scope both lower rows draw on the dialog
background (`48;2;45;45;48`) with no input background anywhere; switching the
picker to Table or view brings `48;2;51;51;55` back on both; Schema scope
brings it back on Schema only. Show still resolves (13 permissions on schema
dbo), three rapid clicks produce one result, and the button is not wedged
afterwards.

## securables-server-side-search-2026-08-06

Item B3 from the 2026-08-05 review, taken as the server-side option
rather than the cheap client-side filter.

The Securables page used to call `TablesContext` + `ViewsContext` +
`SchemasContext` on open and turn the entire result into one dropdown. On a
database with thousands of tables that is slow to open and useless to pick
from, and the existing "Filter securables" box only narrowed the *top grid*,
never the picker.

### gosmo: `Database.FindSecurables`

New `securable_search.go`. `FindSecurablesContext(ctx, SecurableSearch{Name,
Limit})` is one query over `sys.schemas`/`sys.tables`/`sys.views`, matching
`Name` case-insensitively as a substring of the *qualified* name — so both
"ord" and "dbo.ord" find `dbo.Orders` — and ordering schemas, then tables,
then views, each by qualified name, so a capped search returns a stable
prefix rather than an arbitrary subset. `SecurableRef` carries only Type,
Schema and Name: a picker needs the identity, and materialising `Table`/`View`
values for thousands of candidates is work no caller wants.

`escapeLikePattern` makes the term literal under `ESCAPE '\'`. Identifiers
legally contain `_` and `%`, and a name containing `[` turns the pattern into
a character class that matches nothing — the search box would just come up
empty with no explanation. Verified live against HealthClinic: `_` returns
only names literally containing an underscore (13), `%` and `[a` return
nothing, `pat` returns `dbo.Patients` and `dbo.vw_PatientHistory`, `dbo.pat`
returns only the table, `PAT` matches both regardless of collation.

### gossms: the search box

`buildSecurablesMatrix` takes `candidates []securable` plus a
`securableFindFn` instead of a fixed `available` list. The page loader runs one
capped search so the picker has content before anything is typed; a new
"Search to add" row re-queries as the user types.

**The searches coalesce rather than queue.** At most one is in flight; if the
term moved on while it was out, the completion starts the next one. A query
per keystroke puts five round trips behind "Order" *and lets an early one land
last* — the A/B against an unguarded version shows exactly that, the recorder
receiving `[ord o]` and the picker ending up repopulated from a term the user
had already typed past.

`FindSecurables` returns at most `securableSearchLimit+1`; the extra row is how
the page knows to trim to 200 and say "More than 200 matches — type more to
narrow the list." A trimmed list that says nothing reads as complete, and the
user concludes the object does not exist. The database itself is a securable no
name search returns, so the page offers it whenever the typed term matches its
label — the same `matchesFilter` the top grid uses.

### How it was checked

Four tests in `securables_search_test.go`, driving a real built form with a
recording find function: coalescing, the cap and its hint, the database entry
appearing only when the term matches it, and a failed search reporting through
the hint while leaving the picker alone (emptying it would read as "no such
object"). They run under `-race`; the recorder locks, since the find function
runs on a background goroutine.

Live, against ubudock → HealthClinic → clinic_app_user → Securables: the page
opens with no catalog load, typing "pat" narrows the picker to the two
matching objects and drops "(database)", picking the view and clicking Add
gives it a row typed VIEW with the middle grid switching to its permission
catalog, and "pazzqt" leaves "(no matches)" with "Nothing on the server matches
that name."

Note for a future reader: `Database.Tables`/`Views`/`Schemas` are untouched —
gossms simply no longer calls them from this page. They are library surface,
not dead code.

## grid-column-resize-2026-08-06

Mouse-resizable `DataGrid` columns, SSMS-style: drag the separator in the
header row and the column *to its left* changes width; double-click that
separator and it goes back to the width it was given.

The Options dialog's "Max cell length (Query Results)" is now "Max default
cell length" — the same number, but it now caps only what `computeColWidths`
hands a column from its content. A dragged width (`colWidthOverride`) is
applied after that clamp and ignores it, so a 24-character cap no longer
means a 60-character value can never be read in the grid.

### What the state has to survive

`computeColWidths` runs far more often than the data changes — every
`SetBounds`, and every frame after a progressive backfill's
`RefreshColumnWidths`. So the drag can't just write `colWidths`: it records
an override and lets the recompute re-apply it, or a window resize would
silently undo the user's drag. Two consequences fall out of that:

- `SetSource`/`SetError` clear the overrides. Column 2 of the next result set
  is not column 2 of this one.
- `growLastColumnToFill` is skipped for a last column that has an override —
  otherwise the Property/Value detail grids would stretch a deliberately
  narrowed Value column straight back out.

The drag itself follows the scrollbar's shape exactly: `colResizing` latches
on the press and every Button1 event belongs to the resize until the release,
checked ahead of `rect.Contains` so the edge keeps following a pointer that
has left the grid. `resizeStartX`/`resizeStartW` are the grab point, so each
motion resolves against the original grab instead of accumulating.

Hit-testing is the header row only. The separator glyph runs down every data
row, and grabbing it there would eat cell-selection clicks for a
one-column-wide target the user is unlikely to have aimed at.

tcell reports presses, not clicks, so the double-click is timed here
(`sepPressIsDouble`, 500ms, same separator). The press that completes a
double-click still latches `colResizing`: tcell resends Button1 on every
motion while the button is down, and by then the separator has moved, so
those resends have to be absorbed by the latch rather than re-entering the
hit test against a stale position.

### How it was checked

Seven tests in `datagrid_resize_test.go` — widen past the max default, narrow
down to `minResizeWidth`, survive `SetBounds`/`RefreshColumnWidths` but not
`SetData`, double-click restore, header-row-only hit testing, the row-number
gutter's offset, and fill-last-column losing to a drag.

Live against ubudock, driving the built binary under tmux with raw SGR mouse
sequences: `select name, type_desc, create_date from sys.objects`, dragging
the `name` separator from column 74 to 100 took the column from 26 to 52 and
double-clicking it put it back at 26. With a 60-character value and the
default 24-character max, widening the column showed the whole value —
`abcdefghij`×6, untruncated — which is the point of the change. Cell
selection on a data row still worked afterward.

---

## 2026-08-06 — Drag hosts closed, and the drain gate settled live

`drag-hosts-and-live-gate-2026-08-06`

Two open threads closed: the text-selection-drag hosts, and the two
live-only verifications that had been carried since 2026-07-30.

### The drag hosts

`widgets.InputField` has honoured its own `mouseDragging` latch ahead of its
`HitTest` since 2026-08-05, so a field extends a selection wherever the
pointer goes — but only if its host forwards the off-rect motion. Four hosts
still hit-tested every `Button1` first and got a `dragField` press-owner,
the same shape `FindReplaceDialog` already had: `connect_dialog.go` (seven
fields), `backup_dialog_input.go` (`fDest`), `restore_dialog_input.go`
(`fTarget`/`fFile`), `tuikit/dialogs/file_dialog_input.go`
(`pathField`/`nameField`).

Placement mattered in two of them. In `file_dialog_input.go` the replay has
to go *above* `ButtonClicked`, not inside the `Button1` switch below it —
otherwise a selection drag that wandered over the button row fired the
button. In `restore_dialog_input.go` the release has to be handled ahead of
both `ConsumeOutsideClick` and the mode switch, either of which returns
early and would strand the latch.

**Two of the six hosts the open thread listed did not need fixing**, which
is the part worth remembering, because the entry had been carried for a day
naming them:

- `propsheet.Form` gives `Focused()` first refusal of every non-wheel button
  *before* band routing (`form.go:387`), so a focused `TextRow` already saw
  off-band motion.
- `options_dialog.go` calls `fMaxCellLen.HandleMouse` unconditionally — the
  field self-hit-tests, so the drag worked. It did have a real latch bug,
  found while checking: its `ButtonNone` reset list covered `rbIconStyle`
  and `cbIntelliSense` but not `fMaxCellLen`, so a release outside the
  dialog (eaten by `ConsumeOutsideClick`) left the field armed and it
  swallowed the next press. One line.

Each new test was A/B'd against the pre-fix code and fails on it — the
connect and file-dialog ones on `SelectedText()` being empty or the list
stealing the gesture, the options one on the stranded latch.

### The live gate

`executor_sink_test.go`'s fake driver implements the sqlexp contract but not
TDS, so deleting `runBatch`'s drain loop still passes it. Both halves of the
gate were finally run against the real server
(`internal/query/live_drain_test.go`, build tag `livedb`, skipped without
`-livedb`):

- **An extra `Next()` past an exhausted set does swallow the pending
  message — confirmed, and worse than recorded.** The control run saw both
  result sets and the `PRINT` between them; with one extra `Next()`, the
  entire second result set never arrived. That is exactly the shipped
  failure CLAUDE.md forbids reintroducing (empty grid, no error, no Messages
  tab), now reproducible on demand.
- **The drain loop after an abandoned set is *not* load-bearing on this
  server/driver.** Without it, go-mssqldb still advanced past the abandoned
  set and delivered everything after it. Keep the loop — it is what makes
  the behaviour independent of a driver detail — but its removal is no
  longer an unquantified live-only risk.

### ExecProc under WithScript

`gosmo/live_execproc_script_test.go` (same tag) scripts the EXEC form and
then hands the text to the server the way a user pasting it would. The open
thread's specific worry was wrong: a `decimal(18,4) OUTPUT` does not get
`SQL_VARIANT` when the caller passes the natural `*float64` — it gets
`FLOAT`, the server accepts it, and `1234.5678` round-trips exactly. `INT`,
`BIGINT`, `NVARCHAR(MAX)`, `FLOAT`, `BIT` and `DATETIME2` all ran and
round-tripped.

The fallthrough underneath it is a real defect, though: an unmapped pointee
type gets `DECLARE @v SQL_VARIANT`, and against a decimal OUTPUT the server
refuses — "Implicit conversion from data type sql_variant to decimal is not
allowed." The scripted EXEC is handed to the user as text they cannot run.
Left unfixed and recorded in `docs/open-threads.md`: erroring out of
`scriptExecProc` for an unmapped pointee is probably right, but it is a
behaviour change on a published gosmo API and the author's call.

---

## 2026-08-06 — scriptDeclType's SQL_VARIANT gap

`execproc-decltype-fix-2026-08-06`

Follow-on from the live ExecProc check the same day. The first pass reported
the `SQL_VARIANT` fallthrough as a defect and left it for the author; the
fix took a different shape than either option offered there, because two
live probes changed the picture.

### What the probes settled

- **`DECLARE @v SQL_VARIANT` is correct and accepted** against a procedure
  whose parameter really is `sql_variant`. So erroring out of
  `scriptExecProc` on the fallthrough — the option that looked right — would
  have broken the one case the fallthrough exists for. It is a working
  capability, not a bug.
- **The destination type that triggered the original report wasn't reachable
  through gosmo's API anyway.** The probe used a `*struct{X int}`, which the
  driver rejects on the real RPC path too ("unsupported type ..., a struct"),
  so the script path emitting bad SQL for it was moot.

Which left the actual question: which destination types are *valid* on the
RPC path and still fall through to `SQL_VARIANT`? Eleven of them, and they
are not exotic:

```
*sql.NullInt64  *sql.NullInt32  *sql.NullInt16  *sql.NullByte
*sql.NullString *sql.NullBool   *sql.NullFloat64 *sql.NullTime
*mssql.UniqueIdentifier  *mssql.NullUniqueIdentifier
```

All accepted by the driver (`rpc=<nil>`), all scripted as `SQL_VARIANT`, all
refused by the server: *"Implicit conversion from data type sql_variant to
int / nvarchar / uniqueidentifier / decimal is not allowed."* `sql.Null*` is
*the* way to receive a nullable OUTPUT parameter, so this was the ordinary
path, not a corner.

### Why the kind switch missed them

`scriptDeclType` switched on `reflect.Kind`. The `sql.Null*` family and
`NullUniqueIdentifier` are structs — the `Struct` branch only knew
`time.Time` — and `UniqueIdentifier` is a `[16]byte` array, which had no
branch at all. Their Go *kind* cannot give their T-SQL type away, so the fix
is a type-keyed lookup (`declTypeByName`) consulted ahead of the kind
switch. Purely additive: the kind switch is untouched and `SQL_VARIANT`
remains the fallthrough.

`scriptLiteral` needed nothing — it already handles `driver.Valuer`, which
every `sql.Null*` implements, so the `InOut` seed value was always fine.

### Verification

`script_test.go` pins all eleven mappings plus the two `SQL_VARIANT` cases
(unmapped struct, non-pointer) in the ordinary suite.
`live_execproc_script_test.go` (tag `livedb`) runs each scripted `EXEC`
against a procedure with the matching parameter type, and separately
confirms `SQL_VARIANT` still binds to a `sql_variant` parameter — the
regression that widening-instead-of-erroring exists to avoid.

One oddity worth knowing: `*mssql.NullUniqueIdentifier` scripts correctly but
the *driver* rejects it as an `sql.Out` destination ("Data type 0x00 is
unknown"). The mapping is kept — the scripted form is right, and the
limitation is go-mssqldb's.

---

## column-ddl-removal-2026-08-06

Author's call: `Table.AddColumn`/`AddColumnContext` and
`Table.DropColumn`/`DropColumnContext` removed from gosmo outright, rather
than left in as the accepted-non-functional pair `docs/open-threads.md` had
carried since 2026-08-01. `DropColumn` failed on any column an index
referenced, and the fix was a policy choice (drop dependent indexes, or
detect and refuse) nobody wanted to make; neither method had a gossms caller
or a UI path.

This is the standing exception to CLAUDE.md's "never remove a gosmo
capability" rule — that rule binds *me*, not the author, and the removal was
directed explicitly.

`AlterColumn` stays, and so does `CreateTable`, which is the only remaining
user of `ColumnDefinition`'s `IsIdentity`/`IdentitySeed`/`IdentityIncr`/
`DefaultValue` fields — they are not dead now.

Follow-on edits, all of which named the removed methods:

- `table.go` — `AlterColumn`'s doc comment pointed at "DropColumn/AddColumn"
  as the way to change a default; now says "the column, or its default
  constraint", naming no method.
- `script.go` — `bindScriptArgs`'s comment listed the four parameterised
  write methods. `Table.DropColumn` was one; three remain (`Index.Rename`,
  `Database.RenameTable`, `Database.DropTable(cascade=true)`), verified by
  grepping `@p1` across non-test sources and discarding the read paths.
- `script_test.go` — dropped the `DropColumn` case from
  `TestWithScriptBindsParametersIntoTheStatement`. It was the only case
  covering *two* placeholders in one statement, but `Index.Rename` and
  `RenameTable` both bind `@p1`+`@p2`, so the coverage is not lost.
- `table_test.go` — `TestAddColumnRequiresName` deleted; its shared header
  comment now covers `TestAlterColumnRequiresName` alone.
- `permission_allowlist_test.go` —
  `TestAddAndAlterColumnRejectUnknownDataType` is now
  `TestAlterColumnRejectsUnknownDataType`. The `validDataType` allowlist is
  still exercised, by `AlterColumn` and `CreateTable`.
- `README.md` and `gosmo.mermaid` — both carry the same generated `class
  Table` block; both lost the two method lines, plus the two rows of the
  Table method table in README.

`CHANGELOG.md`'s v0.0.x entry still names `Table.DropColumn` as one of the
four methods the script-argument fix repaired. Left alone deliberately: it
records what was true of a released version.

Verified: `gofmt -l`, `go vet ./...`, `go test ./...` clean in gosmo; gossms
builds and its full suite passes against the local checkout through the
active `replace`.

`docs/open-threads.md` compacted in the same pass — 245 lines to ~135. Both
column-DDL entries removed, the closed `ExecProc` line removed (the fix is
recorded in `execproc-decltype-fix-2026-08-06` above), the closed
text-selection-drag paragraph removed, and the now-empty "Follow-ons from the
2026-07-30 two-repo review" and "Left open by the second review" headings
folded away. Activity Monitor had two entries (a stub section and an unbuilt
feature) and is now one. The `formatValue` `float32` note and the `USE
master;` finding moved into "By design — do not re-raise", where they always
belonged: both are do-not-re-raise records, not open work.

## activity-monitor-increment-1-2026-08-06

The Activity Monitor, built in four phases against the work order in
`todo/mockups/ACTIVITY_MONITOR.md`. This is what actually happened and what
the live testing caught, including the four places the implementation
deliberately departs from that work order.

**What shipped.** `internal/tuikit/charts` (eighth-block charts and an
off-screen canvas), `internal/tui/dashboard` (the History and Sample
dashboard layouts over a plain view model), `cmd/amdemo` (a deterministic
mock harness), `internal/tui/activity_monitor*.go` (the panel: four tabs, a
toolbar, two-axis scrolling), and `internal/activity` (DMV collection, rate
derivation, a 30-minute store, and the collector goroutine). Sessions and
Block are placeholders; that is increment 2.

**The four deviations, in one line each.** One collector, not two — Sample
is History's newest sample, so both tabs describe the same instant and the
query load halves. Both dashboards render into a fixed canvas the panel
scrolls, rather than reflowing. Connections are per tab but opened lazily.
`VIEW SERVER STATE` is checked once and its absence stops the collector with
a message, rather than being worked around per metric.

**Bugs found by driving the binary, not by tests.**

`PWAIT_EXTENSIBILITY_CLEANUP_TASK` — the one worth remembering. The waits
panel came up with a Y axis of 150K and a single full-height column,
everything else flattened to nothing. Steady-state probing showed maximum
wait deltas of about 1,500 ms, so the spike was intermittent and did not
reproduce on demand. Chasing it took a temporary live probe inside the
package (a `TestLiveProbe` guarded by an env var, deleted afterwards) that
printed per-type signal deltas whenever the total crossed a threshold. The
culprit: a SQL Server 2025 background task that sleeps for five minutes and
then reports all 300,000 ms of it against one two-second sample. The fix is
not another name in the exclusion list — it is excluding whole families by
`LIKE ... ESCAPE` (`PWAIT\_%`, `SLEEP%`, `QDS\_%`, and nine more), because
every release adds background waits and a list of names is always one
release behind. Both directions are now pinned: the background waits are
excluded, and the waits worth showing survive.

Three from phase 3, all from rendering the panel at 100 columns: the
collector state was drawn right-aligned across the whole toolbar row and
then partly overpainted by the buttons, leaving stray letters between them;
the header's PAUSED marker and sample time live on the canvas and scroll out
of view on a narrow terminal, so the toolbar now repeats them on a row that
never scrolls, dropping parts longest-first; and the toolbar claimed to be
"collecting" when nothing was, which is now driven by a real flag.

One from phase 2, caught by a test rather than the screen: a section bar
starting past the bottom of its rect painted a filled stripe over whatever
sat below the dashboard.

**Live verification.** Against ubudock: History and Sample under a throwaway
`amload` database running a 4,000-iteration insert/select loop (batches,
index searches, locking and log waits all moved and returned to baseline),
pause confirmed to freeze both tabs, the rate selector confirmed by click
and by key, and `sys.dm_exec_sessions` counted before, during and after —
2 → 3 → 2, so the panel's own connection is released on close. The database
was dropped afterwards.

## editor-word-select-and-block-editing-2026-08-06

Three query-editor gaps from `todo/todo.txt`: double-click didn't select a
word, block (column) selection could only select and delete — not type into —
and a block copy pasted back as an ordinary stream of lines.

**Word select.** `core.WordBoundsAt` expands both ways from the clicked rune
using the same word/punctuation class split the existing `WordBoundary*`
navigation helpers use, so double-click and `Ctrl+Left/Right` agree on where a
word ends. tcell reports no click count, so `Editor.pressIsDouble` pairs
presses itself the way `DataGrid` already pairs separator presses for its
restore-default-width double-click; both use 500 ms.

**Block editing.** The block selection already existed as an interpretation of
the same anchor/cursor pair (`selBlock`), so the new part is only that it
*survives* an edit: `blockInsertRune` and friends leave a zero-width block
armed one column over, which is what lets a run of typed characters keep going
into every row instead of the first one collapsing the block. `blockEditing()`
exists because `HasSelection()` reports false for exactly that state. Rows
shorter than the block's column are padded with spaces on insert (SSMS and
Notepad++ both do this) but not on delete — there is nothing there to delete.

**Block paste.** Nothing marks a block copy once it reaches the OS clipboard,
so `SelectedText` remembers the text it handed out for a block selection
(`blockClip`) and `Paste` compares its argument against it. Notepad++ tracks
the same thing internally. Anything else — text from another application, a
plain multi-line copy — still pastes linearly.

**Found by driving it, not by tests.** A zero-width block copies as a run of
newlines unless `SelectedText` special-cases it, which would have made `F5`
execute a blank query after any column-mode typing. Fixed in `SelectedText`
and pinned.

One existing test needed rethinking rather than fixing:
`TestSummaryViewerReleaseClearsDragLatch` clicked twice on the same spot of a
one-character cell viewer, which is now a double-click and selects a word. It
spends a click pair first, since a double-click doesn't pair with a third
press.

**Live verification.** Under tmux: `Alt+Shift+Down` twice then `XY` put `XY`
into all three rows (padding the short one), two `Backspace`es took it back
out, block copy pasted rectangularly past the end of the buffer padding the
appended rows, and an SGR double-click (`\033[<0;38;4M` ×2) highlighted the
whole word.

## review-batches-a-and-b-2026-08-06

Batches A and B of the 2026-08-06 cross-repo review. A was four independent
correctness fixes; B was the draw path. Both documents' status lines are
updated in the plan itself — this is what was learned doing it.

**The plan's proposed cache invalidator was wrong.** Batch B says to
invalidate the cached canvas on "sample count, canvas size, or header state".
Sample count doesn't work: `activity.Store.Append` prunes by time, so once the
30-minute window fills, `Len` sits at a constant while every sample under it
scrolls — a dashboard keyed on it would freeze after half an hour and never
say so. `ActivityMonitor.viewGen`, bumped wherever the view models are
rebuilt, is the key that actually tracks the data.

**Where the allocations were.** `Canvas.SetContent` built a string per cell
(`string(append([]rune{primary}, combining...))`) and then measured it with a
full grapheme segmentation. A cell now holds a primary rune plus the rest of
its grapheme, so a single-rune write — which is every chart glyph —
allocates nothing and measures with `displaywidth.Rune`. `Get` composes the
string on demand for the callers that want one, and `Blit` reads cells
directly instead of going through it. That plus a single-segment fast path in
`composeStack` took `BenchmarkDrawHistory` from 20,685 allocations to 2,183,
and 6.36 ms to 3.05 ms.

**The double scale pass went away without a cache.** `dashboard.drawChart`
drew a chart and then called `Plot` for the hit rect, which recomputed
`maxValue`/`maxStackTotal` and the whole layout. Both history charts' `Draw`
now returns the plot rect it just used. Adding a return value to `Draw` is
source-compatible — existing `c.Draw(s, r)` call sites still compile — so
`Plot` stays for callers that only hit-test.

**Verified by A/B capture, not by tests.** `cmd/amdemo` was built twice, once
with the three chart optimisations reverted in place, and both were driven
under tmux with `capture-pane -p -e`. History and Sample came back
byte-identical including every SGR sequence, which is the only way to be sure
a cell-composition change didn't shift a colour somewhere in eleven charts.

## review-batch-c-2026-08-06

Batch C of the 2026-08-06 cross-repo review — documentation and dead surface.
Five of the seven items were comment or query corrections; the two that
needed a design decision were both wired up rather than trimmed, as the batch
recommended.

**`fileRow.isLog` had no reader because `fileDeltas` grouped by database
alone.** It now groups by database and file kind, so a database contributes a
data row and a log row, and `FileIO.Label()` renders the log half as
`"db (log)"`. Log writes are small and sequential and data writes are not; a
combined row hides which of the two a latency spike came from, which is what
the DMV comment had been claiming the column was there for all along.

**The waits split reverses a documented attribution, deliberately.**
`waitDeltas` used to report each category's *resource* time in its own bucket
and push every category's signal time into `WaitCPU`, with a test pinning it.
That model cannot produce a per-category resource/signal split — the signal
half has already been moved somewhere else by the time a bar is built. It now
returns the whole wait per category plus a parallel signal-per-category array
(`Sample.WaitsSignal`), and the Sample tab draws each bar as a red resource
part under a green signal part with a Resource/Signal legend, which is what
`SampleView.WaitLegend` and `charts.Bar.Parts` were built for and what only
`cmd/amdemo` had been exercising. `CPUPctOfWaits` is unchanged. Consequence
worth knowing: the History waits stack now plots total wait per category, and
its CPU band is no longer inflated by every other category's signal time.

**Live verification is what confirmed both.** Under generated CPU and I/O
load against ubudock, the Sample tab's CPU wait bar decoded as six red cells
under six green ones in a `capture-pane -p -e` dump — the resource part below
the signal part — and the DATABASE IO panel named `test_new_db (log)`
separately from its data file. Neither is something a unit test would have
shown; the drawing code was already correct and simply had nothing to draw.

**The `sessions.go` cut-off mismatch was real.** Sessions were counted with
`is_user_process = 1` and requests with `session_id > 50`, under one comment
calling the latter "the conventional user-session cut-off" — two definitions
under one heading, which makes the request counts look wrong against the
session count at the edges. One `LEFT JOIN` from sessions to requests now
gives all five numbers one definition. Run against ubudock before committing:
`2 0 0 0 0` on an idle instance, with the collecting connection correctly
excluded from the request counts.

## review-batch-d-2026-08-07

Batch D of the 2026-08-06 cross-repo review — undo memory. The batch offered
a byte cap or per-edit deltas and recommended the cap; that is what landed,
with deltas written down in `docs/open-threads.md` § Designed and deferred.

**A step cap alone never bounded undo memory.** `maxUndoSteps` is 500 and
each step is a full deep copy of the buffer, so a 20,000-line script has a
2.5 GB worst case — the existing comment claimed the step count bounded
memory "to a fixed multiple of one snapshot's size", which is true and also
useless when the multiple is 500. `editorState` now carries the snapshot's
approximate size, measured during the copy it already performs, `Editor`
tracks the running total, and `trimUndo` evicts oldest-first until the stack
is inside both the step cap and a 64 MB byte cap.

**The newest step is exempt from the byte cap.** A document whose single
snapshot exceeds 64 MB would otherwise get an empty undo stack and silently
lose the one undo a user is most likely to reach for. `trimUndo` stops at one
step; a test pins it with a 20,000-line, 1,000-column document.

**Eviction copies into a fresh slice.** `e.undoStack = e.undoStack[1:]` left
every dropped snapshot reachable behind the slice header, so the eviction
freed nothing at all — the same trap `Store.Append` in
`internal/activity/store.go` already documents avoiding.

Verified under tmux beyond the tests: typing into a query panel, then six
undos and a redo, still steps one character at a time.

---

## review-batch-a-2026-08-07

Batch A of the 2026-08-07 cross-repo review — the three bugs that review
turned up. All three landed. The review itself deliberately looked where the
2026-08-06 pass had not: the fourteen `agent_*` files, the Activity Monitor's
collector lifecycle, and the property-page edit/apply seam.

**The Steps page listed every subsystem's steps and wrote them all back as
T-SQL.** `pageJobSteps`' doc comment claimed CmdExec/PowerShell/SSIS were
"excluded"; nothing excluded them — `Job.StepsContext` returns every row of
`sysjobsteps`, and `jobStepEdit.request()` hardcoded `Subsystem: "TSQL"`,
which `JobStep.UpdateContext` always sends. So a PowerShell step this page
updated became a T-SQL step still carrying its PowerShell script. The fix
keeps the listing whole (a Type column, so a mixed job doesn't look like it
has fewer steps than it has) and makes non-T-SQL steps read-only, guarded at
the top of `commitCurrent` — refusing to copy the form back is what keeps
such a step out of `changed()` and so out of apply entirely.

**The same page silently rewrote a step's target database.** `indexOf`'s own
doc comment names the hazard — a 0 fallback is only safe when `items[0]` is a
sentinel — and `dbNames` had none, so a step whose `database_name` is NULL
(every non-T-SQL step) or dropped selected the alphabetically first database
on the server, and `commitCurrent` wrote it straight back. gosmo's
`UpdateContext` goes out of its way to treat an empty `Database` as "keep";
the UI defeated that by never producing an empty value. Now an `(unchanged)`
sentinel leads the list, resolved through `indexOfOK`, and maps back to `""`.

The trigger for both was one click: `commitCurrent` runs on every grid row
change, so selecting a second step was enough to make the page dirty, and
`PropDialog` applies every dirty page.

**Live A/B on win10cli was what made both undeniable.** Throwaway job with a
T-SQL/`msdb` step, a CMDEXEC step, and a T-SQL/`master` step; select the
CMDEXEC row, select the next row, OK. Pre-fix, `sysjobsteps` came back with
`2|two_cmdexec|TSQL|HealthClinic|cmd /c echo hello`. Post-fix, byte-identical
to the pre-run state — and changing step 1's database `msdb` → `master`
through the dropdown still applied correctly, so the read-only guard didn't
cost the page its actual job.

**`Collector.send`'s `case <-c.stop` escape was only real if someone called
`Stop`.** Both collectors' `Run` returned without closing `stop` on two paths
(the `VIEW SERVER STATE` prologue failing, and `ctx.Done()`), after which the
8-slot `control` buffer was the only thing absorbing `SetRate`/`SetPaused` —
both called straight from the UI goroutine. `defer c.Stop()` at the top of
`Run` makes the invariant hold by construction. Three committed tests in
`internal/activity/collector_test.go`; A/B'd by reverting the two `defer`s,
all three fail with `SetPaused/SetRate blocked after Run returned`.

**The severity claim on that one was wrong, and live testing is what caught
it.** The review called it an application freeze. It is not reachable today:
gossms cannot connect at all without `VIEW SERVER STATE` (verified with a
disposable `zz_noviewstate` login — the connect fails in `loadServerInfo`, so
no panel is ever created), and the Activity Monitor's connection is its own,
cancelled only by `ActivityMonitor.Close`, which calls `collector.Stop()` on
the line above. Verified live: Activity Monitor open, File > Disconnect,
fifteen Pause clicks — still responsive. What is left is a latent trap the
Sessions/Block increment would walk into, which is reason enough for a
one-line fix, but the plan document now says so rather than the opposite.

**The toolbar gating is `am.collector != nil && !am.collecting`, not
`!am.collecting`.** `am.paused` is a preference the panel carries into
`startCollector`, so a panel still connecting has to keep its controls live.
The over-broad first version was caught by
`TestActivityMonitorHeldClickFiresOnce`, which builds a monitor with no
collector at all.

## review-batches-bcd-2026-08-07

Batches B, C and D of the 2026-08-07 cross-repo review, in one pass after
Batch A. Nothing committed.

**The New Job dialog had A3's bug in the other direction.**
`new_job_pages.go` is a copy-sibling of `agent_job_props_steps.go` and used
the same `indexOf`-into-a-sentinel-less-list pattern, so a step the user
never chose a database for was created against whichever database sorted
first rather than against the server's default. It gets a `(default)`
sentinel rather than A3's `(unchanged)`, and the two constants stay separate
deliberately: a step that does not exist yet has no database to leave alone,
so `""` here means "omit `@database_name` and let `sp_add_jobstep` decide".
A/B'd live on win10cli with `new_job_pages.go` restored from `HEAD` — same
keystrokes, `1|s1|TSQL|HealthClinic|SELECT 1` before,
`1|s1|TSQL|master|SELECT 1` after.

**"Disable the row" had to be spelled with `SetItems`.** The Files page
showed `PRIMARY` as a log file's filegroup for the same not-found-0 reason.
`propsheet.SelectRow` has no disabled state — `TextRow.SetEnabled` has no
counterpart there — so the LOG case swaps the dropdown's items for a single
`(not applicable)` entry and `pickedFilegroup` reads it back as `""`.
`typeSelect.SetOnChange` covers the new-file path, where the type is chosen
rather than loaded. Reproduced live before the fix: selecting the log row put
`PRIMARY` in the box, selecting the data row committed it, and the next grid
rebuild showed `HealthClinic_log | LOG | PRIMARY`. Both dialogs were
cancelled, so nothing reached the database either way.

**`endLoad` had a copy-sibling the review missed.** The plan named
`explorerNode.endLoad` as the one place a `context.CancelFunc` was dropped
rather than called; `completionInventory.endLoad` is the same method and had
the same defect. Both fixed. The interesting part was making the tests bite:
the existing ones only checked that `cancelLoad` came back `nil`, which is
true either way. The new assertion is on the context — `ctx2.Err() !=
context.Canceled` — and reverting the fix produces
`endLoad(current seq) dropped cancelLoad without calling it (ctx err=<nil>)`.

**`xmlOpenBlock` was kept rather than deleted, and the test is what earns
it.** It has no production callers because `XMLHighlighter` reads
`prefixStates` instead, which is the whole point: it is the O(idx) reference
implementation the O(1) cache has to agree with, the same role
`startsInBlockComment` plays for SQL. `TestXMLPrefixStatesMatchFullReplayAcrossEdits`
now checks the cache against it after six edits that open and close comments
and CDATA sections. A/B'd by widening `prefixStates.replay`'s converge test
from `i > from` to `i >= from`, which breaks the cache without touching the
step function: the new test fails with `after edit 0 (row 6 -> "<b/>"): line
7 open-block state = 1, want 0`, and so does the SQL sibling. Deleting the
function would have left that invariant unpinned.

**Batch C stayed a comment-only batch on purpose.** `rowFetchSemaphore`'s
per-call bucket is a behaviour question, not a documentation one — the
comment now states the real bound (per loader, so two folders fan out to 16)
instead of reasoning from a global that does not exist. Moving the bucket to
package level is still available and still costed; it just is not a docs fix.

`staticcheck ./...` is clean on gossms after D, for the first time in this
review.

## xml-cell-panel-by-column-type-2026-08-07

Two changes, both driven by `master.dbo.sp_block` on ubudock, whose `[sql
text]` column is `try_cast('<?query --' + st.text + '--?>' as xml)`.

**"Show Value" on an xml cell opened the grid popup, not an XML panel.**
`classifyCellValue` sniffed shape only — first byte `<`, last byte `>` — and
that value's last byte is `;`. SQL Server serialises the fragment with its
text nodes entity-escaped, so whatever closes the processing instruction
early leaves the value ending in `--?&gt;`, which reads as plain text. Fixed
by making the declared column type win: `DataGrid.OnShowValue` now passes the
cell's column *index* (names in one result set can repeat), `QueryPanel`
resolves it through the new `columnType` against the active result set's
`ColumnTypes`, and `classifyCellKind` maps `xml`/`json` straight to their
panel kinds, falling back to the old text sniff when the type says nothing —
an `nvarchar(max)` holding XML still opens as XML. A NULL or empty cell in an
xml column stays with the popup; there is nothing to open.

Found by driving the built-in binary under tmux against the live server, not
by a test: the value's tail was only visible from
`right(convert(nvarchar(max), @x), 20)` in sqlcmd. The first probe of that
tail was itself misleading — the ad-hoc query matched *its own* session in
`sys.sysprocesses`, and its text contained a literal `--?>`, so the escaping
it showed had a different cause than the one in the procedure's output.
`OBJECT_DEFINITION` is what made the reproduction honest.

**Output Column Metadata now bracket-quotes column names**, `]` doubled:
`[sql text] xml` rather than `sql text xml`, so a name with a space or a
keyword reads as one identifier and pastes into a query. An unnamed column
still shows its bare 1-based position — a position is not an identifier to
quote.

---

## 2026-08-07 — Activity Monitor Block tab

`activity-monitor-block-tab-2026-08-07`

*The Block tab became a real result grid over `sp_block`, with its own
connection, a master/tempdb lookup, and an "Install in master" button.*

User request: the Block tab runs the `sp_block` procedure from
`todo/mockups/sp_block.sql` and shows its output in a result grid with the
grid's full behaviour. On first showing, look for the procedure in `master`;
if it isn't there, look in `tempdb`; if it's in neither, create it in
`tempdb`. Offer an "Install in master" button beside Refresh whenever master
hasn't got it, behind a Yes/No confirmation. Use a dedicated connection, and
never drop the tempdb copy on teardown — a SQL Server restart is what removes
it, and that is understood.

`todo/` is scratch, not source, so the procedure body lives in
`internal/activity/block.go` as `blockProcScript`, with `FindBlockProc`
(one round trip answering both databases) and `InstallBlockProc`. The tab
itself is `internal/tui/activity_monitor_block.go`: connect → resolve →
install-if-absent → run, each step through `safego`/`postAndWake`, with
`blkBusy` gating every toolbar control in between. The result goes through
`query.Execute`, which is what gives the grid its `ColumnTypes` — the
`[sql text]` column comes back as `xml`, so `OnShowValue` opens it in a
highlighted panel exactly as a query panel's grid does (see
[[xml-cell-panel-by-column-type-2026-08-07]]).

**Two live-server findings, both from the `sp_` prefix, neither catchable by
a test.** An `sp_`-prefixed procedure name falls back to `master` when the
current database has no such procedure, and that changes what DDL means:

1. `create or alter procedure dbo.sp_block` run in `tempdb`'s context finds
   master's copy, decides it is altering *that*, and fails with
   `Invalid object name 'dbo.sp_block'`.
2. Far worse: `drop procedure if exists dbo.sp_block` run in `tempdb`'s
   context **drops master's copy**. The first version of `InstallBlockProc`
   did exactly that as a "replace" step and deleted `master.dbo.sp_block` on
   the win10cli test server; it was restored from
   `todo/mockups/sp_block.sql` and verified byte-identical against a
   pre-captured `OBJECT_DEFINITION`.

Note what the fallback means: the hazard lands squarely on the *install*
path, since that is by definition the case where the current database hasn't
got the procedure. Pinned afterwards with a disposable `sp_zzdrop` in both
databases — the drop then took tempdb's copy and left master's alone, so the
rule really is "current database first, master as fallback", not "master
always".

**The fix, at the user's suggestion: drop the prefix.** The tempdb copy is
installed as `usp_block`, so it is an ordinary object resolved in the
database the DDL was issued against, and `CREATE OR ALTER` is safe and
idempotent there. master keeps `sp_block`, the name a hand-installed copy
already has, and `CREATE OR ALTER` is safe there too because master *is* the
fallback target. `BlockProcLocation` now carries `Name()`/`Qualified()` and
every `EXEC` names its database, since neither name is reachable unqualified
from an arbitrary database context.

The install goes to `<db>.sys.sp_executesql` with the script as a parameter:
the three-part procedure name puts the `CREATE` in the target database's
context without a `USE` (this runs on a pooled connection, whose database has
to be left as it was found — verified with `DB_NAME()` afterward), and a
parameter avoids escaping a body full of single quotes.

**The overlay bug the tmux run caught.** `DataGrid.Draw` does not draw the
context menu or the value popup — `DrawOverlay` is a separate call that has
to come after everything else in the frame. Without it the right-click menu
opened, swallowed every event through `OverlayActive`, and was never drawn,
which looks exactly like a dead right-click. `QueryPanel.Draw` had the call
all along; the Block tab now does too.

Verified live on both servers: the blocking chain rendered with a real
blocker/victim pair (`WAITFOR` holding an X lock, `LCK_M_S` indented under
it), Refresh re-ran it, right-click → Show Value opened the `[sql text]`
cell as XML. The whole none-in-master path was exercised on win10cli by
temporarily renaming master's copy aside: the tab installed into `tempdb`,
reported "sp_block in tempdb", offered "Install in master", and after the
confirmation reported "sp_block in master" with the button gone. Everything
renamed back and diffed afterward.

---

## 2026-08-07 — Activity Monitor Sessions tab over sp_WhoIsActive

`activity-monitor-sessions-whoisactive-2026-08-07`

*The last placeholder tab became a result grid over Adam Machanic's sp_WhoIsActive, and the Block tab's machinery was generalized so both tabs are one implementation*

User request: make Sessions run `sp_WhoIsActive`, no auto-refresh (once on
first showing, then Refresh), the query panel's result grid, an
acknowledgement at the top, a Help > About reference, and "the same way as
sp_block is working including the usp_ part".

**One implementation, two tabs.** Copying `activity_monitor_block.go` would
have duplicated ~460 lines across the Go and TUI sides, so `BlockProcLocation`
and its four methods became `activity.Proc` — a procedure described by its
master name, its tempdb name, and a `script(name)` closure — plus a plain
`ProcLocation`. `internal/tui/activity_monitor_proctab.go` is the tab: one
`amProcTab` per procedure, held as `ActivityMonitor.blk` and `.sess`, selected
by `procTab()`. Everything the Block tab already got right is now got right
twice for free: the private connection (a run can take seconds, and a
two-second sample tick must not queue behind it), `DrawOverlay` after
`Draw`, the `ButtonNone` forward to every latch-bearing grid, and Tab
belonging to the panel rather than the grid.

With Sessions no longer a placeholder there are no placeholder tabs left, so
`drawStub`/`refreshStub`/`stubStatus` went with it.

**The licence.** sp_WhoIsActive is GPL-3.0; goSSMS was MIT at the time this
was written. Raised before writing anything, since embedding it is not a
detail: the user chose to embed it, so the repo carries the upstream
`LICENSE` as `internal/activity/LICENSE.sp_whoisactive`, the embedded
`internal/activity/whoisactive.sql` opens with a GPL §5(a) modification
notice, and README's License section says what belongs to whom.

**goSSMS was relicensed GPL-3.0-or-later in the same session**, which is what
the repo carries now: `LICENSE` is the full GPL-3.0 text and README § License
says so. Anything below in this entry that reads "MIT" is describing the
state before that. The two
modifications are exactly what that notice records: the release script's
SET-options and stub-`CREATE` batches removed (`sp_executesql` runs one
batch, and `GO` is not a statement), and the declaration rewritten at install
time.

**The self-referential trap in the notice.** The install rewrites the single
`ALTER PROC dbo.sp_WhoIsActive` line with `strings.Replace(..., 1)`. The
modification notice, as first written, quoted that line verbatim — so the
first occurrence was the comment, and the rewrite would have landed there and
left the real declaration creating `sp_WhoIsActive` in tempdb: the exact `sp_`
fallback disaster the `usp_` rule exists to prevent, arrived by a different
road. The notice now describes the line instead of quoting it, and
`TestWhoIsActiveHeaderAppearsOnce` pins the count at one.

**Verified live on both servers.** ubudock already had
`master.dbo.sp_WhoIsActive`, which exercised the prefer-master path; win10cli
had neither copy, which exercised the install-into-tempdb path. Both: the
install left `DB_NAME()` unchanged, re-installing over an existing copy
worked, master's copy was untouched by the tempdb DDL, and the procedure
returned its full 23-column result. Then driven under tmux against win10cli
with a held `WAITFOR DELAY` session: the credit row and the grid rendered
together, the status line read `1 row(s) 12:56:07 (tempdb.dbo.usp_WhoIsActive)`,
the timestamp did **not** move across eight idle seconds (no auto-refresh) and
did move on `r`, right-click → Show Value opened `sql_text` as XML, and Tab
walked Sessions → Block → History without trapping.

Zero rows on an idle server is correct, not a bug: upstream's `@show_own_spid`
defaults to 0, so the tab's own session is excluded. goSSMS passes no
parameters — same as `sp_block`.

---

## 2026-08-07 — second cross-repo review: the collector rewrite

`review-second-pass-2026-08-07`

*A second review the same day, aimed at the uncommitted Activity Monitor code the first one could not see. Its bug batch was two-thirds "on purpose"; what it was actually worth was the refactor.*

The review's plan file was removed with the other landed plans in the same
pass (see the end of this entry). Four findings in its Batch A,
of which the user took **one**: A2 (the Block tab lists the monitoring
session itself) and A4 (`cross apply dm_exec_sql_text`, `ecid`) are
deliberate and are now recorded as such in `open-threads.md`; A3 (the unused
`dm_exec_cursors` join) the user fixed by hand while the review was being
written.

**A1 — a dead collector still reported itself as collecting.** The morning's
review had given both collectors `defer c.Stop()`, which fixed the hang and
told the panel nothing. `Run` has three exits and reports one of them
(`ErrNoPermission`); a failed permission prologue and a cancelled context
both return silently, so `am.collecting` stayed true, the sample time froze,
and Pause went on toggling its own label while a stopped collector dropped
every send. Now the goroutine is `runCollector`/`runTempDBCollector` — `Run`,
then `postAndWake(collectorStopped)` — and the stopped-callback is checked
against the *current* collector, because a Retry starts a new one whose
predecessor's callback is still in flight.

Retry is the other half: a stopped feed's Pause is replaced by a **Retry**
control that starts a new collector on the connection the panel still owns
(`feedConn`). Before it, one failed prologue turned the panel into a static
picture until it was closed and reopened.

A/B'd by deleting the `postAndWake` line: `TestActivityMonitorLearnsARunReturned`
fails with "the panel still reports collecting after the collector's Run
returned", and passes with it. That test drives the real collector against a
`sql.OpenDB` whose connector always errors, so what it pins is the wiring,
not `collectorStopped`'s arithmetic — which three sibling tests pin
separately (a reported error survives the silent-exit status, a stale
callback is ignored, and Retry replaces Pause).

**C1/C2 — the two collectors and the two stores were one implementation
each, written twice.** `collector[S, Snap]` now holds the prologue, the
select loop, the control channel and the stop latch; `Collector` and
`TempDBCollector` are the exported wrappers that give it its concrete
callbacks and its `probe`/`derive` pair. `sampleStore[T]` likewise, with
retention, detail window and the drop-detail closure passed in per call so
the zero value stays usable — the panel holds both stores as plain value
fields and never constructs them. This is why C1/C2 went **before** A1: the
same fix would otherwise have been written twice, which is exactly how the
morning's `defer c.Stop()` went in twice and how `endLoad`'s copy-sibling was
missed.

**C3 — `amFeed`.** The panel carried seven collector-facing fields twice
(`rateIdx`/`tdRateIdx`, `paused`/`tdPaused`, `collecting`/`tdCollecting`,
`status`/`tdStatus`, `sampleTime`/`tdSampleTime`) and five methods twice, and
re-read them behind `if am.tab == amTabTempDB` in `resolution`,
`collectionState`, `header` and `drawInterval`. All of that is now one
`amFeed` held twice, picked by `feed()`; `buildTools`' two rate arms are one
arm over `f.rateLabels`; `setRate`/`setPaused` are one method each. The
collectors are different types, so the seam is two closures (`applyRate`,
`applyPaused`) re-pointed at each new collector.

Two things fell out of it. The toolbar's stopped-gate is now
`f.started && !f.collecting` rather than `am.collector != nil &&
!am.collecting` — same rule, stated on the feed. And the collector-state line
on the right of the toolbar, which `collectionState()` had always computed
for TempDB, was drawn only on `dashboardTab()`; it is now on every
`canvasTab()`, so a paused TempDB tab shows its PAUSED marker on the row that
doesn't scroll. That was the whole reason the line exists.

**C4 — one spelling of "goroutine with a panic guard".** 36 hand-rolled
`go func() { defer a.recoverPanic(...) }()` became `a.safego(...)` across 19
files, mechanically. Every one already had its defer, so nothing was broken;
what changes is that the next goroutine gets copied from a form that cannot
omit it. `detail_browser_backfill.go` keeps its own `recover` — it has to
queue `markFailed` before `wg.Done` releases the caller — and `ARCHITECTURE.md`
now says so beside the `postAndWake` rule.

**B — the small ones.** `JobStepRequest`'s two string fields read an empty
value differently (`Database` keeps, `OutputFileName` clears) because msdb
does; that is now stated on the fields themselves rather than only on
`UpdateContext`. `scriptDeclType`'s `reflect.Struct` arm says it is the
map's fallback and cannot fire today — noted and kept, per the no-removal
rule. The journal's licence paragraph in the entry above said goSSMS was MIT;
it now records the relicensing to GPL-3.0-or-later that happened in the same
session.

**B1 was dropped, deliberately.** The plan proposed hoisting
`HasViewServerState` out of the two collectors so opening the panel costs one
probe instead of two. A1's Retry is the argument against it: a cached
permission answer would make a retry after a transient failure fail without
asking the server. One round trip at panel open is the cheaper mistake.

**Verified live on ubudock under tmux.** History collecting at 2 s; `-` moved
it to 3 s and then 5 s with the header agreeing; `p` toggled PAUSED and back;
Tab to TempDB showed `TempDB rate: 10 s 30 s 60 s` with its own
`collecting 14:14:51 (30 sec)` — the line that was previously missing there —
and pausing TempDB left History collecting with its own advancing sample time.
Sessions ran `tempdb.dbo.usp_WhoIsActive` and refreshed on `r`; Block returned
its tree.

**Docs cleanup, same pass.** The three landed review plans were removed: the
journal is the permanent record and each of their batches has an entry here.
Entries that cited them by filename now name the review instead.

## 2026-08-07 — Editor undo becomes per-edit deltas

`Editor.pushUndo` deep-copied every line of the buffer, on every keystroke, to
record a one-character change. Measured through `HandleKey` on a 20,000-line
script: **4.5 ms, 5.25 MB and 20,002 allocations per keypress**. The 64 MB
`maxUndoBytes` cap added on 2026-08-06 bounded the *stack*, not the per-edit
copy; this closes the rest of it. Now: **90 µs, 1.05 KB, 5 allocations** — the
new `BenchmarkEditorTypeInto20k` is the permanent measurement.

**The design.** An `editorState` is now a span delta: the lines that occupied
`[row, row+len(old))` before the edit, and `newLen` rows after it. `newLen`
can't be known at push time because the edit hasn't run yet, so the newest
step is left *open* and closed by `finalizeStep` — an edit confined to the
span can only change the line count from inside it, so the span's new extent
is its old one plus the document's net growth. That one observation is what
makes a before-the-edit hook able to record an after-the-edit delta, and
recording `docLen` at push is all it costs.

`Document.replaceRange` is the splice undo and redo apply through, so a
restore moves the version counter once rather than once per line. `undo` and
`redo` are now the same operation: `applyStep` installs a step and returns the
one that reverses it.

**Where the spans come from.** `pushUndo()` still means "the whole document"
and is what the paths whose reach isn't known in advance keep using — Paste,
Cut, Replace All, the line and block actions. The per-keystroke paths call
`pushUndoLocal()`, which derives the span from the cursor and selection and
widens it by one row either side, because Backspace at column 0 joins onto the
line above and Delete at end of line pulls the one below up. Both join cases
were reproduced as document corruption with the widening removed.

**The hazard, and what pins it.** A span narrower than its edit does not fail
loudly — undo restores a document that was never typed. So
`TestEditorUndoRestoresEveryEditPath` round-trips 28 editing paths through
apply/undo/redo and requires the text and cursor back exactly. Mutation
testing: removing the ±1 widening failed it on both join cases with a
duplicated line; making `newLen` ignore the document's growth panicked.

A third mutation *survived* and was the useful one — cloning a step's lines on
the way into the document turned out to be redundant, because every caller
pops a step before applying it and no step is ever applied twice. The clone
came out and the ownership rule went onto `applyStep` in its place.

**A fix the delta exposed.** `trimUndo` copied the retained steps into a fresh
exact-size array, so `cap == len` and the *next* push reallocated the whole
500-entry stack: 38 KB per keystroke once the step cap binds. It had been
invisible behind a 5 MB snapshot. Now it shifts down in place and clears the
vacated tail, which drops the evicted steps' lines just as the fresh array
did, without allocating.

`TestEditorUndoStackCappedByBytes` and `TestEditorUndoKeepsNewestStepOverBudget`
were rewritten onto `Paste`, since ten keystrokes no longer come near either
cap — the caps are real, they just bind on whole-document steps now.

**Verified live under tmux**, not only by the tests: typed three lines,
Backspace-joined two of them, Ctrl+Z and Ctrl+Y round-tripped; 40 undos walked
back to an empty buffer and 40 redos returned it byte-identical; select-all
overtype, and a Delete-join at end of line, both undid clean.

## 2026-08-07 — Second review pass: tooltip staleness, a missing latch, two tidyings

A review of both repos after the undo work. Most of what was swept came back
clean and is recorded here so it isn't re-swept: one `go func` in the tree (in
`safego`), one bare `wakeEventLoop` (the documented `QueryPanel` timer),
`rows.Err()` after every `Next()` loop, and every `fmt.Sprintf` in gosmo that
interpolates a quoted `%s` traced to an `escapeSingle`/`QuoteName`/validated-
enum argument — 27 of them, no injection surface.

**The one real bug: the Activity Monitor's pinned tooltip went stale every two
seconds.** `scrollTo` and `setTab` both drop the tooltip because the canvas has
moved under it, but a *new sample* moves it exactly the same way and nothing
dropped it there. The box kept its old numbers while pointing at a column that
now held different ones. The field comment gave away how it happened — "so a
*paused* dashboard keeps the numbers the user clicked on" — the paused case was
designed and the running case never considered.

Fixed by routing all three `viewGen++` sites through `invalidateView`, which
bumps the counter and clears the tooltip together; the pause behaviour survives
because a paused feed never rebuilds. `rebuild` has one non-test caller
(`applySample`), so the clear fires only on new data.

**How it was proven, before and after.** A throwaway test pinned a tooltip,
appended a sample with a distinguishable value, and printed what the box said
against what the column under it now held. Then the shipped tests
(`...NewSampleDismissesTheTooltip`, `...NewTempDBSampleDismissesTheTooltip`)
were mutation-checked by deleting the `am.tooltip = nil` from `invalidateView`
— both fail. Then a live A/B against ubudock under tmux, driving real mouse
SGR sequences into the pane: pre-fix, a tooltip pinned at 22:34:51 was still
reading `Index searches/sec 10` thirty-eight seconds and nineteen samples later
with the axis showing 22:35:29; post-fix it cleared on the next sample.

**Two smaller things.** `Editor.SetText` cleared four of five mouse latches —
`sbDraggingX` was missing, which is CLAUDE.md's invariant 4 (a latch must not
survive into the widget's next showing). `TestEditorSetTextClearsEveryMouseLatch`
enumerates the fields through a map rather than spot-checking, because the bug
*was* one field missing from a list. And `addSnapshotHit` was a copy of `addHit`
differing only by `Snapshot: true`; they are now one helper in the package's own
`common.go`, taking the flag.

**`editor_undo.go`.** The undo machinery had grown to ~200 of `editor.go`'s 697
lines, mostly my own doing in the previous entry. Extracted by exact line range
per the splitting procedure, and the two files were diffed against the original
to prove the split is a pure move before anything was deleted.

**C1 closed by live query, not by reading.** The standing question of whether
`counterQueryFor`'s instance filter drops NULL-`instance_name` rows was settled
against both servers; the answer and the evidence are now in `open-threads.md`
under "do not re-raise" so the third reviewer doesn't re-derive it.

## 2026-08-08 — Third review pass: a keyboard trap in every grid page, and RESTORE's wrong file list

Two proven bugs, both found by going where the previous two passes hadn't:
the `agent_*` family, the restore path, gosmo's retry/resource layer, and the
`propsheet` row contract.

**Every property-sheet grid was a partial keyboard trap.** `DataGrid.HandleKey`
ends in an unconditional `return true`, so Up at row 0, Down at the last row and
Left at column 0 all reported "handled" having done nothing. `GridRow` forwarded
that verbatim, and `Form` falls back to its own Up/Down navigation only on
`false` — so a focused grid swallowed the keys that were supposed to leave it.
Tab and Escape still worked; `Left`, which is how `PropertySheet` returns to its
page list, did not. An empty grid ate all of them. 21 pages use `NewGridRow`,
and `ToggleGridRow` embeds it.

Fixed in `GridRow`, not `DataGrid`: the grid's blanket answer is right when it
is standalone (QueryPanel results, the Activity Monitor's proc tabs,
DetailBrowser all rely on it), and changing it there would have reached all
three. `GridRow.HandleKey` now snapshots `SelectedCell` and `ScrollCol` around
an arrow key and reports movement rather than predicting it — the same
before/after idiom `TextRow` and `SelectRow` already use. `ScrollCol` was added
to `DataGrid` for it, because a grid with no cell cursor scrolls horizontally
without ever changing `SelectedCell`; without that half, Right/Left over a wide
grid would have been reported as unhandled.

Three mutations, all caught: reverting to the blanket forward, ignoring the
scroll column, and releasing every arrow unconditionally (which the
"Down inside the grid stays in the grid" case exists to catch).

**Live A/B**, Server Properties > Permissions, whose three grids sit in a row.
At the last principal, one Down and typing `ALTER`:

    post-fix   Filter permissions [ALTER                       ]
    pre-fix    Filter permissions [                            ]   (six Downs)

**RESTORE built its MOVE clauses from the wrong backup set.**
`BackupFileListContext` issues `RESTORE FILELISTONLY` with no `WITH FILE = n`,
so it always describes set 1, while the restore ran with `FileNumber`. Appending
to a `.bak` is SSMS's default, so multi-set devices are ordinary. Proven on
ubudock with two throwaway databases backed up to one device: `FILELISTONLY`
returned `zz_set_a`/`zz_set_a_log` where set 2 held `zz_set_b`/`zz_set_b_log`,
and the RESTORE that gossms actually built failed with *Logical file 'zz_set_a'
is not part of database 'zz_restored'*.

gosmo gained `BackupFileListForSet`/`...Context` taking a 1-based set number;
`BackupFileListContext` keeps its exact signature and delegates with 0. The
query moved into `backupFileListQuery` so the `WITH FILE` clause is unit-tested
rather than only reachable through a live server.

**A second manifestation in the same dialog**, found while fixing the first: the
Backup Information view read the file list once, in `analyze`, and never again —
so ←/→ changed the header to another set while the Files Included panel kept
showing set 1's, directly contradicting its own caption ("the restore uses the
one shown"). `selectHeader` now re-reads it, guarded by the same `loadSeq` that
`analyze` uses so a held arrow key can't land a stale answer. `analyze` also
asks for `headers[0].Position` rather than assuming set 1, since a device whose
earlier sets were overwritten starts higher. (**That last reason is wrong** —
overwriting a media set renumbers it back to 1. Measured and corrected the next
day; see the 2026-08-09 entry's P3. The code was safe, for the opposite
reason.)

End-to-end through the TUI: pre-fix, set 2 selected showed `zz_set_a`'s files
and the restore failed; post-fix it showed `zz_set_b`'s and the restore
succeeded, landing `zz_set_b`/`zz_set_b_log` at
`/var/opt/mssql/data/zz_restored_zz_set_b.mdf`.

**Swept and clean, recorded so it isn't re-swept:** the whole `agent_*` family
(3,169 lines — `requireConn` on every entry point including both shared
delegates, `safego`+`postAndWake` throughout, its one interpolated query behind
`sqlStringLiteral`); gosmo's 83 `rows.Next()` loops against 83 `rows.Err()`
checked per-function, not by count; `retry.go`; zero `core.DisplayWidth`
violations; and a structural scan of every function of six lines or more, which
turned up exactly two near-identical pairs, both legitimate parallel
implementations over different types.

---

## 2026-08-09 — Fourth review pass: six scripted setters, a tooltip that had to stop vanishing, and four facts settled on the server

A review of both repos for bugs, inconsistencies, simplifications and
refactoring, worked through as P1–P6. Every finding was A/B'd against a
pre-fix binary or a mutated build, and the four questions that turned on
SQL Server's actual behaviour were settled on win10cli rather than argued
from memory. Nothing was committed.

**P1 — six gosmo setters lied to their own handle under `WithScript`.**
`Database.SetRecoveryModel`, `SetCompatibilityLevel`, `SetReadOnly`,
`SetOffline`, `SetOnline` and `ConfigurationOption.SetValue` each mirrored
the new value onto the receiver immediately after `execContext`. Under a
`WithScript` context `execContext` appends the statement to the collector
and returns nil without touching the server, so gossms's "Script Changes"
button (`prop_dialog.go:344`, which runs every page's apply closure under
`gosmo.WithScript`) left the in-memory handle claiming state the server had
never been given. `setIfApplied` exists for exactly this and was simply not
used in these six places.

The live test that proves it is `live_setifapplied_test.go` (`//go:build
livedb`, DSN via the existing `-livedb` flag — no credentials in the repo,
matching `live_execproc_script_test.go`). **Two ways the first draft failed
to catch its own bug**, both now pinned by comments on the assertions:
asserting state only at the end of the scripted loop nets out, because
`SetOffline` and `SetOnline` write the same field in opposite directions and
a pair that both mirror wrongly lands back on the starting value; and a
scripted `SetOnline` on an already-ONLINE database assigns "ONLINE" over
"ONLINE", which is unobservable. Fixed with per-step assertions plus a
second block that takes the database genuinely OFFLINE first.

**P2 — the Activity Monitor's pinned tooltip was dismissed by its own data.**
`invalidateView` cleared `am.tooltip`, so a box pinned to a chart column
vanished on the next collector tick — two seconds at the default rate — and,
because both collectors land in the same function, a tempdb tick dismissed a
tooltip pinned on History. The first fix was the wrong shape: clearing is not
what a new sample calls for, since the pin is a position in the viewport and
the numbers under it are what changed. `refreshTooltip` now re-resolves the
tooltip from its stored anchor at draw time, after the canvas render has
rebuilt the hit map and before `drawTooltip`; a nil return is the drop.
`Close()` still clears outright, and says why.

Live A/B: the pre-fix binary's tooltip vanished within 3 s; the post-fix one
survived 16+ s with its own time advancing 11:12:51 → 11:13:03 → 11:13:11,
and a TempDB tooltip survived eight activity ticks unchanged. Two existing
tests failed on the new behaviour and were replaced — one of them,
`TestActivityMonitorNewTempDBSampleDismissesTheTooltip`, had a comment
claiming it covered a TempDB-pinned tooltip but never left the History tab,
so it was asserting the cross-tab defect as spec.

**About dialog** — the standalone "Sessions Tab" section became two rows in
Components (procedure version, upstream repo). The GPL-3.0 attribution is
carried by `whoIsActiveCredit()` on the Sessions tab and by the embedded
`whoisactive.sql`'s own copyright header alongside `LICENSE.sp_whoisactive`;
dropping either of those is what would cost the attribution, not this list.
`WhoIsActiveAuthor`/`WhoIsActiveLicense` both still have callers.

**P3 — the backup-set number, and a comment whose premise was false.**
The previous pass (2026-08-08) left `analyze()` reading `headers[0].Position`
"since a device whose earlier sets were overwritten starts higher", guarded
by `len(headers) > 1` — a guard that exempts precisely the case the sentence
describes. **The premise is wrong, measured on win10cli:**

    after 3x WITH NOINIT   → Position 1, 2, 3   (3 sets)
    after WITH INIT        → Position 1         (1 set)
    after WITH FORMAT      → Position 1         (1 set)

Overwriting a media set resets numbering to 1; nothing leaves a lone set
numbered higher. So the guard was safe for the opposite reason to the one
written down. The rule now lives once, in `backupSetNumber`, which the
restore, the MOVE clauses and the Files Included panel all call — two of the
three deriving it separately is what produced the 2026-08-08 bug. Why they
must agree, proven end-to-end on a device holding two *different* databases:

    RESTORE FILE=2 with MOVE from set 2  → succeeded
    RESTORE FILE=2 with MOVE from set 1  → FAILED

**P4 — a zero refresh rate panicked the application, not the panel.**
`internal/activity/collector.go` guarded non-positive rates in `backoff` and
in `retune`, and then panicked before either ran: `time.NewTicker(state.rate)`.
`Run` executes under `App.safego`, so that is the process. `normalizeRate`
now runs at both places `collectorState.rate` is written — `Run`'s argument
and the control-channel `apply` — and `retune`'s `d <= 0` arm came out, since
it was the guard that made the mid-run case *silently* wrong rather than
loud. `backoff` keeps its own guard: that one is its doubling loop's
termination precondition.

Three mutations, three distinct failures, which is what shows the two halves
are both load-bearing:

    original                        panic: non-positive interval for NewTicker
    normalize at Run entry only     panic: non-positive interval for Ticker.Reset
    normalize both, retune inert    1 probes before the rate change, 1 after

`TestCollectorSetRateReachesTheTicker` pins the control path by timing,
because a `SetRate` that updates state but never resets the ticker is
invisible to any other kind of assertion. Live on win10cli, the rate selector
being the changed path: 2 s → 10 s produced samples exactly ten seconds apart
(11:45:54 → 11:46:04 → 11:46:14), 10 s → 2 s two seconds apart, Pause froze
the clock across 9 s, and the TempDB collector — a second instance of the
same code — reported its own 30 s rate.

**P5 — the editor's undo stack: one comment corrected, one guard deliberately
not added.** `maxUndoSteps` claimed the redo stack "can never grow past what
undo popped". True by count, false by bytes: `applyStep`'s inverse carries the
lines being *replaced* — the document as it is now — not the lines the popped
step held, so on a document that grew over its history every inverse is bigger
than the step that produced it. Measured: peak redo 48.4 MB against 46.5 MB of
undo. `inv.bytes` is computed and stored and never accumulated; there is no
`redoBytes` and `maxUndoBytes` does not reach the redo stack at all. No cap was
added — the undo stack's own byte cap bounds it to within one document, and a
redo cap would buy that back in exchange for silently dropping the deepest
redo. `TestEditorRedoStackBound` pins both halves; rewriting `applyStep` so the
inverse carries `st.old` — the world the old comment described — makes the two
totals *exactly equal* (954400 vs 954400) and fails the test, so it
distinguishes the readings rather than merely passing.

`applyStep`'s `e.doc.all()[st.row : st.row+st.newLen]` stays unguarded, on
purpose and now said so on both `editorState.newLen` and `applyStep`. The
invariant is `pushUndoSpan`'s caller promise, and a violated promise means the
document is about to be corrupted; a clamp converts that into an undo that
quietly restores text the user never typed. A drafted per-path assertion was
dropped after A/B showed it cannot fire — on both constructible breakages the
existing "undo left" assertion catches them first, and the genuinely
out-of-range case is unreachable from all 28 paths precisely because they keep
their promise. An assertion that cannot fail is noise.

**P6 — thirteen dropdowns re-derived their database list, and one of them was
wrong.** 16 `DatabasesContext` calls across 12 files; five were byte-identical
name loops. The rule now lives in `internal/tui/database_list.go` and turns on
*when the name is resolved*: stored now and used later (job step, alert, login
default database, restore history) lists every database, system and non-ONLINE
alike, because it is opened when the job runs; acted on now lists only what the
action accepts. Backup is the only dialog in the second class, and both its
exclusions are hard server restrictions, verified:

    BACKUP DATABASE tempdb             FAILED: BACKUP DATABASE is terminating abnormally
    BACKUP DATABASE (offline)          FAILED: BACKUP DATABASE is terminating abnormally
    BACKUP DATABASE (same db, online)  ok

So Backup's `tempdb` filter was not an inconsistency but an *incomplete* one;
with an OFFLINE database on the server the old binary offered it as the
dropdown's default selection.

**The filter then introduced a worse bug, caught before it shipped.** The
Backup dialog is opened *on* a database, `show()` seeds the dropdown with that
one name, and `setDatabaseItems` matches the old selection by name when the
full list arrives. A filtered list doesn't contain an offline database, so the
selection fell through to index 0 — right-clicking an offline database and
choosing Back Up silently retargeted the dialog at whichever database sorted
first and would have backed *that* one up:

    filter, no selection preservation:  gossms_p6_offline → [HealthClinic]
    filter + preservation (shipped):    gossms_p6_offline → [gossms_p6_offline]

`setDatabaseItems` now keeps an unlisted selection at the front. Silently
backing up the wrong database is worse than the error the filter was added to
prevent; the user's choice stands and the server's own refusal is what they
see.

**Process note, recorded because it nearly cost real work:** a
`git checkout <file>` used to undo a temporary A/B edit reverted ~200 lines of
*pre-existing uncommitted* work in `editor_test.go`. Recovered in full from a
dangling blob (`git fsck --lost-found`), verified against two ranges read
earlier in the session and against the set of tests present at HEAD. Revert
points now go to a scratchpad copy, never to git.

---

## 2026-08-11 — Activity Monitor: the chart tooltip tracks its sample

The pinned readout was anchored to a *screen spot*: `refreshTooltip`
re-resolved it from the click coordinates on every draw, so on a live
dashboard the box sat still while the data slid left underneath and started
reporting whichever sample arrived under it — every two seconds at the
default rate. Requested behaviour (todo/todo.txt): the tooltip moves with the
graph, and closes when the point it names leaves the chart.

The pin is now the sample: `amTooltip` holds the chart's title and the
bucket's clock time, and its position is re-derived from those each draw.
Identity is the clock time rather than the bucket index, because a landing
sample renumbers every index under it; `bucketIndex` searches the view's
`Times` newest-first, so two samples inside one second resolve the same way on
every refresh. Three things drop the pin, all of them "there is nothing left
to point at": the chart is gone, the sample has been pruned out of the window,
or `ChartHit.Column` reports it pushed off the left edge of the plot.

New geometry, mirroring what was already there: `charts.ColumnAt` inverts
`BucketAt` (a round-trip test pins them together — a column out and the box
sits beside the bucket it quotes), and
`HistoryChart/StackedHistoryChart.TimeRow` reports the time-scale row for the
same rect `Plot` reports the plot for, carried on `ChartHit.TimeRow`.

A crosshair marking the pinned column was built and then removed at the user's
request the same day: it re-drew that column's cells from the canvas in the
tooltip's background (rather than painting a rule down it, which would have
hidden the very bars the box quotes) and it still read as clutter on a plot
already dense with columns. The callout on the time axis stayed.

Two rendering bugs, both found by driving the binary against win10cli, neither
visible to a test that only asserts the pin:

- The box, placed below-right of the point, covered the time callout it is
  paired with. `place` now takes the callout row as a keep-out and flips
  above.
- The callout overwrote the middle of the axis's own age labels, leaving their
  tails standing as numbers of their own — the axis read
  `-0:211:34:44:56`. `labelRun` widens the cleared span to whole labels by
  reading the canvas row, which is the same rule `drawTimeAxis` already
  applies to its own labels (drop, never truncate).

Verified live: the pinned column stepped 92 → 90 → 88 over two ticks with the
box following and the numbers unchanged, and the whole thing closed on its own
once the sample reached the left edge. Sample's snapshot bar still pins —
one bucket, no time axis, nothing to drift.

Deliberately unchanged: a pan still drops the tooltip (`scrollTo`). It would
survive one now, but panning is the user looking elsewhere, and a box that
follows them there is in the way of what they scrolled to see.
