# Two proposals — 2026-08-05

**Both are closed. Neither was implemented, and neither is open work.** This
file is retained only for the measurements behind the two decisions — the
decisions themselves live in `docs/open-threads.md` § "By design — not
issues, do not re-raise". Trimmed 2026-08-06: § 1's rejected proposal text
was removed and only its measurement and correction kept.

- **§ 1 (`internal/tui` restructuring) — rejected 2026-08-05** on
  re-measurement. `internal/tui/sqlparse` shipped instead. Do not act on it.
- **§ 2 (Databases folder round trips) — decided 2026-08-05: leave it
  alone.** Do not re-open it as a performance finding.

---

## 1. Restructuring `internal/tui` — rejected

The original proposal argued from `grep -E '\bApp\b'`: 120 non-test files and
26,303 non-test lines in one package, of which "56% never mention `App`",
therefore three extractions — `completion`, a ~11,000-line `props` behind a
five-method `Host` interface, and `dialogs`.

Re-derived with a type-checked cross-file reference graph (`go/types`, 549
real symbol edges), four of its claims do not survive:

1. **The 56% is wrong.** `grep -w App` misses `p.app`/`d.app` — lowercase
   field access. The real figure is **48%: 58 files, 12,618 lines**, and nine
   files are misclassified — including two of Step 1's four.

2. **Step 1 is not "a pure lift with no interface needed".** The four files
   hold **30 outbound references to 11 symbols**: `QueryPanel` ×11 (most of
   `completion_candidates.go` consists of `*QueryPanel` methods),
   `nodeIcon`/`nodeData`/`Node*` from `tree_node.go`, `p.app.cfg.IconStyle`
   ×3, `isConnected`, `completionInventory` ×6. Nor does its test suite
   "travel unchanged": `completion_provider_test.go` — 811 of the 1,407 test
   lines — is built on `newTestApp()`.

3. **Step 2's "one interface, five methods" is off by roughly 8×.** The 41
   `propPage` files have **110 outbound references to 44 distinct symbols
   across 16 files**. Only ~6 are `App` services (and the list misses
   `showAgentJobHistory`, `agent_job_props_history.go:73`). The other ~38 are
   shared helpers — `formatSQLDate` ×19, `fqn` ×10, `formatHMS`,
   `dashIfZero`, `intRowValue0`, `formatJobOutcome/State/NotifyLevel`,
   `diskVolumeLabel/Value` — living in files that are 146-221-reference hubs
   for the rest of `tui` and therefore cannot move. Step 2 needs a *fourth*
   package, or duplication.

4. **Step 2's stated benefit already exists.** `owner_transfer_page_test.go`,
   `membership_page_test.go`, `prop_dialog_script_test.go`,
   `index_props_test.go` and `perm_state_test.go` have zero `App` references
   and pass today. The pages are already `App`-free by construction; the
   boundary would enforce a property that already holds.

And the precedent argues the other way: `planview` has **no `Host` interface
and no callbacks**. It is a pure leaf, and that is why it worked. Step 2 is
the opposite shape. Step 3 confirms itself out — 55 outbound references
including `focusable`/`Focus`/`childFetchTimeout`; those dialogs *are* shell.

Nothing was under pressure either: the package builds in 3.8s cold, tests in
12.6s, with no import cycles and one author.

**What shipped instead**, on the seam the measurement actually found: the
tokenizer and scope files have **zero outbound references** — the only such
set in the package. They became `internal/tui/sqlparse` (786 lines, 596 lines
of tests, 16 exported symbols), a leaf like `planview`, worth doing because a
T-SQL lexer has no business in the application shell. Steps 1-as-written, 2
and 3 are rejected. See `docs/journal.md`
(`sqlparse-extraction-2026-08-05`).

The most valuable part of § 1 is its *negative* result — that the
`agent_*`/`new_*`/`*_props_*` name families are not a seam. That stands.

---

## 2. The Databases folder's per-database round trip

### The current state

`loadDatabasesFolderDetails` (`internal/tui/detail_browser_databases.go`)
runs one fast `DatabasesContext` query, paints Name/State/Recovery
immediately, then backfills the five size columns with one
`SpaceUsedContext` per database, up to `maxRowFetchConcurrency` (8) at a
time.

The reason it fans out is real and already recorded: only `TotalMB` could
come from a server-wide view; Data/Log/Unallocated/AvailLog all derive from
`FILEPROPERTY`, which reports on the **current database only**.

### It can be collapsed — verified, not guessed

`FILEPROPERTY` being current-database-only does not force *N client round
trips*. It forces N `USE` statements, and those can live inside one dynamic
batch:

```sql
DECLARE @res TABLE (name sysname, total_mb float, data_mb float,
                    log_mb float, unalloc_mb float, avail_log_mb float);
DECLARE @sql nvarchar(max);
SELECT @sql = STRING_AGG(CAST(
  N'USE ' + QUOTENAME(name) + N'; SELECT DB_NAME(), ' +
  N'SUM(size)*8.0/1024, ... FROM sys.database_files;' AS nvarchar(max)), CHAR(10))
FROM   sys.databases
WHERE  state = 0 AND database_id > 4 AND HAS_DBACCESS(name) = 1;
INSERT @res EXEC sp_executesql @sql;
SELECT * FROM @res ORDER BY name;
```

`INSERT ... EXEC` concatenates every result set the dynamic batch produces,
and `USE` inside `sp_executesql` scopes to that batch.

Run against `ubudock` (4 user databases), warm pool, three runs each:

| approach | time |
|---|---|
| one batch | 11–12 ms |
| fan-out, serial | 20–21 ms |

Figures identical to `SpaceUsedContext`'s, column for column. Round trips go
from *N+1* to *1*.

### The reason not to do it

One unreachable database kills the entire batch. Forced deterministically by
putting a non-existent database in the list:

```
WHOLE BATCH FAILED: mssql: Database 'ClaudeGoneDB' does not exist.
```

Not a truncated result — no rows at all. The `WHERE state = 0 AND
HAS_DBACCESS(name) = 1` filter closes the common cases, but not the race: a
database that goes offline, is dropped, or is detached between building
`@sql` and running it takes every other row's sizes down with it. Today that
same database shows `N/A` in its own row and nothing else is affected.

That asymmetry is the whole decision. The current design degrades per row;
the batch degrades per folder.

Two further costs worth naming: `STRING_AGG` is SQL 2017+, so an older
target needs the `FOR XML PATH` form; and the progressive paint disappears —
today Name/State/Recovery land immediately and sizes fill in visibly, which
on a slow link is better *perceived* behaviour than one 500 ms wait for
everything.

### What the numbers actually say

20 ms versus 12 ms, on a LAN, for a folder the user opens occasionally. The
fan-out is already concurrent 8-wide, so its cost is roughly
`ceil(N/8) × RTT`, not `N × RTT` — at 50 databases and a 5 ms RTT that is
~35 ms, still not a problem. The batch only clearly wins on a high-latency
link with many databases, and that is exactly the situation where its
all-or-nothing failure mode hurts most.

**Recommendation: leave it alone.** The fan-out is not an oversight, and the
batch trades a per-row failure for a per-folder one to save single-digit
milliseconds in the case anyone actually hits. Revisit only if a real user
reports the Databases folder being slow — and then implement the batch as a
*fast path* with the existing fan-out as the fallback on any batch error,
never as a replacement.
