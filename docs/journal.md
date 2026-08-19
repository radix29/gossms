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

---

## Backup/Restore Browse now browses the server's filesystem (2026-08-12)

Two reported bugs, one root cause. Backup's and Restore's Browse buttons opened
`dialogs.FileDialog` against the *client's* disks, and `FileDialog` built every
path with `path/filepath`. Since the device path in `BACKUP`/`RESTORE` is
resolved by the SQL Server host, the listing showed directories the server
cannot see; and on a Linux client browsing win10cli, `filepath.IsAbs` says
`C:\...\backup_test_full.bak` is *relative*, so Save joined it onto the working
directory. Reproduced end to end on the pre-fix binary, which scripted
`BACKUP DATABASE [backup_test] TO DISK = N'/tmp/claude-1000/.../C:\Program
Files\...\backup_test_full.bak'` — no error, no warning, just a destination that
could never work.

**gosmo — `filesystem.go`.** `Server.EnumFileSystem`, `Server.FixedDrives`,
`Server.FileSystemExists`, plus `ServerInfo.Platform` (declared since forever,
never populated; now parsed from `@@VERSION`, which works below 2017 where
`sys.dm_os_host_info` doesn't exist). Each has a version-gated fallback —
`sys.dm_os_enumerate_filesystem` → `xp_dirtree` below 2017,
`sys.dm_os_enumerate_fixed_drives` → `xp_fixeddrives` below 2019 — chosen from
`VersionMajor` rather than by sniffing error strings.

The one load-bearing detail is `WHERE level = 0`. `sys.dm_os_enumerate_filesystem`
recurses the whole subtree, so a listing of `C:\` without it enumerates the
entire drive. SQL Server pushes the predicate *into* the function rather than
filtering afterwards, which is what makes the fix free: measured on
17.0.1125.2, `C:\Program Files\Microsoft SQL Server` returned 3091 rows in 2.1s
unfiltered and 6 rows in 0.6s filtered. `C:\Windows` filtered is 101 rows in
0.6s. Without that pushdown the DMF would have been unusable and the whole
listing would have had to go through `xp_dirtree`.

**tuikit — `dialogs/file_system.go`.** `FileDialog` no longer calls `os` or
`path/filepath` anywhere; it reaches the filesystem through a `FileSystem`
interface, whose path half (`PathRules`) is implemented twice —
`WindowsPathRules` and `PosixPathRules` — so path handling follows the *host
being browsed*, not the client. `ShowOpen`/`ShowSave` reset to
`LocalFileSystem` on every call and `ShowOpenOn`/`ShowSaveOn` take one
explicitly, which is what stops a remote filesystem leaking from Backup's
Browse into the next File > Open Query File through the single shared dialog
instance.

`WindowsPathRules.Parent` had to check the trailing-separator case *before* the
drive-letter case: `C:\` ends in the separator and its last path element ends
in `:`, so the drive-letter branch returned `C:\` as its own parent and stranded
the browse at the drive root with no way back out to the drive list. Caught by
`TestWindowsPathRules`, not by reading it.

**gossms — `server_filesystem.go`.** `serverFS` embeds whichever `PathRules`
matches `ServerInfo.Platform` and implements `List`/`Default`/`Exists` over
gosmo. `List("")` is the level above every root: it returns the host's fixed
drives, which is where `..` out of `C:\` lands. Calls are synchronous on the UI
goroutine — the file dialog has no async path — bounded by a 15s timeout so an
unreachable server can't look like a hang.

Two smaller fixes fell out: `App.OnConfirmOverwrite` used `filepath.Base` for
the "already exists" prompt, which on Linux renders the whole
`C:\Backup\db.bak` as the "file name"; and `browseDest` now clears
`lastAutoDest`, so a path the user picked isn't silently regenerated the next
time the database or backup type changes.

**Verified live on both platforms.** win10cli (Windows): browsed
`MSSQL\Backup` → `MSSQL\` → `DATA\`, picked a file, got the server-overwrite
prompt with the right base name, then ran a real `BACKUP DATABASE backup_test`
to a Browse-picked path — 100%, file confirmed on disk at 528384 bytes — and
Restore's Browse found it and read its header. ubusql1 (Linux): `/` separators
throughout, and no `..` row at `/`, where `Parent` returns the root itself.
File > Open Query File after a server browse still lists the local machine.

### Follow-up: the Destination field drew empty for a short path (2026-08-12)

Reported right after the above: browsing to `C:\temp`, naming the file
`aaa.bak` and confirming left the Backup dialog's Destination box **blank**,
while Script generated the correct `C:\temp\aaa.bak`. The value was never
wrong — only what got painted was.

`InputField.adjustScroll` had one job, keeping the caret inside the box, and
did it correctly. But `SetValue` puts the caret at the end of the *new* value,
so replacing the ~90-column default backup path with a 15-column one left
`col = 15` against `scroll = 37`; the "caret is left of the window" rule pulled
`scroll` back to 15 — which is past the last character of a 15-column string.
The caret was visible; every character was off-screen to the left. A clamp
against the value's own end width, plus a floor at zero, fixes it.

This is a latent `InputField` bug of long standing, not something the Browse
work introduced — it just needed a short value to replace a long one in the
same field, which nothing did until Browse started returning real server paths.
Every other `SetValue` caller benefits.

Pinned by `TestInputFieldSetValueShorterValueStaysVisible`, which asserts on
the runes actually drawn into the box rather than on `Value()` — asserting on
`Value()` would have passed against the bug, since `Value()` was right the
whole time. `TestInputFieldSetValueLongValueShowsItsTail` guards the other
direction, that a too-long value still shows its tail with the caret at the end.
A/B-verified: with the clamp removed the first test reports `drawn text = ""`.

## Restore: file relocation gets a view of its own (2026-08-12)

Requested: the Restore dialog should offer to move the restored files to a new
or default location, show the locations recorded in the backup, and carry a
button that pre-fills the fields with the server's current data/log paths.
Part of the "move-files handling" item under `docs/open-threads.md` § Reworks
named in README's Known Issues.

Until now the dialog had exactly one relocation rule and no way to see or
change it: files were moved to the server's default folders **iff** the target
name differed from the backup's, under `<target>_<logical><ext>` names. That
rule is a good default — it is what lets a copy be restored beside its original
— so it stayed, as the first of three explicit choices in a new **Restore File
Locations** view (`restore_dialog_files.go`, `restoreModeFiles`): relocate when
renaming, keep the backup's own locations, or relocate everything into two
named folders. `relocateFiles` in `restore_dialog_ops.go` is the one function
that turns a choice into `MOVE` clauses, and the view's per-file preview calls
it too, so what the list shows and what the RESTORE does cannot drift apart.
The renaming rule now governs only the file *names*: in folder mode a same-name
restore keeps the backup's file names and changes just the directory.

The view is reachable from the form's new `Files` button and from Backup
Information's new `File Locations` button; both need the set's
`RESTORE FILELISTONLY`, so `analyze` grew into `loadBackupInfo(next)` and an
already-analyzed device skips the round trip. `Analyze Backup` lost its second
word — five buttons at that width no longer fit in the 72-column dialog.

**Paths are clipped from the left, not the right.** The first draft used
`DrawTextClipped` and the live server made the problem obvious: every path in
the list rendered as the same `C:\Program Files\Microsoft SQL Server\MSSQL17.…`
prefix with the file name — the only part that differs — cut off. `clipPathLeft`
keeps the tail instead.

Live-verified against win10cli end to end, on a throwaway `zz_reloc_src`
database and its backup: the preview tracked edits to the folder fields
keystroke by keystroke, `Default Location` put the server's paths back, and a
real restore into a non-default folder landed exactly where the preview said —
`sys.master_files` for the restored copy named the chosen folder and the
`<target>_<logical>` file names. Both databases and the `.bak` dropped
afterwards.

### Follow-ups: the script's MOVE clauses, and truncated errors (2026-08-12)

Two reports against the work above, both live-reproduced first.

**"The Script button should add the MOVE parts."** It already did — invisibly.
`gosmo.BuildRestoreStatement` emitted the whole statement on one line, and a
restore that relocates two files carries two `MOVE` clauses of two full
Windows paths each: ~300 columns, of which the query editor shows the first
80 and no more. The scripted RESTORE looked like it stopped after `FROM DISK`.

Fixed in gosmo (`backup.go`), which now breaks the statement after the target
and after the device list and puts one `WITH` option per line — the layout
SSMS scripts. Whitespace is not significant here, so `RestoreContext` executes
exactly what it did before; the four exact-string tests in `backup_test.go`
were updated to the new layout. Live-verified both ways: the scripted
statement now shows both `MOVE` lines in the editor, and running it with F5
restored the database (`RESTORE DATABASE successfully processed 362 pages`).
`BuildBackupStatement` still emits one line — its options are short — and was
left alone deliberately.

**"Restore error messages get truncated."** They were: the progress view drew
SQL Server's failure on one clipped line — `Failed: gosmo: restore
"zz_err_tgt": mssql: The backup set holds a b` — with eight blank rows under
it. `wrapMessage` (in `backup_common.go`) wraps to a line budget and clips the
last line only if even that overflows; the progress view now draws the message
last, after Elapsed/Remaining, with every row down to the separator to use,
and the option form's status line gets the two rows `cbClose` leaves free.
A/B-verified against the same failing restore (backup of one database
restored over another without REPLACE): the whole message is now on screen.

The Backup dialog's progress view has the same one-line failure message and
was not touched — not reported, and not part of this request.

### Backup and Restore closed out (2026-08-12)

The author's call, same day: the pair no longer needs a rework. README's Known
Issues loses "Database Restore dialog needs a rework" (SQL Agent's stays), and
`docs/open-threads.md` § Reworks named in README's Known Issues records what
closed it — server-side Browse, the File Locations view, wrapped error
messages, left-clipped paths — plus the two rules that outlive the thread:
one source for the backup set number (`backupSetNumber`), and one source for
the relocation paths (`relocateFiles`, shared by the preview and the MOVE
clauses). The Backup dialog's one-line failure message is noted there as the
remaining rough edge.

### The Backup dialog's failure message wraps too (2026-08-12)

Same treatment as the restore side, in `drawProgress`
(`backup_dialog_draw.go`): the message is drawn last, from `inner.Y+12` down
to the separator, through the shared `wrapMessage`. At the dialog's 23-row
height that is a six-line budget.

Live-verified on win10cli by backing `backup_test` up to `Z:\nope\…`: the
progress view now shows all three lines of `Failed: gosmo: backup
"backup_test": mssql: Cannot open backup device 'Z:\nope\backup_test_full.bak'.
Operating system error 3(The system cannot find the path specified.).`, where
the old single `DrawTextClipped` line stopped at "Cannot open backup dev…".

## Review pass: three correctness items from the Browse/Restore work (2026-08-12)

A read-through of both repos after the Backup/Restore close-out. Most of what
it turned up was already settled in `docs/open-threads.md`; three things were
real, all in the server-side Browse work, and all fixed here.

**The Backup dialog's *form* status line still clipped to one line.** The
progress view had been fixed the same day, the form had not — and the form is
where a failed Validate or a failed database load reports SQL Server's own
message. It now uses `wrapMessage` exactly as Restore's `drawStatus` does,
except the line budget is derived from `cbCopyOnly.RectY()` rather than
hardcoded, so it follows the form's layout if that ever changes. On the
shipped layout it works out to two rows, the same number Restore hardcodes;
the old code drew one and left the other blank. Live-verified on win10cli:
Validate's ~150-column `BACKUP DATABASE ... TO DISK = N'C:\Program Files\...'`
now fills both rows where it used to stop at the first.

**`FileSystem.Exists` could not tell "not there" from "couldn't ask".** With a
`(bool, bool)` signature, `serverFS.Exists` had nowhere to put a timeout or a
dropped connection and reported `(false, false)` — which `FileDialog.finish`
reads as "no file there" and uses to *skip* the save-overwrite prompt. So the
one moment the guard exists for, a shaky connection, was the moment it went
quiet. `Exists` now returns an error, `LocalFileSystem` documents that it
never has one to give ("not there" is an answer, not an inability to answer),
and `finish` treats a failed probe as "assume it's there" and prompts: an
unnecessary prompt whose Yes does what the user asked for anyway is strictly
better than a silent overwrite. The other two call sites (`navigateTyped`,
`completeField`) ignore the error on purpose and say why.
`TestSaveOverwritePromptsWhenExistsCannotAnswer` pins it, A/B-verified — with
`|| err != nil` removed it reports `OnConfirmOverwrite path = ""`.

**`newServerFS` fell back to the client's filesystem when there was no
connection.** It returned `dialogs.LocalFileSystem{}` for a nil
`sc`/`Server`/`Info`, so Browse would look like it worked and hand back a path
off *this* machine's disks — a directory the server cannot see. That is the
"a click does the wrong thing" case `CLAUDE.md` § Application rules rules out.
It now returns `(fs, ok)` and both Browse buttons refuse with
`setStatusMsg("Not connected — cannot browse the server's filesystem.", true)`.
Removing the fallback also let Restore's `browseFile` drop its own duplicate
nil check.

**Not a bug, written down so it isn't rediscovered:** gosmo's
`FixedDrivesContext` falls back to `xp_fixeddrives` below version 15, and that
procedure does not exist on SQL Server on Linux. It is unreachable — the drive
list is only asked for when a path walks above a root, and
`PosixPathRules.Parent("/")` returns `"/"`, so a Linux browse never gets
there. Said so on the method rather than synthesizing a `/` entry no caller
would see.

### Making the server browse legible, and cheaper (2026-08-12)

The review's one architectural finding, and the two round trips next to it.

**`dialogs.FileSystem` is synchronous, and `serverFS` puts a network call
inside the event handler.** Every navigation, every Tab, every Enter in the
path bar stopped the whole TUI until the server answered — with the
*previous* directory still painted, which is exactly what a hang looks like.
Reproduced trivially on win10cli: `C:\Windows\System32` takes about ten
seconds to enumerate over the wire.

`FileDialog.showBusy` now paints the dialog with `Listing <dir> ...` in place
of the listing and flushes it before the call. That makes it the only thing in
tuikit that draws outside the app's draw cycle, which is deliberate and
documented in `ARCHITECTURE.md` § The other direction: FileDialog.showBusy —
there is no frame between the keypress and the blocked call for the normal
cycle to run in. It repaints only for a `dialogs.BlockingFileSystem`, a
one-method interface `serverFS` implements and `LocalFileSystem` doesn't, so
a local browse doesn't flicker.

`start()` had to be reordered to `Show()` before the first `loadDir` rather
than after — showBusy can only paint a dialog that is already visible, and the
first listing is the slowest of the session.

**An async `FileSystem` was considered and not built.** It turns Tab
completion and the save-overwrite check into callback chains, for a wait the
indicator now explains. Revisit only if the indicator turns out not to be
enough.

**Two round trips removed.** Tab completion re-listed the directory already on
screen — on every keypress. It now reuses `d.entries`, filtering out the
dialog's own ".." row, which `fs.List` never reports and which would drag the
common prefix to "" and stop completion working at all. `navigateTyped`
likewise probed `Exists` for a name the listing already describes; `entryFor`
answers from the cache when the typed path points into the current directory.
`finish`'s overwrite probe was deliberately left alone: correctness on a
safety guard beats one round trip on a final click.

Live-verified on win10cli: the `Listing C:\Windows\System32 ...` frame holds
for the whole ten seconds and clears into the listing, and completing `winver`
→ `winver.exe` in that same directory is instant, where before it would have
cost a second ten-second enumeration. Round-trip counts are pinned by
`TestCompletionInCurrentDirCostsNoExtraList` and
`TestNavigateTypedUsesTheListingItAlreadyHas` (counters on the fake
filesystem, both A/B-verified), and `TestCompletionIgnoresTheParentRow` pins
the ".." exclusion. `showBusy` itself has no unit test: tcell v3.4.1 ships no
simulation screen, so the package's test screen can size but not draw.

### Three tidy-ups from the same review pass (2026-08-12)

**One destination, one name.** The Restore form's `[ Files ]` button and the
inspect view's `[ File Locations ]` button both call `showFileLocations`. Two
names for one view reads as two views, so the inspect row now says `Files`
too. It went that direction rather than the other because the form row can't
grow: five buttons at `File Locations` width don't fit inside
`restoreDialogW`. The view still titles itself "Restore File Locations", which
is where the long name belongs. Verified live against win10cli — the renamed
button still hit-tests (`ButtonClicked` measures the same slice `DrawButtons`
paints, so the two can't drift) and opens the same view.

**A UNC path bottomed out two levels too low.** `WindowsPathRules.Parent`
walked `\\host\share` by separator, offering `\\host` and then `\` before
reaching the drive list — two levels that enumerate nothing and that Up had to
be pressed through. The share root is the shallowest listable level, so its
parent is now "" (the drive list), the same answer `C:\` gets. Drive-letter
paths are unaffected; the two cases are told apart by component count, since
`Clean` leaves a trailing separator on `C:\` but not on `\\host\share`. The
`Clean` doc comment claimed otherwise and was corrected.
`TestWindowsPathRulesUNCBottomsOutAtTheShare` pins it, A/B-verified: without
the guard it reports exactly the `\\host` and `\` levels.

**gosmo: `platformFromVersionString` moved to `server.go`.** It parses
`@@VERSION` for `ServerInfo.Platform` and its only caller is `loadInfo`;
`filesystem.go` was where its *result* gets consumed, not where it belongs.
The test moved to `server_test.go` with its `firstLine` helper. Pure code
motion — no behavior change.

### Filling the three test gaps the review found (2026-08-12)

All three pin an invariant that was already correct and had nothing holding it
there. Each was A/B-verified by breaking the code and watching the new test
name the failure.

**`filesFocusCycle` shrinking under the cursor.** The Files view's Tab order is
four entries under `relocFolder` and one otherwise, and `handleFilesKey`
indexes it with `filesFocus` unguarded. The `((i % n) + n) % n` in
`setFilesFocus` is the whole defence, and dropping it turns leaving
`relocFolder` from the Default Location button into an index-out-of-range
panic on the next keystroke — on the UI goroutine, where `recoverPanic` can't
catch it. `TestFilesFocusSurvivesTheCycleShrinking` and
`TestFilesTabStaysPutOnASingleEntryCycle`.

**`wrapMessage`'s budget-exhaustion branch.** Four tests, the load-bearing one
being that overflow is *folded into* the last line and clipped there rather
than the slice being cut at `maxLines`. Cutting the slice is the plausible
simplification and it loses the ellipsis, so a truncated server error reads as
a complete sentence that happens to stop early — the exact failure the helper
was written for.

**`serverIsWindows`'s three-way decision.** Eight cases covering
Platform-wins-over-path in both directions, the backup-path fallback for an
instance that reported neither, and an unrecognized platform string falling
through to the path rather than being a third answer. It picks the `PathRules`
the whole browse runs on: Posix rules over a Windows host leave "C:\..."
unsplittable, the dialog lists the wrong directory, and BACKUP gets a
destination the server can't write. The step from that boolean to the rules
`newServerFS` installs stays untested — it needs a `*gosmo.Server` whose
`Info()` answers, which only a real connection's `loadInfo` can arrange — so
that half is noted in the test file and verified live instead.

---

## gosmo `ErrNotFound`, and the endpoint dialog's create-on-any-error (2026-08-12)

Cross-repo review pass. The mechanical floor was already clean in both repos —
`go build`, `go vet`, `staticcheck`, `gofmt -l` and `go test -race` all say
nothing — so both findings came from reading.

**gosmo had no way to say "absent".** Twenty-odd by-name lookups reported a
missing object as `fmt.Errorf("gosmo: login %q not found", name)`: an
unwrapped string, so a caller could only distinguish absence from failure by
matching message text. Three other conventions existed alongside it —
`CertificateByName` returns `(nil, nil)`, `AvailabilityGroupByName` wrapped
`sql.ErrNoRows`, `AgentStatus` synthesises `StatusText: "Unknown"`. Only the
first was a defect; the other three are deliberate and are now written down on
`ErrNotFound` itself, because the split will otherwise read as an oversight
every time someone new looks at it.

`ErrNotFound` plus `notFoundf` went into `errors.go` and the 18 sites now call
it. **The messages did not change by a byte** — `notFoundError` carries the
text and reaches the sentinel through `Unwrap()`, rather than the usual
`": %w"` suffix, which would have appended a second "not found" to every one
of them. `notFoundfAlso` exists for the one method that had already documented
a different sentinel: `AvailabilityGroupByName` goes on satisfying
`errors.Is(err, sql.ErrNoRows)`, since narrowing a published gosmo contract is
exactly what `CLAUDE.md` forbids. `CertificateByName`'s `(nil, nil)` was left
alone for the same reason — changing it is a breaking change, not a cleanup.
Its doc now says so.

The three tests were A/B'd against a deliberately broken `Unwrap` before being
believed; two of them fail without it. That check is worth repeating on any
sentinel test, because `errors.Is` assertions pass vacuously in both
directions if the constructor is wrong.

**What the sentinel was for.** `importPeerCertificate`
(`new_endpoint_dialog.go`) read

```go
if _, err := p.server.LoginByNameContext(ctx, login); err != nil { /* create it */ }
```

— *any* error meant "the login isn't there", so a dropped connection or a
cancelled lookup surfaced as a failed `CREATE LOGIN` rather than as the fault
that actually stopped the pipeline, on a dialog where telling those apart is
the entire diagnosis. It now switches on `errors.Is(err, gosmo.ErrNotFound)`
and returns anything else as a lookup failure. It works today only because
`LoginByNameContext` happens to error on not-found; the same file calls
`CertificateByNameContext` forty lines up and correctly tests it against
`nil` — two conventions, one function apart, which is what made the wrong one
look right.

**The live check corrected the premise.** The first version of this entry said
a *denied SELECT* on `sys.server_principals` was the case being fixed. It
isn't, and win10cli said so: SQL Server does not raise a permission error
there. Metadata visibility **filters silently** — a caller without
`VIEW ANY DEFINITION` gets zero rows, which is indistinguishable from absence
because the server itself does not distinguish them. Measured directly: a
throwaway login saw exactly two principals (`sa` and itself); after
`DENY VIEW ANY DEFINITION` it saw one, having lost sight of **its own row**.
`TestLiveNotFoundCannotSeePastMetadataVisibility` pins that.

So the sentinel does not close the permission case, and no sentinel could.
What closes it is idempotence: `CreateLoginContext`'s error is now passed
through `isAlreadyExists`, exactly as the `CreateUserContext` call three lines
below already was. An existing-but-invisible login reads as absent, the CREATE
collides, and the collision is the proof the login was there. Confirmed live
that the collision is catchable — SQL Server answers
`The server principal 'x' already exists.`, which the helper's "already
exists" substring matches (its other arm, `15023`, is the *user* code; logins
raise 15025 and never reach it).

Worth keeping straight, since it inverts the intuition: for principals,
"not found" from a lookup is weaker evidence than "already exists" from a
write. The lookup can be lied to by permissions; the write cannot.

It was the only site of its shape: no other gossms caller branches on a
by-name error, they all propagate. The gossms branch itself still has **no
unit test** — it needs a live `*gosmo.Server`, a concrete struct with no
interface to fake — so it stays compiler-checked only; what is now covered
live is the gosmo behaviour underneath it, in `live_notfound_test.go`
(four tests, `-tags livedb`, throwaway logins dropped after).

---

## One dropdown helper, ten sites, and what the server refuses to let you break (2026-08-12)

`indexOf` returns 0 when a value isn't in the list, and 0 is a real option. Ten
property-page dropdowns fed it a *server-supplied* value, so a job owned by a
dropped login, a login whose default database no longer exists, or a schema
owned by an unresolvable principal each displayed the first real option as
fact — on exactly the objects an admin opens the page to investigate.

**Four idioms already existed for this, which is why sites kept getting
missed**: a prepended sentinel (`noneItem`, `unknownOwnerItem`), sorted
insertion (`compatItemsFor`), append-and-widen (`agSetSelect`, from Always On),
and `indexOfOK` plus a read-only static. `agSetSelect` was the only one that
*cannot* misreport, because the value the server gave is always in the list it
builds. It is now `preservingItems` in `prop_grid_helpers.go`, with
`selectPreserving` as the build-a-row form and `agSetSelect` kept as the
repoint-a-row form over the same core. Ten sites converted, plus
`database_props.go`'s hand-rolled variant, leaving one implementation.

**`changedTo` is the other half, and matters more than it looks.** A widened
list means a stand-in can be *displayed*; nothing may let it be *written*.
Gating on `Dirty()` alone happens to be enough today — a stand-in is only in
the list when it is also the original selection, so returning to it clears the
flag — but that is a property of how the list is built, three files from the
write. `changedTo` states it instead: dirty, and not on the stand-in.

Only one of the ten wrote bad data: Job Properties > Notifications, where
`emailCheck.Dirty()` gates a write of `operatorSelect.Value()`, so ticking
E-mail on a job with no operator sent whichever operator sorted first. It now
maps `noneItem` back to `""`, which `SetEmailNotify` documents as "leave the
operator unchanged". The other nine were display-only — established by grepping
every `Dirty()` gate in `*_props*.go`/`new_*.go`, not assumed.

**A/B'd live on win10cli, twice.** A login whose default database was dropped
(`zz_orphan_defdb` → `zz_defdb`, database dropped underneath it): old binary
showed `backup_test`, an unrelated real database; new shows `zz_defdb`. An
orphaned schedule owner: old showed `##MS_PolicyEventProce…`, the first login
on the server; new shows `(unresolved owner)`. Escape closed both dialogs
without an unsaved-changes prompt, so the widened row is not spuriously dirty.

**The server refused to reproduce one of them, and that corrected the note.**
`open-threads.md` had job and schedule owners as one bullet. They are not:
SQL Server *blocks* `DROP LOGIN` for a login that owns a job ("This login is
the owner of 1 job(s)"), while a schedule has no such protection — its owner
drops cleanly and `SUSER_SNAME(owner_sid)` goes NULL at once. So the schedule
case is trivially reachable and the job case needs a restored msdb or a removed
AD principal. Both still want the fix; only one can be demonstrated by dropping
a login.

**Testing notes worth keeping.** The unit tests were A/B'd against the old
`indexOf` behaviour — five of eight fail without the fix. One of them,
`TestChangedToRefusesTheStandIn`, passed under *both* on the first attempt: it
reached the stand-in through `SetSelected`/`SetItems`, which reset the dirty
baseline, so it was only re-testing the untouched-row case. Driving the widget
(focus, Enter, Up, Enter) is the only way to make a `SelectRow` dirty, and the
rewritten test fails correctly without the guard. Both premise checks
(`t.Fatal("test premise is wrong")`) earned their place — one caught that
`Down` cannot move off a stand-in, since `selectPreserving` appends it last.

Eight `indexOf` sites remain and are deliberately left: their lists begin with
a sentinel, so a miss lands on `(None)` rather than a real value. See
`open-threads.md` for why converting them is a separate question.

---

## The LIKE-escape duplicate, and one bare `==` (2026-08-12)

Two small items from the same review, both in gosmo.

**`escapeLikePattern` was a byte-identical copy of `likeEscape`** — same four
replacer pairs, one in `securable_search.go`, one in `helpers.go`, each with
its own test. Deduped onto `likeEscape`. Both were unexported, so nothing left
the published API and the no-removal rule never came into it.

Two things were kept rather than dropped with the function. Its doc comment
explained the failure better than the survivor's did — `_` and `%` are legal in
an identifier so a search for one silently wildcard-matches, and a `[` turns
the pattern into a character class that matches nothing — and that is now on
`likeEscape`. Its test had one case the other lacked, every metacharacter at
once (`` `_%[\` ``), which moved into `TestLikeEscape`;
`securable_search_test.go` held nothing else and is gone.

Verified live rather than assumed, because a rename that compiles proves
nothing about the query: three throwaway tables in a throwaway database,
searched through `FindSecurablesContext`. `my_table` returned **only**
`my_table` and not `myXtable` — which is the whole point, since an unescaped
`_` is a single-character wildcard that would match both — and `100%` matched
`pct100%tbl` literally.

**`certificate.go` compared `err == sql.ErrNoRows`** where the other twenty
sites use `errors.Is`. It was correct only by luck of plumbing, and it is the
worst place in the package to rely on that: `CertificateByName` is the one
lookup whose not-found answer is `(nil, nil)`, and `Database.queryRow` already
wraps *some* of its failures (`fmt.Errorf` on the `USE`). A bare comparison
that stopped matching would turn "no such certificate" into an error and send
every caller that branches on `cert == nil` down the other path — and the
endpoint pipeline creates a certificate on exactly that branch.

That contract had no test at all, which is why the hazard was invisible.
`TestLiveCertificateNotFoundIsNilNil` now pins it in both directions: a missing
certificate is `(nil, nil)`, and a real one comes back populated — the second
half matters, or "nil" could quietly become "always nil" and still pass.

The test creates its certificate with `ENCRYPTION BY PASSWORD`, not the
database master key. master has no DMK on a stock instance, and creating one
there would be a real and lasting change to the server for a test's
convenience.

**One bare comparison was left alone on purpose.** `showplan/parse.go` reads
`err == io.EOF` from `xml.Decoder.Token()`. Here `==` is the *stricter* choice:
`io.EOF` is returned bare by convention, and switching to `errors.Is` would
make an error that merely wraps EOF read as a clean end of document, silently
truncating a plan instead of reporting a parse failure. The input is always an
in-memory `strings.Reader`, so the two are equivalent today; the difference
only ever runs one way, and not the safe one.

---

## SQL Server / Agent log viewer (2026-08-12)

`todo/todo.txt` phase 1 item 1. The log itself was already reachable through
gosmo's `ReadErrorLog`; what was missing was the *other* log family, the file
list, and any UI.

**gosmo — `error_log.go`.** The Error Log section moved out of
`server_config.go` (a grab-bag of configuration, sessions, error log and
Database Mail) into a file of its own, byte-for-byte, then grew:
`ErrorLogType` (`ErrorLogSQLServer` = 1, `ErrorLogAgent` = 2 — the values
`xp_readerrorlog` and `sp_enumerrorlogs` take, so they pass straight through),
`ErrorLogFile` and `EnumErrorLogs`, and `ReadLog`/`ReadLogContext` taking the
family. Additive only: `ReadErrorLog` still exists and now delegates with
type 1, and `ErrorLogEntry` keeps `LogDate`/`Process` and *gains* `Date
time.Time` and `ErrorLevel int`.

Three facts came from probing win10cli, not from memory, and each shaped the
code:

- **The middle column differs by family.** `xp_readerrorlog N, 1` returns
  `LogDate, ProcessInfo, Text`; `xp_readerrorlog N, 2` returns `LogDate,
  ErrorLevel, Text` — a `DATETIME`, an `INT`, an `NVARCHAR`. Scanning one
  shape into the other fails in the driver, which is why `ReadLogContext`
  branches on the family for the scan and why `ErrorLogEntry.Source()` exists
  for the callers that just want a column to show.
- **`sp_enumerrorlogs`' Date column is an `NVARCHAR`**, formatted by the
  extended procedure (`"08/12/2026  21:49"`, two spaces, minute precision) —
  not a datetime. `ErrorLogFile` therefore keeps both the raw string and a
  parsed `LastWritten`, and `parseErrorLogFileDate` reports the zero time
  rather than guessing when no known layout matches: the formatting follows
  the server's locale, so an unrecognized one has to degrade to showing what
  the server said. Labels use minute precision for the same reason —
  `formatSQLDate`'s `":00"` seconds would be invented.
- **The Agent family's current log comes back last**, not first
  (`1,2,3,4,5,6,0`), so `EnumErrorLogsContext` sorts by number.

`LogDate` is now derived from `Date` with `RFC3339Nano`, which is exactly what
`database/sql`'s `convertAssign` produced when the old code scanned a
`DATETIME` straight into a `string` — the compatibility is deliberate and was
checked against the live server both ways.

**gossms — the `LogViewer` panel.** Two toolbar selectors (family, archive),
a filter field, Refresh and Export, over a `DataGrid` of Date/Source/Message
and a details pane below a draggable divider. The selectors pop
`App.contextMenu` rather than embedding `widgets.DropDown`: the application's
own overlay already gets first refusal of every key and click, and it is
styled for a panel rather than a dialog. `layout.Splitter` is reused whole, so
the divider's drag rules came for free.

Rows are shown **newest first**, as SSMS's viewer opens, with the direction
marked in the Date header. The sort is stable, so the several entries one
second usually carries keep the order the log wrote them in — reversing those
as well would scramble a startup sequence or a stack dump, verified live on a
`22:44:17` pair. Export follows what is displayed, but writes a plain header:
a column name in a file shouldn't carry the grid's sort marker.

The details pane exists because a log entry is not a grid row: the startup
banner is four lines with tabs. `flattenLogText` makes the one-line grid cell,
`splitLogLines` preserves the structure for the pane, and both are pinned by
tests. Alt+Up/Down scrolls the pane — not PgUp/PgDn, which belong to the grid
the cursor is in.

**Object Explorer** grew a real **Management** folder (SQL Server Logs) beside
Server Objects, and **Error Logs** under SQL Server Agent, each listing one
leaf per file. Which meant renaming `NodeManagement` — it had been the type of
the node *labelled* "Server Objects" — to `NodeServerObjects`, before adding a
second, differently-meaning `NodeManagement` next to it.

**One trap, found live.** Both selectors are labelled with what they point at,
so their labels change without a resize; `layoutTools` ran only from
`SetBounds`, and the rect laid out for the shorter old label let the next
button overpaint the tail of this one ("File: Archive #1 — 2 Refresh").
`Draw` now relabels and relays out together.

**A keyboard trap, avoided by the rule in `CLAUDE.md`.** `App.handleKey` only
moves focus back to Object Explorer when the focused panel *declines* Tab, so
the panel walks grid → filter and then returns false, resetting focus to the
grid on the way out. Both directions are pinned by tests, including the
narrow-panel case where the filter field doesn't fit and the first Tab must
already be declined.

**Verified live on win10cli** through tmux: both families, archive switching
via the selector and via the tree, the filter (2 of 91 rows on "recovery",
case-insensitively), F5 (339 → 345 entries as the live log grew), splitter
drag and keyboard resize, Alt+Down details scroll, Export (91 entries to
TSV), one-panel-per-server reuse, and Tab leaving the panel at both widths.

---

## Object Explorer folder filter (2026-08-13)

SSMS's "Filter Settings" on a folder node — the first of the two items at the
top of `todo/todo.txt`. `internal/tui/explorer_filter.go` is the model,
`filter_dialog.go` the dialog.

**Where the filtering happens decides everything else.** It runs in
`fetchChildren`, on whatever the folder's loader returned, so a filter survives
Refresh, collapse/expand and reconnect without any loader knowing it exists,
and applying or clearing one is just a folder reload (`App.applyNodeFilter`).
The alternative — filtering at draw time — would have had to teach `flatten`
about node semantics and would still have counted hidden rows in every
scroll/selection calculation.

**The property list is bounded by what the loader already fetches.** SSMS
offers Owner and Durability Type on Tables; both live on gosmo's `TableDetail`,
i.e. one query per table, so neither is offered here. Name/Schema come free
from `nodeData`; Creation Date and Is Memory Optimized are new `nodeData`
fields the six relevant loaders now populate. A criterion matched against a
zero `CreateDate` rejects every row, which is why `filterProps` and the loaders
have to stay in step — pinned by `TestFilterPropsAreBackedByNodeData`.

**Sub-folders and error nodes are never filtered out.** A filtered Views folder
keeps "System Views", and a failed expand keeps its error leaf — otherwise a
filter that matches nothing and a folder that failed to load look identical.

**The "(filtered)" suffix is rendered, not stored** (`flatten` reads
`nodeData.Filter`), so clearing a filter can't leave the label mangled.

Dates and booleans are validated in the dialog before a filter is built: an
unparseable value matches nothing, so a typo would otherwise come back as an
empty folder with no error.

**Verified live on win10cli** through tmux: Name contains "pat" (8 tables → 1,
label marked, status line summarising the filter), Creation Date after
2000-01-01 (all 8 kept), Is Memory Optimized equals True (empty folder), the
bad-date validation message, Remove Filter restoring the folder and the label,
the item greyed out when there is no filter, reopening the dialog seeding back
the criteria in force, and the server-level form (Databases: no Database
header line, Name + Creation Date only, "System Databases" retained).

---

## General Delete and Rename (2026-08-13)

The second `todo/todo.txt` item, and the one that needed gosmo work first.
`internal/tui/explorer_object_ops.go` is one table of what Delete and Rename
mean per node type, plus the two shared actions around it; a node type absent
from the table offers neither, which is how folders and system-object families
stay out of the menu instead of showing an item that fails when clicked.

**gosmo gained the drops and renames that weren't there** — `Database`:
`DropView`, `DropFunction`, `DropTrigger`, `DropSequence`, `DropSynonym`,
`DropDatabaseRole`, `RenameObject`; `Table.DropConstraint`;
`Statistic.Rename`; `Server.DropServerRole`, `Server.RenameDatabase`; plus
`DatabaseRole.Drop` and `ServerRole.Drop`. `Sequence.Drop`/`Synonym.Drop` now
delegate to the new Database-level forms, with the same SQL as before —
`DROP SEQUENCE` has no `IF EXISTS` and `DROP SYNONYM` does, and changing
either would change what an existing caller observes. Each new statement is
pinned through `WithScript` in `drop_rename_test.go`, which is how the exact
T-SQL gets asserted without a server.

**One statement class per rename.** `sp_rename` needs the right `@objtype`:
`OBJECT` for views/procedures/functions/sequences/synonyms/triggers and for a
foreign key or CHECK constraint, `INDEX` for an index — and for a primary key
or unique constraint, whose name *is* its backing index's name — and
`STATISTICS` for a statistic. Schemas have no rename at all, so Rename is
absent there rather than offered and failing.

**Renaming a database found a real one, live.** `ALTER DATABASE ... MODIFY
NAME` needs exclusive access, and the tree's own metadata connections are
enough to deny it: the first live attempt came back "could not be exclusively
locked". `RenameDatabaseContext` therefore takes `force`, doing
SINGLE_USER WITH ROLLBACK IMMEDIATE and MULTI_USER around the rename —
including when the rename fails, so a refusal never strands the database
single-user — and Object Explorer asks before using it.

**Delete confirmations scale with the blast radius**: a plain Yes/No for one
object, the retype-the-name dialog for a database (whose drop also closes
connections). `dialogs.PromptDialog` is new for Rename — a one-line input with
OK/Cancel, an initial value that comes back selected so typing replaces it, and
a `Validate` hook; an empty or rejected value keeps the dialog open with the
reason rather than closing silently.

**The Filter and Rename/Delete groups are spliced in above Refresh**
(`insertBeforeRefresh`), matching SSMS's ordering, rather than repeated in
every branch of `nodeMenuItems`. Its divider handling exists because the first
version drew two separator lines in a row: every node menu already has a
divider above Refresh.

**Verified live on win10cli** against a throwaway `gossms_ops_test` database
and login, dropped afterwards: table rename, view delete, index rename, primary
key delete (ALTER TABLE DROP CONSTRAINT), table delete, database rename (forced,
`sys.databases` confirming MULTI_USER/ONLINE afterwards), database delete behind
the retype dialog, and login rename + delete — each cross-checked with `sqlcmd`.

---

## Cross-repo review pass: system objects, drop semantics, endpoint certificates (2026-08-13)

A review of everything new since `v0.0.6` (gossms) and `v0.0.8` (gosmo) —
the log viewer, the folder filter, Delete/Rename, the endpoint dialog, the
server filesystem, the restore Files view. Five things came out of it; the
first is the only real bug.

**Delete and Rename were offered on system objects, and one of them did
damage before failing.** `objectOpsMenuItems` keyed off `node.data.Type`
alone, and the System * folders emit exactly the same node types as the user
ones — `loadSystemDatabasesChildren` builds `NodeDatabase`,
`loadSystemViewsChildren` builds `NodeView`, `agentJobNode` builds
`NodeAgentJob` for both families. So `master` carried Rename and Delete, and
so did `sys.objects` and `syspolicy_purge_history`. Delete fails on the
server, which is merely wrong; **Rename on a system database is worse**,
because `RenameDatabaseContext(force=true)` runs SINGLE_USER WITH ROLLBACK
IMMEDIATE *first* and only then finds out the server won't rename `model` or
`msdb` — every connection to that database is dropped on the way to an error.
Renaming a system Agent job isn't refused at all.

The fix is `nodeData.IsSystem`, set by the four system loaders and by
`agentJobNode` from the `isSystemAgentJob` predicate that already existed, and
checked in `objectOpsMenuItems` *and* again in `deleteObject`/`renameObject` —
the second check is where the DROP is issued, so that is where the guarantee
belongs.

A/B'd live on win10cli with a pre-fix binary built from HEAD: the old one
shows `Rename...`/`Delete...` on `master` and `Rename...` on
`syspolicy_purge_history`; the new one shows neither, and `HealthClinic` still
has both. The Agent family's own `Delete Job...` (`agent_menu.go`) is
deliberately still offered on a system job — SSMS permits deleting one, and
that item was never part of the `objectOps` table.

**gosmo's Drop* methods no longer carry IF EXISTS.** Half the family had it
and half didn't, so the same gesture in Object Explorer reported two different
things about the same situation: deleting a view that was already gone said
"View deleted", deleting a sequence said the server refused. Author's call was
the honest direction — a bare DROP everywhere, so "deleted" means deleted, and
a caller that wants idempotence ignores the error. The generated *scripts*
keep IF EXISTS: Scripter's DROP-and-CREATE output exists to be re-run, which is
the opposite requirement. `TestDropStatementsAreNotIdempotent` pins the whole
family at once, and it was verified live — a second `DROP VIEW` and a second
`DROP TABLE` both now come back "because it does not exist or you do not have
permission".

**`dbOf` was paying a round trip per delete.** It resolved the database with
`DatabaseByNameContext`, a `sys.databases` query, when every statement under it
names its object in the text and reads nothing off the `*gosmo.Database` but
its name. `Server.Database` is the right handle and costs nothing — and is the
only one that would work under a `WithScript` context, which is what a Script
Changes on Delete would need. `Database.Table(schema, name)` is new in gosmo for
the same reason, with its doc comment explicit that ObjectID stays zero so the
methods that query by it are not what the handle is for. Live-checked: the
name-only handle dropped a real CHECK constraint.

**The endpoint exchange trusted a certificate by name.** `importPeerCertificate`
skipped the import whenever a certificate of the expected name existed, so a
reinstalled peer left a stale public certificate in place, the pipeline reported
success, and the endpoint then refused the connection with nothing saying why.
It now compares `Thumbprint` on the two rows, which are already loaded, and
names the instance to fix. Not exercised live — that needs two instances and a
rebuilt one.

**Minor.** `error_log.go`'s scan and iteration errors now carry the `gosmo:`
prefix the rest of the package uses. `LogViewer.Load` gives the enumeration and
the read a `logReadTimeout` each instead of one shared budget, so a slow
`sp_enumerrorlogs` can no longer eat the read's half of it. `panel_toolbar.go`
is new and holds the toolbar geometry Activity Monitor and the Log Viewer were
duplicating (`toolButton`, `layoutToolButtons`, `toolButtonAt`) — geometry only,
since the two disagree on what dimming means. It is *not* `toolbar.go`, which is
App's own icon strip in the menu bar row.

---

## `End` on an empty DataGrid crashed the app (2026-08-13)

Found in a cross-repo review sweep, confirmed live on win10cli, fixed the same
session.

`DataGrid.HandleKey`'s four whole-list jumps had no empty-grid guard.
`End` set `selRow = g.rows.Len() - 1`, which is `-1` with no rows, and `PgDn`
reached the same value through `core.Min(g.rows.Len()-1, ...)`. `ensureVisible`
then copied it into `scrollRow` — it clamps neither — and `Draw`'s row loop
bounds `dataIdx` only from *above* (`dataIdx >= g.rows.Len()`), so it fell
through to `SliceRowSource.Row(-1)` and panicked. On the UI goroutine, which
`safego` does not cover, so `main.run`'s recover logged a trace and the app
exited.

Reachable from any focusable empty grid: a zero-row query result, an empty
Object Explorer Details list, a `propsheet.GridRow` for a table with no
indexes. Repro was three keystrokes after connecting —
`Ctrl+N`, `SELECT 1 AS a WHERE 1=0`, F5, click into the results grid, `End`.

**Why it survived so long.** `TestDataGridEmptyBeforeSetData` already pressed
`End` on an empty grid — and passed, because `HandleKey` itself never panicked.
The panic was two calls away in `Draw`, which the test never ran. That is the
"asserts nothing crashed" failure mode `CLAUDE.md` warns about, caught on the
one test written to cover this exact case. The replacement,
`TestDataGridRowJumpKeysOnEmptyGridKeepIndicesValid`, asserts `selRow` and
`scrollRow` are non-negative and then makes the same `rows.Row(scrollRow)` call
the draw loop makes; it fails with the original panic when the guard is
reverted.

The fix refuses `PgUp`/`PgDn`/`Home`/`End` outright when `rows.Len() == 0`,
which is the guard `SetSelectedRow` and `SetSelectedCell` already carried —
`HandleKey` was the one row-moving path that skipped it. It still returns
`true`, keeping DataGrid's blanket "I claim the arrows" answer that QueryPanel
and DetailBrowser rely on; `propsheet.GridRow` turns "nothing moved" into
`false` for the form case on its own, so an empty grid in a property page still
hands `Up`/`Down` back to `Form` rather than trapping focus. `Up`/`Down` needed
no guard — both are already bounded by a live row index.

## Login Properties could rename a `##MS_*` login (2026-08-13)

`system_principals.go` added `isSystemLogin` and Object Explorer honours it —
the context menu on `##MS_PolicyEventProcessingLogin##` offers neither Rename
nor Delete. Login Properties' General page didn't: it built its `Login name`
row as an editable `propsheet.Text` unconditionally, so `ALTER LOGIN ... WITH
NAME` was still one dialog away for exactly the principal `isSystemLogin`
exists to protect. The server permits that rename; what it doesn't do is fix up
the matching users in master and msdb, which is why the gate is a gate.

The page now follows the shape `user_props.go`, `schema_props.go`,
`role_props.go` and `server_role_props.go` already use: a `builtin` flag picks
`Static` over `Text` for the identity row, a closing "Built-in login" note says
why, and the apply closure's rename branch is nil-guarded. Only the name is
gated — default database, language and credential are ordinary `ALTER LOGIN`
settings that work fine on these logins, and the password rows were already
disabled for them by the existing `isSQLLogin` test.

Verified live on win10cli: `##MS_PolicyEventProcessingLogin##` renders the name
read-only with the note at the foot of the form, `sa` still renders `[sa]`
editable (renaming `sa` is a documented hardening step and stays available).

## `core.Min`/`core.Max` retired in favour of the builtins (2026-08-13)

`core.Min`/`core.Max` predate the project's move to Go 1.21+'s builtin `min`
and `max`, and both spellings were already in the tree — 145 calls through
`core`, plus a dozen bare builtin calls added since. Swept all 145 to the
builtins across 51 files and deleted the two functions; `core.Clamp` stays,
being generic over `cmp.Ordered` and not something the builtins cover (the
layout splitter clamps a `float64` ratio).

Three `planview` scroll helpers held the result in a local named `max`.
`max := max(0, …)` compiles — the RHS resolves before the declaration takes
effect — but it shadows the builtin for the rest of the function, so they're
now `maxScroll`. `TestMinMax` and the `Min/Max` mention in `core/doc.go` went
with the functions.

Smoke-tested live on win10cli after the sweep, since the touched code is
almost all draw-time column math: results grid, scrollbar thumbs, and the
estimated execution plan's graph all render correctly.

## Small fixes alongside (2026-08-13)

- `os.WriteFile` mode literals in `log_viewer.go` and `app_panel_actions.go`
  were `0644`; every other mode in the tree is `0o…`.
- Stray blank line in `loadDatabaseRolesChildren`.
- gosmo's `qualifiedName` emitted `[].[name]` for an empty schema. Most of its
  callers take schema from an exported method's parameter, and `OBJECT_ID`
  resolves that form to NULL — so a caller passing `""` got an empty result set
  and no error. It now returns the unqualified `[name]`, pinned by a case in
  `TestQualifiedName`.

`wakeEventLoop`'s doc comment was on this list as "overstates the deadlock
risk"; re-reading it, it doesn't — `quit()` takes the same `quitMu` from the UI
goroutine while not draining `EventQ()`, so a blocking send really would hang
Ctrl+Q. Left alone.

## panel_toolbar nits (2026-08-13)

A second review pass over the working tree, after the four fixes above landed
on top of the in-progress `dialog_common.go` / `panel_toolbar.go` /
`system_principals.go` extraction. Almost everything checked out; two nits in
the new shared toolbar came out of it.

`layoutToolButtons` guarded its fit test with `r.W == 0 || x+w > r.Right()`.
`Rect.Right()` is exclusive (`X+W`), so a zero-width toolbar puts the first
button at `X+1` with `w >= 2` and fails the width test on its own — the clause
never decided anything, in either of the two inlined copies it was lifted
from. Dropped, after checking the equivalence with a throwaway test over both
the prefixed and unprefixed layouts.

`toolButton.disabled` is documented as "drawn, not enforced", which is true of
Activity Monitor and misleading for the Log Viewer: that panel dims and gates
on `toolsEnabled()`, a whole-row state, so `disabled` on one of its buttons is
neither drawn nor enforced. Said so on the field rather than making the Log
Viewer honour it — the two panels genuinely disagree about what a dimmed
toolbar means, which is why only the geometry is shared.

## Three from the open-threads list (2026-08-13)

**"Jobs Without Schedules" no longer hides what it couldn't check.**
`jobsWithoutSchedulesReport` did `if err != nil || len(scheds) > 0 { continue }`,
so a job whose per-job round trip failed vanished from a report whose subject is
jobs that are missing something — silence read as "all fine". The report now
carries a fourth column, Schedules, reading `None` or `Unknown`, which is the
same answer `countOrDash` gives the summary's census. A cancelled context is
the one case that still returns an error rather than a page of `Unknown`: every
remaining job would fail, and a report claiming nothing is verifiable is worse
than saying the read was cancelled.

Verified live on win10cli — both its jobs have schedules, so the report was
empty until a throwaway `zz_gossms_noschedule` was added, which then listed with
`None`. Dropped afterwards. The `Unknown` path has no live coverage: forcing one
job's `SchedulesContext` to fail while the others succeed needs a failure that
can be aimed at a single round trip.

**The endpoint exchange survives a named instance.** The certificate, login and
user were `<@@SERVERNAME>_Cert` / `_login` / `_user`, and `@@SERVERNAME` on a
named instance is `HOST\INSTANCE` — so the login was `[HOST\INST_login]`, which
is the spelling of a Windows principal. `endpointPrincipalBase` now maps the
backslash to `$` (SQL Server's own `MSSQL$INSTANCE` convention), at all four
sites: `certificateName`, the login and user in `importPeerCertificate`, and the
`GrantConnect` grantee. Not truncated to the host the way gosmo's `endpointURL`
does — that is right for a TCP host and would give two named instances on one
machine the same principal names. A default instance is unchanged, which is why
every deployment so far ran through this untouched, and why the only pin is
`TestEndpointPrincipalBaseSurvivesANamedInstance`: the test cluster is all
default instances.

**Add Replica's endpoint URL is editable.** It was a `Static` row filled from
`DatabaseMirroringEndpoint.URL()`, whose host comes from that instance's own
`@@SERVERNAME` — so an instance whose short name the other replicas cannot
resolve produced a URL that parses, is accepted, and never connects, with no way
to type the FQDN. Now a `Text` row: Connect still fills it and is still required
(it is what proves the endpoint exists and is STARTED, and an empty
`resolved.name` is the tell), but the host can be corrected afterwards.

That makes it the one field in the dialog a typo reaches the server through, so
`validateEndpointURL` was added — `ADD REPLICA` stores a malformed URL without
complaint and the replica simply never connects, which is diagnosed hours later.

Verified live on ubusql1: the row renders as an input, accepts a hand-typed
`tcp://ubusql2.fritz.box:5022`, and both notes render. Connect could not be
shown filling it — the only other instance is already a replica, so `connect`
refuses it before reading the endpoint — and the malformed-URL refusal is
likewise unreachable live for the same reason, since the missing-name check
fires first. Both are unit tested. `sys.availability_replicas` was re-checked
afterwards: still ubusql1 and ubusql2, nothing written.

## Cross-repo review pass: five findings, and one that turned out backwards (2026-08-14)

A full review of both repos on request. The headline is that it found very
little: `go build`/`vet`/`test`/`-race` and **staticcheck clean on both**, zero
TODO/FIXME markers in either, and every documented invariant holding when
checked mechanically rather than assumed — `tuikit`'s three permitted external
imports, `planview`/`sqlparse`/`dashboard` as genuine leaves, `rows.Close`/
`rows.Err` at all 102 gosmo query sites (the six that look bare delegate
`rows.Err()` into `scanColumns`/`scanExtProps`/`scanEffectivePermissions`), and
gosmo's `gosmo: ...: %w` convention at every clause-builder call site. The two
mass refactors in the last commit of each repo — `core.Min`/`Max` to the
builtins plus a generic `Clamp` across 93 files, and gosmo's error-message pass
across 38 — were both mechanically sound. Nothing in `docs/open-threads.md` was
re-raised.

Four small things went in, and a fifth was reverted by its own evidence.

**`writeFileAtomic` never flushed the directory.** `internal/config/config.go`
syncs the temp file before renaming, which is the hard half, but the rename is a
change to the *directory* — so a crash could leave durable bytes under a name
the directory doesn't carry yet, and the config reverts whole. That is the same
"lost every saved connection" outcome the temp-file dance exists to prevent,
through a narrower window. `syncDir` closes it.

The trap here, caught before it shipped: the obvious form returns the `Sync`
error, and **that breaks Windows outright**. Syncing a directory is a POSIX
notion; `FlushFileBuffers` rejects a handle opened for reading, so every `Save`
would have failed on one of the three platforms this single binary targets. It
is best-effort and returns nothing, deliberately — this must not be able to fail
a save that already succeeded.

**`scrollToShow`'s zero-height viewport.** New in the previous commit and shared
by five hand-rolled dialogs. At `dataH == 0` the "past the bottom" arm is
unconditionally true and answers `sel+1` — scrolled one row past the very
selection it was asked to reveal, drawing as an empty pane rather than a short
one. No caller reaches it today; guarded and pinned with three table cases.

**`isSystemUser` and `isSystemSchema` are byte-identical and nothing said so.**
They are kept apart on purpose — different statements, and they could
legitimately diverge — but they must agree *today*, since each of the four names
is undroppable as both a user and a schema. A one-sided edit would offer Delete
on half a principal that cannot take it. `TestSystemUserAndSchemaListsAgree`
pins it, and was A/B'd by removing `"sys"` from the schema list alone and
confirming the failure names `sys`.

**gosmo's filesystem version gate now fails toward the branch that always
works.** `EnumFileSystemContext` gated *negatively* — `xp_dirtree` only on a
known pre-2017 instance, everything else including an **unknown** version to
`sys.dm_os_enumerate_filesystem`. `xp_dirtree` exists on every version and the
DMV does not, so guessing toward the DMV turns an unknown pre-2017 instance into
a hard failure, while guessing the other way costs a known-modern one only
`Size` and `LastModified`. Browse needs names and the directory flag; it can
live without the other two. Degrade, don't fail.

The flip rests on a claim only a server can settle — that `xp_dirtree` returns
the *same directory* as the DMV, just with less detail. If the two disagreed on
which entries exist, the fallback would be a different answer rather than a
lesser one. `live_filesystem_test.go` (`-tags livedb`, read-only) checks exactly
that on win10cli: same connection, `info` nil versus `info` forced to major 17,
**identical 20 entries with identical directory flags**, and `xp_dirtree`
reporting zero sizes and timestamps where the DMV reports 20/20 of both. The
first run pointed at `C:\Program Files\Microsoft SQL Server` and reported 0/6
sized — all six are directories, so that was no evidence at all, and it was
re-run against a path with real files.

### The finding that reversed: `serverFileSystemTimeout`

Raised from 15s to 30s to match the app's other fetch timeouts, on the strength
of ARCHITECTURE.md's statement that listing `C:\Windows\System32` over the wire
takes ten seconds — 1.5x headroom on a case already observed. Timing the live
run above disproved the premise: **1.2s, not ten seconds**, 4551 entries, best
of three, measured through `EnumFileSystemContext` (`C:\Windows` 101 entries in
35ms, `C:\Program Files` 29 in 11ms).

The ten-second figure predates gosmo's own `WHERE level = 0` filter on
`sys.dm_os_enumerate_filesystem`, without which the DMV walks the whole subtree
under the path instead of listing one directory — that fix is the ~8x, and the
prose in ARCHITECTURE.md was never updated to follow it.

With real numbers the argument runs the other way and the change was reverted.
This timeout is not like the other four: they bound *background* work, this one
bounds a **frozen UI**, because `dialogs.FileSystem` is synchronous. 15s was
already more than 10x headroom, and 30s only doubled how long an unreachable
server looks like a hang — the exact case the constant's first sentence exists
to bound. Consistency with the other timeouts was the wrong axis to optimise.

Two lessons worth more than the fix. **A measurement in prose is a claim with a
date on it, and an optimisation elsewhere can silently falsify it** — the
`WHERE level = 0` filter went into gosmo and left a number in gossms's
ARCHITECTURE.md describing a version of the code that no longer existed. Both
that paragraph and `serverFileSystemTimeout`'s own comment now carry the fresh
figures *and* an explicit warning off the old one. And **re-measure the evidence
before acting on it**, even when it is written down in your own repo by your own
hand: this one survived a plan, a decision and an implementation before the
live run contradicted it.

## Open dialogs did not follow a terminal resize (2026-08-14)

Found on the second cross-repo review pass of the day, by driving the binary
rather than reading it. `ModalDialog.recentre` had exactly three callers —
`InitModal`, `SetSize`, `Show` — and none of them is a resize.
`App.layoutAll`, the `EventResize` handler, relaid out the menu bar, toolbar,
splitter, explorer and panels; the dialog stack is not in the layout tree
(dialogs centre themselves on the screen), so nothing reached it.

A dialog open across a resize therefore kept the rect it was centred into.
Reproduced under tmux with the Connect dialog at 120x34 shrunk to 60x20: the
box stayed at x=28 w=60, so its right border and its **entire button row**
were off-screen, while it went on swallowing every key. At 24x5 it drew
nothing at all and the app looked hung — Escape still closed it, which is the
only reason it was recoverable.

Every one of the ~28 app dialogs was affected **except** the propsheet family:
`PropertySheet.Draw` calls `recomputeSize` on every frame, so Properties and
every New-object dialog self-healed. That is exactly why this survived so
long — the dialogs a user resizes around are usually property sheets.

The fix is an exported `ModalDialog.Relayout()` (recentre), `Relayout()` added
to `tui.Dialog`, and `App.relayoutDialogs` broadcast from `layoutAll` over
`dialogStack`. The broadcast goes **above** `layoutAll`'s `w < 20 || h < 5`
early return: the smallest terminals are where an unrefitted dialog is most
broken, so that guard must not skip it. `AlertDialog`, `ConfirmDialog`,
`PromptDialog` and `TypedConfirmDialog` override `Relayout` to re-run
`fitMessage` first — they wrap their message to a fraction of the screen
width, so following a resize means re-wrapping, not only recentring.
`PropertySheet` overrides it with `recomputeSize`, which is what its `Draw`
was already doing a frame later.

Recentring from `DrawBase` instead would have fixed it too, and was rejected:
it does layout work every frame and hides the trigger.

Verified A/B against a pre-fix binary. Post-fix, a resized dialog renders
**byte-identically** to one freshly opened at that size — the Help dialog
capture at 50x16 matches the pre-fix binary launched at 50x16 exactly. Pinned
by `TestLayoutAllRefitsOpenDialogsToTheNewScreenSize`, which fails on the old
code with `rect {X:28 Y:10 W:44 H:9}` against a 44x12 screen.

### Left alone: content is not clipped to a clamped rect

The A/B turned up a second, older problem it would have been easy to blame on
the fix. A fixed-size dialog on a terminal smaller than itself has its *rect*
clamped by `recentre`, but its content still draws at fixed row/column
offsets: at 30x8 the Connect dialog's field rows run past the right border and
the button row lands on top of them. The pre-fix binary **launched** at 30x8
draws the identical mess, so this is untouched by the change and predates it —
noted in `docs/open-threads.md` rather than fixed here.

## The encryption key was the one file not written atomically (2026-08-14)

`config.json` goes out through `writeFileAtomic` — temp file, `Sync`, rename,
`syncDir` — and `gossms.key`, which is the only thing that can read it, went
out through a plain `os.WriteFile`.

`Save` creates the key before it writes the ciphertext that depends on it, so
the crash window is real and one-directional: config.json durable and full of
sealed passwords, `gossms.key` absent or short-written. `loadOrCreateKey`
then refuses a wrong-sized key file by design — the right call in isolation,
since overwriting it would destroy the passwords for good — and the next run
hard-errors instead. Every saved password is unrecoverable. That is the same
"lost every saved connection" outcome the temp-file dance and the `syncDir`
call exist to prevent, reached through the other half of the pair.

One-line fix: `writeFileAtomic(path, key)`. Atomicity is not observable from a
unit test, so `TestLoadOrCreateKeyLeavesOnlyTheKeyFileBehind` pins what is —
the rename leaves no temp file, and the result is still 32 bytes at 0600.

## Three from the same review: EndSet, Load, Clamp (2026-08-14)

### `EndSet` skipped the set whose `BeginSet` failed

`streamResultSet` registered its `EndSet` defer *after* calling `BeginSet`, so
a sink that refused to open a set never got the matching close — contradicting
`RowSink`'s own promise that "EndSet is called for every set BeginSet was
called for", and its instruction to finalise per-set state "there and nowhere
else". Nothing broke in practice, because the only sink is `csvSink` and its
`BeginSet` failure mode (a header write that doesn't reach the disk) leaves
nothing to unwind. It is still the contract a second sink would be written
against.

Fixed by hoisting the defer above the `BeginSet` call rather than by softening
the doc: a sink that took a lock or allocated per-set state before the failure
has `EndSet` as its only place to undo it. `n` is 0 there, and the named-return
guard already prefers the first error, so `BeginSet`'s error is what surfaces —
`TestStreamResultSetEndsASetWhoseBeginFailed` asserts exactly that, not just
that EndSet ran.

### `Load` treated "couldn't read it" as "there isn't one"

Any `os.ReadFile` error returned an empty `Config` — a permission change, EIO,
EMFILE. The app then came up with no saved connections, and the next `Save`, of
some unrelated Options-dialog setting, wrote that emptiness over a file that
was very likely still intact. The corrupt-JSON path immediately below has
always been careful about this, preserving the bytes to `.corrupt`; the
read-error path just dropped them.

Only `fs.ErrNotExist` is a first run now. Anything else logs and sets an
unexported `Config.unreadable`, which makes `Save` refuse the write with the
original error wrapped. Unexported so it neither serialises nor survives being
copied into a fresh `Config`. Both callers of `Save` already route its error to
the status bar, so the refusal is visible without any new plumbing.

Verified live as well as in tests: launched against a `config.json` with mode
0000, the app starts normally, logs the reason, and the file is byte-identical
afterwards. No key file is created either, since `Load` returns before
`loadOrCreateKey`.

### `Clamp`'s empty range, documented

`Clamp(i, 0, len(x)-1)` over an empty `x` asks for `[0, -1]`. That is not a
bug — -1 is the package's "no selection", so the index lands there instead of
on row 0, which doesn't exist — but the doc said only "restricts v to [lo, hi]"
and the behaviour is now generic and reachable from ~40 call sites.

Writing the test caught the doc draft being wrong: `lo` is applied *first*, so
an empty range returns `hi` only for `v >= lo`, and `Clamp(-5, 0, -1)` is 0,
not -1. Both the comment and `TestClampEmptyRangeYieldsHi` now say so. No call
site passes a negative index, but that asymmetry is the part a reader would
guess wrong, and guessing wrong here means "simplifying" Clamp into returning
`lo` and quietly moving every empty-collection selection onto a phantom row 0.

## Two refactors, and what they actually bought (2026-08-14)

Both were named in the review as line-count wins. Neither was. What each
bought is one copy of a shared shape instead of two or six — worth doing, but
the estimates were wrong and it is worth writing down why.

### The six schema-scoped Object Explorer loaders

`loadViewsChildren` / `loadSystemViewsChildren` and the same pair for stored
procedures and functions were six copies of one function: resolve the
database, list one kind of object, build "schema.name" nodes carrying a
CreateDate, and (for the user half) put a "System …" folder in front. They
differed in the fetch method, the NodeType, the folder label, and nothing
else.

Now `loadSchemaScoped` plus `withSystemFolder`, with the six registry entries
reduced to their differences. Go can't reach a struct field through a type
parameter, so each of the three gosmo types supplies a two-line accessor
(`viewFields`, `procFields`, `funcFields`) instead of the union constraint
that would read better.

**Net: -3 lines.** The estimate in the review was "~90 → ~35", which counted
the bodies being deleted and not the helper and accessors replacing them. The
real gain is that the `DatabaseByNameContext` dance and the node construction
exist once, so the next change to how these nodes are built happens once
rather than six times, with no compiler help if a copy is missed.

Verified against win10cli rather than trusted: a scripted tmux drive expands
HealthClinic's Views, System Views, Stored Procedures, System Procedures,
Functions and System Functions and dumps the tree, run against a pre-refactor
binary and a post-refactor one. **Byte-identical**, all six folders, including
the several-hundred-entry sys lists.

### The two history charts

`HistoryChart` and `StackedHistoryChart` had identical `Draw`, `Plot` and
`TimeRow` methods, differing only in where an auto-scale takes its maximum
(`maxValue` vs `maxStackTotal`) and in `drawColumns`. Both now convert
themselves into an unexported `historySpec` and the shared code in `common.go`
does the rest. `autoMax` is a func rather than a value so it stays as lazy as
the `sc.IsZero()` check that used to guard it — these charts redraw on every
collector tick.

**Net: +2 lines**, all of it the doc comment. The point is not the size: the
chrome sequence — gutter, plot, time row, legend, and the order they draw in —
was written twice, and nothing fails to compile when only one copy learns
about a new piece of chrome.

Verified by golden dump rather than by the existing 21 tests passing: a
throwaway test rendered both chart types across 504 combinations of size,
interval, legend rows, grid spacing and series count to a `Canvas`, dumping
the text plus the reported `plot`/`timeRow` rects. Run in a worktree at HEAD
and again on the refactor — **identical**. That covers the degenerate sizes
(1x1, 3x2) and the empty-series cases the hand-written tests mostly skip.

### Not done, deliberately

`agent_menu.go`'s `setAgentXEnabled`×4 / `deleteAgentX`×4 / `agentXMenuItems`×3
are already thin adapters over `setAgentEnabled`/`deleteAgentEntity`; naming
the gosmo type per entity is the clarity that pays. `HandleScrollbarDrag`/`H`
and `perm_state.go`'s `databasePermApply`/`serverPermApply` are axis and scope
mirrors — a `horizontal bool` parameter would be worse than the duplication.

## The detail backfill bounded the work but not the goroutines (2026-08-14)

`DetailBrowser.backfillRows` started one goroutine per row and had each of
them acquire a token from an 8-slot semaphore. The *fetches* were capped at 8,
which is what `maxRowFetchConcurrency`'s comment promises and what the
connection pool needs — but the goroutines enforcing that cap were not capped
at all. A folder with hundreds of entries parked hundreds of goroutines whose
only job was to wait in line.

Now a fixed pool of `min(n, maxRowFetchConcurrency)` workers pulling row
indices off a channel. Workers, not tokens. The per-row body moved to
`backfillRow`, which is where its `recover` has to live: with one goroutine
per row a panic cost only that row, but a worker that dies takes every row it
still owes with it. `rowFetchSemaphore` had no callers left and went with it.

The FIFO argument the caller depends on still holds and is slightly stronger:
every row's closure is queued before *any* worker's `wg.Done`, so a `cacheOnly`
posted after `backfillRows` returns is still appended last.

Two new tests. `TestBackfillRowsGoroutinesAreBoundedNotJustFetches` samples
`runtime.NumGoroutine()` from inside the fetches — the distinction matters,
since concurrent *fetches* were already capped and a test that measured those
would have passed on the old code. Run against HEAD it reports **399
goroutines over baseline for 400 rows**; after, 8 plus slack.
`TestBackfillRowsPanicDoesNotKillTheWorker` panics every worker's first row and
asserts the remaining 32 still get fetched.

Live A/B on win10cli, both progressive loaders: the Databases folder's
backfilled Total/Data/Log/Avail columns and HealthClinic's Tables folder's Row
Count/Data/Index/Unused columns, pre-fix binary against post-fix.
**Byte-identical**, volatile Avail-Log figure included.

## Second review pass: three fixes, one verified against the running binary (2026-08-14)

A second full review of both repos, the same day as the pass above and
deliberately not repeating it. Same mechanical baseline, re-run and still
clean: `go build`/`vet`/`test`/`-race` and `staticcheck -checks all` on both,
no TODO/FIXME in either, every SQL literal interpolation in gosmo through
`escapeSingle`/`QuoteLiteral` (the one bare `'%s'`, `backup.go:406`, is a
formatted timestamp), `rows.Close`/`rows.Err` everywhere. Three findings went
in; two more are recorded in `docs/open-threads.md` rather than fixed.

### `core.WrapText` never broke an over-long word, and every caller clipped it

The headline, and the one with a user-visible face. `WrapText` joined
`strings.Fields` greedily and never split a *single* token, so a word wider
than the wrap width came back as one over-wide line. Every caller then draws
through `DrawTextClipped` — so the overflow was cut at the pane's right edge
with no ellipsis and no way to reach it. Four call sites: the Log File
Viewer's details pane (`log_viewer_draw.go`, the one place built for reading
an entry *in full*), `wrapMessage` in `backup_common.go`, `fitMessage` behind
every Alert/Confirm/Prompt/TypedConfirm, and `propsheet`'s `noteRow`.

A/B'd in the binary, not just in a test: a pre-fix build and a post-fix build,
each at 64x22 under tmux with `XDG_CONFIG_HOME` pointed at a scratch dir, both
asked to connect to a 68-character unbroken hostname. **Pre-fix the name is cut
at 40 columns and `.invalid` never appears on screen at all** — the dialog
jumps straight from forty `a`s to `gosmo: ping: lookup`, and the informative
end of the name the user got wrong is invisible. Post-fix it continues onto the
next line and reads whole.

The fix hard-breaks at a grapheme boundary via `splitGrapheme`, which always
takes at least one grapheme — that is what terminates the split loop when a
single grapheme is itself wider than the line (a CJK rune in a one-column
pane). Two traps found while writing it, both caught by the tests: a word that
divides exactly into full lines left a blank line on the end, and the
"no line exceeds w" invariant has to exempt the lone over-wide grapheme.

`splitGrapheme` deliberately does *not* replace the clip loops in
`PadRight`/`PadLeft`, which the review had flagged as duplication: their
contract is the opposite one. A fixed-width cell must never exceed `n`, so it
drops a wide grapheme that would straddle the edge — exactly the case where
`splitGrapheme` must emit one. Same-looking loops, incompatible guarantees.

Also corrected while in there: `splitLogLines`' doc comment claimed the
details pane preserves the startup banner's tab-indented structure. The line
breaks survive; the indentation does not, since `WrapText` splits on
`strings.Fields`. The comment now says so.

### `core.Itoa` was wrong at `MinInt`, and had no reason to exist

`Itoa` negated `n` to take its digits, which is a no-op at `math.MinInt` — the
loop then produced nothing and the answer was `"-"`. `FormatThousands(int64)`
built on it, via an `int(n)` narrowing that also truncates on a 32-bit target,
and answered `"--"` at `math.MinInt64`. Both verified before the change.

Unreachable from today's callers (row counts, MB, percentages, line numbers),
but the comment justifying the hand-rolled loop — *"without importing
strconv"* — did not survive contact: nothing constrains `core` that way and
the file already imports `strings`. `Itoa` is gone and its seven call sites
call `strconv.Itoa`; `FormatThousands` keeps its comma logic over
`strconv.FormatInt` and is pinned at both int64 extremes.

### A panic left every "busy" latch set for the object's lifetime

Systemic, low probability, and the same failure mode as `backfillRow`'s
`markFailed` recovery. Eight sites set a flag before `safego` and clear it only
inside the callback the goroutine posts on completion — which a panic unwinds
straight past. `safego` reports the panic; nothing releases the latch. The Log
File Viewer's `toolsEnabled` gates its whole toolbar on it, so Refresh, Export
and both selectors go inert until the panel is closed; an Activity Monitor proc
tab sits at "Connecting..."/"Running..." with its buttons dimmed;
`completion_inventory`'s `loading` makes every later lookup see a load in
flight that no longer exists, so IntelliSense never comes back for that
database; `PropDialog.runPageActionOnce`'s latch makes the button refuse every
later click.

One mechanism, `App.safegoRepair(what, repair, fn)`, with `repair` queued on
the UI goroutine only on a panic and *before* the report, so the status bar's
last word is the panic. Each site's repair is written for what it latched:
`amProcTab.panicRepair` clears busy and rebuilds the toolbar,
`LogViewer.readPanicked` is seq-guarded so a superseded panic can't unlatch a
newer read, and the two completion loaders **evict** the entry rather than
merely unlatching it — the next lookup then retries from scratch instead of
reading a catalog half-built by the fetch that died, which is what the
existing closed-connection branch already does for the same reason.
`PropDialog` grew `pageActionBody` so the latched and unlatched forms share one
goroutine body instead of duplicating the timeout dance.

A/B'd: reverting `runPageActionOnce` to plain `safego` makes
`TestPageActionLatchClearsWhenTheActionPanics` time out waiting for the latch,
which is the bug exactly. The test asserts the *next* click runs, not merely
that the flag flipped.

The rule is now in `CLAUDE.md` § Mouse, overlays, and async UI, beside
`postAndWake`'s.

### `backfillRows` could hang the loader, not just lose a row

Found reading the pool that went in earlier the same day. `backfillRow`
recovers a panicking fetch, so a worker dying needs a panic raised inside that
recovery — but the producer handed indices out from the loader goroutine, so if
that ever took every worker, `rowIdx <- i` blocked forever and the whole folder
hung rather than losing the rows one worker owed. The queue is now filled and
closed before any worker exists, so no send can block on one; `n` ints is a few
KB at the folder sizes this runs on. The worker loop also grew a
`defer app.recoverPanic(what)` — it is the one goroutine in the package spawned
directly rather than through `safego`, and a panic escaping it takes the
process down with the terminal still in raw mode.

## Second review pass, findings 3 and 5 (2026-08-14)

The two the previous round left recorded rather than implemented. Both entries
are gone from `docs/open-threads.md` now that the work is done.

### gosmo `Table.IndexesContext` was N+1, at two round trips per index

`IndexesContext` called `indexColumnsContext` *inside* its own `rows.Next()`
loop — the only nested-query-in-a-row-loop in the library. `Database.query`
pins its own pooled `*sql.Conn` and issues its own `USE`, so a table with 20
indexes cost roughly 42 round trips across 21 connection acquisitions, with the
outer connection held throughout. The worst caller is
`internal/tui/index_props.go`, which fetches every index on the table to
display one.

Now two queries whatever the index count: `indexListContext` for the indexes,
then one `sys.index_columns` query for the whole object — no `index_id`
predicate, ordered by `index_id, key_ordinal, index_column_id`, grouped into a
`map[int][]IndexColumn` in Go. The split into a helper is what lets the first
query's rows close before the second opens, so the two never hold two pooled
connections at once. A table with no indexes skips the column query entirely,
which matters for a folder full of heaps. Rows for `index_id` 0 come back and
are simply never looked up; excluding them would cost a predicate to save at
most one row.

The live A/B this needed before it could land, on win10cli against a throwaway
`gossms_idx_ab` built to hit every shape — composite DESC keys, included
columns, a filtered index with non-default fill factor and lock options, a
disabled index, PAGE compression, a unique constraint, a heap with a
nonclustered index, clustered and nonclustered columnstore, primary and
secondary XML, spatial, a table with no indexes at all, and a schema, table and
column whose names need bracket-quoting (`[odd schema].[t.dotted]`, a column
named `col]bracket`):

- **Output byte-for-byte identical**, pre-fix binary against post-fix, over
  `gossms_idx_ab` and `HealthClinic` — 82 lines, 35 indexes, every flag, every
  key and included column in order. Both binaries were checked with `strings`
  for their own query text first, after the previous round's A/B was briefly
  run against a binary built from the wrong source.
- **~640ms → ~50-120ms** over the 18-index database, three runs each.
  `HealthClinic`, whose tables carry one to four indexes, barely moves — the
  win is proportional to indexes per table, which is what an N+1 predicts.
- Driven in the real binary as well: Index Properties on `IX_composite_desc`
  shows `a` Descending, `b` Ascending, `c` Descending in order; on
  `IX_with_included` the Included Columns page has exactly b, c and d ticked;
  `t_noindex`'s Indexes folder opens empty with no error. Throwaway database
  dropped afterwards.

`TestIndexesUsesOneQueryForEveryIndexColumn` pins the query count at one per
object for three indexes, and the grouping with it — including a heap row the
server really does return. `TestIndexesSkipsTheColumnQueryWhenThereAreNoIndexes`
pins the empty case. Both needed the capture driver to reply with more than one
row, so `cannedRow` grew a `rows` field beside its `row` and `captureLog` a
`count`.

### The three small consistency items

- `agent_detail.go`'s `agentScheduleDetail` hand-wrote `.Format("2006-01-02")`
  with a zero-check for the end date and none for the start, so a zero
  `ActiveStartDate` would have rendered `0001-01-01`. Both dates now go through
  `formatAgentDate`, which is in the same package and already answers correctly.
- `restore_dialog_draw.go` hand-wrote `"2006-01-02 15:04:05"`; it calls
  `formatSQLDate` now, which is that layout plus the zero-Time blank.
- `dashboard/history.go` laid every chart out twice per frame — `c.Draw(inner)`
  and then `c.TimeRow(inner)`, each running `historySpec.layout` and with it
  `autoMax()`, a walk over every bucket of every series. `Draw`'s doc comment
  says it returns the plot rect precisely so a caller need not repeat the pass,
  and the very next argument repeated it. `HistoryChart`/`StackedHistoryChart`
  gained `DrawFrame`, returning plot and time row from the one pass;
  `historySpec.draw` became `drawFrame` and `Draw` is now a wrapper on it.
  `BenchmarkDrawHistory` moves ~3.51ms → ~3.33ms median over six runs — small,
  as the open thread predicted, and the point is the shape. What makes it safe
  is `TestDrawFrameAgreesWithDrawAndTimeRow`, which asserts both rects match
  `Draw`/`TimeRow` across four rects including a chart too short for a time row,
  and `TestDrawFrameDrawsWhatDrawDraws`, which compares every cell of two
  canvases.

The fourth item on that list, folding `PadRight`/`PadLeft`'s clip loops into
`splitGrapheme`, was retired in the previous round rather than deferred: their
contract is the opposite one. A fixed-width cell must never exceed n, so it
drops a straddling wide grapheme, where `splitGrapheme` must emit one or
`WrapText`'s loop cannot terminate.

## File > Open was lossy and File > Save destroyed the original (2026-08-14)

Third review pass, finding 1, fixed complete. One root cause with four symptoms,
all reproduced in the running binary before the fix and re-checked after.

`openQueryFile` did `os.ReadFile` → `editor.SetText(string(data))` and then
`qp.savedText = string(data)`. `SetText` normalizes what it is given — expands
tabs, folds CRLF to LF, and `[]rune()` turns every invalid byte into U+FFFD —
so `savedText` was compared against text the editor never held. Only a plain
ASCII, LF, tab-free file survived the round trip.

- **The BOM was never stripped.** A UTF-8-with-BOM script — what SSMS and
  VS Code on Windows write — sent its U+FEFF to the server inside the first
  batch, and SQL Server answered `Incorrect syntax near '﻿'`, pointing at a
  character that does not render. Verified live on win10cli before the fix.
- **UTF-16 was not detected.** SSMS's *own* default `.sql` encoding loaded as
  mojibake that still read as plausible SQL, the NULs invisible on screen.
- **Every such file opened marked dirty**, because `savedText` held the raw
  bytes. A file with one tab in it showed `*` untouched.
- **And so closing it prompted to save, and the save rewrote it.** `utf16.sql`
  went from 80 bytes of UTF-16 LE to 88 bytes of `efbfbd efbfbd 5300…`,
  unrecoverable. That is the sequence that made this the pass's one HIGH: the
  false dirty flag is what walks the user into the destructive write.

`internal/tui/text_encoding.go` is new: `decodeTextFile` detects the encoding
**from a BOM and nothing else** — guessing wrong rewrites a user's script in an
encoding they never chose — and reports the line ending alongside it;
`encodeTextFile` is its inverse. `QueryPanel` carries the pair as
`fileEnc`/`fileCRLF`, whose zero values are already right for a panel with no
file, and `writeQueryFile` re-encodes through it. A file with no BOM that is not
valid UTF-8 cannot be round-tripped at all, so it loads with U+FFFD and says so
in the status bar rather than converting it silently.

Live A/B in the built binary over four fixtures: `utf16.sql` (UTF-16 LE + CRLF)
and `bom.sql` (UTF-8 BOM + CRLF) open, display correctly, show no `*`, and save
back **md5-identical**. `tabs.sql` and `latin1.sql` open clean and undirty; an
explicit save converts the tabs to spaces and the `\xe9` to U+FFFD, which is the
editor's documented behavior and the warned-about case respectively — neither is
reachable without the user choosing to save.

The same one-line defect sat in `openCellValuePanel`, directly under a comment
promising the opposite ("savedText is seeded so the panel isn't born dirty").
An XML or JSON cell value containing a tab or CRLF was born dirty, and with no
`filePath`, closing it pushed the user into Save As for a value they only wanted
to read. Both sites now read `savedText` back from `qp.editor.Text()`.

## Review findings 2-4 (2026-08-14)

**Folding an overflowing message re-injected spaces into hard-broken words.**
`dialogs.fitMessage` and `tui.wrapMessage` both did
`strings.Join(lines[maxLines-1:], " ")`. That was right while `WrapText` only
broke at spaces, and wrong the moment it started hard-breaking long tokens
(earlier the same day) — an unreachable UNC path came back as two
plausible-looking ones. The two were the same wrap-then-fold algorithm
duplicated across the layer boundary, so the fix and the dedup are one change:
`core.WrapTextLimit`. It shares `wrapLines` with `WrapText`, which now also
reports, per line, whether the break after it fell inside a word — the fold
rejoins with a space only where the wrap took one out. Verified live: a
Connection Error dialog on a 60x14 terminal folds
`…example.inval` / `id/share/…` / `…that.` / `keeps.going:` with nothing
inserted at any of the three hard breaks, and clips the tail with "…".

**`PadRight`/`PadLeft` did not deliver the exactly-n columns they promise.**
Found by fuzzing, which neither repo had before. `PadRight("0\xcc", 2)` came
back 3 columns wide: padding is concatenation, and a grapheme cluster can
absorb the bytes after it, so `"0\xcc"` measures 1 column while `"0\xcc "`
re-segments into 3. Only invalid UTF-8 can end mid-cluster, so `padSpaces` —
now the shared body of both, which also retires the byte-identical clip loops —
runs `strings.ToValidUTF8` first. **No reachable path:** go-mssqldb was probed
directly with `varchar 0xCC`, `0xFF` and unpaired surrogates and always hands
back valid UTF-8, and every other caller passes app text. This is a false
invariant made true, not a live bug fixed. The A/B is in
`TestPadIsExactlyNColumnsForAnyInput`, which measured 3 against the pre-fix
body and 2 after.

**`os.WriteFile` for a user's own files.** `config.json` got the full
temp-file + Chmod + Sync + rename + syncDir treatment while a `.sql` script the
user spent an hour on was truncated in place. `writeFileAtomic`/`syncDir` moved
verbatim out of `internal/config` into a new `internal/fileutil` — extracted by
exact range and diffed, the only changes being the exported name and a `perm`
parameter — and now back both `writeQueryFile` and the Log File Viewer's
export. `query_panel_export.go` deliberately stays on `os.Create`: Results To
File streams to a fresh output file rather than replacing one.

**Smaller items.** `LogViewer.detailLines` guarded `w < 1` and then wrapped at
`w-2` for its two-space indent, so a 1- or 2-column pane handed `WrapText` a
non-positive width and got the paragraph back unwrapped — one long line
`DrawTextClipped` cut at the edge with no ellipsis. Now `w < 3`. Its
`len(wrapped) == 0` branch was unreachable (`WrapText` never returns an empty
slice) and is gone; an empty paragraph produced the same two spaces either way.
The three database-scope Permissions pages built their principal list in blocks
identical character for character, and two of them their entry list as well:
`databasePermPrincipals` and `objectPermEntries`. That last one is the change
in this batch **not** verified against a live server — it is a pure data
transform with unit tests pinning order and the named-string-type conversion,
but the pages themselves need a connection.

Left alone deliberately: `gosmo.ErrNotFound` is consumed at exactly one of
gossms's ~40 lookup sites. Not a defect — the other sites want the `(nil, nil)`
convention — but whether it is meant to be used more widely is a call to make
once rather than rediscover per review.

---

## The missed half of the busy-latch pass (2026-08-14)

Phase 1 of a cross-repo review. The 2026-08-13 pass ("a panic left every 'busy'
latch set for the object's lifetime") fixed eight `safego` sites and missed
eight more. Found by auditing all ~57 `safego`/`safegoRepair` callers for state
latched on the UI goroutine *before* the `go`, whose only release is inside the
callback the goroutine posts at the end — which a panic unwinds straight past.

**Query execution was the worst of them, and the one the mechanism was written
for.** `safego`'s own doc names go-mssqldb's `makeGoLangTypeName` panicking on
an unknown column type ID, reached through `query.scanResultSet`'s
`DatabaseTypeName()` — which runs on this exact goroutine. `startRun` latched
`p.executing`, `p.cancel` and a `tickExecuting` goroutine, and released all
three from plain statements at the bottom of the body. One panic therefore left
the panel refusing every later Execute (`menu.go`, `toolbar.go` and
`runEstimatedPlan` all gate on `p.executing`), the context uncancelled, and the
ticker alive — `wakeEventLoop()` once a second for the rest of the process's
life, one leaked goroutine per panicked run. `cancel()` and `close(done)` are
now `defer`red, `execPanicked` is the `safegoRepair` step, and `done` is kept
as `p.execDone` so a test can see the ticker was stopped.

The A/B is `TestExecutePanicReleasesTheLatchAndAllowsTheNextRun` and
`TestExecutePanicStopsTheTickerAndCancelsTheContext`: both time out against the
pre-fix body and pass after. Neither needs an injected panic — a `ServerConn`
with a nil `gosmo.Server` makes the `sc.Server.DB()` inside the goroutine
dereference nil, which is a real panic raised where the driver's would be.

The other seven: both `runPipeline`s (`PropDialog` and `newObjectDialog`),
where `SetApplying(true)` makes `PropertySheet.activateButton` ignore *every*
button — Cancel included — so the dialog was fully inert;
`newObjectDialog.onLoadPage`, where a stuck `d.fetching` means no page of that
dialog ever loads again; both Activity Monitor collectors, whose
`collectorStopped`/`tempDBCollectorStopped` were already written and already
`c`-guarded, so they were their own repair; and the backup and restore starts,
where a panic past `postTaskDone` leaves a `*Task` that is never `Done`, so
`pruneFinishedTasks` never evicts it and the status bar counts it as running
for the rest of the session. `postTaskDone`'s body split out as `markTaskDone`
for the repair, which is already on the UI goroutine and must not add a second
`postAndWake` hop — that would land *after* the panic report and overwrite it.
`TestSheetApplyingBlocksButtonsUntilCleared` pins what the applying latch
actually disables, which is what makes it worth repairing.

**A lossy UTF-16 decode was reported only for a trailing odd byte.**
`decodeTextFile` derived `lossy` from `len(data)%2`, while `utf16.Decode` maps
an unpaired surrogate to U+FFFD — its own comment said so. A UTF-16 `.sql` with
a lone surrogate opened with no warning and Save wrote the replacement
characters back for good: the same failure as the `File > Open` UTF-16 bug from
the day before, through a narrower door. `decodeUTF16` now returns `lossy`
itself, covering both causes; `hasUnpairedSurrogate` is the scan.
`TestDecodeUTF16SurrogatePairIsNotLossy` is the other half — an emoji in a
comment must not flag the file as damaged.

**A finding that did not survive verification.** The review called
`peer.go`'s `p.IsOpen()` a data race against `closePeers`, which closes each
peer *outside* `peerMu` while `ServerConn.closed` is written by `Close`. It is
not: every `IsOpen` on a cached peer happens under `peerMu`, and `closePeers`
takes that same mutex before any peer is closed, so the write is always ordered
after the read — and once `closePeers` has run, `sc.peers` is nil and there is
no cached peer left to test. A `-race` test written to reproduce it (50 rounds
of `Peer` against `Close`) reports nothing, with or without the "fix". The
attempted fix was reverted; `TestPeerLookupRacesDisconnect` stays, pinning why
the shape is safe so the next reviewer doesn't re-raise it.

Also: `evictInventory`'s doc block had been orphaned onto `loadPanicked` when
that function was inserted between the comment and its `func` line, so the
"identity check is what makes this safe" paragraph documented a function with
no identity check. Moved back.

## The per-database fan-out that measured slower (2026-08-14)

Phase 2 of the same review. Two Properties pages walk every ONLINE database
inside one `propFetchTimeout` — `fetchNewLoginPrefetch` (`DatabaseRolesContext`
*and* `SchemasContext`, so 2N round trips before the New Login dialog shows any
page) and `pageLoginUserMapping` (`DatabaseRolesContext` on top of gosmo's own
per-database `UserMappingsContext`). The plan was to fan them out across a
bounded worker pool, the shape `DetailBrowser.backfillRows` already uses.

**Built it, measured it, threw the concurrency away.** Against the live
instance with 40 throwaway databases added (46 online), an 8-wide fan-out of
the New Login per-database work was *slower than serial on every run*, at every
width from 2 to 8:

| width | run 1 | run 2 | run 3 |
|---|---|---|---|
| 1 | 0.41s | 0.47s | 0.37s |
| 2 | 0.62s | 0.64s | 0.73s |
| 4 | 0.77s | 0.79s | 0.88s |
| 8 | 0.57s | 1.54s | 0.64s |

`gosmo.Database.query` pins a pooled connection of its own and issues its own
`USE`, so each worker needs a *physical* connection — and on the pool a
freshly opened dialog actually has, that means a TCP+TLS+login handshake per
worker. A handshake here costs ~180ms; the query latency it overlaps costs
~3.6ms. The concurrency only pays once eight connections are already idle: with
a pre-warmed pool the same fan-out did win, 0.34s → 0.13s, which is exactly the
measurement that would have shipped a regression if it had been the only one
taken. Both halves are now serial with a comment carrying the numbers, so the
next reviewer doesn't re-derive this.

**What was kept.** The two loops now *skip* a database whose fetch fails
instead of failing the whole page — the inconsistency the review actually
found, since gosmo's `UserMappingsContext` on the very same page has always
skipped an unreachable-but-ONLINE one (its comment says so) while the gossms
loops beside it returned the error. One inaccessible availability-group
secondary took down a page with 45 other databases to show. `eachDatabase`
(`db_scan.go`) states that policy once for both, and `onlineDatabases` replaces
the two hand-rolled filters. Verified live by connecting as a login mapped into
exactly one database: `UserMappingsContext` returned that one mapping rather
than an error, with the other 45 skipped.

`Login.UserMappingsContext` kept the `userMappingsIn` split the fan-out
attempt introduced — one database's read, with the skip-vs-abort boundary
stated on it — which is worth having on its own.

**Also.** `applyConfigRows` re-read `sys.configurations` once per dirty option
via `ConfigurationByNameContext`; it now reads the whole set once, lazily, so an
apply with three dirty options costs one round trip instead of three and one
with nothing dirty still costs none.

Both dialogs were driven end to end under tmux against the live instance before
and after, showing all 46 databases with their roles. The 40 throwaway
databases and the test login were dropped; `sys.databases` and
`sys.server_principals` were checked clean afterwards.

---

## Phase 3 of the review: a "duplication" that was hiding a shipped bug (2026-08-14)

Phase 3 was the tidying half of the review plan — a doc gap, two
micro-optimisations, two duplications and two consistency nits. One of the
duplications turned out not to be cosmetic.

**Every replica but the first was unreachable on two AG Properties pages.**
`ag_props_backup.go` and `ag_props_routing.go` both wired their per-row detail
editor to the grid the same way:

```go
grid.OnSelectRow = func(int) {
    commitCurrent()
    syncFromSelection()
    grid.SetData(headers, gridRows())   // ← resets the selection to 0,0
}
```

`DataGrid.SetData` resets `selRow`/`selCol` — it says so on
`RefreshColumnWidths`, which exists precisely because SetData doesn't preserve
them — and this redraw runs from *inside* `OnSelectRow`, after the grid has
already moved. So the move was undone the moment it happened. Two symptoms,
and the second is the one that made the pages unusable rather than merely
annoying:

- clicking any row other than the first left the cursor on the first;
- `propsheet.GridRow.HandleKey` reports movement by comparing `SelectedCell`
  either side of the key, so a grid that always came back to 0,0 answered
  "not handled" — and `propsheet.Form` took that as its cue to move focus on.
  The first arrow key threw focus straight out of the grid onto the field
  below it.

Between them, there was no way — mouse or keyboard — to select the second
replica, and therefore no way to set its backup priority, read-only routing
URL or routing list. Both pages have shipped like that since Always On phase 2.

Found by reading the two functions side by side to extract their shared
scaffolding, and noticing that the `login_props.go` User Mapping page — the
same idiom, written earlier — saves and restores the cell around its own
`SetData` while these two don't.

**Verified live, A/B, against AAG1** (ubusql1/ubusql2, two replicas), driving
both binaries under tmux to Backup Preferences and reading the focused-cell
background out of `capture-pane -e`:

| | pre-fix | post-fix |
|---|---|---|
| click ubusql2 | cursor lands on ubusql1 | cursor lands on ubusql2 |
| then Down | focus leaves the grid for the Backup priority field | ubusql1 → ubusql2 |
| then Up | — | back to ubusql1 |

Both dialogs were cancelled with Escape; nothing was applied to the group.

The fix is the extraction itself: `wireGridEditor` (`ag_props.go`) owns the
`OnSelectRow` wiring, the save/restore around `SetData`, and the redraw the
pages' `RevertFn`s need, and the reason for the restore is stated on it.
`ag_props_grid_editor_test.go` drives a real `DataGrid` through real key events
and pins all three behaviours; removing the restore fails two of them.

**The rest of Phase 3, as planned.** `ARCHITECTURE.md` gained a
*"When the goroutine latched UI state first: safegoRepair"* section next to the
`safego` one — `CLAUDE.md` has stated the rule since 2026-08-13 and pointed at
`ARCHITECTURE.md` for the reasoning, which wasn't there. `core.Truncate` walked
the graphemes twice (once via `displaywidth.String` for the fits-already check);
it now answers both questions in one pass, checked against the old
implementation over 200k random strings including ZWJ emoji clusters and
combining marks. `dialogs.fitMessage` wrapped its message twice whenever the
height cap fired; it computes `maxLines` first and wraps once.
`login_props.go` and `new_login_pages.go` share `setRoleToggles`. The
`.corrupt` config sidecar goes through `fileutil.WriteAtomic` like everything
else the package writes — it is the only remaining copy of bytes nothing can
reconstruct, so a partial one is the loss it exists to prevent.

**One planned item was a non-finding.** The plan flagged `Execute` and
`Estimated Execution Plan` as disagreeing about their proactive gate. They
don't: both, plus the toolbar's three execute-family buttons, gate on
`activeQueryPanel() != nil` alone and carry the same reactive guards
(`isConnected`, `p.executing`) inside `runQuery`/`runEstimatedPlan`. The
`qp.conn != nil && !qp.executing` gate the plan attributed to Execute belongs
to *Reconnect*, where it is correct. Left alone.

---

## Sweeping for the AG grid bug's siblings (2026-08-14)

Phase 3 found one instance of "redraw the grid from inside its own
`OnSelectRow` and the selection is silently undone". This pass went looking for
the rest of the class, and found four more — every one of them, like the first
two, in Always On code.

**The audit.** A script resolved each of the 33 `OnSelectRow`/`OnActivateCell`
assignments in the tree to its callback body (following the named closures, not
just the inline literals), then looked for a `SetData`/`SetSource`/`SetRows` on
*that same grid* with no `SetSelectedCell` to put the cursor back. It also
flagged every grid in the tree that gets `SetData` more than once, so the
non-callback redraws — button handlers, `DirtyFn`, an `OnChange` — got read too.

Reentrant resets, all fixed:

| Site | What was unreachable |
|---|---|
| `ag_props.go` General | every replica but the primary — so no secondary's availability mode, failover mode, seeding mode, readable-secondary setting or session timeout could be edited |
| `ag_props_backup.go`, `ag_props_routing.go` | fixed in Phase 3 |
| `new_ag_pages.go` databases | only the *first* database could be included in a new group |
| `new_ag_pages.go` replicas, backup | same, for the replica list and its priorities |
| `owner_transfer_page.go` | not reentrant, but `transferRow.SetOnChange` redrew without the restore: picking a new owner snapped the grid cursor to the first row while the detail rows below went on showing the row the user was actually editing |

Everything else came back clean or benign. `propsheet.ToggleGridRow.activateCell`
already does `render()` then `SetSelectedCell` — the pattern done right, and
where it was presumably learnt. The add/remove-button redraws
(`ag_add_listener_dialog.go`, `ag_listener_props.go`, `new_endpoint_dialog.go`)
reset the cursor to row 0 after the list changes, which is defensible and not
this bug. `activity_monitor_proctab.go`, `log_viewer.go`, `planview/summary.go`
and `query_panel_exec.go` re-populate from genuinely new data, where resetting
is correct.

**The shared primitive.** `redrawGrid(grid, headers, rows)` in
`prop_grid_helpers.go` does the save/`SetData`/restore, and states why on
itself. `wireGridEditor` (ag_props.go) is now built on it and used by all five
AG sites; `owner_transfer_page.go` calls it directly, since its wiring differs.

**Verified live, A/B, on both servers.** Against AAG1 (ubusql1/ubusql2) for AG
Properties General — pre-fix, clicking `ubusql2` landed on `ubusql1` and the
first arrow key threw focus out of the grid; post-fix, click and Up/Down all
land where they should. Against the New Availability Group dialog with three
databases (two throwaway ones created for it) — same result, plus the
commit-on-move round trip: check "Include the selected database" on
`t4_grid_b`, move off, and the row reads True. Against win10cli for Owned
Schemas, with a throwaway database, user and three schemas — post-fix the grid
cursor stays on `t4_sch_b` across the dropdown change, pre-fix it does not.
Every dialog was escaped rather than applied; `sys.availability_groups`,
`sys.availability_replicas` and `sys.schemas` were re-read afterwards to
confirm nothing was written, and all throwaway objects were dropped and
verified gone on both instances.

**Tests.** `ag_props_grid_editor_test.go` (three, from Phase 3) drives a real
`DataGrid` through real key events. `new_ag_grid_editor_test.go` adds the layer
they don't reach: the New AG databases grid wired exactly as `buildGeneralPage`
wires it, driven through a real `propsheet.Form`. That one is worth having
because its failure message is the whole bug in one line — with the restore
removed it reports `after Down #1 grid is on row 0, want 1 (focused row now
*propsheet.CheckRow)`, i.e. the grid didn't move *and* the form took focus away
from it. All four fail with the restore removed from `redrawGrid` and pass with
it.

---

## Cross-repo review: RevertFn's grid redraws, and fileutil's first tests (2026-08-14)

A review pass over both repos. Everything mechanical was already clean on both
— `go build`, `go vet`, `gofmt -l`, `staticcheck -checks=all`, `go test` and
`go test -race` all pass with nothing to report — so this records only what the
pass actually changed. The rest of what it found is in `docs/open-threads.md`.

Environment note that cost time: `/tmp` here is a 2 GB tmpfs, and a `-race`
link needs more than it has free, so `go test -race ./...` fails with
`disk quota exceeded` from the linker rather than from anything in the code.
`TMPDIR=~/.cache/gotmp go test -race ./...` passes.

**`RevertFn` was the one place `redrawGrid` had not reached.** The 2026-08-14
sweep that introduced it covered `OnSelectRow` and the grid-plus-editor idiom;
`RevertFn` was not on it. Thirteen of the fourteen `RevertFn` bodies still
called `DataGrid.SetData` directly. Most are correct as they stand and were
left alone — `membership_page.go`, `extended_properties_form.go`,
`database_props_files.go` and the three `agent_job_props_*` pages all *drop
rows* on Revert (anything added since load), and a changed row set is the case
where resetting the cursor is the deliberate answer. Five could not: the row
set is fixed and Revert only restores values. Those now use `redrawGrid` —
`login_props.go`, `new_login_pages.go` (twice) and
`server_permissions_matrix.go` (twice).

**The second bug was the one worth finding.** Both user-mapping pages —
`pageLoginUserMapping` and `buildNewLoginUserMappingPage`, which are the same
page twice — followed the redraw with `syncFromSelection(selected)`, and
`syncFromSelection` opens with `commitCurrent()`. At that moment the schema
box and the role toggles still hold the *pre-revert* values, so the commit
wrote the selected row straight back to what Revert had just undone. Revert
worked on every row except the one the user was looking at. The fix is to clear
`selected` before the resync so `commitCurrent` no-ops; `syncFromSelection`
sets it again on the way through, so nothing else changes. Note this is not the
same trap as the `OnSelectRow` family: there the commit is load-bearing and
must keep running, which is why clearing the latch is scoped to Revert alone.

**Tests.** `prop_grid_revert_test.go` drives a real `DataGrid` through real key
events, in the harness style of `ag_props_grid_editor_test.go`. Three: the
cursor survives Revert, the selected row's uncommitted edit is discarded by
Revert, and — the guard against over-fixing — an ordinary row change still
commits. A/B'd by putting the old `SetData`-plus-`syncFromSelection(selected)`
shape back into the harness: the first two fail, naming the cursor reset and
the re-commit respectively, and the third stays green either way.

**`internal/fileutil` had no tests at all**, having arrived the same day as the
newest code in the tree while guarding `config.json`, `gossms.key` and every
`.sql` the user saves. `atomic_test.go` covers contents, both mode paths, whole
replacement, the empty file, the temp file being a sibling of the target, and
no temp file surviving either outcome. 0% to 64%; the remainder is `Chmod`/
`Write`/`Sync`/`Close`/`Rename` failure branches that need injection to reach,
and a 76-line file does not earn a seam for them.

One of those tests is worth more than it looks and is commented on itself:
writing into an **unwritable directory**. Creating the temp file needs write
permission on the directory, where overwriting an existing file does not — so a
plain `os.WriteFile` implementation *succeeds* there and destroys the original,
and only the temp-file-plus-rename fails and leaves it intact. Confirmed by A/B,
replacing `WriteAtomic`'s body with `os.WriteFile`: that test is the single one
of the seven that fails. The others pass under both implementations, so they
pin contract rather than mechanism, which is the right split but worth knowing
before trusting them to catch a regression in the atomicity itself.

## WriteAtomic stops re-widening the files it replaces (2026-08-14)

Follow-on from the review entry above, and the reason the temp-file dance needs
a rule that `os.WriteFile` never did: it creates a **new inode**, so the mode
comes from whatever the code says rather than from the file already on disk.
Every caller passes a constant — 0600 for `config.json` and `gossms.key`, 0644
for a saved script — so a `.sql` the user had chmodded 0600 silently came back
0644 on the next Ctrl+S. That is a behaviour change `fileutil` introduced when
it replaced the plain writes, and nobody would look for it in a durability
helper.

**The fix is `modeFor`, and the interesting part is that it is not "preserve the
existing mode".** Preserving it outright trades the bug for a worse one: a
`config.json` or `gossms.key` that reached 0644 — a legacy write, a stray
chmod, a restore from a backup taken elsewhere — would then keep that mode
forever, because nothing else ever narrows it. So the existing mode is kept
**with `perm` as a ceiling** (`fi.Mode().Perm() & perm`): the caller's constant
is the widest the file is ever allowed to be, and anything narrower survives.
A non-existent or unreadable path just gets `perm`.

The accepted cost, written on `modeFor` so it isn't rediscovered as a bug: a
script at 0664 in a group-writable tree comes back 0644, losing group write.
Erring toward the tighter mode is the right direction for files that sit next
to credentials.

**Two tests, and each catches exactly one of the two ways to get this wrong** —
confirmed by A/B, running the suite against both broken implementations:

- `TestWriteAtomicKeepsAnExistingFilesNarrowerMode` is the only failure when
  `modeFor` returns `perm` verbatim (the shipped bug).
- `TestWriteAtomicTightensAnExistingFileWiderThanPerm` is the only failure when
  it returns `fi.Mode().Perm()` with no ceiling (the naive fix).

Both `chmod` explicitly after `os.WriteFile` rather than trusting its perm
argument, which the umask filters.

Still open, and deliberately not bundled in: `WriteAtomic` replaces a
**symlinked** path with a regular file instead of writing through it. Resolving
it moves both the temp directory and the rename target, so it is its own change
— see `docs/open-threads.md`.

## The two loaders that latched a placeholder under plain safego (2026-08-14)

Third item from the 2026-08-14 review. `CLAUDE.md` § Mouse, overlays, and async
UI already says a background operation that latches UI state before it starts
must use `App.safegoRepair`; two loaders predated the rule and were never
brought across.

**Object Explorer expand.** `handleExpand` sets the node expanded with
`data.Loaded` still false, which is what makes the tree draw a "Loading..."
child, and `SetChildren` in the posted callback is the only thing that clears
it. A panic in any of the ~40 `childLoaders` unwinds past that callback, and
the node spins forever. `childFetchPanicked` now ends the load and installs the
same `errExplorerNode` an ordinary loader failure produces.

**The trade that comes with it, written on the repair.** `SetChildren` marks the
node `Loaded`, so a panicked expand now needs **Refresh** to retry — before,
`Loaded` stayed false and collapsing plus re-expanding refetched. That is a real
capability given up. It is the right side of the trade because it is exactly
what an ordinary loader error already does, and because the old behaviour
recovered only if the user happened to try a gesture nothing told them to try.

**Object Explorer Details.** Same shape, five entry points: the single-shot
`default` arm of `fetch` plus the four progressive loaders, each of which
latches either the "Loading..." status or its own first-stage placeholder rows
(`loadServerDetails` literally writes "Loading..." into two cells). One shared
`DetailBrowser.panicRepair(node, seq)` closure now serves all five. It keeps
both of `postFinal`'s guards for the same reasons and **caches nothing** — a
panic says nothing about what the node's details are, so the pending entry is
dropped and the next selection retries, where an ordinary error is cached
because the server actually answered.

Three `safego` calls in `app_explorer_data.go` were checked and deliberately
left alone: `refreshAgentRootLabel` (a failed check leaves the label as it was),
`scriptObject` and `toggleDatabaseOffline` (status messages only). Nothing to
release.

**Tests** — `async_latch_panic_test.go`. The expand test is end-to-end: it
swaps a panicking loader into `childLoaders`, calls `loadChildren`, and waits
for the node to release. A/B'd against plain `safego`, it doesn't merely fail,
it **times out** — `timed out waiting for the panicking expand to release the
node` — which is the bug stated exactly. The other three cover the guards:
a stale panic must not overwrite a newer expand's children, must not drop a
newer fetch's pending entry, and must not paint an error over a node the user
has since moved off.

## allDialogs completeness is now pinned (2026-08-14)

Fourth item. `buildUI`'s `allDialogs` list is maintained by hand and is the
sole input to `syncDialogStack`, so a dialog missing from it is never drawn,
never offered input and never relayouted — with nothing anywhere raising a
word about it. It was complete (30 fields, 30 entries; the review's "28" was a
miscount of the literal, not a finding).

`dialog_registration_test.go` reflects over `App`'s fields, takes the ones whose
type implements `Dialog`, and requires each to appear in `allDialogs`. Fields
are unexported, so identity is compared with `reflect.Value.Pointer()` —
`Interface()` panics on an unexported field. It also rejects duplicate and nil
entries, and asserts the field count matches the list length, which is what
catches a list naming something that is not an `App` field.

Two guards worth keeping: the `found == 0` check, so the test fails loudly
rather than passing vacuously if the fields ever move behind a struct; and the
A/B — dropping `filterDialog` from the list makes it report
`App.filterDialog (*tui.FilterDialog) is missing from buildUI's allDialogs
list`, naming the field rather than just a count mismatch.

## The user-mapping pages, the peer database, symlinks — and what live testing corrected (2026-08-14)

Last of the 2026-08-14 review items, plus the two open questions it left.

**Finding 6 was the wrong shape, and the right one was already in the repo.**
The proposal was to merge `pageLoginUserMapping` and
`buildNewLoginUserMappingPage` behind one builder. Costed against the actual
code, that needs six injection points over two different row structs
(`mapEdit` carries `orig*` and a real user name; `nloginMapRow` carries neither
and adds `schemaNames`), two different schema widgets (a free-text box vs a
picker), two different dirty rules and two unrelated applies. The shared part
would be wiring, and each page would keep most of its body. **Merge rejected.**

What the two pages genuinely shared was the part that had the bug — the
commit/sync/redraw wiring — and `wireGridEditor` (ag_props.go) already exists
for exactly that, built during the 2026-08-14 grid-cursor sweep and used by all
five AG sites. Neither user-mapping page used it. Both do now. Its `reload()`
is redraw-plus-sync **without a commit**, which is precisely the Revert
semantics that had to be hand-rolled two entries ago; that hand-rolling
(`row := selected; selected = -1; syncFromSelection(row)`) is gone from both.
The bug is now structurally unavailable rather than fixed twice.

**A third bug fell out of the conversion.** `pageLoginUserMapping`'s `rowsFor`
built the Schema column from `e.origSchema` — the loaded value, never the
edited one. Combined with the missing redraw, a schema change was invisible in
the grid until Apply. Both halves are fixed: `e.schema`, and `wireGridEditor`'s
redraw after each commit.

**Live A/B on win10cli, and this one is user-visible.** A throwaway login
(`t6_rev_login`) mapped into `HealthClinic` and `backup_test`. Edit the schema
on the HealthClinic row, move off it, read the grid:

- pre-fix binary: `HealthClinic | t6_rev_login | dbo` — the edit is committed to
  the model and will Apply, but the grid never shows it.
- post-fix binary: `HealthClinic | t6_rev_login | dbot6_alt`, column rewidened,
  and the detail pane below on `master` — proving the redraw preserved the
  cursor rather than snapping it to row 0.

Escaped rather than applied; `sys.database_principals` re-read afterwards
showed `dbo` unchanged in both databases, and every throwaway object was
dropped and verified gone.

**The correction that matters: `Form.Revert()` has no non-test caller.** Found
while setting up the Revert half of that live test — there is no way to trigger
it. The only `.Revert()` outside a `_test.go` in the whole module is
`form.go:480`, inside `Form.Revert` itself, and F5/Refresh goes to
`ConfirmDiscard` plus `startLoad` (a server reload), not here. So the Revert
defects fixed in the two entries above were **real defects in unreachable
code**, not the user-visible bug the review claimed. The fixes and their tests
stand; the severity claim did not. Recorded as an open decision — expose Revert
or retire it — in `docs/open-threads.md`.

**`Peer` no longer carries the connection's database.** The open question was
answered empirically before touching anything: a small probe against win10cli
connecting with `Database:` set to `""`, `master`, `HealthClinic` and a
nonexistent name showed the last failing the **connect**, at ping time —
`Cannot open database "nonexistent_db_xyz" that was requested by the login`.
That is exactly what a peer hits when the database the user connected through
is one a secondary cannot open. `peerOptions` now drops it; everything `Peer`
reaches is server-scoped anyway. Extracted as its own method purely to give the
decision a unit-testable seam, since `Peer` itself needs a second instance.

**`WriteAtomic` now writes through a symlink.** The other half of the mode fix.
A rename replaces a directory entry, so a symlinked script was being turned
into a regular file with the real file left stale — a write-in-place follows a
link for free, a rename has to be made to. `resolveSymlink` uses
`EvalSymlinks` with a fallback for a path that does not resolve (a new file, a
dangling link), and both cases are tested. A/B: removing the call fails the
symlink test with `the symlink was replaced by a regular file`.

**…and through a dangling one (2026-08-15).** The fallback above was "use the
path as given", which quietly left the original bug in place for a link whose
target does not exist yet: `EvalSymlinks` fails on it, so the rename landed on
the link and the target was never created. The test that was supposed to cover
it read the contents back *through the link*, which passes either way — the
kind of assertion that pins nothing. Proven with a scratch test first: `link
was replaced by a regular file` / `target not created`. `resolveSymlink` now
walks the last component by hand with `Lstat`/`Readlink` when `EvalSymlinks`
fails, resolving a relative target against the link's own directory and giving
up after 16 hops so a link cycle returns instead of spinning. Three tests
replace the weak one: the link survives and the target appears, a chain of two
dangling links (one relative) is followed, and a cycle returns inside five
seconds.

## Ctrl+Z reverts a Properties page (2026-08-15)

The answer to the open decision from 2026-08-14: `Form.Revert` had no
non-test caller, and the choice was to expose it or retire it along with the
21 `RevertFn` closures. **Exposed** — author's call. Retiring would have
deleted correct, tested code from `propsheet`'s published row interface with
nothing to show for it, and the whole cost of exposing it is one key.

`PropertySheet.RevertPage(page)` restores the loaded values without a round
trip and reports whether anything changed; `Ctrl+Z` calls it and puts either
`Reverted to the loaded values.` or `Nothing to revert — no unsaved changes on
this page.` on the message line. The message is not decoration: reverting a
page whose values were retyped identically looks exactly like a dead key
otherwise.

**Why `Ctrl+Z`, and why beside `F5`.** `Ctrl+R` was the obvious "reset" key and
is already taken app-wide (Refresh IntelliSense Cache), and a fifth button on a
row that is already `OK/Cancel/Apply/Script Changes` would have cost width on
exactly the terminals where dialog content is already clipped (§ Dialog content
is not clipped to a clamped rect). `Ctrl+Z` is free *inside* a sheet —
`widgets.InputField` takes `Ctrl+A`/`Ctrl+U`, and no propsheet row hosts a
`controls.Editor`, the one widget with its own `Ctrl+Z`. It is handled at the
top of `HandleKey` next to `F5`, before the zone switch, so it works from the
page list and the button row too.

**Live, against win10cli.** Server Properties > Memory: edited two rows
(min server memory 16 → 16999, max 2147483647 → 21474836), one `Ctrl+Z`
restored both and printed the message; a second `Ctrl+Z` printed the
nothing-to-revert one. Then Server Properties > Permissions, the grid case
that `RevertFn` exists for: toggled `ADMINISTER BULK OPERATIONS` to `Grant`,
`Ctrl+Z` put it back to `(none)`, and the next `Down` still moved the
selection — `redrawGrid` kept the cell cursor, so the revert did not leave the
grid in the "every row but the first is unselectable" state. Escape closed the
dialog; nothing was applied.

Also added a **Properties Dialogs** section to Help (F1) and a README row.
The first draft of the help line clipped at the dialog's width — caught in the
capture, not in a test.

## Dialog content is clipped to a clamped rect (2026-08-15)

The last of the small-terminal thread. `recentre` has clamped the dialog
*rect* to the screen since 2026-08-14, but content is drawn at fixed offsets
from the dialog origin for the size the dialog *asked* for, so at 30x8 the
Connect dialog's field rows ran to the screen edge, the bottom border was
overwritten by `Database:`, and `DrawButtons` landed in the middle of the
Port row — captured as `│ Por[ Connect ]3 [ Cancel ] │`.

The open thread costed this as "~28 hand-written Draw methods". It isn't:
one clipping screen and three changes to `ModalDialog` cover every dialog in
the app, present and future.

**`core.ClipScreen`** wraps a `tcell.Screen` and drops cell writes outside a
clip rect. `App.drawDialogs` wraps the screen once and resets the clip before
each dialog's `Draw`, so a nested dialog never inherits its parent's clip.
Every write path is covered, not just `SetContent`: `DimArea` walks a row
with `Get`/`Put`, so `Put` is clipped too — and it has to keep *reporting*
the width it skipped, or that walk never leaves the column. `Fill`/`Clear`
cover the clip rather than the screen.

**`DrawBase` installs the clip** after the dim and the border, both of which
draw outside it legitimately, and **only when the rect is clamped**
(`d.clamped()`, `rect.W < reqW || rect.H < reqH`). The gate is the point: a
dropdown or completion overlay opened inside a dialog may extend past the
box, and clipping those would be the regression. A/B'd — at 120x34 the
Connect and Help dialogs capture byte-identically before and after.

**`DrawButtons` clears from `ButtonRowY()` to the bottom inner row** when
clamped. Buttons are right-aligned, so drawing them over a content row left
the row's left half showing through; a dialog with a button row draws nothing
of its own from that row down.

**The clip is `InnerRect`, and that took a second pass.** The first version
left the right border column writable, because the scrollbar idiom every
dialog uses puts the bar *on* the border (`core.DrawScrollbar` at
`Rect().Right()-1`, which is what `ScrollbarDrag` hit-tests). That kept the
bars but let content overwrite the border, which still read as broken. So the
clip is now the interior, and the five dialog-level bars (Help, Tasks, Query
List, Key Diagnostics, PropertiesDialog) go through the new
`ModalDialog.DrawContentScrollbar`, which widens the clip to the whole box
for the draw and owns the two theme styles all five had copied. **A new
dialog-level scrollbar must use it** — `core.DrawScrollbar` at `Right()-1`
now draws into the clip and vanishes on a clamped rect. A scrollbar inside a
child widget is unaffected; those sit on the widget's own rect, inside the
dialog.

Verified live under tmux at 30x8, 40x12 and 44x12, and across a 120x34 →
44x12 resize: box intact, content confined, button row clean, Help's
scrollbar still drawn at 40x12.

## The folder filter reaches Object Explorer Details, and survives a reconnect (2026-08-15)

Two of the four gaps `docs/open-threads.md` recorded against the folder
filter, closed together.

**The Detail Browser ignored the filter.** The tree drew `Tables (filtered)`
over two rows while the pane beside it listed all eight — `fetchChildren`
filters the loader's `[]*explorerNode`, but `detail_browser_*.go`'s loaders
query gosmo directly and hold `[]*gosmo.Table`, so there was nothing for
`filterChildren` to take. The generic view is the exception:
`fetchChildObjectsDetail` reuses the folder's own `childLoaders` entry and so
holds nodes, and it now calls `filterChildren` — which covers every folder
whose detail view is the plain child list (Users, Roles, Schemas, Sequences,
Synonyms, Triggers, Functions and the System * folders). The five
purpose-built loaders go through **`filterObjects`**, a generic that takes a
function mapping one gosmo object to the `nodeData` fields the criteria read:
Tables, Databases, Logins, and `fetchNodeDetails`'s Views, Stored Procedures
and System Databases cases.

**Filtering the collection, not the rows, is what keeps the progressive
loaders correct.** `loadTablesFolderDetails` and
`loadDatabasesFolderDetails` post placeholder rows first and then backfill by
index from a second query, so a filter applied to `rows` after the fact would
leave the backfill writing row counts and sizes into the wrong rows. Both
filter the gosmo slice before any row is built.

**A filter no longer dies with the connection.** It lives on the tree node,
and a disconnect drops the subtree, so reconnecting came back unfiltered —
SSMS keeps a filter for the session across a reconnect. `App.savedFilters` is
keyed by **`filterKey`** — connection (`sysCompletionInventoryKey`, i.e.
server+port+login), database, schema, table, node type — rather than by node
pointer. Schema and table are in the key because a table's own Triggers
folder and its database's are the same NodeType in the same database.
`applyNodeFilter` writes it, `fetchChildren` restores it onto freshly loaded
children. **The restore has to happen in `fetchChildren`, not after
`SetChildren`**: a folder's filter must be in place before that folder's own
children are fetched, or the node comes back labelled `(filtered)` over a
list nothing filtered. That puts the map on the loading goroutine, hence
`App.filterMu`. Nothing is written to disk, deliberately.

Verified live on win10cli, and A/B'd against a pre-fix binary: with
`Name contains "med"` on HealthClinic's Tables folder, the old binary showed
the tree at two tables and the details pane at all eight; the new one shows
two in both, with row counts and sizes still landing on the right rows. The
Databases folder's filter survived a real Disconnect and reconnect —
`Databases (filtered)`, one row in the tree, one in the pane.

---

## Cross-repo review: gosmo write-statement coverage, and Ctrl+Z on the New-X grids (2026-08-18)

A review pass over both repos. The health baseline came back clean —
`gofmt`, `go vet`, `staticcheck` and `go test -race ./...` all silent in both,
every one of gosmo's ~60 `'%s'` SQL sites correctly escaped, 336 `X`/`XContext`
pairs with no DB-op method missing its variant, no `SetData` inside an
`OnSelectRow`, no bare `postEvent`+`wakeEventLoop`, no dialog-level
`core.DrawScrollbar` outside the clip. Two things came out of it.

**gosmo: 84 of 183 write methods had no test at all.** Not thin coverage —
none, offline or live, so the T-SQL each one generates was never asserted
anywhere. `WithScript` plus a zero-value receiver is a serverless harness for
exactly this (`&Database{server: &Server{}, name: …}` reaches the exec
chokepoint before any `*sql.DB`), which is what made the gap cheap to close:
`script_write_common_test.go` plus five family files, 107 cases, and the count
is now 4 — `BackupHeaders`, `BackupHistory`, `BackupFileList` and
`BackupFileListForSet`, which are reads and need a server. Offline coverage
went 30.4% → 36.2%.

Every case asserts the **whole** statement and feeds a quote-hostile value
through each parameter that reaches the text: `o'brien` for the literals,
`a]b` for the brackets, `Sales.Archive` for a name that resolves as two parts
unbracketed. A substring assert passes straight over both failure modes, which
is the point of the file-level comment saying so.

The one defect it turned up: **`CreateUserContext` with an empty login emitted
`CREATE USER [x] FOR LOGIN []`** — `quoteIdent("")` is `[]` — which the server
rejects with a message naming an empty login the caller never typed. A user
with no login is `CREATE USER ... WITHOUT LOGIN`, a different statement, so it
now refuses rather than guessing which was meant, matching the guard already on
the user name. gossms's only caller always passes a real login, so nothing
there changes.

**gossms: Ctrl+Z lied on four grids.** `GridRow.Revert()` is a no-op without a
`RevertFn`, and four pages set `DirtyFn` without one — the New Availability
Group dialog's databases, replicas and backup-priority grids, and the New
Database Mirroring Endpoint dialog's instances grid. Every other grid page in
the app has both.

Reproduced live on ubusql1 with two throwaway FULL-recovery databases:
tick `zz_revert_a`, move off the row so the checkbox commits, `Ctrl+Z`. The
group name cleared, the sheet said *Reverted to the loaded values*, and the
grid still read `zz_revert_a | True` — OK from there would have created the
group with a database the user had just told the app to forget. A/B'd against
the fixed binary, where the same sequence puts the row back to `False`.

Each page now snapshots its own state at build time and restores it. The
shapes differ and that is deliberate: the databases list is a value slice
(`slices.Clone`); the replicas are pointers, so `cloneAGReplicas` copies each
one — a shared snapshot would be edited by the changes it exists to undo; the
Backup Preferences page keys its baseline **by replica name** rather than
snapshotting the slice, because the replica list belongs to the General page
and a replica added there after the backup page was built has to survive a
revert on it. `new_object_revert_test.go` pins all four, and each of its three
assertions fails against the unfixed closures.

**The dismissing click on an open overlay passes through, and that is the
convention.** Verified under tmux: the click that closes the panel-tab combo
also moves the query editor's caret. `widgets.DropDown` does the same on its
own outside-click branch. Both now say so, since a "fix" to either alone would
split them.
