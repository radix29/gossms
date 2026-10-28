# Command-Line Interface Design for AI Agents

**Purpose:** Specify command-line parameters for **gossms**, an existing SSMS-like TUI application written in Go that runs on Linux, macOS and Windows. The design extends the existing project with a headless command-line mode so that AI agents (and scripts) can use its functionality. Every command returns text that is predictable and easy to parse.

**Scope:** this document defines the command-line interface only: commands, parameters, output, safety rules and exit codes. Implementation details and rollout planning are out of scope and will follow separately.

> **One binary, two modes.** `gossms` with no parameters starts the TUI, as it does today. `gossms <group> <command> …` runs headless CLI mode and never starts the TUI. §2.1 has the rules.
>
> **Agents start here:** `gossms agent guide` describes the tool to an AI agent (§8).

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

The rule is simple: **no parameters means TUI, any parameter means CLI**. An agent that follows the guide always passes a command, so it never ends up in the TUI.

If the TUI is started without a usable terminal (for example, an agent calls bare `gossms` by mistake with piped I/O), it should exit right away with a clear error on stderr and exit code `1` instead of hanging. The error should mention `gossms agent guide`.

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
| `--profile <name>` | `-p` | `GOSSMS_PROFILE` | Use a connection profile from the existing gossms config file (recommended for agents) |
| `--server <host[\instance][,port]>` | `-S` | `GOSSMS_SERVER` | Server name |
| `--database <name>` | `-d` | `GOSSMS_DATABASE` | Initial database |
| `--auth <integrated\|sql\|entra-interactive\|entra-default\|entra-sp>` | | `GOSSMS_AUTH` | Authentication mode. `integrated` = Windows SSPI on Windows, Kerberos on Linux/macOS |
| `--user <name>` | `-U` | `GOSSMS_USER` | Login name (SQL auth) |
| `--password-env <VAR>` | | | Name of the env variable that holds the password |
| `--password-stdin` | | | Read the password from stdin |
| `--encrypt <mandatory\|optional\|strict>` | | `GOSSMS_ENCRYPT` | Connection encryption |
| `--trust-server-certificate` | | | Skip certificate validation (dev only) |
| `--application-intent <readwrite\|readonly>` | | | Route to readable secondaries |
| `--connect-timeout <sec>` | | | Default 15 |

> ⚠️ **Never accept a plain `--password <value>`.** Command lines show up in process lists, shell history and agent logs. Agents should use `--profile`, so credentials are resolved from the gossms config the same way the TUI does today. For ad-hoc connections without a profile, use `--password-env`, `--password-stdin` or integrated/Entra authentication.

The options `--server`, `--database`, `--auth` and `--user` are for ad-hoc connections. Combined with `--profile`, they override the profile's values for that one call.

### 3.2 Execution and safety

| Option | Description |
|--------|-------------|
| `--allow <read\|write\|ddl\|admin>` | Highest permitted operation class (default: `read`). See §6 |
| `--dry-run` | Generate and print the T-SQL but do not run it |
| `--confirm <token>` | Required for destructive operations. The token must equal the target object name (see §6.3) |
| `--transaction <auto\|commit\|rollback>` | `rollback` runs the statement in a transaction and then rolls it back. Useful for testing DML |
| `--timeout <sec>` | Command timeout (default 60, `0` = unlimited) |
| `--var <name=value>` | sqlcmd-style scripting variable `$(name)`. Repeatable |

### 3.3 Output

