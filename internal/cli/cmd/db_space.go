package cmd

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// --- db space ---------------------------------------------------------------

func dbSpaceCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"db", "space"},
		Summary: "Show a database's space summary as sp_spaceused reports it, with its log usage",
		Description: "One row per database, read inside it: total (sp_spaceused's database_size: data and log " +
			"files, and from SQL Server 2017 the memory-optimized checkpoint files), data_size and data_used " +
			"(the data files and the space allocated in them), unallocated, reserved, data, index and unused, " +
			"all in sp_spaceused's arithmetic so they match its output exactly. log_size, log_used and " +
			"log_used_pct are sys.dm_db_log_space_usage's, the DMV's own figures; it needs VIEW " +
			"SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022), and without it they are NULL " +
			"with one warning. log_size is the log's usable size, a little under the log files' size. --all " +
			"reports every database, system ones included; one that cannot be read inside (not ONLINE, or no " +
			"user for the login) is named under warnings instead. A named one that cannot be read fails: exit " +
			"8 for its state, 9 for the login's access. A snapshot's figures are its source's as of the " +
			"snapshot's creation, as sp_spaceused reports them there; it has no log, so its log columns are " +
			"NULL, with a warning.",
		Args:    []spec.Arg{{Name: "name", Help: "Database name; or --all"}},
		Options: append(ConnectOptions(), allOption(), unitOption()),
		Class:   spec.ClassRead,
		Run:     d.dbSpace,
		Examples: []string{
			"gossms db space -p dev AdventureWorks",
			"gossms db space -p dev --all -o json",
			"gossms db space -p dev --all --unit gb",
		},
	}
}

// spaceRow is one database db space reports.
type spaceRow struct {
	Database string
	Space    gosmo.SpaceReport
	Log      *gosmo.LogSpace // nil when unread
}

// logSpaceDenied is the warning for log figures the login may not read.
const logSpaceDenied = "log_size, log_used and log_used_pct were not read: sys.dm_db_log_space_usage needs " +
	"VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022)."

func (d Deps) dbSpace(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	name, all, err := nameOrAll(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, err := d.connectServer(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	srv := sc.Server

	var dbs []storageDB
	if all {
		dbs, err = storageDatabases(ctx, srv, true, res)
	} else {
		var t storageDB
		t, err = storageDatabase(ctx, srv, name, res)
		if err == nil && t.Unreadable != "" {
			err = t.unreadableError()
		}
		dbs = []storageDB{t}
	}
	if err != nil {
		return res, err
	}

	var rows []spaceRow
	logDenied := false
	for _, t := range dbs {
		if t.Unreadable != "" {
			res.Warnings = append(res.Warnings, t.unreadableWarning("space"))
			continue
		}
		sp, err := t.DB.SpaceReport(ctx)
		if err != nil {
			if !all || ctx.Err() != nil {
				return res, readError(err)
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: space not read: %v.", gosmo.QuoteName(t.DB.Name), err))
			continue
		}
		r := spaceRow{Database: t.DB.Name, Space: sp}
		switch {
		case t.DB.IsSnapshot():
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s: a database snapshot; its figures are its source's as of the snapshot's creation, and it has no log.",
				gosmo.QuoteName(t.DB.Name)))
		case !logDenied:
			// The DMV's permission is the server's: one refusal answers
			// for every database, so the rest are not asked.
			ls, err := t.DB.LogSpace(ctx)
			switch {
			case err == nil:
				r.Log = &ls
			case ctx.Err() != nil:
				return res, readError(err)
			case gosmo.IsPermissionDenied(err):
				logDenied = true
				res.Warnings = append(res.Warnings, logSpaceDenied)
			default:
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: log usage not read: %v.", gosmo.QuoteName(t.DB.Name), err))
			}
		}
		if sp.XTPPrecreatedHidden {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s: unallocated leaves out the PRECREATED memory-optimized checkpoint files: "+
					"sys.dm_db_xtp_checkpoint_files needs VIEW DATABASE STATE.", gosmo.QuoteName(t.DB.Name)))
		}
		rows = append(rows, r)
	}
	res.Sets = []output.Set{spaceSet(rows, unitOf(inv))}
	return res, nil
}

