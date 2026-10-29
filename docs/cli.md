# Command line

The command-line contract: how gossms is invoked without its interface, how it
parses arguments, what it writes and how it exits. Agents and scripts depend on
every rule here, so a change to one is a change to this file in the same
commit.

**This document grows per phase.** It states only what is implemented; the
command tree, output formats and safety model arrive with the phases that build
them. Today (through phase 4) the commands are `version`, `help`, `agent
guide/schema/context`, `profile list/show/test`, `db
list/info/files/space/log-usage/table-sizes`, `server info/memory/volumes/config list/config get`, `object
list/search/describe/definition/dependencies/indexes/stats` and `query
classify/run/parse`; the formats `text`, `json`, `jsonl`, `csv`, `tsv` and
`markdown`. `profile test`, `agent context`, every `db`, `server` and `object` command,
`query run` and `query parse` connect to a server, and so does `agent guide`
given a profile.

Code: `cmd/gossms/main.go` (mode switch), `internal/cli` (`Main`, help),
`internal/cli/spec` (the command tree as data), `internal/cli/args` (the
parser), `internal/cli/output` (envelope, renderers, exit codes),
`internal/cli/connect` (connection options and resolution),
`internal/cli/safety` (the T-SQL classifier and its check),
`internal/cli/cmd` (the handlers), `internal/cli/agent` (`agent guide` and
`agent schema`, built from the tree). The command tree itself is
`internal/cli/tree.go`.

## Modes

