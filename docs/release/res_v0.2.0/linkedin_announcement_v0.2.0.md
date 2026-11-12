# 🚀 goSSMS v0.2.0 is now available!

![goSSMS v0.2.0](proposal_01_veil.png)

goSSMS is a free, **open source**, terminal-based SQL Server management tool. It runs on **macOS, Linux and Windows** as a single executable, with no installer, client tools or separate database drivers. It supports SQL Server 2016 SP1 and later.

🎯 **The goal: functional parity with SQL Server Management Studio.** v0.2.0 is the **command-line release**: everything you do in the interface can now start to be scripted, automated, and handed to an AI agent.

## ✨ What's new in v0.2.0

### 💻 Command line, built for scripts and AI agents
`gossms <command>` runs goSSMS without its interface.

- **Built for AI agents:** `agent guide` prints a manual to paste into a system prompt, tailored to what the profile's login can actually do. `agent schema` exports the full command catalog as JSON for tool definitions. `agent context` reports the server, the login and any permissions the agent lacks.
- **Safe by design:** every script is classified read, write, DDL or admin and refused above the `--allow` level or the profile's Allowed Operations ceiling. `--dry-run` prints the exact T-SQL, and `--confirm` guards destructive calls.
- **Machine-friendly:** text, json, jsonl, csv, tsv and markdown output, capped stdout, exit codes that name the failure, and passwords never on the command line.
- **Wide coverage:** queries, execution plans, objects, data, monitoring, Extended Events, security, SQL Agent jobs and backup/restore.
- **Audit log:** every call that reads or changes a server, or is refused, appends one JSON line to `audit.jsonl`. T-SQL is hashed and passwords are never recorded.

```bash
gossms agent guide --brief -p dev          # manual for an agent's system prompt
gossms query run -p dev -Q "SELECT TOP 5 name FROM sys.tables" -o json
gossms object describe -p dev Sales.Customer
gossms plan estimated -p dev --file slow.sql
gossms monitor blocking -p dev
gossms server config set -p dev maxdop 4 --allow admin --dry-run
```

### 🖥️ In the interface
- **Generate Scripts...** for a database: every user object in dependency order, optionally with permissions, to a query window or a file. Runs as a background task with progress and Cancel.
- **New Server Role** dialog
- **SQL format style** option in Tools > Options for Edit > Format Document
- **Disabled objects dimmed** in the Object Explorer tree, and Group by Schema covering more object families
- **Warnings** for hidden index filters and for `NEXT VALUE FOR` advancing a sequence inside a rolled-back transaction

### 🔧 Fixes and polish
- SQL Agent alerts, operators and notifications follow the caller's permissions
- Application Roles work as principals
- Sequences accept decimal-sized ranges
- Text, menu, status and selection colors meet WCAG contrast in every theme

## 📦 Install or upgrade

### macOS and Linux — Homebrew
Apple silicon, Intel, and Linux (amd64/arm64). Already installed? `brew upgrade gossms`.

```bash
brew install radix29/tap/gossms
```

### Debian, Ubuntu and derivatives — APT
GPG-signed repository for `amd64` and `arm64`.

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://radix29.github.io/apt/gossms.asc \
  | sudo tee /etc/apt/keyrings/gossms.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/gossms.asc] https://radix29.github.io/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/gossms.list > /dev/null
sudo apt-get update && sudo apt-get install gossms
```

### Direct download
Windows (amd64, arm64), Linux (amd64, arm64) and macOS (arm64, amd64).

1. Download the archive for your platform from the release page and extract it.
2. **Windows:** open PowerShell in the extracted folder and run:

   ```powershell
   .\gossms.exe
   ```

3. **Linux and macOS:** open a terminal in the extracted folder and run:

   ```bash
   chmod +x ./gossms && ./gossms
   ```

Installed with Homebrew or APT, just run `gossms`. For the command line, start with `gossms help`.

### 🛡️ Windows Defender / SmartScreen

The Windows binaries are built by GitHub Actions and are **not code-signed**, so on first run Windows Defender SmartScreen may show *"Windows protected your PC"* and ask for permission. This is expected for unsigned open source software. To continue, click **More info**, then **Run anyway**. If you prefer, unblock the download first:

```powershell
Unblock-File .\gossms.exe
```

Integrity is covered by `checksums.txt`, published with every release and signed with [cosign](https://github.com/sigstore/cosign), so you can verify the download before running it. Homebrew and APT installs are verified by the package manager.

## Links

- ⬇️ [Direct downloads](https://github.com/radix29/gossms/releases)
- 💻 [GitHub repository](https://github.com/radix29/gossms) — source code and issues
- ⭐ **Star and watch** the repo on GitHub (Watch > Custom > Releases) to get notified about new releases
- 📖 [Command Line guide on the wiki](https://github.com/radix29/gossms/wiki/Command-Line) and [screenshots](https://github.com/radix29/gossms/wiki)
- 💬 [Join the Discord](https://discord.gg/7YVKzB3vZ) — questions, feedback and release announcements
- 📜 [License: GPL-3.0-or-later](https://github.com/radix29/gossms/blob/main/LICENSE) — free to use, study, modify and share
- 📋 [Release notes](https://github.com/radix29/gossms/blob/main/RELEASE.md)

Feedback, bug reports and contributions are always welcome!

> 🤖 **Disclaimer:** goSSMS is developed with the help of AI.

#goSSMS #SQLServer #OpenSource #DatabaseTools #TerminalUI #CLI #AIAgents #macOS #Linux #Windows
