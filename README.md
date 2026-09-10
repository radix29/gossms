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

### 1. Install goSSMS

#### Homebrew — macOS and Linux

```bash
brew install radix29/tap/gossms
```

Apple silicon and Intel; `brew upgrade gossms` tracks new releases.

#### APT — Debian, Ubuntu and derivatives

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://radix29.github.io/apt/gossms.asc \
  | sudo tee /etc/apt/keyrings/gossms.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/gossms.asc] https://radix29.github.io/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/gossms.list > /dev/null
sudo apt-get update && sudo apt-get install gossms
```

`amd64` and `arm64`. Details at
[radix29.github.io/apt](https://radix29.github.io/apt).

#### Direct download

Download the latest release for your platform:

**https://github.com/radix29/gossms/releases/latest**

Release binaries are available for:

- Windows (amd64)
- Linux (amd64 and arm64)
- macOS (Apple silicon/arm64 and Intel/amd64)

Extract the downloaded archive before running the application.

The binaries themselves are not signed for Windows SmartScreen or macOS
Gatekeeper, so those will warn on first run. Integrity is covered instead by
`checksums.txt`, published with every release and signed by the release
workflow with [cosign](https://github.com/sigstore/cosign) in keyless mode:

```bash
sha256sum -c --ignore-missing checksums.txt
cosign verify-blob \
  --bundle checksums.txt.bundle \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/radix29/gossms/\.github/workflows/release\.yml@' \
  checksums.txt
```

Packages installed through Homebrew or APT are covered without any of this:
Homebrew checks the formula's `sha256`, and APT checks the repository's
GPG-signed `Release` file.

### 2. Start goSSMS

Installed through Homebrew or APT, `gossms` is already on your `PATH`:

```bash
gossms
```

Otherwise, on Windows, open PowerShell in the extracted folder and run:

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
specific port directly. An IPv6 address can be typed bare (`fe80::1`),
bracketed, or with a port as `[fe80::1]:1433` or `fe80::1,1433`.

Fields the chosen authentication method does not use are greyed out: a service
principal's application ID goes in **ClientID** with its secret in
**Password**, and **ClientID** is the app registration for Interactive and
Device Code, or the identity's client ID for a user-assigned Managed Identity.

New connections encrypt the whole session (**Encrypt: Mandatory**) with **Trust
Server Certificate** ticked, so an instance using SQL Server's self-signed
certificate still connects. Untick it to validate the certificate, and fill in
**CertHost** when connecting by an IP address or alias the certificate does not
name. **Strict** (TDS 8.0) needs SQL Server 2022 or later, or Azure SQL
Managed Instance, with a real certificate. A saved connection keeps the setting
it was saved with.

**Extra Properties** passes further driver settings as `key=value` pairs
separated by `;` or `&` — for example `ApplicationIntent=ReadOnly;
MultiSubnetFailover=true` or `packet size=8192`. A setting the dialog has its
own field for is refused. The **Connection String** preview is exactly what will
be dialled, with passwords masked.

On the server, goSSMS's sessions report `program_name` as `goSSMS` (Object
Explorer), `goSSMS - Query` (query windows) or `goSSMS - Activity Monitor`.

## Supported SQL Server versions

goSSMS supports **SQL Server 2016 SP1 (13.0.4001) and later** on Windows and
Linux, and **Azure SQL Managed Instance**. Features that are unavailable on the
connected SQL Server version or engine edition are hidden or disabled.

## Highlights

- **Object Explorer** for databases, tables, views, programmable objects,
  security, storage, SQL Server Agent, Always On, and other server objects.
- **Query editor** with multiple tabs, IntelliSense, `GO` batches, unlimited
  result rows, messages, and XML/JSON viewers.
- **Execution plans** as a graph, operator tree, or XML, with `.sqlplan` file
  support, missing-index details, and plan comparison.
- **Query Store** reports with plan viewing, comparison, tracking, forcing, and
  unforcing.
- **Administration tools** for properties, permissions, backup and restore
  (to disk or Azure Storage), attach and detach, indexes, statistics, and
  multi-object delete.
- **Monitoring** through Activity Monitor, blocking chains, sessions, and SQL
  Server and SQL Agent logs — several log files, from either or both, can be
  merged into one date-sorted view.
- **Azure SQL Managed Instance** support, including an Activity Monitor
  Instance tab showing CPU, storage, I/O and the resource governor's limits.
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

Connections are saved automatically, with the 30 most recent listed first.
Tools > Options controls the tree icon style, default results-grid cell width,
and IntelliSense.

Configuration is stored at:

- Linux/macOS: `~/.config/gossms/config.json`
- Windows: `%APPDATA%\gossms\config.json`

Saved passwords are encrypted with AES-256-GCM using `gossms.key`, stored next to
the configuration file. Delete both files to reset saved connections. Each
password is bound to its connection's server, port, user, authentication method
and encryption settings: editing any of those in `config.json` by hand makes the
saved password unreadable, and it has to be typed again.

## Known issues

- Microsoft Entra authentication has not yet been tested against live
  infrastructure.
- Backup and restore to Azure Storage build and validate correctly but have
  not been executed end to end; doing so requires a shared access signature
  credential on the container.
- macOS has not yet been tested on physical Mac hardware.
- Release binaries are not signed for SmartScreen or Gatekeeper, so both warn
  on first run. The published checksums are cosign-signed; see
  [Direct download](#direct-download).

## Links

- [Releases and downloads](https://github.com/radix29/gossms/releases)
- [Release notes](https://github.com/radix29/gossms/blob/main/RELEASE.md)
- [Report an issue](https://github.com/radix29/gossms/issues)

## License

goSSMS is © 2026 radix29 and licensed under
[GPL-3.0-or-later](LICENSE). The bundled
[sp_WhoIsActive](https://github.com/amachanic/sp_whoisactive) is © 2007–2026
Adam Machanic and retains its own GPL-3.0 copyright.
