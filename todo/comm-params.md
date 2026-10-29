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
| `--allow <read\|write\|ddl\|admin>` | Highest permitted operation class (default: `read`). See §6 |
| `--dry-run` | Generate and print the T-SQL but do not run it |
| `--confirm <token>` | Required for destructive operations. The token must equal the target object name (see §6.3) |
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
| `server config set <name\|alias> <value> [<name\|alias> <value> …] [--force] [--override]` | Change one or more settings (`--allow admin`) through `gosmo.Server.ApplyConfiguration`: one batch that turns `show advanced options` on only if needed (decided server-side), runs every `sp_configure`, one `RECONFIGURE`, and restores `show advanced options`. Reports before/after (re-read from `sys.configurations`) and whether a restart is required (`is_dynamic`) |
| `server config reset <name\|alias> […]` | Set settings back to their default (`--allow admin`) |
| `server memory` | Memory overview: physical and available memory, min/max server memory, committed and target memory, page life expectancy, pending memory grants, top memory clerks (gosmo `MemoryStats`, `MemoryClerks`, `PerformanceCounters`) |
| `server volumes [--unit …]` | Disks and mount points that hold database files: total, free, free %, and the number of files per volume (gosmo `DiskVolumes`) |
| `server traceflag list` / `on <flag>` / `off <flag>` `[--global]` | Trace flags (`DBCC TRACESTATUS/TRACEON/TRACEOFF`, new in gosmo). `on`/`off` need `--allow admin` |
| `server log [--lines 100] [--search <text>] [--since <iso>] [--agent]` | SQL Server error log (gosmo `ReadLog` with `LogSearch`); `--agent` reads the SQL Server Agent log |

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

- Values without a unit use the setting's native unit (MB for memory). Memory values also accept `MB`, `GB` and `TB` suffixes (`56GB` → `57344`). The output always shows the converted value.
- Values outside the setting's min/max are a usage error (exit 2).
- Obviously risky values are blocked (exit 7) unless `--force` is given. Examples: `max-memory` above physical memory, or `max-memory` below `min-memory`. `--force` only lifts gossms's own check; `--override` is what SQL Server's `RECONFIGURE WITH OVERRIDE` needs.
- `--dry-run` prints gosmo's batch exactly as it would run (captured under `gosmo.WithScript`) and the planned change, without running anything. The batch is the same whatever the server's state; the planned-change table says whether the `show advanced options` step will fire, read from `sys.configurations` at dry-run time.

### 4.3 `db`: databases

