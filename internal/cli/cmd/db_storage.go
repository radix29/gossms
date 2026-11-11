package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The storage commands' shared options.
const (
	optUnit = "unit"
	optAll  = "all"
)

// --- Units (D5) -------------------------------------------------------------

// sizeUnit is --unit: the unit every size column of a storage command is in,
// and the suffix of its name (size_mb, size_gb). The figures are read and
// filtered in exact KB; only the cell text is in the unit.
type sizeUnit string

const (
	unitKB sizeUnit = "kb"
	unitMB sizeUnit = "mb"
	unitGB sizeUnit = "gb"
)

// unitOption is --unit. The enum is lower-case, as every enum the parser
// takes.
func unitOption() spec.Option {
	return spec.Option{Name: optUnit, Type: spec.TypeEnum, Enum: []string{string(unitKB), string(unitMB), string(unitGB)},
		Default: string(unitMB), Help: "Unit of the size columns, which their names end in: kb as integers, mb and gb with two decimals"}
}

// unitOf is inv's --unit.
func unitOf(inv *spec.Invocation) sizeUnit {
	return sizeUnit(inv.Values[optUnit].(string))
}

// column is a size column named base_<u>: bigint in KB, decimal(18,2)
// otherwise — a string in JSON, as db list's size_mb.
func (u sizeUnit) column(base string, nullable bool) output.Column {
	typ := "decimal(18,2)"
	if u == unitKB {
		typ = "bigint"
	}
	return output.Column{Name: base + "_" + string(u), Type: typ, Nullable: nullable}
}

// text is kb in u.
func (u sizeUnit) text(kb int64) string {
	switch u {
	case unitKB:
		return strconv.FormatInt(kb, 10)
	case unitGB:
		return strconv.FormatFloat(float64(kb)/(1024*1024), 'f', 2, 64)
	}
	return mb(kb)
}

// cell is kb in u as a cell.
func (u sizeUnit) cell(kb int64) *string { return ptr(u.text(kb)) }

// pctColumn is a *_pct column: always two decimals, whatever the unit.
func pctColumn(name string, nullable bool) output.Column {
	return output.Column{Name: name, Type: "decimal(18,2)", Nullable: nullable}
}

// pctOf is part as a percentage of whole, exact — what filters compare; ok is
// false when whole is not positive (the percentage is then undefined).
func pctOf(part, whole int64) (pct float64, ok bool) {
	if whole <= 0 {
		return 0, false
	}
	return float64(part) * 100 / float64(whole), true
}

// pctCell is part as a percentage of whole with two decimals, NULL when whole
// is not positive.
func pctCell(part, whole int64) *string {
	p, ok := pctOf(part, whole)
	if !ok {
		return nil
	}
	return ptr(mbf(p))
}

// --- <name> or --all (D6) ---------------------------------------------------

// allOption is --all: every database instead of the named one.
func allOption() spec.Option {
	return spec.Option{Name: optAll, Type: spec.TypeBool, Help: "Every database on the server instead of <name>"}
}

// nameOrAll is inv's database argument or --all, exactly one of which a
// storage command takes: neither or both is a usage error naming both.
func nameOrAll(inv *spec.Invocation) (name string, all bool, err error) {
	all = inv.Values[optAll].(bool)
	if len(inv.Args) > 0 {
		name = inv.Args[0]
	}
	switch {
	case name == "" && !all:
		return "", false, usageError(fmt.Sprintf("%s: give a database <name> or --all", inv.Command.Name()),
			"Name one database, or pass --all for every database.")
	case name != "" && all:
		return "", false, usageError(fmt.Sprintf("%s: give a database <name> or --all, not both", inv.Command.Name()),
			"Drop <name> to report on every database, or --all to report on one.")
	}
	return name, all, nil
}

// --- Which databases can be read (D3) ---------------------------------------

// storageDB is a database a storage command reports on, and whether the reads
// that run inside it (Database.Files, SpaceReport, LogSpace) can.
type storageDB struct {
	DB *gosmo.Database
	// Unreadable is why those reads cannot run, "" when they can: the
	// database's state, or the login's missing access.
	Unreadable string
	// Denied marks an Unreadable that is the login's access rather than the
	// state.
	Denied bool
}

// storageDBOf is d's readability from access (Server.DatabaseAccess; nil
// when unread, which leaves an ONLINE database to its own reads to answer).
// The state is checked first: HAS_DBACCESS reads false for every login on a
// database that is not ONLINE.
func storageDBOf(d *gosmo.Database, access map[int]bool) storageDB {
	t := storageDB{DB: d}
	switch {
	case d.State != gosmo.DatabaseOnline:
		t.Unreadable = fmt.Sprintf("the database is %s", d.State)
	case access != nil && !access[d.ID]:
		t.Unreadable, t.Denied = "the login has no access to it", true
	}
	return t
}

// unreadableWarning is the warning for t under --all: what was not read, and
// why.
func (t storageDB) unreadableWarning(what string) string {
	return fmt.Sprintf("%s: %s not read: %s.", gosmo.QuoteName(t.DB.Name), what, t.Unreadable)
}

