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
| `--format <markdown\|text\|json>` | Default `markdown`. `json` returns the same sections as structured fields |
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

- `config`: `Profile`, `MaxAllow`, carry-over in `AddOrUpdate`, trim exemption, set-profile `connOp`, `LoadReadOnly`, `ProfileByName`.
- TUI: Connect dialog **Profile…** dialog; History shows names; F1 help; README.
- `db.RoleCLI`. CLI connection resolution: profile + overrides + env + password sources, Entra method refusals.
- Commands: `profile list/show/test`.
- **Done when:** a profile named in the TUI is usable from the CLI, survives 30 more connects and a reconnect, a sealed password still opens after renaming, and a running TUI is undisturbed by CLI calls (§14.7).

### Phase 2 — `query` and the safety model (the core agent loop)

- `query` executor additions (structured errors, rows affected, typed sink); `internal/export` extraction (TUI Results To File re-verified byte-for-byte).
- `cli/safety` classifier over `sqltext`; `--allow`, `max_allow`, `--dry-run`, `--transaction rollback`, `--max-rows-affected`.
- `cli/output`: all formats, row/col/byte limits via the sink, truncation markers, `--out-file`.
- Commands: `query run/parse/classify`.
- **Done when:** the §13 safety and truncation criteria pass on a live server, including a large `SELECT` followed by an `UPDATE` under `--transaction rollback`.

### Phase 3 — Discovery: `db` (read), `object`, `agent`

- `db list/info`, `object list/describe/definition/dependencies/search/indexes/stats`.
- gosmo: search in definitions and columns.
- `agent guide` (+ `--brief`, `--topic`, `-p`), `agent schema`, `agent context` (capabilities probe → missing permissions).
- **Done when:** an agent given only the §8.6 system-prompt snippet can explore a schema end to end (§9 steps 1–2), and every example in the guide parses (test).

### Phase 4 — Storage and size reporting

- `db files/space/log-usage/table-sizes`, `server volumes`; warnings; units in column names.
- gosmo: log reuse wait + VLFs; table sizes if needed.
- **Done when:** sizes match `sys.database_files`, `sp_spaceused` and `sys.dm_db_log_space_usage` on all three majors (§13).

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