| Option | Alias | Description |
|--------|-------|-------------|
| `--output <text\|json\|jsonl\|csv\|tsv\|markdown>` | `-o` | Output format (default: `text`) |
| `--max-rows <n>` | | Rows per result set (default **200**, `0` = unlimited) |
| `--max-col-width <n>` | | Truncate cell text (default **256** characters) |
| `--max-output-bytes <n>` | | Hard cap on total stdout (default 1 MB) |
| `--out-file <path>` | | Write the full result to a file. stdout gets only a summary and the path |
| `--null-text <str>` | | How NULL is shown (default `NULL`) |
| `--no-headers` | | Leave out column headers (csv/tsv/text) |
| `--include-messages` / `--no-messages` | | Include PRINT / info messages (default: include) |
| `--quiet` | `-q` | Results only. No envelope header |
| `--verbose` | `-v` | Diagnostic logging to stderr |
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
├── compare        Schema compare (optional)
└── help / version
```

### 4.1 `profile`: connection profiles

| Command | Description |
|---------|-------------|
Connection profiles already exist in the gossms config file and are shared with the TUI. The CLI **reads** them. Profiles are still created and edited the way they are today.

| Command | Description |
|---------|-------------|
| `profile list` | List the profiles from the config file: name, server, database, auth mode (secrets are never printed) |
| `profile show <name>` | Show one profile's details, including its `maxAllow` ceiling (§6.2) |
| `profile test <name>` | Connect and report version, edition and latency |

```bash
gossms profile list
gossms profile test dev
```

### 4.2 `server`: instance level

| Command | Description |
|---------|-------------|
| `server info` | Version, edition, collation, HADR, uptime, logical CPUs, NUMA nodes, physical memory |
| `server config list [--name <pattern>] [--non-default] [--advanced]` | `sp_configure` values: configured, running, default, min/max, dynamic, advanced. `--non-default` shows only settings that differ from the default |
| `server config get <name\|alias> [<name\|alias> …]` | One or more settings, plus related context (e.g. CPUs and NUMA nodes for MAXDOP, physical memory for memory settings) |
| `server config set <name\|alias> <value> [--force]` | Change a setting (`--allow admin`). Runs `sp_configure` + `RECONFIGURE`, turns `show advanced options` on when needed and restores its previous value afterwards, and reports before/after and whether a restart is required |
| `server config reset <name\|alias>` | Set a setting back to its default (`--allow admin`) |
| `server memory` | Memory overview: physical and available memory, min/max server memory, committed and target memory, page life expectancy, pending memory grants, top memory clerks |
| `server traceflag list` / `on <flag>` / `off <flag>` `[--global]` | Trace flags (`DBCC TRACESTATUS/TRACEON/TRACEOFF`). `on`/`off` need `--allow admin` |
| `server log [--lines 100] [--search <text>] [--since <iso>]` | SQL Server error log |

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
- Obviously risky values are blocked (exit 7) unless `--force` is given. Examples: `max-memory` above physical memory, or `max-memory` below `min-memory`.
- `--dry-run` prints the exact T-SQL and the planned change without running anything.

### 4.3 `db`: databases

| Command | Description |
|---------|-------------|
| `db list [--include-system] [--state online\|offline\|…]` | Databases with state, size, recovery model, compatibility level |
| `db info <name>` | Properties (Database Properties dialog) |
| `db files <name\|--all> [--type data\|log] [--filegroup <name>] [--unit KB\|MB\|GB] [--sort-by name\|size\|used\|free-pct] [--max-free-pct <n>]` | Per file: logical name, type, filegroup, physical path, size, used, free, free %, autogrowth, max size, and free space on the volume that holds the file. `--max-free-pct 10` shows only files with at most 10 % free space |
| `db space <name\|--all> [--unit …]` | Database-level usage (like `sp_spaceused`): total size, data and log size, reserved, data, index, unused and unallocated space |
| `db log-usage [<name>\|--all] [--unit …]` | Transaction log size, used %, `log_reuse_wait_desc` (why the log can't be truncated) and the number of VLFs |
| `db table-sizes <name> [--top 20] [--schema …] [--sort-by reserved\|rows\|data\|index\|unused] [--unit …]` | Largest tables: rows, reserved, data, index and unused space (SSMS report "Disk Usage by Top Tables") |
| `server volumes [--unit …]` | Disks and mount points that hold database files: total, free, free %, and the number of files per volume |
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
| `object search <text> [--in name\|definition\|column]` | Find objects or columns by name or by text in their code |
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
| `script db <name> [--objects …] [--include-data] [--include-permissions] [--out-file …]` | Generate Scripts wizard |

Script options: `--include-indexes`, `--include-triggers`, `--include-fk`, `--if-not-exists`, `--target-version 2019|2022|azure`.

### 4.6 `query`: query editor

| Command | Description |
|---------|-------------|
| `query run` | Run T-SQL and return result sets and messages |
| `query parse` | Syntax check only (`SET PARSEONLY ON`). Nothing is executed |
| `query classify` | Report the operation class (`read/write/ddl/admin`) the safety model assigns to the batch |

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
| `data export <schema.table\|--sql …> --out-file x.csv [--format csv\|json\|parquet]` | Export |
| `data import <schema.table> --in-file x.csv [--format csv\|json] [--truncate-first]` | Import (`--allow write`) |

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

A short header block in `KEY: value` form, then the result sets as aligned tables, then messages. Section markers are fixed so an agent can split the output reliably.

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
- `csv` / `tsv`: RFC 4180. Result sets are separated by a blank line. Only the first result set is written unless `--all-result-sets` is given.
- `markdown`: GitHub tables, handy when the agent shows the result to a human.

### 5.3 Value formatting rules (all formats)

| SQL type | Representation |
|----------|----------------|
| NULL | `NULL` in text, `null` in JSON (never an empty string) |
| date / datetime2 / datetimeoffset | ISO 8601 (`2026-10-28T13:16:23.123+01:00`) |
| decimal / money | Invariant culture, `.` as decimal separator, no thousands separators |
| binary / varbinary | `0x` + hex, truncated to `--max-col-width` |
| xml / large text | Truncated with a `…[+12,345 chars]` marker |
| bit | `true` / `false` in JSON, `1` / `0` in text |
| uniqueidentifier | Lowercase canonical form |

Formatting never depends on the locale, so output is the same on every machine.

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

Classify T-SQL with a proper tokenizer, not a naive regex. It must skip comments, string literals and `[bracketed]` identifiers, split statements and `GO` batches, and classify each statement by its leading keywords. **It fails closed:** anything it does not recognize, including dynamic SQL (`EXEC(@sql)`, `sp_executesql`) and unknown procedure calls, counts as `write` unless a profile allow-list says otherwise.

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

A profile can set a maximum class that no command-line flag can raise. This is a new optional `maxAllow` field on the existing profile entries in the gossms config file. If it is missing, the ceiling is `admin` (only `--allow` applies). For example, a `prod-ro` profile has `maxAllow: read`. This gives a hard guardrail even if the agent passes `--allow admin`.

### 6.3 Destructive confirmation

Drop database, restore with `--replace`, kill and similar operations need `--confirm <exact target name>`. The agent has to state the target deliberately, which catches wrong-object mistakes.

### 6.4 Additional safeguards

- `--dry-run` on every command that changes something. It prints the exact T-SQL that would run.
- `--transaction rollback` to measure the effect of DML (`rowsAffected`) without keeping the change.
- Optional audit log (`audit.jsonl` in the per-user state directory: `%LOCALAPPDATA%\gossms\` on Windows, `~/Library/Application Support/gossms/` on macOS, `$XDG_STATE_HOME/gossms/` (default `~/.local/state/gossms/`) on Linux). Fields: timestamp, profile, command, class, SQL hash, outcome.
- `--max-rows-affected <n>`: abort and roll back if a write affects more rows than expected.

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
EXEC sys.sp_configure N'show advanced options', 1; RECONFIGURE;
EXEC sys.sp_configure N'max degree of parallelism', 8; RECONFIGURE;
EXEC sys.sp_configure N'show advanced options', 0; RECONFIGURE;

=== PLANNED CHANGE ===
name                      | current | new | dynamic
--------------------------+---------+-----+--------
max degree of parallelism | 0       | 8   | yes
```

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
EXEC sys.sp_configure N'max server memory (MB)', 57344; RECONFIGURE;

