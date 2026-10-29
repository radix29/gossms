package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// profileOption is --profile, the saved connection a command connects
// through; the connection options override its values.
func profileOption() spec.Option {
	return spec.Option{Name: optProfile, Short: "p", Type: spec.TypeString, Env: "GOSSMS_PROFILE",
		Help: "Saved connection profile (gossms profile list); connection options override its values"}
}

// ConnectOptions is --profile and the connection options: what every command
// that reads the catalog takes.
func ConnectOptions() []spec.Option {
	return append([]spec.Option{profileOption()}, connect.Options()...)
}

// Catalog is the gosmo reads the discovery commands make, so tests stand in
// for a server. Methods taking a *gosmo.Database call that database's own
// method; the database is one DatabaseByName or Databases returned.
type Catalog interface {
	CurrentDatabase(ctx context.Context) (string, error)
	Databases(ctx context.Context) ([]*gosmo.Database, error)
	DatabaseByName(ctx context.Context, name string) (*gosmo.Database, error)
	DatabaseSizes(ctx context.Context) (map[int]gosmo.DatabaseSize, error)
	DatabaseOptions(ctx context.Context, d *gosmo.Database) (*gosmo.DatabaseOptions, error)
	SpaceUsed(ctx context.Context, d *gosmo.Database) (gosmo.SpaceInfo, error)
	ObjectByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error)
	ObjectsNamed(ctx context.Context, d *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error)
	Search(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error)

	// object list: one listing per family.
	Tables(ctx context.Context, d *gosmo.Database, q gosmo.TableQuery) ([]*gosmo.Table, error)
	Views(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error)
	SystemViews(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error)
	StoredProcedures(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error)
	SystemStoredProcedures(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error)
	UserDefinedFunctions(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error)
	SystemFunctions(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error)
	Triggers(ctx context.Context, d *gosmo.Database) ([]*gosmo.Trigger, error)
	Synonyms(ctx context.Context, d *gosmo.Database) ([]*gosmo.Synonym, error)
	Sequences(ctx context.Context, d *gosmo.Database) ([]*gosmo.Sequence, error)
	UserDefinedDataTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.UserDefinedDataType, error)
	UserDefinedTableTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.UserDefinedTableType, error)
	ClrTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.ClrType, error)
	Schemas(ctx context.Context, d *gosmo.Database) ([]*gosmo.Schema, error)

	// object search --in definition / column.
	SearchDefinitions(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error)
	SearchColumns(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.ColumnSearchResult, error)

	// object describe: the family's by-name read, then its children. A
	// *gosmo.Table or *gosmo.View is one TableByName or ViewByName returned.
	TableByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Table, error)
	ViewByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.View, error)
	TriggerByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Trigger, error)
	SynonymByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Synonym, error)
	SequenceByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Sequence, error)
	TableColumns(ctx context.Context, t *gosmo.Table) ([]*gosmo.Column, error)
	TableIndexes(ctx context.Context, t *gosmo.Table) ([]*gosmo.Index, error)
	ForeignKeys(ctx context.Context, t *gosmo.Table) ([]*gosmo.ForeignKey, error)
	CheckConstraints(ctx context.Context, t *gosmo.Table) ([]*gosmo.CheckConstraint, error)
	TableTriggers(ctx context.Context, t *gosmo.Table) ([]*gosmo.Trigger, error)
	RowCount(ctx context.Context, t *gosmo.Table) (int64, error)
	ViewColumns(ctx context.Context, v *gosmo.View) ([]*gosmo.Column, error)
	ViewIndexes(ctx context.Context, v *gosmo.View) ([]*gosmo.Index, error)
	ViewTriggers(ctx context.Context, v *gosmo.View) ([]*gosmo.Trigger, error)
	Parameters(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Parameter, error)
	ReturnType(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Parameter, error)

	// object definition: the module's T-SQL text, "" when it has none to
	// read (encrypted, CLR).
	ModuleDefinition(ctx context.Context, d *gosmo.Database, kind ModuleKind, schema, name string) (string, error)

	// object dependencies: the edges out of and into schema.name.
	Dependencies(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error)
	Dependents(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error)

	// object indexes / stats, on a table or an indexed view.
	TableIndexUsage(ctx context.Context, t *gosmo.Table) ([]*gosmo.IndexUsage, error)
	ViewIndexUsage(ctx context.Context, v *gosmo.View) ([]*gosmo.IndexUsage, error)
	TableFragmentation(ctx context.Context, t *gosmo.Table, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error)
	ViewFragmentation(ctx context.Context, v *gosmo.View, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error)
	TableStatistics(ctx context.Context, t *gosmo.Table) ([]*gosmo.Statistic, error)
	ViewStatistics(ctx context.Context, v *gosmo.View) ([]*gosmo.Statistic, error)
	StatisticColumns(ctx context.Context, st *gosmo.Statistic) ([]string, error)

	// agent context: what connect read, and what the login may do.
	ServerInfo() *gosmo.ServerInfo
	ServerCapabilities(ctx context.Context) (*gosmo.Capabilities, error)
	DatabaseCapabilities(ctx context.Context, d *gosmo.Database) (*gosmo.DatabaseCapabilities, error)
	DatabaseAccess(ctx context.Context) (map[int]bool, error)

	// The storage commands: files read inside the database and from the
	// server catalog, the volume behind every file, and the sp_spaceused and
	// log figures.
	Files(ctx context.Context, d *gosmo.Database) ([]*gosmo.DatabaseFileInfo, error)
	DatabaseFiles(ctx context.Context, name string) ([]*gosmo.DatabaseFileInfo, error)
	FileVolumes(ctx context.Context) (vols []gosmo.FileVolume, visible bool, err error)
	SpaceReport(ctx context.Context, d *gosmo.Database) (gosmo.SpaceReport, error)
	TableSizes(ctx context.Context, d *gosmo.Database) ([]*gosmo.TableSize, error)
	LogSpace(ctx context.Context, d *gosmo.Database) (gosmo.LogSpace, error)

	// server info / memory and server config: the sys.dm_os_* reads and
	// sys.configurations.
	ProcessorInfo(ctx context.Context) (*gosmo.ProcessorInfo, error)
	StartTime(ctx context.Context) (start time.Time, uptime time.Duration, err error)
	MemoryStats(ctx context.Context) (*gosmo.ServerMemoryStats, error)
	MemoryClerks(ctx context.Context) ([]gosmo.MemoryClerk, error)
	PerformanceCounters(ctx context.Context, counters, instances []string) ([]gosmo.PerformanceCounter, error)
	Configurations(ctx context.Context) ([]*gosmo.ConfigurationOption, error)
}

