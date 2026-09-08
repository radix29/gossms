# Plan — Phase 3, item 13: the tree's missing families

Scope: the eight object families SSMS shows and goSSMS does not — Types,
Assemblies, Rules, Defaults, Plan Guides, External Resources, Database
Snapshots, and the Tables sub-folders. Estimate **3–4 weeks**, most of it in
gosmo: none of the eight has any gosmo read today (`grep` for
`UserDefinedType|Assembly|PlanGuide|ExternalDataSource|source_database_id`
finds nothing outside comments).

Sibling items 14 (Service Broker) and 15 (keys / database audit specs) are
out of scope here. Item 15 is in fact largely shipped already — Database
Audit Specifications and Always Encrypted keys are in the tree — so re-scope
it before starting it, don't plan it from the phase list.

## The target tree

New nodes marked `+`; everything else already exists.

```
Databases
  System Databases
+ Database Snapshots              (source_database_id IS NOT NULL)
  <database>
    Tables
+     System Tables
+     FileTables                  (is_filetable)
+     External Tables             (is_external)
+     Graph Tables                (is_node OR is_edge, 2017+)
      <user tables …>
    Views
+   External Resources
+     External Data Sources
+     External File Formats
+     External Libraries          (2017+)
    Programmability
      Stored Procedures
      Functions
      Database Triggers
+     Assemblies
+     Types
+       System Data Types
+       User-Defined Data Types
+       User-Defined Table Types
+       User-Defined Types (CLR)
+       XML Schema Collections
+     Rules
+     Defaults
+     Plan Guides
      Sequences
      Synonyms
    Query Store / Security / Storage
```

Two placement decisions, both SSMS's:

- **Database Snapshots is a server-level folder**, a sibling of System
  Databases, not a folder under the source database. `loadDatabasesChildren`
  must also *exclude* snapshots from the user-database list it builds today,
  or every snapshot appears twice.
- **Tables sub-folders come before the user tables**, the way System
  Databases precedes the user databases, and each is listed only if it has
  members — SSMS always shows System Tables and FileTables, and shows
  External/Graph only on a server that can have them. Follow SSMS: always
  list System Tables and FileTables; gate External Tables on the folder being
  non-empty and Graph Tables on major ≥ 14.

## Scope of write support, decided up front

Read, Properties (read-only), Script as (CREATE/DROP where the object has a
scriptable definition), and Delete for every family. Beyond that, only:

- **Database Snapshots**: New Snapshot dialog (`CREATE DATABASE … AS SNAPSHOT
  OF`) and *Restore Database from snapshot*. The two operations that make the
  folder worth having.
- **Plan Guides**: Enable / Disable (`sp_control_plan_guide`). One statement,
  and a disabled plan guide is invisible otherwise.

Everything else is read-only, argued rather than deferred: Rules and Defaults
are deprecated by Microsoft (`sp_bindrule` since 2008) and creating one in a
new tool would be a mistake; assemblies need a binary payload no TUI dialog
can supply; UDTs/table types are `CREATE TYPE` with no `ALTER`, so an editor
would be a drop-and-recreate that breaks every column typed on it. Record
these four in `docs/open-threads.md` § Deferred scope when the work lands, so
"why can't I create a rule?" has a standing answer.

## Stage A — gosmo reads (≈8 days, the critical path)

One file per family, matching gosmo's existing one-file-per-domain layout.
Each gets the list + `…ByNameContext` finder pair every family here already
has, a `Drop…Context`, and a live test.

| File | Types | Catalog |
|---|---|---|
| `user_defined_type.go` | `UserDefinedDataType`, `UserDefinedTableType`, `ClrType`, `XmlSchemaCollection` | `sys.types` (`is_user_defined`, `is_table_type`, `is_assembly_type`), `sys.table_types`, `sys.xml_schema_collections` |
| `assembly.go` | `Assembly`, `AssemblyFile` | `sys.assemblies`, `sys.assembly_files`, `sys.assembly_modules` |
| `rule_default.go` | `Rule`, `Default` | `sys.objects` type `'R'` / `'D'`, joined to `sys.sql_modules` for the definition |
| `plan_guide.go` | `PlanGuide` | `sys.plan_guides` |
| `external_resource.go` | `ExternalDataSource`, `ExternalFileFormat`, `ExternalLibrary` | `sys.external_data_sources`, `sys.external_file_formats`, `sys.external_libraries` |
| `database_snapshot.go` | snapshot list, create, restore | `sys.databases.source_database_id`, `sys.master_files` for the sparse files |

