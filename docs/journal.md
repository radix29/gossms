# Engineering journal

Dated record of the work behind goSSMS and gosmo **since the current tag**:
what was built, what bugs were found and how, and which decisions were made
deliberately. Trimmed at each release — entries for work that has shipped come
out, since `CHANGELOG.md` records what shipped and git history keeps the rest.
Trimmed to `v0.0.6` (2026-08-11) on 2026-08-11.

Nothing here is required reading. `CLAUDE.md` carries the rules that still
apply; `docs/open-threads.md` carries the work that is still open. Newest
entries at the bottom. A `slug` under a heading is a note's name from the
Claude Code memory store this file was migrated out of, kept for older
cross-references.

The v0.0.6 entries — the Activity Monitor and its five tabs, Find and Replace,
block editing, the permissions gap-fill, the per-edit undo rewrite, the four
cross-repo review passes, and the relicensing — are in git history at the
`v0.0.6` tag and its parent commits.

---

## Always On, phase 1: read models and the Object Explorer branch (2026-08-11)

First of a planned four phases (read topology → AG properties → dashboard →
operations). Creating an availability group is deliberately deferred: it has to
execute on *every* replica instance, which needs connection and credential
management no other dialog in the app has.

**gosmo — `availability_group.go`.** `AvailabilityGroup` plus
`AvailabilityReplica`, `AvailabilityDatabase`, `AvailabilityGroupListener` and
`AvailabilityListenerIP`, each `Job`-shaped: unexported `server` back-pointer,
exported fields, paired `Foo`/`FooContext`, and `Seq` variants in `iter.go`.
Version-dependent columns (`basic_features` etc. 2016+, `cluster_type` 2017+,
`is_contained` 2022+) are substituted with typed literals rather than omitted,
so the scan destination list stays fixed across versions instead of the query
shape changing under it.

**The whole design turns on one asymmetry.** `sys.availability_groups` and
`sys.availability_replicas` are cluster-wide and agree on every replica; the
`sys.dm_hadr_*` DMVs only describe what the connected instance can see. Read
from a secondary, every peer replica's role, connected state and health come
back empty, and so does all per-database queue detail. Verified directly: from
ubusql2, `ubusql1` reported `role=` / `conn=` / `health=` all blank while the
group-level `primary_replica` was still correct. So gossms follows the primary
— `db.ServerConn.Peer` (new, `internal/db/peer.go`) opens and caches a
connection to another instance using the same credentials, and every Always On
loader re-reads through it when the local instance is not the primary.

Two things that fall out of that and are easy to get wrong later:

- **The database list is a three-way join, not a DMV scan.**
  `sys.availability_databases_cluster` (cluster-wide) is joined to the replica
  list and then LEFT JOINed to `dm_hadr_database_replica_states`, so a database
  that a replica has not finished seeding appears with empty state instead of
  vanishing. An inner join here silently drops exactly the row a user is
  looking for. The live test asserts one row per (database, replica) pair for
  this reason.
