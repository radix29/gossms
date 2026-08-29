package db

import (
	"context"
	"sync"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// capabilityProbeTimeout bounds one capability probe. Both probes are single
// round trips against catalog functions with no I/O behind them, so this is a
// liveness bound rather than a work budget: the server probe runs inside
// Connect, where a hang would look like a hung login dialog.
const capabilityProbeTimeout = 10 * time.Second

// capabilityFields is the ServerConn state behind Capabilities and
// DatabaseCapabilities, kept in its own struct so connection.go's ServerConn
// stays about the connection.
type capabilityFields struct {
	// caps is probed once inside Connect. Never nil after that — a probe that
	// failed leaves an empty one, which reads as "unknown" throughout.
	caps *gosmo.Capabilities

	mu     sync.Mutex
	dbCaps map[string]*gosmo.DatabaseCapabilities
}

// Capabilities reports what the connected login may do at the server scope.
//
// Never nil, and never an error: the probe runs once inside Connect, and a
// failure leaves every answer CapabilityUnknown rather than failing the
// connection. That is the fail-open rule this whole layer follows — see
// gosmo.Capabilities.Allows, and gate on Allows rather than Has anywhere the
// answer decides whether to *withhold* something.
func (sc *ServerConn) Capabilities() *gosmo.Capabilities {
	if sc == nil || sc.caps == nil {
		return &gosmo.Capabilities{}
	}
	return sc.caps
}

// DatabaseCapabilities reports what the connected login may do inside one
// database, probing on first use and caching the answer for the life of the
// connection.
//
// Never nil, and never an error. A probe that failed comes back as
// Accessible with no rights known — "we could not ask", which reads the same
// as an unrestricted login through Allows and so changes nothing. Only an
// answer the server actually gave reports Accessible false, which is the
// signal for "this database cannot be opened at all"; deriving that from a
// failed probe instead would hide databases over a dropped connection.
//
// Two round trips on the first call for a database, none afterwards.
func (sc *ServerConn) DatabaseCapabilities(ctx context.Context, name string) *gosmo.DatabaseCapabilities {
	if sc == nil || sc.Server == nil {
		return unknownDatabaseCapabilities()
	}

	sc.mu.Lock()
	if c, ok := sc.dbCaps[name]; ok {
		sc.mu.Unlock()
		return c
	}
	sc.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, capabilityProbeTimeout)
	defer cancel()

	c, err := sc.Server.Database(name).CapabilitiesContext(ctx)
	if err != nil {
		// Deliberately not cached: a probe that failed for a transient reason
		// would otherwise keep answering "unknown" for the whole session.
		return unknownDatabaseCapabilities()
	}

	sc.mu.Lock()
	if sc.dbCaps == nil {
		sc.dbCaps = map[string]*gosmo.DatabaseCapabilities{}
	}
	sc.dbCaps[name] = c
	sc.mu.Unlock()
	return c
}

// CachedDatabaseCapabilities is DatabaseCapabilities without the round trip:
// it answers from the cache and never touches the network.
//
// This is the accessor for anything that runs on the UI goroutine — a menu
// item's Enabled predicate is evaluated while the menu is being drawn, and a
// probe there would block the whole application on a slow server. A database
// nobody has asked about yet answers "nothing known", which fails open, so the
// action stays offered.
//
// Prime the cache off the UI goroutine (App.onNodeSelected does) so the answer
// is there by the time a menu is opened.
func (sc *ServerConn) CachedDatabaseCapabilities(name string) *gosmo.DatabaseCapabilities {
	if sc == nil {
		return unknownDatabaseCapabilities()
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if c, ok := sc.dbCaps[name]; ok {
		return c
	}
	return unknownDatabaseCapabilities()
}

// unknownDatabaseCapabilities is the answer when the probe could not be run:
// accessible, with nothing known about what may be done inside.
func unknownDatabaseCapabilities() *gosmo.DatabaseCapabilities {
	return &gosmo.DatabaseCapabilities{Accessible: true}
}

// ClearCapabilityCache drops every cached per-database answer, so the next
// call probes again. Rights granted to the connected login while it is
// connected take effect on its existing sessions, so a Refresh that re-reads
// the objects should re-read this too.
func (sc *ServerConn) ClearCapabilityCache() {
	if sc == nil {
		return
	}
	sc.mu.Lock()
	sc.dbCaps = nil
	sc.mu.Unlock()
}

// ProbeCapabilities fills the server-scope capability set. Best effort by
// design: Connect calls it, and a login that cannot be asked what it may do is
// still a login that can work.
//
// Exported because Connect is not the only way a ServerConn comes into
// existence — a test builds one over a scripted pool — and because a set that
// was never probed silently fails open, which is invisible in a test that
// meant to assert a gate.
func (sc *ServerConn) ProbeCapabilities() {
	ctx, cancel := context.WithTimeout(sc.Context(), capabilityProbeTimeout)
	defer cancel()

	if c, err := sc.Server.CapabilitiesContext(ctx); err == nil {
		sc.caps = c
	}
}
