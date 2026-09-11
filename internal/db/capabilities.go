package db

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// capabilityProbeTimeout bounds one capability probe (one round trip for the
// server, two for a database). It's a liveness bound: the server probe runs
// inside Connect.
const capabilityProbeTimeout = 10 * time.Second

// capabilityFields is the ServerConn state behind Capabilities and
// DatabaseCapabilities.
type capabilityFields struct {
	// caps is probed in Connect and on every server-node Refresh, after the
	// connection is shared — hence atomic. Nil until a probe succeeds, which
	// reads as "unknown".
	caps atomic.Pointer[gosmo.Capabilities]

	mu     sync.Mutex
	dbCaps map[string]*gosmo.DatabaseCapabilities
	// dbProbes is the in-flight probe per database. Selecting a node primes its
	// database, so arrowing through nodes asks per keystroke; later callers
	// wait for the first.
	dbProbes map[string]*dbProbe
	// dbGen is bumped by ClearCapabilityCache so a pre-clear probe can't cache
	// its answer afterwards.
	dbGen uint64
}

// dbProbe is one in-flight per-database probe. Fields other than done are
// written before done closes and read after.
type dbProbe struct {
	done chan struct{}
	caps *gosmo.DatabaseCapabilities // nil if the probe failed
	// abandoned means the probing caller's context ended, which says nothing
	// about the server; a waiter still wanting an answer probes again.
	abandoned bool
	// waiters counts joined callers; tests read it.
	waiters int
}

// Capabilities reports what the login may do at server scope. Never nil or an
// error: a failed probe (in Connect or a server Refresh, see ProbeCapabilities)
// leaves every answer CapabilityUnknown. Fail-open — gate on
// gosmo.Capabilities.Allows, not Has, wherever the answer withholds something.
func (sc *ServerConn) Capabilities() *gosmo.Capabilities {
	if sc == nil {
		return &gosmo.Capabilities{}
	}
	if c := sc.caps.Load(); c != nil {
		return c
	}
	return &gosmo.Capabilities{}
}

// DatabaseCapabilities reports what the login may do in one database, probing
// on first use and caching for the connection's life.
//
// Never nil or an error. A failed probe returns Accessible with no rights
// known, which reads like an unrestricted login through Allows. Only a real
// server answer reports Accessible false ("cannot be opened"); deriving that
// from a failure would hide databases over a dropped connection.
//
// Two round trips on first call, none after. Concurrent callers wait for the
// first probe, each bounded by its own ctx.
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
		// The probing caller gave up (e.g. a Properties dialog closed
		// mid-load); nothing was learned, so probe again as first in line.
		if p.abandoned && ctx.Err() == nil {
			continue
		}
		return unknownDatabaseCapabilities()
	}
}

// probeDatabase runs p's probe, caches the answer, and releases p's waiters —
// deferred, so a panic still releases them.
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
			// A clear since the probe started means this answer may be stale;
			// return it, but don't cache it past the Refresh that asked for a
			// re-read.
			if sc.dbGen == gen {
				if sc.dbCaps == nil {
					sc.dbCaps = map[string]*gosmo.DatabaseCapabilities{}
				}
				sc.dbCaps[name] = c
			}
		} else {
			// Failures aren't cached, or a transient error would answer
			// "unknown" all session.
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

// HasDatabaseCapabilities reports whether name's answer is cached, so a
// cache-warming caller can skip the probe.
func (sc *ServerConn) HasDatabaseCapabilities(name string) bool {
	if sc == nil {
		return false
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	_, ok := sc.dbCaps[name]
	return ok
}

// CachedDatabaseCapabilities answers from the cache only, never the network —
// for the UI goroutine (e.g. menu Enabled predicates), where a probe would
// block the app. Unasked databases answer "nothing known", which fails open.
// App.onNodeSelected primes the cache off the UI goroutine.
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

// unknownDatabaseCapabilities is the answer when no probe could run:
// accessible, nothing known.
func unknownDatabaseCapabilities() *gosmo.DatabaseCapabilities {
	return &gosmo.DatabaseCapabilities{Accessible: true}
}

// ClearCapabilityCache drops every per-database answer so the next call probes
// again. Grants to a connected login affect its existing sessions, so a Refresh
// re-reads these too.
func (sc *ServerConn) ClearCapabilityCache() {
	if sc == nil {
		return
	}
	sc.mu.Lock()
	sc.dbCaps = nil
	// A running probe still answers its waiters, but post-clear callers must
	// not join it.
	sc.dbProbes = nil
	sc.dbGen++
	sc.mu.Unlock()
}

// ProbeCapabilities fills the server-scope capability set, best effort: a login
// that can't be asked may still work.
//
// A server-node Refresh calls it again off the UI goroutine, so later grants
// are seen. A failed re-probe keeps the previous answer rather than switching
// every gate to unknown.
//
// Exported because tests build ServerConns over scripted pools, and an unprobed
// set silently fails open, masking a gate under test.
func (sc *ServerConn) ProbeCapabilities() {
	ctx, cancel := context.WithTimeout(sc.Context(), capabilityProbeTimeout)
	defer cancel()

	if c, err := sc.Server.CapabilitiesContext(ctx); err == nil {
		sc.caps.Store(c)
	}
}