// spaceSet is rows as db space's result set, sizes in u. log_used_pct is the
// DMV's own figure, not one recomputed from the rounded KB.
func spaceSet(rows []spaceRow, u sizeUnit) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "database", Type: "sysname"},
		u.column("total", false), u.column("data_size", false), u.column("data_used", false),
		u.column("unallocated", false), u.column("reserved", false), u.column("data", false),
		u.column("index", false), u.column("unused", false),
		u.column("log_size", true), u.column("log_used", true), pctColumn("log_used_pct", true),
	}}
	for _, r := range rows {
		s := r.Space
		var logSize, logUsed, logPct *string
		if r.Log != nil {
			logSize, logUsed, logPct = u.cell(r.Log.SizeKB), u.cell(r.Log.UsedKB), ptr(mbf(r.Log.UsedPercent))
		}
		set.Rows = append(set.Rows, []*string{ptr(r.Database),
			u.cell(s.DatabaseSizeKB), u.cell(s.DataFilesKB), u.cell(s.DataUsedKB), u.cell(s.UnallocatedKB),
			u.cell(s.ReservedKB), u.cell(s.DataKB), u.cell(s.IndexKB), u.cell(s.UnusedKB),
			logSize, logUsed, logPct})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// --- db log-usage -----------------------------------------------------------

func dbLogUsageCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"db", "log-usage"},
		Summary: "Show a database's transaction log usage, why it cannot be truncated, and its VLFs",
		Description: "One row per database, read inside it: log_size, log_used and log_used_pct are " +
			"sys.dm_db_log_space_usage's, the DMV's own figures, as db space shows them; log_size is the log's " +
			"usable size, a little under the log files' size. log_reuse_wait_desc is sys.databases', what the " +
			"log waits on before its space can be reused. vlf_count and vlf_active count sys.dm_db_log_info's " +
			"virtual log files, all and active ones; the DMV exists from SQL Server 2016 SP2 (13.0.5026), and on " +
			"an older build both are NULL with a warning. Both DMVs need VIEW SERVER STATE (VIEW SERVER " +
			"PERFORMANCE STATE from SQL Server 2022): without it the command fails, exit 9. --all reports every " +
			"database, system ones included, except snapshots, which have no log; one that cannot be read " +
			"inside (not ONLINE, or no user for the login) is named under warnings instead. A named one that " +
			"cannot be read fails: exit 8 for its state or a snapshot, 9 for the login's access. Warnings: a " +
			"log whose reuse waits on anything but NOTHING, transient waits such as CHECKPOINT included.",
		Args:    []spec.Arg{{Name: "name", Help: "Database name; or --all"}},
		Options: append(ConnectOptions(), allOption(), unitOption()),
		Class:   spec.ClassRead,
		Run:     d.dbLogUsage,
		Examples: []string{
			"gossms db log-usage -p dev AdventureWorks",
			"gossms db log-usage -p dev --all",
			"gossms db log-usage -p dev --all --unit gb -o json",
		},
	}
}

// logRow is one database db log-usage reports.
type logRow struct {
	Database string
	Log      gosmo.LogSpace
}

// logUsageDenied is db log-usage's refusal without the DMVs' permission: the
// server's, so it answers for every database at once.
func logUsageDenied(err error) *output.Error {
	return output.NewError(output.CodePermissionDenied, err.Error(),
		"sys.dm_db_log_space_usage and sys.dm_db_log_info need VIEW SERVER STATE (VIEW SERVER PERFORMANCE "+
			"STATE from SQL Server 2022). Use a login that has it, or have it granted.")
}

func (d Deps) dbLogUsage(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	name, all, err := nameOrAll(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, err := d.connectServer(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	srv := sc.Server

	var dbs []storageDB
	if all {
		dbs, err = storageDatabases(ctx, srv, false, res)
	} else {
		var t storageDB
		t, err = storageDatabase(ctx, srv, name, res)
		switch {
		case err != nil:
		case t.DB.IsSnapshot():
			err = output.NewError(output.CodeNotFound,
				fmt.Sprintf("database %s is a snapshot: it has no transaction log", gosmo.QuoteName(t.DB.Name)),
				"Name the snapshot's source database instead.")
		case t.Unreadable != "":
			err = t.unreadableError()
		}
		dbs = []storageDB{t}
	}
	if err != nil {
		return res, err
	}

	var rows []logRow
	vlfsUnknown := false
	for _, t := range dbs {
		if t.Unreadable != "" {
			res.Warnings = append(res.Warnings, t.unreadableWarning("log usage"))
			continue
		}
		ls, err := t.DB.LogSpace(ctx)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return res, readError(err)
		case gosmo.IsPermissionDenied(err):
			// The whole command is the DMVs; their permission is the
			// server's, so the other databases would be refused too.
			return res, logUsageDenied(err)
		case !all:
			return res, readError(err)
		default:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: log usage not read: %v.", gosmo.QuoteName(t.DB.Name), err))
			continue
		}
		vlfsUnknown = vlfsUnknown || !ls.VLFsKnown
		rows = append(rows, logRow{Database: t.DB.Name, Log: ls})
	}
	if vlfsUnknown {
		res.Warnings = append(res.Warnings,
			"vlf_count and vlf_active were not read: sys.dm_db_log_info exists from SQL Server 2016 SP2 (13.0.5026).")
	}
	res.Sets = []output.Set{logUsageSet(rows, unitOf(inv))}
	for _, r := range rows {
		if w := logReuseWarning(r.Database, r.Log.ReuseWait); w != "" {
			res.Warnings = append(res.Warnings, w)
		}
	}
	return res, nil
}

// logUsageSet is rows as db log-usage's result set, sizes in u: §10.8's
// columns, and vlf_active beyond them. log_used_pct is the DMV's own figure,
// as db space's.
func logUsageSet(rows []logRow, u sizeUnit) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "database", Type: "sysname"},
		u.column("log_size", false), u.column("log_used", false), pctColumn("log_used_pct", false),
		{Name: "log_reuse_wait_desc", Type: "nvarchar(60)"},
		{Name: "vlf_count", Type: "int", Nullable: true}, {Name: "vlf_active", Type: "int", Nullable: true},
	}}
	for _, r := range rows {
		l := r.Log
		var vlfs, active *string
		if l.VLFsKnown {
			vlfs, active = ptr(strconv.Itoa(l.VLFs)), ptr(strconv.Itoa(l.ActiveVLFs))
		}
		set.Rows = append(set.Rows, []*string{ptr(r.Database),
			u.cell(l.SizeKB), u.cell(l.UsedKB), ptr(mbf(l.UsedPercent)), ptr(l.ReuseWait), vlfs, active})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