=== PLANNED CHANGE ===
name                   | current    | new   | dynamic
-----------------------+------------+-------+--------
max server memory (MB) | 2147483647 | 57344 | yes

=== WARNINGS ===
New value is below currently committed memory (61210 MB). SQL Server will release memory after RECONFIGURE.
```

There is no `show advanced options` step here because it is already on on this server. The T-SQL always reflects the server's real state.

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
- [ ] `gossms` with no parameters always starts the TUI, as today. No CLI command ever starts it.
- [ ] The CLI uses the same profiles from the gossms config file as the TUI, and never changes the config file.
- [ ] `gossms` with options but no command (e.g. `gossms -p dev`) exits with `2` and points to `agent guide`.
- [ ] Bare `gossms` with piped stdin/stdout exits within a second with code `1` and a clear error. It never hangs.
- [ ] `agent guide`, `agent guide --brief` and `agent schema` list every non-hidden command, and every example in them passes the CLI parser.
- [ ] `agent context -p <profile>` reports missing permissions correctly (tested with a read-only login).
- [ ] `--output json` produces valid JSON with the envelope from §5.2 for **every** command, including errors.
- [ ] Exit codes match §7, and each code is covered by at least one automated test.
- [ ] A `write`/`ddl`/`admin` batch with no matching `--allow` is blocked (exit 7) and nothing is executed.
- [ ] `--dry-run` never opens a write transaction.
- [ ] `db files`, `db space` and `db log-usage` sizes match `sys.database_files`, `sp_spaceused` and `sys.dm_db_log_space_usage` for the same database, and column names always carry the unit.
- [ ] `server config set` restores `show advanced options` to its previous value, converts memory units correctly, reports `restart_required`, and blocks risky values without `--force`.
- [ ] Output is identical on Linux, macOS and Windows, and regardless of OS locale (tested with `de-CH`, `en-US`, `fr-FR`).
- [ ] Truncation is always reported (`truncated: yes/true` plus a hint).
- [ ] No secret ever appears in output, logs, `profile show` or the audit log.
- [ ] `help --output json` lists every command and option. A test fails if a command is missing from the catalog.

---

## Open decisions

1. **Default output format for agents:** `text` (shorter, uses fewer tokens) or `json` (stricter to parse). Recommendation: default `text`, and tell agents to use `-o json` when they need to parse results programmatically.
2. **Default `--max-rows`** (200 is proposed). Balance this against your agents' context-window size.
3. **Production profiles:** whether `maxAllow` ceilings should be enforced centrally (e.g. a policy file managed by IT) rather than per user.
