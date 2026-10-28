# Command line

The command-line contract: how gossms is invoked without its interface, how it
parses arguments, what it writes and how it exits. Agents and scripts depend on
every rule here, so a change to one is a change to this file in the same
commit.

**This document grows per phase.** It states only what is implemented; the
command tree, output formats and safety model arrive with the phases that build
them. Today (phase 2, in progress) the commands are `version`, `help`, `profile
list/show/test` and `query classify`; the formats `text`, `json`, `jsonl`,
`csv`, `tsv` and `markdown`; `profile test` is the only command that connects
to a server.

Code: `cmd/gossms/main.go` (mode switch), `internal/cli` (`Main`, help),
`internal/cli/spec` (the command tree as data), `internal/cli/args` (the
parser), `internal/cli/output` (envelope, renderers, exit codes),
`internal/cli/connect` (connection options and resolution),
`internal/cli/safety` (the T-SQL classifier and its check),
`internal/cli/cmd` (the handlers). The command tree itself is
`internal/cli/tree.go`.

## Modes

| Invocation | Behaviour |
|---|---|
| `gossms` (no argument at all) | Starts the interactive UI — but only with **both stdin and stdout on a terminal**. Otherwise it exits `1` at once, with one stderr line pointing at `gossms help` |
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
gossms help [<group>] [<command>]
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
- A list option may repeat and/or take comma-separated items.
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

`gossms --help`, `gossms help`, `gossms help <group> [<command>]` and
`<command> --help` print plain text for people, outside the envelope, exit `0`.
All of it is generated from the command tree: usage line, description,
arguments, options, global options, examples.

The first line of the top-level help is the pointer for AI agents. It names
`gossms help` until `gossms agent guide` exists; `TestCommandMentionsParse`
fails on any `gossms …` named in a hint, help text, example or message that the
tree cannot parse, so the wording has to change when the command does.

Help as JSON will be the command schema (`agent schema`). Until that exists,
help with `--output json` is refused (`USAGE_ERROR`) rather than emitting a
partial catalog an agent would trust.

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

A command that connects takes these options (today: `profile test`). The
precedence is **command line > environment variable > profile > default**.

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
  `UPPER_SNAKE` (`cliSchema` → `CLI_SCHEMA`).
- A result set is `=== RESULT n (r rows, truncated: no) ===` (or `r of t rows,
  truncated: yes`), a header row, a `-+-` rule and the rows, aligned by
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
  the command's fixed order.
- `rowsAffected` is the sum of the DML row counts the server reported, `null`
  when it reported none; `outFile` the `--out-file` written, else `null`.
- An error is `{"code", "message", "hint"}`; a message `{"type", "text"}`; a
  warning a string. An error with a SQL Server error behind it is `{"code",
  "sqlErrorNumber", "severity", "state", "line", "procedure", "message",
  "hint"}` (`procedure` `null` outside a module).
- A result set is `{"columns": [{"name", "type", "nullable"}], "rows",
  "rowCount", "totalRowCount", "truncated", "truncatedBy", "rowsAffected"}`.
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
{"_resultSet":{"index":1,"columns":[{"name":"id","key":"id","type":"int","nullable":false}]}}
{"id":1}
{"id":2}
{"_summary":{"schemaVersion":"1.0","status":"ok",…,"resultSets":[{"columns":[…],"rowCount":2,"totalRowCount":2,"truncated":false,"truncatedBy":null,"rowsAffected":null}],…}}
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
today's commands produce 0, 1, 2, 3, 4, 6, 7, 8 and 130. The table is `output.ExitCodes`, the one
place a code's number is spelled.

**Ctrl+C** (also on Windows) cancels the running command's context: the result
is `CANCELLED`, exit `130`, whatever error the command itself returned. A
second Ctrl+C kills the process.

## Commands

| Command | Answer |
|---|---|
| `version` | Record: `version`, `commit`, `built`, `platform`, `license`, `gosmo` — the fields `gossms --version` prints — plus `cliSchema` |
| `help [<group>] [<command>]` | Help text (§ Help) |
| `profile list [--all]` | Set: `name`, `kind` (`profile`, or `recent` with `--all`), `server`, `port` (NULL when not set), `database`, `auth` (the `--auth` spelling), `user`, `max_allow` (NULL for a recent connection), `password` (`saved`, `none`, `unreadable`). Profiles sorted by name, then recent connections most recent first. An empty list carries a message saying how to make a profile |
| `profile show <name>` | Record: `name`, `kind`, `generatedName`, `server`, `port`, `database`, `auth`, `user`, `tenantId`, `clientId`, `encrypt`, `trustServerCertificate`, `hostNameInCertificate`, `extraProperties` (keys only — a value may be a secret), `maxAllow`, `password`. A password that cannot be opened adds a warning |
| `profile test <name> [connection options]` | Connects (§ Connecting). Header `SERVER`/`DATABASE` name the target, also on failure. Record: `profile`, `serverName`, `productVersion`, `productLevel`, `edition`, `platform`, `login`, `connectedDatabase` (the header's `DATABASE` is the one asked for), `auth`, `passwordSource` (`profile`, `env:<VAR>`, `stdin`, `none`), `maxAllow`, `latencyMs` (the whole connect). A dropped saved password adds a warning |

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

## Safety

`internal/cli/safety` puts every statement into an operation class — `read`,
`write`, `ddl`, `admin` — by its leading keywords over gossms's one lexer
(`tuikit/sqltext`), so a keyword in a comment, literal or quoted identifier
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
   line runs itself.

A refusal is `STATUS: BLOCKED`, `ERROR_CODE: BLOCKED`, exit 7, with
`DETECTED_CLASS`, `ALLOWED_CLASS` and `STATEMENTS` among the record's fields
and a hint naming the `--allow` that would do — or saying that the profile's
ceiling is what refuses.

## The import rule

`internal/cli` never imports `internal/tui`, a `tuikit` package other than
the stdlib-only `tuikit/sqltext`, or tcell: the command line runs without a
screen. See `ARCHITECTURE.md` § Why split this way.
