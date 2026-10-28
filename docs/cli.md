# Command line

The command-line contract: how gossms is invoked without its interface, how it
parses arguments, what it writes and how it exits. Agents and scripts depend on
every rule here, so a change to one is a change to this file in the same
commit.

**This document grows per phase.** It states only what is implemented; the
command tree, output formats and safety model arrive with the phases that build
them. Today (phase 1) the commands are `version`, `help` and `profile
list/show/test`, the formats `text` and `json`; `profile test` is the only
command that connects to a server.

Code: `cmd/gossms/main.go` (mode switch), `internal/cli` (`Main`, help),
`internal/cli/spec` (the command tree as data), `internal/cli/args` (the
parser), `internal/cli/output` (envelope, renderers, exit codes),
`internal/cli/connect` (connection options and resolution),
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
| `--output <text\|json>` | `-o` | Output format (default `text`). The list grows only as renderers are implemented — a format is never advertised before it works |
| `--quiet` | `-q` | Text only: leave out the header block. A JSON envelope is always whole |
| `--verbose` | | Diagnostic logging to stderr. No short alias: `-v` is `--version` |
| `--no-color` | | Accepted; nothing is coloured yet |
| `--help` | `-h` | The command's help (text) |

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
database, duration), then **either** a record (`data`, for answers such as
`version`) **or** result sets, then messages, warnings and errors.

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

- `SERVER` and `DATABASE` appear only when set; `RESULT_SETS: n` only when
  there are sets.
- A record prints as `KEY: value` lines; the key is the JSON key in
  `UPPER_SNAKE` (`cliSchema` → `CLI_SCHEMA`).
- A result set is `=== RESULT n (r rows, truncated: no) ===` (or `r of t rows,
  truncated: yes`), a header row, a `-+-` rule and the rows, aligned by
  display width; the last column is unpadded. NULL prints `NULL`.
- Then `=== MESSAGES ===` and `=== WARNINGS ===` when non-empty, and each error
  as an `ERROR_CODE:` / `MESSAGE:` / `HINT:` block.
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
- An error is `{"code", "message", "hint"}`; a message `{"type", "text"}`; a
  warning a string.
- A result set is `{"columns": [{"name", "type", "nullable"}], "rows",
  "rowCount", "totalRowCount", "truncated", "rowsAffected"}`; a cell is its
  grid text, or `null`.
- Two-space indent, no HTML escaping, one trailing newline.
- `schemaVersion` (also `gossms version`'s `cliSchema`) changes only when the
  structure breaks compatibility.

A usage error is rendered in JSON when the raw command line asks for
`-o json` / `--output json`, although parsing failed.

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
today's commands produce 0, 1, 2, 3, 4, 6, 8 and 130. The table is `output.ExitCodes`, the one
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

No command ever prints a password or secret.

## The import rule

`internal/cli` never imports `internal/tui`, a `tuikit` package other than
the stdlib-only `tuikit/sqltext`, or tcell: the command line runs without a
screen. See `ARCHITECTURE.md` § Why split this way.
