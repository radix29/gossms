# Review plan, 2026-11-21

Five bugs found by reading `~/go/tuikit` and gossms and by driving the built
binary read-only against `win10cli` (SQL Server 17). Nothing is fixed yet.
Close an item by deleting it; delete this file with the last one.

**Scope note.** tuikit came out clean: `go test -race -count=1 ./...` passes,
and reading `core/strutil.go`, `widgets/input_field.go`, `widgets/dropdown.go`,
`controls/treemodel.go`, `controls/datagrid_selection.go` and the editor's line
actions found nothing worth a fix. All five bugs are in gossms or in gosmo,
which gossms drives. Each was reproduced live unless noted.

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

1. **B2 + B3** — `--param` silently changes the value (one file, one commit).
2. **B1** — system objects cannot be addressed (gosmo first, then gossms).
3. **B4** — `query parse` repeats `GO n`.
4. **B5** — `data profile` reads whole LOBs.

B2 and B3 come first because they can aim a `--allow write` statement at the
wrong rows. B1 is the only one that crosses repositories, so it goes before the
two cheap CLI items to leave time for the version sweep. B4 and B5 are
efficiency and cost no correctness, so they go last.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## B2 — an empty `--param` value becomes 0, or 1900-01-01

**Where:** `internal/cli/cmd/param.go`, `parseParam`.

**Repro (live):** `query run --sql "SELECT @p" --param p=:int` returns `0`;
`p=:date` would return `1900-01-01`. The value is sent as `N''` and the server
converts the empty string. A shell variable that expanded to nothing
(`--param id=$ID:int`, `ID` unset) therefore runs with `id = 0`, and
`docs/cli.md` § Parameters says there is no way to pass NULL, so the caller has
no way to say what they meant.

**Fix:**

- In `parseParam`, refuse an empty (or whitespace-only) value for every type
  except `char varchar nchar nvarchar sysname xml sql_variant`, as a
  `USAGE_ERROR`: `--param "p=": an empty value is not an int`, hint "Give the
  value, or use a string type for an empty string."
- Binary already refuses empty (`isHexLiteral` needs `0x`); leave it.
- Do not add NULL syntax here; `docs/decisions.md` governs that and the
  script can write `NULL` itself.

**Tests:** `param_test.go` — empty value refused for `int date decimal(5,1) bit
uniqueidentifier datetime2(3)`, allowed for `nvarchar(10)` and `varchar(max)`.
**Docs:** one sentence in `docs/cli.md` § Parameters.

## B3 — an over-long `--param` value is silently truncated

**Where:** `internal/cli/cmd/param.go`, `parseParam` (after the type is read).

**Repro (live):** `--param p=abcdefghijklmnop:nvarchar(5)` returns `abcde`.
`sp_executesql` converts a parameter to its declared type without raising Msg
8152, so `WHERE name = @p` matches a different value than the one given, and a
`DELETE`/`UPDATE` under `--allow write` hits the wrong rows. Same class: a
`decimal(5,1)` value of `12.55` rounds to `12.6` silently.

**Fix:**

- For `char varchar nchar nvarchar` with `(n)`, refuse a value longer than `n`:
  count UTF-16 code units for `nchar`/`nvarchar`, runes for `char`/`varchar`
  (code-page bytes depend on the collation, so runes are the conservative
  floor; document that). `(max)` and `sysname` (128) are checked the same way.
- For `binary`/`varbinary(n)`, refuse more than `n` bytes (hex digits ÷ 2).
- Leave decimal rounding to the server but say so in § Parameters; an exact
  check would re-implement the server's conversion.
- Message: `--param "p=…": 16 characters do not fit nvarchar(5)` (never echo a
  long value in full; the audit log already hashes `--param`).

**Tests:** boundary values (`n`, `n+1`), a surrogate pair in `nvarchar(1)`
(2 units: refused), `varbinary(2)` with 3 bytes.
**Live check:** re-run the repro; it must be `USAGE_ERROR` before connecting.

## B1 — system objects are listed but cannot be described, read or exported

**Where:** `~/go/gosmo/object_lookup.go:35` (`objectInfoSelect` reads
`FROM sys.objects`); reached from `internal/cli/cmd/common.go:resolveObject`.

**Repro (live):**

- `object list --include-system --schema sys --name objects` lists
  `sys.objects`, `sys.all_objects`, `sys.sysobjects`.
- `object describe sys.objects`, `data top sys.objects`, `data count sys.databases`,
  `data top INFORMATION_SCHEMA.TABLES`, `object describe sys.sp_help` all fail
  `NOT_FOUND: no object [sys].[objects] in database [HealthClinic]`.

**Cause:** `sys.objects` holds user objects only; the shipped ones live in
`sys.all_objects` (and `sys.system_objects`). The list command reads the right
view and the lookup the wrong one, so the two disagree. SSMS opens system views
(Select Top 1000 under Views > System Views).