| Invocation | Behaviour |
|---|---|
| `gossms` (no argument at all) | Starts the interactive UI — but only with **both stdin and stdout on a terminal**. Otherwise it exits `1` at once, with one stderr line pointing at `gossms agent guide` |
| `gossms --version` / `-version` / `-v` | The version block, byte-identical to every earlier release (`release.yml`'s Homebrew test matches it). Extra arguments are ignored |
| `gossms <anything else>` | The command line. The interactive UI is never started, the TUI's log file is never opened, and the terminal is never put in raw mode |

The rule: **no argument means the UI, any argument means the command line**;
the version flags are the one exception, kept for compatibility.

Without the terminal check, bare gossms with piped stdio inside a terminal
session would open `/dev/tty` (`CONIN$` on Windows) and wait for keys no one
sends. On Windows, `term.IsTerminal` is console-mode based: under mintty
without ConPTY the stdio are pipes and the UI is refused, which is also the
case where tcell could not have read keys.

## Syntax

```text
gossms <command> [arguments] [options]
gossms <group> <command> [arguments] [options]
gossms <group> <sub-group> <command> [arguments] [options]
gossms help [<group>] [<command>...]
gossms <command> --help
```

- The command is the deepest path the positional words spell. Global options
  may precede it (`gossms -o json version`).
- A group alone (`gossms <group>`) is a usage error whose hint lists its
  commands.
- Only options and no command (`gossms -p dev`, `gossms --quiet`) is a usage
  error — *no command given* — listing the commands. This holds whatever
  options later phases add.

### Options

- `--name value` and `--name=value` are the same; a short alias is `-x value`
  or `-x=value`.
- A bool is `--name`, or `--no-name` for false; it takes no value
  (`--quiet=yes` is an error).
- A list option may repeat and/or take comma-separated items; one with a fixed
  vocabulary (`object list --type`) refuses an item outside it.
- A non-list option given twice is an error — there is no "last one wins".
- `-` is a value or a positional argument (stdin where a file is expected),
  never an option. A value may start with `-` but not `--`; spell such a value
  `--name=--value`.
- `--` ends the options: every later token is positional.
- An option absent from the command line takes its environment variable (where
  one is defined), else its default.

### Response files

`@path` (relative to the working directory) is replaced by the file's lines,
which avoids shell quoting:

- A line starting with `-` is one option: split at its first whitespace into
  option and value, unless written `--name=value`.
- Any other line is one token, so command words go on separate lines.
- Blank lines are skipped; a BOM and CRLF line ends are tolerated.
- No recursion: `@x` inside a response file is a literal token. So is `@x`
  after `--`, and `@x` as the value of an option that takes one.

```text
version
-o json
```

### Errors

Everything the parser refuses is `USAGE_ERROR`, exit `2`: unknown command or
option, missing or extra arguments, a missing value, a value the type rejects
(not an integer, not in the enum), a repeated option, an unreadable response
file. **Unknown options are never ignored.** Where a command or option name is
close (edit distance ≤ 2), the hint is a did-you-mean: `Did you mean
\`gossms version\`?`.

## Global options

| Option | Alias | |
|---|---|---|
| `--output <text\|json\|jsonl\|csv\|tsv\|markdown>` | `-o` | Output format (default `text`). The list grows only as renderers are implemented — a format is never advertised before it works |
| `--quiet` | `-q` | Text and markdown: leave out the header block. A JSON envelope is always whole |
| `--verbose` | | Diagnostic logging to stderr. No short alias: `-v` is `--version` |
| `--no-color` | | Accepted; nothing is coloured yet |
| `--help` | `-h` | The command's help (text) |
| `--max-rows <n>` | | Rows kept per result set on stdout (default 200; `0` keeps all) |
| `--max-col-width <n>` | | Characters kept per cell (default 256; `0` keeps all) |
| `--max-output-bytes <n>` | | Cap on stdout (default 1048576; `0` is no cap) |
| `--null-text <text>` | | How text and markdown show NULL (default `NULL`) |
| `--no-headers` | | Leave out column headers (text, csv, tsv) |
| `--no-messages` | | Leave out PRINT and informational messages |
| `--out-file <path>` | | Write every row to a file; stdout gets the summary |
| `--force` | | Replace an existing `--out-file` |

The output options are global: every command's answer goes through the same
limits, so `profile list --max-rows 5` is cut like a query's result
(§ Limits). `--headers` and `--messages` are the positive spellings; there is
no `--include-messages`.

## Help

`gossms --help`, `gossms help`, `gossms help <group> [<command>...]` (a
sub-group's command is `gossms help server config get`) and
`<command> --help` print plain text for people, outside the envelope, exit `0`.
All of it is generated from the command tree: usage line, description,
arguments, options, global options, examples.

The first line of the top-level help is the pointer for AI agents: *AI
agents: run `gossms agent guide` for complete usage instructions.* The
no-command usage error (`gossms -p dev`) and bare gossms's no-terminal
refusal name it too. `TestCommandMentionsParse` fails on any `gossms …` named
in a hint, help text, example or message that the tree cannot parse, so a
pointer can never name a command that does not exist.

Help with `--output json` is the command schema of the same topic — exactly
what `gossms agent schema [<group> [<command>]]` prints (§ Agent schema).
Help in any other format is refused (`USAGE_ERROR`).

### Agent guide

`gossms agent guide` is the manual an AI agent reads before its first call,
printed outside the envelope, exit `0`. Sections, in this fixed order (each id
is also a `--topic`):

| Id | Section | Source |
|---|---|---|
| `overview` | What gossms is | `internal/cli/agent/guide/overview.md` |
| `rules` | Rules you must follow | `guide/rules.md` |
| `calling` | How to call it | `guide/calling.md` |
| `output` | Output contract | `guide/output.md` |
| `exit-codes` | Exit codes | generated from the exit-code table |
| `commands` | The command index: command, operation class, summary. In `--brief`, one row per group naming its commands (a sub-group's as `config list`), a non-`read` one marked with its class, and the group's summary — so the brief guide stays under its cap as commands are added | generated from the command tree |
| `recipes` | Explore a schema, read data, make a safe data fix | `guide/recipes.md` |

The first lines are the title and `gossms <version> · CLI schema <n>`, so a
cached guide can be told stale.

| Option | |
|---|---|
| `--brief` | The condensed guide for a system prompt (under 6,000 bytes, about 1,500 tokens): no recipes, each hand-written section up to its `<!-- full -->` line, the exit codes as one line |
| `--topic <id\|group>` | One section; or a command group (`db`, `object`, `query`, …): each of its commands, a sub-group's included, with description, class, usage, arguments, its own options and examples. The connection options are named once at the top |
| `-o markdown` | The default for this command (whatever `--output`'s global default is) |
| `-o text` | The same content as plain lines: headings underlined, tables aligned, code indented, no markup |
| `-o json` | `{"schemaVersion", "gossmsVersion", "title", "brief", "topic", "sections": [{"id", "title", "body"}]}`, `body` in markdown, `topic` null for the whole guide. Not the envelope: its `data` holds only strings |

`jsonl`, `csv` and `tsv` are refused (`USAGE_ERROR`).

**Your environment.** Given a profile or `--server` — on the command line or
through `GOSSMS_PROFILE` — the guide takes every connection option, connects,
and adds a last section, id `environment`, titled `Your environment (profile:
<name>)`: `agent context`'s answer for the same connection, its record as a
list and each set as a table (in `--brief`, each table's first 15 rows and a
count of the rest). It is added after a `--topic`'s section too. When the
context fails the guide still prints, the section names the error code,
message and hint, the summary line goes to stderr and the exit code is the
error's — an agent never takes a guide without its environment for one with
it.

Only the rules and the recipes are prose that can go stale, and
`TestAgentGuideMentionsParse` renders every topic, brief and full, and fails
on any backticked `gossms …` or code-block line starting `gossms ` that names
a missing command or does not parse — so a recipe can name only implemented
commands.

### Agent schema

`gossms agent schema` prints the machine-readable catalog an agent builds tool
definitions from: JSON, outside the envelope, exit `0`. It is JSON for `-o
text` (the default) and `-o json`; any other format is `USAGE_ERROR`. The
output options do not apply. A group, or a group and a command, narrows
`groups` and `commands` to what is under it; an unknown one is `USAGE_ERROR`
with a did-you-mean.

| Key | |
|---|---|
| `schemaVersion` | The envelope's version (`version`'s `cliSchema`) |
| `gossmsVersion` | The build's version, so a cached catalog can be told stale |
| `formats` | The `--output` values |
| `exitCodes[]` | `exit`, `code`, `meaning` — the whole § Exit codes table |
| `globals[]` | The global options, shaped as a command's `options[]` |
| `groups[]` | `name`, `summary` of each command group |
| `commands[]` | `path` (`"object list"`), `summary`, `description` (null when none), `operationClass` (`read\|write\|ddl\|admin`), `arguments[]` {`name`, `help`, `format`, `required`, `repeat`}, `options[]`, `examples[]` |

An option is `name` (`"--max-rows"`), `short` (`"-o"` or null), `type`
(`bool\|string\|int\|enum\|list\|repeat`), `enum` (`[]` when none; on a
list, each item's values), `default` typed by `type` (a bool, a number, a
string, an array for a list; null when none), `negation` (a bool's
`--no-<name>`, null otherwise), `env` (null when none) and `help`. Every key
is always present. Hidden commands are left out.

All of it is generated from the command tree and the exit-code table; nothing
is hand-written. `TestAgentSchemaListsEveryCommand` fails on a command missing
from it, and `internal/cli/testdata/agent_schema.json.golden` pins the whole
document (`go test ./internal/cli -run AgentSchemaGolden -update` once a
change is meant).

## Profiles

A profile is a saved connection the user has named in the TUI: the Connect
dialog's **Profile...** button, on the History row selected, sets

- **Profile Name** — letters, digits, `-`, `_`, `.`, starting with a letter or
  digit (a leading `-` would read as an option), at most 64 characters, unique
  ignoring case. Clearing it makes the entry a plain recent connection again.
- **Allowed Operations** — the ceiling (`max_allow`): `read`, `write`, `ddl`
  or `admin` (the default, and what a missing field means). The safety model
  that enforces it arrives with `query run`; a value this build does not know
  reads as `read`.

Both are stored on the connection in `config.json` (`profile`, `max_allow`),
outside the data a saved password is sealed to, so naming a connection never
invalidates its password. A named connection is exempt from the 30-entry
recent-connections trim, and reconnecting through the Connect dialog keeps its
name and ceiling.

A command takes the profile **name**, ignoring case; the generated name of an
unnamed recent connection (`server,port,database,user`, as `profile list --all`
shows it) is accepted too, so a connection can be used before it is named.

**The command line only reads the configuration** (`config.LoadReadOnly`): it
never writes `config.json`, never creates `gossms.key`, never sweeps temp
files, never re-seals a password, and never opens `gossms.log`. A running TUI
writes `config.json` atomically, so a command sees the old file or the new one.
Without the key file every saved password stays sealed (`password:
unreadable`). A `config.json` that exists but cannot be read or parsed is
`GENERAL_ERROR`, not an empty list.

## Connecting

A command that connects takes these options (`profile test`, `agent
context`, `agent guide`, the `db`, `server` and `object` commands, `query run`,
`query parse`). The precedence is **command line > environment variable > profile >
default**.

Every one but `profile test` names the profile with `--profile <name>`
(`-p`, env `GOSSMS_PROFILE`); `profile test` takes it positionally. Without one the
call is ad hoc: `--server` is required and there is no ceiling beyond
`--allow`.

| Option | Alias | Env variable | |
|---|---|---|---|
| `--server <host[\instance][,port]>` | `-S` | `GOSSMS_SERVER` | The Connect dialog's syntax |
| `--port <n>` | | | When `--server` names none; naming two different ports is a usage error |
| `--database <name>` | `-d` | `GOSSMS_DATABASE` | Initial database |
| `--auth <method>` | | `GOSSMS_AUTH` | `sql`, `windows`, `entra-default`, `entra-password`, `entra-msi`, `entra-sp`, `entra-azcli`; `entra-interactive` and `entra-device-code` are refused |
| `--user <name>` | `-U` | `GOSSMS_USER` | Login (sql, windows, entra-password) |
| `--tenant-id <id>` | | `GOSSMS_TENANT_ID` | Entra tenant |
| `--client-id <id>` | | `GOSSMS_CLIENT_ID` | Entra client: service principal, user-assigned managed identity |
| `--password-env <VAR>` | | | Name of the variable holding the password or client secret |
| `--password-stdin` | | | First line of stdin is the password or client secret |
| `--encrypt <optional\|mandatory\|strict>` | | `GOSSMS_ENCRYPT` | Connection encryption |
| `--trust-server-certificate` | | | Skip certificate validation (development only) |
| `--host-name-in-certificate <name>` | | | Name the certificate must carry |
| `--application-intent <readwrite\|readonly>` | | | Written into the connection's extra properties |
| `--connect-timeout <sec>` | | | 1 to 30, default 15 |

They are options of the commands that connect, not globals: `gossms version
--server x` is an unknown option.

- **No password on the command line, ever** — it would reach process lists,
  shell history and agent logs. A profile's saved password (Remember
  Password) is used as the TUI uses it; otherwise `--password-env` or
  `--password-stdin` (not both). SQL Server Authentication, Entra Password and
  Entra Service Principal with no password are refused before dialling
  (`AUTH_FAILED`, a hint naming both options). Windows Authentication without
  one is integrated sign-in.
- **A saved password goes only where it was saved for.** An override of the
  server (ignoring case), port, user, authentication method, Entra tenant or
  client — from an option or its environment variable — or one that weakens
  TLS (`--encrypt` lower than saved, `--trust-server-certificate` turned on,
  another `--host-name-in-certificate`) leaves the saved password out. The
  database, a stronger `--encrypt`, `--no-trust-server-certificate` and
  `--application-intent` keep it.
- A field the method does not read is not sent, as in the Connect dialog
  (`--tenant-id` with `entra-msi` is dropped).
- **Entra MFA and Device Code are refused** (`AUTH_FAILED`): they prompt a
  person, and the token cache lives one process, so every call would prompt
  again.
- The session's `program_name` is `goSSMS - CLI`.

A failed connect maps to an exit code by error type and number, never by
message text: the server refusing the login (Msg 18456 and its relatives) or
Entra refusing the credential is `AUTH_FAILED`; a database the login cannot
open (Msg 4060, or 4063 when the server falls back to the login's default
database) `NOT_FOUND`; the attempt outliving `--connect-timeout` (or a
network timeout) `TIMEOUT`; anything else `CONNECTION_FAILED`.

## Output

Results go to **stdout** in the `--output` format; errors too, so the caller
parses one stream. On an error, stderr also gets one line,
`gossms: <CODE>: <message>`. Output is UTF-8 without a BOM, `\n` line ends on
every OS, independent of the locale. Library logging is discarded unless
`--verbose`, so nothing else appears on stderr.

### The envelope

Every command returns the same envelope: a header (status, command, server,
database, duration, rows affected, out file), then a record (`data`, for
answers such as `version`), result sets, or both (`query classify`: a summary
record and one row per statement), then messages, warnings and errors.

`status` is `ok`, `error`, `blocked` or `cancelled`. The first error decides
the exit code.

### `text`

`KEY: value` header lines, then sections separated by a blank line. Section
markers are fixed so a caller can split the output.

```text
STATUS: OK
COMMAND: version
DURATION_MS: 0

VERSION: v0.2.0
COMMIT: 72a0e3c
CLI_SCHEMA: 1.0
```

- `SERVER` and `DATABASE` appear only when set; `ROWS_AFFECTED` only when the
  server reported a count, `OUT_FILE` only with `--out-file`; `RESULT_SETS: n`
  only when there are sets.
- A record prints as `KEY: value` lines; the key is the JSON key in
  `UPPER_SNAKE` (`cliSchema` → `CLI_SCHEMA`). A null value prints `KEY:`, as
  an empty one does.
- A multi-line record value (`object definition`'s `definition`, the only
  one) is not a `KEY:` line: it follows the record as its own section,
  `=== DEFINITION (n lines) ===` and then exactly n lines, line breaks kept
  (`\r\n` printed as `\n`) and never cut by `--max-col-width`. A reader takes
  the n lines after the marker without scanning them — the text may hold a
  line that looks like a marker. Markdown fences it as a code block. A null
  one prints no section.
- A result set is `=== RESULT n (r rows, truncated: no) ===` (or `r of t rows,
  truncated: yes`); a command that returns several fixed sets names each,
  `=== RESULT 2: indexes (…) ===`. Then a header row, a `-+-` rule and the rows, aligned by
  display width; the last column is unpadded. NULL prints `--null-text`. A
  set cut by a limit is followed by its `NOTE:` line (§ Limits); a set sent to
  `--out-file` is its marker only, `(n rows, sent to --out-file)`.
- Then `=== MESSAGES ===` and `=== WARNINGS ===` when non-empty, and each error
  as an `ERROR_CODE:` / `MESSAGE:` / `HINT:` block; a SQL Server error adds
  `SQL_ERROR_NUMBER:`, `SEVERITY:`, `STATE:`, `LINE:` and (inside a module)
  `PROCEDURE:` after `ERROR_CODE:`.
- Every value is one line: line breaks and tabs become spaces, so no value can
  forge a marker. An empty value prints `KEY:` — no line ends in a space.

### `json`

```json
{
  "schemaVersion": "1.0",
  "status": "ok",
  "command": "version",
  "server": null,
  "database": null,
  "durationMs": 0,
  "rowsAffected": null,
  "outFile": null,
  "data": {
    "version": "v0.2.0",
    "commit": "72a0e3c",
    "cliSchema": "1.0"
  },
  "resultSets": [],
  "messages": [],
  "warnings": [],
  "errors": []
}
```

- Keys always appear in this order, and every key is always present (`null` or
  `[]`) — except `data`, which appears only for a record answer, its fields in
  the command's fixed order. A `data` value is a string, or `null` where the
  command says so (`object definition` of an encrypted module).
- `rowsAffected` is the sum of the DML row counts the server reported, `null`
  when it reported none; `outFile` the `--out-file` written, else `null`.
- An error is `{"code", "message", "hint"}`; a message `{"type", "text"}`; a
  warning a string. An error with a SQL Server error behind it is `{"code",
  "sqlErrorNumber", "severity", "state", "line", "procedure", "message",
  "hint"}` (`procedure` `null` outside a module).
- A result set is `{"name", "columns": [{"name", "type", "nullable"}], "rows",
  "rowCount", "totalRowCount", "truncated", "truncatedBy", "rowsAffected"}`.
  `name` says what a command's fixed set holds (`object describe`'s
  `columns`, `keys`, …); `null` for a script's sets.
  `rowCount` is the rows in this output, `totalRowCount` the rows the server
  returned; `truncatedBy` is `max-rows` or `max-output-bytes` (the last limit
  that removed rows), `null` when whole. With `--out-file` the rows are in the
  file and `rows` is `[]`.
- A cell is typed by its column's SQL type: `int`, `bigint`, `smallint`,
  `tinyint`, `real`, `float` a number; `bit` `true`/`false`; NULL `null`;
  everything else — `decimal`, `numeric` and `money` included, which most
  parsers would round as a float — the grid's text as a string.
- Two-space indent, no HTML escaping, one trailing newline.
- `schemaVersion` (also `gossms version`'s `cliSchema`) changes only when the
  structure breaks compatibility.

A usage error is rendered in the format the raw command line asks for
(`-o json`), although parsing failed.

### `jsonl`

One JSON object per line, written as the rows arrive:

```text
{"_resultSet":{"index":1,"name":null,"columns":[{"name":"id","key":"id","type":"int","nullable":false}]}}
{"id":1}
{"id":2}
{"_summary":{"schemaVersion":"1.0","status":"ok",…,"resultSets":[{"name":null,"columns":[…],"rowCount":2,"totalRowCount":2,"truncated":false,"truncatedBy":null,"rowsAffected":null}],…}}
```

- Each set starts with a `_resultSet` line; each row is an object keyed by
  column, its values typed as in `json`. A key is the column name; a nameless
  column is `column<n>`, and a duplicate (or a name that would read as
  `_resultSet`/`_summary`) gets `_<n>` appended, `n` its 1-based position —
  the `_resultSet` line maps every name to its key.
- The last line is `_summary`: the `json` envelope with each set's rows left
  out. It is always written, also on an error or a cancel.

### `csv` and `tsv`

Rows only on stdout, written by the TUI's Results To File writer: RFC 4180,
NULL an empty field, the empty string `""` (`docs/decisions.md` § SQL NULL in
results), every set (header row, then rows) separated by a blank line. TSV is
the same with a tab, quoting a field that holds a tab, quote or line break.
Cells are never clipped. Everything else — the header block, each set's
marker and truncation note, messages, warnings, errors — goes to **stderr** as
the text format's summary, on success too; the exit code carries the status.

### `markdown`

The text envelope with each set as a GitHub table, after a blank line: `|`
and `\` escaped, line breaks as `<br>`. `--no-headers` does not apply (a
GitHub table needs its header row).

### Limits

Rows past a limit are dropped from the output, never from the run: the whole
script runs, and `totalRowCount` stays exact.

- **`--max-rows n`** keeps each set's first `n` rows on stdout and counts the
  rest. The rows kept are held in memory until the command ends, so `0` on a
  large result holds every row; `--out-file` and `-o jsonl` keep none.
  `NOTE: Output truncated by --max-rows 200. Use --max-rows 0 or --out-file to
  get all rows.`
- **`--max-col-width n`** cuts a cell to `n` characters (Unicode code points)
  and marks the rest: `…[+12,345 chars]`. Text, markdown, json and jsonl only;
  csv, tsv and `--out-file` are lossless. A typed number or bit is never cut.
- **`--max-output-bytes n`** caps stdout. Text, json, markdown, csv and tsv
  drop whole trailing rows — the last set's first — until the document fits,
  so JSON is never cut inside a value; `NOTE: Output truncated by
  --max-output-bytes …`. If even no rows fit (a huge message), the rowless
  document is written anyway, with a warning. `jsonl` stops writing rows at
  the cap; its `_summary` line is exempt.

### `--out-file`

`--out-file <path>` writes every row — no row, column-width or byte limit — to
the file, in the format its extension names: `.csv`, `.tsv`, `.jsonl` (rows,
then the summary line), `.json`, `.md`, `.txt` (the whole envelope); anything
else is a usage error. `--null-text`, `--no-headers` and `--no-messages` apply
as on stdout. stdout gets the summary in the `-o` format, its sets with no rows
and `outFile` naming the path.

- It is checked before the command runs: an unknown extension, a directory, or
  an existing file without `--force` is `USAGE_ERROR` (never a prompt).
- The rows go to a temporary file beside the target, renamed over it only when
  the command succeeds. On an error or a cancel the destination is left as it
  was, and a warning says so.
- A `.json`, `.md` or `.txt` file is rendered at the end, so its rows are held
  in memory; prefer `.csv`, `.tsv` or `.jsonl` for large results.

## Exit codes

| Code | Name | Meaning |
|---|---|---|
| 0 | `OK` | Success (output may still be truncated; check `truncated`) |
| 1 | `GENERAL_ERROR` | Unexpected or internal error (a recovered panic: stack on stderr under `--verbose`) |
| 2 | `USAGE_ERROR` | Invalid or unknown arguments |
| 3 | `CONNECTION_FAILED` | Server unreachable, TLS or network error |
| 4 | `AUTH_FAILED` | Login failed or credentials are missing |
| 5 | `SQL_ERROR` | The server returned an error (severity 11 or higher) |
| 6 | `TIMEOUT` | Command or connect timeout |
| 7 | `BLOCKED` | Refused by the safety model or a missing `--confirm` |
| 8 | `NOT_FOUND` | Profile, database or object does not exist |
| 9 | `PERMISSION_DENIED` | The login lacks the required permission |
| 10 | `PARTIAL` | Some batches succeeded and some failed |
| 11 | `UNSUPPORTED` | The server's version or edition does not support the operation |
| 130 | `CANCELLED` | Ctrl+C or SIGINT |

All are defined now so a caller's handling never depends on the phase it met;
today's commands produce every one, 11 (`UNSUPPORTED`) only where a catalog
read refuses the server's version. The table is `output.ExitCodes`, the one
place a code's number is spelled.

**Ctrl+C** (also on Windows) cancels the running command's context: the result
is `CANCELLED`, exit `130`, whatever error the command itself returned. A
second Ctrl+C kills the process.

## Commands

| Command | Answer |
|---|---|
| `version` | Record: `version`, `commit`, `built`, `platform`, `license`, `gosmo` — the fields `gossms --version` prints — plus `cliSchema` |
| `help [<group>] [<command>...]` | Help text (§ Help) |
| `agent guide [--brief] [--topic <topic>] [-p <profile>] [connection options]` | The manual for AI agents, outside the envelope (§ Agent guide) |
| `agent schema [<group>] [<command>...]` | The command catalog as JSON, outside the envelope (§ Agent schema) |
| `agent context [-p <profile>] [connection options]` | Connects (§ Connecting). Header `DATABASE` is the connection's. Record: `profile` (null ad hoc), `serverName`, `productVersion`, `majorVersion`, `edition`, `login`, `isSysadmin` (null when the server permissions were not probed), `database`, `databaseRoles` (the fixed roles held there, comma-separated), `defaultAllow` (`read`), `maxAllow` (the ceiling `--allow` cannot pass; `admin` ad hoc), `maxAllowSource` (`profile`, `default` — the profile sets none —, or ad hoc). Named sets — `databases`: `name`, `state`, `accessible` (HAS_DBACCESS: false for a database not ONLINE, whoever asks; NULL when the server does not say), `is_system`; `missing_permissions`: `permission`, `scope` (`server`, or `database [name]`), `commands`, `effect` — each permission a command needs that the login is known to lack (an alternative granted counts: 2022's VIEW SERVER/DATABASE PERFORMANCE STATE, CREATE/ALTER ANY DATABASE for the files' visibility); one the server answers nothing for is left out; `profiles`: `name`, `server`, `database`, `max_allow`. A probe or read that fails is a warning, not an error |
| `profile list [--all]` | Set: `name`, `kind` (`profile`, or `recent` with `--all`), `server`, `port` (NULL when not set), `database`, `auth` (the `--auth` spelling), `user`, `max_allow` (NULL for a recent connection), `password` (`saved`, `none`, `unreadable`). Profiles sorted by name, then recent connections most recent first. An empty list carries a message saying how to make a profile |
| `profile show <name>` | Record: `name`, `kind`, `generatedName`, `server`, `port`, `database`, `auth`, `user`, `tenantId`, `clientId`, `encrypt`, `trustServerCertificate`, `hostNameInCertificate`, `extraProperties` (keys only — a value may be a secret), `maxAllow`, `password`. A password that cannot be opened adds a warning |
| `profile test <name> [connection options]` | Connects (§ Connecting). Header `SERVER`/`DATABASE` name the target, also on failure. Record: `profile`, `serverName`, `productVersion`, `productLevel`, `edition`, `platform`, `login`, `connectedDatabase` (the header's `DATABASE` is the one asked for), `auth`, `passwordSource` (`profile`, `env:<VAR>`, `stdin`, `none`), `maxAllow`, `latencyMs` (the whole connect). A dropped saved password adds a warning |
| `db list [-p <profile>] [connection options] [--include-system] [--state <state>]` | Set, by name: `name`, `database_id`, `state`, `recovery_model`, `compatibility_level`, `collation` (NULL for a database not ONLINE), `read_only`, `size_mb`, `data_mb`, `log_mb`, `created`, `is_system`, `snapshot_of` (the source's name; NULL for a database that is not a snapshot). System databases — the four built-ins and a replication distribution database — only with `--include-system`; snapshots are listed. Sizes are allocated file sizes from `sys.master_files`, so they answer in any state (tempdb's are its startup sizes); NULL where the login cannot see the files (VIEW ANY DEFINITION), and a failed size read is a warning, not an error. `--state` is `state_desc` lower-cased |
| `db info <name> [-p <profile>] [connection options]` | Header `DATABASE` is `<name>`. Record: `name`, `databaseId`, `state`, `recoveryModel`, `compatibilityLevel`, `collation`, `catalogCollation`, `readOnly`, `created`, `isSystem`, `isDistributor`, `snapshotOf`, `sizeMb`, `dataMb`, `logMb`, `dataUnallocatedMb`, `logAvailableMb`, then the `ALTER DATABASE SET` options: `owner`, `userAccess`, `pageVerify`, `containment`, `snapshotIsolation`, `readCommittedSnapshot`, `autoClose`, `autoShrink`, `autoCreateStats`, `autoUpdateStats`, `autoUpdateStatsAsync`, `parameterization`, `isTrustworthy`, `dbChaining`, `isBrokerEnabled`, `isEncrypted`, `delayedDurability`, `targetRecoveryTimeSeconds`, `acceleratedDatabaseRecovery`, `isLedger`. A part that could not be read is empty with a warning saying why — the space-used pair needs the database ONLINE and enterable; the rest answers from the server catalog in any state. An unknown database is `NOT_FOUND`, exit 8 |
| `db files <name>\|--all [--type data\|log\|filestream\|fulltext] [--filegroup <fg>] [--sort-by name\|size\|used\|free-pct] [--max-free-pct <n>] [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, database then `file_id` order: `database` (with `--all`), `logical_name`, `type`, `filegroup`, `physical_path`, `size_<u>`, `used_<u>`, `free_<u>`, `free_pct`, `growth` (`10 %`, `none`, or a size: KB under `--unit kb`, else MB), `max_size_<u>` (text: `unlimited`, or a size), `volume_free_pct`. Read inside the database (`sys.database_files`, `FILEPROPERTY`), so tempdb's sizes are current; one it cannot read inside is listed from `sys.master_files` (§ Storage). A FILESTREAM or full-text container's used, free, growth and max size are NULL. A snapshot lists its own sparse files. `--max-free-pct` (0–100) keeps files whose free % is known and at most it. `volume_free_pct` needs VIEW SERVER STATE (NULL with a warning without) |
| `db space <name>\|--all [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, one row per database: `database`, `total_<u>`, `data_size_<u>`, `data_used_<u>`, `unallocated_<u>`, `reserved_<u>`, `data_<u>`, `index_<u>`, `unused_<u>` — `sp_spaceused`'s figures and arithmetic, equal to its output in KB — then `log_size_<u>`, `log_used_<u>`, `log_used_pct` from `sys.dm_db_log_space_usage` (as `db log-usage` shows them; NULL with one warning without VIEW SERVER STATE). A snapshot shows its source's figures as of creation, log NULL, with a warning |
| `db log-usage <name>\|--all [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, one row per database: `database`, `log_size_<u>`, `log_used_<u>`, `log_used_pct` (`sys.dm_db_log_space_usage`'s), `log_reuse_wait_desc`, `vlf_count`, `vlf_active` (`sys.dm_db_log_info`, from 2016 SP2; NULL with a warning before). Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9. `--all` skips snapshots (no log); a named snapshot is `NOT_FOUND`, exit 8 |
| `db table-sizes <name> [--top <n>] [--schema <s>] [--sort-by reserved\|rows\|data\|index\|unused] [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, largest first (default `reserved`; schema, then name break ties): `schema`, `table`, `rows`, `reserved_<u>`, `data_<u>`, `index_<u>`, `unused_<u>` — each row equal to `EXEC sp_spaceused 'schema.table'` in KB, internal tables (XML, spatial, full-text indexes) included. `--top` default 20, 0 for every table (a cut, not a truncation). Needs no VIEW DATABASE STATE. A memory-optimized table's sizes are NULL with a warning |
| `server info [-p <profile>] [connection options]` | Record: `serverName`, `productVersion`, `majorVersion`, `edition` (as `agent context`), `productLevel`, `engineEdition`, `platform` (null when `@@VERSION` names none), `collation`, `isClustered`, `isHadrEnabled`, `startTime` (the server's local wall clock, no zone), `uptimeSeconds` (by the server's clock), `logicalCpus`, `numaNodes`, `physicalMemoryMb`, `defaultDataPath`, `defaultLogPath`, `defaultBackupPath`. `startTime` through `physicalMemoryMb` need VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE): without it they are null with one warning, exit 0 |
| `server memory [-p <profile>] [connection options]` | Named sets — `memory`, one row: `physical_mb`, `available_os_mb` (`sys.dm_os_sys_memory`), `max_server_memory_mb`, `min_server_memory_mb` (the running values), `committed_mb`, `target_mb` (Memory Manager's Total and Target Server Memory), `page_life_expectancy_s` (Buffer Manager's, instance-wide), `memory_grants_pending` (both null when the server does not publish the counter); `clerks`: `clerk`, `size_mb`, the 10 clerk types holding the most, largest first. Warnings: max server memory unlimited, available OS memory under 5 % of physical, memory grants pending. Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9 |
| `server volumes [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, by volume: `volume` (mount point, else OS label, else the lowest file path), `total_<u>`, `free_<u>`, `free_pct`, `db_files` — every volume holding a database file of any database (`sys.master_files` × `sys.dm_os_volume_stats`). Implausible figures (total 0, free over total) are left out with a warning. Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9 |
| `server config list [--name <text>] [--non-default] [--advanced] [-p <profile>] [connection options]` | Set, by name (case-insensitive): `name`, `alias` (the short name `config get` takes; NULL for none), `configured` (`value`), `running` (`value_in_use`), `default`, `min`, `max`, `dynamic`, `advanced` — every `sys.configurations` row, advanced ones whatever `show advanced options` is. `default` is the engine's default on the server's major (gosmo's table, from Microsoft's `sp_configure` reference), not what Setup or `mssql-conf` writes; NULL where gosmo has none (an option a CU added, Azure), with one warning naming them (the engine-written `version high/low part of SQL Server` aside). `--name` contains, case-insensitive, no wildcards; `--advanced` keeps advanced options; `--non-default` keeps those whose **configured** value differs from the default (min server memory configured 0 runs as 16 and is not listed), leaving out an option with no default |
| `server config get <name>... [-p <profile>] [connection options]` | `config list`'s set for each setting named, in the order given (a repeat once). A name is an alias — `maxdop`, `cost-threshold`, `max-memory`, `min-memory`, `adhoc`, `backup-compression`, `backup-checksum`, `remote-dac`, `clr`, `xp-cmdshell`, `contained-auth`, `blocked-threshold` — or a full `sys.configurations` name, both case-insensitive; anything else is `NOT_FOUND`, exit 8, the hint naming up to 3 names or aliases containing it. Record (context): `logicalCpus`, `numaNodes`, `logicalCpusPerNumaNode` (two decimals when uneven) for MAXDOP and cost threshold; `physicalMemoryMb`, `committedMemoryMb` (Total Server Memory) for max/min server memory; none for other settings. Without VIEW SERVER STATE they are null with one warning, exit 0 |
| `object list [-p <profile>] [connection options] [--type <kind>,…] [--schema <s>] [--name <text>] [--include-system]` | Set, by kind (in `--type`'s order: `table`, `view`, `procedure`, `function`, `trigger`, `synonym`, `sequence`, `type`, `schema`), then schema and name, case-insensitively: `schema` (NULL for a schema row), `name`, `type`, `is_system`. The objects of the connection's database (`-d`, or the profile's). `--schema` is equal and `--name` contains, both case-insensitive and without wildcards (Object Explorer's filter); a schema row matches either by its own name. A trigger is a table's or view's DML trigger; a type is an alias, table or CLR type. Without `--include-system`, what SQL Server ships is left out: system tables, the `sys` schema's views, procedures, functions and CLR types (geography, geometry, hierarchyid), and the system schemas (guest, INFORMATION_SCHEMA, sys, the fixed roles') |
| `object search <text> [-p <profile>] [connection options] [--in name\|definition\|column]` | Set: `schema`, `name`, `type`, and with `--in column` `column`, `data_type`. Case-insensitive, `%`/`_`/`[` literal. `name` (default): tables, views, procedures, functions and triggers whose name contains the text. `definition`: the modules whose T-SQL contains it — an encrypted or CLR module has none. `column`: the columns of tables and views whose name contains it. Empty text is a usage error |
| `object describe <object> [-p <profile>] [connection options]` | `<object>` is `schema.name`, a name alone when one schema has it (two or more: usage error listing them; none: `NOT_FOUND` with similar names), or `database.schema.name` for the connection's own database. Record: `schema`, `name`, `type` (the kind word, or the `type_desc` lower-cased for a kind without a folder: `primary_key_constraint`), `typeDesc`, `objectId`, `created`, `modified`, `parent` (a trigger's or constraint's table), then per kind — table `rowCount` (from partition metadata); view `isSchemaBound`; function `returns` (the type, or `TABLE`); trigger `isEnabled`, `events`; synonym `baseObject`; sequence `dataType`, `startValue`, `increment`, `minValue`, `maxValue`, `isCycling`, `cache`, `currentValue`. Named sets — table: `columns`, `keys`, `indexes`, `check_constraints`, `triggers`; view: `columns`, `indexes`, `triggers`; procedure or function: `parameters`. `columns`: `column_id`, `name`, `data_type`, `nullable`, `identity` (`IDENTITY(seed,increment)`; a view's column `IDENTITY`), `computed` (the expression, `PERSISTED` marked), `default`, `collation`, `primary_key`. `keys`: the primary key, unique constraints, then foreign keys — `name`, `type`, `columns`, `references` (`[schema].[table] (cols)`), `on_delete`, `on_update`, `disabled`, `not_trusted`. `indexes`: `name`, `type`, `unique`, `primary_key`, `key_columns` (`DESC` marked), `included_columns`, `filter`, `disabled`. `check_constraints`: `name`, `column` (NULL table-level), `definition`, `disabled`, `not_trusted`. `triggers`: `name`, `enabled`, `events`. `parameters`: `ordinal`, `name`, `data_type`, `is_output`, `has_default` (NULL for a T-SQL module: the catalog records defaults only for CLR ones). A computed, default or check expression the login cannot see (no VIEW DEFINITION) is NULL, counted in a warning; a user-defined type it holds no permission on reads as its base system type (`nvarchar(25)`, empty for a CLR type), counted in a warning — also in `parameters`, `returns`, a sequence's `dataType` and `object search --in column` |
| `object definition <object> [-p <profile>] [connection options]` | `<object>` as for `describe`. Record: `schema`, `name`, `type`, `definition` — the module's T-SQL as stored, multi-line (§ `text`). A view, procedure, function or DML trigger; any other kind is a usage error, exit 2. An encrypted module (or one the login lacks VIEW DEFINITION on) and a CLR module have `definition` null and a warning, exit 0. The stored text, not a generated script |
| `object dependencies <object> [--direction uses\|used-by\|both] [--depth n] [-p <profile>] [connection options]` | `<object>` as for `describe`. Record: `schema`, `name`, `type`, `direction` (default `both`), `depth` (default 1, 1–5; outside it a usage error). Set `dependencies`, one row per edge, breadth first, `uses` before `used-by`: `direction`, `level`, `via` (the object at the level before; the root at level 1), `schema`, `name`, `type`, `schema_bound`, `caller_dependent` (an unqualified name resolved as the owner would: its schema, then dbo). An object reached twice is followed once, so a cycle ends; a table's self-reference (its filtered index or statistic) is left out; another database's objects and missing ones are not listed. Needs SELECT on `sys.sql_expression_dependencies` (db_owner by default): without it `PERMISSION_DENIED`, exit 9 |
| `object indexes <object> [--fragmentation limited\|sampled\|detailed] [-p <profile>] [connection options]` | A table or view (any other kind: usage error, exit 2). Record: `schema`, `name`, `type`, `fragmentationMode` with `--fragmentation`. Set `indexes`, index_id order, a heap first (`name` NULL, `type` `HEAP`): `name`, `index_id`, `type`, `unique`, `primary_key`, `key_columns` (`DESC` marked), `included_columns`, `filter`, `disabled`, `hypothetical`, `user_seeks`, `user_scans`, `user_lookups`, `user_updates`, `last_user_seek/scan/lookup/update` (NULL: none since the server started). Without VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE) the usage columns are NULL and a warning says so, exit 0. `--fragmentation` (off by default: it reads the index) adds `avg_fragmentation_pct`, `page_count`, `fragment_count`, `avg_page_space_used_pct` (NULL in `limited`, which does not read the leaf; all four NULL for the heap) |
| `object stats <object> [-p <profile>] [connection options]` | A table or view. Record: `schema`, `name`, `type`. Set `statistics`, by name: `name`, `stats_id`, `columns`, `origin` (`index`, `auto`, `user`), `filter`, `last_updated`, `rows`, `rows_sampled`, `sample_pct`, `steps`, `modification_counter`, `no_recompute`, `incremental`. A statistic never computed, or one the login may not read (`sys.dm_db_stats_properties` needs SELECT on the columns or ownership), has `last_updated` through `modification_counter` NULL, counted in a warning |
| `query run` (T-SQL input) `[-p <profile>] [connection options] [run options]` | The script's result sets, then messages; `ROWS_AFFECTED` in the header. `--param` sends it through `sp_executesql` (§ Parameters). A wrapped run (§ Transactions) adds a record: `maxRowsAffected` (with `--max-rows-affected`), `transaction`. A refused script is BLOCKED with the record `detectedClass`, `allowedClass`, `statements`. `--dry-run` answers as `query classify --allow` (§ Running T-SQL) |
| `query parse` (T-SQL input) `[-p <profile>] [connection options] [--timeout <sec>]` | Record: `valid`, `batchCount` (batches sent, each `GO n` repetition counted), `detectedClass`; each syntax error is a `SQL_ERROR` with its script line (§ Running T-SQL) |
| `query classify` (T-SQL input) `[--allow <class>]` | Record: `detectedClass`, `allowedClass` (with `--allow`), `flags`, `statementCount`, and `statements` when blocked; then a set, one row per statement: `batch`, `repeat` (`GO n`), `line`, `class`, `leader`, `object`, `reason` (why the class is above the leader's), `flags`. Needs no connection. With `--allow`, a script it would refuse is `BLOCKED`, exit 7 (§ Safety) |

No command ever prints a password or secret.

### T-SQL input

Every `query` command takes its script from exactly one of:

| Option | Alias | |
|---|---|---|
| `--sql <text>` | `-Q` | Inline T-SQL |
| `--file <path>` | `-i` | A file; `-` reads stdin. UTF-8 (a BOM is dropped), or UTF-16 with a byte-order mark |
| `--sql-base64 <b64>` | | Base64 of the UTF-8 text, padded or not |

None, or more than one, is `USAGE_ERROR`; so are an unreadable file, text
that is not valid UTF-8 (or UTF-16 with an unpaired surrogate), and a script
over 64 MB. `--file -` with `--password-stdin` is refused: both read stdin.
Prefer `--file -` or `--sql-base64` to `--sql` for anything but one line — no
shell quoting can damage them.

## Discovery

The `db`, `object` and `server` commands read the catalog; they are all `read` and
never take `--allow`.

- **Where they look.** An `object` command reads the connection's database —
  the profile's, or `-d/--database` — and the header's `DATABASE` names it.
  There is no per-command database argument; `db info <name>` names one only
  because a database is its subject.
- **Object names.** `<object>` is split with the T-SQL lexer, so a
  `[bracketed]` or `"quoted"` part keeps its dots and doubled `]]`:
  `Sales.[Order.Detail]`. Two parts are `schema.name`; three,
  `database.schema.name`, only when the database is the connection's
  (compared exactly — another spelling may be another database), else a usage
  error naming `-d`. Four parts, an empty part (`db..name`), or anything but
  parts and dots is a usage error.
- **A name alone** is looked up in every schema: one match is that object; two
  or more is `USAGE_ERROR` listing them as `schema.name`; none is `NOT_FOUND`
  with up to five objects whose names contain it in the hint. It never assumes `dbo`. Names
  compare by the database's catalog collation, so in a `_CS_` one
  `sales.customer` is not `Sales.Customer` (the hint then names it).
- **Permissions.** What the login cannot see is left out, as SQL Server's
  metadata visibility does: a table without VIEW DEFINITION is not listed, not
  an error. A command whose read the server refuses outright is
  `PERMISSION_DENIED`, exit 9; a part of an answer that could not be read is
  NULL with a warning, exit 0. `agent context` lists the permissions the login
  is known to lack and the commands each one affects.

## Storage

`db files`, `db space`, `db log-usage`, `db table-sizes` and `server volumes`
report sizes. They are `read`, and the figures are the server's own: `db space`
and `db table-sizes` use `sp_spaceused`'s arithmetic, and the log figures are
`sys.dm_db_log_space_usage`'s, so under `--unit kb` each equals what the
procedure or DMV prints. The TUI's database and table panes split space by
allocation-unit type instead, so their data and index figures can differ.

- **Units.** `--unit kb|mb|gb` (lower-case; default `mb`) names the columns —
  `size_mb` becomes `size_gb` — and sets their text: `kb` integers (`bigint`),
  `mb`/`gb` two decimals (`decimal(18,2)`, a string in JSON as `db list`'s
  `size_mb`). `*_pct` columns always have two decimals. Filters and sorting
  compare the exact KB, never the rounded text.
- **One database or all.** `db files`, `db space` and `db log-usage` take
  exactly one of `<name>` or `--all`; neither or both is `USAGE_ERROR`.
  `--all` walks every database, system ones included, in catalog order, with a
  `database` column first. `db table-sizes` takes `<name>` only.
- **A database that cannot be read inside** — not ONLINE, or no user for the
  login. Named: `db space`, `db log-usage` and `db table-sizes` fail, exit 8
  (`NOT_FOUND`) for its state and 9 (`PERMISSION_DENIED`) for the login's
  access; `db files` lists its files from `sys.master_files` with `filegroup`,
  `used`, `free`, `free_pct` NULL and a warning, exit 0 (the paths are what is
  needed when a database is in trouble; without VIEW ANY DEFINITION
  `master_files` shows none, and only the warning remains). Under `--all` each
  such database is one warning, and the others are reported.
- **Warnings** have fixed thresholds and no options, and are raised only for the
  rows shown: a data or log file under 10 % free inside it, at 90 % or more of
  a capped `max_size`, or growing by a percentage; a file on, or a volume, under
  10 % free; a log whose `log_reuse_wait_desc` is not `NOTHING` (transient
  waits such as `CHECKPOINT` included). They are facts to look at, not
  judgements.
- **Permissions.** `db log-usage` and `server volumes` need VIEW SERVER STATE
  (2022+: VIEW SERVER PERFORMANCE STATE) — the log DMVs are server-scoped
  despite their names — and fail, exit 9, without it. `db files`'
  `volume_free_pct` and `db space`'s log columns are NULL with a warning
  instead. `agent context` lists both.

## Running T-SQL

`query run` runs the script on **one session**, as a query window does: temp
tables, `SET` options and `USE` carry across its `GO` batches; nothing carries
across calls. The order is fixed: read and classify the script, check it
against `--allow` and the profile's ceiling (§ Safety) — a refusal is exit 7
**before anything connects** — then connect, run, and answer.

| Option | |
|---|---|
| `--allow <read\|write\|ddl\|admin>` | Highest class the script may reach (default `read`); the profile's ceiling caps it |
| `--dry-run` | Classify and check without connecting: `query classify`'s answer with `allowedClass` (the effective class); exit 7 when a run would be refused |
| `--transaction <auto\|commit\|rollback>` | § Transactions (default `auto`: each statement commits itself) |
| `--max-rows-affected <n>` | § Transactions |
| `--continue-on-error` | Send every batch even after one fails. The default stops after the first failing batch (within a batch SQL Server decides), with a warning naming how many batches were sent |
| `--timeout <sec>` | How long the script may run (default 60, `0` no limit); `--connect-timeout` bounds the connect. Past it the run is cancelled: `TIMEOUT`, exit 6 |
| `--statistics <io,time>` | `SET STATISTICS IO`/`TIME ON` for the run; their output is in `messages` |
| `--param <name>=<value>:<type>` | § Parameters; repeat for each parameter |

Every row goes through the output options (§ Output): the whole script runs
whatever `--max-rows` keeps, so a later `UPDATE` still runs and
`totalRowCount` stays exact.

- **Errors.** A SQL Server error of severity 11 or more goes to `errors` with
  its number, severity, state, script line and procedure, not to `messages`.
  It is `SQL_ERROR` (exit 5) — `PERMISSION_DENIED` (exit 9) when the server
  says the login lacks a permission. A missing object stays `SQL_ERROR`:
  `NOT_FOUND` is for what the command line names. With
  `--continue-on-error`, some batches failing and some succeeding is
  `PARTIAL` (exit 10), listed first. A connection lost mid-run is
  `CONNECTION_FAILED` (exit 3).
- **Rows affected.** `rowsAffected` sums the counts of statements that
  returned no rows (`INSERT`, `UPDATE`, `DELETE`, `MERGE`, `SELECT INTO`,
  triggers' statements). A `SELECT`'s count is not a change, and neither —
  the protocol cannot tell them apart — is a DML statement's with an `OUTPUT`
  clause. `SET NOCOUNT ON` sends no counts. Each count is also a message.
- **Ctrl+C** cancels the run and closes the session; the server rolls back an
  open transaction. Exit 130.

`query parse` sends `SET PARSEONLY ON`, then every batch (so every syntax
error is reported in one call), then `OFF`. The server checks syntax and runs
nothing, so it needs no `--allow`. **Names are not resolved**: a missing
table or column is no error here, only at run time. A script naming
`PARSEONLY`, `NOEXEC` or `SHOWPLAN_*` anywhere outside literals and comments —
`OFF` included, which would switch the mode off and run the rest — is
BLOCKED, exit 7.

### Parameters

`--param name=value:type` (repeatable) runs the script through
`sp_executesql` with `@name` declared as `type`, so a value is never pasted
into the T-SQL:

```bash
gossms query run -p dev --file by-id.sql --param id=42:int --param "who=O'Brien:nvarchar(50)"
# sends: EXEC sp_executesql N'<by-id.sql>', N'@id int, @who nvarchar(50)', @id = N'42', @who = N'O''Brien';
```

- The name runs to the first `=`, the type starts after the last `:`, so a
  value may contain both (`at=12:30:00:time(0)`) and commas. A leading `@` on
  the name is optional.
- **Names and types are matched, never escaped** — they are T-SQL in the
  declaration list. A name is a regular identifier (no `@@`, no brackets). A
  type is a system type from a closed list, with its own argument form:
  `bigint int smallint tinyint bit money smallmoney real date datetime
  smalldatetime uniqueidentifier xml sql_variant sysname`; `decimal(p[,s])`,
  `numeric(p[,s])`; `float(n)`, `time(n)`, `datetime2(n)`,
  `datetimeoffset(n)`; `char nchar varchar nvarchar binary varbinary` with
  `(n)` or `(max)`. Anything else — a user type, `OUTPUT`, a second
  declaration — is `USAGE_ERROR`.
- The value is sent as a Unicode string literal and the server converts it to
  the declared type, as it does a client's string parameter; a value that
  does not convert is `SQL_ERROR` (Msg 8114, line 0 as the server reports
  it). `binary`/`varbinary` take `0x` and hex digits instead (nvarchar does
  not convert to binary). There is no NULL value: write `NULL` in the script.
- The script must be **one batch, sent once** (no `GO`, no `GO n`):
  `sp_executesql` runs a single batch. Inside it, as in any dynamic SQL, `USE`
  and `SET` last only until the script ends, and a `#temp` table it creates is
  gone afterwards. An error inside it still names its script line.
- The script is **classified as written**, not as the `sp_executesql` that
  carries it (which alone would fail closed to `write`).

### Transactions

`--transaction rollback`, `--transaction commit` and `--max-rows-affected`
wrap the whole script — every batch, one session — in one transaction:
`SET XACT_ABORT ON; BEGIN TRANSACTION` first, then the script, which stops at
the first failing batch (`--continue-on-error` is refused, exit 2: an error
under `XACT_ABORT` rolls the transaction back, and the later batches would
commit on their own). The record's `transaction` says how it ended:

| `transaction` | When |
|---|---|
| `rolled-back` | `rollback`; any error; `--max-rows-affected` exceeded; a timeout or Ctrl+C |
| `committed` | `commit`, or `--max-rows-affected` within the limit, and every batch succeeded |
| `ended-by-script` | The transaction was gone when the script finished: a procedure or dynamic SQL committed or rolled it back (Msg 266) |

- The check refuses (exit 7) a script that controls transactions itself
  (`BEGIN TRAN`, `COMMIT`, `ROLLBACK`, `SAVE TRAN`, `SET
  IMPLICIT_TRANSACTIONS ON`) or holds a statement SQL Server refuses inside
  one (`ALTER DATABASE`, `BACKUP`, `RESTORE`, …).
- **The guarantee covers the script's own statements, not procedure
  bodies.** A procedure's `COMMIT` ends the wrapper and commits what came
  before it, and what follows runs in autocommit. This is found afterwards
  and reported as `SQL_ERROR` "the script ended the wrapping transaction"
  (exit 5): check the data.
- `--max-rows-affected <n>` commits only when `rowsAffected` stays at or
  under `n`; over it the transaction is rolled back and the answer is
  BLOCKED, exit 7. With `--transaction rollback` it rolls back either way.
  It can only trust what the server counts, so it refuses (exit 7, before
  connecting) a script with `SET NOCOUNT ON`, dynamic SQL, a procedure call
  not on the read allow-list or an `OUTPUT` clause. Triggers' counts are
  included: it over-counts rather than under-counts.

## Safety

`internal/cli/safety` puts every statement into an operation class by its
leading keywords:

| Class | Examples | Needs |
|---|---|---|
| `read` | `SELECT`, `USE`, `SET` session options, `DECLARE`, `PRINT`, `EXECUTE AS`, catalog procedures on the read allow-list (`sp_help`, `sp_helptext`, `sp_spaceused`, `sp_who2`, …), `DBCC SHOW_STATISTICS`/`SQLPERF`/`OPENTRAN`/`INPUTBUFFER`/`TRACESTATUS`/`USEROPTIONS` | nothing (the default) |
| `write` | `INSERT`, `UPDATE`, `DELETE`, `MERGE`, transaction control (`BEGIN TRAN`, `COMMIT`, …), a procedure not on the read allow-list, dynamic SQL, `OPENQUERY`/`OPENROWSET` | `--allow write` |
| `ddl` | `CREATE`/`ALTER`/`DROP` of objects, `TRUNCATE`, `SELECT … INTO`, `DISABLE`/`ENABLE TRIGGER`, `UPDATE STATISTICS` | `--allow ddl` |
| `admin` | `ALTER DATABASE`, `ALTER SERVER`, logins, users, roles, keys and certificates, `GRANT`/`DENY`/`REVOKE`, `BACKUP`, `RESTORE`, `KILL`, `SHUTDOWN`, `RECONFIGURE`, `sp_configure`, `xp_` and msdb procedures not on the read list, `DBCC` not on the read list | `--allow admin` |

`query classify --file <path>` shows each statement's class and why. The
classifier reads the script with gossms's one lexer (`tuikit/sqltext`), so a
keyword in a comment, literal or quoted identifier
means nothing. It fails closed: an unrecognised statement, dynamic SQL and a
procedure not on the read allow-list are `write` at least, and a class keyword
anywhere in a statement raises it (`SELECT 1` followed by `DISABLE TRIGGER`
on the next line, which the statement splitter merges, is `ddl`). It is a
guardrail, not the security boundary: that is the login's permissions.

A run's check refuses, in this order, the first of:

1. a script above the allowed class — the lower of `--allow` and the profile's
   ceiling (`max_allow`, which no `--allow` raises). The refusal names the
   statements above it: `STATEMENTS: UPDATE Sales.Customer (line 1)`;
2. `SET PARSEONLY`, `SET NOEXEC` or `SET SHOWPLAN_*` `ON`: modes the command
   line runs itself;
3. with a transaction, and with `--max-rows-affected`, what it cannot hold or
   count (§ Transactions).

A refusal is `STATUS: BLOCKED`, `ERROR_CODE: BLOCKED`, exit 7, with
`DETECTED_CLASS`, `ALLOWED_CLASS` and `STATEMENTS` among the record's fields
and a hint naming the `--allow` that would do — or saying that the profile's
ceiling is what refuses.

## The import rule

`internal/cli` never imports `internal/tui`, a `tuikit` package other than
the stdlib-only `tuikit/sqltext`, or tcell: the command line runs without a
screen. See `ARCHITECTURE.md` § Why split this way.
