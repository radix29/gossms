package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

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

// connectOptions is --profile and the connection options: what every command
// that reads the catalog takes.
func connectOptions() []spec.Option {
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
}

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
	name, err := cat.CurrentDatabase(ctx)
	if err != nil {
		sc.Close()
		return nil, nil, nil, readError(err)
	}
	dbase, err := cat.DatabaseByName(ctx, name)
	if err != nil {
		sc.Close()
		return nil, nil, nil, readError(err)
	}
	res.Database = dbase.Name
	return sc, cat, dbase, nil
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
		return sqlError(se)
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
