package cmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The monitor top-queries options (D4). --plan-database, not --database:
// that is the connection's -d. --top is db table-sizes' name.
const (
	optBy           = "by"
	optAverage      = "avg"
	optPlanDatabase = "plan-database"
)

// --by's values, in topQueriesBy's order; the first is the default.
var topQueriesByNames = []string{"cpu", "reads", "writes", "duration", "executions"}

var topQueriesBy = map[string]gosmo.TopQueriesBy{
	"cpu":        gosmo.TopByCPU,
	"reads":      gosmo.TopByLogicalReads,
	"writes":     gosmo.TopByLogicalWrites,
	"duration":   gosmo.TopByDuration,
	"executions": gosmo.TopByExecutions,
}

func monitorTopQueriesCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "top-queries"},
		Summary: "Rank the cached statements by CPU, reads, writes, duration or executions",
		Description: "sys.dm_exec_query_stats: one row per cached statement, its totals since its plan was cached " +
			"(cached_at, server local time) — a plan leaves the cache at eviction, recompilation or a restart, and " +
			"its totals with it, so this is not a fixed window; Query Store is the durable history (monitor " +
			"querystore). --by cpu (default), reads, writes, duration or executions ranks by the total; --avg by " +
			"the figure per execution (not with executions). Set queries, highest first, --top rows (default 20): " +
			"times in milliseconds (the server keeps microseconds), reads and writes logical (pages), database the " +
			"one the plan was compiled in (the connection's for an ad hoc batch, the module's for a procedure; " +
			"NULL when it names none the login can see, the hidden resource database's among them), text the statement cut from its batch (NULL when the batch has left " +
			"the cache or is an encrypted module's). plan_handle, statement_start and statement_end address the " +
			"statement's plan: give them to plan cached. --plan-database keeps the statements whose plan was " +
			"compiled in that database (exit 8 when no database has the name); it reads every cached plan's " +
			"attributes, so it costs more on a large plan cache. Needs VIEW SERVER STATE (VIEW SERVER " +
			"PERFORMANCE STATE from SQL Server 2022): without it, exit 9.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optBy, Type: spec.TypeEnum, Enum: topQueriesByNames, Default: topQueriesByNames[0],
				Help: "The figure to rank by"},
			spec.Option{Name: optAverage, Type: spec.TypeBool, Help: "Rank by the figure per execution instead of the total"},
			spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultTop),
				Help: "Keep this many statements, the highest first"},
			spec.Option{Name: optPlanDatabase, Type: spec.TypeString,
				Help: "Only statements whose plan was compiled in this database; -d is the one the CLI connects to"},
		),
		Class: spec.ClassRead,
		Run:   d.monitorTopQueries,
		Examples: []string{
			"gossms monitor top-queries -p prod-ro",
			"gossms monitor top-queries --by reads --avg --top 10 -p prod-ro -o json",
			"gossms monitor top-queries --plan-database Sales --by duration -p prod-ro",
		},
	}
}

func (d Deps) monitorTopQueries(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	by := inv.Values[optBy].(string)
	avg := inv.Values[optAverage].(bool)
	if avg && topQueriesBy[by] == gosmo.TopByExecutions {
		return nil, usageError("--avg: executions has no average",
			"Rank by executions without --avg, or by another figure per execution.")
	}
	top := inv.Values[optTop].(int)
	if top < 1 {
		return nil, usageError(fmt.Sprintf("--top: %d is not a number of statements", top),
			"Give the number of statements to keep, 1 or more.")
	}
	database, _ := inv.Values[optPlanDatabase].(string)

	res := &output.Result{}
	closeConn, cat, err := d.connectServerState(ctx, inv, res, "monitor top-queries")
	if err != nil {
		return res, err
	}
	defer closeConn()

	qs, err := cat.TopQueries(ctx, gosmo.TopQueriesOptions{By: topQueriesBy[by], Average: avg, Top: top, Database: database})
	if err != nil {
		return res, readError(err)
	}
	ranking := "total"
	if avg {
		ranking = "average"
	}
	res.Fields = []output.Field{{Key: "rankedBy", Value: by}, {Key: "ranking", Value: ranking}}
	res.Sets = []output.Set{topQueriesSet(qs)}
	return res, nil
}

// hexCell is a binary value as T-SQL writes it: 0x and upper-case hex, as
// SSMS's grid shows it and plan cached reads it back.
func hexCell(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	return ptr("0x" + strings.ToUpper(hex.EncodeToString(b)))
}

// microsMs is a microsecond figure as milliseconds, three decimals.
func microsMs(us float64) *string { return ptr(strconv.FormatFloat(us/1000, 'f', 3, 64)) }

// topQueriesSet is monitor top-queries' queries set: D4's figures, each total
// beside its average.
func topQueriesSet(qs []*gosmo.CachedQueryStat) output.Set {
	set := output.Set{Name: "queries", Columns: []output.Column{
		{Name: "n", Type: "int"}, {Name: "database", Type: "nvarchar(128)", Nullable: true},
		{Name: "executions", Type: "bigint"},
		{Name: "cpu_ms", Type: "decimal(19,3)"}, {Name: "avg_cpu_ms", Type: "decimal(19,3)"},
		{Name: "duration_ms", Type: "decimal(19,3)"}, {Name: "avg_duration_ms", Type: "decimal(19,3)"},
		{Name: "logical_reads", Type: "bigint"}, {Name: "avg_logical_reads", Type: "decimal(19,2)"},
		{Name: "logical_writes", Type: "bigint"}, {Name: "avg_logical_writes", Type: "decimal(19,2)"},
		{Name: "last_execution", Type: "datetime"}, {Name: "cached_at", Type: "datetime"},
		{Name: "query_hash", Type: "binary(8)", Nullable: true}, {Name: "plan_handle", Type: "varbinary(64)"},
		{Name: "statement_start", Type: "int"}, {Name: "statement_end", Type: "int"},
		{Name: "text", Type: "nvarchar(max)", Nullable: true},
	}}
	for i, q := range qs {
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(i + 1)), textOrNull(q.Database), int64Cell(q.ExecutionCount),
			microsMs(float64(q.TotalCPUMicros)), microsMs(q.AvgCPUMicros()),
			microsMs(float64(q.TotalDurationMicros)), microsMs(q.AvgDurationMicros()),
			int64Cell(q.TotalLogicalReads), ptr(mbf(q.AvgLogicalReads())),
			int64Cell(q.TotalLogicalWrites), ptr(mbf(q.AvgLogicalWrites())),
			ptr(gridTime(q.LastExecutionTime)), ptr(gridTime(q.CreationTime)),
			hexCell(q.QueryHash), hexCell(q.PlanHandle),
			ptr(strconv.Itoa(q.StatementStart)), ptr(strconv.Itoa(q.StatementEnd)), textOrNull(q.Text),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
