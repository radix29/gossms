package tui

import (
	"context"
	"strings"

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
	fold bool
	m    map[string]struct{}
}

// newNameSet returns a set comparing names the way collation does, seeded
// with names. An empty collation — not read — folds, which is the
// case-insensitive default every install ships with.
func newNameSet(collation string, names ...string) *nameSet {
	s := &nameSet{fold: collationFoldsCase(collation), m: make(map[string]struct{}, len(names))}
	for _, n := range names {
		s.Add(n)
	}
	return s
}

// collationFoldsCase reports whether collation compares names without regard
// to case: every collation but a CS one or a binary one. Matched by whole
// "_"-separated token, so a collation whose name merely contains the letters
// is not mistaken for one.
func collationFoldsCase(collation string) bool {
	for tok := range strings.SplitSeq(strings.ToUpper(collation), "_") {
		switch tok {
		case "CS", "BIN", "BIN2":
			return false
		}
	}
	return true
}

// sameName reports whether a and b name the same object under collation —
// the one-off form of a nameSet lookup, for a comparison against a single
// name rather than a set of them.
func sameName(collation, a, b string) bool {
	if collationFoldsCase(collation) {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func foldName(fold bool, name string) string {
	if fold {
		return strings.ToLower(name)
	}
	return name
}

func (s *nameSet) key(name string) string { return foldName(s.fold, name) }

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
	fold bool
	m    map[string]V
}

// newNameMap returns an empty map comparing names the way collation does.
func newNameMap[V any](collation string) *nameMap[V] {
	return &nameMap[V]{fold: collationFoldsCase(collation), m: map[string]V{}}
}

// Set stores v under name.
func (m *nameMap[V]) Set(name string, v V) { m.m[foldName(m.fold, name)] = v }

// Get returns the value stored under name under the map's collation.
func (m *nameMap[V]) Get(name string) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}
	v, ok := m.m[foldName(m.fold, name)]
	return v, ok
}

// Len is the number of distinct names in the map.
func (m *nameMap[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.m)
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

// databaseCollation is d's collation, which governs every name inside it, or
// "" for a nil handle.
func databaseCollation(d *gosmo.Database) string {
	if d == nil {
		return ""
	}
	return d.Collation
}
