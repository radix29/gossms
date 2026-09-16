# Plan — Phase 3, item 14: Service Broker (whole subtree)

Scope: the seven object families SSMS files under a database's **Service
Broker** folder — Message Types, Contracts, Queues, Services, Routes, Remote
Service Bindings, Broker Priorities. Estimate **3–4 weeks**, most of it in
gosmo: there is no Service Broker object model there today. `grep -i broker`
over gosmo finds only `Endpoint.ServiceBrokerDetail` (the `SERVICE_BROKER`
endpoint payload, a *server*-level object that is not part of this subtree)
and `DatabaseOptions.IsBrokerEnabled`. gossms has no `NodeType` for any of
it — `docs/open-threads.md` § Permission gating records that absence and is
one of the documents this work updates.

Sibling item 15 (certificates / symmetric / asymmetric keys, database audit
specs) is out of scope; re-scope it before starting it, since audit
specifications and the Always Encrypted keys already ship.

## The target tree

New nodes marked `+`; everything else already exists.

```
<database>
    Programmability
    Query Store
+   Service Broker
+     Message Types
+     Contracts
+     Queues
+     Services
+     Routes
+     Remote Service Bindings
+     Broker Priorities
    Security
    Storage
```

Placement: between Query Store and Security in `loadDatabaseChildren`
(`explorer_databases.go`, the `[]*explorerNode` at ~line 175). That is SSMS's
relative position. Do **not** reorder gossms's existing Security/Storage pair
to match SSMS while here — it is a separate decision and it would churn
`TestNewFamiliesSitWhereSSMSPutsThem`.

Two presence decisions:

- **The folder does not gate on `is_broker_enabled`.** Every one of these
  objects can be created, listed and dropped while the broker is disabled;
  disabling it stops message delivery, not DDL. SSMS shows the folder
  unconditionally, and Database Properties ▸ Options already reports the flag.
- **It does gate on the engine edition — but on Managed Instance the gate is
  one statement wide, not the folder.** Probed live on `t-qmi-01`
  (EngineEdition 8, `ProductVersion` 12.0.2000.8) on 2026-09-16; all probe
  objects created and dropped, nothing left behind. Results:

  | Probe | MI answer |
  |---|---|
  | All seven catalog views + `sys.dm_broker_queue_monitors` | read fine, in `master` and in a user database |
  | `CREATE` message type, contract, queue, service, route, broker priority | all succeed |
  | `ALTER` queue / route / message type / service / broker priority | all succeed |
  | `CREATE REMOTE SERVICE BINDING` | **refused, Msg 41906**, "not supported in SQL Database Managed Instance" |
  | `ALTER` / `DROP REMOTE SERVICE BINDING` | **not** refused — they parse and run; a missing object fails 15151 as anywhere |
  | `sys.remote_service_bindings` | readable, 0 rows |

  So on MI the whole tree ships, Remote Service Bindings included, and
  `edition_gate.go` needs **no folder-level mechanism** — one entry in its
  live-refusal table for `CREATE REMOTE SERVICE BINDING`, beside the six
  `sp_detach_db`-class refusals. The only verb it touches in this pass is
  Script as ▸ CREATE for that one family, since Stage E ships no New-X dialogs.

  **41906 is a compile-time refusal, not a runtime error.** It aborted the
  entire batch before any statement in it ran — the other statements sharing
  that batch never executed. A `CREATE REMOTE SERVICE BINDING` must therefore
  never be emitted in a batch alongside statements expected to survive, and
  the gate has to withhold it rather than let the server answer.

  **Azure SQL Database proper is still unprobed** — there is no SQL Database
  instance in the test estate, only the MI. That half of the question is what
  decides whether a folder-level mechanism is needed at all; see § Open
  questions.

  Incidental confirmation from the same run: `is_broker_enabled` is 0 on MI's
  `master` and 1 on a user database, so the flag is a live disabled-broker
  test case on MI and, as decided above, not a gate.

