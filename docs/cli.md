# Command line

The command-line contract: how gossms is invoked without its interface, how it
parses arguments, what it writes and how it exits. Agents and scripts depend on
every rule here, so a change to one is a change to this file in the same
commit.

**This document grows per phase.** It states only what is implemented; the
command tree, output formats and safety model arrive with the phases that build
them. Today (through phase 7) the commands are `version`, `help`, `agent
guide/schema/context`, `profile list/show/test`, `db
list/info/files/space/log-usage/table-sizes/set`, `db scoped-config
list/get/set`, `server info/memory/volumes/log`, `server config
list/get/set/reset`, `server traceflag list/on/off`, `object
list/search/describe/definition/dependencies/indexes/stats`, `query
classify/run/parse`, `plan estimated/actual/cached`, `script object/db`,
`data top/sample/count/profile/export/import`, `monitor
sessions/blocking/waits/top-queries/querystore/deadlocks/kill` and `xevent
sessions/capture`; the formats `text`, `json`, `jsonl`, `csv`, `tsv` and
`markdown`. Every command but `version`, `help`, `agent guide/schema`,
`profile list/show` and `query classify` connects to a server, and so does
`agent guide` given a profile.

Code: `cmd/gossms/main.go` (mode switch), `internal/cli` (`Main`, help),
`internal/cli/spec` (the command tree as data), `internal/cli/args` (the
parser), `internal/cli/output` (envelope, renderers, exit codes),
`internal/cli/connect` (connection options and resolution),
`internal/cli/safety` (the T-SQL classifier and its check),
`internal/cli/audit` (the audit log),
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
| `--force` | | Replace an existing `--out-file`, a `plan` command's `--plan-file` or a `script` command's `--script-file` |

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
| `recipes` | Explore a schema, read data, check space, change server settings, tune a slow query, script and export, make a safe data fix, diagnose a slow server | `guide/recipes.md` |

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
  parsers would round as a float — the grid's text as a string. A column of
  type `json` (`xevent capture`'s `data`) holds a document, written as a
  JSON value and never cut by `--max-col-width`; other formats show its
  text.
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
| `agent context [-p <profile>] [connection options]` | Connects (§ Connecting). Header `DATABASE` is the connection's. Record: `profile` (null ad hoc), `serverName`, `productVersion`, `majorVersion`, `edition`, `login`, `isSysadmin` (null when the server permissions were not probed), `database`, `databaseRoles` (the fixed roles held there, comma-separated), `defaultAllow` (`read`), `maxAllow` (the ceiling `--allow` cannot pass; `admin` ad hoc), `maxAllowSource` (`profile`, `default` — the profile sets none —, or ad hoc). Named sets — `databases`: `name`, `state`, `accessible` (HAS_DBACCESS: false for a database not ONLINE, whoever asks; NULL when the server does not say), `is_system`; `missing_permissions`: `permission`, `scope` (`server`, or `database [name]`), `commands`, `effect` — each permission a command needs that the login is known to lack (an alternative granted counts: 2022's VIEW SERVER/DATABASE PERFORMANCE STATE, CREATE/ALTER ANY DATABASE for the files' visibility; for `server log` and `server traceflag on/off`, membership in securityadmin or sysadmin); one the server answers nothing for is left out. The table rights the `data` commands need (SELECT; INSERT, and ALTER for `--truncate-first`) are not listed: the database-scope probe reads 0 for a login granted them per schema or table, so a 0 is no known lack — their help states them, and the server's refusal is exit 9; `profiles`: `name`, `server`, `database`, `max_allow`. A probe or read that fails is a warning, not an error |
| `profile list [--all]` | Set: `name`, `kind` (`profile`, or `recent` with `--all`), `server`, `port` (NULL when not set), `database`, `auth` (the `--auth` spelling), `user`, `max_allow` (NULL for a recent connection), `password` (`saved`, `none`, `unreadable`). Profiles sorted by name, then recent connections most recent first. An empty list carries a message saying how to make a profile |
| `profile show <name>` | Record: `name`, `kind`, `generatedName`, `server`, `port`, `database`, `auth`, `user`, `tenantId`, `clientId`, `encrypt`, `trustServerCertificate`, `hostNameInCertificate`, `extraProperties` (keys only — a value may be a secret), `maxAllow`, `password`. A password that cannot be opened adds a warning |
| `profile test <name> [connection options]` | Connects (§ Connecting). Header `SERVER`/`DATABASE` name the target, also on failure. Record: `profile`, `serverName`, `productVersion`, `productLevel`, `edition`, `platform`, `login`, `connectedDatabase` (the header's `DATABASE` is the one asked for), `auth`, `passwordSource` (`profile`, `env:<VAR>`, `stdin`, `none`), `maxAllow`, `latencyMs` (the whole connect). A dropped saved password adds a warning |
| `db list [-p <profile>] [connection options] [--include-system] [--state <state>]` | Set, by name: `name`, `database_id`, `state`, `recovery_model`, `compatibility_level`, `collation` (NULL for a database not ONLINE), `read_only`, `size_mb`, `data_mb`, `log_mb`, `created`, `is_system`, `snapshot_of` (the source's name; NULL for a database that is not a snapshot). System databases — the four built-ins and a replication distribution database — only with `--include-system`; snapshots are listed. Sizes are allocated file sizes from `sys.master_files`, so they answer in any state (tempdb's are its startup sizes); NULL where the login cannot see the files (VIEW ANY DEFINITION), and a failed size read is a warning, not an error. `--state` is `state_desc` lower-cased |
| `db info <name> [-p <profile>] [connection options]` | Header `DATABASE` is `<name>`. Record: `name`, `databaseId`, `state`, `recoveryModel`, `compatibilityLevel`, `collation`, `catalogCollation`, `readOnly`, `created`, `isSystem`, `isDistributor`, `snapshotOf`, `sizeMb`, `dataMb`, `logMb`, `dataUnallocatedMb`, `logAvailableMb`, then the `ALTER DATABASE SET` options: `owner`, `userAccess`, `pageVerify`, `containment`, `snapshotIsolation`, `readCommittedSnapshot`, `autoClose`, `autoShrink`, `autoCreateStats`, `autoUpdateStats`, `autoUpdateStatsAsync`, `parameterization`, `isTrustworthy`, `dbChaining`, `isBrokerEnabled`, `isEncrypted`, `delayedDurability`, `targetRecoveryTimeSeconds`, `acceleratedDatabaseRecovery`, `isLedger`. A part that could not be read is empty with a warning saying why — the space-used pair needs the database ONLINE and enterable; the rest answers from the server catalog in any state. An unknown database is `NOT_FOUND`, exit 8 |
| `db files <name>\|--all [--type data\|log\|filestream\|fulltext] [--filegroup <fg>] [--sort-by name\|size\|used\|free-pct] [--max-free-pct <n>] [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, database then `file_id` order: `database` (with `--all`), `logical_name`, `type`, `filegroup`, `physical_path`, `size_<u>`, `used_<u>`, `free_<u>`, `free_pct`, `growth` (`10 %`, `none`, or a size: KB under `--unit kb`, else MB), `max_size_<u>` (text: `unlimited`, or a size), `volume_free_pct`. Read inside the database (`sys.database_files`, `FILEPROPERTY`), so tempdb's sizes are current; one it cannot read inside is listed from `sys.master_files` (§ Storage). A FILESTREAM or full-text container's used, free, growth and max size are NULL. A snapshot lists its own sparse files. `--max-free-pct` (0–100) keeps files whose free % is known and at most it. `volume_free_pct` needs VIEW SERVER STATE (NULL with a warning without) |
| `db space <name>\|--all [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, one row per database: `database`, `total_<u>`, `data_size_<u>`, `data_used_<u>`, `unallocated_<u>`, `reserved_<u>`, `data_<u>`, `index_<u>`, `unused_<u>` — `sp_spaceused`'s figures and arithmetic, equal to its output in KB — then `log_size_<u>`, `log_used_<u>`, `log_used_pct` from `sys.dm_db_log_space_usage` (as `db log-usage` shows them; NULL with one warning without VIEW SERVER STATE). A snapshot shows its source's figures as of creation, log NULL, with a warning |
| `db log-usage <name>\|--all [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, one row per database: `database`, `log_size_<u>`, `log_used_<u>`, `log_used_pct` (`sys.dm_db_log_space_usage`'s), `log_reuse_wait_desc`, `vlf_count`, `vlf_active` (`sys.dm_db_log_info`, from 2016 SP2; NULL with a warning before). Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9. `--all` skips snapshots (no log); a named snapshot is `NOT_FOUND`, exit 8 |
| `db table-sizes <name> [--top <n>] [--schema <s>] [--sort-by reserved\|rows\|data\|index\|unused] [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, largest first (default `reserved`; schema, then name break ties): `schema`, `table`, `rows`, `reserved_<u>`, `data_<u>`, `index_<u>`, `unused_<u>` — each row equal to `EXEC sp_spaceused 'schema.table'` in KB, internal tables (XML, spatial, full-text indexes) included. `--top` default 20, 0 for every table (a cut, not a truncation). Needs no VIEW DATABASE STATE. A memory-optimized table's sizes are NULL with a warning |
| `db set <name> --option <KEY=VALUE>... [--timeout <sec>] [--rollback-immediate --confirm <name>] --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin`, gated as `config set`. Header `DATABASE` is `<name>` (not on the server: exit 8). One `ALTER DATABASE … SET` per `--option`, in the order given, through gosmo: `RECOVERY=FULL\|SIMPLE\|BULK_LOGGED` (`SetRecoveryModel`), `COMPATIBILITY_LEVEL=<n>` (`SetCompatibilityLevel`; 100 up to the server's major × 10, steps of 10 — Azure and an unread version are left to the server), `READ_ONLY=ON\|OFF` (`SetReadOnly`), `QUERY_STORE=ON\|READ_WRITE\|READ_ONLY\|OFF` (`ON` is `READ_WRITE`; the Query Store options read are re-sent with the new state, so nothing else changes), and `SetDatabaseOption`'s 21: `PAGE_VERIFY=CHECKSUM\|TORN_PAGE_DETECTION\|NONE`, `CURSOR_DEFAULT=LOCAL\|GLOBAL`, `CONTAINMENT=NONE\|PARTIAL`, the rest `ON\|OFF`. Keys and values ignore case; an unknown key (the hint lists them), a value the key does not take, a key given twice or a level above the server's is exit 2 before anything runs. No transaction (ALTER DATABASE cannot run in one): the first failure stops the run, the changes before it stay, the `change` set shows every key with its `after`, a warning names those not attempted, exit 5. `READ_COMMITTED_SNAPSHOT`, `CONTAINMENT` and `READ_ONLY` need the database to themselves and wait for its other sessions, bounded by `--timeout` (default 60, 0 none; `TIMEOUT`, exit 6, the hint naming `--rollback-immediate`); `--rollback-immediate` (only with one of them, else exit 2) sends `WITH ROLLBACK IMMEDIATE` to those and needs `--confirm` equal to `<name>` as given (`BLOCKED`, exit 7, without — §6.3); `--confirm` alone is exit 2. Needs ALTER on the database (Msg 5011: `PERMISSION_DENIED`, exit 9). `--dry-run`: `dryRun: "true"`, `tsql`; set `planned_change`: `option`, `current`, `new` (a warning for a key that would wait). A run: `dryRun: "false"`; set `change`: `option`, `before`, `requested`, `after`, from `sys.databases` (+ `sys.database_query_store_options`) read before and after — values spelled as the statement takes them; an `after` that is not the `requested`, and Query Store's actual state differing from the desired one, are warnings. `USER_ACCESS`, `OFFLINE`, `ONLINE` are not offered |
| `db create <name> [--collation <c>] [--recovery simple\|full\|bulk-logged] [--compat <n>] --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin` (`query run` classes `CREATE DATABASE` so too), gated as `config set`. gosmo `CreateDatabase`: `CREATE DATABASE [<name>] [COLLATE <c>]`, the files model's at the instance's default paths, then `ALTER DATABASE … SET RECOVERY` and `SET COMPATIBILITY_LEVEL` for the options given — not one transaction (CREATE DATABASE cannot run in one): when a `SET` fails the database stays, the record shows it, a warning says so, exit 5. A name already in `sys.databases` (any case, snapshots included) is exit 2 before anything runs; `--compat` is 100 up to the server's major × 10, steps of 10, else exit 2 (Azure and an unread version left to the server). Without CREATE DATABASE / CREATE ANY DATABASE (Msg 262): `PERMISSION_DENIED`, exit 9. Record: `name`, `state`, `recoveryModel`, `compatibilityLevel`, `collation`, `dryRun` — on a real run read back from `sys.databases` (header `DATABASE` is `<name>`; a value other than the one asked for is a warning); on `--dry-run` what the database would be (`state` null; model's recovery model and level, the server's collation, unless given) and `tsql` |
| `db drop <name> --confirm <name> [--close-connections] --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin`, gated as `config set`. `--confirm` must equal `<name>` exactly as given, else `BLOCKED`, exit 7, before connecting (§6.3; a dry run too). Header `DATABASE` is `<name>` (not on the server: exit 8). Refused before anything is sent: a system database or a replication distribution database (`BLOCKED`, 7); the connection's own database (exit 2: connect with `-d master`); a database in an availability group (7, the hint naming the group's `REMOVE DATABASE`; read only when HADR is enabled); a database with snapshots (7, naming them — `db drop` drops a snapshot like any database). Record, read before the drop: `name`, `databaseId`, `state`, `recoveryModel`, `sizeMb` (`sys.master_files`), `lastFullBackup` (msdb's last full backup, copy-only included; null when none, with a warning, and for a snapshot), `closeConnections`, `dryRun` (+ `tsql`); a real run adds `dropped` (`sys.databases` no longer holds the name). A database not ONLINE is a warning: SQL Server deletes the files of an ONLINE database only. gosmo `Database.Drop`, from master: without `--close-connections` a database in use is Msg 3702 (the server waits ~20 s first), `BLOCKED`, 7, the hint naming `--close-connections`; with it, `SET SINGLE_USER WITH ROLLBACK IMMEDIATE` and the `DROP` in one batch, which disconnects the database's sessions and rolls back their open transactions (put back to its access mode if the drop then fails). Msg 3709 (a snapshot made since) and 3752 (joined to an availability group) are `BLOCKED` too. Without CONTROL on the database / ALTER ANY DATABASE (Msg 3701): `PERMISSION_DENIED`, exit 9 |
| `db scoped-config list <name> [-p <profile>] [connection options]` | Header `DATABASE` is `<name>`. Set, by name: `name`, `value`, `value_for_secondary`, `is_default`, `type` — `sys.database_scoped_configurations` read inside the database, values as the view holds them, as text: a switch (`type` `bit`) reads `1`/`0`, a number its digits, a keyword (`nvarchar`) the keyword. `value_for_secondary` is NULL where readable secondaries use the primary's value; `is_default` is NULL on 2016 (no `is_value_default`). The database must be ONLINE (exit 8) and the login need a user in it (exit 9), as `db table-sizes` |
| `db scoped-config get <name> <setting>... [-p <profile>] [connection options]` | The list's row for each setting, in the order given (a repeat once); a name ignores case, an unknown one is `NOT_FOUND`, exit 8, the hint naming up to 3 that contain it |
| `db scoped-config set <name> <setting> <value> [--for-secondary] --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin`, gated as `config set`. `ALTER DATABASE SCOPED CONFIGURATION [FOR SECONDARY] SET <setting> = <value>` inside the database (gosmo `SetDatabaseScopedConfig`). The value is checked against the setting's `type` before anything runs (exit 2): a switch takes `ON`/`OFF` (`1`/`0` are sent as `ON`/`OFF` — the statement refuses a number there, Msg 102), a number a whole number ≥ 0 (its range is the server's: Msg 12108, exit 5), a keyword letters, digits and `_` (a quoted value — a URL — is not supported). `PRIMARY` only with `--for-secondary` (the server's Msg 12109 otherwise), making secondaries follow the primary. `--for-secondary` is accepted for a database in no availability group (stored, used once it is a readable secondary); a setting with no secondary value is Msg 12110, exit 5, with a hint. Needs ALTER ANY DATABASE SCOPED CONFIGURATION (Msg 15247: `PERMISSION_DENIED`, exit 9); on a readable secondary the database is read-only (Msg 3906, exit 5; the hint sends the change to the primary). `--dry-run`: record `dryRun: "true"`, `tsql` (`USE` + the statement); set `planned_change`: `name`, `replica` (`primary`\|`secondary`), `current`, `new`. A run: record `dryRun: "false"`; set `change`, from the view read before and after: `name`, `replica`, `before`, `requested`, `after`. Both sets spell values as the statement does (`ON`/`OFF`, `PRIMARY` for a NULL secondary value); an `after` that is not the `requested` is a warning |
| `server info [-p <profile>] [connection options]` | Record: `serverName`, `productVersion`, `majorVersion`, `edition` (as `agent context`), `productLevel`, `engineEdition`, `platform` (null when `@@VERSION` names none), `collation`, `isClustered`, `isHadrEnabled`, `startTime` (the server's local wall clock, no zone), `uptimeSeconds` (by the server's clock), `logicalCpus`, `numaNodes`, `physicalMemoryMb`, `defaultDataPath`, `defaultLogPath`, `defaultBackupPath`. `startTime` through `physicalMemoryMb` need VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE): without it they are null with one warning, exit 0 |
| `server memory [-p <profile>] [connection options]` | Named sets — `memory`, one row: `physical_mb`, `available_os_mb` (`sys.dm_os_sys_memory`), `max_server_memory_mb`, `min_server_memory_mb` (the running values), `committed_mb`, `target_mb` (Memory Manager's Total and Target Server Memory), `page_life_expectancy_s` (Buffer Manager's, instance-wide), `memory_grants_pending` (both null when the server does not publish the counter); `clerks`: `clerk`, `size_mb`, the 10 clerk types holding the most, largest first. Warnings: max server memory unlimited, available OS memory under 5 % of physical, memory grants pending. Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9 |
| `server traceflag list [-p <profile>] [connection options]` | Set, by flag: `flag`, `global`, `session` — every trace flag on now (`DBCC TRACESTATUS(-1)`, which any login may run). A startup flag (`-T`) shows as `global`, the DBCC does not tell them apart; `session` is this command's own session, so 0 for another session's flag. None on is an empty set |
| `server traceflag on <flag>... --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin`, gated as `config set`. `DBCC TRACEON(<flag>, -1)` for each flag in order (a repeat once): always global, since a session flag would die with the command's connection — there is no `--global`. A flag that is not a positive whole number is exit 2 before connecting; the upper bound is the server's (Msg 2560, exit 5, with a hint; 17698 on 17). Needs sysadmin: Msg 2571 is `PERMISSION_DENIED`, exit 9. `--dry-run`: record `dryRun: "true"`, `tsql`; set `planned_change`: `flag`, `current`, `new`. A run: record `dryRun: "false"`; set `change`, from `TRACESTATUS` read before and after: `flag`, `before`, `requested`, `after`. The first refused flag stops the run (exit 5) with the `change` set still read back. Warnings: the flag is lost at restart (a lasting one is a startup parameter, `-T` or `mssql-conf traceflag`), when one is on; a flag the second read does not show — the server skips a startup-only flag (834) with no error |
| `server traceflag off <flag>... --allow admin [--dry-run] [-p <profile>] [connection options]` | `DBCC TRACEOFF(<flag>, -1)`; otherwise as `traceflag on`. A flag still on afterwards is a warning; one set at startup comes back at the next restart |
| `server log [--lines <n>] [--search <text>]... [--since <time>] [--archive <n>] [--agent] [-p <profile>] [connection options]` | Set, oldest first: `date` (the server's local time, no zone: the log records none), `process` (`level` with `--agent`: 1 error, 2 warning, 3 information), `text` — one log file read by `xp_readerrorlog`, filtered on the server. `--search` (up to twice) keeps entries holding the text, case-insensitively, both when given twice (AND). `--since` is `YYYY-MM-DD[THH:MM[:SS]]` in the server's local time; a `Z` or an offset is exit 2. `--lines` keeps the last *n* matching entries (default 100, 0 for all). `--archive` picks the file (0 current, 1 the one before…), one per run: a `--since` before the file's start needs a higher one; an archive the server does not keep is exit 8, the hint naming the range. A sysadmin reads through `xp_readerrorlog`; any other login through `sp_readerrorlog` (gosmo falls back on the refusal), which needs securityadmin, VIEW SERVER STATE or (2022 on) VIEW ANY ERROR LOG — else exit 9 — and reads the whole file before `--since` applies |
| `server volumes [--unit kb\|mb\|gb] [-p <profile>] [connection options]` | Set, by volume: `volume` (mount point, else OS label, else the lowest file path), `total_<u>`, `free_<u>`, `free_pct`, `db_files` — every volume holding a database file of any database (`sys.master_files` × `sys.dm_os_volume_stats`). Implausible figures (total 0, free over total) are left out with a warning. Without VIEW SERVER STATE: `PERMISSION_DENIED`, exit 9 |
| `server config list [--name <text>] [--non-default] [--advanced] [-p <profile>] [connection options]` | Set, by name (case-insensitive): `name`, `alias` (the short name `config get` takes; NULL for none), `configured` (`value`), `running` (`value_in_use`), `default`, `min`, `max`, `dynamic`, `advanced` — every `sys.configurations` row, advanced ones whatever `show advanced options` is. `default` is the engine's default on the server's major (gosmo's table, from Microsoft's `sp_configure` reference), not what Setup or `mssql-conf` writes; NULL where gosmo has none (an option a CU added, Azure), with one warning naming them (the engine-written `version high/low part of SQL Server` aside). `--name` contains, case-insensitive, no wildcards; `--advanced` keeps advanced options; `--non-default` keeps those whose **configured** value differs from the default (min server memory configured 0 runs as 16 and is not listed), leaving out an option with no default |
| `server config get <name>... [-p <profile>] [connection options]` | `config list`'s set for each setting named, in the order given (a repeat once). A name is an alias — `maxdop`, `cost-threshold`, `max-memory`, `min-memory`, `adhoc`, `backup-compression`, `backup-checksum`, `remote-dac`, `clr`, `xp-cmdshell`, `contained-auth`, `blocked-threshold` — or a full `sys.configurations` name, both case-insensitive; anything else is `NOT_FOUND`, exit 8, the hint naming up to 3 names or aliases containing it. Record (context): `logicalCpus`, `numaNodes`, `logicalCpusPerNumaNode` (two decimals when uneven) for MAXDOP and cost threshold; `physicalMemoryMb`, `committedMemoryMb` (Total Server Memory) for max/min server memory; none for other settings. Without VIEW SERVER STATE they are null with one warning, exit 0 |
| `server config set <name> <value> [<name> <value>…] --allow admin [--dry-run] [--accept-risk] [--override] [-p <profile>] [connection options]` | Class `admin` (as `traceflag on/off`; every other `server` command is `read`): refused before connecting without `--allow admin`, `--dry-run` included (BLOCKED, exit 7, record `detectedClass`, `allowedClass`). A name as in `config get`; a negative value follows `--`. A value is a whole number in the option's unit; an option whose name ends `(MB)` also takes `MB`/`GB`/`TB`, one ending `(KB)` `KB`/`MB` (case-insensitive; `1.5GB` = 1536, `1.3MB` is refused). Exit 2 before anything runs: an odd argument count, a setting named twice, a unit elsewhere, a value outside `min`–`max` (the option's default aside: a self-configuring option's 0 lies below its reported minimum). Blocked (exit 7) unless `--accept-risk`: max or min server memory above physical memory, max below min server memory (values set in the same call count), MAXDOP above the logical CPUs, and any of these when the figure is unreadable (no VIEW SERVER STATE); a value equal to the default never is. All changes go in one `gosmo` `ApplyConfiguration` batch (`show advanced options` enabled only when needed and restored; one `RECONFIGURE`, `WITH OVERRIDE` under `--override`). Needs ALTER SETTINGS (Msg 15247: `PERMISSION_DENIED`, exit 9). `--dry-run`: record `dryRun: "true"`, `tsql` (the batch, verbatim); set `planned_change`: `name`, `current`, `new`, `dynamic`, `advanced`, `enables_show_advanced` (whether the batch's first step fires here). A run: record `dryRun: "false"`; set `change`, read back from `sys.configurations`: `name`, `before`, `requested`, `after_configured`, `after_running`, `restart_required` (running ≠ configured; min server memory 0 running 16 aside). Warnings: new max server memory below committed memory, a value waiting for a restart, a value not taken. A failed batch is exit 5 with the `change` set still read back; a refused `RECONFIGURE` leaves every change configured but not running — pending for the next `RECONFIGURE` — and the hint says so |
| `server config reset <name>... --allow admin [--dry-run] [--override] [-p <profile>] [connection options]` | `config set` to each setting's `default` (as `config list` shows it; a repeat once). A setting with no default (`default` NULL) is `UNSUPPORTED`, exit 11, hint: use `config set`. Output, gate and blocks as `config set` |
| `object list [-p <profile>] [connection options] [--type <kind>,…] [--schema <s>] [--name <text>] [--include-system]` | Set, by kind (in `--type`'s order: `table`, `view`, `procedure`, `function`, `trigger`, `synonym`, `sequence`, `type`, `schema`), then schema and name, case-insensitively: `schema` (NULL for a schema row), `name`, `type`, `is_system`. The objects of the connection's database (`-d`, or the profile's). `--schema` is equal and `--name` contains, both case-insensitive and without wildcards (Object Explorer's filter); a schema row matches either by its own name. A trigger is a table's or view's DML trigger; a type is an alias, table or CLR type. Without `--include-system`, what SQL Server ships is left out: system tables, the `sys` schema's views, procedures, functions and CLR types (geography, geometry, hierarchyid), and the system schemas (guest, INFORMATION_SCHEMA, sys, the fixed roles') |
| `object search <text> [-p <profile>] [connection options] [--in name\|definition\|column]` | Set: `schema`, `name`, `type`, and with `--in column` `column`, `data_type`. Case-insensitive, `%`/`_`/`[` literal. `name` (default): tables, views, procedures, functions and triggers whose name contains the text. `definition`: the modules whose T-SQL contains it — an encrypted or CLR module has none. `column`: the columns of tables and views whose name contains it. Empty text is a usage error |
| `object describe <object> [-p <profile>] [connection options]` | `<object>` is `schema.name`, a name alone when one schema has it (two or more: usage error listing them; none: `NOT_FOUND` with similar names), or `database.schema.name` for the connection's own database. Record: `schema`, `name`, `type` (the kind word, or the `type_desc` lower-cased for a kind without a folder: `primary_key_constraint`), `typeDesc`, `objectId`, `created`, `modified`, `parent` (a trigger's or constraint's table), then per kind — table `rowCount` (from partition metadata); view `isSchemaBound`; function `returns` (the type, or `TABLE`); trigger `isEnabled`, `events`; synonym `baseObject`; sequence `dataType`, `startValue`, `increment`, `minValue`, `maxValue`, `isCycling`, `cache`, `currentValue`. Named sets — table: `columns`, `keys`, `indexes`, `check_constraints`, `triggers`; view: `columns`, `indexes`, `triggers`; procedure or function: `parameters`. `columns`: `column_id`, `name`, `data_type`, `nullable`, `identity` (`IDENTITY(seed,increment)`; a view's column `IDENTITY`), `computed` (the expression, `PERSISTED` marked), `default`, `collation`, `primary_key`. `keys`: the primary key, unique constraints, then foreign keys — `name`, `type`, `columns`, `references` (`[schema].[table] (cols)`), `on_delete`, `on_update`, `disabled`, `not_trusted`. `indexes`: `name`, `type`, `unique`, `primary_key`, `key_columns` (`DESC` marked), `included_columns`, `filter`, `disabled`. `check_constraints`: `name`, `column` (NULL table-level), `definition`, `disabled`, `not_trusted`. `triggers`: `name`, `enabled`, `events`. `parameters`: `ordinal`, `name`, `data_type`, `is_output`, `has_default` (NULL for a T-SQL module: the catalog records defaults only for CLR ones). A computed, default or check expression the login cannot see (no VIEW DEFINITION) is NULL, counted in a warning; a user-defined type it holds no permission on reads as its base system type (`nvarchar(25)`, empty for a CLR type), counted in a warning — also in `parameters`, `returns`, a sequence's `dataType` and `object search --in column` |
| `object definition <object> [-p <profile>] [connection options]` | `<object>` as for `describe`. Record: `schema`, `name`, `type`, `definition` — the module's T-SQL as stored, multi-line (§ `text`). A view, procedure, function or DML trigger; any other kind is a usage error, exit 2. An encrypted module (or one the login lacks VIEW DEFINITION on) and a CLR module have `definition` null and a warning, exit 0. The stored text, not a generated script |
| `object dependencies <object> [--direction uses\|used-by\|both] [--depth n] [-p <profile>] [connection options]` | `<object>` as for `describe`. Record: `schema`, `name`, `type`, `direction` (default `both`), `depth` (default 1, 1–5; outside it a usage error). Set `dependencies`, one row per edge, breadth first, `uses` before `used-by`: `direction`, `level`, `via` (the object at the level before; the root at level 1), `schema`, `name`, `type`, `schema_bound`, `caller_dependent` (an unqualified name resolved as the owner would: its schema, then dbo). An object reached twice is followed once, so a cycle ends; a table's self-reference (its filtered index or statistic) is left out; another database's objects and missing ones are not listed. Needs SELECT on `sys.sql_expression_dependencies` (db_owner by default): without it `PERMISSION_DENIED`, exit 9 |
| `object indexes <object> [--fragmentation limited\|sampled\|detailed] [-p <profile>] [connection options]` | A table or view (any other kind: usage error, exit 2). Record: `schema`, `name`, `type`, `fragmentationMode` with `--fragmentation`. Set `indexes`, index_id order, a heap first (`name` NULL, `type` `HEAP`): `name`, `index_id`, `type`, `unique`, `primary_key`, `key_columns` (`DESC` marked), `included_columns`, `filter`, `disabled`, `hypothetical`, `user_seeks`, `user_scans`, `user_lookups`, `user_updates`, `last_user_seek/scan/lookup/update` (NULL: none since the server started). Without VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE) the usage columns are NULL and a warning says so, exit 0. `--fragmentation` (off by default: it reads the index) adds `avg_fragmentation_pct`, `page_count`, `fragment_count`, `avg_page_space_used_pct` (NULL in `limited`, which does not read the leaf; all four NULL for the heap) |
| `object stats <object> [-p <profile>] [connection options]` | A table or view. Record: `schema`, `name`, `type`. Set `statistics`, by name: `name`, `stats_id`, `columns`, `origin` (`index`, `auto`, `user`), `filter`, `last_updated`, `rows`, `rows_sampled`, `sample_pct`, `steps`, `modification_counter`, `no_recompute`, `incremental`. A statistic never computed, or one the login may not read (`sys.dm_db_stats_properties` needs SELECT on the columns or ownership), has `last_updated` through `modification_counter` NULL, counted in a warning |
| `query run` (T-SQL input) `[-p <profile>] [connection options] [run options]` | The script's result sets, then messages; `ROWS_AFFECTED` in the header. `--param` sends it through `sp_executesql` (§ Parameters). A wrapped run (§ Transactions) adds a record: `maxRowsAffected` (with `--max-rows-affected`), `transaction`. A refused script is BLOCKED with the record `detectedClass`, `allowedClass`, `statements`. `--dry-run` answers as `query classify --allow` (§ Running T-SQL) |
| `query parse` (T-SQL input) `[-p <profile>] [connection options] [--timeout <sec>]` | Record: `valid`, `batchCount` (batches sent, each `GO n` repetition counted), `detectedClass`; each syntax error is a `SQL_ERROR` with its script line (§ Running T-SQL) |
| `plan estimated` (T-SQL input) `[-p <profile>] [connection options] [--plan-format summary\|tree\|xml] [--top <n>] [--plan-file <path>] [--timeout <sec>]` | Record: `planType` (`estimated`), `detectedClass`, `statementCount`, `planFile` when written; `--plan-format xml` adds the verbatim `planXml`. Named sets per format (§ Execution plans) |
| `plan actual` (T-SQL input) `[-p <profile>] [connection options] [run options but --statistics] [--plan-format summary\|tree\|xml] [--top <n>] [--plan-file <path>]` | Runs the script as `query run` does, under `SET STATISTICS XML ON`. Record: `planType` (`actual`), `detectedClass`, `statementCount`, `planXml`/`planFile` as for `plan estimated`, then a wrapped run's `maxRowsAffected`, `transaction`; `ROWS_AFFECTED` in the header. The format's sets, then `result_sets`: one row per set the script returned, its rows counted, not returned (§ Execution plans). Refused, blocked and `--dry-run` answers as `query run`'s |
| `plan cached <plan_handle> [--statement-start <n>] [--statement-end <n>] [--plan-format summary\|tree\|xml] [--top <n>] [--plan-file <path>] [-p <profile>] [connection options]` | Record: `planType` (`cached`), `planHandle` (`0x`, upper-case hex), `statementStart`, `statementEnd`, `statementCount`, `planXml`/`planFile` as for `plan estimated`; the format's sets, the actual columns NULL (§ Execution plans) |
| `script object <object> [-p <profile>] [connection options] [--as <verb>] [--script-file <path>]` | `<object>` as for `object describe`. Record: `object` (`[schema].[name]`), `type`, `verb`, `script` (verbatim), `scriptFile` when written (§ Scripting) |
| `script db <name> [-p <profile>] [connection options] [--as create\|drop\|drop-and-create] [--script-file <path>]` | Record: `database`, `verb`, `script` (verbatim), `scriptFile` when written (§ Scripting) |
| `data top <object> [-p <profile>] [connection options] [--rows <n>] [--columns <list>] [--where <expr>] [--order-by <list>] [--timeout <sec>]` | `<object>` a table or view, as for `object describe`. Record: `object` (`[schema].[name]`), `type`, `tsql` (verbatim: the SELECT sent); then its rows (§ Data) |
| `data count <object> [-p <profile>] [connection options] [--where <expr>] [--approximate] [--timeout <sec>]` | Record: `object`, `type`, `rows` (NULL when the count failed), `method` (`exact`, `approximate`), `tsql` for an exact count (§ Data) |
| `data sample <object> [-p <profile>] [connection options] [--rows <n>] [--columns <list>] [--where <expr>] [--timeout <sec>]` | As `data top`, the rows a random sample (§ Data) |
| `data profile <object> [-p <profile>] [connection options] [--columns <list>] [--where <expr>] [--top-values <n>] [--timeout <sec>]` | Record: `object`, `type`, `tsql` (the aggregate SELECTs, `GO` between); set `columns` (`column type rows nulls null_pct distinct min max min_len max_len`), and with `--top-values` set `top_values` (`column value count`) (§ Data) |
| `data export <object> \| (T-SQL input) --out-file <path> [-p <profile>] [connection options] [--columns <list>] [--where <expr>] [--order-by <list>] [--param …] [--timeout <sec>]` | Every row to `--out-file` (required; format by extension). Record: object form `object`, `type`, `tsql`; T-SQL form `detectedClass`, `statementCount`; each set's `totalRowCount` and `outFile` (§ Data) |
| `data import <table> --in-file <path\|-> [-p <profile>] [connection options] --allow write [--in-format csv\|tsv\|jsonl] [--truncate-first --confirm <table>] [--no-check-constraints] [--fire-triggers] [--tablock] [--dry-run] [--timeout <sec>]` | Bulk-loads the file into the table; `--truncate-first` needs `--allow ddl`. Record: `object`, `type`, `inFile`, `format`, `truncateFirst`, `rows`, `dryRun`, `tsql` (the TRUNCATE, on a dry run); set `columns` (`file_column column type action`) (§ Data) |
| `monitor sessions [--active] [--session-database <db>] [--login <l>] [--program <p>] [--include-system] [-p <profile>] [connection options]` | Set `sessions`, by spid (§ Monitoring): `spid`, `login`, `host`, `program`, `database`, `status`, `command`, `open_tran`, `cpu_ms`, `reads`, `writes`, `logical_reads`, `elapsed_ms`, `blocked_by`, `wait_type`, `wait_ms`, `wait_resource`, `text` |
| `monitor blocking [-p <profile>] [connection options]` | Set `blocking`, in tree order (§ Monitoring): `head`, `depth`, `cycle`, `spid`, `blocked_by`, `wait_type`, `wait_ms`, `wait_resource`, `login`, `host`, `program`, `database`, `open_tran`, `text`. No blocking is an empty set, exit 0 |
| `monitor waits [--top <n>] [--interval <sec>] [--all] [-p <profile>] [connection options]` | Record: `since`, `elapsedMs`. Sets `waits` (`wait_type`, `category`, `waiting_tasks`, `wait_ms`, `signal_ms`, `resource_ms`, `pct`, `avg_ms`) and `categories` (`category`, `waiting_tasks`, `wait_ms`, `signal_ms`, `resource_ms`, `pct`) (§ Monitoring) |
| `monitor top-queries [--by cpu\|reads\|writes\|duration\|executions] [--avg] [--top <n>] [--plan-database <db>] [-p <profile>] [connection options]` | Record: `rankedBy`, `ranking` (`total`, `average`). Set `queries`, highest first: `n`, `database`, `executions`, `cpu_ms`, `avg_cpu_ms`, `duration_ms`, `avg_duration_ms`, `logical_reads`, `avg_logical_reads`, `logical_writes`, `avg_logical_writes`, `last_execution`, `cached_at`, `query_hash`, `plan_handle`, `statement_start`, `statement_end`, `text` (§ Monitoring) |
| `monitor querystore [--report top\|regressed\|variation\|forced\|waits] [--regressed] [--metric <m>] [--stat avg\|max\|min\|stdev\|total] [--since <t>] [--until <t>] [--top <n>] [--min-exec <n>] [--min-regression-pct <n>] [--wait-category <c>] [-p <profile>] [connection options]` | The connection's database. Record: `report`, `from`, `to`, `baselineFrom`/`baselineTo` (regressed), `metric`, `statistic`, `unit`. Set `queries`: `n`, `query_id`, `object`, `executions`, `plans`, `forced_plan_id`, `last_execution`, `value`, + `baseline_value`, `baseline_executions`, `regression`, `regression_pct` (regressed) or `variation` (variation), `text`; `--report waits` puts set `categories` (`category`, `executions`, `value`) first (§ Monitoring) |
| `monitor deadlocks [--since <t>] [--until <t>] [--top <n>] [--deadlock-format summary\|xml] [--deadlock-file <dir>] [-p <profile>] [connection options]` | Record: `source` (`event_file`, `ring_buffer`), `from`, `to` (UTC), `deadlocksInWindow`; `deadlock1`… (verbatim) with `--deadlock-format xml`; `deadlockFile` when written. Set `deadlocks`: `n`, `time_utc`, `victims`, `processes`, `databases`, `objects`; in `summary` set `processes`: `n`, `spid`, `ecid`, `victim`, `login`, `host`, `program`, `database`, `isolation`, `lock_mode`, `wait_resource`, `transaction`, `statement` (§ Monitoring) |
| `monitor kill <spid> --confirm <spid> --allow admin [--dry-run] [-p <profile>] [connection options]` | Class `admin`. Record: `dryRun`, `tsql` (dry run), `killed`, `ended` (after a real KILL). Set `session`: the target's `monitor sessions` row (§ Monitoring) |
| `xevent sessions [-p <profile>] [connection options]` | Set `sessions`, by name: `name`, `scope` (`server`, `database`), `running`, `startup`, `started`, `dropped_events`, `targets`, `events`, `file` (§ Extended Events) |
| `xevent capture --allow admin [--template <t>] [--duration <sec>] [--max-events <n>] [--session-database <db>] [--login <l>] [--program <p>] [--min-duration-ms <n>] [--dry-run] [-p <profile>] [connection options]` | Class `admin`. Record: `template`, `session`, `durationSeconds`, `dryRun`, `tsql` (dry run), `eventsSeen`, `eventsKept`, `sessionDropped`. Set `events`, oldest first: `time_utc`, `event`, `session_id`, `database`, `login`, `program`, `host`, `duration_ms`, `cpu_ms`, `logical_reads`, `writes`, `row_count`, `result`, `error`, `severity`, `text`, + `data` (type `json`) under `-o jsonl` (§ Extended Events) |
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
never take `--allow` — except `server config set/reset`, `server traceflag
on/off`, `db set`, `db scoped-config set`, `db create` and `db drop`, which
are `admin` (as are `monitor kill` and `xevent capture`).

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

## Server configuration

`server config list/get/set/reset` read and change the `sp_configure`
settings (`sys.configurations`). `list` and `get` are `read`; `set` and
`reset` are `admin` and go through § Writes.

- **Names.** A setting is an alias or a full `sys.configurations` name, both
  ignoring case, the alias tried first. A full name with spaces is one
  argument, so the shell needs quotes: `"max worker threads"`. An unknown name
  is `NOT_FOUND`, exit 8, the hint naming up to 3 names or aliases that
  contain it. The aliases:

  | Alias | Setting |
  |---|---|
  | `maxdop` | `max degree of parallelism` |
  | `cost-threshold` | `cost threshold for parallelism` |
  | `max-memory` | `max server memory (MB)` |
  | `min-memory` | `min server memory (MB)` |
  | `adhoc` | `optimize for ad hoc workloads` |
  | `backup-compression` | `backup compression default` |
  | `backup-checksum` | `backup checksum default` |
  | `remote-dac` | `remote admin connections` |
  | `clr` | `clr enabled` |
  | `xp-cmdshell` | `xp_cmdshell` |
  | `contained-auth` | `contained database authentication` |
  | `blocked-threshold` | `blocked process threshold (s)` |

- **Defaults.** `default` is the engine's own default on the server's major
  version: gosmo's table, built from Microsoft's `sp_configure` reference and
  checked against live 2016, 2017 and 2025 instances. It is not what Setup or
  `mssql-conf` writes (Setup recommends MAXDOP and max server memory since
  2019), nor what an instance was installed with. It is NULL where gosmo has
  no entry (an option a CU added, Azure), with one warning naming those
  options; such an option is left out of `--non-default`, and `reset` refuses
  it (`UNSUPPORTED`, exit 11: use `set`). `--non-default` compares the
  configured value (`value`), not the running one.
- **Units.** A value is a whole number in the option's own unit. An option
  whose name ends `(MB)` also takes `MB`, `GB` or `TB`; one ending `(KB)`,
  `KB` or `MB` (case-insensitive). The result must be whole: `1.5GB` is 1536,
  `1.3MB` is exit 2, as is a unit on any other option. A negative value
  follows `--` (`-1` would read as an option).
- **Range.** A value outside `min`–`max` is exit 2 before anything runs,
  except the option's default: a self-configuring option's 0 (`max worker
  threads`, `locks`) lies below the minimum the server reports.
- **Blocks.** Exit 7 (`BLOCKED`) unless `--accept-risk`, a fixed list: max or
  min server memory above physical memory; max below min server memory
  (values set in the same call count); MAXDOP above the logical CPUs; and
  any of these when the figure cannot be read (no VIEW SERVER STATE: it fails
  closed). A value equal to the option's default is never blocked, so `reset
  max-memory` goes through. Warnings, facts only: a new max server memory
  below the memory committed now, a value that waits for a restart.
- **One batch.** Every change of a call runs in one gosmo
  `ApplyConfiguration` batch: `show advanced options` turned on only if an
  advanced option needs it and put back after, each `sp_configure`, one
  `RECONFIGURE` (`WITH OVERRIDE` under `--override`, for a value the plain
  form refuses, such as `recovery interval (min)` above 60). A refused
  `RECONFIGURE` leaves every change of the batch configured but not
  running, pending for anyone's next `RECONFIGURE`; the change set shows it
  and the hint says so.
- **Restart.** `restart_required` is 1 when the running value is not the
  configured one after the batch — a non-dynamic option, or one `RECONFIGURE`
  did not install. `min server memory (MB)` configured 0 runs as 16 and is not
  flagged.

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

## Execution plans

`plan estimated` sends every batch under `SET SHOWPLAN_XML ON` on one
session: the server compiles each statement, returns its plan and runs
nothing. So it needs no `--allow` and the profile's ceiling does not apply,
whatever the script does; `detectedClass` says what running it would need.

- **Refusals.** `PARSEONLY`, `NOEXEC` or `SHOWPLAN_*` anywhere in the
  script, `OFF` included, is BLOCKED, exit 7, before connecting (as `query
  parse`). Without SHOWPLAN in the database the server answers Msg 262:
  `PERMISSION_DENIED`, exit 9, with a hint naming the grant. A statement that
  uses a `#temp` table (or any object) the script creates earlier fails with
  Msg 208, as in SSMS — nothing ran to create it; the error's hint says so.
  Every batch is sent, so every compile error is reported in one call; the
  plans of the batches that compiled are still returned.
- **No `--param`.** Under SHOWPLAN_XML the server plans an `sp_executesql`
  call as the `EXEC` alone (one `EXECUTE PROC` statement, no operators), not
  the statement inside, so a parameterized plan would be empty.
- **`--plan-format summary`** (default), four named sets:
  `statements` — `n`, `type`, `cost` (estimated subtree cost), `est_rows`,
  `actual_rows`, `elapsed_ms`, `cpu_ms`, `dop`, `grant_kb`, `warnings` (the
  statement's and its operators'), `text` (one line, cut to 200 characters);
  `operators` — the `--top` (default 10, `0` for all) operators with the
  highest own estimated cost across all statements (the plan viewer's
  ranking; a trivial statement's lone operator, 100% of its statement, does
  not outrank a costly scan elsewhere): `stmt`, `node`, `physical`,
  `logical`, `object`, `cost_pct` (share of its statement, as the plan viewer
  shows it), `est_rows` (per execution), `actual_rows`, `elapsed_ms`, `parallel`, `mode`,
  `warnings`, `misestimate` (actual rows over all executions more than 10×
  the estimate for as many, either way; NULL when the operator did not run);
  `missing_indexes` — `stmt`, `impact`, `database`, `table`, `equality`,
  `inequality`, `include`, `create_statement` (the plan viewer's one-line
  `CREATE NONCLUSTERED INDEX`, its name a placeholder); `warnings` — `stmt`,
  `node`, `operator` (both NULL for a statement's own), `warning`. The actual
  columns are NULL on an estimated plan.
- **`tree`**: one set, `tree`, every operator in preorder statement by
  statement, with `depth` and `parent` (node id) before the operator columns
  — a column, since text keeps each row on one line.
- **`xml`**: the plan document as the verbatim field `planXml` — one
  `ShowPlanXML` document per batch, joined by a line break, as the plan viewer
  holds it.
- **`--plan-file <path>`** writes that document, UTF-8, exactly as the query
  editor's File > Save Execution Plan As does; `.sqlplan` opens in SSMS and
  in gossms's plan viewer. An existing file is `USAGE_ERROR` before
  connecting unless `--force`. It is written only when the command
  succeeds; otherwise a warning says it was not.
- A row count the server sends for the showplan set is dropped: nothing ran.

`plan actual` runs the script — it is `query run` with the plan captured
(`SET STATISTICS XML ON` around the script, on the same session). Everything
`query run` checks applies unchanged: the class against `--allow` and the
ceiling, `--transaction`, `--max-rows-affected`, `--param`,
`--continue-on-error`, `--timeout`, `--dry-run` (§ Running T-SQL, §
Transactions); only `--statistics` is `query run`'s alone. A write needs
`--allow write`; `--transaction rollback` gives a write's real plan without
keeping the change.

- **Refusals.** As `query run`'s, plus `SET STATISTICS XML` anywhere in the
  script, `ON` or `OFF` (in a list too: `SET STATISTICS IO, XML ON`):
  BLOCKED, exit 7, before connecting — the script's `OFF` would lose the
  plans of what follows. Without SHOWPLAN in the database the run fails with
  Msg 262: `PERMISSION_DENIED`, exit 9, with a hint naming the grant (found
  live on 17: the statement does not run).
- **Rows are counted, not returned.** Set `result_sets`, after the format's
  sets: `n`, `rows`, `columns` (the names, comma-separated), one row per set
  the script returned, empty ones included. `query run` is for the rows.
  `ROWS_AFFECTED` and the messages are `query run`'s, less the count the
  server sends for each plan set.
- **Plans.** One per statement that ran a query plan, in the order they ran:
  a trivial `SELECT 1` or a `PRINT` has none (no plan at all: a warning,
  `statementCount` 0). The formats are `plan estimated`'s; on an actual plan
  the actual columns are filled — `statements` `actual_rows`, `elapsed_ms`,
  `cpu_ms`; `operators` and `tree` `actual_rows` (over all executions),
  `elapsed_ms` (the operator's, which in row mode includes its children's)
  and `misestimate`. An operator the server reports no runtime for (a
  Compute Scalar folded into its parent) has them NULL; when it is a
  statement's root, the statement's `actual_rows` are the operator's below
  it, which it passes through unchanged.
- **A failed run** answers the plans the server sent before it stopped, with
  the error; a batch cut by `--timeout` loses its own plans (the server had
  not yet sent them), earlier batches' stay. `--plan-file` is written only
  when the command succeeds.

`plan cached <plan_handle>` reads a plan the server already holds, from
`sys.dm_exec_text_query_plan` (text: `dm_exec_query_plan` fails on a plan
nested over 128 levels): the estimated plan it compiled and keeps reusing, no
runtime figures. The handle and `--statement-start`/`--statement-end` are
`monitor top-queries`' `plan_handle`, `statement_start`, `statement_end`
(byte offsets into the batch; 0 and -1, the defaults, are the whole batch).
`0x` is optional; a handle that is not hex or over 64 bytes, and a bad
offset, are exit 2 before connecting. A plan that has left the cache, or a
statement not compiled yet (deferred compile), is `NOT_FOUND`, exit 8, the
hint saying to rank again, and `--plan-file` is not written. Formats, `--top`
and `--plan-file` are `plan estimated`'s. Needs VIEW SERVER STATE (exit 9).

## Scripting

`script object` and `script db` answer the text Object Explorer's Script As
generates — the same table (`internal/scripting`) and gosmo's default script
options (headers, schema-qualified names, `IF NOT EXISTS` guards,
`ANSI_PADDING`, extended properties) — so the script equals what the TUI's
Script <Object> as ▸ <verb> ▸ File... writes for the same object. Nothing is
run; both are class `read`.

- **Kinds** (`script object`): table, view, procedure (T-SQL or CLR),
  function, DML trigger, sequence, synonym — the objects `object describe`
  resolves that Object Explorer scripts. A constraint, default or other
  child of a table is a usage error, exit 2, whose hint names the table to
  script instead (its CREATE carries it); an object SQL Server ships is exit
  2. Database- and server-level objects (users, roles, schemas, types, logins
  …) are not covered.
- **`--as`** (default `create`): `create`, `alter`, `drop`,
  `drop-and-create`, `select`, `insert`, `update`, `delete`, `execute`, each
  where Object Explorer offers it — every kind `create`, `drop`,
  `drop-and-create`; `alter` a view, procedure, function or trigger;
  `select`/`insert`/`update`/`delete` a table or view; `execute` a procedure;
  `select` a function. A verb the kind lacks is exit 2, the message listing
  the kind's verbs. `script db` takes `create`, `drop`, `drop-and-create`.
- **Output.** The object's script opens with `USE [database]`; a database's
  own script runs in master and has none. In text the script prints after the
  record under `=== SCRIPT (n lines) ===`, line breaks kept and uncut by
  `--max-col-width`; in JSON it is one string.
- **Refusals.** A definition the login cannot read — a module's text, or a
  table's default, check or computed-column expression, all NULL without VIEW
  DEFINITION (SELECT or EXECUTE alone does not give it) — is
  `PERMISSION_DENIED`, exit 9: gosmo refuses rather than script `DEFAULT`
  followed by nothing. A table's DROP names its schema-bound dependents in a
  comment, read from `sys.sql_expression_dependencies`; without VIEW
  DEFINITION that view hides them rather than refusing, so such a login's
  DROP comes without the comment (exit 0; `docs/open-threads.md`). An
  encrypted module has no text to restate: `create`, `alter` and
  `drop-and-create` are `UNSUPPORTED`, exit 11; `drop` works.
- **`--script-file <path>`** also writes the script, as the TUI's File...
  does: the text exactly, UTF-8, LF line ends, replaced atomically. An
  existing file is `USAGE_ERROR` before connecting unless `--force`; nothing
  is written when the script fails. It is not `--out-file`, which is for
  rows.

## Data

`data top`, `data sample`, `data count`, `data profile` and `data export` compose SELECTs
over a table or view and run them on a query session, as `query run` would: cells are the grid's text, JSON
types them, `--max-rows` and `--out-file` apply. They are class `read`; the
login needs SELECT on the object (Msg 229, or 230 for a denied column, is
`PERMISSION_DENIED`, exit 9).
A procedure, function, synonym or other kind is exit 2 (a synonym's hint
names its base object).

- **`data top`** is the TUI's Select Top 1000 Rows: `SELECT TOP (n) *` with
  `--rows` (default 100; 0 returns the columns alone). `--columns a,[b c]`
  lists column names, bracketed where needed; each is matched to the
  object's columns (exactly, else ignoring case when that finds one) and
  sent bracketed in the catalog's spelling, never as written. An unknown
  column is `NOT_FOUND`, exit 8, naming columns that contain it; a column
  named twice, or anything but names and commas, is exit 2. `--rows` above
  `--max-rows` needs `--max-rows 0` or `--out-file` to show every row.
- **`data sample`** is `data top` with `ORDER BY NEWID()`: a uniformly
  random `--rows` of the rows `--where` keeps, different on every call. It
  reads and sorts all of them (a full scan); TABLESAMPLE is not used — it
  samples pages, returns nothing on a small table and refuses a view.
- **`data profile`** is one aggregate SELECT (one per 100 columns) answered
  as set `columns`: `rows` (those `--where` keeps), `nulls`, `null_pct` (two
  decimals, NULL with no rows), `distinct`, `min`, `max`, `min_len`,
  `max_len`. What a column gets follows its type — an alias type's base
  type: numbers, dates and times, strings (`sysname` too), binary, `uniqueidentifier`,
  `sql_variant`, `hierarchyid` and `rowversion` get `distinct`, `min` and
  `max` (`bit` as 0/1, since MIN(bit) is refused); strings also get the
  lengths in characters (`LEN`, trailing spaces not counted), binary in
  bytes (`DATALENGTH`). `xml`, `text`, `ntext`, `image`, `geography`,
  `geometry`, `vector`, CLR types and Always Encrypted columns get `rows`
  and `nulls` alone; what a column does not get is NULL. A graph table's
  internal columns are left out, and cannot be named in `--columns`.
  `--top-values n` adds set `top_values`: each column with `distinct`, its
  `n` most frequent values (NULL counts as one) by count, then value, one
  `SELECT TOP (n) … GROUP BY` per column. The ANSI warning "Null value is
  eliminated by an aggregate" is dropped: counting NULLs is the point.
  `COUNT(DISTINCT)` sorts or hashes every column, so on a large table narrow
  it with `--columns` or `--where`.
- **`data count`** is `SELECT COUNT_BIG(*)`, exact. `--approximate` reads
  the table's partition statistics (no scan, no session) and refuses
  `--where`, and a view (exit 2).
- **`data export`** writes every row to `--out-file`, which it requires
  (exit 2 without, before connecting); the extension is the format, as for
  any command (§ `--out-file`), so `.csv`/`.tsv` are the bytes the TUI's
  Results To File writes for the same rows. Give an object — `SELECT *` of
  it with `data top`'s `--columns`, `--where` and `--order-by` and no TOP —
  or the T-SQL in `--sql`/`--file`/`--sql-base64` (with `--param` as
  `query run`'s), which runs as `query run` held at `read`: a script above
  read is `BLOCKED`, exit 7, before connecting, its hint pointing at `query
  run --allow`; mode options are refused as there; the run stops at the
  first failing batch. Every result set goes to the file. An object together
  with T-SQL, or `--columns`/`--where`/`--order-by` without an object, is
  exit 2. A failed export leaves an existing file as it was.
- **`data import`** loads `--in-file` into a table through bulk copy
  (gosmo's `BulkInsert`, the TDS path bcp uses). It is class `write`
  (`--allow write`, gated before connecting as § Writes); `--truncate-first`
  makes it `ddl`. The file is what `data export` writes, one result set:
  `.csv`/`.tsv` with a header row, read with the writer's NULL convention (an
  empty field NULL, `""` the empty string, a bare `NULL` the string; a UTF-8
  byte order mark before the header is dropped), or gossms's `.jsonl` (the
  `_resultSet` line names the columns and their keys; numbers are taken as
  written, `true`/`false` as a bit; `_summary` ends the rows). `.json` is
  refused: export to `.jsonl`. `--in-format` names the format for `--in-file
  -` (stdin, not with `--password-stdin`) or another extension. A blank line
  followed by more rows (a second set), a row with the wrong field count, or
  a malformed field is exit 2 naming the line.
  - **Columns.** Each file column is matched to the table's by name, exactly,
    else the one match ignoring case; a name the table lacks, or two naming
    one column, is exit 2. Computed, `rowversion` and `GENERATED ALWAYS`
    columns in the file are skipped with one warning, so an export of the
    table loads back into it; the table's columns the file lacks take their
    defaults. Set `columns` lists each file column with `action` `load` or
    `skipped (…)`, then the defaulted columns (`file_column` NULL, `default`).
  - **Conversion.** Each cell is parsed for its column's type (an alias
    type's base type) before it is sent, and checked as the driver does not:
    range, precision and scale (more fractional digits than the column keeps
    is an error, not rounding), length in the column's unit, `char` text
    encoded to the database's code page and refused by name where it or the
    column's code page lacks a character. The first cell that fails is exit
    2, `line n, column [c]: …`. `sql_variant`, CLR types (`geography`,
    `geometry`, `hierarchyid`), `vector` and Always Encrypted columns are
    `UNSUPPORTED`, exit 11, before loading. A one-column CSV writes a NULL as
    `""`, so it loads every `""` as an empty string, with a warning; `.jsonl`
    keeps them apart.
  - **The load** is one batch, all or nothing: a bad cell, a constraint the
    rows break or a cancel leaves the table as it was (`rows` `0`). NULLs stay
    NULL (KEEPNULLS: a column's default does not replace them); an identity
    column in the file keeps the file's values. CHECK and FOREIGN KEY
    constraints are checked unless `--no-check-constraints` (bcp's default,
    which leaves them untrusted); INSERT triggers fire only with
    `--fire-triggers`; `--tablock` takes a bulk-update lock. `--timeout`
    (default 60 s) bounds the load.
  - **Permissions.** INSERT on the table; ALTER as well to keep identity
    values, or to load with `--no-check-constraints` (or without
    `--fire-triggers`) into a table that has constraints (triggers). The
    server answers a missing INSERT, and a missing ALTER for identity values,
    with Msg 1088 ("does not exist or you do not have permissions"); the
    table was just resolved, so it is `PERMISSION_DENIED`, exit 9, as is Msg
    5304 for the unchecked-constraints case (all live on 17).
  - **`--truncate-first`** runs `TRUNCATE TABLE` before the load. It needs
    `--allow ddl`, ALTER on the table (Msg 1088 without, exit 9), and
    `--confirm` with the table exactly as given (`BLOCKED`, exit 7, before
    connecting otherwise). A TRUNCATE cannot share the bulk load's
    transaction, so a load that fails after it leaves the table empty — the
    hint says so.
  - **`--dry-run`** reads and converts the whole file (a bad cell fails it as
    it would the load), counts `rows`, and loads nothing; with
    `--truncate-first`, `tsql` is the TRUNCATE gosmo would send. A bulk load
    itself has no T-SQL to show.
- **Fragments.** `--where` is spliced as `WHERE (<expr>` + line break + `)`,
  `--order-by` (top only) as `ORDER BY <list>` + line break, so a trailing
  `--` comment ends at its line. A fragment that is blank or leaves a
  string, quoted name or block comment open is exit 2. The composed SELECT
  must classify as one read `SELECT` in one batch with no `;` outside a
  literal or comment and no flag (dynamic SQL, a procedure call), else
  `BLOCKED`, exit 7, before connecting — the record names the statements
  found; `data profile` checks each SELECT it sends. The check keeps a
  fragment from adding a statement; what the
  condition reads is the login's permissions' business.

## Monitoring

`monitor` reads what the server is doing — the DMVs behind SSMS's Activity
Monitor and its performance reports, through gosmo — and `monitor kill` ends
a session. Every `monitor` command is `read` except `kill`, which is `admin`.

- **Permissions.** Every command but `querystore` asks for VIEW SERVER STATE
  (2022+: VIEW SERVER PERFORMANCE STATE) before it reads anything, and is
  `PERMISSION_DENIED`, exit 9, without it: the server would answer the
  session DMVs with the login's own session only, an empty list that reads as
  "nothing is running". `plan cached` and `xevent sessions` do the same.
  `querystore` needs VIEW DATABASE STATE in the database; the server's
  refusal is exit 9. `agent context` lists all three, and ALTER ANY
  CONNECTION for `kill`.
- **No helper procedures.** `sessions` and `blocking` come from gosmo's
  `ActiveSessions`, never from `sp_WhoIsActive` or `sp_block`, which the
  TUI's Sessions and Block tabs install: a read command writes nothing, and
  its columns do not depend on what a server has installed.
- **`-d` is the connection.** The filters that name a database are
  `--session-database` (`sessions`) and `--plan-database` (`top-queries`);
  `querystore` reads the connection's database.
- **`sessions`.** User sessions, the CLI's own left out; `--include-system`
  adds the rest. `command`, `wait_*` are the running request's, NULL for an
  idle session; `cpu_ms`, `reads`, `writes`, `logical_reads` the session's
  totals since login, the running request's so far included
  (`sys.dm_exec_sessions` moves them only when a request ends, so alone they
  showed 0 for a query blocked for minutes); `elapsed_ms` how long the
  running request has run, NULL for an idle session. `text` is the running request's batch, or for an idle
  session the batch its connection sent last (what opened an idle session's
  open transaction), its first 512 characters, auto-parameterized when the
  server parameterized it. `blocked_by` counts a parallel request's blocked
  worker, and the `wait_*` columns are then that worker's. `--active`, and
  `--session-database`, `--login`, `--program` (equal, case-insensitive)
  filter.
- **`blocking`.** One `sessions --include-system` read, built into chains in
  gossms: a head blocks and is not blocked; each chain is the head, then the
  sessions it blocks, depth first, by spid at each level; chains by head
  spid. A session blocked by one the read does not show (ended between, or
  `-2`/`-4`) heads its own chain, `blocked_by` kept. A cycle — a deadlock not
  resolved yet — is reported once, from its lowest spid, `cycle` 1 on its
  members only.
- **`waits`.** `sys.dm_os_wait_stats` is cumulative since the instance
  started (or the last `DBCC SQLPERF(… CLEAR)`, which the server does not
  record): the default, `since` the start and `elapsedMs` the uptime.
  `--interval <sec>` (1–300) reads twice and reports the difference, `since`
  the first read's server time; Ctrl+C during it is exit 130 with no second
  read. A wait type new in the second read counts from zero; one whose totals
  fell (cleared between) is left out with a warning. There is no
  `--reset-baseline`: the CLEAR resets every other tool's baseline on the
  server, and `--interval` measures without it. Idle and background waits are
  left out unless `--all` — the TUI's Waits chart's list (`internal/activity`)
  — and so are wait types with no time and no task. `signal_ms` is clamped to
  `wait_ms` (the DMV updates the two apart). `pct` is the share of all wait
  time counted, not only the `--top` rows shown; `categories` sums all eight
  categories over every type counted.
- **`top-queries`.** `sys.dm_exec_query_stats`, one row per cached
  statement, its totals since `cached_at`: a plan leaves the cache at
  eviction, recompile or restart, so this is no fixed window — Query Store
  (`querystore`) is the durable history. Ranked by the total, or with `--avg`
  per execution (not with `--by executions`: exit 2). `--top` is at least 1
  (default 20; there is no 0-for-all). Times are ms with three decimals (the
  DMV keeps µs); handles and `query_hash` are `0x` upper-case hex;
  `database` is NULL when the plan names none the login can see (the hidden
  resource database included). An unknown `--plan-database` is exit 8.
- **`querystore`.** `--report top` (default), `regressed`, `variation`,
  `forced`, `waits`; `--regressed` is `--report regressed` (with another
  `--report`: exit 2). The window is `--since`–`--until`, default the 24
  hours to the server's now: a time with no zone is the server's local wall
  clock, read in the zone of its `SYSDATETIMEOFFSET`; with `Z` or an offset,
  that instant (unlike `server log`: Query Store's times are
  `datetimeoffset`). Every interval that overlaps the window counts whole; an
  empty window is exit 2. `regressed` compares the window's second half with
  its first (the TUI's Regressed Queries); a query with no executions in the
  first half is left out. `--stat` defaults to `total` for `top` and `waits`,
  `avg` otherwise; `--min-regression-pct` is a whole percentage. `waits`
  (2017+) refuses `--metric` and `--min-exec`, and a `--stat` other than
  `avg`/`total` unless `--wait-category` names one; `--metric log-memory` and
  `tempdb` are 2017+. Before 2017 either is `UNSUPPORTED`, exit 11, before
  any read. Query Store OFF or in ERROR, or `master`/`tempdb`, is exit 8 with
  the `db set <db> --option QUERY_STORE=ON` hint; READ_ONLY reads. Plan
  forcing is not offered.
- **`deadlocks`.** The `xml_deadlock_report` events of `system_health`: its
  event_file, the server keeping only those events, else its ring_buffer.
  `--since`/`--until` are **UTC**, as the events record time: a time with no
  zone is UTC, one with `Z` or an offset converted; `--until` is exclusive;
  default the 24 hours to the server's now. The server still parses every
  event in the files to find them, so on a busy `system_health` the read can
  take a minute or more (538 000 events on a 2016 instance: 70–80 s, the
  same through `sqlcmd`). Newest first, `--top` (default 20);
  `deadlocksInWindow` counts before the cut. A parallel plan's workers share
  a spid and differ by `ecid`. A report that does not decode is a warning,
  left out. `--deadlock-file <dir>` writes each graph as
  `deadlock_<yyyymmdd_hhmmss_mmm>.xdl` (UTC; `_2`… for two in one
  millisecond), creating the directory; every name is checked first, and an
  existing file without `--force` is exit 2 with **no** file written.
  `system_health` missing, stopped, or with no file or ring_buffer target is
  exit 8, the hint naming the fix.
- **`kill`.** `KILL <spid>` through gosmo, so `--dry-run` prints it.
  Before connecting: a spid outside 1–32767 is exit 2; `--confirm` must
  repeat the spid exactly as given (`087` is not `87`), else `BLOCKED`,
  exit 7; then the `admin` gate (§ Writes). Connected: VIEW SERVER STATE is
  asked first (exit 9, nothing sent: without it every other session looks
  ended), then the session is read and shown as set `session` in every later
  outcome. Gone is exit 8; a system session exit 7; **any** of this
  process's connections (client pid and host, not only `@@SPID`: the KILL
  could travel on another pooled one) exit 2. The server's refusals: Msg 6106
  (ended in between) → 8, 6107 → 7, 6104 → 2, 6102 → 9 with an ALTER ANY
  CONNECTION hint. After a real KILL the session is read again: `ended` is
  true when it is gone or its spid now belongs to another login or process;
  false while it rolls back, a warning naming `KILL <spid> WITH STATUSONLY`,
  not waited for.

## Extended Events

- **`xevent sessions`** lists the server's event sessions
  (`sys.server_event_sessions` with `sys.dm_xe_sessions`); `started` (server
  local time) and `dropped_events` are a running session's, NULL for a
  stopped one; `file` is the event_file target's current `.xel`, else its
  `filename`. Engine-private sessions with no definition
  (`hkenginexesession`, `sp_server_diagnostics session`) are not listed. On
  Azure SQL Database it lists the database's sessions, `scope` `database`,
  and needs no VIEW SERVER STATE.
- **`xevent capture` is `admin`**: it creates a server object, and one left
  behind by a killed process is a lasting change. It needs ALTER ANY EVENT
  SESSION — or, from 2022, CREATE, ALTER … ENABLE, ALTER … DISABLE and DROP
  ANY EVENT SESSION, all four — and VIEW SERVER STATE (the buffer is read from
  the DMVs); a known lack is exit 9 with nothing created. Azure SQL Database
  is exit 11.
- **One short-lived session per run**, `gossms_cli_<UTC
  yyyymmddhhmmss>_<4 hex>`: the template's events, one ring_buffer target with
  `max_events_limit` = `--max-events` (1–100000, default 1000: the buffer keeps
  the newest), `MAX_DISPATCH_LATENCY = 1 s`, `STARTUP_STATE = OFF`. Create →
  start → wait `--duration` (1–600, default 30) + 1 s → read → stop and drop,
  **on every exit path**: a failed step, a CREATE that errored but left the
  session (looked up by name), and Ctrl+C (exit 130 after the drop), the
  cleanup under its own 15 s context. A drop that fails is a warning naming
  the session and the `DROP EVENT SESSION` to run; `sessionDropped` says
  which. `xevent sessions` lists any `gossms_cli_` session a killed process
  left.
- **Templates**, gosmo's SSMS ones by name: `standard`, `tsql`, `batches`
  (Query Batch Tracking), `statements` (Query Detail Tracking), `waits`
  (Query Wait Statistics; the CLI adds `session_id > 0`, which the template
  leaves out, so background waits stay out), `connections`, `deadlocks`; and
  `errors` (`error_reported`, severity ≥ 11), defined in gossms. Count Query
  Locks is not offered (a histogram target).
- **Filters are the server's**, ANDed into every event's predicate:
  `client_app_name <> 'goSSMS - CLI'` always (this tool's own events),
  `--session-database`, `--login`, `--program` (exact names), and
  `--min-duration-ms` on the completed batch, RPC, statement and module
  events only. The actions that fill the columns are added to every event.
- **Events**, oldest first (by time, then `event_sequence`): durations and
  CPU µs → ms with three decimals (`wait_info`'s duration is already ms);
  `text` is the batch, statement, message, deadlock graph, `sql_text`, else
  the wait type. Under `-o jsonl` each row adds `data`, `{"fields":{…},
  "actions":{…}}`, a column of type `json` (§ `json`). Fewer kept than
  `eventsSeen` (the target's processed count), or a truncated ring buffer
  (about 4 MB), is a warning: shorten `--duration` or filter tighter.
- **`--dry-run`** reads and checks as a run does, then prints the CREATE,
  START, STOP and DROP as `tsql`; nothing is created.

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
on the next line, which the statement splitter merges, is `ddl`; so is a
merged `UPDATE STATISTICS`, never `UPDATE`'s `write`). It is a
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

## Writes

The commands that change a server through gosmo — `server config
set/reset`, `server traceflag on/off`, `db set`, `db scoped-config set`,
`db create`, `db drop`, `data import`, `monitor kill` and `xevent capture`
— share one layer
(`internal/cli/cmd/write.go`). Each is class `admin`, except `data import`
(`write`, `ddl` with `--truncate-first`). `data import` answers a load
count, `monitor kill` the session and whether it ended, `xevent capture` the
events it caught, `db create` the database read back and `db drop` the
database as it stood — not a before/after change set (§ Commands, § Data,
§ Monitoring, § Extended Events).

- **The gate.** `--allow` and `--dry-run` are options of the writing
  commands and `query run`, not global ones: a read command does not take
  them. Before connecting, the command's class is checked against `--allow`
  (default `read`) capped by the profile's `max_allow`, with `query run`'s
  wording: a refusal is `BLOCKED`, exit 7, with `detectedClass` and
  `allowedClass` and a hint naming the `--allow` that would do, or the
  ceiling that refuses. Nothing is dialed. A dry run is gated too, so
  `--dry-run` still needs `--allow admin`.
- **Dry run.** `--dry-run` connects and reads what the command checks, then
  runs the write under gosmo's `WithScript`, which captures the statements
  instead of sending them. The status stays `OK`; the record gains `dryRun:
  "true"` and `tsql`, gosmo's exact batch (verbatim in text), and the set is
  `planned_change`. Every writing run carries `dryRun`, `"false"` on a real
  one, after the command's own fields. That `tsql` is what the real run then
  sends, statement for statement: `TestDryRunFidelity`
  (`internal/cli/cmd/dryrun_fidelity_test.go`) runs each writer both ways over
  its fakes and compares the dry run's `tsql` with the script of the real
  run's writes (and a bulk load's row count with `rows`). A new writing
  command adds a case to `fidelityCases` — `TestDryRunFidelityCoversEveryWriter`
  fails on a command with `--dry-run` and none — and a new `Writer` method
  does not compile until `fidelityWriter` shadows it. `query run` and `plan
  actual` are exempt: their `--dry-run` classifies and prints no T-SQL.
- **Before and after.** A real run reads the state again after writing and
  reports a `change` set — `before`, `requested`, `after` — so the answer is
  the server's, not the request echoed. An `after` that is not the requested
  value is a warning.
- **Failures.** A write the server refuses is exit 5 (`SQL_ERROR`) with the
  `change` set still read back, so what landed shows; the first failure stops
  a multi-step run. A refusal for permission is `PERMISSION_DENIED`, exit 9,
  with a hint naming the permission — Msg 15247 included, which gosmo reads
  as "missing or denied" (`NOT_FOUND` for a read): a writing command has just
  read the name, so each maps it to `PERMISSION_DENIED` itself (`server config
  set` once did not, and answered exit 8). A refused `ALTER DATABASE` reports the
  error that says why, not the contentless Msg 5069 that ends it. A value
  gosmo refuses before sending is exit 2.
- **Flags that lift a check** are per command and lift only gossms's own
  checks, never the server's:
  - `--accept-risk` (`server config set/reset`): past a blocked value
    (§ Server configuration).
  - `--override` (`server config set/reset`): `RECONFIGURE WITH OVERRIDE`.
  - `--confirm <name>` (`db set`, with `--rollback-immediate`): the database's
    name exactly as given. `--rollback-immediate` rolls back and disconnects
    every other session in the database, so without a matching `--confirm`
    it is `BLOCKED`, exit 7, before connecting.
  - `--confirm <table>` (`data import`, with `--truncate-first`): the table
    exactly as given, likewise.
  - `--confirm <spid>` (`monitor kill`, always): the spid exactly as given.
  - `--confirm <name>` (`db drop`, always): the database's name exactly as
    given. `--close-connections` does not stand in for it.

  The global `--force` keeps its one meaning: replace an existing file the
  command writes — `--out-file`, `--plan-file`, `--script-file`, or the
  files `--deadlock-file` writes.
- **Permissions.** `agent context` lists ALTER SETTINGS, sysadmin, ALTER,
  ALTER ANY DATABASE SCOPED CONFIGURATION, ALTER ANY CONNECTION and ALTER ANY
  EVENT SESSION (met by 2022's four narrow rights together) when the login is
  known to lack them, with the commands each breaks.

## Audit log

Every call that reaches a class above `read`, and every refusal (`BLOCKED`),
appends one JSON line to `audit.jsonl` beside `config.json`
(`os.UserConfigDir()/gossms/`), created owner-only (`0600`). Reads are not
logged. The log is always on: no option, environment variable or profile
field turns it off (`internal/cli/audit`).

```json
{"ts":"2026-10-30T09:12:44.031Z","profile":"dev","server":"devsrv,1500","database":"app","login":"agent","command":"query run","class":"write","dryRun":false,"args":["--sql=sha256:5f1c…","--profile=dev","--allow=write"],"sqlSha256":"5f1c…","outcome":"ok","exit":0}
```

- `class` is what the call reached: the script's class for `query run` and
  `plan actual`, the command's for a writing command; for a refusal, the
  class detected. `outcome` is the envelope's status, `exit` the exit code.
- `server`, `database` and `login` are the connection's, read without
  connecting, so a refusal has them too: `login` is the login name, or
  `(integrated)` / `(entra)` for a method without one.
- `args` is the command line rebuilt from the parse — positional arguments,
  then each option given (on the command line or by its environment
  variable) as `--name=value` — so a response file or a short alias records
  alike. The options that carry T-SQL or data — `--sql`, `--sql-base64`,
  `--param`, `--where` — are recorded as `sha256:<hex>` of their value;
  `sqlSha256` is the hash of the script as read (the file's text for
  `--file`, whose path is recorded as given). A password never reaches the
  command line (`--password-env` names a variable; `--password-stdin` reads
  stdin), so it never reaches the log.
- Lines are appended under the file's lock (`config.json`'s `WithLock`), so
  calls running at once never interleave.
- **A change the log cannot record is not made.** Before a call above
  `read` connects — after its `--allow` check, its dry run included — the
  log is opened for appending; if that fails the call is refused with
  `GENERAL_ERROR`, exit 1, naming the reason. Reads still run. Should the
  append itself then fail, the result carries a warning.
- The log is never rotated or trimmed by gossms.

## The import rule

`internal/cli` never imports `internal/tui`, a `tuikit` package other than
the stdlib-only `tuikit/sqltext`, or tcell: the command line runs without a
screen. See `ARCHITECTURE.md` § Why split this way.
