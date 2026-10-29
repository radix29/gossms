package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
func databaseAccess(ctx context.Context, cat Catalog, res *output.Result) map[int]bool {
	access, err := cat.DatabaseAccess(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Database access was not read: "+err.Error())
		return nil
	}
	return access
}

// storageDatabase is the database name names, with its readability. Not on
// the server is NOT_FOUND, as db info answers it.
func storageDatabase(ctx context.Context, cat Catalog, name string, res *output.Result) (storageDB, error) {
	d, err := cat.DatabaseByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return storageDB{}, output.NewError(output.CodeNotFound, fmt.Sprintf("no database %s on the server", gosmo.QuoteName(name)),
			"Run `gossms db list --include-system` for the databases the login can see.")
	}
	if err != nil {
		return storageDB{}, readError(err)
	}
	res.Database = d.Name
	return storageDBOf(d, databaseAccess(ctx, cat, res)), nil
}

// storageDatabases is --all's databases, system ones included, in the
// catalog's order, each with its readability. A snapshot is left out unless
// withSnapshots: it has no log, so db log-usage has nothing to say of it.
func storageDatabases(ctx context.Context, cat Catalog, withSnapshots bool, res *output.Result) ([]storageDB, error) {
	dbs, err := cat.Databases(ctx)
	if err != nil {
		return nil, readError(err)
	}
	access := databaseAccess(ctx, cat, res)
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

// --- db files ---------------------------------------------------------------

// The db files options.
const (
	optFileType   = "type"
	optFileGroup  = "filegroup"
	optSortBy     = "sort-by"
	optMaxFreePct = "max-free-pct"
)

// fileTypes is --type's enum and the type column's values, by type_desc.
var fileTypes = []struct {
	Name string
	Type gosmo.FileType
}{{"data", gosmo.FileRows}, {"log", gosmo.FileLog}, {"filestream", gosmo.FileStream}, {"fulltext", gosmo.FileFullText}}

func fileTypeNames() []string {
	out := make([]string, len(fileTypes))
	for i, t := range fileTypes {
		out[i] = t.Name
	}
	return out
}

// fileTypeName is t's type column text; an unknown type_desc as it comes,
// lower-cased.
func fileTypeName(t gosmo.FileType) string {
	for _, ft := range fileTypes {
		if ft.Type == t {
			return ft.Name
		}
	}
	return strings.ToLower(string(t))
}

// hasPages is whether f is a data or log file: a FILESTREAM or full-text
// container has no pages, so no space used, growth or max_size (P4-7).
func hasPages(f *gosmo.DatabaseFileInfo) bool {
	return f.Type == gosmo.FileRows || f.Type == gosmo.FileLog
}

func dbFilesCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"db", "files"},
		Summary: "List a database's files: size, space used and free, autogrowth, max size, free space on the volume",
		Description: "One row per file, data and log alike, read inside the database (sys.database_files and " +
			"FILEPROPERTY), so tempdb's sizes are its current ones. --all reports every database, system ones " +
			"included, with a database column first. A database that cannot be read inside (not ONLINE, or no " +
			"user for the login) is listed from sys.master_files with filegroup, used, free and free_pct NULL and " +
			"a warning saying why. A FILESTREAM or full-text container has no pages: its used, free, free_pct, " +
			"growth and max_size are NULL. max_size reads unlimited, or a size in the unit; growth reads as a " +
			"percentage, none, or a size in KB under --unit kb and in MB otherwise. volume_free_pct is the free " +
			"space on the volume holding the file (sys.dm_os_volume_stats, which needs VIEW SERVER STATE; NULL " +
			"with a warning without it). A snapshot lists its own sparse files, whose size is a ceiling. used is " +
			"NULL where FILEPROPERTY answers nothing (the login cannot see the file). --max-free-pct keeps files whose free_pct is known and at most the figure, compared exactly rather " +
			"than on the rounded text. --sort-by: name (database, then logical name), size and used largest " +
			"first, free-pct fullest first; NULLs last. Without it, files are in database, then file_id order. " +
			"Warnings, for the rows shown only: a data or log file under 10 % free inside it, at 90 % or more " +
			"of a capped max_size, or growing by a percentage (not a snapshot's, which never grows); a file on a " +
			"volume under 10 % free; a snapshot's file, whose size is a ceiling, with the space it occupies on disk.",
		Args: []spec.Arg{{Name: "name", Help: "Database name; or --all"}},
		Options: append(ConnectOptions(),
			allOption(), unitOption(),
			spec.Option{Name: optFileType, Type: spec.TypeEnum, Enum: fileTypeNames(), Help: "Only files of this type"},
			spec.Option{Name: optFileGroup, Type: spec.TypeString, Help: "Only files in this filegroup (case-insensitive)"},
			spec.Option{Name: optSortBy, Type: spec.TypeEnum, Enum: []string{"name", "size", "used", "free-pct"},
				Help: "Order the files: name, size or used (largest first), free-pct (fullest first)"},
			spec.Option{Name: optMaxFreePct, Type: spec.TypeInt, Help: "Only files with at most this percentage free inside them, 0 to 100"},
		),
		Class: spec.ClassRead,
		Run:   d.dbFiles,
		Examples: []string{
			"gossms db files -p dev AdventureWorks",
			"gossms db files -p dev tempdb --unit kb",
			"gossms db files -p dev --all --max-free-pct 10 --sort-by free-pct",
			"gossms db files -p dev --all --type log --unit gb -o json",
		},
	}
}