## Scope of write support, decided up front

Service Broker is **not** another read-only Phase 3 family, and the reason
matters: six of the seven have a real `ALTER`, so the argument that withheld
creation from Rules, Defaults, Types and Assemblies (no ALTER ⇒ an editor is a
drop-and-recreate) does not apply here.

| Family | CREATE | ALTER | This pass |
|---|---|---|---|
| Message Types | yes | yes (`VALIDATION`) | read + Script as + Delete |
| Contracts | yes | **no ALTER exists** | read + Script as + Delete |
| Queues | yes | yes (status, retention, activation, poison handling) | **editable Properties** |
| Services | yes | yes (queue, add/drop contracts) | read + Script as + Delete |
| Routes | yes | yes (address, service, lifetime) | **editable Properties** |
| Remote Service Bindings | yes | yes (user, anonymous) | read + Script as + Delete |
| Broker Priorities | yes | yes (priority level) | read + Script as + Delete |

Recommendation, to be confirmed before Stage C: ship reads, Script as
(CREATE + DROP) and Delete for all seven, and editable Properties for **Queues
and Routes only** — the two whose settings change in operation rather than at
design time (a stuck activation procedure, a queue taken out of service, a
route repointed at a new address). No New-X dialogs in this pass: a service or
a contract is authored with its application, not clicked together, and every
one of them needs the others to exist first. Record the deferral in
`docs/open-threads.md` § Deferred scope with that argument, so "why can't I
create a contract?" has a standing answer — and record that it is a deferral,
not the Rules/Defaults refusal, because these do have an ALTER.

## Stage A — gosmo reads (≈7 days, the critical path)

One domain file, `service_broker.go`, with the list + `…ByNameContext` finder
pair + `Drop…Context` each family in gosmo already has, all hanging off
`*Database`. Split into `service_broker.go` (message types, contracts,
services, queues) and `service_broker_routing.go` (routes, remote service
bindings, broker priorities) if the first passes ~600 lines — by exact line
range, diffed byte-for-byte, per CLAUDE.md.

| Type | Catalog |
|---|---|
| `MessageType` | `sys.service_message_types` (+ `sys.xml_schema_collections` for `VALIDATION = VALID_XML WITH SCHEMA COLLECTION`) |
| `ServiceContract` | `sys.service_contracts`, `sys.service_contract_message_usages` |
| `BrokerService` | `sys.services`, `sys.service_contract_usages`, `sys.service_queues` for the bound queue |
| `BrokerQueue` | `sys.service_queues` (+ `sys.objects` type `'SQ'`, `sys.procedures` for the activation procedure) |
| `Route` | `sys.routes` |
| `RemoteServiceBinding` | `sys.remote_service_bindings` |
| `BrokerPriority` | `sys.conversation_priorities` |

Name the Go type `BrokerService`, not `Service` — gosmo already has
`Server`, and a bare `Service` beside it reads as the Windows service.

Traps, each of which otherwise costs a live round trip:

