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