// unreadableError is the error for a named t whose figures cannot be read:
// NOT_FOUND (exit 8) naming the state, PERMISSION_DENIED for missing access.
func (t storageDB) unreadableError() *output.Error {
	msg := fmt.Sprintf("database %s cannot be read: %s", gosmo.QuoteName(t.DB.Name), t.Unreadable)
	if t.Denied {
		return output.NewError(output.CodePermissionDenied, msg,
			"Use a login that has a user in the database, or have one created.")
	}
	return output.NewError(output.CodeNotFound, msg,
		"Bring the database ONLINE, or run `gossms db list --include-system` for its state.")
}

// databaseAccess is the login's access to every database, nil with a warning
// in res when the read fails: an ONLINE database's own read then answers.
func databaseAccess(ctx context.Context, srv *gosmo.Server, res *output.Result) map[int]bool {
	access, err := srv.DatabaseAccess(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Database access was not read: "+err.Error())
		return nil
	}
	return access
}

// storageDatabase is the database name names, with its readability. Not on
// the server is NOT_FOUND, as db info answers it.
func storageDatabase(ctx context.Context, srv *gosmo.Server, name string, res *output.Result) (storageDB, error) {
	d, err := namedDatabase(ctx, srv, name, res)
	if err != nil {
		return storageDB{}, err
	}
	return storageDBOf(d, databaseAccess(ctx, srv, res)), nil
}

// namedDatabase is the database name names, res's header naming it. Not on
// the server is NOT_FOUND.
func namedDatabase(ctx context.Context, srv *gosmo.Server, name string, res *output.Result) (*gosmo.Database, error) {
	d, err := srv.DatabaseByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, output.NewError(output.CodeNotFound, fmt.Sprintf("no database %s on the server", gosmo.QuoteName(name)),
			"Run `gossms db list --include-system` for the databases the login can see.")
	}
	if err != nil {
		return nil, readError(err)
	}
	res.Database = d.Name
	return d, nil
}

// storageDatabases is --all's databases, system ones included, in the
// catalog's order, each with its readability. A snapshot is left out unless
// withSnapshots: it has no log, so db log-usage has nothing to say of it.
func storageDatabases(ctx context.Context, srv *gosmo.Server, withSnapshots bool, res *output.Result) ([]storageDB, error) {
	dbs, err := srv.Databases(ctx)
	if err != nil {
		return nil, readError(err)
	}
	access := databaseAccess(ctx, srv, res)
	out := make([]storageDB, 0, len(dbs))
	for _, d := range dbs {
		if d.IsSnapshot() && !withSnapshots {
			continue
		}
		out = append(out, storageDBOf(d, access))
	}
	return out, nil
}

// --- Warnings (D4) ----------------------------------------------------------

// The fixed warning thresholds, documented in each command's description.
const (
	warnFreePct   = 10.0 // a data or log file with less free inside it
	warnCapPct    = 90.0 // a capped file at least this full of its max_size
	warnVolumePct = 10.0 // a file on a volume with less free
)

// fileVolume is the volume a file sits on, as a warning names it.
type fileVolume struct {
	Name    string
	FreePct float64
}

// fileWarnings is f's warnings in database, one per reason: less than
// warnFreePct free inside it (only when usedKnown — read through
// Database.Files), within warnCapPct of a capped max_size, percentage
// autogrowth, and a volume (nil when unread) with less than warnVolumePct
// free. The free, cap and growth rules apply to data and log files: a
// FILESTREAM or full-text container has no pages, max_size or growth to
// speak of. Nor do a snapshot's sparse files: the snapshot is read-only and
// never grows into them, while sys.master_files gives them the source's
// max_size and growth — only the volume, which the sparse file fills as the
// source changes, speaks for them.
func fileWarnings(database string, f *gosmo.DatabaseFileInfo, usedKnown, snapshot bool, vol *fileVolume) []string {
	where := fmt.Sprintf("%s file %s", gosmo.QuoteName(database), gosmo.QuoteName(f.Name))
	var out []string
	if (f.Type == gosmo.FileRows || f.Type == gosmo.FileLog) && !snapshot {
		if free, ok := pctOf(f.SizeKB-f.SpaceUsedKB, f.SizeKB); usedKnown && ok && free < warnFreePct {
			out = append(out, fmt.Sprintf("%s: %s %% free inside the file (under %g %%).", where, mbf(free), warnFreePct))
		}
		if full, ok := pctOf(f.SizeKB, f.MaxSizeKB); ok && full >= warnCapPct {
			out = append(out, fmt.Sprintf("%s: at %s %% of its max_size (%g %% or more).", where, mbf(full), warnCapPct))
		}
		if f.IsPercentGrowth {
			out = append(out, fmt.Sprintf("%s: grows by %d %% (percentage autogrowth).", where, f.GrowthPercent))
		}
	}
	if vol != nil && vol.FreePct < warnVolumePct {
		out = append(out, fmt.Sprintf("%s: its volume %s is %s %% free (under %g %%).", where, vol.Name, mbf(vol.FreePct), warnVolumePct))
	}
	return out
}

// logReuseWarning is the warning for a log whose reuse waits on anything
// (sys.databases.log_reuse_wait_desc not NOTHING), "" when it does not.
// Transient waits such as CHECKPOINT are warned of too: the warning states a
// fact, not a judgement.
func logReuseWarning(database, reuseWait string) string {
	if reuseWait == "" || reuseWait == "NOTHING" {
		return ""
	}
	return fmt.Sprintf("%s: log reuse waits on %s.", gosmo.QuoteName(database), reuseWait)
}