**Fix (gosmo, a behaviour change; back-compat may break):**

- `objectInfoSelect` reads `sys.all_objects`.
- `ObjectsNamed` (the one-part name path) must not turn a user `Patients` into
  an ambiguity with a system object of the same name. Return the user objects
  when there are any, and system ones only when no user object has the name.
  `ObjectByName` (two-part) needs no rule: the schema decides.
- Audit every caller of `ObjectByName` / `ObjectsNamed` / `ObjectInfo` in
  gosmo and gossms for code that relied on system objects being absent
  (`IsMSShipped` is already on the row), and grep both repos for other
  `sys.objects` lookups that feed a by-name resolution.
- Update the type doc ("one row of sys.objects").

**Tests:** gosmo — a lookup of `sys.objects` and `INFORMATION_SCHEMA.TABLES`
(offline fixture plus a live case); a user table named like a system view still
resolves one-part. gossms — `resolveObject` for a `sys` name.
**Version sweep:** run `TestLiveVersionSweep` on the 2016 instance
(`docs/open-threads.md` § Version support): `sys.all_objects` exists on every
supported major, but run it anyway.

## B4 — `query parse` sends every `GO n` repetition

**Where:** `internal/cli/cmd/query.go` (`parse`, near the `ExecuteToSink`
call) and `internal/query/executor.go` `runScript`'s `for range b.Count`.

**Repro (live):** `query parse --sql "SELECT 1\nGO 3000"` takes 5.5 s and
reports `BATCH_COUNT: 3000` and "Batch execution completed 3000 times."
Parsing a batch twice proves nothing the first parse did not, so `GO 100000`
(a normal load-test script) runs the parse 100000 times: about 1.8 ms each,
so the 60 s `--timeout` fires near 33000 repetitions and a script with valid
syntax is reported as `TIMEOUT`. (The timeout figure is extrapolated, not run.)

**Fix:**

- Add a query option (say `query.WithEachBatchOnce()`) that `runScript` honours
  by running every non-empty batch exactly once, `GO 0` included (a parse of a
  batch that would run zero times still checks its syntax). `parse` passes it.
- `batchCount` for `parse` becomes the batches checked, not repetitions;
  drop the "completed N times" notice there (`WithoutSinkNotices` is not
  enough: the notice is a Message).
- `docs/cli.md`: the `query parse` row says "each `GO n` repetition counted";
  change it to "batches checked". `query run` is unchanged.

**Tests:** parse of `GO 3` yields `batchCount` 1 for one batch and no repeat
notice; a syntax error in batch 2 still reports its script line.

## B5 — `data profile` reads and ships whole `(MAX)` values

**Where:** `internal/cli/cmd/data_profile.go` (the per-type aggregate table
around `agg("MIN("+operand+")", …)`, line ~142).

**Repro (live):** `data profile dbo.Pictures` on a table with a
`varbinary(MAX)` column returns `min` and `max` as the full 259 KB and 214 KB
hex values (clipped client-side to `…[+259,054 chars]`). The server read every
LOB to compute them and sent the winners across the wire; `COUNT(DISTINCT)`
hashes them too. For `nvarchar(MAX)` the same, as min/max of free text.
`xml`, `text`, `ntext` and `image` are already excluded for this reason; the
`(MAX)` types are the same hazard.

**Fix:**

- Treat `varchar(max)`, `nvarchar(max)` and `varbinary(max)` like the LOB types
  already excluded: `rows` and `nulls` only, plus `min_len`/`max_len` from
  `DATALENGTH` (cheap: LOB length is metadata) and NULL for `distinct`, `min`
  and `max`.
- Update the `Description` text at `data_profile.go:25-36` and the `data
  profile` row in `docs/cli.md`.
- Check on the 2016 instance that `DATALENGTH` over a MAX column does not read
  the value (look at `SET STATISTICS IO` LOB reads before and after).

**Tests:** the generated T-SQL for a table with one column of each MAX type;
`--top-values` skips them too (it runs one `GROUP BY` per column that has
`distinct`).

---

## Looked at and left alone

- **CSV/TSV write ESC and bidi controls raw** when piped: documented
  (`docs/cli.md` § `csv` and `tsv`: only a terminal is spelled out). Not a bug;
  I tested through a pipe and nearly reported it.
- **`GO` accepts NBSP before it** (`unicode.IsSpace`): the lexer treats NBSP as
  whitespace on purpose (`internal/sqltext/lexer_test.go`).
- **LRM/RLM/ALM (U+200E, U+200F, U+061C) pass through** text and JSON output.
  The documented set is the overrides and isolates; widening it is a hardening
  choice for the author, not a defect.
- **`fileutil.modeFor` drops the execute bit** of a saved script (an existing
  0755 file is rewritten 0644) and `RemoveStaleTemps` would delete a user's
  own `x.sql.tmp123` older than five minutes. Both are real but need an
  unusual file; noted for `docs/open-threads.md` if you want them tracked.