| Command | Description |
|---------|-------------|
| `db list [--include-system] [--state online\|offline\|…]` | Databases with state, size, recovery model, compatibility level |
| `db info <name>` | Properties (Database Properties dialog) |
| `db files <name\|--all> [--type data\|log] [--filegroup <name>] [--unit KB\|MB\|GB] [--sort-by name\|size\|used\|free-pct] [--max-free-pct <n>]` | Per file: logical name, type, filegroup, physical path, size, used, free, free %, autogrowth, max size, and free space on the volume that holds the file. `--max-free-pct 10` shows only files with at most 10 % free space |
| `db space <name\|--all> [--unit …]` | Database-level usage (like `sp_spaceused`): total size, data and log size, reserved, data, index, unused and unallocated space |
| `db log-usage [<name>\|--all] [--unit …]` | Transaction log size, used %, `log_reuse_wait_desc` (why the log can't be truncated) and the number of VLFs (`sys.dm_db_log_info`; new in gosmo, R12) |
| `db table-sizes <name> [--top 20] [--schema …] [--sort-by reserved\|rows\|data\|index\|unused] [--unit …]` | Largest tables: rows, reserved, data, index and unused space (SSMS report "Disk Usage by Top Tables") |
| `db create <name> [--collation …] [--recovery …]` | `--allow ddl` |
| `db drop <name> --confirm <name>` | `--allow admin` |
| `db set <name> --option <key=value>` | e.g. `RECOVERY=SIMPLE`, `READ_ONLY=ON`, `COMPATIBILITY_LEVEL=160`, `QUERY_STORE=ON`. Repeatable |
| `db scoped-config list <name>` | Database scoped configuration (`sys.database_scoped_configurations`) |
| `db scoped-config get <name> <setting>` | One setting, e.g. `MAXDOP`, `LEGACY_CARDINALITY_ESTIMATION`, `PARAMETER_SNIFFING` |
| `db scoped-config set <name> <setting> <value> [--for-secondary]` | `ALTER DATABASE SCOPED CONFIGURATION` (`--allow admin`) |

**Size and usage rules** (all `db files/space/log-usage/table-sizes` and `server volumes` commands):

- All are `read` commands. They work against SQL Server on Linux (including containers) and on Windows, from any client OS. Physical paths are shown exactly as SQL Server reports them (`/var/opt/mssql/data/…` or `D:\Data\…`).
- The default unit is MB with two decimals. `--unit` changes it for the whole output.
- Column names always carry the unit (`size_mb`, `used_gb`, …), so the agent never has to guess.
- Autogrowth is shown as `64 MB` or `10 %`. Max size is shown as a number or `unlimited`.
- `--all` adds a `database` column and covers every database the login can access. Databases that can't be read (offline, no permission) are listed under `WARNINGS`, not silently skipped.
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
| `script object <schema.name> [--as create\|alter\|drop\|create-or-alter\|select\|insert]` | Script As… |
| `script db <name> [--objects …] [--include-permissions] [--out-file …]` | Generate Scripts wizard |

Script options map onto gosmo's `ScriptOptions` (`Verb`, `IncludeHeaders`, `IncludeIfNotExists`, `ScriptDrops`, `SchemaQualify`, `ExtendedProperties`, …): `--include-indexes`, `--include-triggers`, `--include-fk`, `--if-not-exists`. The exact option list is settled in phase 6 against `ScriptOptions` as it then stands. `--target-version` and `--include-data` were dropped: gosmo's `Scripter` has neither (R13); data is `data export`.

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
| `plan estimated --sql …\|--file …` | Estimated plan (`SET SHOWPLAN_XML ON`). The query is not run |
| `plan actual --sql …\|--file …` | Actual plan (the query runs, so the `--allow` rules apply) |
| `plan cached [--top 20] [--order-by cpu\|reads\|duration]` | Plans from the plan cache |

`--plan-format <summary|xml|tree>`. The default `summary` is an AI-friendly digest: costliest operators, scans vs. seeks, missing index suggestions, warnings (implicit conversions, spills) and estimated vs. actual row counts. `xml` returns the raw `.sqlplan`.

### 4.8 `data`: table data

| Command | Description |
|---------|-------------|
| `data top <schema.table> [--rows 100] [--columns a,b] [--where "<expr>"] [--order-by …]` | Select Top N Rows |
| `data count <schema.table> [--where …]` | Row count |
| `data sample <schema.table> [--rows 20]` | Random sample (`TABLESAMPLE` / `NEWID()`) |
| `data profile <schema.table>` | Per column: null %, distinct count, min/max, top values |
| `data export <schema.table\|--sql …> --out-file x.csv [--format csv\|json\|jsonl]` | Export, streamed through a `RowSink` (no row cap). CSV is the Results To File writer (§5.2) |
| `data import <schema.table> --in-file x.csv [--format csv\|json] [--truncate-first]` | Import through gosmo's bulk copy (`bulkcopy.go`) (`--allow write`; `--truncate-first` needs `--allow ddl`) |

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
| `monitor sessions [--active] [--database …] [--login …]` | `sys.dm_exec_sessions` / `requests` |
| `monitor blocking` | Blocking chains (head blocker → victims), including SQL text |
| `monitor waits [--top 20] [--reset-baseline]` | Wait statistics |
| `monitor top-queries [--by cpu\|reads\|duration\|executions] [--top 20]` | Expensive queries |
| `monitor querystore [--database …] [--regressed]` | Query Store insights |
| `monitor deadlocks [--since <iso>]` | From the `system_health` XEvent session |
| `monitor kill <spid> --confirm <spid>` | `--allow admin` |

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
| `xevent capture --template <standard\|tsql\|errors> --duration 30 [--filter …]` | Capture for a fixed time, then return the events (built for agents: no live UI) |

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

=== RESULT 1 (1 row, truncated: no) ===
name                      | alias  | configured | running | default | min | max   | dynamic | advanced
--------------------------+--------+------------+---------+---------+-----+-------+---------+---------
max degree of parallelism | maxdop | 0          | 0       | 0       | 0   | 32767 | yes     | yes

=== CONTEXT ===
LOGICAL_CPUS: 16
NUMA_NODES: 2
LOGICAL_CPUS_PER_NUMA_NODE: 8
```

**Preview a change, then apply it:**

```bash
gossms server config set maxdop 8 -p dev --allow admin --dry-run
```

```text
STATUS: DRY_RUN
COMMAND: server config set
SERVER: sql-dev-01
OPERATION_CLASS: admin

=== T-SQL ===
DECLARE @show_advanced_enabled bit = 0;
DECLARE @show_advanced_value int = (SELECT CONVERT(int, value) FROM sys.configurations WHERE name = N'show advanced options');
IF EXISTS (SELECT 1 FROM sys.configurations WHERE name = N'show advanced options' AND value_in_use = 0)
   AND EXISTS (SELECT 1 FROM sys.configurations WHERE is_advanced = 1 AND name IN (N'max degree of parallelism'))
BEGIN
    EXEC sys.sp_configure N'show advanced options', 1;
    RECONFIGURE;
    SET @show_advanced_enabled = 1;
END;
EXEC sys.sp_configure N'max degree of parallelism', 8;
RECONFIGURE;
IF @show_advanced_enabled = 1
BEGIN
    EXEC sys.sp_configure N'show advanced options', @show_advanced_value;
    RECONFIGURE;
END;

=== PLANNED CHANGE ===
name                      | current | new | dynamic | advanced | enables_show_advanced
--------------------------+---------+-----+---------+----------+----------------------
max degree of parallelism | 0       | 8   | yes     | yes      | yes
```

The batch is `gosmo.Server.ApplyConfiguration`'s, captured under `WithScript`. It is the same on every server; `enables_show_advanced` says whether its first step will fire here (R10).

```bash
gossms server config set maxdop 8 -p dev --allow admin
```

```text
STATUS: OK
COMMAND: server config set
SERVER: sql-dev-01
OPERATION_CLASS: admin

=== CHANGE ===
name                      | before | after (running) | restart_required
--------------------------+--------+-----------------+-----------------
max degree of parallelism | 0      | 8               | no
```

**Reset to the default:**

```bash
gossms server config reset maxdop -p dev --allow admin
```

**Missing `--allow`.** Without `--allow admin` the change is refused and nothing runs:

```text
STATUS: BLOCKED
ERROR_CODE: OPERATION_NOT_ALLOWED
DETECTED_CLASS: admin
ALLOWED_CLASS: read
HINT: Re-run with --allow admin. Use --dry-run first to see the T-SQL.
```

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

=== RESULT 1 (1 row, truncated: no) ===
physical_mb | available_os_mb | max_server_memory_mb | min_server_memory_mb | committed_mb | target_mb | page_life_expectancy_s | memory_grants_pending
------------+-----------------+----------------------+----------------------+--------------+-----------+------------------------+----------------------
65536       | 1840            | 2147483647           | 0                    | 61210        | 62004     | 412                    | 0

=== RESULT 2 (5 rows, truncated: no) ===
clerk                       | size_mb
----------------------------+--------
MEMORYCLERK_SQLBUFFERPOOL   | 52108
CACHESTORE_SQLCP            | 4210
CACHESTORE_OBJCP            | 1022
MEMORYCLERK_SQLQERESERVATIONS | 640
OBJECTSTORE_LOCK_MANAGER    | 312

=== WARNINGS ===
max server memory (MB) is at the default (2147483647 = unlimited).
Available OS memory is 1840 MB.
```

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
STATUS: DRY_RUN
COMMAND: server config set
SERVER: sql-dev-01
OPERATION_CLASS: admin

=== T-SQL ===
(the ApplyConfiguration batch, as in §10.1, with N'max server memory (MB)', 57344)

=== PLANNED CHANGE ===
name                   | current    | new   | dynamic | advanced | enables_show_advanced
-----------------------+------------+-------+---------+----------+----------------------
max server memory (MB) | 2147483647 | 57344 | yes     | yes      | no

=== WARNINGS ===
New value is below currently committed memory (61210 MB). SQL Server will release memory after RECONFIGURE.
```

`enables_show_advanced: no` because `show advanced options` is already on on this server; the batch is the same either way.

**Risky value.** It is blocked unless `--force` is given:

```bash
gossms server config set max-memory 80GB -p dev --allow admin
```

```text
STATUS: BLOCKED
ERROR_CODE: RISKY_VALUE
MESSAGE: max server memory (81920 MB) is above physical memory (65536 MB).
HINT: Choose a lower value, or re-run with --force if this is intended.
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
gossms server config list --name "*backup*" -p prod-ro

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
=== CHANGE ===
name            | before | after (configured) | after (running) | restart_required
----------------+--------+--------------------+-----------------+-----------------
fill factor (%) | 0      | 90                 | 0               | yes

NOTE: The running value changes after the SQL Server service is restarted.
```

### 10.6 Trace flags

```bash
gossms server traceflag list -p prod-ro
gossms server traceflag on 7745 --global -p dev --allow admin --dry-run
# T-SQL: DBCC TRACEON (7745, -1);
gossms server traceflag off 7745 --global -p dev --allow admin
```

Trace flags set this way are lost at restart. The `on` output includes a note that startup flags are configured in the SQL Server service settings.

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
AW_Data: 4.82 % free space in the file.
AW_Log: 7.10 % free space in the file; log_reuse_wait_desc = LOG_BACKUP.
AW_Log: autogrowth is a percentage (10 %).
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
gossms db files --all --type log --unit GB -p prod-ro
```

**Transaction log usage, and why the log can't be truncated:**

```bash
gossms db log-usage --all -p prod-ro
```

```text
=== RESULT 1 (4 rows, truncated: no) ===
database           | log_size_mb | log_used_mb | log_used_pct | log_reuse_wait_desc | vlf_count
-------------------+-------------+-------------+--------------+---------------------+----------
AdventureWorks     | 8192.00     | 7610.40     | 92.90        | LOG_BACKUP          | 212
WideWorldImporters | 2048.00     | 210.30      | 10.27        | NOTHING             | 16
ReportingDB        | 4096.00     | 3901.10     | 95.24        | ACTIVE_TRANSACTION  | 48
tempdb             | 1024.00     | 88.70       | 8.66         | NOTHING             | 8
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

# Regressed queries from Query Store
gossms monitor querystore --database AdventureWorks --regressed -p prod-ro

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
# Generate the CREATE script of a table
gossms script object Sales.Customer --as create -p prod-ro

# Profile a table's columns (nulls, distinct values, min/max)
gossms data profile Sales.Customer -p prod-ro

# Export a query result to CSV
gossms data export --sql "SELECT * FROM Sales.Customer WHERE ModifiedDate >= '2026-01-01'" --out-file customers.csv -p prod-ro

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
gossms server log --search "Login failed" --since 2026-10-27T00:00:00Z -p prod-ro

# Last 50 entries
gossms server log --lines 50 -p prod-ro
```

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
| View Dependencies | `object dependencies` |
| Tasks → Back Up / Restore | `maint backup` / `maint restore` |
| Import/Export Wizard | `data import` / `data export` |
| SQL Server Agent → Job Activity | `job list/history/start` |
| XEvent Profiler | `xevent capture` |
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

- ✅ **Done 2026-10-28.** **0.a Version fields in `internal/version`.** `version.Fields() []Field` (ordered: version, commit, built, platform, license, gosmo) — `gosmo/version` is already a dependency of `cmd`, and it is a leaf. `printVersion` renders it and stays byte-identical (golden test against today's format). Keep the "in step with `newAboutRows`" note on `Fields`. *(As built: `Field{Key, Label, Value}`; `newAboutRows` no longer exists, so the note names `App.showAbout`; `printVersion(io.Writer)` pinned by `TestPrintVersionFormat` in `cmd/gossms/main_test.go`.)*
- ✅ **Done 2026-10-28.** **0.b Mode switch and TTY guard in `cmd/gossms/main.go`.** Order: version flags → `len(os.Args) > 1` → `os.Exit(cli.Main(...))` *before* `config.OpenLogFile` → terminal check → TUI (unchanged). Factor `isTerminal(fd)` behind a func variable so the decision is unit-testable without a tty; the check is `term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))`, exit 1 with one stderr line naming `gossms help` (D1). Until 0.f lands, `cli.Main` can be a stub returning 2. **Verify on Windows:** `IsTerminal` is `GetConsoleMode`-based, so mintty/Git Bash (a pipe, not a console) now refuses — confirm tcell v3 does not already work there, or the guard regresses those users. *(As built: `selectMode(args, stdin, stdout) mode` — `modeTUI|modeVersion|modeCLI|modeNoTerminal` — with `isTerminal = term.IsTerminal` swappable, pinned by `TestSelectMode`; `x/term` is now a direct require. Stub `internal/cli.Main(ctx, args, Env) int` already has 0.f's signature and `Env`, prints one stderr line, returns 2. Windows, from tcell v3.6.0 source: `tscreen_win.go` always opens `CONIN$`/`CONOUT$` (`tty.NewDevTty`), never stdio, so under mintty without ConPTY (stdio are pipes) the TUI would attach to a hidden console and wait for keys it never gets — inferred from source, not observed; the guard turns that into exit 1, so no working setup regresses; with ConPTY stdio are console handles and `IsTerminal` passes. Live Windows runs (cmd, PowerShell, Git Bash) are still owed in 0.i.)*
- ✅ **Done 2026-10-28.** **0.c `internal/cli/spec`: the command tree as data, types only.** `Command{Path []string, Summary, Description, Args []Arg, Options []Option, Class, Examples []string, Hidden bool, Run Handler}`, `Option{Name, Short, Type (bool|string|int|enum|list), Enum []string, Default, Env, Help, Global bool}`, `Class` (`read|write|ddl|admin`). `Handler func(ctx, *Invocation) (*output.Result, error)`. `spec` imports no handler package: the tree is assembled in `internal/cli/tree.go` (no `init()` registration), so `cmd/<group>` → `spec` stays acyclic. Leaves at depth 1 (`version`, `help`) and depth 2 (`<group> <command>`) both allowed; a group node alone answers usage (exit 2) listing its commands. Phase-0 globals: `--output/-o`, `--quiet/-q`, `--verbose`, `--no-color` (no-op: phase 0 never colours), `--help/-h`. Connection and safety globals arrive with the phases that implement them — an option is in the spec only when it works. *(As built: `spec.Tree{Globals, Commands}` holds the tree; `Arg{Name, Help, Format, Required, Repeat}` (fields from §8.4's schema); `Option.Default` is spelled as on the command line so the parser applies it by the typed-value path; `Class`/`OptionType` are string types in the schema's spelling. `Invocation{Command, Args, Values map[string]any}` (typed per `Option.Type`). `Handler` needs `output.Result`, so `internal/cli/output` exists as a one-type placeholder until 0.e. `internal/cli/tree.go` `commandTree()` holds the five globals plus `version` and `help` (`help` takes optional `<group> <command>`), with `Run` nil until 0.f. `TestCommandTreeWellFormed` checks names, help, enum defaults, unique long/short per command ∪ globals, path depth, arg order and that examples name their command; 0.g adds the examples-parse drift guard.)*
- ✅ **Done 2026-10-28.** **0.d `internal/cli/args`: the parser.** Walk positional tokens to the deepest matching command, then parse options against command ∪ globals: `--x v`, `--x=v`, `--no-x` (bool only), repeat/comma lists, `-` as a *value* never an option, `--` ends options, `@file` response files (one option per line, no recursion, path relative to CWD). Errors carry a did-you-mean (edit distance ≤ 2 over siblings/options). "No positional token at all" (`gossms -p x`, `gossms --quiet`) is its own error — *no command* — checked before option validation, so `-p` need not exist yet. Unknown option, missing/extra args, bad enum/int → usage error. Table tests only; no I/O in the package. *(As built: `args.Parse(tree, argv, Options{ReadFile, Getenv}) (*spec.Invocation, error)`; errors are `*args.Error{Kind, Command, Message, Hint}` (nine kinds), all USAGE_ERROR. Globals may precede the command; before the command only the globals are known, so an unknown option is assumed to take the next token unless that token names a command — that is what makes `-p x` "no command" in every phase. A non-list option given twice is an error (no last-wins); `--bool=v` is refused; a value may start with `-` but not `--` (`--x=--y` spells one). Response file: a line starting with `-` splits at its first whitespace into option and value (unless `--x=v`), any other line is one token — so command words go on separate lines; blank lines skipped, BOM and CRLF tolerated; `@x` stays literal after `--` and as the value of an option that takes one (by name, anywhere in the tree). A group node alone parses (Main answers it); `--help` skips the argument-count checks. Did-you-mean is `args.Suggest`: optimal-string-alignment distance ≤ 2 and < the word's length, case-insensitive. Env and defaults go through the same typed conversion. `spec.Tree.Lookup/Children` and `Command.Name()` added. `FuzzParse` seeded; 30 s clean.)*
- ✅ **Done 2026-10-28.** **0.e `internal/cli/output`: envelope, renderers, error model.** `Result{Status, Command, Server, Database, Duration, Fields, Sets, Messages, Warnings, Errors}`; `Status` `ok|error|blocked|cancelled`. `cli.Error{Code string, Exit int, Message, Hint}` and one table of the §7 codes (name ↔ number) — all of them defined now, since `agent schema` will list them; phase 0 only *produces* 0, 1, 2, 130. Text renderer: header block, D3 fields, `=== RESULT n ===` sets (width via D2), `=== MESSAGES ===`, `ERROR_CODE`/`MESSAGE`/`HINT`; `--quiet` drops the header. JSON: `schemaVersion: "1.0"`, struct field order = output order, `encoding/json` as the rest of the repo, one trailing newline. Golden-file tests per renderer. *(As built: the error type is `output.Error{Code, Message, Hint}` — in `output`, since `cli` imports `output` and not the reverse; there is no `Exit` field: `Code.Exit()` reads the one `ExitCodes` table, so the two cannot disagree. `Field{Key, Value string}`; `Set{Columns, Rows [][]*string (nil = NULL), TotalRowCount, Truncated, RowsAffected}` — cells are the grid's text, typed JSON cells arrive with column types in phase 2. `output.Formats` is the `--output` enum (D5). Text: a blank line between sections; `SERVER`/`DATABASE` only when set, `RESULT_SETS` only with sets; an empty value prints `KEY:` (no line ends in a space); errors are `ERROR_CODE`/`MESSAGE`/`HINT` blocks after `=== WARNINGS ===`; every value is single-lined (line breaks and tabs → space) so no value can forge a marker; the last table column is unpadded. JSON: two-space indent, HTML escaping off, every key always present (`null`/`[]`) except `data`, which appears only for a record. Goldens: `internal/cli/output/testdata/*.golden`, `go test -update`. Code comments cite `comm-params §n` until `docs/cli.md` exists (P0-8) — 0.h switches them.)*
- ✅ **Done 2026-10-28.** **0.f `internal/cli.Main(ctx, args, Env) int`.** `Env{Stdin, Stdout, Stderr io.ReadWriter…, Getenv func}` — no `os.*` inside `internal/cli`, so every test runs in-process. Sequence: `log.SetOutput` (P0-5) → **pre-scan** for `-o/--output` so a usage error is rendered in JSON when asked → parse → `signal.NotifyContext(ctx, os.Interrupt)` (works on Windows Ctrl+C) → handler → render to stdout; on error also one line `gossms: <CODE>: <message>` to stderr → exit code. A context cancelled by the signal maps to 130 / `CANCELLED` regardless of the handler's error. A handler panic is recovered into exit 1 / `GENERAL_ERROR` with the stack only under `--verbose`. Commands: `version` (D3 fields + `cliSchema: 1.0`), `help` (group list; first line per D1), `help <group> [<cmd>]`, `<…> --help`/`-h` (usage, options, examples — all generated from spec). `help --output json` is the schema (§4.14), which arrives with `agent schema` in phase 3; until then it is refused (exit 2) rather than emitting a partial catalog — a half schema is worse than none. *(As built: `Env` gained `ReadFile` (main passes `os.ReadFile`) for `@file`. `run(ctx, tree, argv, env)` is `Main` over any tree, for tests. Help — `help`, `help <group> [<cmd>]`, `--help`/`-h` anywhere, `gossms --help` (the no-command error with a help flag) — is plain text from Main outside the envelope, so `help` has no `Run`; help with `-o json` is refused, exit 2. A group alone: exit 2, hint listing its commands; a leaf with nil `Run`: GENERAL_ERROR "not implemented". `signal.NotifyContext`, stopped after the first interrupt so a second still kills; any cancellation of the context (the parent's too) ends as CANCELLED/130, replacing the handler's errors. A panic: GENERAL_ERROR, hint "Re-run with --verbose", stack on stderr only under `--verbose`. `cli_test.go` covers exit 0, 1 (panic, plain error), 2 (each usage flavour), 8 (a coded handler error) and 130 in-process — overlapping 0.g(3). Driven on Linux: every 0.i Linux item except a live Ctrl+C (no phase-0 command runs long enough) and the pre-change `--version` diff; Windows still owed in 0.i.)*
- ✅ **Done 2026-10-28.** **0.g Guard tests.** (1) Import rule, `internal/cli/imports_test.go`: `go list -deps ./internal/cli/...` from the module root contains neither `/internal/tui`, `/internal/tuikit/` except `tuikit/sqltext`, nor `gdamore/tcell` (fails with the offending chain from `go list -f '{{.ImportPath}} {{.Imports}}'`). (2) Spec consistency: unique paths, unique long/short names per scope, every option and command has help, every enum default is in its enum, every `Examples` line and every `gossms …` command named in a hint/usage/`Description` string parses against the tree (D1's drift guard; it is also §13's "every example passes the parser" criterion, started early). (3) Exit codes: an in-process `Main` test per produced code (0, 1 via a panicking test handler, 2 in each usage flavour, 130 via a pre-cancelled signal context). (4) `cmd/gossms`: mode-selection table and the TTY decision with a fake `isTerminal`. *(As built: (1) `TestNoTUIImports` runs `go list -deps -f '{{.ImportPath}} {{.Imports}}' ./...` from `internal/cli` and reports each offender with its shortest chain (BFS); checked to fail on a planted `tuikit/core` import. (2) `TestCommandMentionsParse` in `tree_test.go` beside `TestCommandTreeWellFormed` (which already had the shape rules): every example parses whole and has no placeholder; every backticked `` `gossms …` `` in the tree's text, in the rendered help of every topic, and in every string literal of `internal/cli/**` and `cmd/gossms/main.go` (go/parser scan) names a command — up to its first placeholder (`<x>`, `[x]`, `%s`, `...`), else it must parse whole, and `help <topic>` must name a command; the version flags pass. `TestCheckMentions` pins the checker; checked to fail on `` `gossms agent guide` `` planted in `noTerminalMessage`. Runtime hints are checked where tests produce them: `TestUsageErrors` and `TestUsageErrorKinds` run `checkMentions` on stdout. (3) `TestUsageErrorKinds` drives every `args.Kind` through Main (exit 2; a required arg via a test tree) and fails on a kind not exercised; `TestUsageErrors` gained missing value, bad enum, bool with a value, repeated option; `TestCancelledBeforeRun` is the pre-cancelled context. (4) was already `TestSelectMode` (0.b).)*
- ✅ **Done 2026-10-28.** **0.h Docs, same change as the code.** `docs/cli.md`: the contract so far — mode rules (§2.1), parsing (§2.2), envelope + text/json (§5.1, §5.2, D3), exit codes (§7) — trimmed from this file, marked "grows per phase". `ARCHITECTURE.md`: package-map rows for `internal/cli/`, `cli/spec`, `cli/args`, `cli/output` (summarized by directory like `tuikit`); doc-ownership row "`docs/cli.md` — the command-line contract: syntax, output formats, exit codes, safety classes"; an invariant paragraph in § Why split this way beside the tuikit one, with the same `go list` snippet the test runs; `cmd/gossms` row mentions the mode switch. README per D6. `CLAUDE.md` per D2. `docs/decisions.md`: the parser choice (D4) once settled. No `CHANGELOG.md`. *(As built: `docs/cli.md` — Modes, Syntax (options, response files, errors), Global options, Help (the agent pointer and its drift guard), Output (envelope, text, json), Exit codes, Commands, the import rule; states only what is implemented. `ARCHITECTURE.md`: the invariant paragraph after tuikit's, rows for `cli/`, `cli/spec/`, `cli/args/`, `cli/output/` after `version/` (whose row now names `Fields`), `cmd/gossms` row names `selectMode`, and the ownership row. `CLAUDE.md` also gained a required-reading row for `docs/cli.md`. `docs/decisions.md` § Command-line argument parser: hand-written. Every `comm-params §n` / `phase 0 Dn` citation in Go now names a `docs/cli.md` section.)*
- ◐ **Linux done 2026-10-28; Windows owed.** **0.i Verification (drive the binary, `docs/testing.md`).** Linux: `go build`, then `./gossms version`, `version -o json | jq .`, `nope; echo $?` (2, did-you-mean), `-p x` (2, pointer), `--help`, `help version`, `version --bogus` (2), `@args.txt`, `</dev/null ./gossms | cat` (1, < 1 s) — inside tmux too, so a controlling tty exists (R5's real case), `./gossms` in tmux still starts the TUI, `--version` diffed against the pre-change binary. Windows: `GOOS=windows go build`, run the same list on win10cli in `cmd`, PowerShell and (if installed) Git Bash; `| more` for the piped case. `gofmt`, `go vet`, `staticcheck`, `go test ./...`. *(Linux, 2026-10-28: every listed item behaves — `version` text/JSON (`jq` parses), `nope`/`verison`/`-p x`/`version --bogus`/`help nope` exit 2 with the stderr line and a hint naming `gossms help`/did-you-mean, `--help` first line is the agent pointer, `@args.txt` works; bare `gossms` with stdin, stdout or both redirected exits 1 in ~20 ms both outside and inside tmux (a controlling tty); bare `gossms` in tmux opens the TUI; `--version`/`-version`/`-v` byte-identical to a 33ea57f build with the same `-ldflags -X`. `gofmt`, `go vet` (also `GOOS=windows`/`darwin`), `staticcheck`, `go test ./...` clean; `GOOS=windows go build` succeeds. Not done: a live Ctrl+C (no phase-0 command runs long enough — covered in-process by `TestCancelled`), and **every Windows run** (cmd, PowerShell, Git Bash on win10cli) — there is no interactive shell on win10cli from here.)*

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

- ✅ **1.a `config`** (`profile.go`, `config.go`, `config_save.go`): `Connection.Profile`/`MaxAllow` (`AllowClass`, `Ceiling()`), outside `connectionAAD`; carry-over in `addOrUpdate`; `trimUnnamed` (named entries uncounted, never trimmed); `SetProfile` + `profileOp` replay; `Profiles`, `ProfileByName`, `ValidateProfileName`; `LoadReadOnly` (no key creation, sweep, corrupt copy or save — `Config.readOnly` makes `Save` refuse). Tests incl. two-instance replay and a mutation check of carry-over and trim.
- ✅ **1.b `db.RoleCLI`** (`goSSMS - CLI`, no capability probe). Verified live: `TestLiveConnectReportsTheRoleAsProgramName` (livedb) against 17.
- ✅ **1.c `internal/cli/connect`**: `Options()`, `Resolve` (precedence, overrides, D3, password sources, refusals, missing-password check before dialling), `Open` (dial as `RoleCLI` under `--connect-timeout`), `Classify` (18456 family / Entra `AuthenticationFailedError` → 4, 4060/4063 → 8, timeouts → 6, else 3; by type and number). Table tests through the real parser; mutation-checked.
- ✅ **1.d `internal/cli/cmd` + tree**: `profile` group, `list [--all]`, `show <name>`, `test <name> [connection options]`; `spec.Invocation.Given/Stdin/Getenv`. In-process tests through `Main` (exit 0, 1, 2, 3, 4, 8; no secret in any output). `checkCommandLine` learned `gossms help <group> <command>`.
- ✅ **1.e TUI**: `profile_dialog.go` (name, Allowed Operations, OK/Cancel; Tab reaches the buttons; clipboard host); Connect dialog **Profile...** beside Delete, gated like it; History label; F1 help. Tests: names the selected (non-first) row and saves, refuses bad/duplicate names in place, clears, gated without a row, keyboard button row, clipboard target.
- ✅ **1.f Docs**: `docs/cli.md` § Profiles, § Connecting, the three commands, exit codes produced; `README.md` § Command line (the full usage R19 promised for phase 1); `ARCHITECTURE.md` rows for `config/profile.go`, `cli/connect/`, `cli/cmd/`, `tui/profile_dialog.go`; open threads in `docs/open-threads.md` § Command line.
- ✅ **1.g Verification on Linux (driven binary, scratch `XDG_CONFIG_HOME`)**: TUI connect with Remember Password → named `dev`/Read through Profile... → `profile list/show/test` read it, password source `profile` (the sealed password opens after naming); the config directory byte-identical before/after CLI calls; 31 further TUI connects (server-case variants) with `profile list` every fifth one, no failure: `dev` kept, unnamed trimmed to exactly 30; a reconnect from the `[dev]` History row kept name and ceiling and made it most recent; no key file → `password: unreadable`, `profile test` refused (exit 4) and no key created. Live exit codes: 18456 → 4, unknown profile → 8 with did-you-mean, `-d nope` → 8 (4063), closed port → 3, unroutable host `--connect-timeout 2` → 6, overridden server without a password → 4, `entra-device-code` → 4. Majors 13 (`win10cli\sql2016,50110`, `--password-env`), 14 (`win10cli\sql2017,55253`, `--password-stdin`) and 17 connect; `GOSSMS_DATABASE` overrides and keeps the saved password. `gofmt`, `go vet` (linux/windows/darwin), `staticcheck`, `go test ./...` clean.
- ◐ **1.h Owed**: every Windows run (as 0.i); `--application-intent readonly` through the AAG listener connected but landed on the primary (`ubusql1`) for both intents — the group has no read-only routing list, so routing itself is unverified; Entra methods live (no Azure instance available).

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

- ✅ **Done 2026-10-28.** **2.a `internal/query` executor additions.** `Message.SQL *SQLError` (P2-4); `Message.RowsAffected` + `AfterSet` (P2-3); `TypedRowSink.BeginTypedSet(columns, types []string, nullable []bool)` asserted in `streamResultSet` (P2-1); `WithStopOnError` (P2-5); `WithoutSinkNotices` (P2-2). TUI unchanged — its text still comes from the same messages. Tests: unit through the existing sink/batch tests; live (`livedb`) for count attribution after a `SELECT` vs `UPDATE`, `NOCOUNT`, a trigger's count, and `ColumnTypeNullable` on 13/14/17. *(As built: `Message.SQL *gosmo.SQLError` on the header line only, `LineNo` script-corrected exactly when `Line` is (a module's error keeps its own line); `Message.RowsAffected *RowCount{N, AfterSet}` on the count message, text unchanged. `AfterSet` mirrors the driver's `colsReceived`: set on `MsgNext`, cleared on `MsgNextResultSet`, which go-mssqldb enqueues after every DONE — read from `token.go`, not assumed. **Limit found:** an `UPDATE … OUTPUT` count is `AfterSet` too — `MsgRowsAffected` drops the DONE token's `CurCmd`, so a DML count after OUTPUT rows reads like a SELECT's; 2.c flags it (`FlagOutput`) and **2.h must treat `FlagOutput` as a D4 blind spot**. `TypedRowSink` asserted in `streamResultSet`; nullability computed once in `newRowScanner` (an undescribed column reads nullable). `WithStopOnError` stops before the next batch or `GO n` repetition when the batch added an error message, and sets `Result.StoppedOnError`. `WithoutSinkNotices` drops `(n row(s) written)`, the per-set Max-fetch-rows notice and `cancelBatch`'s on the sink path. `executor.go` crossed 900 lines: the cell formatters moved byte-for-byte to `cells.go`. Tests: `executor_struct_test.go` (fake gained `counted` sets, `msgDone`, column types/nullability), mutation-checked; live `live_structured_test.go` passes on 17, 14 and 13: SELECT → `{3 AfterSet}`, UPDATE → `{2}`, NOCOUNT → none, SELECT INTO → `{4}`, a trigger's INSERT reports its own count before the statement's, OUTPUT → `AfterSet`. Nullability is the server's claim, not a guarantee: `a + 1` over a NOT NULL column is nullable, `ISNULL(b, N'')` is not.)*
- ✅ **Done 2026-10-28.** **2.b `internal/export` extraction.** `export.NewRecordWriter(w io.Writer, sep rune)` holding `writeRecord`/`csvNeedsQuotes` byte-for-byte; `csvSink` keeps the file stream and delegates. Golden test: the pre-extraction `csvSink` output for a table of edge cases (NULL, `""`, `'NULL'`, `\.`, leading space, lone NULL column, multi-set) is byte-identical after. TSV per D9. `ARCHITECTURE.md` row. *(As built: `export.RecordWriter` owns the `bufio.Writer` and the set separator — `NewRecordWriter(w, export.CSV|export.TSV)`, `BeginSet`, `WriteRecord`, `Flush` — so the CLI's csv gets the TUI's multi-set layout too; `csvSink` keeps `fileutil.Stream`/`Close`/`Abort`/`finish` and delegates. The golden `internal/export/testdata/results_to_file.csv` was written by the pre-extraction `csvSink` (edge cases plus a leading tab, a leading no-break space, a short `isNull`, an empty set); `TestCSVMatchesTheResultsToFileGolden` is byte-identical, a TSV test pins tab quoting, and both separators round-trip through `encoding/csv`. Driven: TUI Results To File from a HEAD build and the new build, same 3-set fixture query on 17 (NULL, `''`, `'NULL'`, leading space, comma, quote, `\.`, CJK, money, lone NULL column, empty set) — `cmp` identical. `ARCHITECTURE.md` rows for `export/` and `query/cells.go`.)*
- ✅ **Done 2026-10-28.** **2.c `internal/cli/safety`: classifier.** `Classify(script) []Statement{Batch, Line, Class, Leader, Reason}` + batch/script class; leader rules (§6.1), D1 floor, P2-10 module-batch rule, implicit-EXEC proc at batch start, read allow-lists (procs, DBCC), flags for transaction control, not-in-transaction statements, `NOCOUNT`, dynamic SQL, `PARSEONLY/NOEXEC/SHOWPLAN`. Table tests per §6.1 bullet plus the P2-9 merge cases; fuzz over `sqltext`'s corpus (no panic, class never below the leader rule). Mutation-check the floor. *(As built: `Classify(script) *Script{Statements, Class, Flags}`; `Statement{Batch, Repeat, Line, Class, Leader, Object, Reason, Flags}` — `Leader`+`Object` are what §6.1's `STATEMENTS: UPDATE Sales.Customer (line 1)` needs; classes are `spec.Class`, `Rank` orders them. Statements split by `StatementCutter` plus `;`; a label alone is no statement. Flags: `transaction-control`, `not-in-transaction`, `nocount`, `dynamic-sql`, `exec` (a call not on the read allow-list — D4's input), `mode-option`, `output-clause` (2.a's limit). SET options are read per ON/OFF list, so `SET NOCOUNT ON` merged with a following statement still flags. **Deviation from D1, for review:** `LOGIN` is not a bare floor word (a common column name); instead CREATE/ALTER/DROP before any administration object (`adminObjects`: DATABASE, SERVER, LOGIN, USER, ROLE, keys, certificates, AG, endpoints, resource governor, event sessions) is admin wherever it stands, cut or not. The FK `ON DELETE/UPDATE` exemption was dropped as dead (it only occurs inside DDL, which outranks write). **Rules beyond §6.1**, all fail-closed or plainly read: `UPDATE STATISTICS` and `ADD SIGNATURE` ddl; `CHECKPOINT`, `SETUSER`, `DBCC SQLPERF(…, CLEAR)`, every `xp_`/`sp_trace_`/`sp_OA` proc and every msdb `sp_` proc not on the read list admin; `SET IMPLICIT_TRANSACTIONS ON` write + transaction-control; `OPENQUERY/OPENROWSET/OPENDATASOURCE` write + dynamic; `EXECUTE AS`/`REVERT` read; `DBCC USEROPTIONS` read; a server-scoped `CREATE TRIGGER … ON ALL SERVER` admin; `RECEIVE`/`WRITETEXT`/`UPDATETEXT` floor at write. Not-in-transaction covers every DATABASE form (scoped configuration included), FULLTEXT, BACKUP, RESTORE, RECONFIGURE — the known set, not exhaustive. Tests: ~170 table cases, merges, lines through multi-line literals, module batches, flags, reasons, object names; `FuzzClassify` (seeded from the lexer's corpus) 2×60 s clean, ~1.5M execs each; 25 mutations of the floor, exemptions and rules all killed.)*
- ✅ **Done 2026-10-28.** **2.d `safety.Check` + `query classify`.** `Check(stmts, allow, ceiling) (*Blocked)`; refusals of §6.4. SQL input options shared by every `query` command: `--sql/-Q`, `--file/-i` (`-` = stdin; conflicts with `--password-stdin` → exit 2), `--sql-base64`, exactly one. `--allow` (default `read`). `query classify` needs no connection — the first end-to-end piece, testable with no server. *(As built: `safety.Check(sc *Script, Policy{Allow, Ceiling, Transaction, MaxRowsAffected}) *Blocked{Detected, Allowed, Statements, Message, Hint}` — the §6.4 transaction refusals and D4's blind spots (`nocount`, `dynamic-sql`, `exec`, `output-clause`) are already in it behind the two Policy flags, so 2.h only wires them; order: class vs min(allow, ceiling) → mode options (always) → transaction → max-rows-affected. The hint says whether `--allow` or the profile's ceiling refuses. `Statement.Describe()` = `UPDATE Sales.Customer (line 1)`, plus `: <reason>` when the leader alone did not decide. `query classify` answers a record **and** a set (envelope rule widened in `docs/cli.md`): `detectedClass`, `allowedClass`, `flags`, `statementCount`, `statements` (when blocked), then one row per statement. **Deviation:** classify's `--allow` has no default — absent, it only reports; given, it applies `Check` (exit 7). It takes no `--profile` (that arrives with `query run`, P2-15). Input: `--file` reads through a new `Invocation.ReadFile` (Main's `Env.ReadFile`); UTF-8 BOM dropped, UTF-16 LE/BE decoded by BOM (an unpaired surrogate refused), anything else must be valid UTF-8; 64 MB cap; Base64 padded or raw. Examples avoid `-Q "…"`: `TestCommandMentionsParse` splits on spaces. Tests: `safety/check_test.go` (class/ceiling/hint table, offending statements, mode options, transaction, D4, order), `cmd/query_test.go` (stdin conflict, `decodeScript`), `cli/query_test.go` (classify end to end, every input form and refusal); mutation-checked.)*
- ✅ **Done 2026-10-28.** **2.e `cli/output`: data model and buffered formats.** CLI `RowSink`/`TypedRowSink` (copy first N cells, count the rest, record truncation, P2-14); typed JSON cells (P2-12); `Column.Type/Nullable`; D5 fields; `--max-rows`, `--max-col-width` (D10), `--null-text`, `--no-headers`, `--include-messages/--no-messages`, the truncation `NOTE`; renderers `csv`, `tsv` (via `internal/export`, D6), `markdown`. `output.Formats` grows; goldens per format. *(As built: one `output.Sink` (`sink.go`) is the CLI's TypedRowSink for every destination — satisfied by shape, `output` imports no `query`. `Render(w, r, Options)` replaces `Render(w, format, r, quiet)`; `output.Options{Format, Quiet, MaxRows, MaxColWidth, MaxOutputBytes, NullText, NoHeaders, NoMessages, OutFile, Force}`, `DefaultOptions(format)`. **The output options are globals** (`cli/render.go`): Main makes the Sink before the handler (`Invocation.Sink`), a handler that streams uses it, one that builds `Result.Sets` has them replayed through it (`Sink.Add`) — so `profile list --max-rows 5` is cut like a query. **Spelling deviation:** bools `--headers`/`--no-headers` and `--messages`/`--no-messages` (the parser's `--no-` rule); no `--include-messages`. D5: `Result.RowsAffected` (`ROWS_AFFECTED`, JSON `rowsAffected` always present), `output.Error.SQL *SQLError{Number, Severity, State, Line, Procedure}` (JSON `sqlErrorNumber, severity, state, line, procedure` only on a SQL error; text lines after `ERROR_CODE`); new JSON keys `outFile` (top level) and `truncatedBy` (`max-rows`/`max-output-bytes`, the last limit that removed rows). Typed cells: `int/bigint/smallint/tinyint/real/float` → number (raw grid text, checked against JSON's number grammar, else string), `bit` → bool; a short hand-built row is padded with nulls. `--max-col-width` counts runes, marker `…[+1,234 chars]` (thousands separators as §5.3). Text: NOTE line after the truncated set's rows. csv/tsv: rows only via `export.RecordWriter` (gained `BeginHeaderlessSet` for `--no-headers`); stdout rows only, `RenderSummary` (the text envelope, sets as marker + note) to stderr on success too. **Deviation from §5.2:** csv/tsv write every set (blank-line separated), not the first only — §13's `cmp` against Results To File needs the TUI's layout; no `--all-result-sets`. Markdown: blank line after the marker, padded GFM table, `|` and `\\` escaped, line breaks `<br>`, `--no-headers` ignored. Goldens: every case × txt, quiet.txt, json, jsonl, md, csv, tsv, plus a `typed` case (SQL error, rows affected, long cell, specials, key clash). Tests: `sink_test.go` (max-rows copy-and-count, export-writer identity, text options, summary); 11 mutations killed.)*
- ✅ **Done 2026-10-28.** **2.f Streaming: `jsonl`, `--out-file`, `--max-output-bytes`.** `Invocation.Stdout` (P2-8); stream renderer with `_summary` line; D7 file handling; D8 cap writer. Tests: 100k-row stream without growth in retained rows; cap never yields invalid JSON (`json.Valid` over every cap 0…n on a fixture). *(As built: **`Invocation.Sink` instead of `Invocation.Stdout`** — the Sink holds stdout and decides per row (keep for Render, stream as a jsonl line, or write to the file), so no handler writes stdout itself. jsonl: a `{"_resultSet":{"index","columns":[{name,key,type,nullable}]}}` line per set, rows as objects keyed by column (nameless → `column<n>`, duplicate or `_resultSet`/`_summary` → `_<n>` appended), then `{"_summary":{…}}` = the json envelope without rows (`rowCount` = lines written), always written; `--max-rows` applies to jsonl too (`0` = stream all). `--out-file` (D7): extension → format, checked in Main **before the handler runs** (unknown extension, directory, existing file without `--force` → `USAGE_ERROR`, exit 2); `fileutil.CreateStream`, committed by `Sink.Finish` only when the result has no error, else aborted with a warning; no limits; `.json/.md/.txt` are rendered at Finish from rows held in memory (documented). stdout's sets carry no rows (`InFile`, marker `(n rows, sent to --out-file)`). D8: buffered formats render, then binary-search the most rows (in set order) that fit, dropping the last set's first; if none fit the rowless document is written with a warning; jsonl checks each line against the cap, summary exempt. `--force` is a global, valid only with `--out-file`. Tests: 100k-row jsonl stream (heap growth < 1 MB, nothing kept), `json.Valid` over every cap 1…n+10 for json and every jsonl line (plus rows ≤ cap), trailing-row order, every out-file extension (rows unclipped), failed run leaves the old file and no temp, refusals; Main-level `--out-file`. **Not yet driven against a server** — nothing streams real rows until `query run` (2.g); 2.l covers it.)*
- ✅ **Done 2026-10-28.** **2.g `query run` (autocommit).** `--profile/-p` + `connect.Options()` (P2-15); order: parse → classify → `Check` against `--allow` and `max_allow` (exit 7 *before* connecting) → `connect.Open` → `query.Open` → `ExecuteToSink` with D2 options → render. `--timeout` (context deadline), `--statistics io,time` (a `SET` batch before), `--dry-run` (D12), D11 mapping, Ctrl+C → 130 with `Close`. In-process tests with a fake connect; live run of every exit code it produces. *(As built: handlers in `cli/cmd/query.go`; `cmd.Deps.OpenSession` (`OpenQuerySession` = `query.Open` on the pool) returns a `cmd.Session` interface so tests fake the session. The ceiling is read from the profile alone, before `Resolve`, so a refusal needs no password; `--dry-run` = `query classify`'s answer with the effective `allowedClass`, needs no server even ad hoc. Executor gained `Result.Batches`/`FailedBatches` (each `GO n` repetition counted) for PARTIAL. Messages: SQL errors ≥ 11 → `errors` (their text line not repeated), counts → `messages` too, `rowsAffected` sums `!AfterSet` counts; a lost session → `CONNECTION_FAILED` first, carrying the driver's text; the executor's cancel notice dropped on cancel/timeout. Permission denied via `gosmo.IsPermissionDenied` over an `mssql.Error` rebuilt from the message's fields (gosmo could take its own `SQLError`). A stop before the last batch is a warning ("n of m batches were sent"). **Spelling deviation:** only `--continue-on-error` (stop is the default), no `--stop-on-error`. `ErrUnsupported…` → 11 does not arise for raw T-SQL. Tests: `cli/query_run_test.go` (rows/counts, five pre-connect refusals, dry run, 208 → 5, 229 → 9, PARTIAL vs all-failed, timeout, Ctrl+C, lost session); 11 mutations killed. Live on 17: 20k-row SELECT + UPDATE with `--max-rows 3` (exact 20000, UPDATE 15000), 208 → 5, read-only login → 229 → 9, timeout → 6, SIGINT on `WAITFOR` → 130 and no open CLI transaction left, `program_name` `goSSMS - CLI`, `--statistics`, jsonl and `--out-file` (20001 lines); smoke on 14 and 13.)*
- ✅ **Done 2026-10-28.** **2.h Transactions.** `--transaction auto|commit|rollback`, `--max-rows-affected`: `SET XACT_ABORT ON; BEGIN TRAN` batch, the script with stop-on-error, then `EndTransactions` by outcome; D3 after-check, D4 refusals, P2-6 refusal of `--continue-on-error`. Live: large `SELECT` + `UPDATE` under rollback (the phase's "done when"), an error mid-script, a proc that `COMMIT`s, `GO 3` counts. *(As built: record fields `maxRowsAffected` (when given) and `transaction` = `committed`/`rolled-back`/`ended-by-script`. D3 refined: `@@TRANCOUNT 0` after a run is XACT_ABORT's rollback when the run failed, **unless Msg 266 is among the errors** — the proc-COMMIT case also fails (266), so "clean run" alone missed it; then a `SQL_ERROR` is put first, worded "its changes may be committed" (live: the proc's rows *were* committed). Over the limit → `BLOCKED` first, rolled back; with `--transaction rollback` it rolls back either way and still exits 7. Timeout/cancel → `Close` (server rollback), `rolled-back`. Unreadable state → rolled back by closing, `GENERAL_ERROR`. Docs: `docs/cli.md` § Running T-SQL, § Transactions. Live on 17: rollback (data unchanged), commit, limit 10 vs 11 rows (commit / exit 7, unchanged), divide-by-zero in batch 2 under commit (exit 5, rolled back, batch 3 not sent), `GO 3` → 6 rows, proc COMMIT → `ended-by-script` exit 5, `--continue-on-error` → PARTIAL 10.)*
- ✅ **Done 2026-10-28.** **2.i `query parse`.** `SET PARSEONLY ON` in its own batch on the Session, the script, `OFF`; reports syntax errors with script lines; class read. Note in docs: names are not resolved. *(As built: every batch is sent (no stop-on-error), so all syntax errors come in one call; record `valid`, `batchCount`, `detectedClass`; no `--allow`. **Found:** `Check`'s mode rule refuses only `… ON`, so `SET PARSEONLY OFF; DROP …` would have switched the mode off and run the DROP — `safety.ModeWords` refuses any word PARSEONLY/NOEXEC/SHOWPLAN_* outside literals/comments for parse (exit 7, `STATEMENTS: line n`); `query run` keeps allowing `OFF` (2.d). Live on 17/14/13: syntax errors with script lines (SQL Server sends some, e.g. 156 for `UPDATE SET`, twice — reported as sent), an `UPDATE` parsed but not run, a missing table valid. `SELEC 1` is valid T-SQL (an implicit proc call).)*
- ✅ **Done 2026-10-28.** **2.j `--param` (D13).** *(As built: `cli/cmd/param.go`. A new option type `spec.TypeRepeat` (`repeat`): given once per item, each value whole — `TypeList` splits on commas, which `decimal(9,2)` and most values contain; help prints `(repeatable)`. `--param name=value:type` on `query run` only (parse under PARSEONLY would check the wrapper, not the script): name up to the first `=`, type after the last `:`, so a value may hold both. Name: optional `@`, then a regular identifier (`@@x`, brackets refused); type from a closed list of system types with their argument forms (`(n)`, `(p[,s])`, `(n|max)`), lower-cased — both are T-SQL in the declaration list, so they are matched, never escaped (`int, @x int` and `OUTPUT` refused). Value `gosmo.QuoteLiteral` (`N'…'`), the server converting to the declared type (bad value → Msg 8114, exit 5, line 0 as the server sends it); `binary`/`varbinary` take a checked `0x…` literal, since nvarchar does not convert to binary implicitly. No NULL value form. Duplicate names (case-insensitive) refused. One batch, `Count` 1, else exit 2 (dry run too). The script is classified as written; the sent text is `EXEC sp_executesql N'<batch>', N'@a t, …', @a = …;` with the batch's `StartLine` restored as leading newlines, so an error inside names its script line. Tests: `args_test` (repeat keeps commas and spaces), `cmd/param_test.go` (forms, 20 refusals incl. injection shapes, exact wrapper text, lines, batch refusals), `TestQueryRunParams` (sent text, UPDATE still blocked, usage errors before connecting). Live on 17: int/nvarchar with quote+comma+Unicode/decimal/varbinary/datetimeoffset round-trip, divide by zero on script line 4 reported as line 4, `abc:int` → 8114 exit 5; smoke on 14 and 13 (binary, time(1)).)*
- ✅ **Done 2026-10-28.** **2.k Docs.** `docs/cli.md`: § Query, § Safety (classes, `--allow`, ceiling, transactions), formats, limits, exit codes now produced; `README.md` § Command line; `ARCHITECTURE.md` rows for `internal/export/`, `cli/safety/`, `cli/cmd/query.go` and a "How a CLI call runs" section (§14.4); `docs/decisions.md` for D1, D3, D6, D7. *(As built: most of `docs/cli.md` landed with each substep (§ Output, § Running T-SQL, § Transactions, § Safety, exit codes); 2.k added § Parameters, the class table in § Safety, `--param` in § Commands and the run options. `README.md` § Command line: a `query` block and the output formats. `ARCHITECTURE.md`: § How a CLI call runs (parse → Sink before the handler → offline check → connect as RoleCLI → run → finish), package-map rows for `cli/cmd/{profile,query,param}.go`, `safety/` with `Check`, `output/` with the Sink and every format; `export/` and `query/cells.go` rows came with 2.b. `docs/decisions.md` § Command-line safety and output (D1 with 2.c's `LOGIN` deviation, D3 as refined in 2.h, D6 with 2.e's every-set deviation and D9, D7).)*
- ✅ **Done 2026-10-29.** **2.l Verification (driven binary).** Majors 13, 14, 17: every format against one fixture query (incl. NULL vs `''` vs `'NULL'`, `nvarchar(max)`, `xml`, money, `datetimeoffset`, GUID, `varbinary`, sql_variant); CLI CSV vs TUI Results To File `cmp` for the same query (§13); a 20k-row `SELECT` then `UPDATE` with `--max-rows 200` (exact total, UPDATE ran); rollback/commit/max-rows-affected paths; `program_name` in `sys.dm_exec_sessions`; a read-only login → exit 9; Ctrl+C on `WAITFOR DELAY` → 130 and no open transaction left; `gofmt`, `go vet` (3 GOOS), `staticcheck`, `go test ./...`. Windows still owed (as 0.i). *(Driven 2026-10-29 against a fresh build, throwaway database `gossms_p2l` and login `gossms_p2l_ro`, both dropped after. Fixture (NULL, `''`, `'NULL'`, leading space + comma + quote, 6000-char `nvarchar(max)`, `xml`, money, `datetimeoffset(7)`, GUID, `varbinary`, `sql_variant`, bit, float; two sets) in text/json/jsonl/csv/tsv/markdown on 17, 14, 13: exit 0 each, and every format identical across the three majors once server name and duration are masked; JSON keeps NULL ≠ `""` ≠ `"NULL"`, int/float numbers, bit bool, column types and nullability. TUI Results To File (tmux, scratch config) vs CLI `--out-file cli.csv` **and** `-o csv` stdout for the same query: `cmp` identical, both. 20k-row `SELECT` + `UPDATE` with `--max-rows 200` on 17/14/13: 200 kept, `totalRowCount` 20000, `truncatedBy` max-rows, `rowsAffected` 15000, data changed. On 17/14/13: `--transaction rollback` unchanged, `commit` +10, `--max-rows-affected 10` 10 rows committed / 11 rows exit 7 rolled back, `--param` under rollback unchanged. 17: `program_name` `goSSMS - CLI`; read-only login `UPDATE` → 229 `PERMISSION_DENIED` exit 9; SIGINT during `WAITFOR` inside `--transaction commit` → 130, `rolled-back`, no CLI session left, data unchanged. `gofmt`, `go vet` (linux/darwin/windows), `staticcheck`, `go test ./...` clean. Windows runs still owed with 0.i.)*

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

- ✅ **Done 2026-10-29.** **3.a gosmo additions** (`dev-with-local-gosmo`; built and live-tested in gosmo first on 13, 14, 17): `Database.ObjectByName` (P3-2/P3-5, incl. a `_CS_` database and a contained one), `SearchDefinitions`, `SearchColumns` (P3-1, `%`/`_`/`[` in the pattern match literally), `Table.IndexUsage` (P3-9, also via `View`), `Server.DatabaseSizes` if D3 is taken. Nothing existing narrowed. *(As built, gosmo working tree, untagged — gossms's `replace` re-enabled in `go.mod`, it had been commented out: `object_lookup.go` — `ObjectInfo{ObjectID, Schema, Name, Type (trimmed), TypeDesc, ParentSchema, ParentName, Created, Modified, IsMSShipped}`, `ObjectByName(ctx, schema, name)` (requireSchema; `SCHEMA_NAME() COLLATE DATABASE_DEFAULT = @p1 AND o.name = @p2`, ErrNotFound) and `ObjectsNamed(ctx, name)` (every schema, ordered by schema — what D2 needs, one shared select). `search.go`: `SearchDefinitions` (sql_modules ⋈ objects; an encrypted/CLR module and a database DDL trigger are never found) → `[]*SearchResult`; `SearchColumns` (U and V) → `[]*ColumnSearchResult{Schema, Object, TypeDesc, Column, DataType}` with `DataType` rendered by `catalogType` (lengths and UDTs; typed-xml/vector facets not read, documented). `Table.IndexUsage`/`View.IndexUsage` (`index_management.go`, `view_indexes.go`) → `[]*IndexUsage{IndexName ("" heap), IndexID, UsageVisible, UserSeeks/Scans/Lookups/Updates, LastUser* time.Time (zero = none)}` — keyed by name like `FragmentationStats`, so a `TableRef` works; heap included; without VIEW SERVER STATE (or 2022's PERFORMANCE STATE) the `IF HAS_PERMS_BY_NAME` branch reads every index with `UsageVisible` false (DatabaseFiles' pattern). `Server.DatabaseSizes` (`database_files.go`) → `map[database_id]DatabaseSize{DataKB, LogKB}` — **KB, not the plan's MB**, gosmo's unit everywhere else. Offline tests: both new searches' LOWER/ESCAPE predicate (mutation-checked), `ObjectByName` in `TestEmptySchemaIsRefused`; the collation, sweep and wrap guards pass unchanged. `live_discovery_reads_test.go` green on 13, 14 and 17 (also `TestLiveVersionSweep`): literal `%`/`_`, encrypted proc not found, PK's parent, heap and indexed view usage, a low-privilege login's `UsageVisible` false, sizes equal to `DatabaseFiles`' sums, and a `_CS_` database (`sales.Customer` not found, `ObjectsNamed` exact). **Not run: a contained database** — `contained database authentication` is 0 on all three instances and turning it on is a server change; owed. ARCHITECTURE.md feature map and diagrams 02/05/10 updated; gosmo `OPEN-THREADS.md` gained the double `gosmo: USE` prefix found in 3.c.)*
- ✅ **Done 2026-10-29.** **3.b CLI plumbing:** `cmd/common.go` (moved `profileOption`, a `connectDatabase(ctx, inv) (*db.ServerConn, *gosmo.Database, error)` helper — resolve, `connect.Open`, `CurrentDatabase` → `DatabaseByName`), the multipart-name parser (P3-3, `FuzzParseObjectName`), one-part resolution + did-you-mean (D2), and the gosmo error mapping (`ErrNotFound` → 8, `IsPermissionDenied` → 9, `ErrUnsupported…` → 11) shared by every read handler. Table tests with a fake connect. *(As built: `cmd/common.go` holds `profileOption` (moved from `query.go`), `connectOptions()`, the **`Catalog` interface** (the gosmo reads the discovery commands make, `Deps.Catalog`, production `cmd.ServerCatalog` over `sc.Server`) so handlers run against a fake in-process; `dial` (resolve + open, header and dropped-password warning) shared with `connectSession`; `connectCatalog` (db commands) and `connectDatabase` (`CurrentDatabase` → `DatabaseByName`, envelope gets the catalog spelling); `readError` — `*output.Error` passes, `ErrNotFound` → 8, `ErrUnsupported`/`ErrUnsupportedVersion` → 11, `IsMissingOrDenied` → 8 with a hint, other SQL errors via `sqlError` (permission → 9), `net.Error` → 3, else 1; `resolveObject` (D2: two-part as given; one-part one match / several = usage error listing them / none = NOT_FOUND) with a did-you-mean of up to 5 names from `Database.Search` (contains, case-insensitive — catches the case-only miss). `cmd/objectname.go`: `parseObjectName(s, database)` over `sqltext.Next` (quoted parts keep dots, `UnquoteName` each; whitespace between parts allowed; a number, comment, literal, empty part or 4+ parts refused), three-part only when the first equals the connection's database **exactly**. Tests: `objectname_test.go` (table + `FuzzParseObjectName`, round-trip through `ObjectName.String`, 30 s clean), `common_test.go` (`resolveObject`, `readError`, `connectDatabase` through `args.Parse`).)*
- ✅ **Done 2026-10-29.** **3.c `db list` / `db info`:** `--include-system` (system = `IsSystem() || IsDistributor`), `--state` enum from `gosmo.DatabaseState`, snapshots flagged; `db info` record: `sys.databases` fields + `Options` + `SpaceUsed`, with P3-7's degrade-to-warnings. In-process tests; live on 13/14/17 incl. an OFFLINE throwaway database. *(As built: `cmd/db.go`, group listed before `query`. `db list` set: `name, database_id, state, recovery_model, compatibility_level, collation (NULL off-line), read_only, size_mb, data_mb, log_mb (decimal strings from DatabaseSizes; NULL when the login cannot see the files), created, is_system, snapshot_of (source name)`; `--state` enum is `state_desc` lower-cased; a failed size read is a warning. `db info <name>` record: the sys.databases fields + `catalogCollation`, `isDistributor`, `snapshotOf`, sizes, `dataUnallocatedMb`/`logAvailableMb` (SpaceUsed — only when ONLINE, else a warning; a refused one too), then 20 SET options. **Correction to P3-7:** `Database.Options` reads `sys.databases` at server scope and works OFFLINE; only `SpaceUsed` needs the database. Unknown database → NOT_FOUND with a `db list --include-system` hint. `internal/cli/db_test.go` (fake `Catalog`): filters, system/distributor/snapshot, JSON typing, OFFLINE degrade, failed options/sizes reads, not found. Driven binary: 17 (incl. a throwaway OFFLINE database, a snapshot, a low-privilege login → NULL sizes + two warnings, exit 0), 14 and 13 ad hoc; throwaways dropped. `docs/cli.md` § Commands has both rows; the rest of 3.j still owed.)*
- ✅ **Done 2026-10-29.** **3.d `object list` / `object search`:** `--type` list enum (`table,view,procedure,function,trigger,synonym,sequence,type,schema`), `--schema`, `--name` (P3-8), `--include-system` off; `search <text> --in name|definition|column` (default `name`). Rows: `schema, name, type` (+ `column, data_type` for `--in column`). *(As built: `cmd/object.go`, group after `db`. **`spec.TypeList` may now carry an `Enum`**, each item checked with the enum's did-you-mean (`args.checkEnum`), help shows `<a|b|…>,...`, `TestCommandTreeWellFormed` allows Enum on a list. `object list` set: `schema` (NULL on a schema row), `name`, `type`, **`is_system` (added beyond the plan)**; sorted by kind in `--type` order, then schema and name case-insensitively (exact as tiebreak). `--schema` = TextEquals, `--name` = TextContains, both trimmed, pushed to gosmo where the listing takes an `ObjectFilter` (tables, views, procedures, functions) and applied in Go by the same rule elsewhere (triggers, synonyms, sequences, the three type listings, schemas — **synonyms, sequences and triggers take no filter either**, not only types/schemas as P3-8 said); a schema row matches either option by its own name. `--include-system` adds `TableKindSystem`, `SystemViews/StoredProcedures/Functions` (sys schema), the system schemas (ids 2–4, 16384–16399) — and the sys schema's CLR types (geography, geometry, hierarchyid), which `ClrTypes` returns and which the first live run listed as user types (fixed, pinned). Triggers = DML triggers only. `object search`: name → `Search`, definition → `SearchDefinitions`, column → `SearchColumns`; `type` is the kind word from `type_desc` (`kindOfDesc`); empty text exit 2. `Catalog` grew the thin per-family reads (`common.go`); `internal/cli/object_test.go` (`objectFake`, embeds `fakeCatalog`, which now embeds `cmd.Catalog` so unanswered reads panic) pins order, filter push-down, the Go rule, system objects, search shapes. Driven on 17, 14, 13 against a throwaway fixture database (all kinds, a CLR proc from gosmo's `testdata/clr/w7clr.dll`) and a `_CS_` one; master `--include-system --name DM_EXEC_REQ`, msdb `backupset`; dropped after.)*
- ✅ **Done 2026-10-29.** **3.e `object describe` / `object definition`:** P3-10 dispatch; describe = record (kind, created, modified, row count) + sets (columns, keys, indexes, check constraints, triggers — or parameters for a module); definition per D4. Live: an encrypted proc, a schema-bound view, a CLR proc (no definition). *(As built: **envelope additions** (`internal/cli/output`): `Set.Name` — text marker `=== RESULT n: name (…) ===`, JSON/jsonl `"name"` on every set, `null` when unnamed (goldens updated; the Sink carries it through `begin`); `Field.Null` (JSON null, text `KEY:`); `Field.Verbatim` — text prints it after the record as **`=== DEFINITION (n lines) ===`** then exactly n lines (**the line count is beyond D4**: a definition can hold a line that looks like a marker, so a reader takes n lines without scanning), `\r\n`→`\n`, never clipped; markdown fences it (fence longer than any backtick run); a null one prints no section. New golden case `verbatim`. **gosmo gained `Database.ReturnType(ctx, schema, name) (*Parameter, error)`** (parameter_id 0; nil for a procedure/TVF/no object; `parameterSelect` now `parameterQuery("p.parameter_id > 0")`, golden unchanged; live subtest green on 17/14/13; ARCHITECTURE feature map + diagram 05; untagged). describe: `lookupObject` (connectDatabase → parseObjectName → resolveObject), record `schema, name, type, typeDesc, objectId, created, modified, parent` + per kind (table `rowCount`; view `isSchemaBound`; function `returns` — type, `TABLE` for IF/TF/FT; trigger `isEnabled, events`; synonym `baseObject`; sequence `dataType … currentValue`); named sets `columns, keys, indexes, check_constraints, triggers` (table), `columns, indexes, triggers` (view), `parameters` (procedure/function); anything else (a constraint) record only. Any child read failing fails the command (no partial describe). Found driving: a view's identity column has no seed (`IDENTITY(,)` → `IDENTITY`), and **`sys.parameters.has_default_value` is always 0 for T-SQL modules**, so `has_default` is NULL for them and read only for CLR. definition: V/P/FN/IF/TF/TR through the family `Ref`'s `Definition` (`Catalog.ModuleDefinition`); PC/FS/FT/TA → null + "CLR module" warning; `""` → null + warning naming encryption or missing VIEW DEFINITION (the catalog cannot tell them apart); any other kind → USAGE_ERROR naming it, no record. Driven on 17/14/13: every kind, encrypted proc, schema-bound indexed view, CLR proc, ambiguous one-part name (exit 2), NOT_FOUND, another database's three-part name (exit 2), `Sales.[vCustomer]`, markdown and JSON; a `_CS_` database (`sales.Customer` → NOT_FOUND suggesting both spellings). `docs/cli.md` § Commands rows and § text/json/jsonl envelope notes updated; the rest of 3.j still owed. Not run: a low-privilege login (3.k).)*
- ✅ **Done 2026-10-29.** **3.f `object dependencies` / `indexes` / `stats`:** D7 traversal; indexes per D5 (key/included columns from `IndexColumn`, usage from 3.a); stats: name, auto/user, filter, last updated, rows, sampled, modification counter. *(As built: `cmd/object_dependencies.go`, `cmd/object_indexes.go` (object.go was 807 lines); `Catalog` grew `Dependencies`, `Dependents`, `Table/ViewIndexUsage`, `Table/ViewFragmentation`, `Table/ViewStatistics`, `StatisticColumns`. **gosmo gained `View.FragmentationStats`** (`view_indexes.go`, over the carrier; live subtest in `live_discovery_reads_test.go` green on 17/14/13; ARCHITECTURE feature map row; untagged). dependencies: `--direction uses|used-by|both`, **default `both`** (the plan named none), `--depth` 1–5 (else exit 2); record `schema, name, type, direction, depth`; set `dependencies` — `direction, level, via, schema, name, type, schema_bound, caller_dependent`, BFS, uses walk then used-by walk, visited set per walk (an edge back to a reached object is listed, not followed). **Found driving: a table's filtered index/statistic makes `sys.sql_expression_dependencies` record the table referencing itself** — `Dependencies` returns it; self-edges are dropped in the walk (gosmo's read left as is). Without SELECT on `sys.sql_expression_dependencies` (db_owner only by default) → exit 9 with a hint naming it. indexes: table or view (else exit 2); set `indexes` — structure + `hypothetical` (beyond the plan) + usage counts and last-use times (NULL when not readable, with a warning); a heap leads as `HEAP` index 0 (from IndexUsage, which `Indexes` (type > 0) lacks); `--fragmentation limited|sampled|detailed` adds `avg_fragmentation_pct, page_count, fragment_count, avg_page_space_used_pct` (NULL in LIMITED: gosmo's 0 there is "not read"), record gains `fragmentationMode`. stats: set `statistics` — `name, stats_id, columns` (**beyond the plan**, one `Statistic.Columns` read per statistic), `origin` (index/auto/user), `filter, last_updated, rows, rows_sampled, sample_pct, steps, modification_counter, no_recompute, incremental`; no `dm_db_stats_properties` row (never computed, or the login may not read it — indistinguishable) → those NULL, not gosmo's zeros, and a "n of m statistics show no properties" warning. **`readError` now gives a permission error a read's hint**; `sqlError`'s was `query run`'s ("the classifier is not the boundary") and every discovery command had shown it. Tests: `internal/cli/object_storage_test.go` (`storageFake`: a cyclic graph with a self-edge, heap, hidden usage, fragmentation modes, never-computed stats, denied dependencies), `TestReadError` hint; mutation-checked (visited set, self-edge, LIMITED NULL, stats NULLs). Driven on 17/14/13 against a throwaway database (filtered index and statistic, heap, schema-bound indexed view over a function, a pA ↔ pB EXEC cycle with an unqualified call), and on 17 as a low-privilege login (usage warning, stats warning 5 of 5, dependencies exit 9); dropped after. `docs/cli.md` § Commands rows; the rest of 3.j still owed.)*
- ✅ **Done 2026-10-29.** **3.g `agent schema` + `help -o json`:** generated from `spec` (P3-14): commands (path, summary, class, args, options with type/enum/default/env, examples), globals, exit codes, formats, `schemaVersion`. Test: every non-hidden command present; JSON stable (golden). *(As built: `internal/cli/agent/schema.go` — `agent.Build(tree, version, topic) *Schema`, a pure function of `spec.Tree` + `output.ExitCodes` + `output.Formats`; keys `schemaVersion, gossmsVersion (beyond the plan: a cached catalog can be told stale), formats, exitCodes[]{exit, code, meaning}, globals[], groups[]{name, summary} (group nodes listed apart from runnable commands), commands[]{path, summary, description (null when none, beyond the plan), operationClass, arguments[]{name, help, format, required, repeat}, options[], examples[]}`; an option is `name` (`--max-rows`, dashes included), `short` (`-o`/null), `type`, `enum` ([]), **`default` typed** (bool/number/string/array for a list, null when none), **`negation`** (a bool's `--no-<name>`, beyond the plan), `env`, `help`; every key always present; hidden commands and anything under a hidden group left out. Tree: `agent` group node + `agent schema [<group>] [<command>]` after `help`, no `Run` — Main answers it like help (`showSchema`/`writeSchema` in `help.go`): JSON outside the envelope for `-o text` (default) and `-o json`, any other format USAGE_ERROR, unknown topic USAGE_ERROR with did-you-mean (`unknownHelpTopic` generalized to `unknownTopic(tree, invoked, words)`). `help [-o json]` at any topic (`help`, `--help`, `<cmd> --help`, `help <group> [<cmd>]`) prints the same document for that topic; help in jsonl/csv/tsv/markdown refused. `checkCommandLine` checks an `agent schema` topic as it does a help topic. Tests: `internal/cli/schema_test.go` — golden `internal/cli/testdata/agent_schema.json.golden` (version pinned to `test`; `-update`), `TestAgentSchemaListsEveryCommand` (mutation-checked: dropping `query run` fails it), help-JSON ≡ schema for six spellings, topic narrowing, hidden left out; `internal/cli/agent/schema_test.go` pins the option shapes; `TestUsageErrors` rows for the refusals. Driven binary: full catalog (5 groups, 18 commands), `help query run -o json`, `-o jsonl` refusal, bare `agent` exit 2. `docs/cli.md` § Help + new § Agent schema + § Commands row; ARCHITECTURE package map `cli/agent/` row. The three agent pointers still name `gossms help` — they switch with `agent guide` in 3.h.)*
- ✅ **Done 2026-10-29.** **3.h `agent guide`:** sections per §8.2 (D6, D8), `--brief` (test: under ~6,000 bytes ≈ 1,500 tokens), `--topic overview|rules|output|exit-codes|recipes|<group>`, `-p` adds 3.i's environment. **Same change:** the three pointers switch to `gossms agent guide` (P3-12), `docs/cli.md` § Help updated. Tests: every example and every backticked command in the rendered guide parses (`checkMentions` over all topics × brief). *(As built: `internal/cli/agent/guide.go` — `BuildGuide(tree, version, topic, brief) *Guide` with `Markdown()`, `Text()`, `WriteJSON()`; `Topics(tree)`. Sections `overview, rules, calling, output, exit-codes, commands, recipes` (§8.2's "How to call it" and "Command index" are topics too, **`calling` and `commands` beyond the plan's list**), then the groups (`agent, profile, db, object, query`) — `--topic` is an enum filled from the tree in `newCommandTree`, so a typo gets a did-you-mean. Hand-written per D8: `agent/guide/{overview,rules,calling,output,recipes}.md` (go:embed); text before a `<!-- full -->` line is the brief form. Generated: exit codes (brief = one line, full = table), command index (command, class, summary — every runnable non-hidden command), group topics (description, class, *connects*, *checked against --allow*, usage, args, own options, examples; the connection options — the intersection of every `--profile` command's options, derived, no list — named once). Brief = all but recipes, 4,960 bytes. Recipes: explore a schema (§9 steps 1–2), read data (heredoc on stdin), safe data fix (classify → parse → rollback → `--max-rows-affected`); no plan/monitor recipe (phases 6/7). **D6 deviation: `-o json` is a JSON document outside the envelope** (`{schemaVersion, gossmsVersion, title, brief, topic, sections[]{id, title, body}}`, body markdown), like `agent schema` — the envelope's `data` holds only string fields, so `data.sections[]` cannot exist, and a result set would be clipped by `--max-col-width`. Markdown unless `--output` is given (`inv.Given`), `-o text` = `agent/plain.go` (headings underlined, tables aligned, code indented 4, no markup), jsonl/csv/tsv USAGE_ERROR; Main answers it like schema (`showGuide`, `guideCommand` in `help.go`). **`-p` not added: it moves to 3.i** — the environment section is `agent context`'s output and an option is in the tree only once it works; until then `gossms agent guide --brief -p dev` (§8.6) is exit 2. Pointers switched: `agentLine` ("AI agents: run `gossms agent guide` for complete usage instructions."), the no-command hint (`… Run `gossms help` for usage; AI agents: `gossms agent guide`.`), `noTerminalMessage`. `checkCommandLine` accepts bare `gossms` (rule 6 names it). Tests: `internal/cli/guide_test.go` — `TestAgentGuideMentionsParse` (every topic × brief: backticked mentions + every code-block line starting `gossms `; mutation-checked with a `plan estimated` recipe line and an unknown option in rules), brief < 6,000 bytes, section order, brief/full split, index completeness, group topic, formats and refusals; `agent/plain_test.go`; schema golden updated (new command); `TestCheckMentions` rows. Driven binary: brief, full text, `--topic object -o text`, `--topic rules -o json`, `-o csv` refusal, `--topic objct` did-you-mean, `gossms -p dev`, `--help` first line, bare gossms with stdin redirected. `docs/cli.md` § Modes, § Help, new § Agent guide, § Commands row; ARCHITECTURE package map rows.)*
- ✅ **Done 2026-10-29.** **3.i `agent context -p`** (also `agent guide -p`, moved here from 3.h: adds the "Your environment" section, `agent.Guide` gains it as the last section; the §8.6 snippet parses only then): server version/edition/major, login, current database, effective ceiling (profile `max_allow` ∧ default `--allow`), databases (name, state, accessible), missing permissions from `Capabilities` (VIEW SERVER STATE, VIEW ANY DEFINITION, VIEW DATABASE STATE, VIEW DEFINITION in the current db) each mapped to the **implemented** commands it breaks, profiles available. Live with a read-only login (§13: "reports missing permissions correctly"). *(As built: **gosmo gained `Server.DatabaseAccess(ctx) map[database_id]bool`** (HAS_DBACCESS for the whole instance in one read, beside `Database.Capabilities`; a database not ONLINE reads false even for sa — checked live) and **`VIEW DATABASE PERFORMANCE STATE` in `ProbedDatabasePermissions`** (2022's narrow name, Unknown before; `live_probednames_test` skips it below 2022); live subtest `DatabaseAccess` (OFFLINE scratch database, a login with no user) and the probed-names test green on 13, 14, 17; ARCHITECTURE feature map + diagram 02; untagged. `cmd/agent.go`: `agent context` (after `agent schema`), `ConnectOptions()` (exported, was `connectOptions`); `Catalog` grew `ServerInfo`, `ServerCapabilities`, `DatabaseCapabilities`, `DatabaseAccess`; `currentDatabase` split out of `connectDatabase`. Record `profile` (null ad hoc), `serverName, productVersion, majorVersion, edition, login, isSysadmin` (null unprobed), `database, databaseRoles` (**beyond the plan**: fixed roles held there), `defaultAllow` (read), `maxAllow` (admin ad hoc), `maxAllowSource` (profile / default / ad hoc). Named sets `databases` (name, state, accessible NULL when unanswered, is_system), `missing_permissions` (permission, scope, commands, effect), `profiles` (name, server, database, max_allow). **Missing = the server answered for one of the need's alternatives and granted none**; Unknown is never a lack, so a failed probe lists nothing (warning instead). Needs (`permissionNeeds`, only implemented commands — `TestAgentContextNamesOnlyCommands`): VIEW SERVER STATE | PERFORMANCE STATE → `object indexes` usage NULL; **VIEW ANY DATABASE (beyond the plan's four)** → `db list` shows only master, tempdb and owned; VIEW ANY DEFINITION | CREATE/ALTER ANY DATABASE → sizes NULL; VIEW DEFINITION (current db) → the object commands see only permitted objects, definitions null, **dependencies empty — not exit 9: on 14 and 17 a db_datareader login reads `sys.sql_expression_dependencies` filtered, contradicting 3.f's hint (open-threads)**; VIEW DATABASE STATE | PERFORMANCE STATE → `--fragmentation` exit 9 (Msg 297, checked live). `agent guide` takes the connection options; given a profile or `--server` (command line or GOSSMS_PROFILE) `showGuide` runs `agent context`'s handler over the same invocation (`call` now takes the handler; signal context as for a command) and appends `agent.Environment(res, profile, brief)` — id `environment`, "Your environment (profile: x)", the record as a list, each set a table, **brief caps each table at 15 rows with a count**; after a `--topic`'s section too. A failed context still prints the guide, the section names code/message/hint, stderr gets the summary line, exit = the error's. `calling.md` brief names `agent context`. Tests: `internal/cli/agent_context_test.go` (record, NULL access, alternatives, failed probes, guide section last in markdown and JSON, brief cap, connect failure exit 3, no connection without -p); `missing` mutation-checked both ways; schema golden updated. Driven: 14 as sa and as a db_datareader login (all four gaps; each effect checked against the real command), 17 likewise, `agent guide --brief -p win2025` (exit 0, **6,330 bytes with the environment — the 6,000 target holds only without -p**), `-p nosuch` (guide + NOT_FOUND section, exit 8), `--topic rules -p … -o text`. `docs/cli.md` § Agent guide (environment), § Commands rows; ARCHITECTURE package map. Not run: 13 through the CLI (gosmo side only), a profile with `max_allow read` live.)*
- ✅ **Done 2026-10-29.** **3.j Docs:** `docs/cli.md` § db, § object, § agent, exit codes produced; README § Command line; `ARCHITECTURE.md` rows (`cli/agent/`, `cmd/db.go`, `cmd/object.go`, `cmd/common.go`); `docs/decisions.md` for D2, D4, D6. *(As built: the § Commands rows and § Agent guide/schema had landed with 3.c–3.i, so `docs/cli.md` gained the rest — intro and code list through phase 3, § Connecting's command list (`-p` on every connecting command but `profile test`), the exit-code sentence (11 only from a catalog read refusing the version), and a new **§ Discovery**: D1's database, the name parser's rules, D2's one-part resolution (the NOT_FOUND hint lists names *containing* the given one — not edit-distance), catalog-collation comparison, metadata visibility vs exit 9. README § Command line: a `db`/`object` example block and the `agent guide --brief -p`/`agent schema` pointer. ARCHITECTURE package map: `cmd/common.go`, `objectname.go`, `db.go`, `object.go`, `object_dependencies.go`, `object_indexes.go` (`cli/agent/` and `cmd/agent.go` were in already). `docs/decisions.md` § Command-line discovery and the agent guide (D2, D4 with the line count, D6 with the out-of-envelope JSON).)*
- ✅ **Done 2026-10-29.** **3.k Verification (driven binary):** every command on 13/14/17 against AdventureWorks-style schema and a throwaway `_CS_` database; read-only login for exit 9 and `agent context`; `gofmt`, `go vet` (3 GOOS), `staticcheck`, `go test ./...`. **The phase's "done when":** a fresh subagent given only the §8.6 snippet and a profile runs §9 steps 1–2 unaided — record its transcript's command list and any call that failed to parse. *(As built: throwaway `gossms_p3k` (Sales/Production schemas, identity, persisted computed column, alias and table types, sequence default, FKs incl. cascade, filtered and included-column indexes, user statistic, heap, schema-bound indexed view, scalar UDF + inline TVF + proc chain, encrypted proc, DML trigger, synonym, `dbo.Customer` view beside `Sales.Customer` for ambiguity, a definition holding a fake `=== RESULT` line), `gossms_p3k_cs` (`Latin1_General_CS_AS`) and a db_datareader login `gossms_p3k_ro`, all dropped after (checked 0 left on each). 43 command lines per instance (every phase-3 command, each refusal: ambiguous one-part name, NOT_FOUND, another database's three-part name, four parts, `--type tabel`, `--depth 6`, `indexes` of a procedure, `definition` of a table) on 17, 14 and 13: **identical exit codes and, normalized for ids/timestamps/sizes, identical output** across majors. Read-only login on all three: `agent context` lists VIEW SERVER STATE, VIEW ANY DEFINITION, VIEW DEFINITION and VIEW DATABASE STATE, and each effect matches the command (`--fragmentation` exit 9, sizes NULL + warning, invisible proc NOT_FOUND, dependencies empty). **Found and fixed:** (1) **gosmo `Table.CheckConstraints` scanned a NULL `sys.check_constraints.definition` into a string — `object describe` of any table with a CHECK was GENERAL_ERROR (exit 1) for a login without VIEW DEFINITION**; now `ISNULL(…, '')` as `columnSelect` already did (gosmo `table.go`, untagged). (2) A hidden computed/default/check expression printed as `""` or a bare ` PERSISTED`; now NULL with one "n computed, default or check definitions read NULL" warning (`hiddenDefinitions`, `TestObjectDescribeHiddenDefinitions`). (3) **A column of an alias type the login cannot see was dropped** — gosmo inner-joined `sys.types` (Sales.Customer showed 4 of 5 columns). Fixed after 3.k on request: gosmo LEFT JOINs `sys.types` in `columnSelect`, `parameterQuery`, `catalogColumnsSelect` and `SearchColumns` (`typeNameSQL`/`typeHiddenSQL`, `column_type.go`); **`IsTypeHidden` on `Column`, `Parameter`, `CatalogColumn`, `ColumnSearchResult`** — DataType the base system type (`TYPE_NAME(system_type_id)`; "" for a CLR type, whose 240 has no name), TypeSchema "", IsUserDefinedType true (so the TUI full-text editor still skips it), no bound rule/default (cannot be told from the type's); `TypeString` renders the storage type (`nvarchar(25)`); **`ScriptTable` and table-type scripts refuse with ErrUnsupported** (`hiddenTypeRefusal`). Tests: `hidden_type_test.go` (mutation-checked), version-gate goldens `columns.sql`/`parameters.sql` regenerated, `live_hidden_type_test.go` (a SELECT/EXECUTE-only login: columns, CHECK definition, parameters, SearchColumns, Catalog, ScriptTable refused, sa still sees `[dbo].[Phone]`) green on 17, 14, 13 with `TestLiveDiscoveryReads` and `TestLiveVersionSweep`. gossms: `hiddenTypes` warning in describe (columns, parameters, return type) and `search --in column`; the five TUI fake `sys.columns` responses widened to 42 values. Driven as a db_datareader login: `ph` reads `nvarchar(25)` with the warning, sa still `[dbo].[Phone]`. Untagged. Then, on request, the remaining reads: **a sequence over a hidden alias type vanished from `Sequences` and was NOT_FOUND by name** — `sequenceSelect` now LEFT JOINs, `Sequence.IsTypeHidden` (DataType the base, DataTypeSchema ""), `ScriptSequence` refuses with ErrUnsupported, gossms describe warns (driven: `dataType` `int` + warning, exit 0); `catalogAggregatesSelect` LEFT JOINs (a CLR aggregate returning a hidden CLR type dropped from the catalog; `Returns.IsTypeHidden`); `partitionFunctionSelect` LEFT JOINs too, though the server refuses an alias input type (Msg 7704, live-checked), so it could not drop. Golden `sequences.sql` regenerated; live test green on 17, 14, 13. Still inner-joined: the CLR module scripter and the linked-server column read. Done-when: a fresh general-purpose subagent given only the §8.6 snippet, the binary's path, profile `win2025` and the task "the orders-for-a-customer lookup in gossms_p3k is slow" ran 18 calls — `agent guide --brief -p`, `agent guide --topic object`, `object list --type procedure,function,view`, `object search order`, `object definition` ×5, `object dependencies --direction uses --depth 3`, `object indexes` ×2, `object describe` ×2, `object stats` ×2, `db info`, one read `query run` over `sys.sql_modules` — **every one exit 0, none failed to parse**, and it found the proc → inline TVF → per-row scalar UDF chain and the catch-all `@Since` predicate. It noted that the definition-marker rule was only in the full guide, so a text parser could be fooled; moved into the brief (`output.md`, brief 5,390 bytes). gofmt, vet ×3 GOOS (gosmo also `-tags livedb`), staticcheck, `go test ./...` in both repos green.)*

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

- ✅ **Done 2026-10-29.** **4.a gosmo additions** (`dev-with-local-gosmo`; built and live-tested in gosmo first on 13, 14, 17): `Server.FileVolumes` + the plausibility method (P4-2; `usableDiskVolumes` switched to it in the same change, TUI behavior unchanged), `Database.SpaceReport` and `Database.TableSizes` (D1; live test parses `sp_spaceused`'s KB strings and compares exactly — incl. a table with an XML index, a LOB column in a nonclustered index's INCLUDE, a heap, a partitioned table), `Database.LogSpace` (D2; version gate unit-tested with a fake `ServerInfo`, live on 13 SP3 / 14 / 17). Nothing existing narrowed; ARCHITECTURE feature map rows; untagged. *(As built, gosmo untagged: `database_space.go` holds `SpaceReport`, `TableSizes`, `LogSpace`; `FileVolumes` and the `Plausible()` methods on `FileVolume` and `DiskVolumeInfo` are in `server_config.go`, and the TUI's `usableDiskVolumes` calls `DiskVolumeInfo.Plausible` (same predicate). `FileVolumes(ctx) ([]FileVolume, visible bool, error)` — the "Visible=false" of P4-2 is a second return; `FileVolume` also carries `PhysicalName` (D7's sample-path fallback). `sp_spaceused` read live on 13, 14 and 17: 2016 counts pages over `sys.partitions`, 2017+ over `sys.system_internals_partitions` and adds the memory-optimized checkpoint files to database_size and PRECREATED ones to unallocated — gated on 2017; file sizes come from `sys.sysfiles` (ROWS and LOG only, as the procedure). The internal-type list is 2025's superset (adds 30, 31, 235). **2025's own `sp_spaceused` fails Msg 262 for a reader without VIEW DATABASE PERFORMANCE STATE** (its xtp DMV); `SpaceReport` asks first and sets `XTPPrecreatedHidden` instead. `TableSizes` reads `sys.partitions` + `sys.allocation_units` rather than `dm_db_partition_stats`, so it needs no VIEW DATABASE STATE — matches `sp_spaceused <table>` exactly for a heap with LOB, primary+secondary XML indexes, an nvarchar(max) INCLUDE, a 3-way partitioned table and an empty table on all three. `LogSpace` adds `UsedPercent` (the DMV's own figure); the 13.0.5026 gate is `buildAtLeast` in `version_gate.go`. **P4-5/P4-9 correction: both `dm_db_log_space_usage` and `dm_db_log_info` need VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE), not VIEW DATABASE STATE** — a reader granted VIEW DATABASE STATE still gets Msg 300 on 13 and 17; 4.h's `permissionNeeds` row for `db log-usage` must name the server permission. Live test: `live_space_report_test.go`, which brackets `SpaceReport` with two `sp_spaceused` calls because a fresh database's figures settle a second after setup (2016 moved 2 pages). `TestLiveVersionSweep` green on all three.)*
- ✅ **Done 2026-10-29.** **4.b CLI storage plumbing** (`cmd/db_storage.go`): `--unit` option and the column-name/format helpers (D5), the `<name>`-xor-`--all` check (D6), the `--all` walker (`Databases` + `DatabaseAccess` + state → readable list and warnings, D3), the warning builder (D4), `Catalog` additions (`Files`, `DatabaseFiles`, `FileVolumes`, `SpaceReport`, `TableSizes`, `LogSpace`). Table tests with the fake `Catalog`. *(As built: `sizeUnit` (`column(base, nullable)` → `base_kb` bigint / `base_mb`·`base_gb` decimal(18,2); `text`/`cell`), `pctOf` (exact, for filters) / `pctCell` / `pctColumn`; `allOption`, `nameOrAll` (USAGE_ERROR prefixed with the command's name); `storageDB{DB, Unreadable, Denied}` from `storageDatabase(name)` (NOT_FOUND as `db info`; sets the envelope's database) and `storageDatabases(withSnapshots)` (system databases included, catalog order); state is checked before HAS_DBACCESS, and a failed `DatabaseAccess` read is one warning with ONLINE databases left to their own reads. `unreadableError`: a state → NOT_FOUND (exit 8, D3); **no access → PERMISSION_DENIED (exit 9)** — D3 names only the state case, and 9 is what the read itself would have answered (Msg 916). `fileWarnings(db, f, usedKnown, *fileVolume)` in the order free / cap / growth / volume; free, cap and growth apply to ROWS and LOG only; `max_size` 0 (no growth) is not "capped". `logReuseWarning`. **D5 correction: the parser's enums are case-*sensitive*** (`args.checkEnum` is `slices.Contains`) — `--unit MB` is a usage error with a did-you-mean, not accepted; D5's outcome (lower-case) stands. Unit tests: `cmd/db_storage_test.go`; ARCHITECTURE package-map row added. Nothing driven against a server — 4.b has no command yet.)*
- ✅ **Done 2026-10-29.** **4.c `db files`:** P4-1/P4-6/P4-7, `--type`, `--filegroup`, `--sort-by name|size|used|free-pct`, `--max-free-pct`, `volume_free_pct` (D7). `--all` adds `database` first. Tests: OFFLINE fallback, FILESTREAM row, no-VSS login, filter keeps warnings in step. *(As built, in `cmd/db_storage.go` § db files; registered from `DBCommands`, whose group summary now names files; agent-schema golden updated. Columns `[database] logical_name type filegroup physical_path size_<u> used_<u> free_<u> free_pct growth max_size_<u> volume_free_pct`; `growth` and `max_size_<u>` are `nvarchar(30)` text (`unlimited`, `none`, `10 %`). Default order is database (catalog order), then **file_id** — gosmo's `Files`/`DatabaseFiles` order by `type_desc`. `--sort-by`: name = database then logical name; size/used largest first; free-pct fullest first; unknowns last. `--max-free-pct` is an int 0–100 (the parser has no float type) and keeps only files whose free % is known. `--filegroup` is case-insensitive and drops master_files-fallback rows (filegroup unknown). Database-level warnings (fallback, master_files invisible) stay whatever the filters; per-file ones (incl. a snapshot's on-disk size) only for rows shown. A `Files` error that is not cancellation falls back to master_files like an unreadable state. **Found live and fixed:** (1) **a snapshot's `sys.database_files` is the source's file list** — source paths, its log (FILEPROPERTY NULL → "100 % free") and its FILESTREAM container; a snapshot is now listed from `master_files` (its own `.ss` files) with filegroup/used merged by file_id. (2) A data/log `SpaceUsedKB` of 0 (FILEPROPERTY NULL) reads as unknown → used/free/free_pct NULL, no free warning. (3) Growth under `--unit gb` rounded 1 MB to "0 GB": growth is in KB under `--unit kb`, else MB. Driven on 17, 14, 13 with a throwaway (`gossms_p4c`: PRIMARY + capped `Archive` filegroup, 10 % log growth, FILESTREAM on 17 only), an OFFLINE throwaway, a snapshot and a db_datareader login without VIEW SERVER STATE / VIEW ANY DEFINITION: output identical across majors; data files equal `sys.database_files` size×8 / FILEPROPERTY×8 KB exactly (log moved 8 KB between calls); refusals exit 2/8 as specified; the read-only login gets `volume_free_pct` NULL + one warning, unreachable databases named under warnings (exit 0). All dropped, 0 left on each. `gofmt`, `go vet` (3 GOOS), `staticcheck ./internal/cli/...`, `go test ./...` green. Not done here (4.h): `permissionNeeds` rows, docs/cli.md, README.)*
- **4.d `db space`:** one row per database (D1 figures + `log_size/log_used/log_used_pct` from `LogSpace`'s size/used, so `db space` and `db log-usage` cannot disagree); `--all` per D3.
- **4.e `db log-usage`:** D2 columns per §10.8 + `vlf_active` (beyond §10.8 — say so); reuse-wait warning; snapshots excluded.
- **4.f `db table-sizes`:** `--top` (default 20, 0 = all), `--schema` (TextEquals, as `object list`), `--sort-by reserved|rows|data|index|unused` descending, name as tiebreak.
- **4.g `server volumes`:** `server` group node (summary names volumes only, P4-9) + the command per D7.
- **4.h Agent and docs:** `permissionNeeds` rows (P4-9) with `TestAgentContextNamesOnlyCommands` green; guide recipe "Check space" (`db files --all --max-free-pct 10`, `db log-usage --all`, `server volumes`) passing `checkMentions`; `docs/cli.md` § Storage (units, warnings, `--all`, D3), README example, ARCHITECTURE package map (`db_storage.go`, `server.go`), `docs/decisions.md` for D1, D2, D4; §4.3/§10.8 of this file corrected (D5 case, D6).
- **4.i Verification (driven binary):** throwaway database with two filegroups, a capped file, percent growth, a FILESTREAM filegroup (where enabled), an XML-indexed table and a partitioned one; an OFFLINE throwaway and a snapshot; the read-only login. Every command and refusal on 17, 14, 13: figures compared to `sys.database_files`, `sp_spaceused` (db and per table) and `dm_db_log_space_usage` run through `gossms query run` on the same connection — exact in KB (§13). tempdb on each; Linux (17) and Windows (13/14) paths. `gofmt`, `go vet` (3 GOOS), `staticcheck`, `go test ./...` in both repos. Throwaways dropped and checked.

**Not in phase 4:** shrink or any file write (`maint`, phase 8), `db set` (phase 5), tempdb contention/version-store detail (`TempDBSpace` — `monitor`, phase 7), backups' sizes (phase 8), changing the TUI's figures to `sp_spaceused`'s.

### Phase 5 — Server configuration

- `server info/config list/get/set/reset/memory/traceflag/log`, `db scoped-config list/get/set`, `db set --option`.
- gosmo: `sp_configure` defaults table, trace flags.
- **Done when:** the §10.1–§10.6 examples run as written (dry-run batch from `ApplyConfiguration`, risky-value blocks, `restart_required`).

### Phase 6 — Plans, scripting, data

- `plan estimated/actual` (+ `summary` digest from `internal/showplan`), `script object/db`, `data top/count/sample/profile/export/import`.
- **Done when:** `script object` output equals the TUI's Script As for the same object, and an export → import round trip preserves NULLs and empty strings.

### Phase 7 — Monitoring

- `monitor sessions/blocking/waits/top-queries/querystore/deadlocks/kill`, `plan cached`, `xevent sessions/capture` (reusing `internal/activity`, `internal/xevent` and gosmo's XE templates).
- gosmo: plan-cache top queries, `system_health` deadlocks.

### Phase 8 — Administration

- `security …`, `job …`, `maint backup/restore/backup-history/index-fragmentation/index-rebuild/update-stats/checkdb`, `db create/drop`.
- gosmo: `DBCC CHECKDB`.
- Audit log.
- **Done when:** every destructive command needs `--confirm`, every write has a `--dry-run` that matches what then runs, and the audit log never holds a secret.

### Phase 9 (optional) — Session mode and MCP

- `gossms shell --protocol jsonl` over the same `spec` handlers with one held Session; then an MCP server exposing `spec` as tools. Scope decided at the end of phase 8 (§Open decisions 6).
