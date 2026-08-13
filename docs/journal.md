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
