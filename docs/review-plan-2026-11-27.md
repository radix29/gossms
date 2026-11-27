# Review plan, 2026-11-27

A review of gossms, `~/go/gosmo` and `~/go/tuikit` at gossms `fd264cc`, gosmo
`aa3f197`, tuikit `d381914`. Three bugs, all in gossms; B1 (W1) and B2 (W2)
are done. B1 needed one gosmo change (`tsql.Spans` now skips a number, so
`1e--5` is code, as on the server). tuikit needs none. Close an item by deleting
it, and delete this file with the last one.

**Method.** I ran `nilness`, `unusedwrite` and `shadow` (x/tools analyzers,
built in a scratch module) over all three repos. I wrote property probes
(scratch module, outside the trees) for tuikit's `core` string, wrap and
grapheme helpers. I sent about 130 unusual statements through
`safety.Classify`. Where the code and SQL Server might disagree (lexing,
comment ends, `sql_variant` text conversion, `sp_verify_schedule`'s end
time), I checked against win10cli with read-only queries and session temp
tables only. B2 was reproduced with the built binary. I read the code for
the last three days of commits, plus restore planning, bulk value
conversion, cell parsing, the executor's `PinnedConn`, `fileutil`, `textenc`,
`--param`, the write-target check, Agent schedules, partition boundaries,
the editor's search, actions and undo, `InputField`, the DataGrid keyed
column map, charts scale math and Activity Monitor counter deltas.

**What came out clean.** nilness and unusedwrite found nothing. Every shadow
hit is harmless (each shadowed value is returned or used as intended). Line
comments end only at CR or LF on the server (U+2028, U+2029, U+0085, FF and
VT do not end one), as the lexer has it. The `sql_variant` → text styles in
`partitionFunctionSelect` round-trip on 17. `sp_verify_schedule` maps an end
time of 0 to 235959, as gosmo's doc says. The tuikit probes found only
documented behaviour: past-end virtual columns, zero-width clusters, and a
wide character in a 1-column wrap.

Severity is ordered by what a user can lose.

## Recommended implementation order

1. **W3, B3:** `--max-rows-affected`'s target check compares names the way
   the server does.
2. **W4:** write anything left undone into `docs/open-threads.md` (see the
   TUI editor question below), then delete this file.

Do each step the same way. Run
`gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`. Then drive the built
binary (`docs/testing.md`). Nobody commits but the user.

---

## Bugs

### Left from B2

**Open question for you (left from B2, which W2 fixed for the command
line):** the TUI rewrites a script's line breaks by design. The tuikit
editor folds line ends on load, so F5 sends LF where the file has CRLF,
while SSMS sends CRLF. `docs/decisions.md` mentions the fold
only as a completion cost. Should the editor keep each line's ending, or
should execute rebuild the file's EOL (`textenc.WithEOL` with the panel's
detected EOL) before sending? The second is a small gossms change, the
first a tuikit one. If neither, record it in `docs/decisions.md` as by
design.

A related note for W4: W2 counts `Batch.StartLine` as an editor shows lines
(CRLF, lone CR, LF each one), but the server numbers an error's line by LF
only (on 17, `SELECT 1<CR>SELECT 1/0` fails on line 1). The two agree on any
LF or CRLF script; inside a batch of a CR-only file, an error names the
batch's first line. Recorded in `split.go`; probably by design.

### B3: gossms: `--max-rows-affected`'s trigger and foreign-key check compares names without the collation, so a case-variant name skips the check

**Where:** `internal/cli/cmd/query_writes.go`, in three places:

- `targetCheck.write`, line 97: the `seen` cache key is
  `strings.ToLower(strings.Join(w.Parts, …))`.
- `targetCheck.database`, line 79: the `dbs` cache key is
  `strings.ToLower(name)`.
- `checkWriteTargets`, lines 49–53: the run's databases are de-duplicated
  with `strings.EqualFold`.

**What:** the check refuses a write whose target has an enabled DML trigger
or a cascading foreign key, because the server's row count leaves those
rows out. It reads each target once and caches the verdict. Two cases go
wrong:

- **In a case-sensitive database** (a `_CS_` contained database exists on
  13, 14 and 17 here), the script is `UPDATE t SET …; UPDATE T SET …`, where
  only `T` has a trigger. The second write hits `t`'s cached empty verdict
  and passes. `T`'s trigger then changes rows the limit never counts. Found
  by reading the code; not run, because it needs disposable objects.
- **On a case-sensitive server** with databases `Sales` and `sales`: the
  run's database `sales` and the script's `USE Sales` fold into one, so
  writes after the USE are checked against `sales` only. The database cache
  can also hand one database back for the other.

Everywhere else the command line compares server-scoped names under the
server collation (`gosmo.SameName` with `serverCollation(srv)`: `db drop`,
`db create`, `security permissions`). This check is the one that does not.

**Fix:**

- Key `seen` on the parts as written (exact). A case-insensitive database
  then reads the same table twice when a script spells it two ways, which is
  harmless.
- Better still, key the verdict on the resolved object (database name plus
  `object_id`) after `objectsNamed`. That shares reads across spellings in
  a case-insensitive database too.
- Key `dbs` on `gosmo.NameKey(serverCollation(srv), name)`.
- De-duplicate `runDBs` with `gosmo.SameName(serverCollation(srv), …)`.

**Tests:** pin the offline cases with a fake driver, as
`TestDBDropCaseSensitive` does:

- A `Latin1_General_CS_AS` server where `USE Sales` and run database
  `sales` are both checked.
- A `_CS_` database where `T` (with a trigger) and `t` (without) both get
  checked and the script is refused.

**Verify (live, disposable objects, with your go-ahead):** on win10cli's
contained `_CS_` database, create tables `t` and `T` and an AFTER UPDATE
trigger on `T`. Run
`query run --allow write --max-rows-affected 10 --sql "UPDATE t SET a=1; UPDATE T SET a=1"`.
Expect BLOCKED, naming `T`'s trigger. Then drop all three. The
server-collation half waits on a CS server (`docs/open-threads.md` §
Environment). Add this check to that list there.