// ModuleKind is which family's Definition ModuleDefinition reads.
type ModuleKind int

const (
	ModuleView ModuleKind = iota
	ModuleProcedure
	ModuleFunction
	ModuleTrigger
)

// ServerCatalog is Deps.Catalog: the reads, made through sc's gosmo Server.
func ServerCatalog(sc *db.ServerConn) Catalog { return serverCatalog{sc.Server} }

type serverCatalog struct{ s *gosmo.Server }

func (c serverCatalog) CurrentDatabase(ctx context.Context) (string, error) {
	return c.s.CurrentDatabase(ctx)
}

func (c serverCatalog) Databases(ctx context.Context) ([]*gosmo.Database, error) {
	return c.s.Databases(ctx)
}

func (c serverCatalog) DatabaseByName(ctx context.Context, name string) (*gosmo.Database, error) {
	return c.s.DatabaseByName(ctx, name)
}

func (c serverCatalog) DatabaseSizes(ctx context.Context) (map[int]gosmo.DatabaseSize, error) {
	return c.s.DatabaseSizes(ctx)
}

func (serverCatalog) DatabaseOptions(ctx context.Context, d *gosmo.Database) (*gosmo.DatabaseOptions, error) {
	return d.Options(ctx)
}

func (serverCatalog) SpaceUsed(ctx context.Context, d *gosmo.Database) (gosmo.SpaceInfo, error) {
	return d.SpaceUsed(ctx)
}

