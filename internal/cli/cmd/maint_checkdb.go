package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// maint checkdb: DBCC CHECKDB, admin as query run classes a DBCC off its read
// list (D1), with no repair option ever (D2): a repair needs SINGLE_USER and
// can lose data, a decision for a person.

const (
	optPhysicalOnly          = "physical-only"
	optNoIndex               = "no-index"
	optExtendedLogicalChecks = "extended-logical-checks"
	optDataPurity            = "data-purity"
)

// The messages CHECKDB's summary rows carry.
const (
	msgCheckDBSummary = 8989 // CHECKDB found n allocation errors and m consistency errors in database '…'.
	msgCheckDBNoDB    = 2520 // Could not find database '…'.
	msgCheckDBState   = 7928 // The database snapshot for online checks could not be created.
)

// checkDBCounts reads 8989's counts: "CHECKDB found 0 allocation errors and
// 3 consistency errors in database 'x'."
var checkDBCounts = regexp.MustCompile(`(\d+) allocation errors? and (\d+) consistency errors?`)

// repairRank orders the repair levels CHECKDB names, the lightest first.
var repairRank = map[string]int{"repair_rebuild": 1, "repair_allow_data_loss": 2}

func checkDBCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"maint", "checkdb"},
		Summary: "Check a database's integrity (DBCC CHECKDB); never repairs",
		Description: "DBCC CHECKDB WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS on <database> (default the connection's, " +
			"-d). Needs --allow admin (query run classes DBCC CHECKDB so too) and sysadmin or db_owner (exit 9 " +
			"without). It reads every page: hours on a large database, with heavy I/O. --physical-only checks page " +
			"and allocation structure only (much faster; not with --data-purity or --extended-logical-checks, exit " +
			"2), --no-index skips nonclustered indexes, --extended-logical-checks adds indexed views, XML and " +
			"spatial indexes, --data-purity checks column values against their types, --maxdop n (1-64) caps the " +
			"parallelism. The set errors lists what it found, the per-table and database summaries left out; " +
			"empty is a clean database. Finding corruption is not a failure of the command: the status stays ok " +
			"(exit 0), consistencyErrors and allocationErrors count the errors, minimumRepair names the least " +
			"repair that fixes them, and a warning says so. There is no repair option: a repair needs the " +
			"database in SINGLE_USER and REPAIR_ALLOW_DATA_LOSS deletes what it cannot fix — restore from a clean " +
			"backup instead, or decide on a repair yourself. --dry-run prints the statement.",
		Args: []spec.Arg{{Name: "database", Help: "Database to check; default the connection's"}},
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optPhysicalOnly, Type: spec.TypeBool, Help: "PHYSICAL_ONLY: page and allocation structure only (much faster)"},
			{Name: optNoIndex, Type: spec.TypeBool, Help: "NOINDEX: skip the nonclustered indexes of user tables"},
			{Name: optExtendedLogicalChecks, Type: spec.TypeBool, Help: "EXTENDED_LOGICAL_CHECKS: indexed views, XML and spatial indexes too"},
			{Name: optDataPurity, Type: spec.TypeBool, Help: "DATA_PURITY: check column values against their types"},
			{Name: optMaxDOP, Type: spec.TypeInt, Help: "MAXDOP = n (1-64): cap the check's parallelism"},
		}),
		Class: spec.ClassAdmin,
		Run:   d.maintCheckDB,
		Examples: []string{
			"gossms maint checkdb AdventureWorks --physical-only -p dev --allow admin",
			"gossms maint checkdb -p dev -d AdventureWorks --allow admin --dry-run",
		},
	}
}

func parseCheckDB(inv *spec.Invocation) (gosmo.CheckDBOptions, error) {
	opts := gosmo.CheckDBOptions{
		PhysicalOnly:          inv.Values[optPhysicalOnly].(bool),
		NoIndex:               inv.Values[optNoIndex].(bool),
		ExtendedLogicalChecks: inv.Values[optExtendedLogicalChecks].(bool),
		DataPurity:            inv.Values[optDataPurity].(bool),
	}
	if inv.Given[optMaxDOP] {
		if opts.MaxDOP = inv.Values[optMaxDOP].(int); opts.MaxDOP < 1 || opts.MaxDOP > maxDOPLimit {
			return opts, usageError(fmt.Sprintf("--maxdop: %d is not 1 to %d", opts.MaxDOP, maxDOPLimit),
				"Leave --maxdop out for the server's own setting.")
		}
	}
	if opts.PhysicalOnly && (opts.DataPurity || opts.ExtendedLogicalChecks) {
		with := "--data-purity"
		if opts.ExtendedLogicalChecks {
			with = "--extended-logical-checks"
		}
		return opts, usageError("--physical-only skips the logical checks "+with+" asks for",
			"Choose one: --physical-only for speed, or the logical checks without it.")
	}
	if len(inv.Args) > 0 && strings.TrimSpace(inv.Args[0]) == "" {
		return opts, usageError("the database name is empty", "Name it, or leave it out for the connection's.")
	}
	return opts, nil
}

