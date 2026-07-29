# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`), each verified against the current code at that date.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

## Blocking the next release

- **gosmo is untagged past `v0.0.6`, and `go.mod`'s `replace` is active.**
  Intentional during development (see ARCHITECTURE.md, "Developing against a
  local gosmo checkout"), but a CI release build cannot resolve gosmo in this
  state, and `HEAD` calls gosmo code that no tag contains (e.g.
  `gosmo.Scripting`). Before the next gossms release tag: tag and push gosmo,
  bump `require`, comment out the `replace`/`ignore` pair, verify a clean
  build and test run against the tagged module. Raised 2026-07-18 as "P1",
  deprioritized by the user at the time; still outstanding 2026-07-30.

## Known bugs, deliberately unfixed

- **Script Changes is broken on any create dialog whose later page depends on
  an earlier page having actually run.** New Schedule reports
  `gosmo: schedule "X" not found`: page 2 (Jobs) calls `AttachSchedule`, which
  resolves by name, but under `gosmo.WithScript` page 1's `CreateSchedule` only
  *collected* a statement, so there is nothing to look up. New Job's Schedules
  page and New Alert's Response page are the same shape and likely affected.
  Apply works correctly — script mode alone is broken. Confirmed identical on a
  pre-refactor binary, so the generic `New*Dialog` base didn't cause it.
  Unfixed because the fix is a design choice: script the dependent statement
  blindly, skip dependent pages, or have gosmo's script mode fake the lookup.
  Found 2026-07-28; still present.

## Deferred scope (repeatedly, deliberately)

These have been re-deferred on every properties/dialog pass. They are choices,
not oversights — but they are also the standing answer to "why isn't this in
the UI?"

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first.
- **WITH GRANT OPTION** on any permissions grid — gosmo's
  `Grant`/`Deny`/`RevokePermission` have no with-grant parameter.
- **Effective permissions** (the SSMS tab that resolves role membership) —
  nothing in gosmo resolves it or calls `fn_my_permissions`.
- **Filter / search boxes** on the long securables and permissions grids.
- **Column-level permissions** in Database Role / User Properties.
- Assorted mockup pickers and warning modals in the create dialogs (owner
  picker, owner-change warning, and similar).

## Soft / housekeeping

- `query_panel.go` (679 lines) remains a file-split candidate, along with
  `restore_dialog.go` (696), `planview/planview.go` (685) and
  `backup_dialog.go` (660). Raised 2026-07-14 as "P5"; `datagrid.go` from the
  same item **is done** (split into `datagrid_draw/_input/_overlay.go`, now 450
  lines). Not urgent — CLAUDE.md's file-organization convention already names
  these.

## Closed since being recorded (verified 2026-07-30, do not re-open)

- ~~Disconnecting a server root shortly after connecting leaves a real SQL
  session alive for up to 30s~~ — the completion-inventory load used
  `context.Background()`; `completion_inventory.go` now derives from
  `sc.Context()`.
- ~~Stale-name-after-rename in Login / User / Role / Server Role / Key
  Properties~~ — all now thread the name as `*string` through their page
  closures, as Job Properties did first.
- ~~`datagrid.go` needs splitting~~ — done.