func (serverCatalog) ObjectByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	return d.ObjectByName(ctx, schema, name)
}

func (serverCatalog) ObjectsNamed(ctx context.Context, d *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error) {
	return d.ObjectsNamed(ctx, name)
}

func (serverCatalog) Search(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	return d.Search(ctx, pattern)
}

func (serverCatalog) Tables(ctx context.Context, d *gosmo.Database, q gosmo.TableQuery) ([]*gosmo.Table, error) {
	return d.Tables(ctx, q)
}

func (serverCatalog) Views(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error) {
	return d.Views(ctx, f)
}

func (serverCatalog) SystemViews(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error) {
	return d.SystemViews(ctx, f)
}

func (serverCatalog) StoredProcedures(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error) {
	return d.StoredProcedures(ctx, f)
}

func (serverCatalog) SystemStoredProcedures(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error) {
	return d.SystemStoredProcedures(ctx, f)
}

func (serverCatalog) UserDefinedFunctions(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error) {
	return d.UserDefinedFunctions(ctx, f)
}

func (serverCatalog) SystemFunctions(ctx context.Context, d *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error) {
	return d.SystemFunctions(ctx, f)
}

func (serverCatalog) Triggers(ctx context.Context, d *gosmo.Database) ([]*gosmo.Trigger, error) {
	return d.Triggers(ctx)
}

func (serverCatalog) Synonyms(ctx context.Context, d *gosmo.Database) ([]*gosmo.Synonym, error) {
	return d.Synonyms(ctx)
}

func (serverCatalog) Sequences(ctx context.Context, d *gosmo.Database) ([]*gosmo.Sequence, error) {
	return d.Sequences(ctx)
}

func (serverCatalog) UserDefinedDataTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.UserDefinedDataType, error) {
	return d.UserDefinedDataTypes(ctx)
}

func (serverCatalog) UserDefinedTableTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.UserDefinedTableType, error) {
	return d.UserDefinedTableTypes(ctx)
}

func (serverCatalog) ClrTypes(ctx context.Context, d *gosmo.Database) ([]*gosmo.ClrType, error) {
	return d.ClrTypes(ctx)
}

func (serverCatalog) Schemas(ctx context.Context, d *gosmo.Database) ([]*gosmo.Schema, error) {
	return d.Schemas(ctx)
}

func (serverCatalog) SearchDefinitions(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	return d.SearchDefinitions(ctx, pattern)
}

func (serverCatalog) SearchColumns(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.ColumnSearchResult, error) {
	return d.SearchColumns(ctx, pattern)
}

func (serverCatalog) TableByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return d.TableByName(ctx, schema, name)
}

func (serverCatalog) ViewByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.View, error) {
	return d.ViewByName(ctx, schema, name)
}

func (serverCatalog) TriggerByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Trigger, error) {
	return d.TriggerByName(ctx, schema, name)
}

func (serverCatalog) SynonymByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Synonym, error) {
	return d.SynonymByName(ctx, schema, name)
}

func (serverCatalog) SequenceByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Sequence, error) {
	return d.SequenceByName(ctx, schema, name)
}

func (serverCatalog) TableColumns(ctx context.Context, t *gosmo.Table) ([]*gosmo.Column, error) {
	return t.Columns(ctx)
}

func (serverCatalog) TableIndexes(ctx context.Context, t *gosmo.Table) ([]*gosmo.Index, error) {
	return t.Indexes(ctx)
}

func (serverCatalog) ForeignKeys(ctx context.Context, t *gosmo.Table) ([]*gosmo.ForeignKey, error) {
	return t.ForeignKeys(ctx)
}

func (serverCatalog) CheckConstraints(ctx context.Context, t *gosmo.Table) ([]*gosmo.CheckConstraint, error) {
	return t.CheckConstraints(ctx)
}