Traps, each of which will otherwise cost a live-test round trip:

- **`sys.objects` type `'D'` is both standalone defaults and default
  constraints.** A standalone default (the one that belongs in the Defaults
  folder) has `parent_object_id = 0`. Without that predicate the folder fills
  with every `DF_…` on every table.
- **A table type's columns are not on `sys.columns` under the type's id** —
  they hang off the *internal* table, `sys.table_types.type_table_object_id`.
- **Version gates**, all via `colSince` per column, never a whole-query
  branch, and all recorded in `version_gate_inventory_test.go`:
  `sys.tables.is_node`/`is_edge` (2017), `sys.external_libraries` (2017 —
  refuse the whole read with `ErrUnsupportedVersion` on 13, as the Query Store
  wait reads already do), `sys.plan_guides` and `sys.assemblies` are fine on
  13. `is_external` and `is_filetable` are fine on 13.
- **`Database` vs `DatabaseByName`** — every new collection method hangs off
  `*Database` and every gossms loader reaches it through
  `DatabaseByNameContext`, matching the existing loaders. See
  `go doc gosmo.Server.Database`.
- **Snapshot create is a `CREATE DATABASE` with one file clause per source
  data file**, read from `sys.master_files` of the source; the log file is
  excluded and getting that wrong is the usual failure.

Extend `live_versionsweep_test.go` with every new read, labelled, and run it
on `win10cli\SQL2016` (major 13) before writing any gossms code against it —
that sweep is what caught the last nine version defects.

## Stage B — the tree (≈4 days)

Per family, the five touch points from ARCHITECTURE.md § Adding an Object
Explorer node type, plus the two that document doesn't list:

1. `tree_node.go` — `NodeType` constants (folder + leaf), `nodeIcon` **and**
   its ASCII fallback (both switches, ~line 300 and ~line 410), `nodeTypeName`,
   and the leaf added to `hasChildren`'s false list.
2. `explorer_loaders.go` — one `childLoaders` entry per expandable type.
3. Loaders: Types/Assemblies/Rules/Defaults/Plan Guides and the Tables
   sub-folders go in `explorer_objects.go`; External Resources gets its own
   `explorer_external.go` (three folders + three leaf loaders is enough for a
   file); Database Snapshots in `explorer_databases.go`, next to
   `loadDatabasesChildren`, which changes in the same edit.
4. `loadProgrammabilityChildren` and `loadDatabaseChildren` gain the new
   folders, in the SSMS order shown above.
5. `explorer_filter.go`'s `filterProps` — every new *folder* type needs a
   case, or the Filter menu item silently offers nothing. Name+date for the
   schema-scoped families; name-only for plan guides and external resources.

`explorer_objects.go` is 365 lines and will roughly double. Split it when it
passes ~600: `explorer_objects.go` keeps tables and their sub-objects,
`explorer_programmability.go` takes types/assemblies/rules/defaults/plan
guides — by exact line range, diffed byte-for-byte, per CLAUDE.md.

## Stage C — Detail Browser + Properties (≈5 days)

- **Detail Browser**: a new `detail_browser_programmability.go` and the
  folder-list cases in `detail_browser.go`'s dispatch (~line 523 is the shape
  to copy: one `case` listing several node types, delegating to one file).
  Each leaf reuses the same finder its Properties page uses — that rule is
  already stated in `detail_browser_storage.go`'s header comment and it is
  what keeps the pane and the dialog from disagreeing.
- **Properties**: read-only pages, one file per family
  (`type_props.go`, `assembly_props.go`, `rule_default_props.go`,
  `plan_guide_props.go`, `external_resource_props.go`,
  `database_snapshot_props.go`), each registered in the object's page slice
  and each with a `*_props_page_test.go` beside it.
  - Every page that cannot write is exempt from `withRequires` only by being
    named in `pagesThatOnlyRead`; `prop_page_requires_test.go` fails otherwise.
    Plan Guide's page *can* write (enable/disable) — it takes
    `rightAlterDatabase`, not a made-up "plan guide" right.
  - **Labels must fit `propsheet.LabelWidth` (30 columns)** and clip silently
    if not. "User-defined table type columns" (33) and "External data source
    credential" (32) are already over — shorten at authoring time;
    `TestNoPropertySheetLabelIsTruncated` is the backstop, not the design.

