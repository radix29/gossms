package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// stubLoad is a completionLoad over fetch whose owned/evict/apply record what
// startCompletionLoad did with the result.
type stubLoad struct {
	l       latest
	cached  bool
	evicted bool
	applied error
	ran     bool
}

func (s *stubLoad) load(fetch func(ctx context.Context) (int, error)) completionLoad[int] {
	s.cached = true
	return completionLoad[int]{
		what: "test", timeout: completionInventoryTimeout, load: &s.l,
		owned: func() bool { return s.cached },
		evict: func() { s.cached, s.evicted = false, true },
		fetch: fetch,
		apply: func(_ int, err error) { s.ran, s.applied = true, err },
	}
}

// An error with the connection still open is the entry's own to record; one
// after it closed is the teardown's, and evicts so the next lookup retries.
func TestCompletionLoadClosedConnectionEvicts(t *testing.T) {
	boom := errors.New("boom")
	for _, closeIt := range []bool{false, true} {
		sc, _ := newFakeConn(t)
		a := newTestApp()
		var s stubLoad
		startCompletionLoad(a, sc, s.load(func(context.Context) (int, error) {
			if closeIt {
				sc.Close()
			}
			return 0, boom
		}))
		drainUntil(t, a, func() bool { return s.ran || s.evicted }, "the load")
		if closeIt && (s.ran || !s.evicted) {
			t.Errorf("closed: applied=%v evicted=%v, want evicted only", s.ran, s.evicted)
		}
		if !closeIt && (s.applied != boom || s.evicted) {
			t.Errorf("open: applied %v evicted=%v, want boom applied", s.applied, s.evicted)
		}
	}
}

// A panicking fetch evicts the entry rather than leaving it latched.
func TestCompletionLoadPanicEvicts(t *testing.T) {
	sc, _ := newFakeConn(t)
	a := newTestApp()
	var s stubLoad
	startCompletionLoad(a, sc, s.load(func(context.Context) (int, error) { panic("fetch blew up") }))
	drainUntil(t, a, func() bool { return s.evicted }, "the panic repair")
	if s.ran {
		t.Error("apply ran for a fetch that panicked")
	}
}

// A result for an entry no longer in its cache reaches nothing.
func TestCompletionLoadDropsAnUnownedResult(t *testing.T) {
	sc, _ := newFakeConn(t)
	a := newTestApp()
	var s stubLoad
	release := make(chan struct{})
	startCompletionLoad(a, sc, s.load(func(context.Context) (int, error) {
		<-release
		return 1, nil
	}))
	s.cached = false
	close(release)
	// Done runs before the ownership check and releases the run.
	drainUntil(t, a, s.l.Idle, "the result")
	if s.ran || s.evicted {
		t.Errorf("applied=%v evicted=%v for an entry that left its cache", s.ran, s.evicted)
	}
}

// K10: the database directory and the linked-server list say so when they
// fail on a live connection, as the catalog loads always have, instead of
// caching an empty list silently until Ctrl+R.
func TestCompletionDirectoryFailuresAreReported(t *testing.T) {
	for name, c := range map[string]struct {
		start func(a *App, sc *db.ServerConn) (loading func() bool)
		want  string
	}{
		"database list": {
			start: func(a *App, sc *db.ServerConn) func() bool {
				d := a.ensureCompletionDirectory(sc)
				return func() bool { return d.loading }
			},
			want: "database list unavailable",
		},
		"linked-server list": {
			start: func(a *App, sc *db.ServerConn) func() bool {
				d := a.ensureLinkedDirectory(sc)
				return func() bool { return d.loading }
			},
			want: "linked-server list unavailable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			sc, _ := newFakeConn(t) // nothing scripted: every read fails
			a := newTestApp()
			loading := c.start(a, sc)
			drainUntil(t, a, func() bool { return !loading() }, "the load")
			if !strings.Contains(a.statusText, c.want) {
				t.Errorf("status = %q, want it to contain %q", a.statusText, c.want)
			}
		})
	}
}
