### Explore a schema

```bash
gossms db list -p dev
gossms object list -p dev --type table,view --schema Sales
gossms object search -p dev "MonthlySales" --in name
gossms object definition -p dev Reporting.usp_MonthlySales
gossms object dependencies -p dev Reporting.usp_MonthlySales --direction uses
gossms object describe -p dev Sales.SalesOrderDetail -o json
gossms object indexes -p dev Sales.SalesOrderDetail
gossms object stats -p dev Sales.SalesOrderDetail
```

`object search --in column` finds a column by name in every table and view;
`--in definition` finds text inside procedures, functions, views and
triggers. `object dependencies --direction used-by --depth 3` shows what
breaks if an object changes.

### Read data

```bash
gossms data top -p dev Sales.SalesOrderHeader --rows 20 --columns SalesOrderID,OrderDate,TotalDue --order-by "OrderDate DESC"
gossms data count -p dev Sales.SalesOrderHeader --where "OrderDate >= '2026-01-01'"
gossms data profile -p dev Sales.Customer --top-values 5
gossms query run -p dev --file - -o json <<'SQL'
SELECT TOP (20) h.SalesOrderID, h.OrderDate, c.AccountNumber
FROM Sales.SalesOrderHeader h JOIN Sales.Customer c ON c.CustomerID = h.CustomerID
ORDER BY h.OrderDate DESC;
SQL
```

Select the columns and rows you need: the output is capped, and a `TOP`
costs the server less than `--max-rows`. `--where` and `--order-by` must
leave one SELECT (exit 7 otherwise); `data profile` gives null counts,
distinct counts and min/max per column; joins need `query run`.

### Tune a slow query

```bash
gossms object definition -p dev Reporting.usp_MonthlySales
gossms plan estimated -p dev --file - <<'SQL'
EXEC Reporting.usp_MonthlySales @Year = 2026, @Month = 9;
SQL
gossms object indexes -p dev Sales.SalesOrderDetail
gossms monitor querystore -p dev --regressed
gossms plan actual -p dev --file slow.sql --transaction rollback
gossms query run -p dev --file fix_index.sql --allow ddl --dry-run
```

`plan estimated` runs nothing; its `operators` set ranks the costliest
operators, and `missing_indexes` holds a `CREATE INDEX` to review, not to
run as given. It has no `--param`: write the values into the script.
`plan actual` runs the script (a write needs `--allow write`; `--transaction
rollback` undoes it) and adds actual rows and times; `misestimate` flags an
estimate off by more than 10×. Both need SHOWPLAN (exit 9 without).
`monitor querystore` is the runtime history Query Store keeps for the
connection's database; `--regressed` lists what got slower in the last 24
hours (second half of the window against the first). It needs VIEW DATABASE
STATE; Query Store OFF is exit 8.

### Diagnose a slow server

```bash
gossms monitor waits --interval 10 -p dev
gossms monitor top-queries --by cpu --top 10 -p dev
gossms plan cached <plan_handle> --statement-start <n> --statement-end <n> -p dev
gossms monitor blocking -p dev
gossms monitor sessions --active -p dev
```

`--interval 10` measures the next 10 seconds; without it the totals since
the instance started mix in old load. The `categories` set says where the
time goes: CPU → `top-queries --by cpu`, Disk IO → `--by reads`, Locking →
`monitor blocking`. A top query's `plan_handle`, `statement_start` and
`statement_end` go to `plan cached`, which digests its plan as `plan
estimated` does; the plan cache forgets at eviction or restart, Query Store
(`monitor querystore`) does not. In `monitor blocking` the head (depth 0) is
the cause, often an idle session holding an open transaction, its `text` the
batch it sent last. Ending it (`monitor kill`, `--allow admin`, `--confirm`
the spid) or capturing events (`xevent capture`, `admin`: it creates a
short-lived event session) only with the user's approval. All need VIEW
SERVER STATE (exit 9 without).

### Script and export

```bash
gossms script object -p dev Sales.Customer
gossms script object -p dev Sales.uspGetOrders --as alter --script-file uspGetOrders.sql
gossms script db -p dev AdventureWorks
gossms data export -p dev Sales.Customer --where "ModifiedDate >= '2026-01-01'" --out-file customers.csv
gossms data import -p dev Staging.Customer --in-file customers.csv --allow write --dry-run
gossms data import -p dev Staging.Customer --in-file customers.csv --allow write
```

A script is generated, never run: it is the TUI's Script As text. Use
`--script-file` to save it, not `--out-file` (which is for rows). `data
export` streams every row, uncapped, to `--out-file` in the format its
extension names; `.jsonl` keeps types and tells NULL from an empty string
in every case. `data import` loads such a file in one all-or-nothing batch;
its dry run converts every cell and loads nothing. Load only with the
user's approval; `--truncate-first` empties the table first and needs
`--allow ddl` and `--confirm <table>`.

### Check space

```bash
gossms db files -p dev --all --max-free-pct 10 --sort-by free-pct
gossms db log-usage -p dev --all
gossms server volumes -p dev
gossms db table-sizes -p dev AdventureWorks --top 10
```

The first lists the files under 10 % free inside them, fullest first;
`log_reuse_wait_desc` says why a log cannot be reused; the warnings name
what is worth a look (fixed thresholds, each command's help lists them).
Sizes are in MB; `--unit kb` matches `sp_spaceused` and the DMVs exactly.
`db log-usage` and `server volumes` need VIEW SERVER STATE (exit 9 without).

### Review and change server settings

Only with the user's approval of the change.

```bash
gossms server config list -p dev --non-default
gossms server config get -p dev maxdop cost-threshold
gossms server config set -p dev maxdop 4 cost-threshold 50 --allow admin --dry-run
gossms server config set -p dev maxdop 4 cost-threshold 50 --allow admin
```

`--non-default` compares the configured value to the engine default, not
Setup's recommendation. `get` adds the CPU or memory figures the setting is
judged by. The dry run prints the exact batch and a `planned_change` set and
changes nothing; show it to the user. The real run's `change` set is read
back from the server: `restart_required` 1 means the value waits for a
service restart. A value above physical memory or the CPU count is blocked
(exit 7); `--accept-risk` lifts it only when the user means it.

### Make a safe data fix

Only with the user's approval of the change. Write the T-SQL to a file, then:

```bash
gossms query classify --file fix.sql --allow write
gossms query parse -p dev --file fix.sql
gossms query run -p dev --file fix.sql --allow write --transaction rollback -o json
gossms query run -p dev --file fix.sql --allow write --max-rows-affected 10
```

The rollback run reports `rowsAffected` and leaves nothing changed; show it
to the user. The last run commits only when at most 10 rows change, and
rolls back otherwise (exit 7).