- **System vs user objects — probed, and the id convention is only two-thirds
  true.** Measured on `win10cli` (major 17) and `win10cli\SQL2016` (major 13)
  on 2026-09-16, across `master`, `msdb`, `HealthClinic` and a fresh database
  with one object of each family; identical on both majors, same ids
  literally. Probe database created and dropped.

  | Family | Discriminator | Evidence |
  |---|---|---|
  | Message Types | id < 65536 **works** | system 1–14 (13 `schemas.microsoft.com/…` + `DEFAULT`), first user object 65536 |
  | Contracts | id < 65536 **works** | system 1–6 (5 + `DEFAULT`), first user 65536 |
  | Services | id < 65536 **works** | system 1–3 (`QueryNotificationService`, `EventNotificationService`, `ServiceBroker`), first user 65536 |
  | Broker Priorities | id < 65536 vacuous | no system members at all; first user 65536 |
  | Remote Service Bindings | id < 65536 vacuous | no system members; first user 65536 |
  | **Queues** | **`is_ms_shipped`** | object_ids are ordinary (`1977058079`…), nowhere near 65536 — the id rule is meaningless here |
  | **Routes** | **neither** | `AutoCreatedLocal` is `route_id` **65536** — inside the *user* range — and exists in every database; the first user route is 65537 |

  Three things this changes:

  - **`sys.service_queues` carries `is_ms_shipped` itself.** No join to
    `sys.objects` is needed, contrary to the catalog table above — keep the
    `sys.objects` join only for `create_date`/`modify_date` if the Properties
    page wants them (they are on `sys.service_queues` too).
  - **`AutoCreatedLocal` cannot be detected by id.** It is auto-created in
    every database, it sits at 65536, and `sys.routes` has no system flag of
    any kind. Either special-case the name or — the recommendation — treat
    every route as a user object and mark none, since SSMS lists
    `AutoCreatedLocal` plainly and a user can legitimately drop it.
  - **The two classifications *cannot* be made to agree, and that is msdb, not
    an edge case.** Database Mail's queues (`InternalMailQueue`,
    `ExternalMailQueue`, `syspolicy_event_queue`) are `is_ms_shipped = 1` →
    system, while the very same feature's services (`InternalMailService`
    65542, `ExternalMailService` 65543, `syspolicy_event_listener` 65544),
    message types (`{//www.microsoft.com/databasemail/messages}SendMail`
    65540/65541) and contract (65538) are all in the **user** range → user.
    So expanding msdb's Service Broker folder will show a system queue beside
    a user service of the same feature. Do not "fix" it by name-matching;
    pin it in a test with msdb as the fixture so the asymmetry is recorded as
    intended rather than rediscovered as a bug.

  Decision stands: list and mark "(system)", following gossms's Assemblies
  loader (`TestAssembliesLoaderMarksTheShippedOnesSystem`) and SSMS, with
  Routes marking nothing per the above.
- **Queues are schema-scoped, the other six are not.** `sys.service_queues`
  has a `schema_id`; message types, contracts, services, routes, bindings and
  priorities have an owner (`principal_id`) and no schema at all. This drives
  the whole permission story below, the drag/transfer story, and the
  `nodeData.Schema` a loader fills.
- **A queue's message count must not be read by selecting from the queue.**
  `SELECT COUNT(*) FROM <queue>` needs RECEIVE on the queue and takes locks on
  a live queue. Read it from `sys.internal_tables` joined to
  `sys.dm_db_partition_stats` on the queue's `object_id`, which needs only
  VIEW DATABASE STATE, and treat an unavailable count as blank, not zero.
- **`sys.dm_broker_queue_monitors` is where activation state lives** (`state`,
  `tasks_waiting`, `last_activated_time`) and it has a row only for a queue the
  broker has monitored — no row is the normal case, not an error.
- **Contract message usage is two bits, not one column.**
  `is_sent_by_initiator` / `is_sent_by_target` decode to `SENT BY INITIATOR`,
  `SENT BY TARGET`, `SENT BY ANY` (both set). Getting the both-set case wrong
  produces a contract script that does not parse.
- **Version gates**: nothing in this set is newer than the major-13 floor
  (`is_poison_message_handling_enabled` arrived in 2012, conversation
  priorities in 2008 R2). Say so explicitly in
  `version_gate_inventory_test.go` rather than leaving the family absent from
  it — and remember `serverMajorVersion()` answers 0 on Azure by design.
- **`Database` vs `DatabaseByName`** — every collection method hangs off
  `*Database`, every gossms loader reaches it via `DatabaseByNameContext`.
  `go doc gosmo.Server.Database`.

Scripter: a new `scripter_service_broker.go` beside `scripter_programmability.go`,
with `Script<Family>Context` per family emitting CREATE and DROP. The queue
script is the one with substance — `WITH STATUS`, `RETENTION`,
`ACTIVATION (STATUS/PROCEDURE_NAME/MAX_QUEUE_READERS/EXECUTE AS)`,
`POISON_MESSAGE_HANDLING`, and `ON <filegroup>` from the queue's internal
table. Add each verb to `scripter_verb_test.go` and the family to
`live_scripter_families_test.go`.

