# Release Notes

What changed in the current goSSMS release. The detail behind each line — and
every earlier release — is in [CHANGELOG.md](CHANGELOG.md).

## v0.0.11 — 2026-09-15

**Install it from a package manager, and sign in with Microsoft Entra ID.**

### ⭐ Homebrew — macOS and Linux

```bash
brew install radix29/tap/gossms
```

Apple silicon, Intel and Linux (amd64/arm64). `brew upgrade gossms` tracks
every new release. **This is the first release the tap actually serves**: the
v0.0.10 release job rendered the formula correctly and then pushed nothing, so
`brew install radix29/tap/gossms` returned 404 for that whole cycle. Fixed
here, and the job now fails instead of going green on an empty push.

### ⭐ APT — Debian, Ubuntu and derivatives

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://radix29.github.io/apt/gossms.asc \
  | sudo tee /etc/apt/keyrings/gossms.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/gossms.asc] https://radix29.github.io/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/gossms.list > /dev/null
sudo apt-get update && sudo apt-get install gossms
```

GPG-signed, `amd64` and `arm64`, at
[radix29.github.io/apt](https://radix29.github.io/apt). `apt-get upgrade`
tracks new versions.

### ⭐ Microsoft Entra ID sign-in

Seven methods — **MFA**, **Device Code**, **Password**, **Service Principal**,
**Managed Identity**, **Default** and **Azure CLI** — all but Managed Identity
verified end to end against a live tenant. **Device Code** is the one that
makes goSSMS usable where a browser is not: it shows a code to enter at
Microsoft's sign-in page from a phone or another machine, so a server reached
over SSH signs in like any other. One sign-in covers the whole session —
Object Explorer, every query window, Activity Monitor and every reconnect, on
every server in the same tenant.

Also in this release: query windows keep a real SQL Server session, long
writes run behind a cancellable progress dialog, and Object Explorer gained
the remaining SSMS database families. Updates `gosmo` to v0.0.13.

### New

- ⭐ **Homebrew tap that works** — `brew install radix29/tap/gossms`, macOS
  (Apple silicon and Intel) and Linux.
- ⭐ **APT repository** — GPG-signed, `amd64` and `arm64`, at
  [radix29.github.io/apt](https://radix29.github.io/apt).
- ⭐ **Microsoft Entra ID sign-in, seven methods**, verified against a live
  tenant; **TenantID optional**, as in SSMS.
- ⭐ **Device Code sign-in** in a dialog, with Copy Code — for a machine with
  no browser, or over SSH.
- **File > Clear Microsoft Entra Sign-ins** — switch accounts without a
  restart.
- **Query windows hold a session**: temp tables, `SET` options, `USE` and open
  transactions survive between runs, and the connection bar shows the SPID and
  any open transaction count.
- **A progress dialog for long writes** — spinner, elapsed time and a Cancel
  that really cancels the statement; greyed with a reason on a failover or a
  revert to a snapshot.
- **Database Snapshots** in Object Explorer, with **New Snapshot...**,
  **Revert to Snapshot** and Properties.
- **Programmability > Types** (all five folders), **XML Schema Collections**,
  **Assemblies**, **Rules**, **Defaults** and **Plan Guides** — Plan Guides
  can be enabled and disabled.
- **External Resources** — external data sources, file formats and libraries.
- **Tables now has System, FileTables, External and Graph sub-folders.**
- **Move to another schema** for the new type families that permit it.
- **Resource Governance page** on Azure databases — usage shown against the
  limit it is a percentage of.
- **Disk-usage charts in Object Explorer Details**, with a pinned readout.
- **Live row counter while a query runs** — `Executing... | 128413 rows`.
- **IPv6 addresses** in the Connect dialog.

### Fixes

- **Copy and paste garbled non-ASCII text on Windows** in both directions.
- Object Explorer selection jumped to a different node after a background
  load.
- A Properties dialog's previous showing could deliver loads and errors into
  the next one.
- SQL Server Agent Start/Stop/Enable/Disable/Delete had no permission gate.
- Closing a Query Store panel did not cancel its reads.
- Refresh leaked every node it replaced and never re-read server-scope
  permissions.
- Check for Updates reported "up to date" for a development build newer than
  the last tag.
- The Restore dialog showed an empty set for a database whose only backups are
  Azure automated ones; it now says why.
- An empty `varbinary` scripted as `0x00` — a different value.
- A schema-qualified security policy or trigger was named unqualified in its
  confirmation prompt.
- A long server or database name overflowed the Connect dialog's list.

### Changes

- `gosmo` v0.0.12 → v0.0.13, `tcell/v3` v3.4.2 → v3.5.0.
- **New connections encrypt by default** — `Encrypt: Mandatory` with Trust
  Server Certificate ticked. Saved connections keep their own setting.
- **A saved password is bound to its server, port, user, authentication method
  and encryption settings**, so it cannot be moved to another server by
  editing `config.json`.
- The Connect dialog greys the fields the chosen authentication method does
  not use, and sends only those.
- Recent connections: 30, up from 15.
- Closing a panel cancels its reads and returns its memory to the OS.
- Permission gating understands per-securable `CONTROL` — on an assembly, a
  type or an XML schema collection — and names the right at its real scope.
- The Detail Browser cancels a fetch it has moved past; the per-database
  capability probe is single-flight.
- Log File Viewer merges SQL Server and SQL Agent files together.
- Agent screens show Yes/No instead of `true`/`false`.
- `InstanceKey` includes the port for an address with no instance name.
