[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Resource Governor

Under **Management**: resource pools with their workload groups nested beneath, and
external resource pools. Pools and groups show live counters (active and queued
requests, CPU used) beside their limits, and the node's label says whether the governor
is disabled or has a reconfiguration pending.

![resource governor](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/16_resource_governor.png)

## What is browsable

- **Resource Governor** — the governor itself: its state and the live counters.
- **Resource Pools** → each pool → **Workload Groups** under it.
- **External Resource Pools** — the pools an external (PolyBase / SQL Server Big Data
  / Azure) request runs in, on the editions that have them.

Rows the edition does not support, and objects the login cannot see, each get their own
row naming the reason rather than an empty folder.

The right it takes is asymmetric, and the pages say so: **`VIEW ANY DEFINITION`** to
read the configuration at all, **`CONTROL SERVER`** to change it. Read it and you get
the pages; not quite, and what you get is the sentence telling you which is missing.

## Resource Governor Properties

**Configuration**

| | |
|---|---|
| **Enabled** | The governor's state. Clear it and Apply ends with `ALTER RESOURCE GOVERNOR DISABLE` — the stored changes stay stored, and a disabled governor is kept disabled rather than being silently reconfigured into being enabled. |
| **Classifier function** | The function that decides which group a request lands in. The list holds the schema-bound, parameterless functions in `master` that return `sysname`, with **New** to start one from a template. |
| **Max outstanding I/O per volume** | The I/O resource cap; **0 lets the server choose**. |

**In force** — what `sys.resource_governor_configuration` says is actually running,
beside what has been written but not yet put in force. This is the pair the tree's
"(reconfiguration pending)" label comes from.

**OK and Apply end with `ALTER RESOURCE GOVERNOR RECONFIGURE`**, which is what puts
every stored change in force. That single trailing statement is why the dialog is worth
using instead of typing the `CREATE RESOURCE POOL`s by hand: a stored-but-not-in-force
configuration is one of the easy ways to lose an afternoon.

**Affinity** — the pool's NUMA-node and CPU affinity as the server holds it. It is
displayed, not edited: affinity is set with `ALTER RESOURCE GOVERNOR
… (AFFINITY …)`, and a page that listed editable CPU masks alongside it would be an
invitation to write something the server refuses.

## Pools and workload groups

Each pool page lists **its workload groups** beside the pool's own limits, with the live
counters — active requests, queued requests, CPU used — so a limit that is saturated is
visible as a limit that is saturated rather than a number out of context.
`internal` accepts no changes, and `internal` and `default` cannot be removed; a group
added on the page is added to the pool listed above it and is offered for selection at
once.

Each workload group page edits what `CREATE WORKLOAD GROUP`/`ALTER WORKLOAD GROUP`
takes, and writes only what changed:

| | |
|---|---|
| **Importance** | The group's request priority class |
| **Max memory grant %** | Request max memory grant percentage |
| **Max CPU time per request** | The per-request CPU cap, in the units the server uses |
| **Memory grant timeout** | How long a request waits for a grant before failing |
| **Maximum DOP** | The group's degree-of-parallelism cap |
| **Maximum requests** | The group's request ceiling |
| **Max outstanding I/O per volume** | The group's I/O cap; 0 lets the server choose |
| **Tempdb data limit %** / **(MB)** | tempdb space per request; blank means no limit |
| **Uses external pool** / **Uses resource pool** | For an external resource pool: which pool an external request uses |

**0 means unlimited, or the server's own value**, for CPU time, timeout, DOP and
requests, and the pages say which of the two they mean rather than leaving `0` to be
interpreted.

**Add or remove** creates a group (named, in the pool you were looking at) or marks one
for removal — pending until Apply, and scriptable instead of run like everything else
in a properties dialog.

## The context menu

**Enable**, **Disable**, **Reconfigure**, **Reset Statistics**, **Delete** and **Script
as** are on the tree items, each gated on the right it needs and each offering the
statement before it runs. **Reconfigure** is the one to reach for when something else
changed the configuration — a script, another session — and the tree's
"(reconfiguration pending)" label is the tell that something is stored and not running.

## See also

- [Activity Monitor](https://github.com/radix29/gossms/wiki/Activity-Monitor) — the Azure Instance tab shows the governor's fixed limits beside the instance's own resource history
- [Server and database properties](https://github.com/radix29/gossms/wiki/Server-and-Database-Properties) — the configured-versus-running pattern again
- [Scripting](https://github.com/radix29/gossms/wiki/Scripting)

[← Home](https://github.com/radix29/gossms/wiki)