Extend `live_versionsweep_test.go` with every new read, labelled, and run it on
`win10cli\SQL2016` (major 13) **before** any gossms code is written against
it; extend `live_tree_families_test.go` the same way. Add
`live_service_broker_test.go`: create a disposable database, create one object
of each family in dependency order (message type → contract → queue → service
→ route → binding → priority), read each back, script each, execute the script
into a second database, drop everything.

Two fixture traps found while probing, both of which cost a round trip:
a remote service binding's user **cannot be mapped to a certificate** (Msg
28083, "cannot own certificates … 1) roles, 2) groups or 3) principals mapped
to certificates or asymmetric keys") — `CREATE USER … WITHOUT LOGIN` is what
works; and `CREATE CERTIFICATE` needs a database master key first (Msg 15581),
which majors 13 and 14 do not have in `master`.

## Stage B — the tree (≈3 days)

1. `tree_node.go` — 14 `NodeType` constants (7 folders + 7 leaves), plus the
   parent `NodeServiceBroker`; `nodeIcon` **and** its ASCII fallback (both
   switches); `nodeTypeName`; each leaf in `hasChildren`'s false list. A
   service is arguably expandable (its contracts) — keep it a leaf, the way a
   plan guide is, and put the contract list on the Properties page.
2. `explorer_loaders.go` — one `childLoaders` entry per expandable type, one
   `nodeMenus` entry per type that needs more than New Query + Refresh.
3. A new `explorer_service_broker.go` for all fifteen loaders — seven folders,
   seven lists, and `loadServiceBrokerChildren` for the parent. Do not grow
   `explorer_programmability.go`.
4. `loadDatabaseChildren` gains the `Service Broker` folder at the position
   above.
5. `explorer_filter.go`'s `filterProps` — one case per new *folder*, or the
   Filter menu item silently offers nothing. Name-only for the six schemaless
   families; name + schema for Queues. Only push a filter down into gosmo
   (`nodeFilter.pushdown`) if the clause reproduces `matchText` exactly,
   escaping included; otherwise refuse it and read the whole folder.
6. `explorer_tree_families_test.go` — the family-level tests
   (`TestNewFamilyFoldersAreWired`, `…LeavesAreLeaves`, `…HaveAnIconInEveryStyle`,
   `…AreNamed`, `…FoldersAreFilterable`) are table-driven and must gain all
   fourteen types; they are the cheapest check that nothing was half-wired.

## Stage C — Detail Browser + Properties (≈5 days)

- **Detail Browser**: a new `detail_browser_service_broker.go`, plus folder and
  leaf cases in `detail_browser.go`'s dispatch — copy the
  `NodePlanGuides` / `programmabilityFolderDetail` shape (one `case` listing
  several node types, delegating to one file). Apply `filterObjects` to the
  collection *before* rows are built, never to rows afterwards. Each leaf
  reuses the same finder its Properties page uses.
  Useful columns: Queues — name, status, retention, activation, readers,
  messages; Routes — name, remote service, address, expires; Services — name,
  queue, contracts; Message Types — name, validation.
- **Properties**, one file per family (`message_type_props.go`,
  `contract_props.go`, `queue_props.go`, `service_props.go`, `route_props.go`,
  `remote_service_binding_props.go`, `broker_priority_props.go`), each
  registered in `prop_dialog.go`'s page slice for its node and each with a
  `*_props_page_test.go` beside it, driven through `fakedb_test.go` with
  `editText`/`editSelect`, rows addressed by name, and asserted with
  `StatementsIn(db)`.
  - A page that cannot write is exempt from `withRequires` only by being named
    in `pagesThatOnlyRead`; `prop_page_requires_test.go` fails otherwise.
  - Queue and Route pages *do* write — see the rights below, and use
    `withRequiresOn` for the queue (its right is object-scoped, on the queue).
  - **Labels must fit `propsheet.LabelWidth` (30 columns)** and clip silently
    if not. Already over at authoring time: "Poison message handling enabled"
    (31), "Remote service binding user" is fine at 27, "Activation execute as
    principal" (33). Shorten them, don't widen the column;
    `TestNoPropertySheetLabelIsTruncated` is the backstop, not the design.