func (serverCatalog) TableTriggers(ctx context.Context, t *gosmo.Table) ([]*gosmo.Trigger, error) {
	return t.Triggers(ctx)
}

func (serverCatalog) RowCount(ctx context.Context, t *gosmo.Table) (int64, error) {
	return t.RowCount(ctx)
}

func (serverCatalog) ViewColumns(ctx context.Context, v *gosmo.View) ([]*gosmo.Column, error) {
	return v.Columns(ctx)
}

func (serverCatalog) ViewIndexes(ctx context.Context, v *gosmo.View) ([]*gosmo.Index, error) {
	return v.Indexes(ctx)
}

func (serverCatalog) ViewTriggers(ctx context.Context, v *gosmo.View) ([]*gosmo.Trigger, error) {
	return v.Triggers(ctx)
}

func (serverCatalog) Parameters(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Parameter, error) {
	return d.Parameters(ctx, schema, name)
}

func (serverCatalog) ReturnType(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.Parameter, error) {
	return d.ReturnType(ctx, schema, name)
}

// ModuleDefinition reads through the family's Ref: Definition reads by name,
// so the handle needs nothing loaded.
func (serverCatalog) ModuleDefinition(ctx context.Context, d *gosmo.Database, kind ModuleKind, schema, name string) (string, error) {
	switch kind {
	case ModuleView:
		return d.ViewRef(schema, name).Definition(ctx)
	case ModuleProcedure:
		return d.StoredProcedureRef(schema, name).Definition(ctx)
	case ModuleFunction:
		return d.UserDefinedFunctionRef(schema, name).Definition(ctx)
	case ModuleTrigger:
		return d.TriggerRef(schema, name).Definition(ctx)
	}
	return "", fmt.Errorf("module kind %d has no definition read", kind)
}

func (serverCatalog) Dependencies(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error) {
	return d.Dependencies(ctx, schema, name)
}

func (serverCatalog) Dependents(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error) {
	return d.Dependents(ctx, schema, name)
}

func (serverCatalog) TableIndexUsage(ctx context.Context, t *gosmo.Table) ([]*gosmo.IndexUsage, error) {
	return t.IndexUsage(ctx)
}

func (serverCatalog) ViewIndexUsage(ctx context.Context, v *gosmo.View) ([]*gosmo.IndexUsage, error) {
	return v.IndexUsage(ctx)
}

func (serverCatalog) TableFragmentation(ctx context.Context, t *gosmo.Table, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	return t.FragmentationStats(ctx, mode)
}

func (serverCatalog) ViewFragmentation(ctx context.Context, v *gosmo.View, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	return v.FragmentationStats(ctx, mode)
}

func (serverCatalog) TableStatistics(ctx context.Context, t *gosmo.Table) ([]*gosmo.Statistic, error) {
	return t.Statistics(ctx)
}

func (serverCatalog) ViewStatistics(ctx context.Context, v *gosmo.View) ([]*gosmo.Statistic, error) {
	return v.Statistics(ctx)
}

func (serverCatalog) StatisticColumns(ctx context.Context, st *gosmo.Statistic) ([]string, error) {
	return st.Columns(ctx)
}

func (c serverCatalog) ServerInfo() *gosmo.ServerInfo { return c.s.Info() }

func (c serverCatalog) ServerCapabilities(ctx context.Context) (*gosmo.Capabilities, error) {
	return c.s.Capabilities(ctx)
}

func (serverCatalog) DatabaseCapabilities(ctx context.Context, d *gosmo.Database) (*gosmo.DatabaseCapabilities, error) {
	return d.Capabilities(ctx)
}

func (c serverCatalog) DatabaseAccess(ctx context.Context) (map[int]bool, error) {
	return c.s.DatabaseAccess(ctx)
}

func (serverCatalog) Files(ctx context.Context, d *gosmo.Database) ([]*gosmo.DatabaseFileInfo, error) {
	return d.Files(ctx)
}

func (c serverCatalog) DatabaseFiles(ctx context.Context, name string) ([]*gosmo.DatabaseFileInfo, error) {
	return c.s.DatabaseFiles(ctx, name)
}