// fileRow is one file db files reports, with what its cells and warnings
// need.
type fileRow struct {
	Database   string
	DatabaseID int
	File       *gosmo.DatabaseFileInfo
	// Snapshot is whether File is a snapshot's sparse file, whose size is a
	// ceiling and SizeOnDiskKB its footprint.
	Snapshot bool
	// Inside is whether File's filegroup and space used were read inside the
	// database, not only from sys.master_files.
	Inside bool
	Vol    *fileVolume // nil when unread or implausible
}

// usedKnown is whether r's space used is known: read inside the database,
// for a data or log file, and not 0 — FILEPROPERTY answers NULL (read as 0)
// without the permission to see the file, and for a snapshot's log, while a
// data or log file always holds its header pages.
func (r fileRow) usedKnown() bool {
	return r.Inside && hasPages(r.File) && r.File.SpaceUsedKB > 0
}

// freePct is r's exact free percentage, ok false when unknown.
func (r fileRow) freePct() (float64, bool) {
	if !r.usedKnown() {
		return 0, false
	}
	return pctOf(r.File.SizeKB-r.File.SpaceUsedKB, r.File.SizeKB)
}

// volumeIndex is FileVolumes by database and file id, implausible figures
// left out (Azure MI's total 0 or available over total). nil with a warning
// in res when the volumes cannot be read.
func volumeIndex(ctx context.Context, cat Catalog, res *output.Result) map[[2]int]*fileVolume {
	vols, visible, err := cat.FileVolumes(ctx)
	switch {
	case err != nil:
		res.Warnings = append(res.Warnings, "volume_free_pct was not read: "+err.Error())
		return nil
	case !visible:
		res.Warnings = append(res.Warnings,
			"volume_free_pct was not read: sys.dm_os_volume_stats needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022).")
		return nil
	}
	out := make(map[[2]int]*fileVolume, len(vols))
	for _, v := range vols {
		if !v.Plausible() {
			continue
		}
		free, _ := pctOf(v.AvailableBytes, v.TotalBytes)
		out[[2]int{v.DatabaseID, v.FileID}] = &fileVolume{Name: cmp.Or(v.MountPoint, v.VolumeName, v.PhysicalName), FreePct: free}
	}
	return out
}

