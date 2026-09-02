# Fixable known issues — plan

Compiled 2026-09-02 from `docs/open-threads.md`, excluding everything that
file marks by design / do-not-re-raise, and excluding unbuilt features README
already promises (Reports, Entra auth, merged log view, per-query time series,
plan graphs). Ordered as agreed; close an item by deleting it.

## User-visible defects / limits

1. ~~**A job's state is binary.**~~ Fixed 2026-09-02 — see
   `docs/open-threads.md` § A job's state comes from Agent, not from msdb.
   One thing is left: the Agent-stopped fallback has no live run.

2. **Column-scoped DENY is invisible to the permission gate.**
   `ProbedObjectPermissions` is `ALTER` alone and gosmo's object block reads
   `class = 1` with `minor_id = 0`, so a DENY on a column (class 1,
   minor_id > 0) is not seen and a wider grant answers for it. Latent —
   nothing gossms writes today is column-scoped.

3. **No Object Explorer filter on the six server-level folders** (Credentials,
   Backup Devices, Server DDL Triggers, Endpoints, Audits, Server Audit
   Specifications). Client-side `filterChildren`/`filterObjects` only;
   `nodeFilter.pushdown` may simply refuse rather than approximate.

4. **Eight `indexOf` dropdown sites fall back to a sentinel** where
   `selectPreserving` would show the real, deleted-out-from-under value —
   `login_props.go:119`, `agent_operator_props.go:66`, `user_props.go:154`,
   `agent_alert_props.go:78`, `:82`, `:210`. Changes documented, tested
   behaviour, so it is its own change, not a drive-by.

5. **Semantic comment-drift batches 3 and 4 unread** —
   `internal/tuikit/controls/datagrid.go`, `editor*.go`, `treeview.go`,
   `internal/tuikit/propsheet/rows.go`,
   `internal/tuikit/layout/panel_manager.go`, then everything else. Batch 2
   found three wrong counts, so the hit rate is non-zero.

## Never-verified paths (unit-tested only)

6. `ServerConn.Peer`'s retry with `parentPeerOptions` when a saved replica
   credential will not connect — pinned only through the option derivation.
   Closes with Always On on ubusql1/ubusql2 plus a deliberately broken saved
   replica password.

7. `resolveAGView`'s degradation to the partial local view, and `agOnPrimary`'s
   opposite choice of treating an unreachable primary as an error. Closes by
   making a replica genuinely unreachable on the cluster.

8. `endpointPrincipalBase`'s `HOST\INSTANCE` -> `HOST$INSTANCE` mapping has
   never run live — needs a second, *named* SQL Server instance on the Windows
   host.

9. `legacyListingRefusal` / the `xp_dirtree` silent-empty guard — unit-tested
   across all five combinations, never run against a real pre-2017 instance.

10. `RemoveReplica` and `Drop` have no live coverage against a real group;
    `TestLiveAvailabilityGroupOperations` skips them. Closes with a throwaway
    `CLUSTER_TYPE = NONE` group across ubusql1/ubusql2.

11. The `Unknown` arm of the "Jobs Without Schedules" report. Needs a
    fault-injection seam that fails one job's round trip while the others
    succeed, not just a live run.

## Hygiene / debt

12. `core.NewRect`, `core.JoinPath` and `activity.WaitCategory.String` are
    genuinely idle. `tuikit` and `internal/activity` are gossms-internal, so
    removal is allowed here — the no-removal rule is gosmo's only.

13. The `internal/tui` package split is not attempted; the cost is in
    § Architecture of the 2026-09-02 cross-repo review.

14. **Column encryption key rotation is a dialog cap, not a gosmo one.**
    `CreateColumnEncryptionKey` takes as many encrypted values as it is given;
    the dialog offers one.

15. **An audit's destination cannot be changed from Properties** even where
    `ALTER SERVER AUDIT` allows it on a disabled audit. gosmo's `AlterContext`
    accepts any destination; the greying matches SSMS.
