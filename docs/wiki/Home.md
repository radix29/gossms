Welcome to the goSSMS wiki!

<p align="center">
  <img src="https://github.com/radix29/gossms/raw/main/gossms_logo.png" alt="gossms" width="720">
</p>

<p align="center">
  <strong>Manage SQL Server without leaving macOS or Linux</strong>
</p>

<p align="center">
  <a href="https://github.com/radix29/gossms/commits/main"><img src="https://img.shields.io/github/last-commit/radix29/gossms?style=flat" alt="Last commit"></a>
  <a href="https://github.com/radix29/gossms/releases/latest"><img src="https://img.shields.io/github/v/release/radix29/gossms?sort=semver" alt="Latest release"></a>
  <a href="https://github.com/radix29/gossms/blob/main/LICENSE"><img src="https://img.shields.io/github/license/radix29/gossms?style=flat" alt="License"></a>
</p>

# goSSMS

A terminal-based SQL Server Management Studio for Linux, macOS, and Windows.
One executable — no GUI, no installer, no SQL client tools or drivers required.

Current release: **v0.1.0**. See
[RELEASE.md](https://github.com/radix29/gossms/blob/main/RELEASE.md) for what
changed and
[CHANGELOG.md](https://github.com/radix29/gossms/blob/main/CHANGELOG.md) for
the detail behind it. Supported servers: **SQL Server 2016 SP1 and later**, on
Windows and Linux, and **Azure SQL Managed Instance**. Features unavailable on
the connected version or engine edition are hidden or greyed with the reason,
not left to fail at the server.

**New in v0.1.0:** Windows on ARM, backup and restore safety checks, atomic
config writes, and Results To File that no longer overwrites on a stopped run.

Questions and feedback: [goSSMS on Discord](https://discord.gg/7YVKzB3vZ).
Bugs and feature requests: [issues](https://github.com/radix29/gossms/issues).

## Install

```bash
brew install radix29/tap/gossms          # macOS and Linux
```

Debian, Ubuntu and derivatives install from the APT repository at
[radix29.github.io/apt](https://radix29.github.io/apt); single-file binaries
for Windows (amd64 and arm64), Linux (amd64 and arm64) and macOS (Apple
silicon and Intel) are on the
[releases page](https://github.com/radix29/gossms/releases/latest). The full
instructions, including what to expect on first run, are in
[README.md](https://github.com/radix29/gossms/blob/main/README.md#installation).

## Pages

### Getting connected

| | |
|---|---|
| [Connecting](https://github.com/radix29/gossms/wiki/Connecting) | The Connect dialog: saved connections, every authentication method, encryption and certificate validation, where passwords are kept. |
| [Options and the terminal](https://github.com/radix29/gossms/wiki/Options-and-Terminal) | Tools ▸ Options, config and key files, themes and icon styles, update checks, clipboard, crash recovery, and what a terminal that swallows a key does to you. |
| [Keys and mouse](https://github.com/radix29/gossms/wiki/Keys-and-Mouse) | The keys and gestures people ask for most, and where the complete list lives. |

### Browsing and editing

| | |
|---|---|
| [Object Explorer](https://github.com/radix29/gossms/wiki/Object-Explorer) | The tree and the Details pane: what is browsable, Filters Settings, drag-to-editor, Rename/Delete/Move to Schema, and how permissions and editions grey a menu. |
| [Scripting](https://github.com/radix29/gossms/wiki/Scripting) | Script <Noun> as ▸ and its destinations, Script Changes in every write dialog, and the operations that only ever script. |
| [Query Editor](https://github.com/radix29/gossms/wiki/Query-Editor) | The editor, Find and Replace, Format Document, IntelliSense, executing a batch, the result tabs, Messages, Results To File and export. |
| [Security](https://github.com/radix29/gossms/wiki/Security) | Logins, server and database roles, users, credentials, permissions and securables, auditing, and row-level security policies. |
| [Encryption and keys](https://github.com/radix29/gossms/wiki/Encryption-and-Keys) | Database master keys, certificates, asymmetric and symmetric keys, and the Always Encrypted key stores. |
| [Server and database properties](https://github.com/radix29/gossms/wiki/Server-and-Database-Properties) | Every properties page, what it writes, and what a page shows when the login could not read a value. |
| [Database tasks](https://github.com/radix29/gossms/wiki/Database-Tasks) | New Database, snapshots, attach and detach, offline and online, Shrink, backup history, and the Background Tasks view. |
| [Backup and Restore](https://github.com/radix29/gossms/wiki/Backup-and-Restore) | The two dialogs: what each checks before it runs, and what it refuses to do at all. |
| [SQL Server Agent](https://github.com/radix29/gossms/wiki/SQL-Server-Agent) | Jobs, steps, schedules, alerts, operators and the Agent reports. |

### Diagnosing and tuning

| | |
|---|---|
| [Execution plans](https://github.com/radix29/gossms/wiki/Execution-Plans) | Estimated and actual plans, the graph/tree/XML views, Live Query Statistics, .sqlplan files and Compare Showplan. |
| [Query Store](https://github.com/radix29/gossms/wiki/Query-Store) | The seven SSMS reports, the Query Store panel, forced plans and tracked queries. |
| [Activity Monitor](https://github.com/radix29/gossms/wiki/Activity-Monitor) | The charts, the Block and Sessions tabs over sp_WhoIsActive, TempDB, a session's live plan, and the Azure instance tab. |
| [Log File Viewer](https://github.com/radix29/gossms/wiki/Log-File-Viewer) | SQL Server and SQL Agent logs and archives, the client filter and the server-side search. |
| [Replication](https://github.com/radix29/gossms/wiki/Replication) | Publications, subscriptions and the Replication Monitor — monitored, not configured. |

### Server features

| | |
|---|---|
| [Always On](https://github.com/radix29/gossms/wiki/Always-On) | Availability groups, the dashboards, replicas, listeners, backup preferences and read-only routing. |
| [Service Broker](https://github.com/radix29/gossms/wiki/Service-Broker) | The seven families, and the two you can edit. |
| [Extended Events](https://github.com/radix29/gossms/wiki/Extended-Events) | XEvent Profiler, the live/target viewer, sessions and their four pages, and merging .xel files. |
| [Full-Text Search](https://github.com/radix29/gossms/wiki/Full-Text-Search) | Catalogs, stoplists, search property lists, and a table's full-text index and populations. |
| [Resource Governor](https://github.com/radix29/gossms/wiki/Resource-Governor) | Pools, groups, the classifier function and the live counters beside each limit. |
| [Database Mail](https://github.com/radix29/gossms/wiki/Database-Mail) | Status, queues, profiles and accounts, the properties sheet that replaces the wizard, and the test mail that waits for the outcome. |
| [Azure SQL Managed Instance](https://github.com/radix29/gossms/wiki/Azure-SQL-Managed-Instance) | What works against a Managed Instance, and what the edition refuses. |

## Reading this wiki next to the app

**The F1 help dialog is the keyboard reference.** It is part of the binary, so
it is always the version you are running; the
[Keys and mouse](https://github.com/radix29/gossms/wiki/Keys-and-Mouse) page
lists the keys people reach for most and does not try to replace it.

Sections marked *(Screenshots for this section are not captured yet.)* describe
shipped behaviour whose capture has not landed yet — the feature is there, the
picture is not. The captures live beside the wiki's source in the repository at
[docs/wiki/screenshots](https://github.com/radix29/gossms/tree/main/docs/wiki/screenshots);
a new one is a pull request against that folder plus one line in the page that
uses it.

## License

goSSMS is © 2026 radix29 and licensed under
[GPL-3.0-or-later](https://github.com/radix29/gossms/blob/main/LICENSE). The
bundled [sp_WhoIsActive](https://github.com/amachanic/sp_whoisactive) is
© 2007–2026 Adam Machanic and retains its own GPL-3.0 copyright.
