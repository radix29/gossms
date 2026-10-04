package tui

import (
	"context"
	"iter"
	"maps"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// nameSet is the set of names a New-object dialog's "already exists"
// preflight checks against. Whether two names are the same one is the
// collation's call, not ours: on a case-sensitive or binary collation
// (…_CS_…, …_BIN, …_BIN2) `Sales` and `sales` are two principals, and a
// lowered map refused the second with "already exists" although the server
// would have created it. The collation is the one of the scope the object
// lives in — the server's for logins, databases and other server objects, the
// database's for its principals, keys and indexes, msdb's for Agent objects.
//
// A nil *nameSet is an empty one, so Has is safe on a prefetch that never
// filled it.
type nameSet struct {
	collation string
	m         map[string]struct{}
}

// newNameSet returns a set comparing names the way collation does, seeded
// with names. An empty collation — not read — folds, which is the
// case-insensitive default every install ships with.
func newNameSet(collation string, names ...string) *nameSet {
	s := &nameSet{collation: collation, m: make(map[string]struct{}, len(names))}
	for _, n := range names {
		s.Add(n)
	}
	return s
}

// key is gosmo.NameKey, so the set agrees with gosmo.SameName rune for rune —
// a lowered key split names EqualFold joins (`ſ`/`s`, final `ς`/`σ`).
func (s *nameSet) key(name string) string { return gosmo.NameKey(s.collation, name) }

// Add puts name in the set.
func (s *nameSet) Add(name string) { s.m[s.key(name)] = struct{}{} }

// Has reports whether name is in the set under the set's collation.
func (s *nameSet) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.m[s.key(name)]
	return ok
}

// nameMap is nameSet with a value per name: a map keyed by server-supplied
// names, where which keys are the same one is the collation's call. A nil
// *nameMap is an empty one.
type nameMap[V any] struct {
	collation string
	m         map[string]V
}

// newNameMap returns an empty map comparing names the way collation does.
func newNameMap[V any](collation string) *nameMap[V] {
	return &nameMap[V]{collation: collation, m: map[string]V{}}
}

// Set stores v under name.
func (m *nameMap[V]) Set(name string, v V) { m.m[gosmo.NameKey(m.collation, name)] = v }

// Get returns the value stored under name under the map's collation.
func (m *nameMap[V]) Get(name string) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}
	v, ok := m.m[gosmo.NameKey(m.collation, name)]
	return v, ok
}

// Values yields every stored value, in no particular order.
func (m *nameMap[V]) Values() iter.Seq[V] {
	if m == nil {
		return func(func(V) bool) {}
	}
	return maps.Values(m.m)
}

// serverCollation is the instance's default collation, which governs
// server-scoped names (logins, databases, credentials, audits, endpoints,
// availability groups), or "" when there is no server info.
func serverCollation(sc *db.ServerConn) string {
	if sc == nil {
		return ""
	}
	return instanceCollation(sc.Server)
}

// instanceCollation is serverCollation for a server handle that is not the
// connection's own — an availability group's primary, reached through a
// follow.
func instanceCollation(s *gosmo.Server) string {
	if s == nil || s.Info() == nil {
		return ""
	}
	return s.Info().Collation
}

// msdbCollation is msdb's collation, which governs the Agent's object names
// (jobs, schedules, operators, alerts). msdb normally has the server's
// collation, and a failed read falls back to it rather than failing the
// dialog over a nicety the server enforces anyway.
func msdbCollation(ctx context.Context, sc *db.ServerConn) string {
	if d, err := sc.Server.DatabaseByName(ctx, "msdb"); err == nil && d.Collation != "" {
		return d.Collation
	}
	return serverCollation(sc)
}

// databaseCollation is the collation every name inside d compares under, or
// "" for a nil handle: its catalog collation, which differs from its data
// collation in a partially contained database (case-insensitive whatever the
// data says) and an Azure SQL Database created WITH CATALOG_COLLATION. A
// DatabaseRef handle carries neither, and a Database built by hand only the
// data collation, so that is the fallback.
func databaseCollation(d *gosmo.Database) string {
	if d == nil {
		return ""
	}
	if d.CatalogCollation != "" {
		return d.CatalogCollation
	}
	return d.Collation
}