func (c serverCatalog) FileVolumes(ctx context.Context) ([]gosmo.FileVolume, bool, error) {
	return c.s.FileVolumes(ctx)
}

func (serverCatalog) SpaceReport(ctx context.Context, d *gosmo.Database) (gosmo.SpaceReport, error) {
	return d.SpaceReport(ctx)
}

func (serverCatalog) TableSizes(ctx context.Context, d *gosmo.Database) ([]*gosmo.TableSize, error) {
	return d.TableSizes(ctx)
}

func (serverCatalog) LogSpace(ctx context.Context, d *gosmo.Database) (gosmo.LogSpace, error) {
	return d.LogSpace(ctx)
}

func (c serverCatalog) ProcessorInfo(ctx context.Context) (*gosmo.ProcessorInfo, error) {
	return c.s.ProcessorInfo(ctx)
}

func (c serverCatalog) StartTime(ctx context.Context) (time.Time, time.Duration, error) {
	return c.s.StartTime(ctx)
}

func (c serverCatalog) MemoryStats(ctx context.Context) (*gosmo.ServerMemoryStats, error) {
	return c.s.MemoryStats(ctx)
}

func (c serverCatalog) MemoryClerks(ctx context.Context) ([]gosmo.MemoryClerk, error) {
	return c.s.MemoryClerks(ctx)
}

func (c serverCatalog) PerformanceCounters(ctx context.Context, counters, instances []string) ([]gosmo.PerformanceCounter, error) {
	return c.s.PerformanceCounters(ctx, counters, instances)
}

func (c serverCatalog) Configurations(ctx context.Context) ([]*gosmo.ConfigurationOption, error) {
	return c.s.Configurations(ctx)
}

// dial resolves and opens the connection inv asks for through profile (empty
// ad hoc) in cfg. res's header names the target from the start, so a failure
// says where it failed.
func (d Deps) dial(ctx context.Context, inv *spec.Invocation, cfg *config.Config, profile string, res *output.Result) (*connect.Target, *db.ServerConn, error) {
	t, err := connect.Resolve(inv, cfg, profile)
	if err != nil {
		return nil, nil, err
	}
	res.Server, res.Database = connect.Address(t.Conn), t.Conn.Database
	if t.SavedPasswordDropped {
		res.Warnings = append(res.Warnings,
			"The profile's saved password was not sent: an override changed the server, login, authentication or TLS.")
	}
	sc, err := connect.Open(ctx, t, d.Connect)
	if err != nil {
		return nil, nil, err
	}
	return t, sc, nil
}

// connectCatalog connects as inv asks and returns the catalog reads over the
// connection; the caller closes sc.
func (d Deps) connectCatalog(ctx context.Context, inv *spec.Invocation, res *output.Result) (*db.ServerConn, Catalog, error) {
	cfg, profile, _, err := d.profileCeiling(inv)
	if err != nil {
		return nil, nil, err
	}
	_, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return nil, nil, err
	}
	return sc, d.Catalog(sc), nil
}

// connectDatabase is connectCatalog plus the connection's database — the
// profile's, or -d's — read from the catalog, which the object commands look
// in. The envelope's database is its catalog spelling.
func (d Deps) connectDatabase(ctx context.Context, inv *spec.Invocation, res *output.Result) (*db.ServerConn, Catalog, *gosmo.Database, error) {
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return nil, nil, nil, err
	}
	dbase, err := currentDatabase(ctx, cat, res)
	if err != nil {
		sc.Close()
		return nil, nil, nil, err
	}
	return sc, cat, dbase, nil
}

// currentDatabase is the connection's database read from the catalog; res's
// database becomes its catalog spelling.
func currentDatabase(ctx context.Context, cat Catalog, res *output.Result) (*gosmo.Database, error) {
	name, err := cat.CurrentDatabase(ctx)
	if err != nil {
		return nil, readError(err)
	}
	dbase, err := cat.DatabaseByName(ctx, name)
	if err != nil {
		return nil, readError(err)
	}
	res.Database = dbase.Name
	return dbase, nil
}

