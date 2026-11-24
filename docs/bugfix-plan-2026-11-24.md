# Bug-fix plan, 2026-11-24 (command-line guardrails)

Three defects in the command line's safety net, found reading gossms, gosmo
and tuikit and each **reproduced live on win10cli (17.0) with the built
binary**, with disposable objects (tempdb tables and procedure, a throwaway
login), all dropped afterwards. None is in `docs/open-threads.md` or
`docs/decisions.md`. All three are in gossms; gosmo and tuikit need no change.
Close an item by deleting it; delete this file with the last one. Nobody
commits but the user.

| # | Where | Effect | Severity |
|---|---|---|---|
| C1 | `internal/cli/safety/classify.go` (`floor`, flags), `check.go` | `--max-rows-affected` commits a `TRUNCATE TABLE` of any size | Data loss behind a guard that says it holds |
| C3 | `internal/cli/cmd/param.go` (`parseParam`) | `--param d=2026-01-02:datetime` reaches the server as 1 Feb for a login whose language is `dmy` (British, German, French, …) | Silent wrong value |

Each step: `go build -o gossms ./cmd/gossms && go test ./internal/cli/... &&
gofmt -l . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary with the item's repro
(`docs/testing.md`). Update `docs/cli.md` in the same change as the code.

## Recommended implementation order

1. **C1** — the most severe (rows deleted for good under a flag sold as the
   safe way to run a write), and it adds the flag-raising pattern C2 reuses.
2. **C3** — independent (`param.go`); last because its live check needs a
   throwaway login with `DEFAULT_LANGUAGE = British`.

---

## C1 — `--max-rows-affected` lets `TRUNCATE TABLE` through

**Cause.** The limit trusts the server's row counts, and `Check` refuses only
the statements whose counts it knows to be blind (`FlagNoCount | FlagDynamic
| FlagExec | FlagOutput | FlagUncounted`, `check.go:88`). `TRUNCATE TABLE`
reports **no** count at all, so `rowsAffected` stays 0, which is under any
limit, and the wrapper commits. `DROP TABLE` and `ALTER TABLE … SWITCH`
(which moves every row of a table or partition to another) have the same
shape: rows gone, nothing counted. With `--allow ddl` (which `TRUNCATE`
needs), nothing else stops them.

**Repro (live, verified):** a tempdb table of 1000 rows.

```sh
gossms query run -d tempdb --allow ddl --max-rows-affected 10 -Q "TRUNCATE TABLE dbo.t"
# STATUS: OK, TRANSACTION: committed, no ROWS_AFFECTED — table now empty
gossms query run -d tempdb --allow ddl --max-rows-affected 10 -Q "DELETE FROM dbo.t"
# STATUS: BLOCKED, ROWS_AFFECTED: 1000, rolled-back — table still 1000 rows
```

**Fix.** Raise `FlagUncounted` (it is already in the blind set and its
refusal message already says the server does not count these) for:

- `TRUNCATE TABLE`, in `floor` at the verb (so one nested behind `IF` or merged
  into another statement is caught, not just the leader in `leaderRule`);
- `DROP TABLE` (`createAlterDrop` / the floor's `CREATE|ALTER|DROP` arm, when
  the object word is `TABLE`);
- `ALTER TABLE … SWITCH` (a top-level `SWITCH` word inside an `ALTER TABLE`).

Extend `FlagUncounted`'s doc comment (`classify.go:49`), the `uncounted` list
in `Check`'s message (`check.go:91`) and `docs/cli.md` § Transactions (the
`--max-rows-affected` bullet naming `RECEIVE`, `WRITETEXT`, `UPDATETEXT`).

*Alternative, broader:* refuse every `ddl`/`admin` statement under
`--max-rows-affected`. Simpler and future-proof, but it also refuses
`SELECT … INTO` and `CREATE INDEX`, which are counted or harmless. Recommended:
the targeted flag; ask the user if the broad rule is preferred.

**Tests** (`internal/cli/safety/classify_test.go`, `check_test.go`):
`TRUNCATE TABLE t`, `IF 1=1 TRUNCATE TABLE t`, `DROP TABLE t`,
`DROP TABLE IF EXISTS a, b`, `ALTER TABLE t SWITCH PARTITION 2 TO t2 PARTITION
2` each carry `uncounted` and are refused under `MaxRowsAffected`;
`ALTER TABLE t ADD c int`, `DROP VIEW v`, `TRUNCATE` as a column name
(`SELECT [truncate] FROM t`) do not. Live: the repro above answers BLOCKED,
exit 7, before connecting, and the table keeps its rows.

---

## C3 — `--param` date values depend on the login's language; `bit` coerced

**Cause.** `parseParam` sends every non-binary value as `N'…'` and lets the
server convert it to the declared type (`param.go`, the `p.value =
gosmo.QuoteLiteral(value)` line). For `datetime` and `smalldatetime`,
`YYYY-MM-DD` is read under the session's DATEFORMAT: a login whose default
language is `dmy` (British, German, French, Italian, …) gets `ydm`, so
`2026-01-02` is **1 February**. `date`, `datetime2` and `datetimeoffset` read
`YYYY-MM-DD` correctly, but any non-ISO form (`01/02/2026`) swaps on them too.
No error is raised, so the docs' promise that "a value that would change is
refused before connecting" (`docs/cli.md`, the `--param` bullets) does not
hold. Smaller, same promise: a `bit` value other than 0/1 (`2`, `-1`) becomes 1
silently. gosmo already avoids the trap everywhere it writes a date as text
(`YYYYMMDD`, or ISO 8601 with the `T`; see `timeLiteral` in gosmo's
`script.go`); this is the one path that does not.

**Repro (live, verified):** throwaway login with `DEFAULT_LANGUAGE = British`.

```sh
gossms query run -U probe_british ... -Q "SELECT @d AS d" --param d=2026-01-02:datetime
# d = 2026-02-01 00:00:00.000
```

**Fix.** For `date`, `datetime`, `smalldatetime`, `datetime2` and
`datetimeoffset`, accept ISO 8601 only — `YYYY-MM-DD`, optionally `[T or
space]hh:mm[:ss[.fffffff]]`, and for `datetimeoffset` an optional `Z` or
`±hh:mm` — and refuse anything else (USAGE_ERROR, hint "write the date as
YYYY-MM-DD[Thh:mm:ss]"). Send what was accepted in a form every
DATEFORMAT/LANGUAGE reads the same: `YYYYMMDD` for a date alone,
`YYYY-MM-DDThh:mm:ss[.f…]` with a time (seconds filled in). Leave the
server to refuse what does not fit the type (more than three decimals for
`datetime`, a year before 1753), which is a refusal, not a silent change.
For `bit`, accept `0`, `1`, `true`, `false` (any case) and refuse the rest.
Update the `--param` bullets in `docs/cli.md` (accepted forms, and drop
nothing from the "still round" list: `money`, `float`, `real`, `time`).

**Tests** (`internal/cli/cmd`, beside the existing `--param` tests): the
normalised literal for each date type with and without a time and fraction;
refusals for `01/02/2026`, `2026-1-2`, `2026-01-02 25:00`; `datetimeoffset`
with `Z` and `+02:00`; `bit` `1`/`TRUE` accepted, `2` refused. Live: the repro
with the British login returns 2026-01-02 for `datetime` and
`smalldatetime`; drop the login afterwards.

---

## Reviewed and clean

Probed live where the claim was a server behaviour:

- gosmo: partition-boundary and extended-property literal styles (style 126
  is honoured on a `sql_variant`), `MASKED WITH (FUNCTION = '…')` keeps
  Unicode without `N`, `tsql.CommentSafe` (a `--` comment ends only at CR/LF,
  not U+2028/U+2029/NEL/VT/FF), index/columnstore/spatial/XML index
  scripting, `buildBackupStatement`, `SetDatabaseOption`, `sp_rename`
  wrappers, `likeEscape`/`ObjectFilter.clause`, Agent history time
  conversion, `killDatabaseSessionsBatch`, `SplitName`/`UnquoteName`.
- gossms: statements after a `CREATE VIEW`/inline function body are refused
  by the server (the classifier's module rule holds), the read allow-listed
  system procedures write nothing (only `sp_spaceused @updateusage`, already
  handled), nested `DBCC` is cut and classified admin, `textenc`,
  `fileutil` atomic writes, `export` JSON/CSV writer and reader, `sqltext`
  GO separator and statement splitter, `query` cell rendering (json/vector
  columns arrive as text), `db` flights, `activity` counter math,
  `config` address keys, `format` token guard, showplan missing-index script.
- tuikit: editor undo/redo snapshots and line actions, `Document` caches,
  `InputField`, `Buffer` wide-glyph writes and `Blit`, file-dialog path rules,
  `TypedConfirmDialog`, DataGrid preserve paths, menu scroll clamps,
  zero-width control/bidi characters dropped when drawing.