## Stage D — permission gating (≈3 days, do not fold into C)

This is the part with no existing entry to copy, and `docs/db-rules.md`'s rule
applies in full: **a node with no schema must never fall to
`objectWriteRights()`** — six of these seven have no schema, and
`TestSchemalessDatabaseOpsAreGated` will refuse them until each has an explicit
entry.

- New `requiredRight` values in `permission_gate.go`, database-scoped:
  `ALTER ANY MESSAGE TYPE`, `ALTER ANY CONTRACT`, `ALTER ANY SERVICE`,
  `ALTER ANY ROUTE`, `ALTER ANY REMOTE SERVICE BINDING`. Broker priorities have
  no `ALTER ANY` of their own — `CREATE`/`ALTER`/`DROP BROKER PRIORITY` is
  documented as requiring ALTER on the database, so that one is
  `rightAlterDatabase` alone.
- Queues are `sys.objects` (type `'SQ'`) and take the object-scoped set, with
  the **queue** as the securable — the same shape an index's page uses with the
  table.
- **Probed on 2026-09-16** with `WITHOUT LOGIN` users on majors 13, 14 and 17
  (`win10cli\SQL2016`, `win10cli\SQL2017`, `win10cli`), one right per user,
  each DDL run under `EXECUTE AS USER`. **The three majors answered
  identically, line for line.** Probe databases created and dropped on all
  three. What the server actually accepts:

  | Right held (alone) | ALTER the object | DROP the object |
  |---|---|---|
  | `ALTER ANY MESSAGE TYPE` | yes | yes |
  | `ALTER ANY CONTRACT` | (no ALTER exists) | yes |
  | `ALTER ANY SERVICE` | yes | yes |
  | `ALTER ANY ROUTE` | yes | yes |
  | `ALTER ANY REMOTE SERVICE BINDING` | yes | yes |
  | `ALTER` on the database | yes, every family | yes, every family |
  | `ALTER` on the **queue** (`OBJECT::`) | **yes** | **no** — Msg 15151 |
  | `CONTROL` on the queue | yes | yes |
  | `ALTER` on `SCHEMA::dbo` | yes (queue) | yes (queue) |
  | nothing | no | no |

  So the narrow right alone is enough for all five schemaless
  `ALTER ANY …` families — pair none of them with `rightAlterDatabase`. A
  right from one family confers nothing on another (`ALTER ANY MESSAGE TYPE`
  cannot alter a route, Msg 15151).

  **The queue's ALTER and DELETE need different rights**, and that is the one
  asymmetry in the set: `ALTER ON OBJECT::<queue>` drives the Properties write
  but is refused for the drop, which needs `CONTROL` on the queue or `ALTER`
  on its schema. Gate the Queue Properties page and Delete separately; do not
  reuse one entry for both.

- **`ALTER ANY BROKER PRIORITY` does not exist** — confirmed:
  `HAS_PERMS_BY_NAME(DB_NAME(),'DATABASE','ALTER ANY BROKER PRIORITY')`
  answers **NULL** on all three majors, and `sys.fn_builtin_permissions` has no
  DATABASE-class row for anything matching `%PRIORITY%`. Yet the server's own
  refusal message names `CREATE BROKER PRIORITY permission denied` /
  `ALTER BROKER PRIORITY permission denied` — a permission it enforces, does
  not publish and cannot be granted. Only `ALTER` on the database works, so
  broker priorities are `rightAlterDatabase` alone, as planned. Do **not** add
  a broker-priority name to `ProbedDatabasePermissions`: it would read
  `CapabilityUnknown` forever.

