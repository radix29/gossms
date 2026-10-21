[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Always On

The **Always On High Availability** branch at the instance root: availability groups,
each group's replicas, its availability databases, and its listeners — plus the New
Availability Group dialog, the dashboard SSMS calls *Show Dashboard*, and failover.

Where Always On is not enabled, the branch says **"Always On is not enabled on this
instance"** as a node rather than failing the expansion: that is normal for most
instances, and SSMS shows the folder regardless.

![always on availability groups](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/12_alway_on_AG.png)

## Reading it: why everything goes to the primary

Only `sys.availability_groups` and `sys.availability_replicas` are cluster-wide. The
`sys.dm_hadr_*` views describe **what the connected instance can see**, so a secondary
reports empty roles, empty health and no per-database queue detail for any replica but
itself.

Every Always On loader therefore resolves the group's primary and re-reads through a
peer connection built from the saved connection info (falling back to this connection's
credentials). Two consequences you can see:

- Expand from a secondary and you still get the real health and the real queues.
- When the primary is unreachable, the expansion still succeeds with the **local partial
  view**, and the labels say which view you are looking at rather than letting a blank
  column read as a healthy replica. Properties and the dashboard go further and refuse:
  a secondary-loaded page would offer edits the server rejects.

## The dashboard

**Show Dashboard** on the Always On root lists **every group and its replicas**; on one
group it lists that group: the health rollup, each replica's role and connection state,
and per-database queues with the **estimated data loss** and **estimated recovery time**.

`msdb`'s health model is deliberately not reused for the rollup — its rollup column can
answer *Unknown* for a group that has never been evaluated, and a tool that reported
Unknown as green is the worst possible failure mode in a HA dashboard.

## Availability Group Properties

Three pages, every one applied on the primary (`ALTER AVAILABILITY GROUP` is rejected on
a secondary), and every one gated on `ALTER ANY AVAILABILITY GROUP` with the group named,
because `DENY ALTER` on the group refuses it specifically:

- **General** — the group's own settings and each replica's: availability mode,
  failover mode and availability replica failover, seeding mode and seeding port, the
  backup priority, and the connection ports.
- **Backup Preferences** — where automated backups run (**Prefer Secondary**, **Secondary
  only**, **Primary**, **Any Replica**) and each replica's backup priority.
- **Read-Only Routing** — the routing **URL** (a secondary-role property: *reach me here
  when I'm a readable secondary*) and the routing **list** (a primary-role property:
  *when I'm primary, send read-intent connections to these, in this order*). **Routing
  does nothing until both are set**, and the page says so; the list is edited in the
  `ALTER AVAILABILITY GROUP` order-and-parentheses form, so a half-typed list is a row
  validation error rather than a statement that runs.

Membership changes are deliberately not on the property sheet: adding or removing
databases, replicas and listeners is cluster-wide, has its own confirmation, and has
seeding concerns — so they live on the Object Explorer menu.

## Creating and changing a group

| | |
|---|---|
| **New Availability Group…** | The wizard's pages as one dialog; creates the group on the primary |
| **Add Replica…** | The only way to add a replica to an existing group |
| **Add Database…** | Bring a database into the group (SSMS's Add Database to Availability Group) |
| **Add Listener…** | SSMS's New Availability Group Listener dialog |
| **New Database Mirroring Endpoint…** | The endpoint the data transport needs |
| **Remove Listener… / Remove Replica from Group… / Delete Availability Group…** | Each with a confirmation naming what it will do on which instance |

**A group allows exactly one listener** — the dialog checks first and names the existing
one rather than letting SQL Server answer error 19477.

Two facts that turn "it ran without error" into "it did nothing", both stated in the
dialogs that need them:

- **`ADD REPLICA` runs on the primary and leaves the replica disconnected**: each
  secondary must then run `ALTER AVAILABILITY GROUP … JOIN` against itself (the primary
  cannot join on its behalf), and goSSMS emits that pair per secondary.
- **Automatic seeding needs `GRANT CREATE ANY DATABASE` on the replica.** Without it,
  `SEEDING_MODE = AUTOMATIC` seeds nothing **and reports no error**.

Peer connections reuse this connection's credentials; a replica that needs its own is out
of scope, and the dialog says so rather than silently connecting as the wrong login.

## Databases, replicas and failover

- **Suspend Data Movement… / Resume Data Movement** act on the copy of the instance they
  run on: from a secondary that replica, from the primary every secondary — using the
  tree's own connection, and **the confirmation names which** rather than implying a
  group-wide switch.
- **Remove Database from Group…** and the mirroring-flavoured removal are distinguished
  in their confirmations, because one leaves the database `RESTORING` and the other does
  not.
- **Fail Over to This Replica…** (planned) and **Force Failover to This Replica…**
  (possible data loss, said plainly) are offered on a **secondary's** leaf and run **on
  the replica being promoted**, through a connection to it. Their confirmation is
  **uninterruptible**: an abandoned failover can leave the group `RESOLVING` with no
  primary at all.
- **No cluster manager is invoked, and the group's cluster type decides what failover
  even means.** `CLUSTER_TYPE = EXTERNAL` (distributed availability groups) belongs to
  the external cluster manager: goSSMS refuses and names the tool — on Linux, Pacemaker's
  `crm resource move` / `pcs resource move`, since SQL Server rejects
  `ALTER AVAILABILITY GROUP … FAILOVER` there with error 47104. `CLUSTER_TYPE = NONE`
  (read-scale) has no arbiter, so **planned failover is unsupported** (error 47122) and
  only forced failover is offered, with the transaction loss named.

## Listeners

**Listener Properties** is the only place to change a listener after creation, and the
limits are SQL Server's: you can **change the port** or **add an address**, one per
statement, and **no form removes an address or renames the listener**. Added addresses are
therefore permanent — fixing a typo means recreating the listener — and the page says so
instead of offering a Remove that would work only on rows that were never written.

## See also

- [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer)
- [Backup and restore](https://github.com/radix29/gossms/wiki/Backup-and-Restore) — where backups run when a secondary is preferred
- [Server and database properties](https://github.com/radix29/gossms/wiki/Server-and-Database-Properties)

[← Home](https://github.com/radix29/gossms/wiki)