// databaseFileRows is t's files in file_id order: read inside the database
// when it can be, else from sys.master_files with a warning in res (D3).
//
// A snapshot is listed from sys.master_files with the filegroup and space
// used of its inside read: sys.database_files in a snapshot is the source's
// file list — the source's paths, its log and its FILESTREAM container —
// while master_files holds the snapshot's own sparse files (P4-6).
func databaseFileRows(ctx context.Context, cat Catalog, t storageDB, res *output.Result) ([]fileRow, error) {
	var files []*gosmo.DatabaseFileInfo
	inside := t.Unreadable == ""
	if inside {
		var err error
		if files, err = cat.Files(ctx, t.DB); err != nil {
			if ctx.Err() != nil {
				return nil, readError(err)
			}
			inside, t.Unreadable = false, err.Error()
		}
	}
	var master []*gosmo.DatabaseFileInfo
	if !inside || t.DB.IsSnapshot() {
		var err error
		if master, err = cat.DatabaseFiles(ctx, t.DB.Name); err != nil {
			if ctx.Err() != nil {
				return nil, readError(err)
			}
			if !inside {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: files not read (%s), nor from sys.master_files: %v.",
					gosmo.QuoteName(t.DB.Name), t.Unreadable, err))
				return nil, nil
			}
		}
	}
	switch {
	case !inside:
		files = master
		why := fmt.Sprintf("%s: filegroup and space used not read (%s); files listed from sys.master_files.",
			gosmo.QuoteName(t.DB.Name), t.Unreadable)
		if len(files) == 0 {
			why = fmt.Sprintf("%s: files not read (%s), and sys.master_files shows none to the login (VIEW ANY DEFINITION).",
				gosmo.QuoteName(t.DB.Name), t.Unreadable)
		}
		res.Warnings = append(res.Warnings, why)
	case t.DB.IsSnapshot() && len(master) == 0:
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%s: a snapshot whose own files sys.master_files does not show the login (VIEW ANY DEFINITION); paths and files are the source database's.",
			gosmo.QuoteName(t.DB.Name)))
	case t.DB.IsSnapshot():
		byID := make(map[int]*gosmo.DatabaseFileInfo, len(files))
		for _, f := range files {
			byID[f.FileID] = f
		}
		for _, f := range master {
			if in := byID[f.FileID]; in != nil {
				f.FileGroup, f.SpaceUsedKB = in.FileGroup, in.SpaceUsedKB
			}
		}
		files = master
	}
	rows := make([]fileRow, len(files))
	for i, f := range files {
		rows[i] = fileRow{Database: t.DB.Name, DatabaseID: t.DB.ID, File: f, Inside: inside,
			Snapshot: t.DB.IsSnapshot() && len(master) > 0}
	}
	slices.SortStableFunc(rows, func(a, b fileRow) int { return cmp.Compare(a.File.FileID, b.File.FileID) })
	return rows, nil
}

func (d Deps) dbFiles(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	name, all, err := nameOrAll(inv)
	if err != nil {
		return nil, err
	}
	maxFree, byFree := inv.Values[optMaxFreePct].(int), inv.Given[optMaxFreePct]
	if byFree && (maxFree < 0 || maxFree > 100) {
		return nil, usageError(fmt.Sprintf("--max-free-pct: %d is not between 0 and 100", maxFree),
			"Give the most free space, in percent, a file may have to be listed: --max-free-pct 10.")
	}
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var dbs []storageDB
	if all {
		dbs, err = storageDatabases(ctx, cat, true, res)
	} else {
		var t storageDB
		t, err = storageDatabase(ctx, cat, name, res)
		dbs = []storageDB{t}
	}
	if err != nil {
		return res, err
	}
	var rows []fileRow
	for _, t := range dbs {
		r, err := databaseFileRows(ctx, cat, t, res)
		if err != nil {
			return res, err
		}
		rows = append(rows, r...)
	}

	typ, _ := inv.Values[optFileType].(string)
	fg, _ := inv.Values[optFileGroup].(string)
	rows = slices.DeleteFunc(rows, func(r fileRow) bool {
		if typ != "" && fileTypeName(r.File.Type) != typ {
			return true
		}
		if fg != "" && (!r.Inside || !strings.EqualFold(r.File.FileGroup, fg)) {
			return true
		}
		free, ok := r.freePct()
		return byFree && (!ok || free > float64(maxFree))
	})
	if sortBy, _ := inv.Values[optSortBy].(string); sortBy != "" {
		sortFileRows(rows, sortBy)
	}

	var vols map[[2]int]*fileVolume
	if len(rows) > 0 {
		vols = volumeIndex(ctx, cat, res)
	}
	for i := range rows {
		rows[i].Vol = vols[[2]int{rows[i].DatabaseID, rows[i].File.FileID}]
	}

	res.Sets = []output.Set{fileSet(rows, unitOf(inv), all)}
	for _, r := range rows {
		if r.Snapshot && r.File.SizeOnDiskKB > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s file %s: a snapshot's sparse file; its size is a ceiling, it occupies %s MB on disk.",
				gosmo.QuoteName(r.Database), gosmo.QuoteName(r.File.Name), mb(r.File.SizeOnDiskKB)))
		}
		res.Warnings = append(res.Warnings, fileWarnings(r.Database, r.File, r.usedKnown(), r.Snapshot, r.Vol)...)
	}
	return res, nil
}