## Stage D — scripting and object ops (≈3 days)

- `scripting.go`'s `scriptables`: one entry per new leaf. Assemblies get
  CREATE (with the hex payload elided the way the credential secret is —
  see `NodeDatabaseScopedCredential`'s comment) and DROP; rules, defaults,
  UDTs, table types and XML schema collections get CREATE/DROP; plan guides
  get an `sp_create_plan_guide` CREATE and a DROP. **No ALTER anywhere in
  this set** — none of these objects has one.
- gosmo side: a new `scripter_programmability.go` beside `scripter_objects.go`.
- `explorer_object_ops.go`'s table: `drop` for all, plus `rename`/`transfer`
  only where SQL Server allows it — types and XML schema collections transfer
  between schemas, plan guides and assemblies do not (no schema), and a type
  in use by a column refuses the drop, so give it a `warning`.
- `explorer_drag.go`'s draggable list gains the schema-scoped leaves.
- Context-menu items in `app_explorer_data.go`'s `nodeMenuItems`: New Snapshot
  on `NodeDatabaseSnapshots` and on `NodeDatabase`, Restore-from-snapshot and
  Delete on a snapshot leaf, Enable/Disable on a plan guide.

## Stage E — Database Snapshots and Tables sub-folders (≈4 days)

These two are separated from the rest because both change existing,
well-covered behaviour rather than adding beside it:

- `loadDatabasesChildren` stops listing snapshots as user databases; the
  Detail Browser's Databases list (`detail_browser_databases.go`) must make
  the same exclusion or the two panes disagree.
- A snapshot database is read-only and has no log; the Database Properties
  pages already reachable from it must not offer writes. Check the
  recovery-model and files pages specifically.
- `loadTablesChildren` becomes a folder builder plus four filtered lists. The
  folder's server-side filter pushdown (`serverFilter(node.data.Filter)`) has
  to keep working *inside* each sub-folder, which means the sub-folder node
  carries the parent's `Filter` — the point at which the existing filter tests
  will fail if it doesn't.

New Snapshot dialog: source database (fixed), snapshot name, and a per-file
path grid defaulted from the source's data files. Follow
`attach_database_dialog.go`, which is the same shape (a file grid over a
database-level DDL statement).

## Stage F — verification (≈2 days, interleaved, not last)

`docs/testing.md` first. Green `go test ./...` is not verification here: every
one of these families is a catalog read, and the failure mode is a column the
instance doesn't have, which unit tests cannot see.

- gosmo: `live_versionsweep_test.go` extended and run on majors 13, 14 and 17.
  Confirm the new reads were actually *called* (the sweep's `call` labels),
  not merely not-failed.
- gossms: drive the built binary under the tmux harness — expand every new
  folder on a database that has members and on one that has none, on 13 and on
  17. An empty folder that errors instead of showing nothing is the most
  likely defect.
- Fixture objects (assemblies, plan guides, a snapshot) go on the live test
  server as disposable objects, named and dropped per the standing rule.

## Sequencing

A → B → (C ∥ D) → E → F, with F's live sweep run at the end of A as well as
at the end. E depends on nothing in C/D and can run in parallel with them if
the snapshot work goes to a second pass.

Suggested commit boundaries, one family at a time end-to-end rather than one
stage at a time across families — a half-wired family is hard to review, and
the per-family slice is what the live test can actually exercise:
Types → Assemblies → Rules+Defaults → Plan Guides → External Resources →
Tables sub-folders → Database Snapshots.

## Open questions to settle before Stage A

- **External Streams** (`sys.external_streams`) exist only on Azure SQL Edge
  and 2019+ Big Data Clusters. Recommendation: omit, note in open-threads.
- **System Data Types** under Types is a fixed list of built-ins with no
  catalog read worth making — populate it from `sys.types` where
  `is_user_defined = 0`, which is one query and stays correct per version.
- **Graph Tables on major 13** — the folder is simply absent, not empty. Pin
  that in a test; an absent folder and an empty one are different bugs.
