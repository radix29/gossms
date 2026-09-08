<p align="center">
  <img src="gossms_logo.png" alt="goSSMS" width="720">
</p>

<p align="center">
  <strong>SQL Server management for Linux, macOS, and Windows</strong>
</p>

<p align="center">
  <a href="https://github.com/radix29/gossms/commits/main"><img src="https://img.shields.io/github/last-commit/radix29/gossms?style=flat" alt="Last commit"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/radix29/gossms?style=flat" alt="License"></a>
</p>

# goSSMS

goSSMS is a terminal-based SQL Server management application. It is distributed
as a single executable and does not require an installer, SQL client tools, or
separate database drivers.

![Demo](demo.gif)

## Installation

### 1. Download goSSMS

Download the latest release for your platform:

**https://github.com/radix29/gossms/releases/latest**

Release binaries are available for:

- Windows (amd64)
- Linux (amd64 and arm64)
- macOS (Apple silicon/arm64 and Intel/amd64)

Homebrew and PPA installation options are coming soon.

Extract the downloaded archive before running the application. Release binaries
are currently unsigned; SHA-256 checksums are published with each release.

### 2. Start goSSMS

On Windows, open PowerShell in the extracted folder and run:

```powershell
.\gossms.exe
```

On Linux or macOS, open a terminal in the extracted folder and run:

```bash
chmod +x ./gossms
./gossms
```

`gossms --version` prints the version, commit, build date and licence and exits,
without starting the interface — quote it when reporting a bug.

You need a modern terminal with UTF-8 and 256-colour support, plus network access
to a SQL Server instance.

### 3. Connect to SQL Server

The Connect to Server dialog opens at startup. Enter the server name and choose
SQL Server, Windows Integrated, or a supported Microsoft Entra ID authentication
method.

For a named instance such as `SERVER\INSTANCE`, leave **Port** blank so SQL
Browser can resolve the instance. Use a port only when you want to connect to a
specific port directly.

## Supported SQL Server versions

goSSMS supports **SQL Server 2016 SP1 (13.0.4001) and later** on Windows and
Linux. Features that are unavailable on the connected SQL Server version are
hidden or disabled.

## Highlights

- **Object Explorer** for databases, tables, views, programmable objects,
  security, storage, SQL Server Agent, Always On, and other server objects.
- **Query editor** with multiple tabs, IntelliSense, `GO` batches, unlimited
  result rows, messages, and XML/JSON viewers.
- **Execution plans** as a graph, operator tree, or XML, with `.sqlplan` file
  support, missing-index details, and plan comparison.
- **Query Store** reports with plan viewing, comparison, tracking, forcing, and
  unforcing.
- **Administration tools** for properties, permissions, backup and restore,
  attach and detach, indexes, statistics, and multi-object delete.
- **Monitoring** through Activity Monitor, blocking chains, sessions, and SQL
  Server and SQL Agent logs.
- **Least-privilege operation**: unavailable actions are disabled and missing
  permissions are identified where SQL Server exposes that information.

## Required rights

goSSMS does not add privileges to your login. Connecting requires `CONNECT SQL`;
seeing and opening databases may also require `VIEW ANY DATABASE` and `CONNECT`
on the database. Each query and administrative action requires the same SQL
Server permission it would require in another client.

When a permission is missing, goSSMS disables the affected action, opens an
editable page as read-only, displays inaccessible values as `N/A`, or reports the
required permission where possible.

### `VIEW SERVER STATE`

Live server information, including Activity Monitor data and some server
properties, requires `VIEW SERVER STATE`.

On SQL Server 2022 and later, `VIEW SERVER STATE` is split into
`VIEW SERVER PERFORMANCE STATE` and `VIEW SERVER SECURITY STATE`; either narrower
grant covers its corresponding information.

## Configuration

Connections are saved automatically, with the 15 most recent listed first.
Tools > Options controls the tree icon style, default results-grid cell width,
and IntelliSense.

Configuration is stored at:

- Linux/macOS: `~/.config/gossms/config.json`
- Windows: `%APPDATA%\gossms\config.json`

Saved passwords are encrypted with AES-256-GCM using `gossms.key`, stored next to
the configuration file. Delete both files to reset saved connections.

## Known issues

- Microsoft Entra authentication has not yet been tested against live
  infrastructure.
- macOS has not yet been tested on physical Mac hardware.
- Release binaries are unsigned; checksums are provided.

## Links

- [Releases and downloads](https://github.com/radix29/gossms/releases)
- [Release notes](https://github.com/radix29/gossms/blob/main/RELEASE.md)
- [Report an issue](https://github.com/radix29/gossms/issues)

## License

goSSMS is © 2026 radix29 and licensed under
[GPL-3.0-or-later](LICENSE). The bundled
[sp_WhoIsActive](https://github.com/amachanic/sp_whoisactive) is © 2007–2026
Adam Machanic and retains its own GPL-3.0 copyright.
