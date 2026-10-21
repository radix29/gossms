[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Azure SQL Managed Instance

A Managed Instance (`EngineEdition 8`) is **SQL Server with an Azure control plane
under it**: most of what this wiki describes works there unchanged, and a short list of
things it does not. goSSMS hides the second list rather than letting you find out from an
error message — and where hiding it would also take away something that *does* work, it
gates the individual row instead.

*(A screenshot of goSSMS on a Managed Instance is not captured yet.)*

## How the edition gates work

Two questions are asked in the same shape and compose: **what the login may not do**
(permission gating, see
[Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer)) and **what the
engine edition refuses** (edition gating). A refused item is absent, or greyed with a
short **`N/A`** note at its right edge — menus keep the note short because a menu note
widens every menu it appears in for a fact you already know from how you connected; the
property sheets, which have the room, **name the edition**.

An "Azure" check means *any* Azure engine edition — SQL Database, Managed Instance,
Synapse, SQL Edge — because those share the refusals. Where the two differ, the code reads
the **engine edition** rather than "is Azure", which matters because an Azure edition's
`VersionMajor` (12 on a Managed Instance) is not its feature level.

## What a Managed Instance keeps

The folders that Azure SQL Database does not have are **there** on an MI, because the
engine has them:

- **SQL Server Agent**, msdb, and **Database Mail**. On an MI, Agent sends notifications
  only through a profile named **`AzureManagedInstance_dbmail_profile`** — the Profiles
  page says so where you are choosing one, because a notification pointing at any other
  profile is silently the notification that never arrives.
- **Replication** — an MI publishes and distributes; Azure SQL Database can only be a push
  subscriber and has no replication catalogue to read, so it has no folder at all.
- **Resource Governor** — on Azure SQL Database the platform governs and nothing is
  configurable, so the node is absent; an MI keeps it.
- **Extended Events** as **server-scoped** sessions under Management, the way SSMS shows
  them. On Azure SQL Database sessions belong to a database (`ON DATABASE`), so there the
  folder hangs off each **database** node and holds Sessions alone — no XEvent Profiler,
  which is built on server-scoped sessions.
- **Service Broker** families, **availability groups** including distributed groups, and
  **database mirroring endpoints**.

An MI also writes `event_file` targets to **Azure Blob Storage**: a filename must be an
`https://` **blob URL**, and the container's SAS comes from a **server credential** named
after it (on Azure SQL Database, a database-scoped one). The Data Storage page offers the
credentials as the containers to write to and refuses a path before anything is sent.

## What a Managed Instance refuses

Every one of these was driven live against an MI, and is why the item is gated:

| | What the server answers |
|---|---|
| **Detach / Attach Database** | `Could not find stored procedure 'sp_detach_db'` |
| **Take Database Offline** | Msg 5008 — `ALTER DATABASE` statement is not supported |
| **Changing the recovery model** | Msg 5008 — user databases are `FULL` only |
| **`CREATE DATABASE` with files/filegroups** | Msg 41918 — specifying files and filegroups is not supported |
| **Script as ▸ CREATE REMOTE SERVICE BINDING** | Msg 41906 — not supported on MI |

The last row is about a *script*, and the reason is batch semantics: a statement the
server refuses to **compile** aborts the whole batch before any statement in it runs, so
the verb is withheld while `ALTER` and `DROP REMOTE SERVICE BINDING` stay available.

The New Database dialog is the instructive case for the opposite choice: `CREATE DATABASE`
**succeeds** on an MI as long as the file and filegroup clauses are left off — the
documented "server default" path — so the dialog withholds **those rows** and keeps the
dialog. Blocking it wholesale would withhold something that works.

`N/A` everywhere else means exactly that: the same dialog, one row fewer.

## Entra on an Azure edition

New Login and New User offer a Microsoft Entra principal
(`CREATE LOGIN/USER … FROM EXTERNAL PROVIDER`) on **every Azure edition**, and on a boxed
instance only from SQL Server 2022 (2016–2019 refuse the syntax outright, so offering it
there only lets you fill in a page to be refused). Both dialogs ask the one question, so
they cannot disagree. See
[Security](https://github.com/radix29/gossms/wiki/Security).

## How much of this is driven live

Being honest about it is better than a wiki that reads as if it were: on-premises SQL
Server from 2016 SP1 upward is the swept environment, and **Azure SQL Managed Instance is a
fourth environment, not swept**. MI behaviour — the refusals above, the folder set, the
database-scoped sessions on Azure SQL Database, the blob `event_file` — is pinned by tests
against a fake driver, with the refusals confirmed against a real MI. Resource Governor and
Database Mail on an MI, and an end-to-end blob event session on Azure SQL Database, are
**shown and not yet verified against a live Azure instance**, and `docs/open-threads.md` in
this repository tracks them as such.

If you run goSSMS against an Azure instance and something behaves differently from the two
tables above, that is worth reporting: the refusal list is a list of things someone actually
saw refused.

## See also

- [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer) — what is gated and why
- [Extended Events](https://github.com/radix29/gossms/wiki/Extended-Events) — session scope and blob targets
- [SQL Server Agent](https://github.com/radix29/gossms/wiki/SQL-Server-Agent) · [Database Mail](https://github.com/radix29/gossms/wiki/Database-Mail)

[← Home](https://github.com/radix29/gossms/wiki)