- The five names that do exist are in `sys.fn_builtin_permissions` at DATABASE
  class, spelled exactly as gosmo's `security.go` allowlist has them:
  `ALTER ANY CONTRACT`, `ALTER ANY MESSAGE TYPE`,
  `ALTER ANY REMOTE SERVICE BINDING`, `ALTER ANY ROUTE`, `ALTER ANY SERVICE`.
  (The class also defines `CREATE CONTRACT`, `CREATE MESSAGE TYPE`,
  `CREATE QUEUE`, `CREATE REMOTE SERVICE BINDING`, `CREATE ROUTE`,
  `CREATE SERVICE` — not needed while this pass ships no New-X dialogs.
  Worth knowing if that changes: `ALTER ANY <X>` implies `CREATE <X>`, but
  creating a **contract** or a **service** additionally needs `REFERENCES` on
  each message type / contract named and `CONTROL` on the queue — the narrow
  right alone fails them with Msg 15151, and so does `ALTER` on the database.)

- Dependency refusals are **Msg 3716**, verbatim: "The message type 'x' cannot
  be dropped because it is bound to one or more contract." / "The contract 'x'
  cannot be dropped because it is bound to one or more conversation priority."
  `ALTER` on the database does not override it. That is the § Stage E
  "let the server's message through" case, with the real message.
- gosmo's `security.go` allowlist (`databasePermissionNames`) already accepts
  **all five** — `ALTER ANY CONTRACT`, `ALTER ANY MESSAGE TYPE`,
  `ALTER ANY REMOTE SERVICE BINDING`, `ALTER ANY ROUTE`, `ALTER ANY SERVICE`
  (and `CREATE REMOTE SERVICE BINDING` besides), so nothing is needed there.
  `ProbedDatabasePermissions` in `capabilities.go` has **none** of them —
  add all five, or `rightsAllow` reads `CapabilityUnknown` forever and the
  gate never fires. Spellings confirmed live above;
  `live_probednames_test.go` is what keeps them honest.
- The securable classes (`sys.database_permissions.class_desc`
  `MESSAGE_TYPE`, `SERVICE_CONTRACT`, `SERVICE`, `REMOTE_SERVICE_BINDING`,
  `ROUTE`) are not among the ten gossms gates today. Deciding whether to read
  explicit DENY rows for them is a scope call: recommendation is **no** for
  this pass, and to update `docs/open-threads.md` § The rest, whose "no
  certificate … or Service Broker node" sentence this work partly invalidates.

## Stage E — object ops, scripting, menus (≈2 days)

- `scripting.go`'s `scriptables`: one entry per leaf, `ddlVerbs(script…, false)`
  — CREATE and DROP, **no ALTER** in the generated script set even for the
  families that have one (the same choice the rest of the tree makes).
- `explorer_object_ops.go`: `drop` for all seven; `rename` only where the
  statement exists — there is no `sp_rename` path for any of these, so
  **no renames at all**; `transfer` (Move to Schema) only for queues, through
  `securableTransferRights` (CONTROL on the securable), never the
  Rename/Delete set.
- **Remote Service Bindings on MI**: `drop` is fine (probed), and the family's
  Script as ▸ CREATE is the one verb the edition gate withholds there. Emit
  that CREATE as its own batch — see the 41906 note under § The target tree.
- A drop that the server refuses because something depends on it is the normal
  case here (a contract in use by a service, a queue bound to a service, a
  message type in a contract) — give each a `warning` and let the server's
  message through; do not pre-check dependencies.