// readError maps a gosmo read's failure to the envelope's code: NOT_FOUND for
// gosmo's ErrNotFound, PERMISSION_DENIED when the server says the login lacks
// a permission, UNSUPPORTED for a request gosmo or the server's version has
// no form for, SQL_ERROR for any other server error, CONNECTION_FAILED for a
// broken connection, GENERAL_ERROR otherwise. An *output.Error passes through.
func readError(err error) *output.Error {
	if e, ok := errors.AsType[*output.Error](err); ok {
		return e
	}
	msg := err.Error()
	switch {
	case errors.Is(err, gosmo.ErrNotFound):
		return output.NewError(output.CodeNotFound, msg, "")
	case errors.Is(err, gosmo.ErrUnsupported), errors.Is(err, gosmo.ErrUnsupportedVersion):
		return output.NewError(output.CodeUnsupported, msg, "")
	case gosmo.IsMissingOrDenied(err):
		return output.NewError(output.CodeNotFound, msg,
			"The server does not say which: the object is missing, or the login may not see it.")
	}
	if se, ok := gosmo.AsSQLError(err); ok {
		e := sqlError(se)
		if e.Code == output.CodePermissionDenied {
			// sqlError's hint is query run's, about a script.
			e.Hint = "The login lacks a permission this read needs: the message names the object. " +
				"Use a login that has it, or have it granted."
		}
		return e
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return output.NewError(output.CodeConnectionFailed, msg, "")
	}
	return output.NewError(output.CodeGeneralError, msg, "")
}

// maxSuggestions bounds a did-you-mean list.
const maxSuggestions = 5

// resolveObject finds the object n names in dbase. A two-part name is looked
// up as given; a one-part name across every schema (D2): one match is that
// object, several are a usage error listing them. Nothing found is NOT_FOUND
// with the objects whose names contain n's as the hint — which also catches a
// case-only miss in a case-sensitive database.
func resolveObject(ctx context.Context, cat Catalog, dbase *gosmo.Database, n ObjectName) (*gosmo.ObjectInfo, error) {
	if n.Schema != "" {
		o, err := cat.ObjectByName(ctx, dbase, n.Schema, n.Name)
		if errors.Is(err, gosmo.ErrNotFound) {
			return nil, objectNotFound(ctx, cat, dbase, n)
		}
		if err != nil {
			return nil, readError(err)
		}
		return o, nil
	}
	found, err := cat.ObjectsNamed(ctx, dbase, n.Name)
	if err != nil {
		return nil, readError(err)
	}
	switch len(found) {
	case 0:
		return nil, objectNotFound(ctx, cat, dbase, n)
	case 1:
		return found[0], nil
	}
	names := make([]string, len(found))
	for i, o := range found {
		names[i] = gosmo.QualifiedName(o.Schema, o.Name)
	}
	return nil, usageError(fmt.Sprintf("%s is in %d schemas: %s", gosmo.QuoteName(n.Name), len(found), strings.Join(names, ", ")),
		"Name the schema: schema.name.")
}

// objectNotFound is NOT_FOUND for n in dbase, suggesting the objects whose
// names contain n's. A failed suggestion read leaves the hint out rather than
// replacing the answer.
func objectNotFound(ctx context.Context, cat Catalog, dbase *gosmo.Database, n ObjectName) *output.Error {
	msg := fmt.Sprintf("no object %s in database %s", n, gosmo.QuoteName(dbase.Name))
	hint := "Check the name, or pass -d to look in another database."
	if similar, err := cat.Search(ctx, dbase, n.Name); err == nil && len(similar) > 0 {
		names := make([]string, 0, maxSuggestions)
		for _, r := range similar {
			name := gosmo.QualifiedName(r.Schema, r.Name)
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
			if len(names) == maxSuggestions {
				break
			}
		}
		hint = "Did you mean " + strings.Join(names, ", ") + "?"
	}
	return output.NewError(output.CodeNotFound, msg, hint)
}
