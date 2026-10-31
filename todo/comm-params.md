# Command-Line Interface Design for AI Agents

**Purpose:** Specify command-line parameters for **gossms**, an existing SSMS-like TUI application written in Go that runs on Linux, macOS and Windows. The design extends the existing project with a headless command-line mode so that AI agents (and scripts) can use its functionality. Every command returns text that is predictable and easy to parse.

**Scope:** §1–§13 define the command-line interface: commands, parameters, output, safety rules and exit codes. §0 records what a review against the gossms and gosmo source changed. §14 is the integration architecture and §15 the phased implementation plan; each phase gets its own detailed analysis before it is implemented.

> **One binary, two modes.** `gossms` with no parameters starts the TUI, as it does today. `gossms <group> <command> …` runs headless CLI mode and never starts the TUI. §2.1 has the rules.
>
> **Agents start here:** `gossms agent guide` describes the tool to an AI agent (§8).

---

## 0. Review against the codebase (2026-10-28)

The first draft was checked against gossms `feat/agentic-use` (33ea57f) and gosmo `main`. Each finding is resolved in the section named; nothing below is still open unless §Open decisions lists it.

| # | Finding in the source | Resolution |
|---|---|---|
| R1 | **There are no named profiles.** `config.json` holds only auto-saved *recent connections*: `Config.AddOrUpdate` names each one `GeneratedName()` (`server,port,db,user (tag)`), that name is the dedup key, the list is trimmed to `MaxSavedConnections` (30), and a password is kept only when Remember Password was ticked | Author's decision: a profile **is** a saved connection the user has given a name in the TUI. New fields `Profile` and `MaxAllow` on `config.Connection`, set from the Connect dialog's History pane; named entries are exempt from the 30-entry trim; `AddOrUpdate` carries both fields over when it replaces an entry. §4.1, §6.2, §14.4 |
| R2 | `config.Load` is not read-only: it creates `gossms.key` when missing, sweeps stale temp files and (via the TUI's startup save) re-seals legacy passwords | The CLI uses a new `config.LoadReadOnly` that opens passwords with an existing key only, sweeps nothing, creates nothing and has no `Save`. §14.4 |
| R3 | The plan's auth names (`integrated`, `entra-interactive`, …) are not gossms's nine `config.AuthMethod`s | `--auth` takes the real set; Entra Interactive and Device Code are refused in CLI mode (they prompt, and `db.entraCache` lives one process, so every call would prompt again). §3.1 |
| R4 | `-v` already means `--version` (`cmd/gossms/main.go`, used by `brew test`) | `-v` stays version; verbose logging is `--verbose` only. §2.1, §3.3 |
| R5 | Bare `gossms` without a controlling terminal already fails fast (`init screen: open /dev/tty`, exit 1), but with piped stdio **and** a controlling tty, tcell opens `/dev/tty` and the TUI starts — an agent hangs | Explicit check: stdin and stdout must both be terminals (`golang.org/x/term.IsTerminal`, already an indirect dependency, no GOOS branch). §2.1 |
| R6 | `query.WithMaxRows` **cancels** the batch at the cap and runs no later GO batch (`docs/db-rules.md` § Query execution). Using it for `--max-rows` would skip an `UPDATE` that follows a large `SELECT`, and the total row count would be unknown | CLI truncation happens in a CLI `RowSink` that keeps the first N rows and counts the rest; the executor runs uncapped. §5.1, §14.5 |
| R7 | The executor renders cells as the SSMS grid does (`2026-03-14 09:12:44.000`, uppercase GUID, `0x` uppercase hex); rows-affected and SQL errors exist only as message *text*; `RowSink.BeginSet` gets no column types | One renderer for TUI and CLI: the CLI prints the grid's text. Executor gains structured rows-affected, structured SQL errors (number, severity, state, line, procedure) and column types for sinks. §5.3, §14.5 |
| R8 | CSV NULL handling is settled (`docs/decisions.md` § SQL NULL in results): NULL is an empty field, the empty string is `""`. The writer lives in `internal/tui/query_panel_export.go` | CLI CSV reuses that writer, extracted to a TUI-free package; `--null-text` applies to `text`/`markdown` only. §5.2, §14.3 |
| R9 | `sqltext.Next` is the one T-SQL lexer, by settled decision (§ T-SQL lexing) | The safety classifier walks `sqltext` (`SplitBatches`, the statement-verb tables), not gosmo's `tsql.Spans` or a new lexer. §6.1 |
| R10 | `gosmo.Server.ApplyConfiguration` already does the `show advanced options` dance — as **one conditional batch** decided server-side, identical scripted and executed | `server config set` calls it; `--dry-run` prints that batch verbatim (examples in §10 corrected). Out-of-range values need `RECONFIGURE WITH OVERRIDE`: new `--override`, separate from `--force`. §4.2, §10 |
| R11 | `sys.configurations` has no default column and gosmo has no defaults table | New gosmo capability: per-major `sp_configure` defaults. Needed by `--non-default` and `config reset`. §15 phase 5 |
| R12 | Not in gosmo today: trace flags (`DBCC TRACESTATUS/ON/OFF`), `DBCC CHECKDB`, `log_reuse_wait_desc` + VLF count, plan-cache top queries, `system_health` deadlocks, object search in definitions/columns (`Database.Search` matches names only) | Added to gosmo in the phase that needs them (gosmo is the author's library: add, don't work around). §15 |
| R13 | gosmo's `Scripter` has no target-version option; there is no parquet writer; no SQLCMD mode exists in gossms; schema compare does not exist | Dropped: `--target-version`, `--format parquet`, `compare`. Deferred: `--var` (§Open decisions). |
| R14 | `--dry-run` for gosmo-backed writes is free: `gosmo.WithScript` records every write (with secrets as placeholders) and still runs reads | Every gosmo-backed write command implements `--dry-run` through `WithScript`; `query run --dry-run` prints the classified batches. §6.4 |
| R15 | Everything gossms writes lives beside `config.json` (`os.UserConfigDir()/gossms`: log, key, `recovered/`) | The audit log goes there too, not to an XDG state directory. §6.4 |
| R16 | The headless building blocks already exist and import no TUI: `internal/db` (connect), `internal/query` (Session, executor), `internal/showplan`, `internal/activity`, `internal/format`, `internal/config` | The CLI is a new `internal/cli` tree over these; it never imports `internal/tui`. §14 |
| R17 | `db.Role` sets `program_name`; there is no CLI role | New `db.RoleCLI` (`goSSMS - CLI`), so CLI sessions are identifiable in Activity Monitor and DMVs. §14.4 |
| R18 | gosmo supports major 13 (SQL Server 2016) and later; Azure SQL MI reports major 12 but is engine 18 | Version and edition refusals (`gosmo.ErrUnsupportedVersion`, `ErrUnsupported`) get their own exit code 11. §7 |
| R19 | `docs/decisions.md` § No top-level plan document | This file stays in `todo/`. The CLI contract moves to `docs/cli.md` (registered in `ARCHITECTURE.md` § Which document owns what), started in phase 0 and grown per phase; user-facing usage to `README.md` (one sentence in phase 0, full usage with phase 1). §15 phase 0 |

---

## 1. Design principles

| # | Principle | Why it matters for AI agents |
|---|-----------|------------------------------|
| 1 | **Noun → verb structure** (`gossms <group> <command> [options]`), like `az`, `kubectl`, `gh` | The agent can guess commands, and the tree is easy to discover |
| 2 | **Non-interactive by default.** No prompts, dialogs, spinners or pagers | A prompt would stall the agent |
| 3 | **Stable, self-describing output** (`text`, `json`, `csv`, `tsv`, `markdown`) | The agent can parse output without guessing |
| 4 | **stdout = results, stderr = diagnostics** | Results stay clean when the agent pipes or captures them |
| 5 | **Meaningful exit codes** | The agent can branch on success or failure without reading text |
| 6 | **Read-only by default.** Writes need explicit opt-in | Limits the damage from a hallucinated `DELETE` |
| 7 | **Every action can show its T-SQL** (`--dry-run`) | The agent (or a human) can check the change before running it |
| 8 | **Bounded output** (row limits, column width limits, truncation markers) | Keeps output within the agent's context window |
| 9 | **Self-describing** (`agent guide`, `agent schema`, `agent context`) | The agent can learn the tool, its rules and its environment by itself |
| 10 | **Stateless calls.** Each call connects, runs and disconnects | Calls can be repeated safely and need no hidden session state |

---

## 2. General syntax

```text
gossms <group> <command> [positional-args] [options]
gossms <group> <command> --help
gossms help [--output json]
gossms agent guide            # start here if you are an AI agent
```

### 2.1 Mode selection (TUI vs. CLI)

| Invocation | Behavior |
|------------|----------|
| `gossms` (no parameters at all) | **Always starts the TUI** (existing behavior) |
| `gossms <group> <command> …` | CLI mode. The TUI is never started |
| `gossms` with only options and no command (e.g. `gossms -p dev`) | Usage error: lists the command groups and points to `gossms agent guide`. Exit code `2` |
| `gossms --help` | Help text. The first line reads: *"AI agents: run `gossms agent guide` for complete usage instructions."* |
| `gossms --version` / `-version` / `-v` | Version block, byte-identical to today (`brew test` and CI depend on it). `gossms version` reports the same fields inside the envelope (§5.2 `data`) |

Until `agent guide` ships (phase 3) these messages name `gossms help` instead; a spec test fails on any hint naming a command the tree lacks, so the wording must change when it lands (phase 0 D1).

The rule is simple: **no parameters means TUI, any parameter means CLI** (the version flags are the one shipped exception). An agent that follows the guide always passes a command, so it never ends up in the TUI.

Bare `gossms` checks that **both stdin and stdout are terminals** before it opens the screen (`golang.org/x/term.IsTerminal`; works on every GOOS without a branch). If either is not, it exits `1` at once with an error on stderr that mentions `gossms agent guide`. Today it fails only when there is no controlling terminal at all; with piped stdio inside a terminal session tcell opens `/dev/tty` and the TUI would start and wait for keys (R5).

In CLI mode there is no alternate screen, mouse capture, raw terminal mode or spinner. Colors are used only when stdout is a terminal and `NO_COLOR` is not set.

### 2.2 Parsing rules

- Long options use `--kebab-case`. Short aliases exist only for very common options (`-S`, `-d`, `-o`, `-q`).
- `--option value` and `--option=value` are equivalent.
- Boolean flags: `--flag` and `--no-flag`.
- List options can be repeated (`--type table --type view`) or comma-separated (`--type table,view`).
- `-` means stdin, wherever a file is expected (`--file -`).
- `@args.txt` reads options from a response file (one option per line). This avoids shell-quoting problems.
- Unknown options are an **error** (exit code 2). They are never silently ignored.
- Shell quoting differs between bash, zsh, PowerShell and cmd. That is why `--file -` (stdin) and `--sql-base64` exist (§4.6). Agents should prefer them for anything except trivial SQL.

### 2.3 Configuration precedence

Highest wins:

```text
command-line option  >  environment variable  >  connection profile (gossms config file)  >  built-in default
```

### 2.4 Platforms

The CLI behaves identically on **Linux, macOS and Windows**:

- Output is UTF-8 without a BOM, with `\n` line endings on every OS.
- Output never depends on the OS locale (see §5.3).
- Ctrl+C / SIGINT cancels the running query, prints a `CANCELLED` result and exits with `130`.
- Examples in this document use bash. PowerShell equivalents are shown where quoting or piping differs.

---

## 3. Global options

These options apply to every command.

### 3.1 Connection

| Option | Alias | Env variable | Description |
|--------|-------|--------------|-------------|
| `--profile <name>` | `-p` | `GOSSMS_PROFILE` | Use a saved connection by its profile name (§4.1; recommended for agents) |
| `--server <host[\instance][,port]>` | `-S` | `GOSSMS_SERVER` | Server name, in the Connect dialog's syntax (`config/address.go`) |
| `--port <n>` | | | Port, when not part of `--server` |
| `--database <name>` | `-d` | `GOSSMS_DATABASE` | Initial database |
| `--auth <method>` | | `GOSSMS_AUTH` | One of `sql`, `windows`, `entra-default`, `entra-password`, `entra-msi`, `entra-sp`, `entra-azcli` (gossms's `config.AuthMethod`s). `windows` = SSPI on Windows, Kerberos on Linux/macOS. `entra-interactive` and `entra-device-code` exist in the TUI but are **refused** here (exit 4): they prompt, and the token cache lives one process, so every call would prompt again (R3) |
| `--user <name>` | `-U` | `GOSSMS_USER` | Login name (SQL, Entra Password) |
| `--tenant-id <id>` / `--client-id <id>` | | `GOSSMS_TENANT_ID` / `GOSSMS_CLIENT_ID` | Entra tenant and client (service principal, user-assigned managed identity) |
| `--password-env <VAR>` | | | Name of the env variable that holds the password or client secret |
| `--password-stdin` | | | Read the password from stdin (then `--file -` is unavailable in the same call) |
| `--encrypt <optional\|mandatory\|strict>` | | `GOSSMS_ENCRYPT` | Connection encryption (`config.EncryptMode`) |
| `--trust-server-certificate` | | | Skip certificate validation (dev only) |
| `--host-name-in-certificate <name>` | | | Expected certificate host name |
| `--application-intent <readwrite\|readonly>` | | | Route to readable secondaries (written into the connection's extra properties) |
| `--connect-timeout <sec>` | | | Default 15 |

> ⚠️ **Never accept a plain `--password <value>`.** Command lines show up in process lists, shell history and agent logs. Agents should use `--profile`, so the password saved with that connection (Remember Password) is used the same way the TUI uses it. For a profile saved without a password, or an ad-hoc connection, use `--password-env`, `--password-stdin` or Windows/Entra authentication. A SQL-auth call with no password anywhere fails with exit 4 and a hint naming both options.

The options `--server`, `--database`, `--auth`, `--user` and the TLS options are for ad-hoc connections. Combined with `--profile`, they override the profile's values for that one call. A profile's saved password is sealed to its server, port, user, auth method, TLS settings, extra properties, tenant and client (`connectionAAD`, `config/secret.go`) so that it cannot be redirected to another host. The CLI keeps that promise after decryption: an override of server, port, user, auth method, tenant or client, or one that weakens TLS (`--encrypt optional`, `--trust-server-certificate`, `--host-name-in-certificate`), drops the saved password and needs `--password-env`/`--password-stdin`. `--database` and `--application-intent` keep it.

### 3.2 Execution and safety

| Option | Description |
|--------|-------------|
| `--allow <read\|write\|ddl\|admin>` | Highest permitted operation class (default: `read`). See §6. **Per command, not global** (phase 5 D1): only `query run` and the writing commands take it |
| `--dry-run` | Generate and print the T-SQL but do not run it. **Per command** as `--allow`; a writing command's dry run is still gated (`--allow admin`) |
| `--confirm <token>` | Required for destructive operations. The token must equal the target object name (see §6.3). Per command (`db set --rollback-immediate`) |
| `--accept-risk` | `server config set/reset` only: proceed past a blocked value (§4.2). Unrelated to the global `--force` (phase 5 D3) |
| `--transaction <auto\|commit\|rollback>` | `rollback` runs the statement in a transaction and then rolls it back. Useful for testing DML. Restrictions in §6.4 |
| `--timeout <sec>` | Command timeout (default 60, `0` = unlimited) |
| `--override` | `server config set/reset` only: `RECONFIGURE WITH OVERRIDE` (gosmo `ConfigApplyOptions.Override`), for values SQL Server's plain `RECONFIGURE` refuses. Unrelated to `--force` |

`--var` (sqlcmd `$(name)` variables) is deferred: gossms has no SQLCMD mode, and substitution must respect the lexer (no replacement inside literals or comments). See §Open decisions.

### 3.3 Output

| Option | Alias | Description |
|--------|-------|-------------|
| `--output <text\|json\|jsonl\|csv\|tsv\|markdown>` | `-o` | Output format (default: `text`). The enum grows with the phases that implement each format: `text\|json` in phase 0, the rest in phase 2 — `agent schema` never lists a format that is not implemented (phase 0 D5) |
| `--max-rows <n>` | | Rows per result set (default **200**, `0` = unlimited) |
| `--max-col-width <n>` | | Truncate cell text (default **256** characters) |
| `--max-output-bytes <n>` | | Hard cap on total stdout (default 1 MB) |
| `--out-file <path>` | | Write the full result to a file. stdout gets only a summary and the path |
| `--null-text <str>` | | How NULL is shown in `text` and `markdown` (default `NULL`). CSV/TSV follow the settled rule in §5.2; JSON uses `null` |
| `--no-headers` | | Leave out column headers (csv/tsv/text) |
| `--include-messages` / `--no-messages` | | Include PRINT / info messages (default: include) |
| `--quiet` | `-q` | Results only. No envelope header |
| `--verbose` | | Diagnostic logging to stderr. No short alias: `-v` is `--version` (R4) |
| `--no-color` | | Always on when stdout is not a TTY |

---

## 4. Command tree

Overview of the groups (grouped like SSMS features):

```text
gossms             (no parameters: start the TUI)
├── agent          Self-description for AI agents (guide, schema, context)  → §8
├── profile        Saved connections            (Connect dialog)
├── server         Instance info and config     (Server properties, sp_configure)
├── db             Databases                    (Object Explorer → Databases)
├── object         Schema objects               (Object Explorer tree)
├── script         Generate scripts             (Script As… / Generate Scripts wizard)
├── query          Run T-SQL                    (Query editor)
├── plan           Execution plans              (Display Estimated/Actual Plan)
├── data           Table data                   (Select Top N / Edit Top N / Import-Export)
├── security       Logins, users, roles, perms  (Security node)
├── job            SQL Server Agent             (Agent node, Job Activity Monitor)
├── monitor        Activity Monitor and DMVs    (Activity Monitor, reports)
├── maint          Maintenance                  (Backup, restore, indexes, DBCC)
├── xevent         Extended Events              (XEvent Profiler)
└── help / version
```

(Schema compare was dropped: nothing in gossms or gosmo does it, R13.)

### 4.1 `profile`: connection profiles

**A profile is a saved connection with a name.** gossms saves every successful connection automatically (the Connect dialog's *Recent Connections*). In the TUI the user selects one there and chooses **Profile…**, which sets:

- **Profile name**: a short name such as `dev` or `prod-ro`. Unique (case-insensitive) across saved connections; letters, digits, `-`, `_`, `.`. Clearing it turns the entry back into a plain recent connection.
- **Allowed operations** (`maxAllow`, §6.2): `read`, `write`, `ddl`, `admin` (default `admin`).

A named connection is never trimmed by the 30-entry recent list, and reconnecting through the Connect dialog keeps its name and ceiling. Its password is the one saved with Remember Password; a profile saved without one needs `--password-env`/`--password-stdin` (or Windows/Entra auth).

The CLI **only reads** profiles. It never writes `config.json`, never creates `gossms.key`, and never re-seals passwords (R2).

`-p <name>` matches the profile name (case-insensitive). The full generated name of an unnamed recent connection (`srv,1433,db,user`) is accepted too, so a connection can be used before it is named; `profile list --all` shows those names.

| Command | Description |
|---------|-------------|
| `profile list [--all]` | Named profiles: name, server, port, database, auth method, `maxAllow`, whether a password is saved. `--all` adds unnamed recent connections. Secrets are never printed |
| `profile show <name>` | One profile's details, including its `maxAllow` ceiling (§6.2) and whether its saved password can be opened |
| `profile test <name>` | Connect and report version, edition, login and latency |

```bash
gossms profile list
gossms profile test dev
```

### 4.2 `server`: instance level

| Command | Description |
|---------|-------------|
| `server info` | Version, edition, collation, HADR, uptime, logical CPUs, NUMA nodes, physical memory |
| `server config list [--name <pattern>] [--non-default] [--advanced]` | `sp_configure` values: configured, running, default, min/max, dynamic, advanced. `--non-default` shows only settings that differ from the default. The default comes from a per-major table added to gosmo (`sys.configurations` has none, R11) |
| `server config get <name\|alias> [<name\|alias> …]` | One or more settings, plus related context (e.g. CPUs and NUMA nodes for MAXDOP, physical memory for memory settings) |
| `server config set <name\|alias> <value> [<name\|alias> <value> …] [--accept-risk] [--override]` | Change one or more settings (`--allow admin`) through `gosmo.Server.ApplyConfiguration`: one batch that turns `show advanced options` on only if needed (decided server-side), runs every `sp_configure`, one `RECONFIGURE`, and restores `show advanced options`. Reports before/after (re-read from `sys.configurations`) and whether a restart is required (running ≠ configured after the batch) |
| `server config reset <name\|alias> […]` | Set settings back to their default (`--allow admin`) |
| `server memory` | Memory overview: physical and available memory, min/max server memory, committed and target memory, page life expectancy, pending memory grants, top memory clerks (gosmo `MemoryStats`, `MemoryClerks`, `PerformanceCounters`) |
| `server volumes [--unit …]` | Disks and mount points that hold database files: total, free, free %, and the number of files per volume (gosmo `FileVolumes` — `DiskVolumes` has no file count, phase 4 P4-2) |
| `server traceflag list` / `on <flag>...` / `off <flag>...` | Trace flags (`DBCC TRACESTATUS/TRACEON/TRACEOFF`, new in gosmo). `on`/`off` need `--allow admin` and are always global (`-1`): there is no `--global`, a session flag would die with the command's connection (D6) |
| `server log [--lines 100] [--search <text>]... [--since <YYYY-MM-DD[THH:MM[:SS]]>] [--archive <n>] [--agent]` | SQL Server error log (gosmo `ReadLog` with `LogSearch`); `--agent` reads the SQL Server Agent log |

**Setting aliases.** Agents can use short aliases instead of the full `sp_configure` names. This avoids quoting names that contain spaces and brackets. Full names always work too.

| Alias | `sp_configure` name | Unit |
|-------|---------------------|------|
| `maxdop` | `max degree of parallelism` | count |
| `cost-threshold` | `cost threshold for parallelism` | cost units |
| `max-memory` | `max server memory (MB)` | MB |
| `min-memory` | `min server memory (MB)` | MB |
| `adhoc` | `optimize for ad hoc workloads` | 0/1 |
| `backup-compression` | `backup compression default` | 0/1 |
| `backup-checksum` | `backup checksum default` | 0/1 |
| `remote-dac` | `remote admin connections` | 0/1 |
| `clr` | `clr enabled` | 0/1 |
| `xp-cmdshell` | `xp_cmdshell` | 0/1 |
| `contained-auth` | `contained database authentication` | 0/1 |
| `blocked-threshold` | `blocked process threshold (s)` | seconds |

Value rules for `server config set`:

- Values without a unit use the setting's native unit (MB for memory). An option whose name ends `(MB)` also accepts `MB`, `GB` and `TB` suffixes (`56GB` → `57344`), one ending `(KB)` `KB` and `MB`; the result must be whole (`1.3MB` is exit 2). The output always shows the converted value (`requested`).
- Values outside the setting's min/max are a usage error (exit 2).
- Obviously risky values are blocked (exit 7, `BLOCKED`) unless `--accept-risk` is given: `max-memory` or `min-memory` above physical memory, `max-memory` below `min-memory`, `maxdop` above the logical CPUs, and any of these when the figure cannot be read. A value equal to the default never is. `--accept-risk` only lifts gossms's own check; `--override` is what SQL Server's `RECONFIGURE WITH OVERRIDE` needs.
- `--dry-run` prints gosmo's batch exactly as it would run (captured under `gosmo.WithScript`) and the planned change, without running anything. The batch is the same whatever the server's state; the planned-change table says whether the `show advanced options` step will fire, read from `sys.configurations` at dry-run time.

### 4.3 `db`: databases

| Command | Description |
|---------|-------------|
| `db list [--include-system] [--state online\|offline\|…]` | Databases with state, size, recovery model, compatibility level |
| `db info <name>` | Properties (Database Properties dialog) |
| `db files <name>\|--all [--type data\|log\|filestream\|fulltext] [--filegroup <name>] [--unit kb\|mb\|gb] [--sort-by name\|size\|used\|free-pct] [--max-free-pct <n>]` | Per file: logical name, type, filegroup, physical path, size, used, free, free %, autogrowth, max size, and free space on the volume that holds the file. `--max-free-pct 10` shows only files with at most 10 % free space |
| `db space <name>\|--all [--unit …]` | Database-level usage (like `sp_spaceused`): total size, data and log size, reserved, data, index, unused and unallocated space |
| `db log-usage <name>\|--all [--unit …]` | Transaction log size, used %, `log_reuse_wait_desc` (why the log can't be truncated) and the number of VLFs, all and active (`sys.dm_db_log_info`; new in gosmo, R12). Exactly one of `<name>` or `--all` (phase 4 D6) |
| `db table-sizes <name> [--top 20] [--schema …] [--sort-by reserved\|rows\|data\|index\|unused] [--unit …]` | Largest tables: rows, reserved, data, index and unused space (SSMS report "Disk Usage by Top Tables") |
| `db create <name> [--collation …] [--recovery …] [--compat <n>]` | `--allow admin` (phase 8 D9: CREATE DATABASE is `admin` to the classifier) |
| `db drop <name> --confirm <name>` | `--allow admin` |
| `db set <name> --option <key=value>` | e.g. `RECOVERY=SIMPLE`, `READ_ONLY=ON`, `COMPATIBILITY_LEVEL=160`, `QUERY_STORE=ON`. Repeatable |
| `db scoped-config list <name>` | Database scoped configuration (`sys.database_scoped_configurations`) |
| `db scoped-config get <name> <setting>` | One setting, e.g. `MAXDOP`, `LEGACY_CARDINALITY_ESTIMATION`, `PARAMETER_SNIFFING` |
| `db scoped-config set <name> <setting> <value> [--for-secondary]` | `ALTER DATABASE SCOPED CONFIGURATION` (`--allow admin`) |

**Size and usage rules** (all `db files/space/log-usage/table-sizes` and `server volumes` commands):

- All are `read` commands. They work against SQL Server on Linux (including containers) and on Windows, from any client OS. Physical paths are shown exactly as SQL Server reports them (`/var/opt/mssql/data/…` or `D:\Data\…`).
- The default unit is MB with two decimals. `--unit kb|mb|gb` (lower-case: the parser's enums are case-sensitive, phase 4 D5) changes it for the whole output; `kb` prints integers.
- `db files`, `db space` and `db log-usage` take exactly one of `<name>` or `--all`; neither or both is a usage error (phase 4 D6).
- Column names always carry the unit (`size_mb`, `used_gb`, …), so the agent never has to guess.
- Autogrowth is shown as `64 MB` or `10 %`. Max size is shown as a number or `unlimited`.
- `--all` adds a `database` column and covers every database the login can access. Databases that can't be read (offline, no permission) are listed under `WARNINGS`, not silently skipped; `db files` also lists their files from `sys.master_files` with the inside-only columns NULL (phase 4 D3).
- Warnings report facts only, for example: a file with less than 10 % free space, a file close to its max size, percentage autogrowth, a volume with less than 10 % free, or a log that can't be truncated (`log_reuse_wait_desc` other than `NOTHING`).

### 4.4 `object`: Object Explorer

| Command | Description |
|---------|-------------|
| `object list [--type …] [--schema …] [--name <pattern>]` | List objects. `--type`: `table,view,procedure,function,trigger,synonym,sequence,type,schema` |
| `object describe <schema.name>` | Columns, data types, nullability, defaults, PK/FK, indexes, constraints, row count |
| `object definition <schema.name>` | Source code of a view, procedure, function or trigger |
| `object dependencies <schema.name> [--direction uses\|used-by\|both] [--depth n]` | View Dependencies |
| `object search <text> [--in name\|definition\|column]` | Find objects or columns by name or by text in their code. `name` is gosmo `Database.Search`; `definition` and `column` are new in gosmo, with the same case-insensitive `LOWER(…) LIKE LOWER(@p)` + `ESCAPE` rule (`docs/db-rules.md` § T-SQL and filters) |
| `object indexes <schema.table>` | Indexes with key and included columns, fragmentation and usage |
| `object stats <schema.table>` | Statistics and last update |

```bash
gossms object list -p dev --type table --schema Sales
gossms object describe -p dev Sales.SalesOrderHeader -o json
gossms object search -p dev "CustomerID" --in column
```

### 4.5 `script`: Generate Scripts / Script As

| Command | Description |
|---------|-------------|
| `script object <schema.name> [--as create\|alter\|drop\|drop-and-create\|select\|insert\|update\|delete\|execute] [--script-file <path>]` | Script As…: each verb only where Object Explorer offers it (exit 2 otherwise, listing the kind's verbs) |
| `script db <name> [--as create\|drop\|drop-and-create] [--script-file <path>]` | Script Database As (the database's own CREATE, not the Generate Scripts wizard) |

There are no script options: the output is exactly Script As, gosmo's default `ScriptOptions` (phase 6 D5, D6). `create-or-alter` and `--include-indexes/--include-triggers/--include-fk` were dropped: gosmo has no CREATE OR ALTER verb and `ScriptTable` always emits indexes, FKs and checks (P6-6). `--script-file` writes the script as the TUI's File… does; the global `--out-file` is for rows. The Generate Scripts wizard (`--objects`, `--include-permissions`) is deferred (D7, `docs/open-threads.md`). `--target-version` and `--include-data` were dropped: gosmo's `Scripter` has neither (R13); data is `data export`.

### 4.6 `query`: query editor

| Command | Description |
|---------|-------------|
| `query run` | Run T-SQL and return result sets and messages |
| `query parse` | Syntax check only (`SET PARSEONLY ON`). Nothing is executed |
| `query classify` | Report the operation class (`read/write/ddl/admin`) the safety model assigns to each statement and the batch. Needs no connection |

`query run` uses one `query.Session` for the whole script (as a query window does), so temp tables, `SET` options and `USE` carry across its `GO` batches. Nothing carries across calls.

**SQL input** (exactly one is required):

| Option | Description |
|--------|-------------|
| `--sql "<text>"` / `-Q` | Inline T-SQL (fine for short queries) |
| `--file <path>` / `-i` | T-SQL file. `GO` batch separators are supported |
| `--file -` | Read T-SQL from stdin (**recommended for agents**: no escaping) |
| `--sql-base64 <b64>` | UTF-8 T-SQL as Base64. Avoids every shell-quoting problem |

Additional options: `--batch-separator GO`, `--stop-on-error` (default) / `--continue-on-error`, `--param name=value:type` (runs as a parameterized query through `sp_executesql`), `--statistics io,time`.

```bash
# Simple read
gossms query run -p dev -Q "SELECT TOP 5 name, create_date FROM sys.tables ORDER BY create_date DESC"

# From stdin (safe quoting), bash/zsh
cat fix.sql | gossms query run -p dev --file - --allow write --transaction rollback

# Same in PowerShell
Get-Content fix.sql -Raw | gossms query run -p dev --file - --allow write --transaction rollback

# Parameterized
gossms query run -p dev -Q "SELECT * FROM Sales.Customer WHERE CustomerID = @id" --param id=42:int
```

### 4.7 `plan`: execution plans

| Command | Description |
|---------|-------------|
| `plan estimated --sql …\|--file …\|--sql-base64 …` | Estimated plan (`SET SHOWPLAN_XML ON`). The query is not run; class `read` whatever the script does. No `--param` (D3) |
| `plan actual --sql …\|--file …\|--sql-base64 …` | Actual plan: `query run` with the plan captured, so its whole gate applies (`--allow`, `--transaction`, `--max-rows-affected`, `--param`, `--dry-run`). Rows are counted, not returned (set `result_sets`) (D2) |
| `plan cached <plan_handle> [--statement-start n] [--statement-end n]` | One plan from the plan cache (`sys.dm_exec_text_query_plan`), addressed by `monitor top-queries`' `plan_handle`, `statement_start` and `statement_end`; the formats and `--plan-file` as `plan estimated`. Ranking is `monitor top-queries` (phase 7 D4) |

`--plan-format summary|tree|xml`, default `summary`: sets `statements`, `operators` (the `--top 10` by own estimated cost across statements, with `misestimate`), `missing_indexes` and `warnings`; `tree` is every operator in preorder with `depth` and `parent`; `xml` is the plan document as a verbatim `planXml` field. `--plan-file <path>` writes the `.sqlplan` as Save Execution Plan As does (D4).

### 4.8 `data`: table data

| Command | Description |
|---------|-------------|
| `data top <schema.table> [--rows 100] [--columns a,b] [--where "<expr>"] [--order-by …]` | Select Top N Rows |
| `data count <schema.table> [--where …]` | Row count |
| `data sample <schema.table> [--rows 100] [--columns …] [--where …]` | Random sample: `TOP (n) … ORDER BY NEWID()` (a full scan; no TABLESAMPLE, D8) |
| `data profile <schema.table> [--columns …] [--where …] [--top-values n]` | Per column: rows, nulls, null %, distinct, min/max, min/max length; `--top-values` adds set `top_values` |
| `data export <schema.table> [--columns …] [--where …] [--order-by …] \| --sql …\|--file …\|--sql-base64 … --out-file <path>` | Export, streamed (no row cap); `--out-file` required, the format from its extension as for the global option — no `--format` (D10). CSV is the Results To File writer (§5.2) |
| `data import <schema.table> --in-file <path\|-> [--in-format csv\|tsv\|jsonl] [--truncate-first --confirm <schema.table>] [--no-check-constraints] [--fire-triggers] [--tablock] [--dry-run]` | Bulk copy, one all-or-nothing batch, KEEPNULLS (`--allow write`; `--truncate-first` needs `--allow ddl`). `.csv`/`.tsv`/`.jsonl` only, no `.json`; `--in-format` names stdin's format (D11) |

### 4.9 `security`

| Command | Description |
|---------|-------------|
| `security logins list` / `users list [--database …]` / `roles list` | List principals |
| `security permissions <principal> [--database …]` | Effective permissions |
| `security role-members <role>` | Role membership |
| `security grant\|revoke\|deny <permission> --on <object> --to <principal>` | `--allow admin` |

### 4.10 `job`: SQL Server Agent

| Command | Description |
|---------|-------------|
| `job list [--enabled] [--failed-since <iso>]` | Jobs with last outcome and next run |
| `job show <name>` | Steps, schedules, notifications |
| `job history <name> [--top 20]` | Run history, including step messages |
| `job start <name> [--step <n>]` / `job stop <name>` | `--allow admin` |

### 4.11 `monitor`: Activity Monitor and DMVs

| Command | Description |
|---------|-------------|
| `monitor sessions [--active] [--session-database …] [--login …] [--program …] [--include-system]` | `sys.dm_exec_sessions` / `requests` / `connections` through gosmo `ActiveSessions`; an idle session's text is its last batch (`-d` is the connection's database, so the filter is `--session-database`) |
| `monitor blocking` | Blocking chains (head blocker → blocked, depth first), a cycle reported once with `cycle` 1, including SQL text |
| `monitor waits [--top 20] [--interval <sec>] [--all]` | Wait statistics since start, or the difference over `--interval` (1–300 s); benign waits out unless `--all`; sets `waits` and `categories`. No `--reset-baseline` (phase 7 D3) |
| `monitor top-queries [--by cpu\|reads\|writes\|duration\|executions] [--avg] [--top 20] [--plan-database …]` | Costliest cached statements (`sys.dm_exec_query_stats`), with the `plan_handle` and offsets `plan cached` takes |
| `monitor querystore [--report top\|regressed\|variation\|forced\|waits] [--regressed] [--metric …] [--stat …] [--since …] [--until …] [--top 25] [--min-exec n] [--min-regression-pct n] [--wait-category …]` | Query Store reports for the connection's database (`-d`); default window the last 24 h, zoneless times server local; `--regressed` = `--report regressed` |
| `monitor deadlocks [--since <t>] [--until <t>] [--top 20] [--deadlock-format summary\|xml] [--deadlock-file <dir>]` | From the `system_health` XEvent session; `--since`/`--until` UTC (no zone = UTC); `.xdl` files with `--deadlock-file` |
| `monitor kill <spid> --confirm <spid>` | `--allow admin`; the session read first and shown; own connections → 2, system session → 7, gone → 8; `ended` after |

### 4.12 `maint`: maintenance

| Command | Description |
|---------|-------------|
| `maint backup <db> --to <path> [--type full\|diff\|log] [--compression] [--copy-only]` | Backup |
| `maint restore <db> --from <path> [--with-move …] [--replace] [--verify-only]` | Restore (`--confirm <db>` when `--replace` is used) |
| `maint backup-history <db\|--all> [--last] [--type full\|diff\|log]` | Backup history from `msdb`. `--last` shows only the most recent backup per database and type |
| `maint index-fragmentation [--database …] [--min-percent 10]` | Fragmentation report |
| `maint index-rebuild <schema.table> [--index …] [--online]` | Rebuild or reorganize |
| `maint update-stats <schema.table\|--all> [--fullscan]` | Update statistics |
| `maint checkdb <db> [--physical-only]` | `DBCC CHECKDB` |

### 4.13 `xevent`

| Command | Description |
|---------|-------------|
| `xevent sessions` | List XEvent sessions |
| `xevent capture --template <standard\|tsql\|batches\|statements\|waits\|connections\|deadlocks\|errors> [--duration 30] [--max-events 1000] [--session-database …] [--login …] [--program …] [--min-duration-ms n]` | `--allow admin`. A short-lived ring_buffer session, dropped on every exit path; capture for a fixed time, then return the events (built for agents: no live UI). Server-side filters only: no `--filter` expression (phase 7 D8, D9) |

### 4.14 Meta commands

| Command | Description |
|---------|-------------|
| `agent guide` / `agent schema` / `agent context` | Self-description for AI agents. See §8 |
| `help [--output json]` | Human help. `--output json` is an alias for `agent schema` |
| `<group> <command> --help` | Help for one command, with examples |
| `version` | App version and CLI schema version |

---

## 5. Output format

### 5.1 `text` (default): compact and self-describing

A short header block in `KEY: value` form, then the result sets as aligned tables, then messages. Section markers are fixed so an agent can split the output reliably. Column alignment uses display width, never byte length: `displaywidth.String`, the function `core.DisplayWidth` wraps — `tuikit/core` imports tcell, so the CLI cannot (§14.1).

Row limits are applied by the CLI's own row sink, not by the executor's `WithMaxRows`: the whole script always runs, rows past `--max-rows` are counted and discarded, so `18,432` below is exact (R6). A server-side cap (stop reading and cancel) would skip later statements and is never used.

```text
STATUS: OK
COMMAND: query run
SERVER: localhost\SQL2022
DATABASE: AdventureWorks
DURATION_MS: 38
RESULT_SETS: 1

=== RESULT 1 (5 rows, truncated: no) ===
name                  | create_date
----------------------+------------------------
SalesOrderHeader      | 2026-03-14T09:12:44.000
Customer              | 2026-03-14T09:12:41.000
Person                | 2026-03-14T09:12:40.000
Product               | 2026-03-14T09:12:39.000
Store                 | 2026-03-14T09:12:38.000

=== MESSAGES ===
(5 rows affected)
```

Truncation is always explicit:

```text
=== RESULT 1 (200 of 18,432 rows, truncated: yes) ===
...
NOTE: Output truncated by --max-rows 200. Use --max-rows 0 or --out-file to get all rows.
```

### 5.2 `json`: stable envelope

The structure is identical for every command. `schemaVersion` changes only when the structure breaks compatibility.

```json
{
  "schemaVersion": "1.0",
  "status": "ok",
  "command": "query run",
  "server": "localhost\\SQL2022",
  "database": "AdventureWorks",
  "durationMs": 38,
  "resultSets": [
    {
      "columns": [
        { "name": "name", "type": "sysname", "nullable": false },
        { "name": "create_date", "type": "datetime", "nullable": false }
      ],
      "rows": [
        ["SalesOrderHeader", "2026-03-14T09:12:44.000"]
      ],
      "rowCount": 5,
      "totalRowCount": 5,
      "truncated": false,
      "rowsAffected": null
    }
  ],
  "messages": [ { "type": "info", "text": "(5 rows affected)" } ],
  "warnings": [],
  "errors": []
}
```

- `jsonl`: one JSON object per row (good for streaming large results), followed by a final `{"_summary": {...}}` line.
- `csv` / `tsv`: RFC 4180, written by the same writer as the TUI's Results To File (extracted from `internal/tui/query_panel_export.go`). NULL follows the settled rule (`docs/decisions.md` § SQL NULL in results): NULL is an empty field, an empty string is `""`. Result sets are separated by a blank line. Only the first result set is written unless `--all-result-sets` is given.
- `markdown`: GitHub tables, handy when the agent shows the result to a human.

A command whose answer is a record rather than a table (`version`, `profile show`, `server info`, `db info`) fills an ordered **`data`** object instead of `resultSets` (field order is fixed per command). In `text` it prints as `KEY: value` lines after the header block. Settled 2026-10-28 (phase 0 D3).

### 5.3 Value formatting rules (all formats)

**The cell text is the grid's text.** The executor already renders every value the way the SSMS grid does (`internal/query/executor.go`, `appendValue`); the CLI prints that text and never formats a value a second way (R7). JSON adds types on top, from the column's declared type.

| SQL type | text / csv / markdown | JSON |
|----------|-----------------------|------|
| NULL | `--null-text` (default `NULL`); CSV: empty field | `null` |
| date / time / datetime / datetime2 / datetimeoffset | The column's own layout: `2026-10-28`, `2026-10-28 13:16:23.123`, `2026-10-28 13:16:23.1234567 +01:00` (only the parts the type stores) | same string |
| int / bigint / smallint / tinyint | digits | number |
| real / float | shortest round-trip form | number |
| decimal / numeric / money | `.` as decimal separator, no thousands separators, the column's scale | **string** (a JSON number would lose precision in most parsers) |
| bit | `1` / `0` | `true` / `false` |
| binary / varbinary | `0x` + uppercase hex, truncated to `--max-col-width` | same string |
| uniqueidentifier | uppercase canonical form, as SSMS shows it | same string |
| xml / large text | Truncated with a `…[+12,345 chars]` marker | same string, same marker |

Go's formatting has no locale, and the executor uses none, so output is the same on every machine.

### 5.4 Errors

Errors go to **stdout in the chosen format** (so the agent can parse them). A one-line summary also goes to **stderr**.

```text
STATUS: ERROR
COMMAND: query run
ERROR_CODE: SQL_ERROR
SQL_ERROR_NUMBER: 208
SEVERITY: 16
STATE: 1
LINE: 1
MESSAGE: Invalid object name 'Sales.Customers'.
HINT: Did you mean 'Sales.Customer'? Run: gossms object search "Customers" --in name
```

```json
{
  "schemaVersion": "1.0",
  "status": "error",
  "errors": [
    {
      "code": "SQL_ERROR",
      "sqlErrorNumber": 208,
      "severity": 16,
      "state": 1,
      "line": 1,
      "message": "Invalid object name 'Sales.Customers'.",
      "hint": "Did you mean 'Sales.Customer'?"
    }
  ]
}
```

**Hints are what make this work well for agents.** Wherever possible, suggest the next command to try: closest object names, missing permission, the `--allow` level needed, and so on.

The SQL error fields come from the driver's `mssql.Error` (number, class, state, line number, procedure). The executor keeps them structured on its error messages (R7); today it keeps only the text. Error classification reuses gosmo's predicates (`gosmo.AsSQLError`, `ClassifyRefusal`, `IsPermissionDenied`, `ErrNotFound`, `ErrUnsupported…`) and `db.ConnectionError`, never message-text matching.

---

## 6. Safety model

### 6.1 Operation classes

Every command, and every T-SQL batch sent through `query run`, is put into one class:

| Class | Examples | Required flag |
|-------|----------|---------------|
| `read` | `SELECT`, metadata, DMVs, `plan estimated` | none (default) |
| `write` | `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `data import` | `--allow write` |
| `ddl` | `CREATE`, `ALTER`, `DROP` of objects, `TRUNCATE` | `--allow ddl` |
| `admin` | backup and restore, `KILL`, `sp_configure`, security, Agent jobs, `DROP DATABASE`, `SHUTDOWN` | `--allow admin` |

Classify T-SQL with gossms's one lexer, `internal/tuikit/sqltext` (settled: `docs/decisions.md` § T-SQL lexing): `SplitBatches` for `GO`, `sqltext.Next` to skip comments, literals and quoted identifiers, and the statement-verb tables (`StatementCutter`, `IsDMLLeader`) to split statements. Each statement is classified by its leading keywords. **It fails closed:** anything it does not recognize, including dynamic SQL (`EXEC(@sql)`, `sp_executesql`) and unknown procedure calls, counts as `write` unless it is on the built-in read allow-list (system catalog procedures such as `sp_help`, `sp_helptext`, `sp_spaceused`, `sp_who2`, `sp_WhoIsActive`). A few rules worth stating:

- `SELECT … INTO` is `ddl` (it creates a table); a CTE is classified by the statement after it; `MERGE`, `INSERT … EXEC` and `OUTPUT … INTO` are `write`.
- `USE`, `SET` (session options), `DECLARE`, `PRINT`, `IF`/`BEGIN`/`END`, `RAISERROR`, `THROW` are `read`.
- `BEGIN TRAN`/`COMMIT`/`ROLLBACK`/`SAVE TRAN` are `write`.
- `KILL`, `SHUTDOWN`, `BACKUP`, `RESTORE`, `DBCC` (except a read allow-list: `SHOW_STATISTICS`, `SQLPERF`, `TRACESTATUS`, `OPENTRAN`, `INPUTBUFFER`), `ALTER DATABASE`, `ALTER SERVER`, `sp_configure`, `RECONFIGURE`, security DDL (`CREATE/ALTER/DROP LOGIN|USER|ROLE`, `GRANT`, `DENY`, `REVOKE`, `ALTER AUTHORIZATION`) and msdb Agent procedures are `admin`.
- `SET PARSEONLY`, `SET NOEXEC`, `SET SHOWPLAN_*` are refused in `query run` (the CLI owns those modes through `query parse` and `plan`).

> The classifier is a guardrail, not the security boundary. The real boundary is SQL Server permissions: read-only agent profiles should use a login that only has e.g. `db_datareader`, `VIEW DEFINITION` and `VIEW SERVER STATE`.

If the batch is above the permitted class, it is **refused** with exit code 7:

```text
STATUS: BLOCKED
ERROR_CODE: OPERATION_NOT_ALLOWED
DETECTED_CLASS: write
ALLOWED_CLASS: read
STATEMENTS: UPDATE Sales.Customer (line 1)
HINT: Re-run with --allow write. Consider --dry-run or --transaction rollback first.
```

### 6.2 Profile-level ceilings

A profile can set a maximum class that no command-line flag can raise. This is the new optional `max_allow` field on a saved connection in `config.json`, set in the TUI's **Profile…** dialog together with the profile name (§4.1). If it is missing, the ceiling is `admin` (only `--allow` applies; author's decision). For example, a `prod-ro` profile has `max_allow: read`. This gives a hard guardrail even if the agent passes `--allow admin`.

An ad-hoc connection (no `-p`) has no ceiling beyond `--allow`. A profile used with `--server`/`--user` overrides keeps its ceiling.

### 6.3 Destructive confirmation

Drop database, restore with `--replace`, kill and similar operations need `--confirm <exact target name>`. The agent has to state the target deliberately, which catches wrong-object mistakes.

### 6.4 Additional safeguards

- `--dry-run` on every command that changes something. It prints the exact T-SQL that would run. For gosmo-backed commands this is `gosmo.WithScript`: every write is recorded instead of executed, secrets appear as gosmo's placeholders, reads still run (so the output reflects the server's real state). For `query run` it prints the classified statements and runs nothing.
- `--transaction rollback` to measure the effect of DML (`rowsAffected`) without keeping the change. It wraps the whole script (all `GO` batches, one Session) in `BEGIN TRAN … ROLLBACK` with `SET XACT_ABORT ON`. It is refused (exit 7) when the script itself contains transaction control (`BEGIN TRAN`, `COMMIT`, `ROLLBACK`, `SAVE TRAN`) or a statement SQL Server refuses inside a user transaction (`ALTER DATABASE`, `BACKUP`, `RESTORE`, `CREATE/ALTER/DROP DATABASE`, `CREATE/ALTER/DROP FULLTEXT CATALOG|INDEX`, `RECONFIGURE`, …): a silent partial commit is worse than a refusal.
- `--max-rows-affected <n>`: implies a transaction like `--transaction rollback`; commits only if the total rows affected (structured count, R7) stays at or below `n`, otherwise rolls back and exits 7.
- Optional audit log (`audit.jsonl` beside `config.json`, i.e. `os.UserConfigDir()/gossms/`, owner-only `0600` like `gossms.log`; R15). Fields: timestamp, profile, server, login, command, class, SQL SHA-256, outcome, exit code. Never the SQL text or any secret. Writes take `fileutil.WithLock` so parallel CLI calls do not interleave lines.

---

## 7. Exit codes

| Code | Name | Meaning |
|------|------|---------|
| 0 | `OK` | Success (output may still be truncated; check `truncated`) |
| 1 | `GENERAL_ERROR` | Unexpected or internal error |
| 2 | `USAGE_ERROR` | Invalid or unknown arguments |
| 3 | `CONNECTION_FAILED` | Server unreachable, TLS or network error |
| 4 | `AUTH_FAILED` | Login failed or credentials are missing |
| 5 | `SQL_ERROR` | The server returned an error (severity ≥ 11) |
| 6 | `TIMEOUT` | Command or connect timeout |
| 7 | `BLOCKED` | Refused by the safety model or a missing `--confirm` |
| 8 | `NOT_FOUND` | Profile, database or object does not exist |
| 9 | `PERMISSION_DENIED` | The login lacks the required permission |
| 10 | `PARTIAL` | Some batches succeeded and some failed (`--continue-on-error`) |
| 11 | `UNSUPPORTED` | The server's version or edition does not support the operation (gosmo `ErrUnsupportedVersion` / `ErrUnsupported`; e.g. a feature not on Azure SQL MI) |
| 130 | `CANCELLED` | Ctrl+C or SIGINT |

---

## 8. Agent self-description: `gossms agent`

The `agent` command group is how gossms explains itself to an AI agent. An agent (or the person setting it up) runs it first, so it doesn't have to learn the tool by trial and error.

| Command | Returns | Typical use |
|---------|---------|-------------|
| `agent guide` | A usage manual written for an LLM: what gossms does, the rules, call patterns, output contract, exit codes, command index and recipes | The agent runs it at the start of a task, or you paste `--brief` output into its system prompt |
| `agent schema` | JSON catalog of every command, argument, option, type, default, operation class and example (same as `help --output json`) | Build tool definitions, validate calls before running them |
| `agent context -p <profile>` | Live facts: server version and edition, databases, current login, effective `--allow` ceiling, missing permissions, available profiles | Ground the agent in the real environment before it acts |

### 8.1 `agent guide` options

| Option | Description |
|--------|-------------|
| `--brief` | Condensed version for system prompts (target: about 1,500 tokens) |
| `--topic <name>` | One section only: `overview`, `rules`, `output`, `exit-codes`, `recipes`, or any command group (`query`, `object`, `monitor`, …) for full detail on that group |
| `-o <markdown\|text\|json>` | The global `--output`; default `markdown` for this command (phase 3 D6). The guide is printed outside the envelope; `json` returns the envelope with the same sections as `data.sections[]{id, title, body}` |
| `-p <profile>` | Adds a **"Your environment"** section (the `agent context` output) |

### 8.2 Guide structure (fixed section order)

1. **What gossms is.** One paragraph.
2. **Rules you must follow.** Safety levels, approval before changes, no passwords on the command line, never start the TUI.
3. **How to call it.** Syntax, profiles, SQL via stdin.
4. **Output contract.** Text and JSON layout, truncation, errors and hints.
5. **Exit codes.**
6. **Command index.** Group → command → one-line summary → operation class. Generated from the command tree.
7. **Recipes.** Explore a schema, diagnose a slow query, investigate blocking, make a safe data fix.
8. **Your environment.** Only with `-p`.

### 8.3 Example: `gossms agent guide --brief -p dev`

Illustrative output (versions and environment values are made up):

````markdown
# gossms – guide for AI agents (brief)
gossms 1.4.0 · CLI schema 1.0

gossms manages Microsoft SQL Server from the command line: run T-SQL, explore
schemas, read execution plans, monitor activity and perform maintenance.
Every call is stateless and non-interactive, prints results on stdout and
returns an exit code.

## Rules
1. Always pass `-p <profile>`. Never put passwords on the command line.
2. The default is read-only. Changing data or schema needs `--allow write|ddl|admin`
   AND explicit approval from the user.
3. Before any change: run with `--dry-run`, then `--transaction rollback`, then for real.
4. Send T-SQL through stdin (`--file -`) to avoid quoting problems.
5. Results are capped at 200 rows. Check `truncated`; use `--out-file` for more.
6. Never run `gossms` without a command: that starts the interactive TUI.

## Call pattern
gossms <group> <command> [args] -p <profile> [-o text|json]

## Commands (selection)
| Command          | Class    | Returns                                        |
|------------------|----------|------------------------------------------------|
| object list      | read     | Objects filtered by type, schema, name         |
| object describe  | read     | Columns, keys, indexes, constraints            |
| object search    | read     | Objects and columns matching a text            |
| query run        | from SQL | Result sets and messages                       |
| plan estimated   | read     | Plan summary: costly operators, missing indexes|
| monitor blocking | read     | Blocking chains with SQL text                  |
Full list: `gossms agent schema` · Details: `gossms agent guide --topic <group>`

## Exit codes
0 ok · 2 usage · 3 connection · 4 auth · 5 SQL error · 6 timeout ·
7 blocked by safety · 8 not found · 9 permission denied · 10 partial

## Your environment (profile: dev)
Server:    localhost\SQL2022 · SQL Server 2022 Developer Edition
Login:     CORP\svc-agent · allowed up to: write (profile ceiling)
Databases: AdventureWorks, WideWorldImporters, ReportingDB
Missing:   VIEW SERVER STATE → monitor commands will fail with exit 9
````

### 8.4 `agent schema`: machine-readable catalog

An agent can load this once and build correct calls without trial and error:

```json
{
  "schemaVersion": "1.0",
  "commands": [
    {
      "path": "object describe",
      "summary": "Describe a table or view: columns, keys, indexes, constraints.",
      "operationClass": "read",
      "arguments": [
        { "name": "object", "type": "string", "required": true, "format": "schema.name" }
      ],
      "options": [
        { "name": "--include-row-count", "type": "bool", "default": true }
      ],
      "examples": [
        "gossms object describe -p dev Sales.Customer",
        "gossms object describe -p dev Sales.Customer -o json"
      ]
    }
  ]
}
```

### 8.5 Requirements

- **Always current.** The command index, the schema and the per-group topics are derived from the actual command definitions: each command's summary, examples and operation class. Only the rules and recipes are hand-written. This way the guide can never drift from the real CLI.
- **Version it.** The first line carries the gossms version and `schemaVersion`, so an agent can tell whether a cached guide is stale.
- **Point to it.** The root `--help`, the "options but no command" usage error and the TUI's "no terminal" error all mention `gossms agent guide`.
- Each `--help` shows **two or three real examples**. Agents learn more from examples than from syntax diagrams.
- Keep summaries to one sentence that says *what the command returns*.
- Keep old flags working as deprecated aliases for at least one release.

### 8.6 Suggested snippet for the agent's system prompt

Because the tool describes itself, the system prompt can stay short:

```text
You can access SQL Server through the `gossms` CLI.
Before your first gossms call in a task, run `gossms agent guide --brief -p <profile>`
and follow its rules. Never change data or schema without the user's approval.
```

---

## 9. End-to-end example: agent workflow

**Task:** "Why is the monthly sales report slow?"

```bash
# 1. Orient
gossms agent guide --brief -p dev
gossms object search -p dev "MonthlySales" --in name

# 2. Understand the code and the tables it uses
gossms object definition -p dev Reporting.usp_MonthlySales
gossms object dependencies -p dev Reporting.usp_MonthlySales --direction uses
gossms object describe -p dev Sales.SalesOrderDetail -o json

# 3. Look at the plan without running the query
gossms plan estimated -p dev -Q "EXEC Reporting.usp_MonthlySales @Year=2026, @Month=9"

# 4. Check the indexes and the runtime history
gossms object indexes -p dev Sales.SalesOrderDetail
gossms monitor querystore -p dev --regressed

# 5. Propose a fix and check it without applying it
gossms query run -p dev --file fix_index.sql --allow ddl --dry-run
```

---

## 10. Command examples by task

The example outputs are illustrative: server names and values are made up. Read-only checks use the profile `prod-ro`, changes use `dev`.

### 10.1 MAXDOP (server level)

**Get the current value.** The context block gives the agent the CPU layout it needs to judge the value:

```bash
gossms server config get maxdop -p prod-ro
```

```text
STATUS: OK
COMMAND: server config get
SERVER: sql-prod-01
DURATION_MS: 11
RESULT_SETS: 1

LOGICAL_CPUS: 16
NUMA_NODES: 2
LOGICAL_CPUS_PER_NUMA_NODE: 8

=== RESULT 1 (1 row, truncated: no) ===
name                      | alias  | configured | running | default | min | max   | dynamic | advanced
--------------------------+--------+------------+---------+---------+-----+-------+---------+---------
max degree of parallelism | maxdop | 0          | 0       | 0       | 0   | 32767 | 1       | 1
```

The context is the record's fields, printed before the set (phase 5 D2); a `bit` column is `1`/`0` as in every set.

**Preview a change, then apply it:**

```bash
gossms server config set maxdop 8 -p dev --allow admin --dry-run
```

```text
STATUS: OK
COMMAND: server config set
SERVER: sql-dev-01
DURATION_MS: 14
RESULT_SETS: 1

DRY_RUN: true

=== TSQL (30 lines) ===
DECLARE @show_advanced_enabled bit = 0;
DECLARE @show_advanced_value int = (SELECT CONVERT(int, value) FROM sys.configurations WHERE name = N'show advanced options');
IF EXISTS (SELECT 1 FROM sys.configurations WHERE name = N'show advanced options' AND value_in_use = 0)
   AND EXISTS (SELECT 1 FROM sys.configurations WHERE is_advanced = 1 AND name IN (N'max degree of parallelism'))
BEGIN
    EXEC sys.sp_configure N'show advanced options', 1;
    BEGIN TRY
        RECONFIGURE;
    END TRY
    BEGIN CATCH
        EXEC sys.sp_configure N'show advanced options', @show_advanced_value;
        THROW;
    END CATCH;
    SET @show_advanced_enabled = 1;
END;
EXEC sys.sp_configure N'max degree of parallelism', 8;
BEGIN TRY
    RECONFIGURE;
END TRY
BEGIN CATCH
    IF @show_advanced_enabled = 1
        EXEC sys.sp_configure N'show advanced options', @show_advanced_value;
    THROW;
END CATCH;
IF @show_advanced_enabled = 1
BEGIN
    EXEC sys.sp_configure N'show advanced options', @show_advanced_value;
    RECONFIGURE;
END;
GO

=== RESULT 1: planned_change (1 row, truncated: no) ===
name                      | current | new | dynamic | advanced | enables_show_advanced
--------------------------+---------+-----+---------+----------+----------------------
max degree of parallelism | 0       | 8   | 1       | 1        | 1
```

A dry run is `STATUS: OK` with `DRY_RUN: true`, not a status of its own (phase 5 D2); it still needs `--allow admin`.

The batch is `gosmo.Server.ApplyConfiguration`'s, captured under `WithScript`. It is the same on every server; `enables_show_advanced` says whether its first step will fire here (R10).

```bash
gossms server config set maxdop 8 -p dev --allow admin
```

```text
STATUS: OK
COMMAND: server config set
SERVER: sql-dev-01
DURATION_MS: 38
RESULT_SETS: 1

DRY_RUN: false

=== RESULT 1: change (1 row, truncated: no) ===
name                      | before | requested | after_configured | after_running | restart_required
--------------------------+--------+-----------+------------------+---------------+-----------------
max degree of parallelism | 0      | 8         | 8                | 8             | 0
```

**Reset to the default:**

```bash
gossms server config reset maxdop -p dev --allow admin
```

**Missing `--allow`.** Without `--allow admin` the change is refused and nothing runs:

```text
STATUS: BLOCKED
COMMAND: server config set
DURATION_MS: 1

DETECTED_CLASS: admin
ALLOWED_CLASS: read

ERROR_CODE: BLOCKED
MESSAGE: server config set is admin; the allowed class is read
HINT: Re-run with --allow admin if the change is intended.
```

It is refused before connecting, `--dry-run` included.

### 10.2 MAXDOP (database level)

```bash
# Get the database-scoped value (0 = use the server setting)
gossms db scoped-config get AdventureWorks MAXDOP -p prod-ro

# Set it for one database only
gossms db scoped-config set AdventureWorks MAXDOP 4 -p dev --allow admin --dry-run
# T-SQL: USE [AdventureWorks]; ALTER DATABASE SCOPED CONFIGURATION SET MAXDOP = 4;

# Different value for readable secondaries
gossms db scoped-config set AdventureWorks MAXDOP 2 --for-secondary -p dev --allow admin
```

### 10.3 Cost threshold for parallelism

```bash
gossms server config get cost-threshold -p prod-ro
gossms server config set cost-threshold 50 -p dev --allow admin --dry-run

# Both parallelism settings in one call
gossms server config get maxdop cost-threshold -p prod-ro -o json
```

### 10.4 Memory settings

**Overview:**

```bash
gossms server memory -p prod-ro
```

```text
STATUS: OK
COMMAND: server memory
SERVER: sql-prod-01
DURATION_MS: 24
RESULT_SETS: 2

=== RESULT 1: memory (1 row, truncated: no) ===
physical_mb | available_os_mb | max_server_memory_mb | min_server_memory_mb | committed_mb | target_mb | page_life_expectancy_s | memory_grants_pending
------------+-----------------+----------------------+----------------------+--------------+-----------+------------------------+----------------------
65536       | 1840            | 2147483647           | 0                    | 61210        | 62004     | 412                    | 0

=== RESULT 2: clerks (5 rows, truncated: no) ===
clerk                         | size_mb
------------------------------+---------
MEMORYCLERK_SQLBUFFERPOOL     | 52108.00
CACHESTORE_SQLCP              | 4210.25
CACHESTORE_OBJCP              | 1022.50
MEMORYCLERK_SQLQERESERVATIONS | 640.00
OBJECTSTORE_LOCK_MANAGER      | 312.13

=== WARNINGS ===
max server memory (MB) is at the default (2147483647 = unlimited).
Available OS memory is 1840 MB (2.81 % of physical memory, under 5 %).
```

The real `clerks` set is the top 10.

Warnings report facts only. The agent decides what to recommend.

**Get only the memory settings:**

```bash
gossms server config get max-memory min-memory -p prod-ro
```

**Set max server memory.** Units are converted, and the output confirms the value in MB:

```bash
gossms server config set max-memory 56GB -p dev --allow admin --dry-run
```

```text
STATUS: OK
COMMAND: server config set
SERVER: sql-dev-01
DURATION_MS: 15
RESULT_SETS: 1

DRY_RUN: true

=== TSQL (30 lines) ===
(the ApplyConfiguration batch, as in §10.1, with N'max server memory (MB)', 57344)

=== RESULT 1: planned_change (1 row, truncated: no) ===
name                   | current    | new   | dynamic | advanced | enables_show_advanced
-----------------------+------------+-------+---------+----------+----------------------
max server memory (MB) | 2147483647 | 57344 | 1       | 1        | 0

=== WARNINGS ===
New value is below currently committed memory (61210 MB). SQL Server will release memory after RECONFIGURE.
```

`enables_show_advanced` 0 because `show advanced options` is already on on this server; the batch is the same either way.

**Risky value.** It is blocked unless `--accept-risk` is given (`--force` is the global out-file flag, phase 5 D3):

```bash
gossms server config set max-memory 80GB -p dev --allow admin
```

```text
STATUS: BLOCKED
COMMAND: server config set
SERVER: sql-dev-01
DURATION_MS: 12

ERROR_CODE: BLOCKED
MESSAGE: max server memory (81920 MB) is above physical memory (65536 MB).
HINT: Choose another value, or re-run with --accept-risk if it is intended (it lifts only gossms's own checks).
```

**Set min server memory:**

```bash
gossms server config set min-memory 8GB -p dev --allow admin
```

### 10.5 Other server settings

```bash
# Everything that differs from the defaults (a quick configuration review)
gossms server config list --non-default -p prod-ro

# Search settings by name
gossms server config list --name backup -p prod-ro

# Optimize for ad hoc workloads
gossms server config set adhoc 1 -p dev --allow admin

# Backup compression and checksum by default
gossms server config set backup-compression 1 -p dev --allow admin
gossms server config set backup-checksum 1 -p dev --allow admin

# Remote dedicated admin connection (DAC)
gossms server config set remote-dac 1 -p dev --allow admin

# Blocked process report threshold (seconds)
gossms server config set blocked-threshold 20 -p dev --allow admin

# Full sp_configure name (quotes needed because of the spaces)
gossms server config get "cost threshold for parallelism" -p prod-ro
```

**A setting that needs a restart** is applied to the configured value only. The output says so:

```text
=== RESULT 1: change (1 row, truncated: no) ===
name            | before | requested | after_configured | after_running | restart_required
----------------+--------+-----------+------------------+---------------+-----------------
fill factor (%) | 0      | 90        | 90               | 0             | 1

=== WARNINGS ===
fill factor (%) is configured 90 but runs 0 until the SQL Server service restarts.
```

### 10.6 Trace flags

```bash
gossms server traceflag list -p prod-ro
gossms server traceflag on 7745 -p dev --allow admin --dry-run
# T-SQL: DBCC TRACEON(7745, -1) WITH NO_INFOMSGS
gossms server traceflag off 7745 -p dev --allow admin
```

Always global (`-1`): there is no `--global`, since a session flag would die with the command's connection (phase 5 D6). Trace flags set this way are lost at restart. The `on` output warns so and names the lasting form (`-T` in the service's settings, or `mssql-conf traceflag` on Linux). A startup-only flag (834) is not set and gets a warning.

### 10.7 Database settings

```bash
# Recovery model and compatibility level of all databases
gossms db list -p prod-ro

# Change compatibility level
gossms db set AdventureWorks --option COMPATIBILITY_LEVEL=160 -p dev --allow admin --dry-run

# Switch to the FULL recovery model
gossms db set AdventureWorks --option RECOVERY=FULL -p dev --allow admin

# Turn on Query Store
gossms db set AdventureWorks --option QUERY_STORE=ON -p dev --allow admin

# Several options in one call
gossms db set AdventureWorks --option AUTO_UPDATE_STATISTICS_ASYNC=ON --option PAGE_VERIFY=CHECKSUM -p dev --allow admin

# All database-scoped settings
gossms db scoped-config list AdventureWorks -p prod-ro

# tempdb file layout (number of files, sizes, growth)
gossms db files tempdb -p prod-ro
```

### 10.8 Database files: size and usage

**Files of one database:**

```bash
gossms db files AdventureWorks -p prod-ro
```

```text
STATUS: OK
COMMAND: db files
SERVER: sql-prod-01
DATABASE: AdventureWorks
DURATION_MS: 19
RESULT_SETS: 1

=== RESULT 1 (3 rows, truncated: no) ===
logical_name   | type | filegroup | physical_path                          | size_mb  | used_mb  | free_mb | free_pct | growth | max_size_mb | volume_free_pct
---------------+------+-----------+----------------------------------------+----------+----------+---------+----------+--------+-------------+----------------
AW_Data        | data | PRIMARY   | /var/opt/mssql/data/AW_Data.mdf        | 51200.00 | 48730.25 | 2469.75 | 4.82     | 512 MB | unlimited   | 18.40
AW_Data2       | data | ARCHIVE   | /var/opt/mssql/data/AW_Data2.ndf       | 20480.00 | 11020.50 | 9459.50 | 46.19    | 512 MB | 25600.00    | 18.40
AW_Log         | log  |           | /var/opt/mssql/log/AW_Log.ldf          | 8192.00  | 7610.40  | 581.60  | 7.10     | 10 %   | unlimited   | 41.75

=== WARNINGS ===
[AdventureWorks] file [AW_Data]: 4.82 % free inside the file (under 10 %).
[AdventureWorks] file [AW_Log]: 7.10 % free inside the file (under 10 %).
[AdventureWorks] file [AW_Log]: grows by 10 % (percentage autogrowth).
```

**Database-level summary:**

```bash
gossms db space AdventureWorks -p prod-ro
```

```text
=== RESULT 1 (1 row, truncated: no) ===
database       | total_mb | data_size_mb | data_used_mb | unallocated_mb | reserved_mb | data_mb  | index_mb | unused_mb | log_size_mb | log_used_mb | log_used_pct
---------------+----------+--------------+--------------+----------------+-------------+----------+----------+-----------+-------------+-------------+-------------
AdventureWorks | 79872.00 | 71680.00     | 59750.75     | 11929.25       | 59750.75    | 41210.00 | 17890.50 | 650.25    | 8192.00     | 7610.40     | 92.90
```

**All databases on the server, as JSON:**

```bash
gossms db space --all -p prod-ro -o json
```

**Only files that are almost full (at most 10 % free), across all databases:**

```bash
gossms db files --all --max-free-pct 10 --sort-by free-pct -p prod-ro
```

**Log files only, in GB:**

```bash
gossms db files --all --type log --unit gb -p prod-ro
```

**Transaction log usage, and why the log can't be truncated:**

```bash
gossms db log-usage --all -p prod-ro
```

```text
=== RESULT 1 (4 rows, truncated: no) ===
database           | log_size_mb | log_used_mb | log_used_pct | log_reuse_wait_desc | vlf_count | vlf_active
-------------------+-------------+-------------+--------------+---------------------+-----------+-----------
AdventureWorks     | 8192.00     | 7610.40     | 92.90        | LOG_BACKUP          | 212       | 198
WideWorldImporters | 2048.00     | 210.30      | 10.27        | NOTHING             | 16        | 2
ReportingDB        | 4096.00     | 3901.10     | 95.24        | ACTIVE_TRANSACTION  | 48        | 46
tempdb             | 1024.00     | 88.70       | 8.66         | NOTHING             | 8         | 1
```

**Largest tables in a database:**

```bash
gossms db table-sizes AdventureWorks --top 10 --sort-by reserved -p prod-ro
```

```text
=== RESULT 1 (10 rows, truncated: no) ===
schema | table              | rows       | reserved_mb | data_mb  | index_mb | unused_mb
-------+--------------------+------------+-------------+----------+----------+----------
Sales  | SalesOrderDetail   | 121317000  | 18450.25    | 12010.00 | 6290.50  | 149.75
Sales  | SalesOrderHeader   | 31465000   | 7120.00     | 4980.25  | 2100.00  | 39.75
...
```

**Free space on the disks that hold database files:**

```bash
gossms server volumes -p prod-ro
```

```text
=== RESULT 1 (2 rows, truncated: no) ===
volume             | total_gb | free_gb | free_pct | db_files
-------------------+----------+---------+----------+---------
/var/opt/mssql/data| 500.00   | 92.00   | 18.40    | 14
/var/opt/mssql/log | 200.00   | 83.50   | 41.75    | 9
```

**tempdb file layout and usage:**

```bash
gossms db files tempdb -p prod-ro
```

### 10.9 Performance and activity

```bash
# What is running right now
gossms monitor sessions --active -p prod-ro

# Who is blocking whom
gossms monitor blocking -p prod-ro

# Top waits and the most expensive queries
gossms monitor waits --top 10 -p prod-ro
gossms monitor top-queries --by cpu --top 10 -p prod-ro -o json

# The plan of one of them (its plan_handle, statement_start and statement_end)
gossms plan cached 0x0600050057E1D03290A1C2B4F0020000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000 --statement-start 112 --statement-end 418 -p prod-ro

# Regressed queries from Query Store
gossms monitor querystore -d AdventureWorks --regressed -p prod-ro

# Deadlocks since yesterday
gossms monitor deadlocks --since 2026-10-27T00:00:00Z -p prod-ro

# End a blocking session (target must be confirmed)
gossms monitor kill 87 --confirm 87 -p dev --allow admin
```

### 10.10 Maintenance and backups

```bash
# Last full, differential and log backup for every database
gossms maint backup-history --all --last -p prod-ro

# Back up a database
gossms maint backup AdventureWorks --to /var/opt/mssql/backup/AW_full.bak --type full --compression -p dev --allow admin

# Check a backup file without restoring it
gossms maint restore AdventureWorks --from /var/opt/mssql/backup/AW_full.bak --verify-only -p dev --allow admin

# Fragmented indexes, then rebuild one table (preview first)
gossms maint index-fragmentation --database AdventureWorks --min-percent 30 -p prod-ro
gossms maint index-rebuild Sales.SalesOrderDetail --online -p dev --allow ddl --dry-run

# Update statistics and run an integrity check
gossms maint update-stats Sales.SalesOrderDetail --fullscan -p dev --allow ddl
gossms maint checkdb AdventureWorks --physical-only -p dev --allow admin
```

### 10.11 SQL Server Agent

```bash
# Jobs that failed since yesterday
gossms job list --failed-since 2026-10-27T00:00:00Z -p prod-ro

# Why did a job fail?
gossms job history "Nightly ETL" --top 5 -p prod-ro

# Start a job at step 3
gossms job start "Nightly ETL" --step 3 -p dev --allow admin
```

### 10.12 Security

```bash
# Who has sysadmin?
gossms security role-members sysadmin -p prod-ro

# Effective permissions of a user in a database
gossms security permissions app_reader --database AdventureWorks -p prod-ro

# Grant SELECT on a schema (preview first)
gossms security grant SELECT --on SCHEMA::Sales --to app_reader -p dev --allow admin --dry-run
```

### 10.13 Schema and data

```bash
# Generate the CREATE script of a table, and save it to a file
gossms script object Sales.Customer --as create -p prod-ro --script-file Customer.sql

# Profile a table's columns (nulls, distinct values, min/max)
gossms data profile Sales.Customer -p prod-ro

# Export a table's rows (or a query result: --sql/--file) to CSV
gossms data export Sales.Customer --where "ModifiedDate >= '2026-01-01'" --out-file customers.csv -p prod-ro

# Load them into another table (dry run converts every cell, loads nothing)
gossms data import Staging.Customer --in-file customers.csv -p dev --allow write --dry-run

# Tables without a primary key (plain T-SQL through stdin)
cat <<'SQL' | gossms query run --file - -p prod-ro
SELECT s.name AS [schema], t.name AS [table]
FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE OBJECTPROPERTY(t.object_id, 'TableHasPrimaryKey') = 0
ORDER BY 1, 2;
SQL
```

### 10.14 Error log

```bash
# Failed logins in the last day
gossms server log --search Login --search failed --since 2026-10-27 -p prod-ro

# Last 50 entries
gossms server log --lines 50 -p prod-ro
```

`--since` is the server's local time with no zone (the log records none; a `Z` or an offset is exit 2). Two `--search` texts must both appear (xp_readerrorlog's AND); one text with a space needs shell quoting. One log file per run: an earlier day may need `--archive 1` (phase 5 D7).

---

## 11. SSMS feature → CLI mapping

| SSMS feature | CLI command |
|--------------|-------------|
| Connect to Server dialog | `profile list/test`, `--profile`, `--server/--auth` |
| Server Properties → Advanced (MAXDOP, cost threshold) | `server config get/set maxdop`, `server config get/set cost-threshold` |
| Server Properties → Memory | `server memory`, `server config get/set max-memory/min-memory` |
| Database Properties → Options | `db set --option …`, `db scoped-config get/set` |
| Object Explorer tree | `db list`, `object list`, `security … list`, `job list` |
| Table → Design / Properties | `object describe`, `object indexes` |
| Script Table As → CREATE To | `script object --as create` |
| Generate Scripts wizard | `script db` |
| Select Top 1000 Rows | `data top --rows 1000` |
| New Query / Execute (F5) | `query run` |
| Parse (Ctrl+F5) | `query parse` |
| Display Estimated Plan (Ctrl+L) | `plan estimated` |
| Include Actual Plan (Ctrl+M) | `plan actual` |
| Activity Monitor | `monitor sessions/blocking/waits/top-queries` |
| Activity Monitor → Kill Process | `monitor kill` |
| Plan cache (Activity Monitor → Show Execution Plan) | `plan cached` |
| Query Store reports (Top Resource Consuming, Regressed, …) | `monitor querystore` |
| system_health deadlock graphs | `monitor deadlocks` |
| View Dependencies | `object dependencies` |
| Tasks → Back Up / Restore | `maint backup` / `maint restore` |
| Import/Export Wizard | `data import` / `data export` |
| SQL Server Agent → Job Activity | `job list/history/start` |
| XEvent Profiler | `xevent capture` |
| Management → Extended Events → Sessions | `xevent sessions` |
| Error log viewer | `server log` |
| Reports → Disk Usage / Disk Usage by Top Tables | `db space`, `db files`, `db table-sizes` |
| Database Properties → Files | `db files` |
| Standard reports | `monitor …`, `db log-usage`, `server volumes`, `maint backup-history` |

---

## 12. Optional extension: interactive JSONL mode

Multi-step work that needs a single connection (temp tables, open transactions, `SET` options) does not fit stateless calls. For this, offer a long-running mode:

```bash
gossms shell -p dev --protocol jsonl
```

The agent writes one JSON request per line to stdin and reads one JSON response per line from stdout:

```json
{"id":1,"command":"query run","sql":"CREATE TABLE #t (id int); INSERT #t VALUES (1);","allow":"write"}
{"id":2,"command":"query run","sql":"SELECT * FROM #t;"}
```

This is also a natural basis for a future **MCP server** (Model Context Protocol), which exposes the same commands as tools to AI agents directly.

---

## 13. Acceptance criteria

- [ ] Every command runs without any interactive prompt when stdout is not a TTY.
- [ ] `gossms` with no parameters always starts the TUI, as today. No CLI command ever starts it. `gossms --version`/`-v` print the version as today.
- [ ] The CLI uses the TUI's saved connections as profiles, and never changes `config.json`, never creates `gossms.key`, never deletes a temp file beside them (checked by comparing the directory listing and mtimes before and after a CLI run).
- [ ] A named connection survives 30+ further connects in the TUI and keeps its name and `max_allow` when reconnected through the Connect dialog.
- [ ] `gossms` with options but no command (e.g. `gossms -p dev`) exits with `2` and points to `agent guide`.
- [ ] Bare `gossms` with piped stdin/stdout exits within a second with code `1` and a clear error — also when run from inside a terminal session (controlling tty present). It never hangs.
- [ ] A script whose first statement returns more than `--max-rows` rows still runs its later statements and batches, and the total row count is exact.
- [ ] CLI CSV and the TUI's Results To File produce byte-identical files for the same result.
- [ ] CLI sessions show `program_name = goSSMS - CLI` in `sys.dm_exec_sessions`.
- [ ] `agent guide`, `agent guide --brief` and `agent schema` list every non-hidden command, and every example in them passes the CLI parser.
- [ ] `agent context -p <profile>` reports missing permissions correctly (tested with a read-only login).
- [ ] `--output json` produces valid JSON with the envelope from §5.2 for **every** command, including errors.
- [ ] Exit codes match §7, and each code is covered by at least one automated test.
- [ ] A `write`/`ddl`/`admin` batch with no matching `--allow` is blocked (exit 7) and nothing is executed.
- [ ] `--dry-run` never opens a write transaction.
- [ ] `db files`, `db space` and `db log-usage` sizes match `sys.database_files`, `sp_spaceused` and `sys.dm_db_log_space_usage` for the same database, and column names always carry the unit.
- [ ] `server config set` restores `show advanced options` to its previous value, converts memory units correctly, reports `restart_required`, and blocks risky values without `--force`.
- [ ] Output is identical on Linux, macOS and Windows, and regardless of OS locale (tested with `de-CH`, `en-US`, `fr-FR`), and the cell text equals what the TUI grid shows for the same query.
- [ ] Truncation is always reported (`truncated: yes/true` plus a hint).
- [ ] No secret ever appears in output, logs, `profile show` or the audit log.
- [ ] `help --output json` lists every command and option. A test fails if a command is missing from the catalog.

---

## Open decisions

Settled in the 2026-10-28 review (not open any more): profiles are named saved connections, renamed in the TUI; named connections are exempt from the 30-entry trim; `max_allow` is set in the same TUI dialog and defaults to `admin`.

1. **Default output format for agents:** `text` (shorter, uses fewer tokens) or `json` (stricter to parse). Recommendation: default `text`, and tell agents to use `-o json` when they need to parse results programmatically.
2. **Default `--max-rows`** (200 is proposed). Balance this against your agents' context-window size.
3. **Production profiles:** whether `maxAllow` ceilings should be enforced centrally (e.g. a policy file managed by IT) rather than per user.
4. **`--var` / SQLCMD variables:** add `$(name)` substitution (lexer-aware, outside literals and comments) in a later phase, or leave SQLCMD features out entirely as the TUI does.
5. ~~**Argument parser**~~ — settled 2026-10-28 (phase 0 D4): a small hand-written parser in `internal/cli/args` over `spec` — no new dependency, and the command definitions it reads are the same data `agent schema` prints. Cobra rejected: a new dependency tree whose flag model would still need mapping onto the schema.
6. **MCP server (§12):** whether `gossms shell --protocol jsonl` and an MCP mode are in scope for this branch at all, or a follow-up.

---

## 14. Integration architecture

### 14.1 Shape

One binary, one `main`. `cmd/gossms/main.go` decides the mode before anything opens a log file or a screen (it already does this for `--version`):

```text
cmd/gossms/main.go
  ├─ len(os.Args) == 1          → TTY check → tui.NewApp().Run()        (unchanged path)
  ├─ --version / -version / -v  → printVersion()                        (unchanged)
  └─ anything else              → os.Exit(cli.Main(ctx, os.Args[1:], cli.Env{Stdin, Stdout, Stderr, Getenv}))
```

The CLI is a new package tree that **never imports `internal/tui` or `internal/tuikit` widgets** (only the stdlib-only leaf `tuikit/sqltext`; **not** `tuikit/core`, which imports tcell — display width is `displaywidth.String` directly, phase 0 D2). It sits beside the TUI on the same lower layers:

```text
                 cmd/gossms
                /          \
       internal/tui      internal/cli ─────────────┐
            │  \            │  │  │                │
            │   \           │  │  └─ cli/safety ───┤── tuikit/sqltext (one lexer)
            │    \          │  └──── cli/output ───┤── internal/export (CSV writer, extracted)
            │     \         │                      │
            ▼      ▼        ▼                      ▼
   internal/config  internal/db  internal/query  internal/showplan  internal/activity
                         │            │
                         └──── gosmo ─┘  (catalog, scripting, WithScript, config, DMVs)
```

A new test in the style of `ARCHITECTURE.md` § Why split this way pins the rule: `go list -deps ./internal/cli/...` must not contain `internal/tui` or `tcell`.

### 14.2 Packages

| Package | Responsibility |
|---|---|
| `internal/cli` | `Main(ctx, args, env) int`: parse, resolve connection, check safety, dispatch, map the error to an exit code. Owns SIGINT → context cancel → exit 130 |
| `internal/cli/spec` | The command tree as **data**: each command's path, summary, arguments, options (name, type, default, env var), operation class, examples, and handler. The parser, `--help`, `agent schema`, `agent guide`'s command index and the acceptance tests all read it — one source, no drift (§8.5) |
| `internal/cli/args` | Parser over `spec`: `--kebab`, `--x=v`, `--no-x`, repeat/comma lists, `-` stdin, `@file` response files, unknown option → exit 2 |
| `internal/cli/safety` | Statement classifier over `sqltext` (§6.1), `--allow` × `max_allow` ceiling, `--confirm`, `--transaction rollback` refusals |
| `internal/cli/output` | The envelope (`Result{Status, Command, Server, Database, Duration, Sets, Messages, Warnings, Errors, Context}`) and one renderer per format: text, json, jsonl, csv, tsv, markdown. Implements `query.RowSink` for row capping and streaming (§14.5) |
| `internal/cli/cmd/<group>` | One file per command group (`profile.go`, `server.go`, `db.go`, `object.go`, `query.go`, …): a handler turns gosmo / query results into `output.Result`. No formatting, no exit codes |
| `internal/cli/agent` | `agent guide` (hand-written rules and recipes embedded with `go:embed`, plus sections generated from `spec`), `agent schema`, `agent context` |
| `internal/export` | The CSV/TSV writer extracted byte-for-byte from `internal/tui/query_panel_export.go`; the TUI's Results To File then calls it |

Handlers follow `internal/db`'s rule: context first, one form, no `Foo`/`FooContext` pairs.

### 14.3 Data flow of one call

```text
args ──► args.Parse(spec) ──► Invocation{cmd, opts}
                                   │
         config.LoadReadOnly() ──► resolve profile (+ overrides, env, password source)
                                   │
         safety.Check(cmd.Class or classify(sql), --allow, max_allow)  ── refuse → exit 7
                                   │
         db.Connect(ctx, conn, db.RoleCLI)  ── ConnectionError → exit 3/4/6
                                   │
         handler(ctx, sc, inv) ──► output.Result     (dry-run: ctx = gosmo.WithScript(ctx))
                                   │
         output.Render(format, result, limits) ──► stdout; one-line summary ──► stderr
                                   │
         audit.Append (optional) ──► exit code from result status
```

Everything after `args.Parse` runs under one context derived from the process context, which SIGINT cancels. `sc.Close()` is deferred right after connect, so a cancel stops every in-flight gosmo statement (`ARCHITECTURE.md` § How a query runs).

### 14.4 Changes to existing gossms code

| Area | Change | Why |
|---|---|---|
| `cmd/gossms/main.go` | Mode switch (§14.1); TTY check before `tui.NewApp()` | R4, R5 |
| `internal/config` | `Connection.Profile string \`json:"profile,omitempty"\`` and `Connection.MaxAllow string \`json:"max_allow,omitempty"\``, both **outside** `connectionAAD` (renaming must not invalidate a sealed password). `AddOrUpdate` carries both over from the entry it replaces. The 30-entry trim skips entries with a `Profile`. New `connOp` kind for set-profile so `Save`'s replay merges it with a concurrent instance's save. `ProfileByName(name)` lookup. `LoadReadOnly()`: no key creation, no temp sweep, no reseal, no `Save` | R1, R2 |
| `internal/tui` (Connect dialog) | History pane: a **Profile…** button (beside Delete) opening a small dialog: name field, Allowed operations dropdown, validation (unique, charset). History rows show the profile name first when set. F1 help and README updated | R1 |
| `internal/db` | `RoleCLI` → `program_name = "goSSMS - CLI"`, no capability probe (like `RoleQuery`) | R17 |
| `internal/query` | (a) `Message` gains a structured `*SQLError` (number, severity, state, line, procedure) next to its text; (b) rows affected recorded as a number per statement, not only as `(n rows affected)` text; (c) a sink can receive column types — an optional `TypedRowSink` interface with `BeginTypedSet(columns, types []string)`, so existing sinks stay unchanged. The TUI keeps working off the text | R6, R7 |
| `internal/tui/query_panel_export.go` | Its `csvSink` writer moves to `internal/export`; the TUI calls it | R8 |
| `ARCHITECTURE.md` | Package map rows for every new package and file; a "How a CLI call runs" section; the CLI's import rule | `TestPackageMapListsEveryTUIFile`, doc ownership |
| `docs/cli.md` (new) | The authoritative CLI contract (this spec, trimmed), registered in § Which document owns what | R19 |

### 14.5 Execution path for `query run`

`query.Open` one Session for the script (the same type the TUI's query window uses), then `Session.ExecuteToSink` with the CLI's `output` sink and **no** `WithMaxRows`. The sink keeps the first `--max-rows` rows per set (cells copied, since the executor reuses the slices), counts the rest, and records truncation; `--out-file` and `jsonl` stream every row instead. `--max-col-width` truncates cell text at render time. `--transaction rollback` and `--max-rows-affected` wrap the script with `BEGIN TRAN` / `ROLLBACK` (or `COMMIT`) on the same Session, then `Session.Close` discards the connection, so nothing can leak back into a pool. `plan estimated/actual` use `ExecuteEstimatedPlan` / `ExecuteWithPlan` and `internal/showplan` for the `summary` digest (missing indexes, warnings, costliest operators — all already parsed there).

### 14.6 gosmo additions

Built and tested in gosmo first, against the live servers, using the `dev-with-local-gosmo` workflow (`replace` active during development; a gosmo tag before a gossms release). None narrows an existing API.

| Addition | Used by | Phase |
|---|---|---|
| `sp_configure` defaults per major (13, 14, 15, 16, 17; Azure MI) | `server config list --non-default`, `config reset` | 5 |
| Trace flags: list / on / off (`DBCC TRACESTATUS/TRACEON/TRACEOFF`) | `server traceflag` | 5 |
| Log usage: `log_reuse_wait_desc`, VLF count (`sys.dm_db_log_info`, 13 SP2+) | `db log-usage` | 4 |
| Table sizes (Disk Usage by Top Tables) if not already derivable from `DiskUsage` / `Table` | `db table-sizes` | 4 |
| `Database.Search` in definitions and columns | `object search --in` | 3 |
| Plan-cache top queries (`sys.dm_exec_query_stats`) | `monitor top-queries`, `plan cached` | 7 |
| `system_health` deadlock reports | `monitor deadlocks` | 7 |
| `DBCC CHECKDB` | `maint checkdb` | 8 |

### 14.7 Concurrency with a running TUI

The CLI and the TUI may run at the same time against the same config directory. The CLI only reads `config.json` (atomically written by the TUI, `fileutil.WriteAtomic`), so it sees either the old or the new file. It never takes the TUI's file lock, never sweeps temp files and never writes `gossms.log` (diagnostics go to stderr with `--verbose`), so it cannot disturb a TUI save in progress. The audit log is the CLI's only file, under its own lock.

---

## 15. Implementation plan

Phases are ordered so each one ships something usable and testable on its own, and each builds only on finished phases. Every phase ends with: `gofmt`, `go vet`, `staticcheck`, `go test ./...` in both repos, **and driving the built binary** against the live servers (majors 13, 14, 17; AAG for `--application-intent`) per `docs/testing.md` — green tests are not verification. Each phase is analysed in detail before it starts; the bullets below are scope, not design.

### Phase 0 — Groundwork (no database commands yet; see P0-3)

- `cmd/gossms`: mode switch and TTY check (R5) — the one change that also helps the TUI today.
- `internal/cli` skeleton: `Main`, `spec` data model, `args` parser, `output` envelope with `text` and `json`, exit-code mapping, SIGINT. Commands: `version`, `help`, `--help`.
- Import-rule test (`internal/cli` never reaches `internal/tui`/tcell).
- `docs/cli.md` started; `ARCHITECTURE.md` package map and doc-ownership rows.
- **Done when:** `gossms version -o json`, `gossms nope` (exit 2), `gossms -p x` (exit 2 + guide pointer), bare `gossms | cat` (exit 1, no hang) all behave on Linux and Windows.

#### Phase 0 analysis (2026-10-28, against 33ea57f)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P0-1 | `tuikit/core` imports tcell (`drawing.go`, `screen.go`, `clip_screen.go`, … and `strutil.go` itself); `core.DisplayWidth` is a one-line wrapper over `displaywidth.String` | §14.1's "CLI may import `tuikit/core`" contradicts the import rule "no tcell". `tuikit/sqltext` is stdlib-only and fine. Decision D2 |
| P0-2 | `config`, `db`, `query`, `showplan`, `activity`, `format`, `version`, `fileutil` reach neither tcell nor `internal/tui` (`go list -deps`) | The §14.1 layering holds today; the import test only has to keep it |
| P0-3 | `main` ignores every argument except the three version flags: `gossms --help`, `gossms foo` start the TUI today | Phase 0 *is* user-visible: any argument stops starting the TUI. README needs a sentence now, not in phase 1 (D6) |
| P0-4 | `golang.org/x/term` v0.47.0 is an `// indirect` require (via tcell) | Becomes direct (`go mod tidy` moves it); no new module |
| P0-5 | Packages below `cmd` call `log.Printf`; `log`'s default output is stderr | The CLI must set `log.SetOutput(io.Discard)` (stderr only with `--verbose`) before any handler runs, or library chatter lands on stderr between the summary lines. It must also never call `config.OpenLogFile` (§14.7) |
| P0-6 | `release.yml` brew test is `assert_match version.to_s, shell_output("#{bin}/gossms --version")`; `printVersion` is kept in step with `newAboutRows` (`internal/tui/menu.go`) | `--version` stays byte-identical and is dispatched before the CLI. `gossms version` shares the *fields*, not the bytes (envelope header) — §2.1 corrected |
| P0-7 | `agent guide` arrives in phase 3, but phase 0's usage error, `--help` first line and TTY error all name it | Pointing at a command that answers "unknown command" is the worst first contact. Decision D1 |
| P0-8 | `TestNoDanglingDocReference` walks every `.md` and Go comment | `docs/cli.md` must land in the same change as the first reference to it from `ARCHITECTURE.md`/code |

**Decisions** — all six settled 2026-10-28 as recommended (the alternatives are kept for the record)

- **D1 — guide pointer before phase 3:** point at `gossms help` until `agent guide` exists, and a spec test (0.g) fails if any hint/usage string names a `gossms <cmd>` the tree lacks — the wording then *has* to change in phase 3. *Alternative:* a stub `agent guide` printing only §8.2 sections 1–3 (hand-written, `go:embed`).
- **D2 — display width without tcell:** `internal/cli/output` calls `displaywidth.String` directly, with a comment naming `core.DisplayWidth` as the same function; `CLAUDE.md`'s DisplayWidth line gains "(`internal/cli`: `displaywidth.String`, core pulls tcell)". *Alternative:* move `strutil.go`'s width helpers to a tcell-free `tuikit/textwidth` leaf that `core` re-exports — cleaner, but a tuikit change for one call.
- **D3 — non-tabular results:** the envelope gets an ordered `Fields` list (key, value). Text: `KEY: value` lines after the header block; JSON: `"data": {…}` in field order. `version` needs it now; `profile show`, `server info`, `db info` later. §5.2 has only `resultSets`, so this is a spec addition.
- **D4 — Open decision 5 (parser):** phase 0 assumes the hand-written parser. Settle before 0.d.
- **D5 — `--output` enum in phase 0:** only `text|json`; phase 2 extends the enum. `agent schema` must never advertise a format that answers "not implemented".
- **D6 — README:** one sentence beside the `--version` paragraph: any argument runs the command-line mode; bare `gossms` needs a terminal on stdin and stdout.

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-28.** **0.a Version fields in `internal/version`.**
- ✅ **Done 2026-10-28.** **0.b Mode switch and TTY guard in `cmd/gossms/main.go`.**
- ✅ **Done 2026-10-28.** **0.c `internal/cli/spec`: the command tree as data, types only.**
- ✅ **Done 2026-10-28.** **0.d `internal/cli/args`: the parser.**
- ✅ **Done 2026-10-28.** **0.e `internal/cli/output`: envelope, renderers, error model.**
- ✅ **Done 2026-10-28.** **0.f `internal/cli.Main(ctx, args, Env) int`.**
- ✅ **Done 2026-10-28.** **0.g Guard tests.**
- ✅ **Done 2026-10-28.** **0.h Docs, same change as the code.**
- ◐ **Linux done 2026-10-28; Windows owed.** **0.i Verification (drive the binary, `docs/testing.md`).**

**Not in phase 0** (stated so review does not expect them): profiles and every connection option, colour, `--max-*` limits, csv/tsv/jsonl/markdown, `agent *`, audit log, any DB access.

### Phase 1 — Profiles

✅ **Done 2026-10-28** (Linux; the Windows runs owed since 0.i still are — see 1.h).

- `config`: `Profile`, `MaxAllow`, carry-over in `AddOrUpdate`, trim exemption, set-profile `connOp`, `LoadReadOnly`, `ProfileByName`.
- TUI: Connect dialog **Profile…** dialog; History shows names; F1 help; README.
- `db.RoleCLI`. CLI connection resolution: profile + overrides + env + password sources, Entra method refusals.
- Commands: `profile list/show/test`.
- **Done when:** a profile named in the TUI is usable from the CLI, survives 30 more connects and a reconnect, a sealed password still opens after renaming, and a running TUI is undisturbed by CLI calls (§14.7).

#### Phase 1 analysis (2026-10-28, against 90beff8)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P1-1 | `AddOrUpdate` records its op and `Save` replays it onto the file as it is then (another instance may have saved) | The carry-over lives in the list function `addOrUpdate`, not in `AddOrUpdate`: the recorded op holds no profile, so its replay carries over whatever name the *file* holds, including one another instance set meanwhile |
| P1-2 | `parseConfig` copies a corrupt file aside (`keepCorrupt`) and `Load` sweeps temps and creates the key | `LoadReadOnly` needs a parse without the copy: split into `decodeConfig` (shared) and `parseConfig` (adds the copy). A corrupt or unreadable file is an error for the CLI, not an empty list |
| P1-3 | A profile name is typed on command lines; the parser reads a token starting `-` as an option | Names start with a letter or digit (`-dev` would be `gossms profile show -dev` → unknown option) |
| P1-4 | "An option is in the spec only when it works", and only `profile test` connects in phase 1 | Connection options are **options of the commands that connect** (a shared `connect.Options()`), not globals: `version --server x` is an unknown option. `--profile`/`-p` itself arrives with the first command that takes it (phase 2's `query run`); `profile test` takes the name positionally |
| P1-5 | An override must be distinguishable from a default (`--no-trust-server-certificate` vs. absent) | `spec.Invocation.Given` (options set on the command line or by their env variable); the parser fills it |
| P1-6 | Handlers need stdin and env (`--password-stdin`/`--password-env`) and a config/dial they can be tested without | `Invocation.Stdin/Getenv` (Main fills them); `cmd.Deps{LoadConfig, Connect}`, `newCommandTree(deps)` for tests |
| P1-7 | `db.Connect` bounds every attempt at 30 s (`connectTimeout`, also in the DSN) | `--connect-timeout` is 1–30 (default 15) via the dial's context, rather than a db API change; a value above 30 would be a promise nothing keeps |
| P1-8 | Live: "Cannot open database" arrives as **Msg 4063** (fallback to the login's default database), not 4060, on 13, 14 and 17 | Both map to `NOT_FOUND`; unit tests had passed with 4060 only |

**Decisions** (made in implementation, recorded for review)

- **D1 — `MaxAllow` unknown value:** reads as `read` (fails closed); missing is `admin` (author's decision, §6.2).
- **D2 — name collision across instances:** a replayed set-profile op whose name another instance gave a different entry moves the name (later save wins), so names stay unique on disk. `ProfileByName` prefers the most recent entry should a hand edit duplicate one.
- **D3 — password drop rule (§3.1):** server compared ignoring case; port by `DialPort` (1433 = none); user, method, tenant, client exact; TLS weakened = `--encrypt` lower than saved, trust turned on, a different `--host-name-in-certificate`. Database, a stronger `--encrypt`, `--no-trust-server-certificate`, `--application-intent` keep it. Env variables count as overrides.
- **D4 — `--auth` enum lists `entra-interactive`/`entra-device-code`** so asking for them gets the refusal's reason (`AUTH_FAILED`, exit 4, §3.1) instead of "not a valid value".
- **D5 — `profile show` prints Extra Properties keys only:** a value may be anything the user put there.
- **D6 — the History label** leads with `[name]`, the server budget shrinks by the prefix width.

**Substeps**

- ✅ **1.a `config`**
- ✅ **1.b `db.RoleCLI`**
- ✅ **1.c `internal/cli/connect`**
- ✅ **1.d `internal/cli/cmd` + tree**
- ✅ **1.e TUI**
- ✅ **1.f Docs**
- ✅ **1.g Verification on Linux (driven binary, scratch `XDG_CONFIG_HOME`)**
- ◐ **1.h Owed**: every Windows run (as 0.i); AAG read-only routing (landed on the primary); Entra methods untested live.

**Not in phase 1**: `--profile/-p` (phase 2, with `query run`), `--allow` and enforcing the ceiling (phase 2), ad-hoc connections without a profile (resolution supports them; no command takes them yet).

### Phase 2 — `query` and the safety model (the core agent loop)

- `query` executor additions (structured errors, rows affected, typed sink); `internal/export` extraction (TUI Results To File re-verified byte-for-byte).
- `cli/safety` classifier over `sqltext`; `--allow`, `max_allow`, `--dry-run`, `--transaction rollback`, `--max-rows-affected`.
- `cli/output`: all formats, row/col/byte limits via the sink, truncation markers, `--out-file`.
- Commands: `query run/parse/classify`.
- **Done when:** the §13 safety and truncation criteria pass on a live server, including a large `SELECT` followed by an `UPDATE` under `--transaction rollback`.

#### Phase 2 analysis (2026-10-28, against 90beff8 + phase 1 working tree)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P2-1 | `RowSink.BeginSet(columns)` gets names only, but `rowScanner` already computes `types` (`columnTypeNames`) per set; go-mssqldb v1.11.2 implements `ColumnTypeNullable` | `TypedRowSink` is a type assertion in `streamResultSet`, no new scan work. Whether the driver reports nullability (`ok`) for every column is unverified — check live before §5.2's `nullable` is promised |
| P2-2 | The sink path appends TUI text per set: `(n row(s) written)` and the Max-fetch-rows notice | The CLI sink would print TUI wording in `messages`. Executor option to suppress the sink notices (`WithoutSinkNotices`) |
| P2-3 | Rows affected exist only as `(n rows affected)` notice text, and SQL Server sends them for a `SELECT` too (the DONE count); `SET NOCOUNT ON` suppresses all of them, also inside procedures | Structured count on `Message` must say whether it followed a result set (`SELECT` count) or not (DML count) — the order MsgNext → rows → MsgRowsAffected is assumed, **verify live**. `--max-rows-affected` can only trust what the server reports (D4) |
| P2-4 | `batchErrorMessages` already holds `gosmo.AsSQLError`'s number, class, state, line (script-corrected), procedure — then flattens them into two text lines | R7's structured error is keeping that value: `Message.SQL *SQLError` on the error's first line. No new driver work |
| P2-5 | The executor never stops on an error: every later batch runs (SSMS behaviour) | `--stop-on-error` (the CLI default) needs an executor option (`WithStopOnError`): stop *between* batches; within a batch SQL Server decides |
| P2-6 | Under `SET XACT_ABORT ON` an error rolls back the wrapping transaction and aborts the batch — later GO batches then run in **autocommit** | `--transaction rollback` forces stop-on-error and refuses `--continue-on-error` (exit 2). Residual hole: a `COMMIT` inside an EXEC'd procedure ends the outer transaction (Msg 266 at exit, data committed). `Result.State.TranCount == 0` after the run detects it after the fact (D3) |
| P2-7 | `csvSink` is welded to `fileutil.Stream` + `bufio`; `writeRecord`/`csvNeedsQuotes` (lone-NULL row, `\.`, leading space) are the settled lossless contract (`docs/decisions.md` § SQL NULL in results). There is no TSV | `internal/export`: a record writer over `io.Writer` with a separator rune; the TUI keeps `Stream`/`Commit`/`Abort`/`finish`. TSV is new, so its quoting is a decision (D9) |
| P2-8 | `cli.Main` renders the whole `Result` after the handler returns (`output.Render`, `cli.go:164`) | Fine for capped sets. `jsonl` to stdout and `--out-file` must write *while* the query runs: the handler needs a writer (`Invocation.Stdout`, filled by Main like `Stdin`) and the stream renderer writes the summary itself |
| P2-9 | `StatementCutter` cuts only at `statementLeaders` and `;`. A statement starting with a non-leader word (`ENABLE/DISABLE TRIGGER`, `THROW`, `WRITETEXT`/`UPDATETEXT`, `REVERT`, `SETUSER`, a bare proc name mid-batch) is **merged into the previous statement** | Classifying by leader alone fails *open*: `SELECT 1` + newline + `DISABLE TRIGGER …` reads as `read`. The classifier needs a keyword floor over every word token (D1) |
| P2-10 | `CREATE/ALTER PROCEDURE|FUNCTION|TRIGGER|VIEW` must be alone in its batch; the cutter does not cut after `AS` but does after a `;` inside the body | Rule: a batch led by one of these is one `ddl` statement — its body's `COMMIT` or `SET NOCOUNT ON` must not trigger the §6.4 refusals |
| P2-11 | The lexer works on `[]rune`; `SplitBatches` gives each batch's `StartLine` and repeat `Count` | Statement line = batch start + newlines before its first token. `GO n` repeats: a write repeated is still one class; `--max-rows-affected` counts every repetition |
| P2-12 | Cell text: bit `1`/`0`; float `strconv` `'f'`/`'E'` (both valid JSON numbers); decimal/money digits; GUID upper | Typed JSON is a mapping on the column type — ints/real/float → number, bit → bool, everything else string — never a second formatter (§5.3) |
| P2-13 | `Session.EndTransactions(ctx, commit)` and `Result.State.TranCount` exist; `Session.Close` discards the connection | Rollback/commit wrapping needs no new session API. Cancel or timeout mid-run → `Close` → server rolls back |
| P2-14 | `output.Set.Rows [][]*string` and the executor reuses its row slices | The CLI sink copies the first `--max-rows` cells. `--max-rows 0` retains everything in memory — documented; `--out-file`/`jsonl` are the unbounded paths |
| P2-15 | P1-4: `--profile/-p` arrives with the first command that takes it; resolution already supports ad-hoc connections | `query run`/`parse` take `connect.Options()` + `--profile` (`GOSSMS_PROFILE`). An ad-hoc call has no ceiling beyond `--allow` (§6.2) |

**Decisions** — all thirteen settled 2026-10-28 as recommended (the alternatives are kept for the record)

- **D1 — classifier = leader rule + keyword floor.** Per statement: class from its leading keywords (§6.1 rules), then raised to the highest class of any *word* token in it (outside literals, comments, quoted identifiers) found in a floor table (`INSERT/UPDATE/DELETE/MERGE/INTO` → write, `CREATE/ALTER/DROP/TRUNCATE/DISABLE/ENABLE` → ddl, `KILL/SHUTDOWN/BACKUP/RESTORE/GRANT/DENY/REVOKE/RECONFIGURE/LOGIN` → admin), with the known read contexts exempt (`FOR UPDATE`, `UPDATE(` in a trigger, `ON DELETE/UPDATE` of a foreign key, the `DBCC` read list). Over-classification (a column named `kill`) is accepted and the output names the word. *Alternative:* extend `statementLeaders` — changes Ctrl+Enter selection and Format Document too.
- **D2 — `--stop-on-error`:** between batches only; default on (unlike the TUI). Exit 5 on a SQL error; 10 (`PARTIAL`) only with `--continue-on-error` when at least one batch succeeded.
- **D3 — rollback hole:** after the run, `TranCount == 0` (the wrapper's transaction is gone) → error `SQL_ERROR` "the script ended the wrapping transaction; later changes may be committed", exit 5. The guide states the guarantee covers the script's own statements, not procedure bodies. *Alternative:* refuse any EXEC under `--transaction rollback` — makes it useless for testing procedures.
- **D4 — `--max-rows-affected` blind spots:** refused (exit 7) when the script contains `SET NOCOUNT ON`, dynamic SQL, or an EXEC not on the read allow-list — their counts can be invisible, and "committed because nothing was counted" is the failure this option exists to prevent. Counts from triggers are included (over-count, safe side).
- **D5 — envelope additions:** top-level `rowsAffected` (sum of DML counts; `null` when none reported), text `ROWS_AFFECTED:`; per-set `rowsAffected` stays `null` for result sets. `output.Error` gains `sqlErrorNumber, severity, state, line, procedure` (omitted when not a SQL error). SQL errors go to `errors`, not `messages`. Blocked output: `DETECTED_CLASS`, `ALLOWED_CLASS`, `STATEMENTS` as record fields (phase 0 D3).
- **D6 — csv/tsv carry no envelope:** stdout gets rows only; errors, truncation `NOTE` and the summary go to stderr in full (not one line); the exit code carries the status. `markdown` = the text envelope with GFM tables (`|` escaped, line breaks → `<br>`).
- **D7 — `--out-file`:** the file's format comes from its extension (`.csv .tsv .jsonl .json .md .txt`, else exit 2); `-o` governs the stdout summary. No row or column limits in the file. Written through `fileutil.CreateStream` and, as Results To File, discarded on error or cancel (destination unchanged, said in the summary). An existing file is refused without `--force` (no prompt in CLI mode).
- **D8 — `--max-output-bytes`:** buffered formats render, and if over the cap drop trailing rows (last set first) until it fits, marking `truncated` with reason `max-output-bytes`; `jsonl` stops emitting rows and still writes the `_summary` line (exempt from the cap). Never cuts a JSON document mid-value.
- **D9 — TSV quoting:** the CSV rules with tab as the separator (quote a field containing tab, quote or line break). *Alternative:* PostgreSQL TEXT-style `\t`/`\n` escapes — not what spreadsheets read.
- **D10 — `--max-col-width`:** counts characters (runes), marker `…[+N chars]`; applies to text/markdown/json/jsonl, never to csv/tsv/`--out-file` (exports are lossless).
- **D11 — exit mapping for `query run`:** severity ≥ 11 → 5, except `gosmo.IsPermissionDenied` → 9 and `ErrUnsupported…` → 11; `208` stays 5 (`NOT_FOUND` is for profiles/databases/objects named on the command line); `SessionLost` mid-run → 3; `--timeout` → 6.
- **D12 — `query run --dry-run`:** no connection; prints the classification (as `query classify`) and applies the same `--allow`/ceiling check — exit 7 when it would be blocked, so the agent learns the `--allow` it needs without running anything.
- **D13 — `--param`:** last code substep (2.j). *Alternatives:* defer to phase 3; real driver parameters via an executor `WithArgs`. Built by the CLI as `EXEC sp_executesql N'…', N'@id int', @id = …` with gosmo's quoting; single batch only; classified on the inner statement, not as `sp_executesql` (which D1 would fail closed to `write`).

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-28.** **2.a `internal/query` executor additions.**
- ✅ **Done 2026-10-28.** **2.b `internal/export` extraction.**
- ✅ **Done 2026-10-28.** **2.c `internal/cli/safety`: classifier.**
- ✅ **Done 2026-10-28.** **2.d `safety.Check` + `query classify`.**
- ✅ **Done 2026-10-28.** **2.e `cli/output`: data model and buffered formats.**
- ✅ **Done 2026-10-28.** **2.f Streaming: `jsonl`, `--out-file`, `--max-output-bytes`.**
- ✅ **Done 2026-10-28.** **2.g `query run` (autocommit).**
- ✅ **Done 2026-10-28.** **2.h Transactions.**
- ✅ **Done 2026-10-28.** **2.i `query parse`.**
- ✅ **Done 2026-10-28.** **2.j `--param` (D13).**
- ✅ **Done 2026-10-28.** **2.k Docs.**
- ✅ **Done 2026-10-29.** **2.l Verification (driven binary).**

**Not in phase 2**: `plan …` (phase 6, although it shares the executor), `--var` (Open decision 4), the audit log (phase 8), `agent …` (phase 3 — hints still name `gossms help`), `--confirm` (no destructive *command* yet; `query run` relies on `--allow`).

### Phase 3 — Discovery: `db` (read), `object`, `agent`

- `db list/info`, `object list/describe/definition/dependencies/search/indexes/stats`.
- gosmo: search in definitions and columns.
- `agent guide` (+ `--brief`, `--topic`, `-p`), `agent schema`, `agent context` (capabilities probe → missing permissions).
- **Done when:** an agent given only the §8.6 system-prompt snippet can explore a schema end to end (§9 steps 1–2), and every example in the guide parses (test).

#### Phase 3 analysis (2026-10-29, against 23fe68d)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P3-1 | `Database.Search` matches **names only**, over `sys.objects` types `U,V,P,FN,IF,TF,TR` (no synonyms, sequences, types; no columns, no definitions), with `LOWER(…) LIKE LOWER(@p)` + `likeEscape` | gosmo gains `SearchDefinitions` (`sys.sql_modules.definition`) and `SearchColumns` (`sys.columns` of tables and views → schema, object, column, type), same LOWER/ESCAPE rule. `--in name` stays `Search` as is — gossms never calls it today, but it has other users (gosmo `CLAUDE.md`), so its type list is not widened; synonyms/sequences are found by `object list --name` instead |
| P3-2 | gosmo has **no kind-generic lookup by name**: `TableByName`, `ViewByName`, `StoredProcedureByName`, `UserDefinedFunctionByName`, `TriggerByName`, `SynonymByName`, `SequenceByName` are per family. `object definition/describe/dependencies <schema.name>` do not know the kind | gosmo gains `Database.ObjectByName(ctx, schema, name) (*ObjectInfo{ObjectID, Schema, Name, Type, TypeDesc, ParentSchema, ParentName}, error)` — one `sys.objects` read, `ErrNotFound` when absent. The handler dispatches on `Type` to the family's `…ByName` (a `Ref` would leave `ObjectID` zero; `Table.Columns` etc. `requireLoaded`) |
| P3-3 | No multipart-name parser exists (gosmo `tsql` has `QuoteName/QualifiedName/UnquoteName` only); `Sales.[Order.Detail]`, `[a]]b].x` are legal | A CLI parser over `sqltext` tokens (quoted identifiers stay one token): 1 or 2 parts, 3 parts only if the first is the connection's database (else usage error naming `-d`); `UnquoteName` each part. Fuzz-tested |
| P3-4 | A one-part name has no schema; gosmo refuses empty schemas (`requireSchema`) | D2: resolve one-part names against `sys.objects` across schemas — exactly one match → that one; several → usage error listing them; none → `NOT_FOUND` with a did-you-mean from `Search` (§5.4's hint, now possible) |
| P3-5 | Name comparison follows `Database.CatalogCollation` (a `_CS_` catalog: `sales.customer` ≠ `Sales.Customer`) | `ObjectByName` compares with the catalog's own `=` (server-side, as `OBJECT_ID` does); never case-fold in Go. The did-you-mean then catches the case-only miss |
| P3-6 | `Server.Databases` carries name, id, state, recovery, compat, collation, read-only, created, snapshot source, distributor — **no size**. The TUI's Databases pane gets size from one `SpaceUsed` round trip **per database** (`detail_browser_databases.go`) | D3: `db list` size |
| P3-7 | `Database.Options`, `SpaceUsed`, `Files`, `Parameters`, module `Definition` go through `procRead` (`[d].sys.sp_executesql`): an OFFLINE / RESTORING / no-access database fails them; `Server.DatabaseFiles` (master_files) answers for any state | `db info` on a database not ONLINE answers the `sys.databases` fields and lists the unreadable parts under `warnings` — never a hard error for the whole record |
| P3-8 | Per-family listings take `ObjectFilter{Name, Schema []TextCriterion}`, `TextContains/Equals/…` — no wildcards; `UserDefinedDataTypes/TableTypes/ClrTypes` and `Schemas` take no filter | `object list --name` is *contains* (SSMS filter semantics, documented; no `%`); for types/schemas the handler filters in Go with the same rule as `filterObjects` (trimmed, case-insensitive). `--type` absent = every family, one gosmo call each (≤ 9 round trips; fine for a CLI, no new gosmo listing) |
| P3-9 | Index **usage** (`sys.dm_db_index_usage_stats`) is read nowhere in gosmo; fragmentation is `Table.FragmentationStats(mode)` (`dm_db_index_physical_stats`, can be slow on big tables) | gosmo gains `Table.IndexUsage(ctx)` (seeks, scans, lookups, updates, last-use times; VIEW SERVER STATE, else the columns read NULL as `DatabaseFiles` does). Fragmentation is opt-in: `--fragmentation limited\|sampled\|detailed`, default off (D5) |
| P3-10 | `View` has `Columns`, `Indexes`, `Statistics` (carrier table); procedures/functions have `Database.Parameters` | `object describe` covers tables and views (columns, PK/FK, indexes, checks, defaults, triggers, row count for tables), and for modules prints parameters + return type instead of failing. `indexes`/`stats` take tables and views; anything else → usage error naming the kind |
| P3-11 | `db.RoleCLI` runs **no capability probe** (phase 1); `gosmo.Server.Capabilities` is one round trip, `Database…Capabilities` per database | `agent context` calls `Capabilities` itself; per-database only for the connection's database (not all of them) |
| P3-12 | Phase 0 D1: every pointer names `gossms help` until `agent guide` exists; `help -o json` is refused until `agent schema` (0.f); `TestCommandMentionsParse` fails on any hint naming a missing command | Phase 3 switches the three pointers (root `--help` first line, no-command usage error, `noTerminalMessage`) to `gossms agent guide` and turns `help -o json` into `agent schema`. The drift guard already enforces the ordering: the strings can only change in the substep that adds `agent guide` |
| P3-13 | Help is rendered outside the envelope (0.f); §8.1 gives `agent guide` its own `--format markdown\|text\|json`, which collides with the global `-o` | D6 |
| P3-14 | `spec` already holds summary, args, options, class, examples, enum, defaults, env; the exit-code table is `output.ExitCodes` | `agent schema` is a pure function of `spec.Tree` + `ExitCodes` + `output.Formats` — no hand-written catalog. A test walks the tree and fails on a command missing from the schema (§13) |
| P3-15 | `cmd/query.go` is 854 lines | New groups go in their own files (`cmd/db.go`, `cmd/object.go`, `cli/agent/`); shared `profileOption()`/connect plumbing moves to `cmd/common.go` rather than growing `query.go` past 900 |

**Decisions** — all eight settled 2026-10-29 as recommended (the alternatives are kept for the record)

- **D1 — where object commands look:** the connection's database (profile's, or `-d/--database`, already a connect option; 4060/4063 → exit 8 as in phase 1). No per-command `--db` argument. The envelope's `database` says which. *Alternative:* a required `<db>` positional like `db info` — noisier, and `-d` already exists.
- **D2 — one-part names:** resolve across schemas (P3-4); ambiguity is exit 2 listing `schema.name` candidates. *Alternative:* assume `dbo` — silently wrong for `Sales.Customer`-style databases.
- **D3 — `db list` size:** add gosmo `Server.DatabaseSizes(ctx) map[int]DatabaseSize{DataMB, LogMB}` — one `sys.master_files` read (any state; a database the login cannot see reads absent → `size` NULL, not an error). *Alternative:* leave size to phase 4 and drop it from `db list` now (§4.3 lists it).
- **D4 — definition output:** `object definition` returns a record (`schema`, `name`, `type`) and the definition as **one** field printed verbatim in text (not single-lined, the only such field — a `KEY: value` block cannot hold it) after a `=== DEFINITION ===` marker; JSON a string. `--max-col-width` does not apply. Encrypted module → `definition: null` + warning, exit 0. *Alternative:* a one-row set — `--max-col-width` would cut it and newlines would be flattened.
- **D5 — `object indexes`:** structure + usage by default; fragmentation only with `--fragmentation <mode>` (it scans). *Alternative:* always `LIMITED` — cheap on most tables but unbounded on a heap of billions of rows.
- **D6 — `agent guide` format:** the guide is help-like text outside the envelope, markdown by default (`-o text` = same content, markdown markup stripped to plain lines; `-o json` = the envelope with `data.sections[]{id, title, body}`). No separate `--format` (§8.1 corrected). *Alternative:* keep `--format` and reject `-o` on this command — two spellings of one idea.
- **D7 — `object dependencies --depth`:** `--depth 1` default, max 5, BFS over gosmo `Dependencies`/`Dependents` with a visited set (cycles via synonyms/caller-dependent edges), one row per edge with `level`. *Alternative:* depth 1 only, defer `--depth`.
- **D8 — guide content ownership:** rules and recipes as `internal/cli/agent/guide/*.md` (`go:embed`); everything else generated from `spec`. Recipes may only name implemented commands — they pass the same `checkMentions` drift guard, so §9's recipe that uses `plan estimated`/`monitor querystore` waits for phases 6/7 (only steps 1–2 now).

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-29.** **3.a gosmo additions**
- ✅ **Done 2026-10-29.** **3.b CLI plumbing:**
- ✅ **Done 2026-10-29.** **3.c `db list` / `db info`:**
- ✅ **Done 2026-10-29.** **3.d `object list` / `object search`:**
- ✅ **Done 2026-10-29.** **3.e `object describe` / `object definition`:**
- ✅ **Done 2026-10-29.** **3.f `object dependencies` / `indexes` / `stats`:**
- ✅ **Done 2026-10-29.** **3.g `agent schema` + `help -o json`:**
- ✅ **Done 2026-10-29.** **3.h `agent guide`:**
- ✅ **Done 2026-10-29.** **3.i `agent context -p`**
- ✅ **Done 2026-10-29.** **3.j Docs:**
- ✅ **Done 2026-10-29.** **3.k Verification (driven binary):**

**Not in phase 3**: sizes beyond `db list`'s total (`db files/space/log-usage/table-sizes`, phase 4), `db set`/`scoped-config` (phase 5), `script object` (phase 6 — `object definition` is the module text, not Script As), `object` writes of any kind, §9 steps 3–5 in the guide's recipes (their commands do not exist yet).

### Phase 4 — Storage and size reporting

- `db files/space/log-usage/table-sizes`, `server volumes`; warnings; units in column names.
- gosmo: log reuse wait + VLFs; table sizes if needed.
- **Done when:** sizes match `sys.database_files`, `sp_spaceused` and `sys.dm_db_log_space_usage` on all three majors (§13).

#### Phase 4 analysis (2026-10-29, against 2647982 + gosmo eab73e8)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P4-1 | gosmo has two file reads: `Database.Files` (`sys.database_files` through the database — filegroup, `SpaceUsedKB` from `FILEPROPERTY`, ONLINE + access only) and `Server.DatabaseFiles` (`sys.master_files` — any state, `FileGroup` always "", no used space, `SizeOnDiskKB` only with VIEW SERVER STATE). `FILEPROPERTY` answers for the current database only, so there is no one-round-trip "used space of every file on the server" | `db files <name>` = `Database.Files`; a database not ONLINE (or not accessible) falls back to `DatabaseFiles` with `filegroup/used/free/free_pct` NULL and one warning (D3). `--all` is one `Database.Files` per accessible ONLINE database (N round trips, accepted — the same as SSMS's reports), skipped ones via `Server.DatabaseAccess` (3.i), never a hard error |
| P4-2 | `Server.DiskVolumes` groups `dm_os_volume_stats` by volume: no file count, no file → volume mapping, so neither `server volumes`' `db_files` nor `db files`' `volume_free_pct` can be built from it. The DMV is refused without VIEW SERVER STATE (Msg 300). The sanity rule that drops nonsense rows (total 0, available > total — Azure MI) and dedups by mount point + name is `usableDiskVolumes` in **`internal/tui/detail_browser_server.go`**, unreachable from the CLI | gosmo gains `Server.FileVolumes(ctx) ([]FileVolume{DatabaseID, FileID, MountPoint, VolumeName, TotalBytes, AvailableBytes}, error)` — one read, gated with the `HAS_PERMS_BY_NAME` branch `DatabaseFiles` uses (no permission → no rows and `Visible=false`, not an error; D7). The plausibility rule moves to gosmo as a method on the volume figures; the TUI's `usableDiskVolumes` calls it (no behavior change). `DiskVolumes` stays as is (other users) |
| P4-3 | Neither database-level read reproduces `sp_spaceused`. `SpaceInfo` (`SpaceUsed`) has total/data/log/unallocated/available-log, no reserved/data/index/unused. `DiskUsage` has data/index/unused but counts `used_pages` for index 0/1 (IAM and B-tree upper levels land in *data*) and no internal tables rule; `sp_spaceused` counts `data_pages` for in-row, `used_pages` for LOB/overflow of index < 2, puts the rest of `used_pages` into *index*, and zeroes the internal-table types (XML index, full-text, …) out of *data* | D1. The §13 criterion ("match `sp_spaceused`") is not met by any existing read |
| P4-4 | Table level is the same mismatch: `TableSpaceUsedAll` (keyed by `object_id`, no schema/name/rows) splits `Data/Index/LOB/Unused` by allocation-unit type, `sp_spaceused <table>` folds the base table's LOB into *data*, a nonclustered index's LOB into *index*, uses `data_pages` for in-row, and adds the object's internal tables (XML/spatial index, full-text) to reserved/used. Rows: `TableRowCounts` (`sys.partitions`, index 0/1) = `sp_spaceused`'s `dm_db_partition_stats.row_count` | D1: a new gosmo read in `sp_spaceused`'s arithmetic with schema, name and rows in one round trip. `TableSpaceUsedAll` stays — the TUI's Tables pane uses it (its figures then differ from the CLI's; say so in both doc comments) |
| P4-5 | gosmo reads no log figure at all: `log_reuse_wait_desc` is not on `gosmo.Database`; `sys.dm_db_log_space_usage` answers for the **current** database only; `sys.dm_db_log_info(db_id)` (VLFs) and `dm_db_log_stats` exist from **2016 SP2** (13.0.5026) — gosmo's floor is major 13, and the only 2016 test instance is SP3, so RTM/SP1 cannot be driven. Permissions: VIEW DATABASE STATE (2022: VIEW DATABASE PERFORMANCE STATE) — to be confirmed live with the read-only login, not assumed | D2 |
| P4-6 | `Database.query` runs inside the database (3.c: OFFLINE/RESTORING/no-access fail). tempdb: `master_files` holds startup sizes, `database_files` the current ones (`TempDBFiles`' comment); a snapshot's `size` is a ceiling, its real footprint is `SizeOnDiskKB` | `db files/space/log-usage` read `database_files`-side, so tempdb is current. A snapshot's files get one warning with the on-disk size when `DatabaseFiles` can read it; `db log-usage` lists no snapshot (it has no log), `--all` skips them silently (not unreadable — logless) |
| P4-7 | `type_desc` has four values: ROWS, LOG, FILESTREAM, FULLTEXT; a FILESTREAM container has no pages (`SpaceUsedKB` 0 by design, `size` meaningless), `max_size`/`growth` do not apply | `type` column `data\|log\|filestream\|fulltext`; `--type` enum takes all four; a FILESTREAM row's `used/free/free_pct/growth/max_size` are NULL and it raises no free-space warning |
| P4-8 | 3.c already formats sizes (`mb`, `mbf`, `decimal(18,2)` columns); `agent schema` describes options, not result columns | `--unit` renames columns per call (`size_mb` → `size_gb`) without touching the schema. Filters (`--max-free-pct`) and `--sort-by` work on exact KB, never on the rounded text (D5) |
| P4-9 | There is no `server` group yet; phase 5 adds `server config`. The guide's group topics, `checkMentions` and `TestAgentContextNamesOnlyCommands` reject any mention of a command that does not exist | Phase 4 adds the `server` node with `volumes` only; its summary names only volumes until phase 5 widens it. `permissionNeeds` (`cmd/agent.go`) gains rows: VIEW SERVER STATE → `server volumes`, `db files` (volume column); VIEW DATABASE STATE → `db log-usage`; VIEW ANY DEFINITION → `db files --all` on a non-ONLINE database (master_files fallback) |
| P4-10 | `cmd/db.go` is 267 lines but gains four commands with a shared unit/`--all`/warning layer; `object.go` is already 876 | New `cmd/db_storage.go` (files, space, log-usage, table-sizes + the shared layer) and `cmd/server.go`; ARCHITECTURE package-map rows in the same substep |
| P4-11 | `spec` has no "exactly one of positional / flag" rule; §4.3 gives `db files/space <name\|--all>` but `db log-usage [<name>\|--all]` | D6 |

**Decisions** — all seven settled 2026-10-29 as recommended (the alternatives are kept for the record)

- **D1 — `sp_spaceused` fidelity:** gosmo gains `Database.SpaceReport(ctx) (SpaceReport, error)` (database_size, data/log file sizes and used, unallocated, reserved, data, index, unused — `sp_spaceused`'s arithmetic, read from its source on 17 via `sp_helptext`, not from memory) and `Database.TableSizes(ctx) ([]*TableSize{ObjectID, Schema, Name, Rows, ReservedKB, DataKB, IndexKB, UnusedKB}, error)` (per-object `sp_spaceused` arithmetic incl. internal tables, every user table in one round trip; `--top/--schema/--sort-by` applied in Go). Live tests compare both to `sp_spaceused` output on 13/14/17. *Alternative:* reuse `SpaceUsed`/`DiskUsage`/`TableSpaceUsedAll` and reword the done-when to "matches gosmo's TUI figures" — cheaper, but an agent cross-checking with `sp_spaceused` sees different numbers and no reason.
- **D2 — log figures:** gosmo gains `Database.LogSpace(ctx) (LogSpace{SizeKB, UsedKB, ReuseWait string, VLFs, ActiveVLFs int, VLFsKnown bool}, error)` — one read inside the database: `dm_db_log_space_usage` + `sys.databases.log_reuse_wait_desc` + `COUNT(*)`/`SUM(vlf_active)` from `dm_db_log_info(DB_ID())`, the last only when the server is ≥ 13.0.5026 (else `VLFsKnown=false` → `vlf_count` NULL + warning). `--all` loops ONLINE accessible databases. `DBCC LOGINFO` is not used (undocumented, sysadmin). *Alternative:* `DBCC SQLPERF(LOGSPACE)` — one call for all databases, but VIEW SERVER STATE, no reuse wait or VLFs, and its used % is not `dm_db_log_space_usage`'s (the §13 comparison).
- **D3 — unreadable databases under `--all`:** `db files` lists them from `master_files` with the database-side columns NULL + one warning each (the paths are what someone needs when a database is in trouble); `db space`, `db log-usage` list them only under warnings (they have no figures to show). Named `<name>` that is unreadable: `db files` the same fallback, exit 0; `db space`/`log-usage` exit 8 with the state in the message. *Alternative:* warnings-only for all three — loses the paths.
- **D4 — warnings:** fixed thresholds, documented in each command's description, no options: file free < 10 % (data and log files only, growth or not — a fact, per §4.3), size ≥ 90 % of a capped `max_size`, percentage autogrowth, volume free < 10 %, `log_reuse_wait_desc` ≠ NOTHING (transient waits such as CHECKPOINT included — a fact, not a judgement). Warnings only for rows the filters keep; one warning per (file, reason). *Alternative:* `--warn-free-pct <n>` — an option nobody asked for.
- **D5 — units:** `--unit kb|mb|gb` (enum, case-insensitive like every enum), default `mb`; `kb` prints integers, `mb`/`gb` two decimals; `*_pct` always two decimals; JSON numbers stay `decimal(18,2)` strings as 3.c's `size_mb` (one typing rule). *Alternative:* `KB\|MB\|GB` upper-case only, as §4.3 spells it — the parser's enums are lower-case everywhere else.
- **D6 — argument shape:** `db files`, `db space`, `db log-usage` take exactly one of `<name>` or `--all` (neither or both → exit 2 naming both); `db table-sizes <name>` required, no `--all` (§4.3). §4.3's optional form for `log-usage` corrected. *Alternative:* default to the connection's database as phase 3 D1 does for `object` — but the `db` group already takes the database positionally (`db info <name>`).
- **D7 — volumes without VIEW SERVER STATE:** `server volumes` → exit 9 with a hint naming the permission (the whole command is the DMV); `db files` → `volume_free_pct` NULL + one warning, exit 0 (the rest of the record is still right). `server volumes` columns: `volume` (mount point, else volume label, else the sample path — as the TUI shows it), `total_<u>`, `free_<u>`, `free_pct`, `db_files`, after the plausibility rule. *Alternative:* exit 9 for `db files` too — refuses the useful 90 % of the answer.

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-29.** **4.a gosmo additions**
- ✅ **Done 2026-10-29.** **4.b CLI storage plumbing**
- ✅ **Done 2026-10-29.** **4.c `db files`:**
- ✅ **Done 2026-10-29.** **4.d `db space`:**
- ✅ **Done 2026-10-29.** **4.e `db log-usage`:**
- ✅ **Done 2026-10-29.** **4.f `db table-sizes`:**
- ✅ **Done 2026-10-29.** **4.g `server volumes`:**
- ✅ **Done 2026-10-29.** **4.h Agent and docs:**
- ✅ **Done 2026-10-29.** **4.i Verification (driven binary):**

**Not in phase 4:** shrink or any file write (`maint`, phase 8), `db set` (phase 5), tempdb contention/version-store detail (`TempDBSpace` — `monitor`, phase 7), backups' sizes (phase 8), changing the TUI's figures to `sp_spaceused`'s.

### Phase 5 — Server configuration

- `server info/config list/get/set/reset/memory/traceflag/log`, `db scoped-config list/get/set`, `db set --option`.
- gosmo: `sp_configure` defaults table, trace flags.
- **Done when:** the §10.1–§10.6 examples run as written (dry-run batch from `ApplyConfiguration`, risky-value blocks, `restart_required`).

#### Phase 5 analysis (2026-10-29, against 1d9c274 + gosmo 6134e7f)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P5-1 | Phase 5 brings the CLI's **first gosmo-backed writes**. Today `--allow` and `--dry-run` are `query run`'s own options (`cmd/query.go`), `Main` never gates on `Command.Class` (every command so far is `read`), `gosmo.WithScript` is used nowhere in `internal/cli`, and the envelope has no dry-run status and no `CONTEXT` section (`output.Status` = ok/error/blocked/cancelled; `Result` has `Fields` and `Sets`) | A shared write layer comes before any write command (5.b): D1, D2 |
| P5-2 | `--force` is already a **global** option, "Replace an existing --out-file" (`render.go`) | §4.2's `server config set --force` collides with it: D3 |
| P5-3 | `ApplyConfiguration` takes exact `sys.configurations` names; a bad name or out-of-range value fails only its own `sp_configure`, the `RECONFIGURE` still installs the rest, and the error names the failures. `Configurations` is one read with min/max/`is_dynamic`/`is_advanced`, and lists advanced options whatever `show advanced options` is set to | The CLI resolves aliases and names and checks min/max against one `Configurations` read **before** the batch (exit 2, nothing runs), so a partial apply can only come from the server. After the batch it reads again: before/after come from the two reads, and `restart_required` = `value ≠ value_in_use` in the second read, not `is_dynamic` alone (that also catches a dynamic option `RECONFIGURE` refused). A partial failure exits 5 and still prints the CHANGE table of what landed |
| P5-4 | There are no defaults anywhere (R11). The engine defaults are not what Setup writes: since 2019 Setup recommends MAXDOP and max server memory (and `mssql-conf` on Linux), Agent turns `Agent XPs` on itself, and CUs add options (2022 and 2025 added several) | D4 |
| P5-5 | `ServerInfo` (cached at connect, no round trip) holds version, level, edition, engine edition, platform, collation, clustered, HADR, default paths, login, and physical memory + logical CPUs (`SysInfoUnavailable` without VIEW SERVER STATE). gosmo never reads `sqlserver_start_time`, so there is no uptime. The NUMA node count is `ProcessorInfo` (`dm_os_schedulers`, VIEW SERVER STATE) | `server info` = `ServerInfo` + `ProcessorInfo` + one new gosmo read (D5). Without VIEW SERVER STATE those fields are NULL with one warning, exit 0 (phase 4 D7: the rest of the record stands) |
| P5-6 | Every `server memory` source exists: `MemoryStats` (physical, available, target, total), `MemoryClerks` (per type), `PerformanceCounters` (Buffer Manager *Page life expectancy*, Memory Manager *Memory Grants Pending*), and min/max from `Configurations`. `internal/activity/memory.go` folds clerks into display groups for the TUI chart | The CLI lists raw clerk types, top 10 by size (§10.4), and does not reuse activity's grouping. Every source except min/max is a DMV, so without VIEW SERVER STATE the command fails with exit 9, like `server volumes`. PLE is the instance-wide Buffer Manager row, not the per-node Buffer Node rows |
| P5-7 | gosmo has no trace-flag code (R12). `DBCC TRACESTATUS(-1)` returns TraceFlag/Status/Global/Session. TRACEON/TRACEOFF need sysadmin. A session-scoped flag dies with the CLI's connection | D6 |
| P5-8 | `ReadLog(ctx, type, n, LogSearch{Text1, Text2, From, To})` filters on the server, returns oldest first, reads one archive per call, and its dates are the server's local wall clock "carried in UTC" (the log stores no zone). The TUI's log viewer already calls it | `--lines` is a tail taken in Go after the filtered read. D7 covers `--since` and archives |
| P5-9 | Scoped configuration: `DatabaseScopedConfigs` runs inside the database. `IsValueDefault` is false on 2016, which has no such column. `ValueType` tells a bit from an int from a keyword. `SetDatabaseScopedConfig(name, value, replica)` checks only the token shape and runs USE + ALTER through `d.exec` | `get`/`set` check the name against the list first (exit 8, naming up to 3 close matches). `is_default` is NULL on 13. ON/OFF goes through as typed for a bit option (SQL Server accepts both forms: verify live on 13). The database must be ONLINE and readable (the phase 4 `storageDatabase` rules: 8 for its state, 9 for access) |
| P5-10 | `db set` has no single gosmo entry point. `SetDatabaseOption` allowlists 21 SET options (PAGE_VERIFY is among them; RECOVERY, COMPATIBILITY_LEVEL, READ_ONLY and QUERY_STORE are not). The others are separate: `SetRecoveryModel`, `SetCompatibilityLevel` (no list of valid levels), `SetReadOnly(term)`, `SetUserAccess`, and `QueryStore().SetOptions(full struct)`. READ_COMMITTED_SNAPSHOT, CONTAINMENT and READ_ONLY need exclusive access and wait forever under `TerminationNone` | D8 |
| P5-11 | The classifier puts `sp_configure`, `RECONFIGURE`, `ALTER DATABASE` and `DBCC TRACEON` in `admin`. `query run --dry-run` still checks the class | Every phase 5 write is `ClassAdmin` (§4.2, §4.3). `--dry-run` still needs `--allow admin`, as in query run and §10.1's example |
| P5-12 | `permissionNeeds` (`cmd/agent.go`), `checkMentions` and `TestAgentContextNamesOnlyCommands` reject commands that do not exist. The `server` group summary names volumes only (P4-9) | Each substep widens the summary to the commands it adds. Rows to add in 5.j: ALTER SETTINGS → `config set/reset`; sysadmin → `traceflag on/off`; VIEW SERVER STATE → `server memory` (exit 9) and `server info` / `config get` context (NULL fields); the error log's permission (securityadmin or VIEW SERVER STATE: **verify live** with the read-only login, do not assume) → `server log` |
| P5-13 | `server.go` is 154 lines, `db.go` 271, `db_storage.go` 924 | New files: `cmd/write.go` (the shared layer), `cmd/server_info.go` (info, memory), `cmd/server_config.go`, `cmd/server_traceflag.go`, `cmd/server_log.go`, `cmd/db_settings.go` (db set, scoped-config). ARCHITECTURE package-map row in the substep that adds each |

**Decisions** — all ten settled 2026-10-29 as recommended (the alternatives are kept for the record)

- **D1 — write gate:** `cmd/write.go` gains `writeOptions()` = `--allow` (enum, default `read`) + `--dry-run`, added only to writing commands, and `d.gate(inv, class)`, which runs before connecting. It uses `safety.Policy.Allowed()` and the hint wording query run uses, so a refusal is BLOCKED with exit 7 and `DETECTED_CLASS`/`ALLOWED_CLASS` (no `STATEMENTS`, which is §10.1's shape). There is also a `Writer` interface beside `Catalog` (gosmo writes; a fake in tests). *Alternative:* global `--allow`/`--dry-run` as §3.2 has them, which makes every read command accept a flag that does nothing, and `agent schema` would advertise it there.
- **D2 — dry-run envelope:** status stays `ok` with a `dryRun: true` field. The T-SQL is one `Verbatim` field (`ScriptCollector.String()`, gosmo's exact batch) and PLANNED CHANGE is a set. §10.1's `CONTEXT` block is record `Fields` beside the set. §10.1/§10.4's `STATUS: DRY_RUN` changes to `STATUS: OK` + `DRY_RUN: yes`. *Alternative:* a new `StatusDryRun`, which touches every renderer and the schema's status enum, and query run's `--dry-run` (status ok) would then disagree unless it changed too.
- **D3 — risky-value override:** a command-local `--accept-risk` on `config set/reset`; `--force` keeps its global out-file meaning. *Alternative:* `--force` for both, one flag that lifts two unrelated checks.
- **D4 — defaults:** gosmo adds `ConfigurationOption.Default int64` + `HasDefault bool`, filled by `Configurations`/`ConfigurationByName` from a per-major table (13, 14, 15, 16, 17). The table holds the **engine** default (MAXDOP 0, max server memory 2147483647), not Setup's recommendation, and the command description says so. It is built from Microsoft's sp_configure reference and checked against the live instances (they are not fresh installs, so every difference is explained, not copied). Azure MI is unknown and unavailable, so `HasDefault` is false there. An option missing from the table (a new CU) gets `default` NULL, stays out of `--non-default` and gets one warning naming them; `reset` of it exits 11 with a hint to use `set`. *Alternative:* the defaults table in gossms, against §14.6 and gosmo-first, with nothing for the TUI to share.
- **D5 — uptime:** gosmo adds `Server.StartTime(ctx) (time.Time, error)` from `dm_os_sys_info.sqlserver_start_time` (VIEW SERVER STATE). `server info` gets `start_time` + `uptime_s`. *Alternative:* `tempdb`'s `create_date`, which needs no permission but trails the start by however long recovery took.
- **D6 — trace flags:** gosmo adds `Server.TraceFlags(ctx) ([]TraceFlag{Flag int, Global, Session bool}, error)` and `Server.SetTraceFlag(ctx, flag int, on bool)`, always global (`-1`). The CLI drops `--global` (§4.2, §10.6), because a session flag cannot outlive a one-shot process. `on`/`off` read TRACESTATUS again afterwards. Whether TRACEON of a startup-only or unknown flag errors or silently does nothing is to be **verified live**; if it does nothing, a warning fires when the second read does not show the flag. The output adds a note that the flag is lost at restart.
- **D7 — server log:** `--since` takes `YYYY-MM-DD[THH:MM[:SS]]` with **no zone**, read as the server's local time (the log has none). A `Z` or an offset is exit 2 with that explanation (§10.14's example to be fixed). `--search` is repeatable up to 2 (Text1 AND Text2, xp_readerrorlog's semantics). `--archive <n>` (default 0) reads older files. `--lines` (default 100, 0 = all) is a tail. `--agent` reads the Agent log, with a `level` column in place of `process`. Columns: `date` (`2006-01-02 15:04:05.000`, no zone), `process`|`level`, `text`. *Alternative:* walk every archive back to `--since`, which is N reads of possibly huge files on a busy server.
- **D8 — `db set` keys:** a fixed CLI table maps each key to its gosmo call: the 21 `SetDatabaseOption` keys; `RECOVERY=FULL|SIMPLE|BULK_LOGGED` → `SetRecoveryModel`; `COMPATIBILITY_LEVEL=<n>` → `SetCompatibilityLevel` (100…the server's level, step 10, checked in the CLI); `READ_ONLY=ON|OFF` → `SetReadOnly`; `QUERY_STORE=ON|OFF|READ_ONLY` → read `QueryStore().Options`, change `DesiredState`, `SetOptions` (the current options re-emitted unchanged; check that this really is a no-op on 13). An unknown key is exit 2 listing the keys. Values are upper-cased and checked per key. Options apply in command-line order, one statement each, with no transaction (ALTER DATABASE cannot run in one). The first failure stops the run, and the ones already applied are reported (exit 5). Exclusive-access options use `TerminationNone`, bounded by `--timeout`, with a hint on timeout. `--rollback-immediate` needs `--confirm <db>` (§6.3: it kills sessions). Before/after come from `Options` + `DatabaseByName` read before and after. USER_ACCESS, OFFLINE and ONLINE are out of scope (phase 8). *Alternative:* pass `KEY=VALUE` through as raw `ALTER DATABASE … SET` text, which bypasses gosmo, its allowlists and WithScript fidelity.
- **D9 — names and filters:** the alias table lives in the CLI (`cmd/server_config.go`), §4.2's 12 entries. A name resolves as an alias (case-insensitive), then as an exact `sys.configurations` name (case-insensitive); otherwise exit 8 with up to 3 substring matches. `--name` is a case-insensitive substring, as in `object list --name`; §10.5's `"*backup*"` becomes `backup`. `--advanced` keeps only advanced options. `--non-default` needs D4.
- **D10 — units and risk:** a unit suffix is accepted only by options whose name carries a unit: `(MB)` options take `MB|GB|TB`, `(KB)` options (`min memory per query (KB)`) take `KB|MB`. The suffix is case-insensitive and the result must be a whole number in the native unit (`1.5GB` = 1536 is accepted; `1.3MB` is exit 2). The blocks (exit 7, lifted by `--accept-risk`) are a fixed, documented list: max-memory above physical memory; max-memory below min-memory (taking the values the same call also sets); min-memory above physical; maxdop above the logical CPUs. Unknown physical memory (no VIEW SERVER STATE) blocks too, failing closed, with a hint; serveradmin, the role that can set them, has the permission anyway. Warnings that are facts only: a new max below committed memory (§10.4), and a value that needs a restart.

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-29.** **5.a gosmo additions**
- ✅ **Done 2026-10-29.** **5.b CLI write layer**
- ✅ **Done 2026-10-29.** **5.c `server info` + `server memory`**
- ✅ **Done 2026-10-29.** **5.d `server config list/get`**
- ✅ **Done 2026-10-29.** **5.e `server config set/reset`:**
- ✅ **Done 2026-10-29.** **5.f `server traceflag list/on/off`**
- ✅ **Done 2026-10-29.** **5.g `server log`**
- ✅ **Done 2026-10-29.** **5.h `db scoped-config list/get/set`**
- ✅ **Done 2026-10-29.** **5.i `db set --option`:**
- ✅ **Done 2026-10-29.** **5.j Agent and docs:**
- ✅ **Done 2026-10-29.** **5.k Verification (driven binary)**

**Not in phase 5:** restarting the service; startup trace flags and startup parameters (Configuration Manager / `mssql-conf`); `ALTER DATABASE SCOPED CONFIGURATION CLEAR PROCEDURE_CACHE`; USER_ACCESS, OFFLINE and ONLINE (phase 8); Query Store options other than its state; the audit log (phase 8); changing the TUI's Server Properties to show defaults (possible once D4 lands, not asked).

### Phase 6 — Plans, scripting, data

- `plan estimated/actual` (+ `summary` digest from `internal/showplan`), `script object/db`, `data top/count/sample/profile/export/import`.
- **Done when:** `script object` output equals the TUI's Script As for the same object, and an export → import round trip preserves NULLs and empty strings.

#### Phase 6 analysis (2026-10-29, against 0da5e50 + gosmo 912ec30)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P6-1 | `Session.ExecuteEstimatedPlan` / `ExecuteWithPlan` exist, but neither takes a sink: `ExecuteWithPlan` buffers every row in `Result.Sets`, and `WithMaxRows` would cancel the run (R6). `runScript` already pulls showplan sets out before the sink sees them (`isShowplanResultSet`), so capture + sink needs only an exported entry. The CLI's `Session` interface (`cmd/query.go`) has neither plan method | 6.a adds the entry (D1) |
| P6-2 | Every check `plan actual` needs (SQL input, `--param`, classify + gate, `--transaction`/`--max-rows-affected`, stop-on-error, timeout, `endTransaction`) is inline in `queryRun`; `query.go` is 838 lines | The run core is factored out of `queryRun` so `plan actual` is query run with a capture mode, not a copy. Plan commands go in a new `cmd/plan.go` |
| P6-3 | `internal/showplan` already parses what a digest needs: per statement cost, DOP, `TimeStats`, `MemoryGrant`, `MissingIndexes` (+ `CreateStatement()`), `Warnings`; per node `Cost(total)`, est/actual rows, `Runtime`, `Warnings`, `Object.Short()`. The TUI's operator summary ranks with `summaryRow`/`nodeRows`/`nodeTime` (`tui/planview/summary.go`), pure functions in a tcell package | Move those helpers into `showplan` so the CLI digest and the TUI summary rank operators the same way (6.a) |
| P6-4 | SHOWPLAN_XML compiles and runs nothing, but needs the **SHOWPLAN** permission in the database (Msg 262, gosmo `PermissionDenied` → exit 9) plus rights on the objects. A statement that uses a `#temp` created earlier in the same script fails to compile (Msg 208), as it does in SSMS. A `SET SHOWPLAN_*`/`NOEXEC`/`PARSEONLY` in the script would switch the mode itself; the classifier already flags these as `mode-option` | `plan estimated` is `read` whatever the script holds (§6.1), and it refuses mode options (exit 7) as query run does. The Msg 208 case gets a hint (`plan actual`, or create the table first) |
| P6-5 | Script As is `internal/tui/scripting.go` (651 lines): a `NodeType` → verbs table of about 60 families, `ddl()` that adds `DatabaseContext()` (USE + GO) to everything except a database, `DefaultScriptOptions()` (headers, schema-qualify, if-not-exists, ANSI_PADDING, extended properties), system objects not offered, `editionRefusesScriptVerb` (Azure). The CLI cannot import `internal/tui` (`imports_test.go`) | Parity is the done criterion, so the generator moves to a shared headless package (D5) |
| P6-6 | gosmo `ScriptOptions` has seven fields: `Verb`, `IncludeHeaders`, `IncludeIfNotExists`, `ScriptDrops` (legacy), `SchemaQualify`, `AnsiPadding`, `ExtendedProperties`. `ScriptTable` always emits indexes, FKs and checks. The verbs are Create, Drop, DropAndCreate, Alter; there is no CREATE OR ALTER. The row templates are `ScriptSelect/Insert/Update/Delete`, EXECUTE for a procedure, SELECT for a function | §4.5's `--include-indexes/--include-triggers/--include-fk` and `create-or-alter` have nothing behind them (D6) |
| P6-7 | No multi-object scripting exists: no Generate Scripts wizard in gossms, and no dependency-ordered "script everything" in gosmo. `ScriptDatabase` is the database's own CREATE (files, filegroups, options, owner) | `script db --objects …` would be a new gosmo feature (D7) |
| P6-8 | The global `--out-file` already streams every row of any command to `.csv/.tsv/.jsonl/.json/.md/.txt`, chosen by extension, through `export.RecordWriter` / the jsonl writer, with `--force` | `data export` is a SELECT through the existing sink. §4.8's `--format` would duplicate the extension rule (D10) |
| P6-9 | The CSV NULL convention (`export.RecordWriter`, decisions § SQL NULL in results): NULL is an empty field, an empty string is `""`, and the string `NULL` is written bare. `encoding/csv`'s Reader cannot tell `""` from an empty field, and there is no reader in `internal/export`. A NULL in a **one-column** row is written `""`, the same as an empty string. jsonl keeps `null` vs `""` and carries column types in its `_resultSet` line | Import needs its own quote-aware reader beside the writer (6.i). The one-column CSV case cannot round-trip, so it is documented and the hint points to jsonl |
| P6-10 | Cells are grid text: per-type date/time layouts and scales, money `#0.00##`, float as shortest round-trip, binary as `0x` uppercase hex, GUID uppercase, bit 1/0. `sql_variant` loses its base type, and CLR UDTs (geography, geometry, hierarchyid) come out as hex. `Database.BulkInsert` takes `[]any`. go-mssqldb v1.11.2 bulk copy accepts a **string** only for char/nchar, date/time (fixed layouts), money and decimal. int, bit, float, binary and **GUID (wire byte order `[]byte`)** need Go values. The driver's `BulkOptions` has **no KEEPIDENTITY**. Without `KeepNulls`, a NULL becomes the column's DEFAULT. Under `WithScript`, BulkInsert refuses (`ErrUnsupported`); it also refuses inside a transaction. Nothing in gossms calls it yet | Import converts text to a typed value per destination column, the inverse of `query/cells.go` (6.i). `KeepNulls` is always on (otherwise the NULL round trip fails on any column with a default). **Verify live** whether `SET IDENTITY_INSERT ON` on the pinned session keeps the identity values of an INSERT BULK; if it does not, gosmo needs a KEEPIDENTITY path (6.i). Also verify live that one INSERT BULK with no `RowsPerBatch` is all-or-nothing |
| P6-11 | `data top/count/sample/profile` have no gosmo or gossms counterpart except the TUI's literal `SELECT TOP 1000 * FROM <fqn>` (`explorer_objects.go`). `--where`, `--order-by` and `--columns` are T-SQL fragments from the caller, spliced into a statement. `Catalog.RowCount` (partition stats) already exists | D8, D9 |
| P6-12 | §6.1: `data import` is `write`; `--truncate-first` is `ddl` (`Table.Truncate` exists; it needs ALTER on the table) and destructive (§6.3). TRUNCATE and the load cannot share a transaction (BulkInsert refuses one) | `--truncate-first` needs `--confirm <schema.table>`. A load that fails after the truncate leaves the table empty, and the hint says so |
| P6-13 | The brief guide is one row per group (4121 bytes), so three new groups fit. §9's recipe step 3 has waited for `plan estimated` since phase 3 (D8). `permissionNeeds` has no SHOWPLAN, VIEW DEFINITION or table-level rows | 6.k |

**Decisions** — all eleven settled 2026-10-29 as recommended (the alternatives are kept for the record)

- **D1 — plan capture with a sink:** `query` gains `Session.ExecuteToSinkWithPlan(ctx, script, mode gosmo.PlanMode, sink, opts...)`, the one missing combination; the CLI `Session` interface gains it and `ExecuteEstimatedPlan`. *Alternative:* a `WithPlanCapture` Option. Options set `*Result` fields, but capture is a parameter of `execute`, so this would be a second mechanism.
- **D2 — `plan actual` = query run + capture:** it takes the same options and gate (`--allow`, `--transaction`, `--max-rows-affected`, `--param`, `--continue-on-error`, `--timeout`, `--dry-run` = classify only). **Rows are counted, not returned** (a counting sink), and each set's count is reported: the agent wants the plan, and `query run` returns rows. *Alternative:* return rows under `--max-rows` as query run does. That doubles the output size for every plan.
- **D3 — `plan estimated` class:** always `read`, with no `--allow`. Mode options are refused (exit 7). ~~`--param` is supported.~~ **Verified live (6.b): SHOWPLAN_XML over `sp_executesql` returns only the EXEC's statement, so `plan estimated` has no `--param`.**
- **D4 — plan formats:** `--plan-format summary|tree|xml`, default summary. **summary:** set `statements` (`n type cost est_rows actual_rows elapsed_ms cpu_ms dop grant_kb warnings text`, text cut to 200 chars), set `operators` (top `--top 10` by cost % across statements: `stmt node physical logical object cost_pct est_rows actual_rows parallel mode warnings`, with a row-misestimate flag when actual/est > 10× either way), set `missing_indexes` (`stmt impact table equality inequality include create_statement`), set `warnings`. **tree:** one set, preorder, `depth` + the operator columns. **xml:** a Verbatim `planXml` field (`Plan.XML`). `--plan-file <path>` writes what the TUI's Save Execution Plan As writes (6.b reads which encoding that is). *Alternative:* a `tree` drawn with leading spaces. The text renderer's one-line rule makes indentation unreliable, so `depth` is a column.
- **D5 — one Script As:** a new headless `internal/scripting` holds the generators: family → gosmo binding, the verbs each family offers, the USE prefix rule, `DefaultScriptOptions`, and the system-object and Azure refusals. `tui/scripting.go` keeps the menus, labels and destinations, and maps `NodeType` → family. The CLI maps `ObjectInfo.Type` → family. Parity holds by construction, and the TUI's existing script tests guard the move. *Alternative:* the CLI calls gosmo directly, with a parity test. That is two tables to keep in step, and the test would sit in `tui` importing `cli`.
- **D6 — `script object` scope:** schema-scoped objects that `resolveObject` finds (table, view, procedure, function, trigger, sequence, synonym). `--as create|alter|drop|drop-and-create|select|insert|update|delete|execute`, each available only where the TUI offers it; a verb the family lacks is exit 2 and lists the family's verbs. Default `create`. `create-or-alter` and the `--include-*` options are dropped (P6-6). **No script options in phase 6:** the output is exactly Script As. Output: record `object type verb` + a Verbatim `script`. `--script-file <path>` writes the TUI's File… bytes (LF, UTF-8, `WriteAtomic`), refuses to replace an existing file unless given `--force` (its help widened), and is not the global `--out-file`, which is for rows. Database-level families (users, roles, schemas, types, keys) and server-level ones are out of scope (`--type` later if asked).
- **D7 — `script db`:** `script db <name> [--as create|drop|drop-and-create]` = the TUI's Script Database As. The Generate Scripts wizard (`--objects`, `--include-permissions`, every object in dependency order) is **deferred out of phase 6** to `docs/open-threads.md`: it needs dependency-ordered scripting in gosmo first (P6-7). *Alternative:* build it in gosmo now. That is a feature the size of a phase (dependency order, SCHEMABINDING, cycles), and nothing in the done criteria needs it.
- **D8 — data reads go through the executor:** `top/count/sample/profile` build their SELECT in the CLI from the resolved table or view (`gosmo.QuoteName`, columns from `TableColumns`/`ViewColumns`) and run it on a `query.Session` with the command's sink. That gives the TUI grid's cell text, typed JSON, `--max-rows` and `--out-file` for free. **top:** `SELECT TOP (n) <cols> FROM fqn [WHERE …] [ORDER BY …]`, `--rows` default 100. **count:** `COUNT_BIG(*)` with `--where`; `--approximate` = `Catalog.RowCount` (no `--where`). **sample:** `TOP (n) … ORDER BY NEWID()` (exact, but a full scan, and the description says so); TABLESAMPLE is dropped because it reads pages, is empty on small tables and does not work on views. **profile:** one aggregate statement, set `columns` (`column type rows nulls null_pct distinct min max min_len max_len`). min/max only for comparable types; xml, text, ntext, image and CLR types get count and nulls only. `--top-values n` (default 0) adds set `top_values` (`column value count`), one query per column. *Alternative:* gosmo `Table.Profile`/`Sample`. These are row reads with grid rendering, not catalog state, and through gosmo they would lose the executor's rendering.
- **D9 — fragments:** `--where` is spliced as `WHERE (<expr>` + newline + `)` and `--order-by` as `ORDER BY <expr>` + newline, so a `--` comment cannot swallow the rest. The composed statement goes through `safety.Classify` and must be **one read statement**, else BLOCKED exit 7 (a `;`, a second statement, `GO` or dynamic SQL cannot slip in). `--columns a,b` is parsed as identifiers (bracket-aware) and checked against the column list (exit 8 with close matches); it is never spliced raw.
- **D10 — `data export`:** `data export <object> | --sql/--file/--sql-base64`, with `--out-file` **required** (exit 2 without). The format comes from the extension, as for the global option; §4.8's `--format` is dropped. The object form is `SELECT <all columns> FROM fqn` (+ `--where`/`--columns`). The SQL form is query run's read path: classified, must be read, no `--allow`.
- **D11 — `data import`:** `data import <schema.table> --in-file <path|->`, `.csv`/`.tsv` (header row; columns matched by name, case-insensitive; an unknown header is exit 2; destination columns missing from the file take their defaults) and `.jsonl` (gossms's own format: `_resultSet` + row objects, `_summary` ignored). No `.json`: the envelope is a whole document read into memory, and jsonl is its streaming form. Conversion per destination type (6.i); `sql_variant` and CLR UDT columns are exit 11 before loading. rowversion and computed columns in the file are skipped with one warning, so an export → import round trip works. Bulk options: `KeepNulls` always; `CheckConstraints` on (`--no-check-constraints` to turn it off; bcp's default is off, but an agent should not admit rows an INSERT would reject); `--fire-triggers`; `--tablock`. The first conversion error is exit 2 naming the line and column. The run is a single streaming pass; P6-10 still has to show it is all-or-nothing. Class `write`; `--truncate-first` makes it `ddl` and needs `--confirm <schema.table>` (P6-12). `--dry-run` reads and converts the whole file, reports `rows` and the column mapping set and the TRUNCATE T-SQL (from WithScript), and loads nothing (a bulk load has no T-SQL).

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-29.** **6.a Groundwork in `query` and `showplan`**
- ✅ **Done 2026-10-29.** **6.b `plan estimated`**
- ✅ **Done 2026-10-29.** **6.c `plan actual`**
- ✅ **Done 2026-10-29.** **6.d `internal/scripting` extraction**
- ✅ **Done 2026-10-29.** **6.e `script object` + `script db`**
- ✅ **Done 2026-10-29.** **6.f Data read core + `data top` + `data count`**
- ✅ **Done 2026-10-29.** **6.g `data sample` + `data profile`**
- ✅ **Done 2026-10-30.** **6.h `data export`**
- ✅ **Done 2026-10-30.** **6.i Import groundwork**
- ✅ **Done 2026-10-30.** **6.j `data import`**
- ✅ **Done 2026-10-30.** **6.k Agent and docs:** `permissionNeeds` rows (SHOWPLAN → `plan estimated/actual`; VIEW DEFINITION → `script object`; table SELECT/INSERT/ALTER → `data …`: decide whether table-level rights are probed at all or only stated); §9's recipe steps 3 (and 5) in the guide; a "Script and export" recipe; `docs/cli.md` § Plans, § Scripting, § Data; README; `docs/decisions.md` for D2, D5, D7, D9, D11; this file's §4.5, §4.7, §4.8 and §10.13 corrected (dropped options, `--plan-format`, `--script-file`, no `--format`).
- ✅ **Done 2026-10-30.** **6.l Verification (driven binary)** on 17, 14, 13 (win10cli) and ubusql1: `script object` for each family and verb, diffed against the TUI's Script As → File for the same object (tmux harness); `plan estimated/actual` summaries checked against the TUI plan panel for the same query; the round trip export → import into an empty copy (made from `script object`) of a table with every importable type plus NULLs, empty strings, the string `NULL`, identity, rowversion and computed columns, compared with `EXCEPT` both ways and per-column NULL / empty counts, in CSV and jsonl; the read-only login (SHOWPLAN missing → 9, import → 9, a refused `--where` → 7). Disposable databases only; dropped afterwards.
  *Result:* on all four, `script object`/`script db` for every family × verb (table, odd-named table, view, procedure, encrypted procedure, scalar/inline/multi-statement function, trigger, sequence, synonym, database) was byte-identical to the TUI's Script As ▸ File... (52 files each); refusals as documented (a verb the kind lacks, a constraint, a shipped object → 2; encrypted create/alter/drop-and-create → 11). `plan estimated/actual` matched the TUI plan panel on every statement cost, operator cost %, est/actual row count and missing index. Export → import (CSV and jsonl) into an empty copy made from `script object`: `EXCEPT` 0 rows both ways, every column's NULL and empty counts equal, identity kept. Read-only login: SHOWPLAN → 9 (both plans), import and `--truncate-first` → 9, a module or view it can see but not read → 9, a `;`/EXEC `--where` → 7. **Fixed:** `plan actual`'s statement `actual_rows` was NULL when the root is a Compute Scalar the server reports no runtime for (`rowSource`, `cmd/plan.go`). **Found, not fixed:** a table DROP's schema-bound-dependents comment silently disappears without VIEW DEFINITION (no Msg 229, unlike what `docs/cli.md` said; corrected, `docs/open-threads.md`).

**Not in phase 6:** `plan cached` (phase 7, needs gosmo's plan-cache read); the Generate Scripts wizard (D7); script options beyond Script As's defaults; database- and server-level families in `script object`; `.json` import; parquet/Excel; `--var`.

### Phase 7 — Monitoring

- `monitor sessions/blocking/waits/top-queries/querystore/deadlocks/kill`, `plan cached`, `xevent sessions/capture` (reusing `internal/activity`, `internal/xevent` and gosmo's XE templates).
- gosmo: plan-cache top queries, `system_health` deadlocks.
- **Done when:** the §10.9 examples run as written (with the corrections below), `monitor sessions`/`blocking` agree with the TUI's Sessions and Block tabs for the same blocking scenario, and `xevent capture` leaves no event session behind on any exit path, Ctrl+C included.

#### Phase 7 analysis (2026-10-30, against c9bff10 + gosmo d071143)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P7-1 | The TUI's Sessions and Block tabs run **helper procedures** (`activity.WhoIsActiveProc` = sp_WhoIsActive, GPL-3.0; `activity.BlockProc` = sp_block) that it **installs** into tempdb on first use, or master on request (`activity_monitor_proctab.go`). decisions § Activity Monitor keeps them in gossms, not gosmo | The CLI cannot reuse them: installing a procedure is a write into a system database, and a `read` command must not do one. And whether the procedure exists would change a read command's output shape. D1 |
| P7-2 | gosmo `Server.ActiveSessions(ctx, scope)` gives session, login, host, program, database, status, cpu, memory, elapsed, last request start, the **current request's** text (first 512 chars), and the blocker / wait from `sessionBlockerJoin`, the same join `BlockProc` uses (a parallel request's blocked worker counts). It has no open-transaction count, reads/writes, request command, wait resource, or `most_recent_sql_handle` text, so an idle head blocker, the usual culprit, shows **no text**. `BlockedRequests` covers only **one client process** (`clientPID`); there is no server-wide blocking read | `monitor sessions` and `monitor blocking` both come from `ActiveSessions`. gosmo's `ActiveSession` gains the missing fields (7.a), and the CLI builds the chains from `BlockingSessionID` (D2) |
| P7-3 | Without VIEW SERVER STATE, `ActiveSessions` returns **only the caller's own session**, which it then filters out: the result is empty, not an error (`BlockedRequests`' doc says so; `HasViewServerState` exists to tell the two apart). `WaitStats` is Msg 300 → `PermissionDenied`. Phase 4 already maps a DMV refusal to exit 9 (`volumesDenied`) | Every `monitor` read except `querystore` asks `HasViewServerState` first and exits 9 with the same hint shape. An agent never sees an empty list that really means "not allowed" |
| P7-4 | `WaitStats` is cumulative since start (or the last `DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR)`). The benign-wait filter (`isBenignWait`) and the categories (`categorize`, `WaitCategoryNames`) are in `internal/activity`, a TUI-free package, but **unexported**. decisions § Activity Monitor: "the benign-wait list … is a monitoring opinion" that stays in gossms | Export the two (7.b) so the CLI and the Waits chart filter and group in the same way. §4.11's `--reset-baseline` would be the CLEAR: a server-wide `admin` write that wipes every other tool's baseline. D3 |
| P7-5 | **Nothing reads the plan cache**: no `sys.dm_exec_query_stats` / `dm_exec_text_query_plan` anywhere in gosmo or gossms. `InFlightPlan(sessionID)` (`dm_exec_query_statistics_xml`) is the running statement only | New gosmo reads (7.a): `Server.TopQueries` and `Server.CachedPlan`. `plan cached` digests the plan with the phase 6 `showplan` summary. D4 |
| P7-6 | Query Store reports are complete in gosmo (`QueryStore.TopResourceQueries`, `RegressedQueries`, `HighVariationQueries`, `ForcedPlanQueries`, `WaitCategories`, `WaitingQueries`, `Plans`, `QueryText`; `QueryStoreReportOptions` with metric, statistic, window, baseline, top, min exec count, min regression %). The TUI's Query Store panel uses them. `WaitStatsSupported()` is false below major 14 | `monitor querystore` is a thin layer, with no gosmo work. §4.11's single `--regressed` flag undersells it: D5 |
| P7-7 | `system_health` deadlocks can be read with **existing** gosmo: `XEvents.SessionByName("system_health")` → `EventFilePattern()` → `XEvents.ReadFile(pattern, cursor, max)`; events decode with `xml_report` as an `IsXML` field. But `ReadFile` has **no server-side filter**: system_health holds everything (`sp_server_diagnostics`, errors, waits, connectivity), and on 2019+ it keeps up to 10 × 100 MB files. A deadlock read would pull every event across the wire. `timestamp_utc` is not on 2016 (decisions § Extended Events: timestamps come from the event XML). Nothing parses a deadlock graph | gosmo gains an object-name filter on the file read (server-side `WHERE object_name IN …`, every major) and a pure `DecodeDeadlockReport` (7.a). The time window is filtered client-side from the XML timestamp. MI's system_health also writes a local file, read only by its wildcard, which `EventFilePattern` already gives (decisions § Extended Events). D6 |
| P7-8 | `Server.KillSession(ctx, spid)` exists (refuses `< 1`; `exec("KILL n")`, so `WithScript` records it). `SessionByID` reads one session and returns `ErrNotFound` when it is gone. The server refuses to kill your own session (Msg 6104) and a system session (Msg 6107) | `monitor kill` gets `--dry-run` for free. It reads the target first so the output shows what was killed. D7 |
| P7-9 | XE templates in gosmo: `XEProfilerStandard`/`XEProfilerTSQL`, and `XESessionTemplates()` (Standard, TSQL, Connection Tracking, Count Query Locks [histogram target], Deadlocks, Query Batch/Detail Tracking, Query Wait Statistics), each with no name and no target. The TUI Profiler creates `gossms_QuickSession*` **persistent** sessions (event_file, or ring_buffer on Azure), reuses one if it exists, and filters out its own reader by `client_app_name`. There is no "errors" template (§4.13) | `xevent capture` builds its own short-lived session from a template, not the Profiler's. D8, D9 |
| P7-10 | Creating, starting, stopping and dropping a server event session needs ALTER ANY EVENT SESSION (or, from 2022, the finer CREATE/ALTER/DROP ANY EVENT SESSION). That is server state, which §6.1 puts in `admin`. `internal/xevent` (store, dedupe, filter, columns) is pure data with no imports | The class of `xevent capture` is a real choice (D8). The ring_buffer dedupe on `event_sequence` can be reused (`xevent.Dedupe`); the client-side filter language is not needed for phase 7 |
| P7-11 | `permissionNeeds` (`cmd/agent.go`) has a VIEW SERVER STATE row (storage commands) and none for ALTER ANY EVENT SESSION or VIEW DATABASE STATE (Query Store reports). The brief guide is one row per group, so `monitor` and `xevent` add two rows. §9's step 4 ("indexes and runtime history") has no command until `monitor querystore` exists | 7.k |

**Decisions** — all ten settled 2026-10-30 as recommended (D1, D3, D4, D5, D6, D8, D9 asked explicitly; D2, D7, D10 follow from them; the alternatives are kept for the record)

- **D1 — sessions source:** gosmo `ActiveSessions`, extended (7.a), and never the helper procedures. *Alternative:* call `sp_WhoIsActive` when it is already installed (it is on the read allow-list). Rejected: the output columns would then depend on the server, and the agent guide could not describe them.
- **D2 — `monitor sessions` / `monitor blocking`:** **sessions:** set `sessions` (`spid login host program database status command open_tran cpu_ms reads writes logical_reads elapsed_ms blocked_by wait_type wait_ms wait_resource text`). `--active` = has a request; `--database`, `--login`, `--program` filter on equality, case-insensitive; `--system` adds `AllSessions`. Text cut to `--max-col-width` as for every cell. **blocking:** chains built from `BlockingSessionID` over one `ActiveSessions(AllSessions)` read. A head blocker is one that blocks and is not itself blocked; a cycle (a deadlock not resolved yet) is reported once with `cycle=yes`, not walked forever. Set `blocking` (`head depth spid blocked_by wait_type wait_ms wait_resource login host program database open_tran text`), in tree order. Text is the request's, or for an idle session the connection's most recent batch, as `sp_block` shows it. No blocking is an empty set and `STATUS: ok`. *Alternative:* a gosmo `Server.BlockingChains`. The chain is a pure function of the session list, so it stays in gossms and is unit-tested there.
- **D3 — `monitor waits`:** default = cumulative since start, with a `since` field (`sqlserver_start_time`). `--interval <s>` (1–300) takes two reads and reports the delta, which is what "what is the server waiting on now" needs. Benign waits are left out unless `--all`. Set `waits` (`wait_type category waiting_tasks wait_ms signal_ms resource_ms pct avg_ms`), top `--top 20` by `wait_ms`, plus set `categories` (the 8 `WaitCategoryNames`, summed). **`--reset-baseline` is dropped** (P7-4). *Alternative:* keep it as an `admin` command. That clears the baseline for every other monitoring tool on the server, a side effect that `--interval` avoids.
- **D4 — `monitor top-queries` vs `plan cached`:** gosmo `Server.TopQueries(ctx, TopQueriesOptions{By, Top, Database})` reads `sys.dm_exec_query_stats` per statement (text sliced by the statement offsets, database from `dm_exec_plan_attributes` `dbid`, `query_hash`, `plan_handle`, offsets, execution count, total and avg cpu / duration / logical reads / writes, last execution time, creation time). `--by cpu|reads|writes|duration|executions` (default cpu), ranked by total; `--avg` ranks by average. **`plan cached <plan_handle> [--statement-start n]`** fetches one plan through `Server.CachedPlan` (`dm_exec_text_query_plan`, text form: `dm_exec_query_plan` fails on plans nested more than 128 levels deep) and renders it with phase 6's `--plan-format summary|tree|xml` and `--plan-file`. The handle and offset come from `top-queries`. §4.7's `plan cached [--top] [--order-by]` would be `top-queries` with plans attached, so it is replaced. *Alternative:* §4.7 as written. N plan digests in one answer are mostly noise; an agent picks one query and then asks for its plan.
- **D5 — `monitor querystore`:** `--database` (default: the connection's database), `--report top|regressed|variation|forced|waits` (default `top`), `--metric` (gosmo `Metrics()`, default duration), `--stat avg|max|min|stdev|total`, `--since`/`--until` (server local time, as `server log`), `--top`, `--min-exec`, `--min-regression-pct`. `--regressed` stays as an alias for `--report regressed` because §10.9 uses it. Set `queries` from `QSQueryStat`, with the columns the report fills (P7-6's doc). `--report waits` below major 14 → exit 11. If Query Store is OFF: exit 8, hinting `db set --option QUERY_STORE=ON`. Class `read`, needs VIEW DATABASE STATE. Plan forcing (`ForcePlan`/`UnforcePlan`) is **out of scope** (an `admin` write; phase 8 if asked).
- **D6 — `monitor deadlocks`:** `--since`/`--until` are **UTC** (XE timestamps are UTC, unlike the error log): an ISO time with `Z` or an offset is converted, and a time with no zone is taken as UTC (documented). Default: the last 24 h. Source: the system_health event_file through the new filtered read; ring_buffer if the session has no file target; exit 8 if system_health is not running. Set `deadlocks` (`n time_utc victims processes databases objects`) and set `processes` (`n spid victim login host program database isolation lock_mode wait_resource transaction statement`). `--deadlock-format summary|xml` (`xml`: each report as a Verbatim field) and `--deadlock-file <dir>` writes one `.xdl` per deadlock, as SSMS saves them (`--force` as for `--plan-file`). Newest first, capped by `--top 20`.
- **D7 — `monitor kill`:** `admin`. `--confirm <spid>` is always required (§6.3), and it must equal the argument. The CLI's own spid → exit 2. It reads `SessionByID` first: gone → exit 8; `is_user_process = 0` → exit 7 before sending anything (the server would refuse with Msg 6107 anyway). Output: the target's session row, then `killed: yes`. `--dry-run` prints `KILL n` (WithScript) and the row. A rollback in progress (Msg 6120 / `KILL … WITH STATUSONLY`) is reported, not waited for.
- **D8 — `xevent capture` class:** **`admin`**, since it creates a server-level object (§6.1, P7-10), and an agent profile with `max_allow: read` cannot use it. *Alternative:* a new rule "temporary diagnostics are `read`". That weakens the "classes follow what the server changes" rule for one command; and a stuck session (kill -9) is a lasting server change.
- **D9 — `xevent capture` mechanics:** a fresh session `gossms_cli_<yyyymmddhhmmss>_<4 hex>` per run: **ring_buffer** target (no files to clean up, works on MI), `MAX_DISPATCH_LATENCY = 1 s`, `STARTUP_STATE = OFF`, `package0.event_sequence` collected, and every event predicated `client_app_name <> 'goSSMS - CLI'` (as the Profiler excludes its viewer). Create → start → wait `--duration` (1–600 s, default 30) + latency → read the ring buffer → **stop + drop on every exit path**, under a fresh 15 s context so a SIGINT still cleans up (exit 130 after the drop). If the drop fails, a warning names the session. Templates: `--template standard|tsql|batches|statements|waits|connections|deadlocks` mapped to gosmo's (`Count Query Locks` left out: histogram target), plus `errors` = `error_reported` with `severity >= 11`, defined in the CLI (a gossms opinion, not added to gosmo's SSMS list). Server-side filters, ANDed into every event's predicate: `--database`, `--login`, `--program`, `--min-duration-ms` (completed events only). `--max-events` (default 1000). Set `events` with fixed columns (`time_utc event session_id database login program host duration_ms cpu_ms logical_reads writes row_count result error severity text`), filled when the event carries them. `-o jsonl` adds every field and action of each event as an object. A truncated ring buffer (`RingBufferData.Truncated`/`DroppedCount`) is reported as truncation with a hint (shorter `--duration`, tighter filter). `xevent sessions`: set `sessions` (`name scope running startup targets events`) from `XEvents.Sessions` + `Status`; class `read`.
- **D10 — permissions:** P7-3's VIEW SERVER STATE check (exit 9) for sessions/blocking/waits/top-queries/plan cached/deadlocks; `querystore` VIEW DATABASE STATE (Msg 262/300 → 9); capture ALTER ANY EVENT SESSION (→ 9, and nothing is created).

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-30.** **7.a gosmo additions** (dev-with-local-gosmo; live on 13, 14, 17 + version sweep): `ActiveSession` fields `OpenTransactionCount`, `Reads`, `Writes`, `LogicalReads`, `Command`, `WaitResource`, `RecentText` (`dm_exec_connections.most_recent_sql_handle`), additive with no behaviour change for the TUI; `Server.TopQueries` + `TopQueriesOptions`; `Server.CachedPlan(ctx, planHandle []byte, stmtStart, stmtEnd int)`; an object-name filter on the event_file read (a new option or method; `ReadFile`'s signature unchanged); `DecodeDeadlockReport(xml) (*DeadlockReport, error)` (victims, processes, resources), with fixtures from 13 and 17 (an intra-query parallel deadlock included).
  *Result:* `ActiveSession` gained `RecentText`, `Command`, `OpenTransactionCount`, `Reads`/`Writes`/`LogicalReads` (session totals, like `CPUTime`) and `WaitResource`; `SessionByID` fills them too (`server_config.go`, `server_activity.go`). New `plan_cache.go`: `Server.TopQueries(ctx, TopQueriesOptions{By, Average, Top, Database})` → `[]*CachedQueryStat` (ranked before text/dbid are looked up; unknown database → ErrNotFound; executions has no average) and `Server.CachedPlan(ctx, handle, start, end)` (`dm_exec_text_query_plan`; evicted → ErrNotFound). `XEvents.ReadFileEvents(ctx, pattern, cursor, max, events)`: `ReadFile` plus a server-side `object_name IN (…)`; `ReadFile` unchanged. New `deadlock_report.go`: `DecodeDeadlockReport` (victim-list and the .xdl `victim=` form, processes with frames and input buffer, resources with owners/waiters and every attribute; `Statement()`). Offline tests in `monitor_reads_test.go`; live `live_monitor_reads_test.go` (idle head + waiter, top-queries → plan, two provoked deadlocks read back from system_health) and the version sweep pass on 13, 14 and 17; fixtures `testdata/deadlocks/deadlock_{13,14,17}_{rows,parallel}.xml`. gossms's three TUI fakes widened to the 23-column session row; TUI behaviour unchanged. **Found live:** `dm_os_waiting_tasks.resource_description` words a lock differently from `r.wait_resource` (`keylock hobtid=…` vs `KEY: …`), so `WaitResource` takes the request's own unless only a parallel worker is blocked; `RecentText` is the auto-parameterized form when the server parameterized the batch; `currentdbname` in a deadlock graph depends on the build (2016 SP3 and 2025 have it, 2017 RTM does not); parallel workers in a graph carry no login, and a coordinator waiting on its workers no lock mode. **Not captured:** a true intra-query parallelism deadlock (one session deadlocking itself), which cannot be provoked on demand; the "parallel" fixtures are two parallel plans blocked on each other's locks, with `exchangeEvent` resources and ecid > 0 workers.
- ✅ **Done 2026-10-30.** **7.b `internal/activity` exports:** `IsBenignWait`, `WaitCategoryOf` (the current `categorize`); the TUI's call sites renamed; no behaviour change.
  *Result:* both renamed in `internal/activity/waits.go` and its tests; the only callers were inside `internal/activity` (`collectWaits`, the category summing), so no `internal/tui` file changed. Build, vet, `internal/activity` and `internal/tui` tests pass.
- ✅ **Done 2026-10-30.** **7.c `monitor sessions` + `monitor blocking`:** new `cmd/monitor.go`; the chain builder as a pure function, with unit tests (single chain, fan-out, idle head, cycle, parallel worker).
  *Result:* new `cmd/monitor.go` (group `monitor`, registered in `tree.go`); `Catalog` gained `HasViewServerState` and `ActiveSessions`. Both commands ask `HasViewServerState` first and exit 9 without reading a session (`connectServerState`, for 7.d–7.g to reuse). `blockingChains` is the pure builder: heads by spid, depth first with each level by spid; a session whose blocker the read does not show (ended, or negative -2/-4) heads its own chain with `blocked_by` kept; what is left is walked once per cycle from the loop's lowest spid, `cycle` = 1 on the loop's members only (a column after `depth`, `bit`). `text` in both sets is the request's batch, else `RecentText`. Unit tests in `cmd/monitor_test.go` (single chain, fan-out, two chains, unseen blocker, cycle, tree off a cycle, cycle entered off its lowest spid, idle head, parallel worker's wait, filters, scope, no VSS → 9 with no read); mutations of the loop head, the text fallback and the permission check each fail them. Schema golden updated. **Changed from D2:** `--database` is the connection's `-d`, so the filter is `--session-database`; `--system` is `--include-system`, the name `db list` already uses (7.k: §4.11 and `docs/cli.md`). **Driven live on win10cli (17):** an idle head with an open transaction and two waiters (key and page lock) matched `dm_exec_requests`/`dm_os_waiting_tasks`; `--active` and a case-differing `--session-database` filtered right; a login without VIEW SERVER STATE → 9 for both, text and JSON. The parallel waiter did not go parallel there (one task); 7.l's scenario covers it. Not yet compared with the TUI's Block/Sessions tabs (7.l).
- ✅ **Done 2026-10-30.** **7.d `monitor waits`:** cumulative and `--interval` delta; the categories set.
  *Result:* new `cmd/monitor_waits.go`; `Catalog` gained `WaitStats`. Behind `connectServerState` (exit 9 before any read). Each read is `StartTime` + `WaitStats`, so the window is the server's clock: fields `since` (the instance start, or with `--interval` the first read's server time) and `elapsedMs` (the uptime, or the server time between the two reads). `--interval` 1–300 (0, the default, = since start; out of range → 2), sleeping on the context so Ctrl+C → 130 with no second read; a wait type new in the second read counts from zero, one whose totals fell (cleared in between) is left out with a warning. Benign waits out unless `--all` (`activity.IsBenignWait`), and wait types with neither time nor a task; signal time clamped to the wait time (the TUI chart's clamp — the DMV's two totals are not updated together, and a live 2 s delta showed `signal 1 > wait 0`). Set `waits` as D3, `--top` (default 20, 0 = all) cutting rows but not `pct`, which is the share of all wait time counted (NULL if none); set `categories` (`category waiting_tasks wait_ms signal_ms resource_ms pct`, all eight, most wait time first). The VIEW SERVER STATE refusal's message is now generic (it named sessions). Unit tests in `cmd/monitor_test.go` (cumulative, `--all` + `--top`, interval delta with new/cleared/clamped types, nothing counted, cancelled interval, usage, no VSS → 9 with no read). Schema golden updated. **Driven live:** cumulative on 17 matched `sys.dm_os_wait_stats` via sqlcmd; a provoked 3.5 s table lock during `--interval 8` came back as `LCK_M_IS 1 3519`; `--interval 2` on 13, 14, 17 and ubusql1; SIGINT mid-interval → 130; a login without VIEW SERVER STATE → 9; `--interval 301` → 2. **For 7.k:** D3's `since` is joined by `elapsedMs`; the categories set has more columns than D3 named.
- ✅ **Done 2026-10-30.** **7.e `monitor top-queries` + `plan cached`:** `plan cached` in `cmd/plan.go`, reusing the phase 6 digest and `--plan-file`.
  *Result:* new `cmd/monitor_top_queries.go`; `Catalog` gained `TopQueries` and `CachedPlan`; both commands behind `connectServerState` (exit 9 before any read). **`monitor top-queries`:** `--by cpu|reads|writes|duration|executions` (default cpu), `--avg` (with executions → 2), `--top` (default 20, at least 1: gosmo's 0 means 20, so the CLI's usual "0 = all" is refused), `--plan-database`. Fields `rankedBy`, `ranking` (total/average); set `queries` (`n database executions cpu_ms avg_cpu_ms duration_ms avg_duration_ms logical_reads avg_logical_reads logical_writes avg_logical_writes last_execution cached_at query_hash plan_handle statement_start statement_end text`), times as ms with three decimals (the DMV keeps µs), handles as `0x` upper-case hex. Unknown `--plan-database` → 8. **`plan cached <plan_handle>`:** `0x` optional, at most 64 bytes, else 2; `--statement-start` (default 0) and `--statement-end` (default -1, the batch's end: D4 named only the start, but `CachedPlan` takes both and top-queries gives both); bad offsets → 2 before connecting; `--plan-format`/`--top`/`--plan-file` as plan estimated (`addPlan`); fields `planType: cached`, `planHandle`, `statementStart`, `statementEnd`. Gone from the cache → 8 with a hint to rank again, `--plan-file` not written (warning). The plan group's description now covers a T-SQL-less command. Unit tests in `cmd/monitor_top_queries_test.go` (cells and µs→ms, defaults, usage, unknown database, plan cached render + `--plan-file` bytes + 0x-less handle and default offsets, gone → 8, usage, no VSS → 9); `runMonitor` became `runGroup` over the monitor and plan groups. Schema golden updated. **Driven live:** top-queries on 17, 14 and 13 (by name and by port), `--by reads --avg -o json`, `--plan-database HealthClinic`; `plan cached` with a handle from it on all three, and on 17 the `--plan-file` was byte-identical to `sys.dm_exec_text_query_plan` via sqlcmd (65 648 chars); an evicted/made-up handle → 8; `nothex` → 2; `--plan-database NoSuchDb` → 8; a throwaway login without VIEW SERVER STATE → 9 for both (dropped after). Seen live: a statement in the resource database (dbid 32767) has `database` NULL, as the description now says. **For 7.k:** D4's `Database` option is `--plan-database` (`-d` is the connection's); `plan cached` has `--statement-end`; `--top` is ≥ 1. Not yet compared with the TUI plan panel for the same handle (7.l).
- ✅ **Done 2026-10-30.** **7.f `monitor querystore`.**
  *Result:* new `cmd/monitor_querystore.go`; `Catalog` gained `CurrentTime`, `QueryStoreMetrics` and the six report reads (`QSTopResourceQueries`, `QSRegressedQueries`, `QSHighVariationQueries`, `QSForcedPlanQueries`, `QSWaitCategories`, `QSWaitingQueries`). The database is the connection's `-d` (D5's `--database` *is* `-d`). `--report top|regressed|variation|forced|waits` (`--regressed` its alias; with a different `--report` → 2), `--metric` (11 names: `duration cpu logical-reads logical-writes physical-reads clr dop memory rows log-memory tempdb`; one the instance lacks → 11 before any read), `--stat avg|max|min|stdev|total` (default per report as the TUI panel: total for top and waits, avg otherwise), `--since`/`--until`, `--top` (default 25, gosmo's and the panel's; ≥ 1), `--min-exec`, `--min-regression-pct` (regressed only, whole percent), plus `--wait-category` (waits only). **Window:** default the 24 h to the server's now (the panel's default, not gosmo's 1 h); a zoneless time is the server's local wall clock, read in the zone of the server's `SYSDATETIMEOFFSET` (new gosmo `Server.CurrentTime`), and — unlike `server log` — a time with `Z` or an offset is accepted as that instant, since Query Store's times are `datetimeoffset`. Empty window → 2. **Regressed** compares the second half of the window against the first, as the TUI (`queryStoreRegressionOptions`), not gosmo's default prior window. Fields `report from to [baselineFrom baselineTo] metric statistic unit` (times server local; `unit` ms/pages/bytes/count, µs shown as ms). Set `queries` (`n query_id object executions plans forced_plan_id last_execution value text`, plus `baseline_value baseline_executions regression regression_pct` for regressed, `variation` for variation); waits adds set `categories` (`category executions value`) before it and refuses `--metric`, `--min-exec`, and a stat other than avg/total across every category (2). Query Store OFF/ERROR → 8 with the `db set … QUERY_STORE=ON` hint; READ_ONLY reads. Refusals (Msg 300) → 9 with a VIEW DATABASE STATE hint. **gosmo:** `Server.CurrentTime`; `QueryStore.Options` on a database with no row (master, tempdb) now wraps `ErrNotFound`, not a bare `sql.ErrNoRows` (→ 8, "cannot be enabled in master or tempdb"); `WaitingQueries`' `LastExecutionTime` was the interval's *end* (up to an interval in the future), now `MAX(rs.last_execution_time)` as every other report, the interval end only when no runtime row matched. Unit tests in `cmd/monitor_querystore_test.go` (defaults, server-zone and zoned windows, regressed halves and a zero baseline, variation, forced, waits both forms, usage, OFF/no-row/READ_ONLY, unsupported metric and waits, VDS refusal); mutations of the zone, the halves split and the state check each fail them. gosmo offline tests for the two fixes; `TestLiveCurrentTime` and the live Query Store + version-sweep tests pass on 13, 14, 17. Schema golden updated. **Driven live** on 17, 14, 13 against a disposable `gossms_p7f` (Query Store at 1-minute intervals, a two-phase workload, dropped after): `top` equal to a hand-written `sys.query_store_runtime_stats` sum on all three; `--regressed` over a 4-minute window equal per query to the two halves' execution-weighted averages; a forced plan (`sp_query_store_force_plan`) shown in `--report forced`; variation, waits (both forms), `-o json`/`tsv`; `--metric tempdb`/`--report waits` → 11 on 13 and run on 14; `-d master` → 8; a `db_datareader`-only login → 9. **For 7.k:** D5's `--since` takes zones; `--wait-category`; the 24 h default; regressed's halves; `--min-regression-pct` is an integer. Not yet compared with the TUI Query Store panel (7.l).
- ✅ **Done 2026-10-30.** **7.g `monitor deadlocks`.**
  *Result:* new `cmd/monitor_deadlocks.go`; `Catalog` gained `EventSessionByName`, `ReadFileEvents` and `ReadRingBuffer`. system_health is read by `SessionByName`: missing, not running, or with neither an event_file nor a ring_buffer target → 8 with a hint each (the u_tables.sql recreate, `ALTER EVENT SESSION … STATE = START`, `ADD TARGET`). The event_file is read whole through `ReadFileEvents(…, ["xml_deadlock_report"])`; the ring_buffer (only when there is no file target) is filtered client-side. `--since`/`--until` per D6 (zoneless = UTC, a zone converted; `--until` exclusive), default the 24 h to the server's `CurrentTime` in UTC; an empty window → 2 (before connecting when both are given). Newest first, `--top` (default 20, ≥ 1). Fields `source from to deadlocksInWindow` (UTC, the count before `--top`). Set `deadlocks` (`n time_utc victims processes databases objects`: victims the victims' spids, databases/objects distinct, comma-joined) and, in `summary`, set `processes`; `--deadlock-format xml` replaces the processes set with Verbatim fields `deadlock1…n` (the deadlocks set stays, so `n` still says which is which). A report that does not decode is a warning and left out. A process with no `currentdbname` (2017 RTM) is named from one `Databases` read, made only when one is missing. The server's page `wait_resource` ends in a space, trimmed. `--deadlock-file <dir>` writes the xml_report bytes as `deadlock_<yyyymmdd_hhmmss_mmm>.xdl` (UTC, no colon for Windows; `_2`… for two in one millisecond), creating the directory; an existing file without `--force` → 2 and **no** file written (every name checked first); `deadlock-file` added to `render.go`'s `forceFileOptions`, and `--force`'s help names it. **Changed from D6:** `processes` has `ecid` after `spid` (a parallel plan's workers share a spid); fields `source` and `deadlocksInWindow` added. Unit tests in `cmd/monitor_deadlocks_test.go` (window default in a non-UTC server zone, zoned/zoneless bounds, `--top` vs. the in-window count, xml format, ring_buffer fallback, bad report, missing/stopped/no-target → 8, no VSS → 9 with no read, usage, files with `_2`, refusal without `--force` writing nothing, replacement with it); mutations of the sort, UTC parse, `.UTC()` of the server's now, the running check, the `--force` check and the name lookup each fail them. Schema golden updated. **Driven live:** 17 (the 2026-10-30 rows and parallel deadlocks from gosmo's live test, 10-process parallel graph with ecids; `.xdl` byte-identical to gosmo's `deadlock_17_parallel.xml`; `-o json` with `--deadlock-format xml`; `--force` 0, without it 2; zoned `--since`), 14 (no `currentdbname` → `master` from the catalog), 13; a login without VIEW SERVER STATE → 9. **Found live:** the server parses every event in system_health's files even with the `object_name` filter — 13's holds 538 k events, and both a bare `sqlcmd` count and the command took 70–80 s (17: 8 s for a 24 h window, 476 ms on 14). The description says so; nothing cheaper reads the file target (the time window can only be applied after parsing). Not yet opened in the TUI (7.l). **For 7.k:** D6's `ecid`, `source`, `deadlocksInWindow`; the slow read on a busy system_health; `--force` covers `--deadlock-file`.
- ✅ **Done 2026-10-30.** **7.h `monitor kill`:** through the shared write layer (`gateClass`, `applyWrite`, `--dry-run`).
  *Result:* new `cmd/monitor_kill.go`; `Catalog` gained `SessionByID`, `Writer` `KillSession`. Before connecting: the spid is 1–32767 (else 2), `--confirm` must repeat it exactly as given (`087` for `87` is refused; missing or different → 7), then the admin gate (7). Connected: `HasViewServerState` first (without it → 9 with nothing sent — `SessionByID` would see every other session as ended), then `SessionByID`: missing → 8; one of **this process's** connections (client pid = `os.Getpid()` from the caller's host, as the TUI's Kill Process tells its own) → 2 — the pool's, not only `@@SPID`, since the KILL could go out on another of them; `is_user_process = 0` → 7 before sending. The session's row is set `session` (monitor sessions' columns) in every outcome after the read. `applyWrite` runs `KILL n` (dry run: `tsql` = `KILL n`/`GO`); fields `dryRun`, `killed`, and after a real KILL `ended` from a second read — a different login or client pid on the spid counts as ended (the id was reused); still there → warning, "rolling back its open transaction" when the command says ROLLBACK, pointing at `KILL n WITH STATUSONLY`, not waited for. The server's refusals: 6106 (ended between read and KILL) → 8, 6107 → 7, 6104 → 2, 6102 → 9 with an ALTER ANY CONNECTION hint. **gosmo:** `ActiveSession.IsUserProcess` (`s.is_user_process`, a 24th column; the three TUI fakes widened, behaviour unchanged); Msg 6102 in `refusalNumbers` as PermissionDenied, captured live from a login with VIEW SERVER STATE only (Msg 6106 for a spid that does not exist comes first for such a login). Unit tests in `cmd/monitor_kill_test.go` (kill, dry run, rollback/still-there/reused after-read, the eight pre-connect refusals with no dial, ended/own/system/no-VSS with no KILL, another process on this host killed, the four server refusals); mutations of the own-process pid check, the user-process check, the confirm compare, the VSS check, the reuse check, the upper bound, the 6106 mapping and `killed` under dry run each fail them. Schema golden updated. **Driven live** on 17, 14, 13: an idle sqlcmd session with an open transaction killed (dry run first, `KILL n` shown, nothing sent), `ended: true`; spid 999 → 8; spid 5/3 → 7 with the row shown; 0 → 2; no `--confirm`/no `--allow` → 7; a 1.5 M-row tempdb transaction killed mid-insert → `ended: false` and the rollback warning (17); re-running on the freed spid hit the CLI's own pooled connection → 2 (17, twice — the own-process check doing its job); a throwaway login with VIEW SERVER STATE but no ALTER ANY CONNECTION → 9 with the hint, and without VIEW SERVER STATE → 9 (dropped after). **Not captured:** a second KILL of a session already rolling back (Msg 6109's progress report) — tempdb rollbacks ended before the second call. **For 7.k:** D7's `killed` is joined by `ended`; "own spid" is every connection of the CLI process; VIEW SERVER STATE is checked first.
- ✅ **Done 2026-10-30.** **7.i `xevent sessions`:** new `cmd/xevent.go`.
  *Result:* new `cmd/xevent.go` (group `xevent`, registered in `tree.go`); `Catalog` gained `EventSessions(ctx, d)` (nil = the server's; a database = Azure SQL Database's database-scoped sessions) and `EventSessionStatus`. VIEW SERVER STATE asked first (exit 9 before any read), except on Azure SQL Database (engine edition 5), where it does not exist: the connection's database's sessions are read there, scope `database`, and a refused read is 9 through `readError`. Set `sessions` (`name scope running startup started dropped_events targets events file`), by name: `started` (server local time — `dm_xe_sessions.create_time` is local, checked live against `SYSDATETIME`) and `dropped_events` from `Status`, read for running sessions only, NULL for a stopped one; a session that stopped between the list and its `Status` (ErrNotFound) shows as stopped. `targets`/`events` are comma-joined names; `file` is the event_file target's current `.xel` when running, else its `filename` parameter. **Added to D9's columns:** `started`, `dropped_events`, `file`. Engine-private sessions with no definition (`hkenginexesession`, `sp_server_diagnostics session`) are not listed (they are not in `sys.server_event_sessions`; documented). Unit tests in `cmd/xevent_test.go` (rows, status read only for running sessions, stopped-between-reads, Azure SQL Database scope with no VSS check, no VSS → 9 with no read, status error). Schema golden updated. **gosmo:** `EventSession.Status` took **4.1 s** on a full system_health (it cast the whole ~4 MB ring_buffer `target_data` to xml for four root attributes); it now casts only the root tag for a ring_buffer (`REPLACE(LEFT(…, CHARINDEX('>')), '/>', '>') + '</RingBufferTarget>'`), 0.3 s, counters identical to a full cast live; the TUI's XEvents detail browser and viewer feed get the speedup too. Live XE tests (`TestLiveEventSession*`, `TestLiveXE*`, the lifecycle test now also asserting `EventCount > 0`) pass on 17, 14, 13 and ubusql1. **Driven live:** `xevent sessions` on 17, 14 (SQL2017), 13 (SQL2016) and ubusql1 matched `sys.server_event_sessions`/`sys.dm_xe_sessions` via sqlcmd (whole run ~0.8–1.4 s, was 5–7 s); a throwaway login without VIEW SERVER STATE → 9 (dropped afterwards); `-o json` checked.
- ✅ **Done 2026-10-30.** **7.j `xevent capture`:** cleanup on every exit path, tested with a fake that fails in each phase (create, start, read) and with a cancelled context.
  *Result:* new `cmd/xevent_capture.go` (in the `xevent` group, whose summary now names it); `Writer` gained `CreateEventSession`, `StartEventSession`, `StopEventSession`, `DropEventSession`. Class `admin` (D8). Before connecting: `--template` (D9's eight; `errors` defined in the CLI), `--duration` 1–600 (default 30), `--max-events` 1–100000 (default 1000), `--min-duration-ms` ≥ 0 (else 2), then the admin gate (7). Connected: Azure SQL Database → 11; `HasViewServerState` (→ 9: the ring buffer is read from the DMVs); then `ServerCapabilities` — ALTER ANY EVENT SESSION denied and any of CREATE / ALTER … ENABLE / ALTER … DISABLE / DROP ANY EVENT SESSION not granted → 9 naming it, nothing created (a failed probe lets the server decide). The session per D9: `gossms_cli_<UTC yyyymmddhhmmss>_<4 hex>`, the template's events and options, `MAX_DISPATCH_LATENCY = 1 s`, `STARTUP_STATE = OFF`, one ring_buffer with **`max_events_limit` = `--max-events`** (so the buffer itself keeps the newest n), and on every event: the template's predicate AND `client_app_name <> 'goSSMS - CLI'` AND the filters (`--session-database` → `sqlserver.database_name`, `--login` → `server_principal_name`, `--program` → `client_app_name`, all exact), `[duration] >= ms×1000` on the completed batch/RPC/statement/`module_end` events only; event_sequence, session_id, database_name, server_principal_name, client_app_name and client_hostname added to every event's actions so the columns fill whatever the template collects. Create → start → `sleepCtx(duration + 1 s)` → `ReadRingBuffer` → stop (if started) + drop under `context.WithoutCancel` + 15 s, on every path; a CREATE that errored is looked up by name and dropped if it exists (gosmo's read-back after the CREATE, or a cancel racing it). A failed drop is a warning naming the session with the DROP to run; field `sessionDropped`. Ctrl+C → 130 after the drop (cli.go keeps the warnings). Fields `template session durationSeconds dryRun sessionDropped eventsSeen eventsKept` (`eventsSeen` = the target's `totalEventsProcessed`); fewer kept than seen, or a truncated `target_data`, is a warning with D9's hint. Set `events` (D9's columns, oldest first by time then event_sequence; µs → ms with three decimals, `wait_info`'s duration already ms; `text` = batch_text, statement, message, xml_report, the sql_text action, else the wait type). **`-o jsonl`** adds column `data`, type `json`: new `output.TypeJSON`, written by json/jsonl as a value and never clipped by `--max-col-width` (that would break the document), `{"fields":{…},"actions":{…}}`. `--dry-run`: real reads and checks, then CREATE/START/STOP/DROP through `applyWrite` (WithScript), nothing sent. Unit tests in `cmd/xevent_capture_test.go` (the session definition and rows, every template resolving in gosmo, jsonl data, truncation, a failure in create — absent and present after —, start, read, stop and drop, a cancelled wait with the cleanup's context live, a cancel during the CREATE, dry run, the eleven refusals with dials and no step, 2022 granular rights accepted); `output` test for TypeJSON. Mutations of the cleanup's `WithoutCancel`, the deferred cleanup, the CREATE-error lookup, `started`, the granular check, the latency, the CLI exclusion, wait_info's ms and the sequence tie-break each fail them. Schema golden updated. **Driven live:** dry run on 17 (the T-SQL above); every template with `--duration 1` on 17, 14 and 13, each ending `sessionDropped: true`; batches with `--min-duration-ms 200` kept the 336 ms batch and the error and dropped `SELECT 1`; `--template errors -o jsonl` on 13 with a RAISERROR → `data` an object; `--program SQLCMD --max-events 3` over 20 batches → kept 3 of 22 with the warning; SIGINT 3 s into `--duration 60` → 130, the session listed during the run and gone after; a throwaway login: no VIEW SERVER STATE → 9, VSS only → 9 (CREATE ANY EVENT SESSION), + CREATE ANY → 9 (ENABLE), + ALTER ANY EVENT SESSION → ok; `sys.server_event_sessions LIKE 'gossms_cli_%'` empty after every run (login dropped after). **Found live:** gosmo's Query Wait Statistics template (`is_system = 0`) still collects session 0's background waits (all SOS_WORK_DISPATCHER on 17), so the CLI adds `session_id > 0` to `wait_info`; the template itself is unchanged. **Not driven:** ubusql1 and MI (MI unavailable) — 7.l. **For 7.k:** D9's filters are `--session-database`/`--login`/`--program`, as monitor sessions names them; `--max-events` is the ring buffer's `max_events_limit` (1–100000), so the *newest* are kept; fields above; the `data` column and `output.TypeJSON`; the waits template's session filter; the 2022 granular rights.
- ✅ **Done 2026-10-30.** **7.k Agent and docs:** `permissionNeeds` rows (VIEW SERVER STATE → the monitor reads; VIEW DATABASE STATE → `monitor querystore`; ALTER ANY EVENT SESSION → `xevent capture`; KILL needs ALTER ANY CONNECTION → `monitor kill`); §9 step 4 in the guide; a "Diagnose a slow server" recipe (waits → top-queries → plan cached → blocking); brief-guide rows for `monitor` and `xevent`; `docs/cli.md` § Monitoring, § Extended Events; README; `docs/decisions.md` for D1, D3, D4, D8, D9; this file's §4.7, §4.11, §4.13, §10.9 and §11 corrected (`--reset-baseline` gone, `plan cached <plan_handle>`, `--report`, UTC `--since`, the template list).
  *Result:* `permissionNeeds` (`cmd/agent.go`) gained a **second** VIEW SERVER STATE row (`monitor sessions/blocking/waits/top-queries/deadlocks/kill`, `plan cached`, `xevent sessions/capture`: fail before reading, kill and capture send nothing) — one list with the storage commands would pass a cell's 256-character `--max-col-width` and reach an agent cut; `monitor querystore` on the VIEW DATABASE STATE row; new rows ALTER ANY CONNECTION (`monitor kill`; `--dry-run` still prints) and ALTER ANY EVENT SESSION (`xevent capture`), the latter with a new `allOf` field so 2022's four narrow rights together meet it, as `eventSessionDenied` accepts them. **Found:** the storage VIEW SERVER STATE row's `effect` (382 chars) already reached agents cut (`…[+126 chars]`); shortened to 253, and `TestAgentContext` now fails on any cut cell. Tests: the fake lists the new rows, `TestAgentContextEventSessionRights` (three of four narrow rights → listed). Guide: overview names monitoring; "Tune a slow query" gained `monitor querystore --regressed` (§9 step 4) and a note; new recipe "Diagnose a slow server" (waits `--interval` → top-queries → plan cached → blocking → sessions). The brief guide's `monitor`/`xevent` rows were already generated from the tree (5297 bytes, under 6000). `docs/cli.md`: intro through phase 7, command rows for `plan cached`, the seven `monitor` and two `xevent` commands, `plan cached` in § Execution plans, new § Monitoring and § Extended Events, the `json` column type in § `json`, § Writes naming kill and capture, `--confirm <spid>`, `--force`'s four files and the two new permissions, the recipes row. README: a `monitor`/`xevent` paragraph. `docs/decisions.md`: § Command-line monitoring and Extended Events (D1, D3, D4, D8, D9). This file: §4.7, §4.11, §4.13, §10.9 (`-d`, a `plan cached` line), §11 (kill, plan cache, Query Store reports, deadlocks, XE sessions). **Driven live:** `agent context` as a throwaway login on 17 listed both VIEW SERVER STATE rows, ALTER ANY CONNECTION and ALTER ANY EVENT SESSION, the last gone after granting the four narrow rights; on 14 (no narrow rights) listed; `sa` → none; logins dropped after.
- ✅ **Done 2026-10-30.** **7.l Verification (driven binary)** on 17, 14, 13 (win10cli) and ubusql1: a scripted blocking scenario (an idle head with an open transaction, two waiters, one parallel), compared with the TUI's Block and Sessions tabs; a forced deadlock read back by `monitor deadlocks` (summary and `.xdl` opened in the TUI); `top-queries` → `plan cached` against the TUI plan panel for the same handle; `querystore` against the TUI Query Store panel (top and regressed, same window); `kill` of a disposable session (dry-run, missing/wrong `--confirm` → 7, own spid → 2, gone → 8); `xevent capture` for each template, then `SELECT name FROM sys.server_event_sessions WHERE name LIKE 'gossms_cli_%'` is empty after a normal run, a SIGINT mid-capture and a refused filter; the read-only login (every monitor read → 9, capture → 9 with nothing created).
  *Result:* driven on 17, 14 (SQL2017), 13 (SQL2016) and ubusql1 (all by name; the Browser answered) against a disposable `gossms_p7l` per server (dropped after), the TUI in tmux under a scratch config. **Blocking:** an idle sqlcmd head (`BEGIN TRAN; UPDATE` one key), a key waiter and a `MAXDOP 2, QUERYTRACEON 8649` scan; `monitor blocking` gave head → both waiters on every server, the parallel one through its blocked worker (`dm_os_waiting_tasks` ecid 1 on a pagelock, the request itself on CXSYNC_PORT/CXPACKET with blocker 0), and the TUI's Block tab (sp_block) the same tree and resources, its Sessions tab (sp_WhoIsActive, installed in tempdb) the same three sessions. **Kill** on all four: dry run prints `KILL n` and sends nothing; no/other/zero-padded `--confirm` and no `--allow` → 7; 999 → 8; a system spid → 7; 0 → 2; the CLI's own pooled spid (found by `query run "select @@spid"` and re-used by the next process) → 2; the head killed → `ended: true`, the waiters then completed. **Deadlocks:** two crossed UPDATEs per server, read after system_health's 120 s dispatch latency (a read sooner finds nothing — expected); victim and both processes right, 14's database named from the catalog (no `currentdbname`), 13's read 60 s (the known busy system_health); `--deadlock-file` `.xdl`s parse, a second run without `--force` → 2 with no file written, with it 0. Opened in the TUI (Ctrl+O) the `.xdl` is XML text: the TUI has no deadlock-graph view, so the check is the text against the summary (victim process, wait resources). **Plans:** `top-queries --plan-database` → `plan cached` with the statement offsets: `--plan-file` byte-identical to `sys.dm_exec_text_query_plan` via sqlcmd on all four, and the TUI's plan panel (graph and tree) shows the same six operators and costs (91/9 %). **Query Store** (1-minute intervals, a two-phase workload: 5 000 rows, then 405 000): TUI panel and CLI agree to the hundredth for top (24 h on 17, the same 5 m window on 13, 14, ubusql1) and regressed (15 m on 17, 5 m on the others): values, baselines, executions. Both list a query whose second half got *faster* (14, a window past the phase switch: −21 %), as the panel always has. **XE capture:** each of the eight templates for 3 s with traffic on all four, every one `sessionDropped: true` and the expected events (`deadlocks` caught one when provoked during a 9 s capture); SIGINT 3 s into 60 s → 130, the session listed during the run and gone after; refusals (negative `--min-duration-ms`, unknown template, `--duration 0`, no `--allow`) → 2/7; `sys.server_event_sessions LIKE 'gossms_cli_%'` empty after each. **Read-only login** (db_datareader in `gossms_p7l`, no server rights) on all four: sessions, blocking, waits, top-queries, deadlocks, querystore, plan cached, xevent sessions, capture and kill each → 9, nothing created; dropped after. **Found and fixed:** (1) `monitor sessions`' `cpu_ms`/`reads`/`writes`/`logical_reads`/`elapsed_ms` came from `dm_exec_sessions`, which moves only when a request ends — a query blocked 125 s showed `elapsed_ms` 0 and a parallel one `cpu_ms` 0 where sp_WhoIsActive showed 604 ms (confirmed live on 17 and 13: session counters 0 mid-request, the request's growing). gosmo `ActiveSession` gained `RequestCPUTime`, `RequestElapsedMS`, `RequestReads`, `RequestWrites`, `RequestLogicalReads` (additive, 29 columns; the two TUI fakes widened); the CLI adds them to the session's totals and `elapsed_ms` is the running request's, NULL when idle (`TestMonitorSessionsRow`, four mutations fail it; `TestSessionByID`; `TestLiveMonitorIdleHeadBlocker` asserts the blocked waiter's elapsed, on all four). Driven after: the CLI's figures equal `dm_exec_requests`' on all four. (2) `xevent capture --program`/`--login`/`--session-database` longer than about 2 000 characters made the server refuse the CREATE with "Incorrect syntax near 'with'" (exit 5); a name longer than a sysname's 128 characters is now exit 2 before connecting (`TestXEventCaptureRefused`, counted in characters). **Not driven:** MI (unavailable).

**Not in phase 7:** Query Store plan forcing; live query plans (`InFlightPlan`) and Live Query Statistics; a client-side `--filter` expression for captures (`internal/xevent`'s language); reading an existing session's target (`xevent read <session>`); `--reset-baseline`; Count Query Locks (histogram target).

### Phase 8 — Administration

- `security …`, `job …`, `maint backup/restore/backup-history/index-fragmentation/index-rebuild/update-stats/checkdb`, `db create/drop`.
- gosmo: `DBCC CHECKDB`.
- Audit log.
- **Done when:** every destructive command needs `--confirm`, every write has a `--dry-run` that matches what then runs, and the audit log never holds a secret.

#### Phase 8 analysis (2026-10-30, against e8829af + gosmo 688ad28)

**Source facts that change the scope**

| # | Fact | Consequence |
|---|---|---|
| P8-1 | No `security`, `job` or `maint` group exists in `tree.go`, and `db` has no create/drop. The write layer (`writeOptions`, `gate`/`gateClass`, `connectWriter`, `applyWrite` under `WithScript`, `writeError`) is in place since phase 5 and used by 9 writing commands; `Writer` is the fakeable write interface | Every phase 8 write goes through it: `--dry-run` for free, one refusal shape. Each new write adds `Writer` methods, nothing else structural |
| P8-2 | **Security is complete in gosmo:** `Server.Logins`, `Database.Users`, `Server.ServerRoles`, `Database.Roles`, `ServerRoleMembers`/`RoleMembers`, `AddMember`/`RemoveMember` on both role kinds, `Server/Database.ApplyPermission(verb, Securable, perm, principal, PermissionOptions)` (validates the name per `SecurableClass`, column lists, WITH GRANT OPTION / CASCADE / GRANT OPTION FOR), explicit reads `PermissionsForPrincipal`, `ColumnPermissionsForPrincipal`, `ServerPermissions`, `DatabasePermissions`, and effective reads `EffectivePermissions`/`EffectiveServerPermissions` (`fn_my_permissions` under EXECUTE AS) | No gosmo work for `security`. But **effective permissions cannot be read for a role** (gosmo doc: Msg 15517, a role cannot be impersonated), and EXECUTE AS needs IMPERSONATE (or sysadmin/db_owner) — so `security permissions` needs an explicit-grants form too. D6 |
| P8-3 | `SecurableClass` has no `OBJECT`: tables, views, procedures, scalar/inline/table functions, sequences, synonyms and types are separate classes, and the permission names allowed differ per class | `--on OBJECT::s.n` (and a bare `s.n`) must be resolved to a class by `ObjectByName` first; `SCHEMA::`, `DATABASE`, `SERVER`, `TYPE::`, `CERTIFICATE::` map directly. D6 |
| P8-4 | **Agent:** `Server.Jobs`, `JobByName`, `Job.Steps`, `Job.Schedules`, `Job.History(limit)` (step rows with message), `Job.Start(stepName)` (`sp_start_job`, asynchronous, takes a step **name**), `Job.Stop`, `Server.AgentInfo` (`Running`). There is **no server-wide history read**: `--failed-since` can only use `Job.LastRunOutcome/LastRunDate`, which misses a failure followed by a success | `--step <n>` maps through `Steps()`. Agent stopped (Linux default, Express) is a distinct outcome. gosmo gains `Server.JobHistory(ctx, filter)` (D8) |
| P8-5 | **Backup/restore is complete in gosmo:** `Server.Backup(BackupOptions)` (full/diff/log via Action, compression tri-state, copy-only, checksum, init/format, stats + Progress callback, URL + credential), `Server.Restore(RestoreOptions)` (relocate, recovery/norecovery/standby, replace, `CloseExistingConnections` done safely in one batch, MI-aware), `VerifyBackup`, `BackupHeaders`, `BackupFileList`, `PlanRestore`, `DiskTarget` (URL detected). Server info carries `DefaultDataPath`/`DefaultLogPath`/`DefaultBackupPath`. The TUI's backup and restore dialogs use them | No gosmo work for backup/restore themselves. `--to` can default into `DefaultBackupPath`; a restore under a new name can relocate into the default paths from `BackupFileList`. D4, D5 |
| P8-6 | `Server.BackupHistory(db)` is **per database** (`WHERE bs.database_name = @p1`). Nothing gives the last backup of each type for every database, nor shows a database never backed up | `backup-history --all --last` would be N round trips and miss never-backed-up databases. gosmo gains `Server.LastBackups(ctx)` (sys.databases LEFT JOIN msdb, one row per database × type). D4 |
| P8-7 | **Fragmentation is per table/view only** (`Table/View.FragmentationStats(mode)`); `object indexes --fragmentation` uses it. No database-wide read | `maint index-fragmentation --database` needs gosmo `Database.FragmentationStats(ctx, mode, minPct, minPages)`: one `dm_db_index_physical_stats(db_id, NULL, NULL, NULL, mode)` filtered server-side, not a loop over tables. D7 |
| P8-8 | `IndexRebuildOptions` has FillFactor, PadIndex, DataCompression — **no ONLINE, no MAXDOP**. `Table.RebuildAllIndexes(ctx, fillFactor)` takes only a fill factor. `Index.Reorganize` exists. ONLINE is Enterprise/Developer/Azure only | gosmo: `Online`, `MaxDOP` on `IndexRebuildOptions` (additive) and a new `Table.RebuildAllIndexesWith(ctx, opts)` (the old method stays). ONLINE on another edition → exit 11 before sending (edition from server info). D7 |
| P8-9 | Statistics: `Statistic.Update(samplePct)`, `Table.UpdateAllStatistics(samplePct)`, `Index.UpdateStatistics`. **No database-wide update** (no `sp_updatestats`) | `update-stats --all` needs gosmo `Database.UpdateStatistics(ctx, resample bool)` over `sp_updatestats`. D7 |
| P8-10 | **Nothing runs DBCC CHECKDB** in gosmo or gossms (§14.6 planned it). `WITH TABLERESULTS` returns rows; `NO_INFOMSGS` suppresses the per-object chatter; a clean run returns no rows | gosmo `Database.CheckDB(ctx, CheckDBOptions{PhysicalOnly, NoIndex, ExtendedLogicalChecks, DataPurity, MaxDOP})` → `[]*CheckDBError` via TABLERESULTS. No REPAIR_* option at all (D2) |
| P8-11 | `Server.CreateDatabase(CreateDatabaseRequest{Name, Collation, RecoveryModel, CompatLevel, PrimaryFile, LogFile})` and `Database.Drop(ctx, Termination)` exist (drop handles SINGLE_USER/STANDBY/RESTORING, releases this Server's idle sessions, moves the pool off a dropped initial database) | `db create/drop` are thin. **Conflict:** §4.3 says `db create … --allow ddl`, but the query classifier puts CREATE/ALTER/DROP DATABASE in `admin` (`adminObjects`). D9 |
| P8-12 | Classifier vs. §4.12: `ALTER INDEX` → `ddl` (matches §10.10); `UPDATE STATISTICS` starts with the `UPDATE` floor word → `write` (§10.10 says `ddl`); `BACKUP`/`RESTORE` (VERIFYONLY and HEADERONLY included) and every non-allow-listed DBCC → `admin` | Each command's class should equal what `query run` gives the T-SQL it sends, or an agent can lower its class by choosing the other route. D1, D2 |
| P8-13 | **No audit log exists.** Passwords never reach argv (`--password-env`/`--password-stdin` only), but `query run --sql …`/`--sql-base64` put the SQL text on argv, and §6.4 forbids SQL text in the log. `output.Result` has `Server`/`Database`; the effective login is not recorded anywhere. `fileutil.WithLock` exists | The audit entry is written once, in `cli.run` after the handler, from a small record the handler fills (class reached, SQL hash). A spec option flag marks values that are hashed or dropped instead of logged. D10 |
| P8-14 | `permissionNeeds` (`cmd/agent.go`) has no rows for sysadmin/db_owner-only work (CHECKDB, restore), BACKUP DATABASE/LOG, msdb Agent roles (SQLAgentUserRole/ReaderRole/OperatorRole), ALTER ANY LOGIN/ROLE, IMPERSONATE | 8.l |

**Decisions** — D1, D3, D6, D9, D10 settled 2026-10-30 as recommended (asked explicitly; D9 follows from D1); D2, D4, D5, D7, D8, D11 proposed and follow from them unless revisited (the alternatives are kept for the record)

- **D1 — class rule for commands that send T-SQL the classifier knows:** a command's class is the class `query run` gives its statements (P8-12). So `maint backup`, `restore` (VERIFYONLY too), `checkdb`, `security grant/revoke/deny`, role membership, `job start/stop` (msdb Agent procedures), `db drop` → `admin`; `index-rebuild` → `ddl`; `update-stats` → `write` (not §10.10's `ddl`), or the classifier gains `UPDATE STATISTICS` → `ddl`. *Recommended:* classifier change, `update-stats` → `ddl` (it changes optimizer state, not rows; `--allow write` letting stats change is surprising). *Alternative:* phase 7's "classes follow what the server changes", which would make `checkdb` and VERIFYONLY `read`; rejected — the two routes would then disagree, and a `read` profile could start hours of I/O on production.
- **D2 — `maint checkdb`:** `admin` (D1). `<db>` or `-d`; `--physical-only`, `--no-index`, `--extended-logical-checks`, `--data-purity`, `--maxdop n` (supported from 2014 SP2, so on every major gosmo supports). `WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS`. Set `errors` (`error level state message repair_level object index partition page`), empty = clean; field `consistencyErrors`, `allocationErrors`, `minimumRepair` (from the rows). Any error row → exit 5? *Recommended:* exit 0 with `STATUS: ok` and a warning naming the minimum repair level — the command did its job; the agent reads `consistencyErrors`. **No repair options**, ever (needs SINGLE_USER and can lose data: a human decision).
- **D3 — `--confirm` scope:** §6.3 lists drop database, restore with `--replace`, kill. *Recommended:* `--confirm <db>` on `db drop`, and on `maint restore` whenever the **target database exists** (with or without `--replace`: restoring the same database over itself overwrites it without REPLACE), and on `maint backup --init/--format` (overwrites the media). Not on grant/revoke/deny/role membership/job start (reversible; `--dry-run` is the safeguard). `--close-connections` (restore, drop) never implies confirmation on its own.
- **D4 — `maint backup` / `backup-history`:** `backup <db> [--to <path|url> …] [--type full|diff|log] [--compression|--no-compression] [--copy-only] [--checksum] [--init --confirm <db>] [--credential <name>]`. `--to` repeatable (stripes); **omitted → `DefaultBackupPath` + `<db>_<type>_<UTC yyyymmdd_hhmmss>.bak|.trn`** (agents do not know server paths; joined with `JoinServerPath` in the server's separator). `--compression` tri-state: neither flag = server default. Stats 10 → Progress lines on stderr under `--verbose` only. After the backup: `BackupHeaders` of the new set → fields (`file`, `setNumber`, `sizeBytes`, `compressedBytes`, `firstLsn`, `lastLsn`, `checksum`, `finished`); dry run prints the BACKUP and reads nothing after. Log backup on SIMPLE (Msg 4208) → 5 with a `db set --option RECOVERY=FULL` hint; diff with no full base (Msg 3035) → 5 with a hint; compression on Express → 11. `backup-history <db>|--all [--last] [--type …] [--since …] [--top 50]`: `<db>` from `BackupHistory`, `--all --last` from the new `LastBackups` (a database never backed up shows NULLs and a warning (§4.3's "warnings report facts" rule)).
- **D5 — `maint restore`:** `restore <db> --from <path|url> … [--file-number n] [--with-move <logical>=<path> …|--move-to-default] [--replace] [--norecovery|--standby <undo file>] [--close-connections] [--checksum] [--verify-only] [--confirm <db>]`. Before anything: `BackupHeaders` + `BackupFileList` (real reads in dry run too), output as sets `backup` and `files` with each file's planned target. `--move-to-default` = every file into `DefaultDataPath`/`DefaultLogPath` as `<db>[_<n>].mdf|.ndf|.ldf` (the copy-under-a-new-name case); a planned target that already belongs to another database's file (`sys.master_files`) → 7 before sending. `--verify-only` → `VerifyBackup` and nothing else (no `--confirm`). Log/diff chains (`PlanRestore`) are **out of scope** for phase 8: one backup set per call; `--norecovery` lets an agent chain calls itself. Restore of `master`/`msdb`/`model`/`tempdb` → 7 (single-user-mode work).
- **D6 — `security`:** reads `logins list [--include-disabled] [--type sql|windows|group|certificate|…]`, `users list` (connection's `-d`), `roles list [--server|--database-scope]` (server roles, else `-d`'s roles), `role-members <role>` (server role when the name is a server role and no `-d` was given explicitly — *recommended:* explicit `--server` flag instead of guessing; default = database role in `-d`). `security permissions <principal> [--effective|--explicit]`: **default `--explicit`** (works for roles and needs no IMPERSONATE: `PermissionsForPrincipal` + `ColumnPermissionsForPrincipal`, or `ServerPermissions` filtered for a login); `--effective` = `fn_my_permissions` under EXECUTE AS (user → database, login → server), a role → exit 2 with the reason, no IMPERSONATE → 9 with a hint. Writes `grant|revoke|deny <permission> --on <securable> --to|--from <principal> [--columns a,b] [--with-grant-option] [--cascade] [--grant-option-only]`, `admin`: `--on` = `SERVER` | `DATABASE` | `SCHEMA::s` | `OBJECT::s.n` | `s.n` | `TYPE::s.n` | `CERTIFICATE::n`, OBJECT resolved through `ObjectByName` (P8-3); a permission name the class refuses → 2 before the write (gosmo `ErrInvalidRequest` → 2 already). `security role add-member|remove-member <role> <member> [--server]` (`admin`, `--dry-run`): included (settled). **Out:** create/alter/drop login or user (passwords, SIDs, mapping — the TUI owns it).
- **D7 — `maint index-*` / `update-stats`:** `index-fragmentation [--min-percent 10] [--min-pages 1000] [--mode limited|sampled|detailed] [--schema …] [--top 50]` over `-d` (the connection's database: `--database` would clash with `-d`, as phase 7 found), set `indexes` (`schema table index index_id type partition avg_fragmentation_pct page_count fragment_count avg_page_space_used_pct suggestion`), `suggestion` = `reorganize` 5–30 %, `rebuild` > 30 % (Microsoft's guideline, documented as advice only). `index-rebuild <schema.table> [--index <name>] [--reorganize] [--online] [--maxdop n] [--fill-factor n]` (`ddl`): `--reorganize` excludes `--online/--fill-factor/--maxdop` (2); no automatic rebuild-vs-reorganize choice. `update-stats <schema.table>|--all [--fullscan|--sample-percent n] [--index <name>]` : `--all` = `sp_updatestats` (stale statistics only, with `--resample`), `--all --fullscan` → 2 (a full scan of every table is a choice to make per table).
- **D8 — `job`:** reads `list [--enabled] [--failed-since <t>] [--category …]`, `show <name>` (sets `job`, `steps`, `schedules`; notifications as fields), `history <name> [--top 20] [--since <t>] [--failed]` (step 0 = the job outcome row, steps with messages). `--failed-since` from the new gosmo `Server.JobHistory(ctx, JobHistoryFilter{Since, Outcome})` (P8-4), times **server local** as msdb stores them (zoned input converted through `CurrentTime`'s zone, as `monitor querystore`). Writes `start <name> [--step <n|name>] [--wait <sec>]`, `stop <name>` (`admin`). `--wait` polls `JobByName` every 2 s until idle, then reports the run's outcome from history; Ctrl+C stops waiting, never the job. Agent not running (`AgentInfo.Running` false) → `start` exit 11 with a hint (`mssql-conf set sqlagent.enabled true` on Linux), reads still work. `stop` of an idle job (Msg 22022) → 8? *Recommended:* exit 0 with `stopped: false` and a warning (the wanted state holds).
- **D9 — `db create/drop` class:** `create` per §4.3 is `ddl`, the classifier says `admin` (P8-11). *Recommended:* `admin` for both (D1), §4.3 corrected. `create <name> [--collation …] [--recovery simple|full|bulk-logged] [--compat n]`, files at the instance defaults (no file options in phase 8). `drop <name> --confirm <name> [--close-connections]`: system database or the connection's `-d` → 7/2; database in an AG → 7 with a hint (remove it first); without `--close-connections` a database in use (Msg 3702) → 7 with a hint; snapshots on it (Msg 3709) → 7 naming them. Before dropping, the database's row (state, size, last full backup from `LastBackups`) is output, with a warning when it has no full backup.
- **D10 — audit log:** *Recommended:* **always on** for every command whose class is above `read` and for every BLOCKED refusal; reads are not logged (noise, and the log is for changes). One JSON line per call at the end of `cli.run`: `ts` (UTC), `profile`, `server`, `database`, `login` (the profile's or `--user`'s, `(integrated)`/`(entra)` otherwise — no extra round trip), `command`, `class` (reached), `dryRun`, `args` (argv with every option marked `Audit: hash` replaced by its SHA-256 — `--sql`, `--sql-base64`, `--file` contents, `--var` values — and `Audit: omit` options dropped), `sqlSha256` (query run's script), `outcome` (status), `exit`. `0600` in `os.UserConfigDir()/gossms/audit.jsonl` under `fileutil.WithLock`. If the log cannot be written, a **write is refused before it runs** (exit 1: the guardrail is not optional), checked in `gate`; the end-of-call append failing is a warning. No rotation in phase 8 (open thread). No opt-out flag or env var (an agent could set either). *Alternative:* opt-in via a profile field — rejected: the CLI never writes `config.json`, and an off-by-default audit protects nobody.
- **D11 — dry-run fidelity ("matches what then runs"):** a test helper runs each writing command twice against the recording fake — once with `--dry-run`, once without, the fake capturing the T-SQL each `Writer` call would produce via `gosmo.WithScript` — and compares; live, 8.m compares the dry run's `tsql` with the statements seen by an XE capture of the real run.

**Substeps** (each builds, tests and can be reviewed on its own, in this order)

- ✅ **Done 2026-10-30.** **8.a gosmo additions** (dev-with-local-gosmo; live on 13, 14, 17 + version sweep): `Database.CheckDB` + `CheckDBOptions` + `CheckDBError` (TABLERESULTS; a corrupted-page fixture if one can be made on a disposable database with `DBCC WRITEPAGE`, else synthetic rows); `Server.LastBackups`; `Database.FragmentationStats(ctx, mode, minPct, minPages)`; `IndexRebuildOptions.Online`/`MaxDOP` + `Table.RebuildAllIndexesWith`; `Database.UpdateStatistics(ctx, resample)` (`sp_updatestats`); `Server.JobHistory(ctx, JobHistoryFilter)`. All additive; nothing existing narrowed.
  *Result:* gosmo (uncommitted, on top of 688ad28). New `database_maintenance.go`: **`Database.CheckDB(ctx, CheckDBOptions{PhysicalOnly, NoIndex, ExtendedLogicalChecks, DataPurity, MaxDOP}) ([]*CheckDBError, error)`** — `DBCC CHECKDB (N'db'[, NOINDEX]) WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS[, …]`, run through `execIn` (never retried), captured under `WithScript`, observed after. TABLERESULTS has the same 23 columns on 13, 14 and 17 but is read **by column name** (DBCC output, undocumented shape). `CheckDBError{Error, Level, State, Message, RepairLevel, ObjectID, IndexID, PartitionID, AllocUnitID, File, Page, Slot}` + `IsSummary()` (Level ≤ 10: the 8990 per-table and 8989 database summaries come back as rows at level 10 — the CLI's `consistencyErrors`/`allocationErrors` can be counted from the non-summary rows or parsed from 8989). Corruption is **not** an error and raises no client error (`@@ERROR` 0). PHYSICAL_ONLY with DATA_PURITY or EXTENDED_LOGICAL_CHECKS refused before sending (the server refuses both too — checked live). **`Database.FragmentationStats(ctx, mode, minPct float64, minPages int64) ([]*DatabaseIndexFragmentation, error)`**: one DMV scan with a NULL object, leaf IN_ROW_DATA rows, partitions folded as `fragmentationSelect` does, thresholds as `HAVING` parameters, user tables and indexed views, heaps out, most fragmented first; the row is `IndexFragmentation` embedded + `Schema`, `ObjectName`, `ObjectID`, `IsView`, `IndexType`, `PartitionCount` (partitions are folded, so D7's `partition` column becomes `partitions`). New `indexTypeFromDesc` shared with `Table.Indexes`. **`Database.UpdateStatistics(ctx, resample)`**: `EXEC sys.sp_updatestats [@resample = 'resample']`. **`IndexRebuildOptions.Online`/`MaxDOP`** (bool / int, zero sends nothing) via a shared `withClause` (also refuses fill factor outside 0–100 and MAXDOP outside 0–64), and **`Table.RebuildAllIndexesWith(ctx, opts)`**; ONLINE on a non-Enterprise edition is left to the server (Msg 1712) — the CLI's exit 11 check stays in 8.k. **`Server.LastBackups(ctx) ([]*DatabaseLastBackups, error)`**: two reads (sys.databases minus tempdb and snapshots; the newest D/I/L set per name through `backupHistorySelect`, now shared with `BackupHistory` via `scanBackupInfo`), one row per database with `RecoveryModel` and `Full`/`Differential`/`Log *BackupInfo` — nil = none in msdb (the plan's "database × type" rows became one row with three pointers; the CLI flattens). Copy-only backups count (`IsCopyOnly` says so). **Job history: `Server.JobHistory(ctx, limit int)` already existed** (P8-4 missed it: it is instance-wide, outcome rows only, no filter), so the filtered read is **`Server.SearchJobHistory(ctx, JobHistoryFilter{JobName, Since, Until, Outcomes []JobOutcome, IncludeSteps, Max})`** and `JobHistory` now delegates to it (same SQL). Bounds are inclusive, compared on msdb's `run_date`/`run_time` integers using the bound's UTC fields — RunDate's server-wall-clock-stamped-UTC convention, so 8.h converts a zoned `--failed-since` through `CurrentTime`'s zone into that form. Rows are newest first; a step that starts a second after its job sorts **above** the outcome row (seen on 13), so 8.h must not assume the outcome row comes first. Tests: `database_maintenance_test.go` (statements, refusals, the CHECKDB scan with shuffled columns, the fragmentation query and binding, LastBackups grouping/striping/never-backed-up/dropped-database history, the job-history predicates and parameter order; two mutations checked); `live_maintenance_test.go` (a `DBCC WRITEPAGE`-corrupted index page: 8939/8928/8980/8978 + 8990/8989 rows naming the page; every option form on a clean database; database-wide vs per-table fragmentation equal; online rebuild; `STATS_DATE` moves under both `sp_updatestats` forms; LastBackups vs BackupHistory; a failing Agent job found by outcome/since/until) — **passes on 17, 14 and 13**; `TestLiveVersionSweep` (three new `sweepMustCall` entries) clean on all three. ARCHITECTURE.md feature map + diagrams 02/05/10 updated. Left on each server: `gosmo_p8a_maint.bak` in the default data directory (overwritten by the next run, as `live_recoverystatus_test.go` leaves its own).
- ✅ **Done 2026-10-30.** **8.b classifier alignment:** `UPDATE STATISTICS` → `ddl` (D1), with classify tests; `docs/cli.md` § Classes.
  *Result:* **P8-12 was half wrong:** the leader rule already made a statement led by `UPDATE STATISTICS` `ddl` (since 25b3dd1, with a test row), wherever the splitter cuts at it — after `SELECT`, `IF`, `WHILE`, `SET`, `ELSE`, `BEGIN TRY`; `docs/cli.md` § Safety already listed it under `ddl`. The real gap was the **floor**: a merged statement (`SELECT 1 AS` + newline + `UPDATE STATISTICS t`, no cut after `AS`) floored on `UPDATE` alone → `write`. `floor` in `internal/cli/safety/classify.go` now raises `UPDATE` followed by `STATISTICS` to `ddl` (reason `keyword UPDATE STATISTICS (line n)`). Tests: the merged case in `TestClassifyMergedStatementsAreFloored` (fails without the fix: `write`), plus leader rows for a bracketed name with an index and `WITH FULLSCAN`, under `IF`, and inside `BEGIN TRY`. `docs/cli.md` § Safety's fail-closed paragraph names the merged case. Driven binary: `query classify` gives `ddl` for all four forms; `query run --allow write` on it → BLOCKED, `DETECTED_CLASS: ddl`, before connecting. So `maint update-stats` (8.k) is `ddl`, and `query run` gives the same class to the T-SQL it sends (D1 holds).
- ✅ **Done 2026-10-30.** **8.c audit log** (`internal/cli/audit`): the record, `spec.Option.Audit`, the hook in `cli.run`, the pre-write openability check in `gate`, `WithLock`; retrofitted to every existing writer (`query run`, `server config set/reset`, `traceflag`, `db set`, `scoped-config set`, `data import`, `monitor kill`, `xevent capture`). Tests: secret/SQL never in the line (fuzz over argv), parallel processes don't interleave, refusal logged, unwritable log refuses a write and not a read.
  *Result:* new `internal/cli/audit` (`Record`, `Logged`, `Check`, `Append` under `fileutil.WithLock`, `Args`, `Hash`, `DefaultPath` = `config.ConfigDir()/audit.jsonl`, created `0600` in a `0700` directory). `spec.Option.Audit` (`AuditPlain`/`AuditHash`/`AuditOmit`) marks `--sql`, `--sql-base64`, `--param` and `--where` as hash; nothing is `omit` yet (no option carries a secret: passwords never reach argv). `spec.Invocation.Audit` carries what only the handler knows: `Check` (set by `cli.run`), the class reached, `SQLSha256` (set in `readSQL`, so every script-reading command has it), and profile/server/database/login. **`args` is rebuilt from the parse, not copied from argv** (positional, then each given option as `--name=value` in spec order, globals last; a bool `--name`/`--no-name`; each repeat value hashed apart), so an `@file`, `--sql=…` or `-Q` cannot slip a value past its mark. **Login without a round trip:** `connect.Resolve` split into `resolveConn` + the password steps, and `connect.Describe` returns address, database and login (`(integrated)`/`(entra)` when there is no name) without touching stdin — so a refusal made before connecting is logged with its target too. `gateClass` records class + target, refuses as before, then calls `checkAudit` (dry runs included: a dry run is logged too); `runScript` (query run, plan actual) does the same for a script above read. `cli.run`'s `finish` split into `settle` (sink, status, exit) and `render`, with `logCall` between them: class = handler's, else a BLOCKED result's `detectedClass` field (query classify `--allow`, the SHOWPLAN refusals — no gate), else the command's; logged when above read or BLOCKED; a failed append is a warning. `cli.Env.AuditPath` (empty = no log, tests only; `main.go` always sets it). **Changed from D10:** `--file`'s path is recorded as given (the content's hash is `sqlSha256`); `--param` is hashed beside `--var` (which does not exist). **Bug found by driving:** a refusal as the first call on a fresh config directory was not logged — `WithLock`'s `.lock` needs the directory, which only `open` made; `Append` now makes it first (test changed to Append without a Check). Tests: `internal/cli/audit/audit_test.go` (Args marks and spellings, Logged, 0600 + directory made by Append alone, Check, **six processes × 40 records of 8 KiB** re-executing the test binary — every line whole; unlocked it still passes on Linux, where one O_APPEND write is atomic, and fails once a line takes two writes unlocked: checked), `internal/cli/audit_test.go` (**every writer's first `--allow` example, `--allow` stripped → BLOCKED with one line**, generated from the tree so 8.e–8.k's writers join it; query run: a read unlogged, dry run/real lines with hash not text; unwritable log → exit 1 before dialing for traceflag, kill and query run's dry run, a read still runs, a refusal still BLOCKED with a warning; classify `--allow` refusal logged as `ddl`; `FuzzAuditNeverHoldsSQL` over `--sql`/`--sql=`/`-Q`/`--sql-base64`/`--file -`/`@file`/`--password-stdin` with a `--param` value and an env password — 169k execs, 60 s, clean), `connect.TestDescribe`. Mutations: gate's check off → the unwritable test fails; `--sql` unmarked → the query-run test and the fuzz seeds fail. `cli_test`'s `runTree`/`runWith` now give every test a temp log, so the whole suite runs through the hook. Driven binary on win10cli (17) with a scratch `XDG_CONFIG_HOME`: SELECT not logged; UPDATE refused → line; `CREATE TABLE #…` with `--allow ddl` → `ddl` line with hash; traceflag `--dry-run` → `dryRun: true`; read-only log directory → write refused exit 1 before connecting, read exit 0; 20 parallel processes → 21 whole lines; file `0600`. Docs: `docs/cli.md` § Audit log (new) and the package list, `ARCHITECTURE.md` package map row, `docs/open-threads.md` (no rotation).
- ✅ **Done 2026-10-30.** **8.d dry-run fidelity harness** (D11) over the existing writers, so 8.e–8.j each add a row to it.
  *Result:* new `internal/cli/cmd/dryrun_fidelity_test.go` (test-only; no production code changed). **`fidelityWriter`** wraps each command's existing fake writer: in a real run every `Writer` call is made twice — first through the fake under **one shared `gosmo.WithScript` context** (every fake already hands a scripted call to the real `serverWriter` over a never-connected Server, so the collector gets gosmo's exact batch), then with the command's own context so the fake's simulated state changes and the reads after it see the change; in a dry run calls pass straight through. A write made under the other mode's context fails the test (a dry run that writes, a real run that only scripts). Event sessions map the fake's real handle to the scripted one (the fake's has no Server). `BulkInsert` has no scripted twin (TDS, not T-SQL): a dry run reaching it fails, and the real run's row count is compared with the dry run's `rows`. It implements `Writer` without embedding, so **a new `Writer` method does not compile until it is shadowed**. **`TestDryRunFidelity`**: 12 cases (config set ×2 incl. `--override`, config reset, traceflag on/off, db set ×2 incl. QUERY_STORE and `READ_ONLY --rollback-immediate`, scoped-config set ×2 incl. `--for-secondary`, monitor kill, xevent capture, data import `--truncate-first`), each over fresh fakes; the dry run's `tsql` must equal the real run's `ScriptCollector.String()` byte for byte, be non-empty, and the real run must print no `tsql`; nothing may be scripted outside `applyWrite`. **`TestDryRunFidelityCoversEveryWriter`**: every command with `--dry-run` needs a case or an exemption (`query run`, `plan actual`: their dry run classifies and prints no T-SQL), every case must name a writer, and the harness's group list is checked against the `cmd.*Commands(d)` calls in `tree.go`, so a new group (8.e–8.k's `security`, `job`, `maint`) cannot slip past. **Changed from D11:** the comparison reuses each command's own fakes in package `cmd` rather than one new recording fake, so before/after reads behave as in the per-command tests. Mutations, each caught: the traceflag dry run scripting only its first flag; `applyWrite`'s dry run writing for real (all 11 gosmo-write cases fail); data import's dry run miscounting `rows`. `docs/cli.md` § Writes' Dry run paragraph names the harness and the add-a-case rule. Live comparison against an XE capture of the real run stays in 8.m.
- ✅ **Done 2026-10-30.** **8.e `db create` / `db drop`** (`cmd/db_create.go`).
  *Result:* new `internal/cli/cmd/db_create.go`, both `admin` (D9). `Writer` gains `CreateDatabase` (gosmo `Server.CreateDatabase`) and `DropDatabase` (`Database.Drop`); `Catalog` gains `AvailabilityGroups`, `AvailabilityGroupDatabases`, `SnapshotsOf`, `LastBackups` — no gosmo change. **`db create <name> [--collation] [--recovery simple|full|bulk-logged] [--compat n]`:** name already in `sys.databases` (any case, a snapshot's too) → 2 before sending; `--compat` not 100+ in steps of 10 → 2 before connecting, above the server's major × 10 → 2 (Azure/unread left to the server, as `db set`). Record `name state recoveryModel compatibilityLevel collation dryRun`: real run read back (a value other than asked → warning); dry run = what it would be (model's recovery/level, the server's collation; `state` null) + `tsql`. CREATE then the SETs are not atomic: a SET failing leaves the database, the record shows it plus a warning, exit 5. Msg 262 → 9 with a hint. **`db drop <name> --confirm <name> [--close-connections]`:** `--confirm` ≠ name → 7 before connecting (dry run too). Before sending: system (`IsSystem()`) → 7; distribution database → 7 (hint `sp_dropdistributiondb`); the connection's own database (`CurrentDatabase`) → 2 (`-d master`); AG member → 7 naming the group's `REMOVE DATABASE` (AGs read only when `IsHADREnabled`; `AvailabilityGroups` → `Databases`, no new gosmo read); snapshots on it → 7 naming them (a snapshot itself drops like any database). Record before the drop: `name databaseId state recoveryModel sizeMb lastFullBackup closeConnections dryRun` (+ `tsql` / `dropped`); no full backup in msdb → warning; not ONLINE → warning that the files stay on disk. Server refusals: 3702 → 7 hinting `--close-connections`, 3709 / 3752 → 7, 3701 → 9. **Deviation from D9:** D9's "system database or the connection's `-d` → 7/2" kept as 7 for system, 2 for `-d` — but `-d` is read as the connection's `CurrentDatabase` (a profile's database counts too). Tests: `db_create_test.go` (create real/dry/refusals/failures incl. partial create; drop real incl. snapshot-then-source, dry run tsql both forms, every refusal with its dial count, server refusals, OFFLINE warning); three `fidelityCases` (create with all options, drop, drop `--close-connections`); agent schema golden updated. Mutations: confirm check off → 3 drop tests fail; snapshot check off → `TestDBDrop` fails. **Driven binary:** on 17 (win10cli) create dry run / real with collation+recovery+compat / name taken → 2; drop without and with wrong-case `--confirm` → 7 before connecting; `master` → 7; `-d` itself → 2; missing → 8; snapshot made by hand → 7 naming it, then the snapshot dropped by `db drop`; a session holding the database → 7 after the server's own ~20 s wait (bare sqlcmd waits the same), then `--close-connections` → dropped, the holder killed, files gone from the data directory; a no-permission login → 9 for create (262) and drop (3701); on 13 `--compat 140` → 2, create bulk-logged/130 and drop; on ubusql1 `testdb_1` (in AAG1) → 7 naming the group (dry run only). Audit log: one line per call, all `admin`, `0600`. Docs: `docs/cli.md` § Commands rows, § Discovery, § Writes (list, `--confirm` bullet); `ARCHITECTURE.md` package map row; §4.3's `db create` row corrected to `--allow admin`. **Found, not fixed (8.c's shape, shared with `db set`/`monitor kill`/`data import`):** a `--confirm` refusal is made before `gate`, so its audit line has no `server`/`login` — the refused call's target is not recorded.
- ✅ **Done 2026-10-30.** **8.f `security` reads:** `logins list`, `users list`, `roles list`, `role-members`, `permissions` (explicit + effective). New `cmd/security.go`.
  *Result:* new `internal/cli/cmd/security.go`, all `read`, no gosmo change. `Catalog` gains `Logins`, `LoginByName`, `ServerRoles`, `ServerRoleByName`, `ServerRoleMembers`, `Users`, `UserByName`, `DatabaseRoles`, `DatabaseRoleByName`, `DatabaseRoleMembers`, `PermissionsForPrincipal`, `ColumnPermissionsForPrincipal`, `ServerPermissions`, `EffectivePermissions`, `EffectiveServerPermissions`. **Changed from D6: `--scope database|server` (default `database`) replaces `--server` / `--database-scope`** — `--server` is the connection option (phase 7's `--database` clash again); **8.g's `role add-member/remove-member` must take `--scope` too.** `--explicit` dropped: explicit is the default, `--effective` the switch. **`logins list [--include-disabled] [--type sql|windows|windows-group|external|external-group|certificate|asymmetric-key]`**: set `logins` (`system` marks `##…##`). **`users list`** (connection's `-d`): set `users` with `login`, `login_disabled`, `orphaned` — 1 only for a SQL user with `INSTANCE` auth and no login, NULL for a Windows user without its own login (may enter through a group) and on Azure SQL Database. **`roles list [--scope]`**: `fixed`, `owner`, `member_count`, `members`. **`role-members <role> [--scope]`**: record `role scope fixedRole`, set `members` (`name type`, nested roles not expanded); missing → 8, hinting the other scope when it has the name. **`permissions <principal> [--scope] [--effective]`**: resolves user → database role (or login → server role) first; a miss → 8 hinting the other scope or **the user a login maps to here** (an agent's likeliest slip: asking for the login name in database scope). Record `principal principalType scope mode memberOf` (`memberOf` from the role lists' members, since explicit entries omit what roles give). Explicit set `class schema name column permission state` (securables, then column entries; server scope = `ServerPermissions` filtered to the principal). `--effective` set `entity permission`; a role → 2 before any EXECUTE AS; Msg 15517/15406 → 9 with an IMPERSONATE hint. **`--effective` is database-/server-level only** (`fn_my_permissions(NULL,'DATABASE'|'SERVER')`): a schema SELECT given through a role does not show — documented; per-securable effective permissions (gosmo has `EffectiveObjectPermissions`/`EffectiveSchemaPermissions`) could ride 8.g's `--on` parser if asked. Rows keep the server's `ORDER BY name` (collation order), as `db list` does. Tests: `security_test.go` (each list and filter, orphaned's three answers incl. Azure, both scopes of roles/role-members/permissions, every not-found hint, explicit rows and `memberOf`, effective for user/login, role refusal with no impersonation made, 15517/15406 → 9); `SecurityCommands` added to the fidelity harness's group list; agent schema golden regenerated (additive). Mutations: role check off, orphaned for every type, server entries unfiltered — each caught. **Driven binary** on 17 (win10cli) over disposable `gossms_p8f` + `p8f_*` logins/roles (dropped after): every command, both scopes; explicit incl. GRANT WITH GRANT OPTION and a column DENY; orphaned user from a dropped login → 1, WITHOUT LOGIN → 0; `sysadmin` without `--scope` → 8 with the hint; login name in database scope → 8 naming its user; server role `--effective` → 2; **a login without VIEW DEFINITION sees no other principal → 8** (metadata visibility; the hints and descriptions now say so), with VIEW (ANY) DEFINITION but no IMPERSONATE → **real Msg 15517 and 15406 → 9**. On 13 (SQL2016): every command `ok`. Docs: `docs/cli.md` § Commands rows + the intro's command list; `ARCHITECTURE.md` package map row. Not done here (8.l): `permissionNeeds` rows, `docs/cli.md` § Security, guide recipe.
- **8.g `security` writes:** `grant/revoke/deny` (securable parser with unit tests per class), and role add/remove-member (D6). `cmd/security_permissions.go`.
- **8.h `job`:** list/show/history (reads), start/stop with `--wait` (writes). `cmd/job.go`, `cmd/job_start.go`.
- **8.i `maint backup` + `backup-history`.** `cmd/maint_backup.go`.
- **8.j `maint restore`** (incl. `--verify-only`, `--move-to-default`, target-collision check). `cmd/maint_restore.go`.
- **8.k `maint index-fragmentation` / `index-rebuild` / `update-stats` / `checkdb`.** `cmd/maint_index.go`, `cmd/maint_checkdb.go`.
- **8.l Agent and docs:** `permissionNeeds` rows (P8-14); guide recipes "Back up before a risky change" (backup → change → verify) and "Grant a reader access" (permissions → grant --dry-run → grant → permissions); `docs/cli.md` § Security, § Agent jobs, § Maintenance, § Audit log; README; `docs/decisions.md` for D1, D3, D5, D10; §4.3, §4.9, §4.10, §4.12, §6.4, §10.10–§10.12, §11 corrected; open thread for audit rotation.
- **8.m Verification (driven binary)** on 17, 14, 13 and ubusql1 against disposables: backup → `backup-history --all --last` → `restore --verify-only` → restore under a new name with `--move-to-default` → drop it (`--confirm` missing/wrong → 7); restore over an existing database without `--confirm` → 7; `index-fragmentation` vs a hand query, `index-rebuild --online` on Developer and → 11 on an Express/Standard instance if one is available; `update-stats` then `STATS_DATE` moved; `checkdb` clean and on a deliberately corrupted disposable; grant/revoke/deny dry run vs real (XE-captured statements equal), `permissions --effective` for a user and → 2 for a role; a job created for the test, started with `--wait`, made to fail, found by `--failed-since`; Agent stopped on ubusql1 → 11; the audit log after the whole run: one line per write and refusal, no SQL text, no password, `0600`; a read-only login → 9 on every admin command with nothing changed.

**Not in phase 8 (proposed):** restore chains / point-in-time (`PlanRestore`), REPAIR options, login/user create/alter/drop, job create/alter, Query Store plan forcing (phase 7 D5 parked it here; still out unless asked), backup to a backup device by logical name, audit log rotation.

### Phase 9 (optional) — Session mode and MCP

- `gossms shell --protocol jsonl` over the same `spec` handlers with one held Session; then an MCP server exposing `spec` as tools. Scope decided at the end of phase 8 (§Open decisions 6).