func (d Deps) maintCheckDB(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	opts, err := parseCheckDB(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, err := d.connectGated(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var dbase *gosmo.Database
	if len(inv.Args) > 0 {
		dbase, err = namedDatabase(ctx, cat, inv.Args[0], res)
	} else {
		dbase, err = currentDatabase(ctx, cat, res)
	}
	if err != nil {
		return res, err
	}
	if dbase.State != gosmo.DatabaseOnline {
		return res, usageError(fmt.Sprintf("%s is %s: only an ONLINE database can be checked", gosmo.QuoteName(dbase.Name), dbase.State),
			"Bring it online first; db list --include-system shows its state.")
	}

	res.Fields = []output.Field{
		{Key: "database", Value: dbase.Name},
		{Key: "physicalOnly", Value: boolText(opts.PhysicalOnly)},
		{Key: "noIndex", Value: boolText(opts.NoIndex)},
		{Key: "extendedLogicalChecks", Value: boolText(opts.ExtendedLogicalChecks)},
		{Key: "dataPurity", Value: boolText(opts.DataPurity)},
		{Key: "maxdop", Value: nonZero(opts.MaxDOP), Null: opts.MaxDOP == 0},
	}
	var rows []*gosmo.CheckDBError
	check := func(ctx context.Context) error {
		var err error
		rows, err = dbase.CheckDB(ctx, opts)
		return err
	}
	if err := applyWrite(ctx, inv, res, check); err != nil {
		return res, checkDBError(err, dbase.Name)
	}
	if dryRun(inv) {
		return res, nil
	}
	res.Fields = append(res.Fields, checkDBFields(rows, res)...)
	res.Sets = []output.Set{checkDBSet(rows)}
	return res, nil
}

// checkDBFields is what a real check found: the counts from its 8989
// summary (counted from the rows when it has none), and the least repair
// that fixes every error. A database with errors adds a warning.
func checkDBFields(rows []*gosmo.CheckDBError, res *output.Result) []output.Field {
	alloc, cons, found := 0, 0, false
	errs, minRepair := 0, ""
	for _, r := range rows {
		if r.Error == msgCheckDBSummary {
			if m := checkDBCounts.FindStringSubmatch(r.Message); m != nil {
				alloc, _ = strconv.Atoi(m[1])
				cons, _ = strconv.Atoi(m[2])
				found = true
			}
		}
		if r.IsSummary() {
			continue
		}
		errs++
		if repairRank[r.RepairLevel] > repairRank[minRepair] {
			minRepair = r.RepairLevel
		}
	}
	if !found {
		// No summary row: a clean database has none, and the rows are all
		// there is to count. Which are allocation errors only the summary says.
		cons = errs
	}
	if errs > 0 || alloc+cons > 0 {
		repair := "no repair fixes them all"
		if minRepair != "" {
			repair = minRepair + " is the least repair that fixes them"
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("CHECKDB found %d allocation and %d consistency errors; %s. "+
			"Restoring from a clean backup loses nothing; a repair needs SINGLE_USER, and repair_allow_data_loss "+
			"deletes what it cannot fix — gossms offers none.", alloc, cons, repair))
	}
	return []output.Field{
		{Key: "allocationErrors", Value: strconv.Itoa(alloc)},
		{Key: "consistencyErrors", Value: strconv.Itoa(cons)},
		{Key: "minimumRepair", Value: minRepair, Null: minRepair == ""},
	}
}

// checkDBSet is the errors CHECKDB found, its summaries left out (the
// fields carry their counts).
func checkDBSet(rows []*gosmo.CheckDBError) output.Set {
	set := output.Set{Name: "errors", Columns: []output.Column{
		{Name: "error", Type: "int"}, {Name: "level", Type: "int"}, {Name: "state", Type: "int"},
		{Name: "message", Type: "nvarchar(2048)"}, {Name: "repair_level", Type: "nvarchar(30)", Nullable: true},
		{Name: "object_id", Type: "int", Nullable: true}, {Name: "index_id", Type: "int", Nullable: true},
		{Name: "partition_id", Type: "bigint", Nullable: true}, {Name: "alloc_unit_id", Type: "bigint", Nullable: true},
		{Name: "file", Type: "int", Nullable: true}, {Name: "page", Type: "int", Nullable: true}, {Name: "slot", Type: "int", Nullable: true},
	}}
	nz := func(n int64) *string {
		if n == 0 {
			return nil
		}
		return int64Cell(n)
	}
	for _, r := range rows {
		if r.IsSummary() {
			continue
		}
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(r.Error)), ptr(strconv.Itoa(r.Level)), ptr(strconv.Itoa(r.State)),
			ptr(r.Message), textOrNull(r.RepairLevel), nz(int64(r.ObjectID)), nz(int64(r.IndexID)), nz(r.PartitionID),
			nz(r.AllocUnitID), nz(int64(r.File)), nz(int64(r.Page)), nz(int64(r.Slot))})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// checkDBError is checkdb's failure, with the permission it needs and the
// state refusals explained.
func checkDBError(err error, db string) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = "DBCC CHECKDB needs sysadmin, or db_owner in " + gosmo.QuoteName(db) + ". Use a login that has one."
		return e
	}
	switch e.SQL.Number {
	case msgCheckDBNoDB:
		e.Code = output.CodeNotFound
	case msgCheckDBState:
		e.Hint = "CHECKDB checks a hidden snapshot of the database, which could not be created (disk space, or " +
			"the file system): the message after it says why."
	}
	return e
}