- **An unreachable primary is not an error.** Following can fail legitimately
  (different port, firewall, credentials that don't carry). The loaders fall
  back to the partial local view and append a `NodeError` note naming the host,
  because a half-empty replica list with no explanation reads as a fault.

**Bug found by the live test, not by unit tests.** `create_date`/`modify_date`
on `sys.availability_replicas` are NULL for a replica the instance holds only
as cluster metadata — every row on a secondary, in practice — and scanning them
into `time.Time` failed with `unsupported Scan, storing driver.Value type <nil>`.
`live_availability_group_test.go` (build tag `livedb`, `-liveag`) caught it on
the first run against ubusql1. It now runs green from both the primary and the
secondary; running it from *both* is the point, since the secondary is where
the DMVs return least.

**gossms.** Nine node types, `explorer_alwayson.go` with the six loaders, and
`Always On High Availability` inserted between Server Objects and SQL Server
Agent in `loadServerChildren`, matching SSMS's placement. The folder is listed
unconditionally and reports "Always On is not enabled on this instance" on
expansion, rather than being hidden behind a query at root-expansion time.
`agLabel` takes `(name, primaryReplica, isLocalPrimary)` instead of the group
so it is testable — `IsLocalPrimary` depends on gosmo's unexported back-pointer,
which nothing outside gosmo can populate.

Labels distinguish three states, and the distinction is load-bearing: no
visible primary renders as `(Not synchronizing)`, never `(Secondary)`, because
calling an unknown primary a secondary claims a healthy topology that isn't
there. Same reason `agDatabaseLabel` shows every distinct state across replicas
(`testdb_1 (Synchronized, Synchronizing)`) instead of collapsing them — the
collapsed form hides the replica that is behind.

Verified end to end under tmux against the live two-node cluster, from both the
primary and the secondary; the secondary's tree shows full replica roles plus a
`(read from primary ubusql1)` note. The unreachable-primary fallback is unit
tested but has **not** been exercised live — there is no cheap way to make one
replica unreachable on that cluster without breaking it.

## Always On, phase 2: the AG Properties dialog (2026-08-11)

Second of four phases. Three pages over the existing `PropDialog` shell —
General, Backup Preferences, Read-Only Routing — plus the gosmo write layer
behind them: eight `ALTER AVAILABILITY GROUP ... SET` / `MODIFY REPLICA`
setters on `AvailabilityGroup` and `AvailabilityReplica`, and
`AvailabilityReplica.ReadOnlyRoutingList` to read what the page edits.

**Everything routes through `agOnPrimary`**, which is `resolveAGView`'s
stricter twin: `ALTER AVAILABILITY GROUP` is rejected on a secondary, so a page
that loaded from one would show rows whose Apply the server refuses. The
explorer degrades to a partial view when the primary is unreachable; this
errors out instead. Apply re-resolves the primary and re-reads the replicas
rather than writing through the objects captured at load, and diffs against
originals captured at load — diffing against the *fresh* values instead would
write a stale setting back over someone else's change to a replica this user
never touched.

**Three things the live cluster taught that nothing else would have.**

- **`READ_ONLY_ROUTING_URL = NULL` is a syntax error, and `N''` is rejected as
  "Invalid usage of the option".** The keyword that clears it is `NONE`, the
  same one the routing list uses. Found by the first run of the new
  `TestLiveAvailabilityGroupWrite`; there is no way to guess it from the set
  form.
- **`sys.availability_groups` reports `cluster_type_desc` and
  `automated_backup_preference_desc` in lower case**, while every
  `sys.availability_replicas` `*_desc` is upper case. `AvailabilityGroup`'s own
  doc comment promised "WSFC, EXTERNAL or NONE", and the obvious
  `ClusterType == "EXTERNAL"` test silently never fired. `agColumns` now
  `UPPER`s both, and the live read test asserts it — the fix makes the
  documented contract true rather than papering over it with `EqualFold` at
  every call site.
- **Read-only routing has to be written in three phases.** SQL Server refuses a
  routing list naming a replica with no routing URL, and refuses to clear a URL
  a list still names. Writing each replica's URL and list together failed on
  the very first live Apply. `planAGRoutingOps` orders it: URLs being set, then
  every list, then URLs being cleared — split out from the writing precisely so
  the ordering is unit-testable instead of only discoverable as a server error.

**Two deliberate divergences from the SSMS mockup.** SSMS's Backup Preferences
grid has both a priority spinner and an Exclude Replica checkbox, but they are
one server value — exclude *is* `BACKUP_PRIORITY = 0`. `propsheet.CheckRow` has
no change callback to grey the spinner with, so two live controls over one
value would have made an inconsistent state reachable; the page shows one
`Int` row and an Excluded column that just reports it. And SSMS builds the
read-only routing list with paired list boxes and Up/Down buttons; with no
equivalent control, the list is edited as text in the same
order-and-parentheses form `ALTER` itself uses, with a parser that resolves
every name against the group's replicas so a typo is a row validation error
rather than a server error at Apply.

Dropdowns use the T-SQL keywords (`SYNCHRONOUS_COMMIT`, `READ_ONLY`) rather
than SSMS's prose, matching Database Properties' recovery-model row. `agSetSelect`
widens the item list for any value the server reports that the list doesn't
have, and shows `(unknown)` for an empty one — `SeedingMode` is empty before
2016, and `indexOf`'s not-found 0 would have displayed "AUTOMATIC" as though
the server had said so, then written it on Apply.

Verified end to end under tmux against the live two-node cluster, driven **from
the secondary** throughout: all three pages render with full replica detail
followed from ubusql1, Script Changes emits the exact two ALTERs, and Apply
round-trips both directions — setting a routing URL plus list, then clearing
both — confirmed against `sys.availability_replicas` after each. A General-page
Apply (health check timeout) was checked the same way and restored. The one
thing still not exercised live is `AVAILABILITY_MODE`/`FAILOVER_MODE`: dropping
the last synchronous replica of an EXTERNAL group deadlocks Pacemaker
unrecoverably, so the live write test skips both by design and their statement
text is pinned by unit test instead.

## Always On, phase 3: the dashboard, and AG state in the Databases folder (2026-08-11)

Third of four phases. `AGDashboard` is a panel, not a dialog — SSMS's "Show
Dashboard" is a document tab and this is the same thing: a header rollup, a
replica grid, a per-database grid, refreshed every 10 seconds. It reads through
the primary for the same reason AG Properties does, and says so on screen: the
send and redo queues for a *secondary's* copy of a database are reported by the
primary, so a dashboard built from a secondary would blank exactly the replicas
being watched.

**The two numbers worth having are the two SQL Server does not report.**
Estimated data loss is a secondary's last hardened commit measured against the
*primary's* row for the same database — a different row, which is why
`agComputeDatabaseMetrics` takes the whole result set rather than mapping over
it. Estimated recovery time is redo queue over redo rate. Both are optional,
and that is the point: "no data loss" and "we cannot tell" are opposite answers
to the question this panel exists to answer, so an unknown renders as an em
dash and never as 0s. A caught-up secondary reports queue 0 *and* rate 0, which
is a known zero rather than an unknown — without that special case the healthy
state looked identical to a stalled one.

Verified against real divergence, not just a healthy group: suspending data
movement on ubusql2 and writing 60k rows on ubusql1 moved the panel to
`AAG1 — NOT_HEALTHY`, the replica row to `Not synchronizing / Not healthy /
1 database(s) suspended`, and estimated data loss to **21m 48s**. Resuming
brought it back to HEALTHY on its own next tick, with no interaction. A
secondary that reports a commit *later* than its primary is clock skew between
two rows, not negative data loss, and is clamped to zero.

**Two bugs the live run caught.**

- **The replica grid came up one row short.** It is sized to its own row count,
  and the only layout it had seen was the one from before the first reading
  landed — so a two-replica group showed one replica, and the row it cut was
  the one naming the primary. `apply` now re-splits.
- **F5 never reached the panel.** `app_events.go` intercepted it globally and
  sent it to `executeActiveQuery`, which for a dashboard is a status message
  about there being no query panel. It now gives the active panel first refusal
  before that fallback, exactly as plain Tab already did. QueryPanel's own F5
  branch (`query_panel_input.go`) had been dead code for the same reason and
  does what the fallback did, so query panels are unaffected.

Refreshing rewrites the grids' row slices in place rather than calling
`SetData`, which resets scroll and selection — on a 10-second poll that threw
the reader back to the top of the grid, and to the left of eleven columns,
every tick. `SetData` is used only when the row count actually changes.

**Databases folder.** A database in an availability group now carries its state
in the label — `testdb_1 (Synchronized)` — in the same form the Availability
Databases folder writes it. The state shown is the *local* replica's alone,
which is the one distinction that matters here: that folder lists this
instance's own databases, so annotating one with a peer's state would report
something untrue of the database being listed. SSMS draws the same line.
`agLocalDatabaseStates` returns nil at every failure rather than an error — it
decorates a list that has to render with or without Always On.

## Always On, phase 4: operations (2026-08-11)

Last of the four. gosmo grew the operation layer — `AddDatabase`,
`RemoveDatabase`, `JoinDatabase`, `UnjoinDatabase`, `SuspendDatabase`,
`ResumeDatabase`, `RemoveReplica`, `AvailabilityReplica.Drop`, `AddListener`,
`RemoveListener`, `Drop`, `Failover`, `ForceFailoverAllowDataLoss` — and gossms
got `alwayson_menu.go` plus two create dialogs on the `newObjectDialog` shell.

**Where an operation runs is part of what it means**, and that is the whole
design of `alwayson_menu.go`. Three connections are in play. Membership changes
go to the primary through `agOnPrimary`, which opens a peer connection when the
tree is sitting on a secondary — verified live by adding and removing a
database from ubusql2 and watching it land on ubusql1. Suspend and resume go to
the tree's *own* connection, because their scope is the instance they run on,
and the confirmation says which one the user is about to get: from ubusql1 it
reads "the primary, so this suspends the database on EVERY secondary", from
ubusql2 "a secondary, so this suspends only its own copy". Both confirmed
against `sys.dm_hadr_database_replica_states` — from the secondary only that
one row went to `SUSPEND_FROM_USER`. Failover goes to the replica being
promoted, which is why it lives on the replica leaf and not on the group.

**The cluster type decides which failovers exist**, and the two refusals are
not the same. Probed directly rather than trusted: EXTERNAL rejects both forms
with `Msg 47104`; `CLUSTER_TYPE = NONE` rejects only the lossless form, with
`Msg 47122` ("only force failover is supported"), and performs the forced one.
`agFailoverRefusal` encodes exactly that and explains instead of sending the
statement — for EXTERNAL it names Pacemaker's `crm resource move`, which is the
thing that actually owns the operation. The alert was verified live on AAG1: no
statement sent, group untouched.

**Four facts about SQL Server 2025 that the live probing settled**, all now in
gosmo's doc comments because each contradicts what a reasonable person would
assume:

- `ADD LISTENER` and `REMOVE LISTENER` *are* accepted under an EXTERNAL cluster
  type, unlike failover — SQL Server records the metadata and leaves the
  address to the cluster manager. The group's own listener was removed and
  re-added byte for byte to prove it.
- `PORT = n` parses after `WITH DHCP`, which the documented grammar allows only
  after `WITH IP`. Leaving it off a DHCP listener would have silently given it
  1433.
- After `REMOVE DATABASE`, the secondary's copy is *not* left in RESTORING as
  the docs say. `sys.databases` still reports it ONLINE, and every connection
  to it fails with error 983 ("the database replica is not in the PRIMARY or
  SECONDARY role"). So `state_desc` is not how you find one.
- A removed replica keeps a stale row for the group in its own
  `sys.availability_groups`, which only `DROP AVAILABILITY GROUP` run *there*
  clears. Removing a replica and then dropping the group on the primary still
  leaves it listed on the instance that was removed.

`RemoveReplica` and `Drop` are the two that cannot be tested against AAG1
without destroying it, so they were verified on a throwaway
`CLUSTER_TYPE = NONE` group stood up across the same two instances and torn
down again — which is also where the stale-row fact came from. That group is
gone; `TestLiveAvailabilityGroupOperations` skips both by design.

**One wording bug the live run caught.** The `newObjectDialog` shell hardcoded
`"%s %q created"`, so adding an existing database to a group reported
`Database "tuiprobe" created` — a statement that is simply false, and the kind
that makes a user go looking for a database they did not create. The shell now
takes an optional `verb`.

The test cluster needed one permanent change: `ALTER AVAILABILITY GROUP [AAG1]
GRANT CREATE ANY DATABASE` on ubusql2. Both replicas were already configured
`SEEDING_MODE = AUTOMATIC` but the grant that makes automatic seeding actually
work was missing, so every `ADD DATABASE` would have seeded nothing. It was
left in place.

## Always On, phase 5: creating a group (2026-08-11)

The phase that was deferred at the start, on the grounds that creating a group
runs on instances the user may not have registered. That is still true, and it
is what the design is built around rather than something worked around.

gosmo grew `endpoint.go` — the database mirroring endpoint, read, created,
started, stopped, dropped, and `GRANT CONNECT` — plus
`CreateAvailabilityGroup`, `Join`, `GrantCreateAnyDatabase` and its DENY. The
one-per-instance rule is the fact everything else follows from: a second group
on the same pair of instances reuses the first one's endpoint, so the useful
operation is *read the endpoint*, not create one.

**Creating a group is four statements on three instances**, and that is the
whole shape of `new_ag_dialog.go`. CREATE runs on the instance the dialog is
connected to, which is what makes it the primary. Each secondary then runs JOIN
against *itself* through `db.ServerConn.Peer`, and — if it is to seed
automatically — GRANT CREATE ANY DATABASE, without which
`SEEDING_MODE = AUTOMATIC` seeds nothing and says nothing about it. Add Replica
connects to the instance being added rather than taking the name on trust,
because the endpoint URL has to come from the instance itself and one that
cannot be reached now certainly cannot join later.

**Script Changes needed its own implementation.** The shell emits exactly the
statements it would have run, which here are statements for three different
instances with nothing saying so — run whole against the primary that either
errors or joins the primary to its own group. `annotateScript` labels each with
its target, and the labels shift correctly when a MANUAL-seeding replica skips
its GRANT.

**Two things the live cluster settled that the docs do not say.**

- `FOR` introduces the whole body whether or not there are databases: with none
  it reads `... FOR REPLICA ON`. Dropping it is a syntax error reported against
  the *replica's* `WITH`, pointing at entirely the wrong place — which is
  exactly how long it took to find.
- **Under `CLUSTER_TYPE = EXTERNAL` or `NONE` the group does not exist on the
  secondary until JOIN succeeds.** Only WSFC propagates the metadata ahead of
  the join, so `AvailabilityGroupByName` there returns no rows and there is
  nothing to call `Join` on. That is why `Server.AvailabilityGroup(name)` now
  exists — a lightweight by-name handle, the same split as `Server.Database`
  vs `Server.DatabaseByName` — and why `Join` takes the cluster type as an
  argument instead of reading it off a group it cannot have.

The cluster type also fixes every replica's failover mode, and the first
version of the check got it half right: it rejected AUTOMATIC under NONE but
let EXTERNAL through, which the server answers with `Msg 47101 — only supports
MANUAL failover mode`. `agFailoverModesFor` is now a three-way table, and the
error names the replica and the value to set.

`TestLiveAvailabilityGroupCreate` builds a real `CLUSTER_TYPE = NONE` group
across ubusql1/ubusql2, joins it, grants it, and tears it down — read-scale on
purpose, so it needs no cluster manager and never touches the Pacemaker group
running beside it. It also asserts the stale-row behaviour the teardown depends
on: dropping on the primary leaves the secondary still listing the group.

**The test cluster broke underneath this, and it is worth writing down what it
looked like.** Both instances restarted at 19:30; afterwards every endpoint
handshake failed with `Error 15581 ... while initializing the private key
corresponding to the certificate`, and `ALTER MASTER KEY ADD ENCRYPTION BY
SERVICE MASTER KEY` failed with `Msg 33094 — an error occurred during Service
Master Key decryption`. So ubusql2's Service Master Key no longer decrypts,
its endpoint certificate's private key is unusable, and no replica can connect
to it — AAG1 sits RESOLVING on both nodes while Pacemaker reports itself
healthy, because Pacemaker is watching the resource and not the endpoint. The
repair is `ALTER SERVICE MASTER KEY FORCE REGENERATE`, which is left for the
author to run.

That fault is also what produced the one live exercise of the partial-failure
path: the dialog created the group, `ubusql2` could not download the
configuration (`Msg 47106`), and the message said so — *"availability group
"AAG2" was created, but ubusql2 could not join it"* — rather than reporting a
plain failure for something that had half happened.

---

## Always On, phase 6: the four gaps phase 5 scoped out (2026-08-11)

Phases 1-5 finished with five limitations recorded in `docs/open-threads.md`
as scoped-out rather than unfinished. Four of them were the work order for
this phase; the fifth (per-replica credentials) is unchanged and still open.

### The test cluster was broken first, and not by any of this

Both instances had restarted at 19:30 and every endpoint handshake since was
failing with `Error 15581 ... while initializing the private key corresponding
to the certificate`, in both directions. `AAG1` sat RESOLVING on both nodes
while Pacemaker reported itself perfectly healthy — it watches the resource,
not the endpoint.

The misleading part is that every repair for the obvious diagnosis also fails:
`ALTER MASTER KEY ADD ENCRYPTION BY SERVICE MASTER KEY` gives `Msg 33094`, and
so does `ALTER SERVICE MASTER KEY FORCE REGENERATE`, which reads as
unrecoverable key corruption. It was not. The one line that names the cause is
in the errorlog at startup:

```
Service Master Key could not be decrypted using one of its encryptions.
An error occurred during Service Master Key initialization.
  SQLErrorCode=33095, State=14, LastOsError=2
```

`LastOsError=2` is ENOENT, and `machine-key` was present and mssql-owned the
whole time — it was `/var/opt/mssql/secrets` itself that had become
`drwx------ root root`, so the `mssql` process could not traverse the directory
to reach it. A permissions problem on the directory reports as the file being
missing. The directory had been left that way when the Pacemaker `passwd` file
was created in it the day before, and the damage only surfaced at the next
restart. `chown mssql:mssql /var/opt/mssql/secrets`, a restart of both
instances under `maintenance-mode=true`, and re-adding the SMK encryption to
each master DMK (which survives, because it still has its password encryption)
brought everything back with no certificate or endpoint recreated. Recorded in
the cluster memory note, since nothing about it is guessable from the symptom.

### Cluster-type gating in AG Properties

The Failover mode dropdown offered AUTOMATIC and MANUAL on an EXTERNAL group,
where only EXTERNAL is legal, and left the rejection to the server. The
open-threads entry expected this to need a form-level validator in `propsheet`,
which only has one on `TextRow`. It did not: narrowing the item list with the
create dialog's existing `agFailoverModesFor` is the whole gate, because
`agSetSelect` already widens the list back for a value the server holds that
is not in it. So a replica that is *already* set to something illegal still
displays it and can be corrected, which a validator would not have given.
Verified live against `AAG1` — the dropdown opens with exactly one entry.

### The dashboard's all-groups view and rate selector

SSMS hangs Show Dashboard off the Always On root as well as off each group, and
the root one lists every group. `AGDashboard` now runs in two modes keyed on an
empty `agName`, sharing the refresh loop, layout and input; only the columns
and rows differ. Its two grids were renamed `topGrid`/`bottomGrid` for that
reason — they hold replicas and databases in one mode and groups and replicas
in the other, and a `replicaGrid` holding groups would be a lie a future reader
would act on. The rename immediately caught a test that built a bare
`AGDashboard{}`, which now means the all-groups view.

Each group resolves independently through `resolveAGView`, the Object
Explorer's degrade-to-partial rule rather than AG Properties' treat-it-as-an-
error rule: this page only reads, and a root dashboard that fails outright
because one group of five has an unreachable primary is useless exactly when it
is wanted.

The rate selector is `+`/`-` over 5/10/30/60 s, matching the Activity Monitor's
keys. The refresh goroutine went from a ticker to a timer, because the interval
it re-arms with can now change while it is waiting; a rate change wakes it
through its own channel and deliberately takes no reading, since changing the
cadence is not a request for data now and must not produce one while paused.

### MODIFY LISTENER and multi-subnet addresses

Two statements, and the grammar takes exactly one option per statement — there
is no form that changes the port and adds an address at once. Both verified
live against `ubuaag` and reverted:

```sql
ALTER AVAILABILITY GROUP [AAG1] MODIFY LISTENER N'ubuaag' (PORT = 14330)
ALTER AVAILABILITY GROUP [AAG1] MODIFY LISTENER N'ubuaag' (ADD IP (N'192.168.178.100', N'255.255.255.0'))
```

Two findings worth keeping. There is no `REMOVE IP`: an address is permanent
for the life of the listener, and reverting the test meant REMOVE LISTENER and
ADD LISTENER — which is why Listener Properties offers no Remove button that
would only ever work on rows not yet written. And under an EXTERNAL cluster
type the added address comes up **OFFLINE** in
`sys.availability_group_listener_ip_addresses`, because Pacemaker owns the
address and SQL Server only records it.

The Add Listener dialog now takes a list of addresses. The typed IP/mask fields
count toward the spec without pressing Add Address, because the single-subnet
case is the common one and making the user press a button to commit the one
address they typed surfaces as "listener has neither DHCP nor a static
address".

### Creating the endpoints, without copying files

The documented certificate exchange is BACKUP CERTIFICATE to a file on each
host, copy the files by hand, then CREATE CERTIFICATE FROM FILE. gossms has no
filesystem access to either host, so that route was why phase 5 reported a
missing endpoint as a blocker rather than fixing it.

There is another route, and it is better: `CERTENCODED` returns the
ASN.1-encoded **public** certificate and `CREATE CERTIFICATE ... FROM BINARY`
takes it back, so the exchange is two queries over connections gossms already
has and no private key is read, transmitted or written anywhere. Verified live
before building on it — 973 bytes out of ubusql1, imported into a throwaway
database on ubusql2 as `NO_PRIVATE_KEY` with the subject intact.

That also settles the shape: per-instance certificates with the public halves
exchanged, not one shared certificate copied around. The file recipe produces
the latter, and sharing one certificate requires moving its private key. The
existing hand-built cluster turned out to already be in the per-instance shape
(`ubusql1_Cert` owned by `dbo`, `ubusql2_Cert` owned by `ubusql2_user`, and
`GRANT CONNECT ON ENDPOINT TO ubusql2_login`), which is exactly what the
pipeline now produces.

Every step is skipped when what it creates already exists, so the dialog
completes a half-configured pair rather than failing on it. That is also what
made it safe to test end to end against the live cluster: run against
ubusql1/ubusql2, which are fully configured, it reported *Database mirroring
endpoint "AGEP" configured* and left every certificate, login, endpoint and
grant exactly as it found them, with the AG still SYNCHRONIZED.

One tmux lesson, re-learned the slow way: `grep -bo` gives a **byte** offset,
and a captured line full of box-drawing characters is not ASCII, so clicking at
that column misses. The OK button was at byte 108 and display column 105. Every
click column has to be computed with `east_asian_width` accounting, which
`CLAUDE.md` already says and this session still got wrong once.

## Always On, phase 7: adding a replica, and joining a secondary's copy (2026-08-11)

The two verbs phases 1-6 left with no path in the UI. Neither is new
functionality in the sense of "another dialog like the last one": both are
cases where the group already existed and the only way to change it was a query
panel.

### Add Replica

`AvailabilityGroup.AddReplica` in gosmo takes the same
`AvailabilityReplicaSpec` the CREATE path uses and renders it through the same
`withClause`, so `ALTER AVAILABILITY GROUP ... ADD REPLICA ON` and CREATE's
`REPLICA ON` list cannot drift — `TestAddReplicaMatchesTheCreateReplicaClause`
pins that by cutting the WITH body out of both and comparing them.

`ag_add_replica_dialog.go` is the same three-statements-on-two-instances shape
as `NewAGDialog.createGroup`: ADD REPLICA on the primary, then JOIN and (for
automatic seeding) GRANT CREATE ANY DATABASE through `db.ServerConn.Peer` to
the replica itself. Script Changes gets the same per-instance annotation, for
the same reason — run whole against the primary, the JOIN joins the primary to
its own group.

The endpoint URL is read by a **Connect** button rather than at apply time. It
cannot be guessed, the instance has to be reachable for JOIN to work later
anyway, and reading it early turns an unreachable replica into a message in the
dialog instead of a half-run pipeline. Connect also replaces what was typed
with the instance's own `@@SERVERNAME`: ADD REPLICA addresses the replica by
the name the catalog will report, and an alias or an IP address there produces a
group whose JOIN finds no matching replica.

Defaults come from the primary replica's own settings, and the failover mode
from the cluster type — the only defensible guess in both cases, and under
EXTERNAL or NONE there is exactly one legal value.

### Join / unjoin a secondary's copy

`JoinDatabase`/`UnjoinDatabase` have been in gosmo since phase 4 with no
callers. They are the manual-seeding counterpart of Add Database, and unlike
every other Always On operation they act on **one replica's copy**, not on the
group — which is why they run against the tree's own connection with no
following of the primary.

Gating them needed one more fact than the tree carried. The Availability
Databases folder is read from the primary, but whether *this* instance's copy
has joined is a local question, so `agLocalDatabaseJoinState` re-reads from the
local connection when the folder was followed. The test is having a
synchronization state at all: `AvailabilityGroup.Databases` cross-joins the
cluster-wide database list with the replica list, so a database the local copy
has not joined still produces a row, with every `dm_hadr_database_replica_states`
column empty. Empty means "not joined here", not "unknown".

The menu then shows one or the other, the way Suspend/Resume already does: Join
on an unjoined secondary copy, "Remove Secondary Database from Group..." on a
joined one, neither on the primary. A copy that has not joined has no data
movement either, so the Suspend item goes with it.

### Verified live, on a throwaway group

`AAGTEST`, `CLUSTER_TYPE = NONE`, created on ubusql1 with one replica so that
AAG1 was never touched. Add Replica added ubusql2 through the dialog: the
catalog came back with the full WITH body intact (endpoint URL, both modes,
seeding, priority 50, timeout 10, ALL/NO), ubusql2 reported SECONDARY /
CONNECTED, and the Replicas folder refreshed to show it.

That the GRANT actually ran is not visible in any catalog view worth querying —
what proves it is that `ALTER AVAILABILITY GROUP [AAGTEST] ADD DATABASE
[agtest_db]` then seeded ubusql2 automatically, which is exactly what a missing
GRANT makes silently not happen.

Unjoin left ubusql2's copy RESTORING and the menu flipped to Join on the next
load; Join brought it back to ONLINE / SYNCHRONIZED and the menu flipped back.
The primary's own menu offered neither, throughout. Everything was dropped
afterwards and AAG1 re-checked HEALTHY on both nodes.

One thing worth knowing: `DatabaseMirroringEndpoint.URL()` builds the host from
`@@SERVERNAME`, so ubusql2's endpoint came back as `tcp://ubusql2:5022` while
the hand-built AAG1 uses the FQDN form. Both work here because `/etc/hosts`
resolves the short names — see `gossms-aag-test-cluster` — but a topology where
only the FQDN resolves has no way to say so, since the dialog does not let the
URL be edited.
