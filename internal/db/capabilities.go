package db

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// capabilityProbeTimeout bounds one capability probe — the server's single
// round trip, or the database's two. Both ask catalog functions with no I/O
// behind them, so this is a liveness bound rather than a work budget: the
// server probe runs inside Connect, where a hang would look like a hung login
// dialog.
const capabilityProbeTimeout = 10 * time.Second

// capabilityFields is the ServerConn state behind Capabilities and
// DatabaseCapabilities, kept in its own struct so connection.go's ServerConn
// stays about the connection.
type capabilityFields struct {
	// caps is probed inside Connect, and again by every Refresh of the server
	// node — after the connection is shared, which is why it is atomic. Nil
	// until a probe succeeds; Capabilities answers "unknown" for that.
	caps atomic.Pointer[gosmo.Capabilities]

	mu     sync.Mutex
	dbCaps map[string]*gosmo.DatabaseCapabilities
	// dbProbes is the probe in flight per database. Selecting a node primes
	// its database, so arrowing through an unprobed database's nodes asks
	// once per keystroke; everyone after the first waits for its answer.
	dbProbes map[string]*dbProbe
	// dbGen is bumped by ClearCapabilityCache, so a probe that started before
	// a clear cannot store its pre-clear answer after it.
	dbGen uint64
}

// dbProbe is one per-database probe in flight. Fields other than done are
// written before done is closed and read only after it.
type dbProbe struct {
	done chan struct{}
	caps *gosmo.DatabaseCapabilities // nil if the probe failed
	// abandoned is a failure caused by the caller that ran the probe giving up
	// — its context ended — which says nothing about the server. A waiter that
	// still wants an answer runs a probe of its own rather than inherit it.
	abandoned bool
	// waiters counts the callers that joined; tests read it to know one has.
	waiters int
}

// Capabilities reports what the connected login may do at the server scope.
//
// Never nil, and never an error: the probe runs inside Connect (and again on a
// Refresh of the server node — see ProbeCapabilities), and a failure leaves
// every answer CapabilityUnknown rather than failing the connection. That is the fail-open rule this whole layer follows — see
// gosmo.Capabilities.Allows, and gate on Allows rather than Has anywhere the
// answer decides whether to *withhold* something.
func (sc *ServerConn) Capabilities() *gosmo.Capabilities {
	if sc == nil {
		return &gosmo.Capabilities{}
	}
	if c := sc.caps.Load(); c != nil {
		return c
	}
	return &gosmo.Capabilities{}
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
// Two round trips on the first call for a database, none afterwards. Callers
// that ask while that first probe is running wait for it rather than start
// their own, each for no longer than its own ctx allows.
func (sc *ServerConn) DatabaseCapabilities(ctx context.Context, name string) *gosmo.DatabaseCapabilities {
	if sc == nil || sc.Server == nil {
		return unknownDatabaseCapabilities()
	}

	for {
		sc.mu.Lock()
		if c, ok := sc.dbCaps[name]; ok {
			sc.mu.Unlock()
			return c
		}
		p, inFlight := sc.dbProbes[name]
		if !inFlight {
			p = &dbProbe{done: make(chan struct{})}
			if sc.dbProbes == nil {
				sc.dbProbes = map[string]*dbProbe{}
			}
			sc.dbProbes[name] = p
			gen := sc.dbGen
			sc.mu.Unlock()
			return sc.probeDatabase(ctx, name, gen, p)
		}
		p.waiters++
		sc.mu.Unlock()

		select {
		case <-p.done:
		case <-ctx.Done():
			return unknownDatabaseCapabilities()
		}
		if p.caps != nil {
			return p.caps
		}
		// The probe's own caller gave up before the server answered — a
		// selection primes under the connection's context, but a Properties
		// dialog closed mid-load cancels its own. Nothing was learned, so ask
		// again, as the first caller in line.
		if p.abandoned && ctx.Err() == nil {
			continue
		}
		return unknownDatabaseCapabilities()
	}
}

// probeDatabase runs the probe p stands for, caches its answer, and releases
// p's waiters — deferred, so a panicking probe still releases them rather than
// leaving every later caller for this database blocked on done.
func (sc *ServerConn) probeDatabase(ctx context.Context, name string, gen uint64, p *dbProbe) *gosmo.DatabaseCapabilities {
	var c *gosmo.DatabaseCapabilities
	var err error
	defer func() {
		sc.mu.Lock()
		if sc.dbProbes[name] == p {
			delete(sc.dbProbes, name)
		}
		if err == nil && c != nil {
			p.caps = c
			// A ClearCapabilityCache since the probe started means this answer
			// may predate the change the clear was for; it is still the best
			// one to return, but caching it would outlive the Refresh that
			// asked for a re-read.
			if sc.dbGen == gen {
				if sc.dbCaps == nil {
					sc.dbCaps = map[string]*gosmo.DatabaseCapabilities{}
				}
				sc.dbCaps[name] = c
			}
		} else {
			// Deliberately not cached: a probe that failed for a transient
			// reason would otherwise keep answering "unknown" for the whole
			// session.
			p.abandoned = ctx.Err() != nil
		}
		sc.mu.Unlock()
		close(p.done)
	}()

	pctx, cancel := context.WithTimeout(ctx, capabilityProbeTimeout)
	defer cancel()
	c, err = sc.Server.Database(name).CapabilitiesContext(pctx)
	if err != nil {
		return unknownDatabaseCapabilities()
	}
	return c
}

// HasDatabaseCapabilities reports whether name's answer is already cached, so
// a caller that only wants the cache warm can skip starting a probe.
func (sc *ServerConn) HasDatabaseCapabilities(name string) bool {
	if sc == nil {
		return false
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	_, ok := sc.dbCaps[name]
	return ok
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
	// A probe already running still finishes and answers its waiters, but a
	// caller from after the clear must not join it: its answer is pre-clear.
	sc.dbProbes = nil
	sc.dbGen++
	sc.mu.Unlock()
}

// ProbeCapabilities fills the server-scope capability set. Best effort by
// design: Connect calls it, and a login that cannot be asked what it may do is
// still a login that can work.
//
// Also called again by a Refresh of the server node, off the UI goroutine and
// while the connection is in use — a right granted to the login after it
// connected is otherwise never seen. A re-probe that fails keeps the previous
// answer rather than falling back to "unknown": one dropped round trip should
// not switch every gate off.
//
// Exported because Connect is not the only way a ServerConn comes into
// existence — a test builds one over a scripted pool — and because a set that
// was never probed silently fails open, which is invisible in a test that
// meant to assert a gate.
func (sc *ServerConn) ProbeCapabilities() {
	ctx, cancel := context.WithTimeout(sc.Context(), capabilityProbeTimeout)
	defer cancel()

	if c, err := sc.Server.CapabilitiesContext(ctx); err == nil {
		sc.caps.Store(c)
	}
}
