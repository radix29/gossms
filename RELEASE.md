# Release Notes

What changed in the current goSSMS release. The detail behind each line — and
every earlier release — is in [CHANGELOG.md](CHANGELOG.md).

## v0.0.10 — 2026-09-09

**goSSMS installs from a package manager now.** Homebrew on macOS and Linux,
APT on Debian, Ubuntu and derivatives — both published by the release workflow
from the same binaries the GitHub release carries, so `brew upgrade` and
`apt-get upgrade` track new versions.

```bash
brew install radix29/tap/gossms          # macOS and Linux
```

```bash
# Debian / Ubuntu — full instructions in README.md
sudo apt-get update && sudo apt-get install gossms
```

Also: Azure SQL Managed Instance is properly supported, four new Object
Explorer families, and a merged Log File Viewer. Updates `gosmo` to v0.0.12.

### New

- ⭐ **Homebrew tap** — `brew install radix29/tap/gossms`, macOS (Apple silicon
  and Intel) and Linux.
- ⭐ **APT repository** — GPG-signed, `amd64` and `arm64`, at
  [radix29.github.io/apt](https://radix29.github.io/apt).
- **Azure SQL Managed Instance support**: engine-edition version gating,
  backup and restore to Azure Storage (`TO URL` / `FROM URL`), and operations
  the edition refuses are withheld with a note instead of failing.
- **Activity Monitor "Instance" tab** on Azure editions — CPU, storage against
  the quota, IO, and the resource governor's limits.
- **Database Triggers, Database Audit Specifications, Database Scoped
  Credentials and Cryptographic Providers** in Object Explorer, with
  Properties, Script and Delete; the first two also Enable/Disable.
- **New Database Audit Specification** and **New Database Scoped Credential.**
- **A view now has a Triggers folder** — its INSTEAD OF triggers.
- **Log File Viewer merges files** — several archives in one date-sorted grid,
  each row naming its file.
- **Query Store: Plot History** — the selected query's per-plan history,
  interval by interval.
- **`gossms --version`** prints version, commit, build date and licence
  without starting the interface.
- **The Connect dialog stays open while connecting**, with a spinner and a
  working Cancel.
- **macOS Intel binaries** are published again — five release targets.
- **FILESTREAM files** are handled in Database Properties > Files.
- **Query > Execute at Cursor** and **Edit > Delete Line.**

### Fixes

- Database Properties > General was empty on a Managed Instance.
- Every version gate silently degraded on a Managed Instance, which reports
  version 12 while running an 18.x engine.
- Agent status, Platform and disk-space rows were wrong on a Managed Instance.
- The server filesystem lost file size and modification time on Azure.
- Adding a file to a FILESTREAM filegroup failed the whole Add.
- The Back Up dialog did not re-gate the device for a hand-typed destination.
- A cancelled connection could pop an alert over an unrelated screen, or leak
  a live session.
- A scripted column encryption key rotation read the wrong value count.

### Changes

- `gosmo` v0.0.11 → v0.0.12.
- **The flat Triggers folder under a database is gone** — a DML trigger is
  listed under its own table or view, as in SSMS.
- Explicit `DENY` is honoured at database-principal, server and availability
  group scope.
- Server Properties > Advanced is editable.
- Database Role Properties > Members is gated on the role itself.
- Database Role and Server Role Properties share one General page.
- Database-scoped credentials require `CONTROL` on the database.
- Copy works in the Detail Browser and the Always On dashboard grids.
- `Button`, `CheckBox` and `RadioBox` can be disabled; a `Spinner` widget was
  added.
- Documentation split: `docs/ui-rules.md`, `docs/db-rules.md` and
  `docs/testing.md` own what `CLAUDE.md` used to carry.