// sortFileRows orders rows by --sort-by: name by database then logical name;
// size and used largest first; free-pct fullest first. Unknown figures sort
// last; ties go by database, then logical name.
func sortFileRows(rows []fileRow, by string) {
	byName := func(a, b fileRow) int {
		return cmp.Or(cmp.Compare(a.Database, b.Database), cmp.Compare(a.File.Name, b.File.Name))
	}
	// known orders a known figure before an unknown one, then by cmpKnown.
	known := func(aok, bok bool, cmpKnown int) int {
		switch {
		case aok && bok:
			return cmpKnown
		case aok:
			return -1
		case bok:
			return 1
		}
		return 0
	}
	slices.SortStableFunc(rows, func(a, b fileRow) int {
		var c int
		switch by {
		case "size":
			c = cmp.Compare(b.File.SizeKB, a.File.SizeKB)
		case "used":
			c = known(a.usedKnown(), b.usedKnown(), cmp.Compare(b.File.SpaceUsedKB, a.File.SpaceUsedKB))
		case "free-pct":
			af, aok := a.freePct()
			bf, bok := b.freePct()
			c = known(aok, bok, cmp.Compare(af, bf))
		}
		return cmp.Or(c, byName(a, b))
	})
}

// fileSet is rows as db files' result set, sizes in u; withDatabase puts the
// database column first (--all).
func fileSet(rows []fileRow, u sizeUnit, withDatabase bool) output.Set {
	var cols []output.Column
	if withDatabase {
		cols = append(cols, output.Column{Name: "database", Type: "sysname"})
	}
	cols = append(cols,
		output.Column{Name: "logical_name", Type: "sysname"}, output.Column{Name: "type", Type: "nvarchar(60)"},
		output.Column{Name: "filegroup", Type: "sysname", Nullable: true},
		output.Column{Name: "physical_path", Type: "nvarchar(260)"},
		u.column("size", false), u.column("used", true), u.column("free", true), pctColumn("free_pct", true),
		output.Column{Name: "growth", Type: "nvarchar(30)", Nullable: true},
		output.Column{Name: "max_size_" + string(u), Type: "nvarchar(30)", Nullable: true},
		pctColumn("volume_free_pct", true),
	)
	set := output.Set{Columns: cols}
	for _, r := range rows {
		f := r.File
		var filegroup, used, free, freePct, growth, maxSize, volFree *string
		if r.Inside {
			filegroup = nonEmpty(f.FileGroup)
		}
		if hasPages(f) {
			if r.usedKnown() {
				used, free, freePct = u.cell(f.SpaceUsedKB), u.cell(f.SizeKB-f.SpaceUsedKB), pctCell(f.SizeKB-f.SpaceUsedKB, f.SizeKB)
			}
			growth, maxSize = ptr(growthText(f, u)), ptr(maxSizeText(f, u))
		}
		if r.Vol != nil {
			volFree = ptr(mbf(r.Vol.FreePct))
		}
		var row []*string
		if withDatabase {
			row = append(row, ptr(r.Database))
		}
		set.Rows = append(set.Rows, append(row, ptr(f.Name), ptr(fileTypeName(f.Type)), filegroup, ptr(f.PhysicalName),
			u.cell(f.SizeKB), used, free, freePct, growth, maxSize, volFree))
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// growthText is f's autogrowth: "10 %", "none", or a size with its unit —
// KB under --unit kb, else MB (no ".00" on a whole figure): in GB to two
// decimals the usual 1 MB or 64 MB increment would read 0.
func growthText(f *gosmo.DatabaseFileInfo, u sizeUnit) string {
	switch {
	case f.IsPercentGrowth:
		return fmt.Sprintf("%d %%", f.GrowthPercent)
	case f.GrowthKB == 0:
		return "none"
	case u == unitKB:
		return u.text(f.GrowthKB) + " KB"
	}
	return strings.TrimSuffix(mb(f.GrowthKB), ".00") + " MB"
}

// maxSizeText is f's max_size in u, or "unlimited". A log's MAXSIZE =
// UNLIMITED reads as its 2 TB ceiling, as SSMS shows it.
func maxSizeText(f *gosmo.DatabaseFileInfo, u sizeUnit) string {
	if f.MaxSizeKB < 0 {
		return "unlimited"
	}
	return u.text(f.MaxSizeKB)
}

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
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var dbs []storageDB
	if all {
		dbs, err = storageDatabases(ctx, cat, true, res)
	} else {
		var t storageDB
		t, err = storageDatabase(ctx, cat, name, res)
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
		sp, err := cat.SpaceReport(ctx, t.DB)
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
			ls, err := cat.LogSpace(ctx, t.DB)
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
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var dbs []storageDB
	if all {
		dbs, err = storageDatabases(ctx, cat, false, res)
	} else {
		var t storageDB
		t, err = storageDatabase(ctx, cat, name, res)
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
		ls, err := cat.LogSpace(ctx, t.DB)
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