- `explorer_drag.go`: queues only, for the same reason as `transfer`.
- Context menu (`app_explorer_data.go`'s `nodeMenuItems`): nothing beyond the
  standard verbs in this pass. If Queue Properties ships editable, no separate
  Enable/Disable item is needed — the page's Status row is the one place it
  lives, unlike the plan guide, which has no other page.

## Stage F — verification (≈2 days, interleaved, not last)

`docs/testing.md` first. Green `go test ./...` is not verification: every
family here is a catalog read whose failure mode is a column or a permission
the instance answers differently.

- gosmo: `live_service_broker_test.go` and the extended
  `live_versionsweep_test.go` on majors 13, 14 and 17, plus `t-qmi-01` for the
  Azure question in the edition gate. Confirm the new reads were actually
  *called*, not merely not-failed.
- gossms: drive the built binary under the tmux harness — expand all seven
  folders on a database that has broker objects and on one that has none
  (`master` is the second case), on 13 and on 17, and on the MI. An empty
  folder that errors instead of showing nothing is the likeliest defect;
  a folder that shows only system members on an empty database is the second.
- The Queue Properties write path is exercised live: change activation status,
  max readers and retention on a disposable queue, re-read, revert, drop.
- All fixture objects go on the live test server as disposable objects, named
  and dropped per the standing rule.

## Sequencing

A → B → (C ∥ D) → E → F, with F's version sweep also run at the end of A.
D (gating) is scheduled beside C rather than after it because the Queue and
Route pages cannot be declared `pagesThatOnlyRead` and cannot be gated with a
right that has not been probed yet — the probe is the long pole.

Commit boundaries one family at a time, end to end, not one stage at a time
across families: Message Types → Contracts → Queues → Services → Routes →
Remote Service Bindings → Broker Priorities. The order is the dependency
order, which is also the order the live test has to create them in.

## Open questions to settle before Stage A

**All four are closed as of 2026-09-16 — Stage A is unblocked.** Each entry
keeps its question for the record and states what was decided and why.

- **Azure.** *Settled for this pass.* Managed Instance was probed on
  2026-09-16 and needs no folder-level mechanism — the full table is under
  § The target tree, and the one refusal is `CREATE REMOTE SERVICE BINDING`
  (Msg 41906, at compile time). **SQL Database proper is deliberately not
  probed and not designed for**: build `edition_gate.go` with the single
  statement-level entry MI needs and **no folder-level mechanism**. If a SQL
  Database instance ever joins the test estate, run the same two probes
  (`SELECT COUNT(*) FROM sys.service_queues`, then a `CREATE MESSAGE TYPE`)
  and revisit only if its catalog views refuse. Decided 2026-09-16.
- **System objects: list, mark, or hide.** *Answered* — probed on majors 13
  and 17 on 2026-09-16; the table is under § Stage A. List and mark
  "(system)": id < 65536 for message types, contracts and services;
  `is_ms_shipped` for queues; **nothing for routes**, since `AutoCreatedLocal`
  sits at 65536 inside the user range. Pin it in a test, with msdb as a
  fixture for the Database Mail asymmetry — an absent member and an unmarked
  one are different bugs, and so is a "fixed" msdb.
- **Permission names.** *Answered* — probed on majors 13, 14 and 17 on
  2026-09-16; the table is under § Stage D. Five `ALTER ANY …` names exist and
  suffice alone, broker priorities have no grantable right at all, and the
  queue's ALTER and DELETE need different rights.

- **Editable Queue and Route Properties in this pass, or read-only like the
  rest of Phase 3.** *Decided 2026-09-16: **ship them**.* The cost that made
  this expensive was Stage D's probing, and that is done — the rights are
  known and identical on majors 13, 14 and 17, and all five names are in
  gosmo's `ProbedDatabasePermissions`. What remains is encoding the one
  asymmetry the probe found: the queue's Properties write takes
  `ALTER ON OBJECT::<queue>`, its Delete does not, needing `CONTROL` on the
  queue or `ALTER` on its schema. Neither page may be listed in
  `pagesThatOnlyRead`.
- **Message counts in the Detail Browser.** Worth the
  `sys.dm_db_partition_stats` join, or is a queue's row count a number that is
  stale the moment it is drawn? Recommendation: show it, in the folder listing
  only, as the one column that says whether a queue is doing anything.
