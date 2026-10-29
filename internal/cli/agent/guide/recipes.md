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
gossms query run -p dev --file - -o json <<'SQL'
SELECT TOP (20) SalesOrderID, OrderDate, TotalDue
FROM Sales.SalesOrderHeader
ORDER BY OrderDate DESC;
SQL
```

Select the columns and rows you need: the output is capped, and a `TOP`
costs the server less than `--max-rows`.

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
