package cmd

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/db"
)

// catalogServer is real gosmo over canned rows (gosmotest) for the handlers
// that read gosmo directly past resolving their object: the connection's
// database, objects resolved by name, and the tables' and views' by-name
// reads and columns. A test adds what else its command reads with On or
// OnFunc before the first command runs; rules answer in registration order,
// so a caller's more specific rule goes in through newCatalogServerWith.
type catalogServer struct {
	*gosmotest.Fake
	t       *testing.T
	current string // the connection's database
	objects []*gosmo.ObjectInfo
	// columns is each table's or view's columns, by object id.
	columns map[int][]*gosmo.Column
}

var catalogAt = time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)

func newCatalogServer(t *testing.T, current string, objects ...*gosmo.ObjectInfo) *catalogServer {
	return newCatalogServerWith(t, current, nil, objects...)
}

// newCatalogServerWith is newCatalogServer with first's rules ahead of the
// catalog's.
func newCatalogServerWith(t *testing.T, current string, first func(*catalogServer), objects ...*gosmo.ObjectInfo) *catalogServer {
	s := &catalogServer{Fake: gosmotest.New(t), t: t, current: current, objects: objects, columns: map[int][]*gosmo.Column{}}
	if first != nil {
		first(s)
	}
	s.rules()
	return s
}

// conn is a ServerConn over s, for Deps.Connect.
func (s *catalogServer) conn(ctx context.Context) (*db.ServerConn, error) {
	srv, err := s.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &db.ServerConn{Server: srv}, nil
}

// has is whether q's text contains every one of subs.
func has(q gosmotest.Query, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(q.Text, sub) {
			return false
		}
	}
	return true
}

// on answers the queries containing every one of subs with fn's rows.
func (s *catalogServer) on(fn func(gosmotest.Query) [][]any, subs ...string) {
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) },
		func(q gosmotest.Query) gosmotest.Result {
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: fn(q)}}}
		})
}

func argString(q gosmotest.Query, i int) string { s, _ := q.Arg(i).(string); return s }

// object is s's object schema.name of one of types, nil when there is none.
func (s *catalogServer) object(schema, name string, types ...string) *gosmo.ObjectInfo {
	for _, o := range s.objects {
		if o.Schema == schema && o.Name == name && (len(types) == 0 || slices.Contains(types, o.Type)) {
			return o
		}
	}
	return nil
}

func (s *catalogServer) rules() {
	s.on(func(gosmotest.Query) [][]any { return [][]any{{s.current}} }, "SELECT DB_NAME()")
	s.on(func(q gosmotest.Query) [][]any {
		if !strings.EqualFold(argString(q, 0), s.current) {
			return nil
		}
		return [][]any{clitest.DatabaseRow(s.current, 5, catalogAt)}
	}, "FROM sys.databases", "WHERE name = @p1")
	// ObjectByName and ObjectsNamed: the catalog compares names exactly.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, o := range s.objects {
			if has(q, "@p2") && o.Schema == q.Arg(0) && o.Name == q.Arg(1) || !has(q, "@p2") && o.Name == q.Arg(0) {
				rows = append(rows, clitest.ObjectInfoRow(o))
			}
		}
		return rows
	}, "OBJECT_SCHEMA_NAME(o.parent_object_id)", "FROM   sys.objects o")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, o := range s.objects {
			if strings.Contains(strings.ToLower(o.Name), strings.ToLower(argString(q, 0))) {
				rows = append(rows, []any{o.Schema, o.Name, o.TypeDesc})
			}
		}
		return rows
	}, "LOWER(o.name) LIKE '%' + LOWER(@p1) + '%'")
	s.on(func(q gosmotest.Query) [][]any {
		o := s.object(argString(q, 0), argString(q, 1), "U")
		if o == nil {
			return nil
		}
		return [][]any{clitest.TableRow(&gosmo.Table{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name}, catalogAt)}
	}, "FROM   sys.tables t", "AND  t.name")
	s.on(func(q gosmotest.Query) [][]any {
		o := s.object(argString(q, 0), argString(q, 1), "V")
		if o == nil {
			return nil
		}
		return [][]any{clitest.ViewRow(&gosmo.View{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name, IsSchemaBound: true}, catalogAt)}
	}, "FROM   sys.views o", "AND  o.name")
	s.on(func(q gosmotest.Query) [][]any {
		id, _ := q.Arg(0).(int64)
		var rows [][]any
		for _, c := range s.columns[int(id)] {
			rows = append(rows, clitest.ColumnRow(c))
		}
		return rows
	}, "FROM   sys.columns c", "WHERE  c.object_id = @p1")
	s.on(func(gosmotest.Query) [][]any { return [][]any{{1252}} }, "COLLATIONPROPERTY(@p1, 'CodePage')")
}
