package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// testPrefetch stands in for a real create dialog's prefetch payload.
type testPrefetch struct{ call int }

// newTestObjectDialog builds a two-page create dialog whose fetch announces
// itself on started, takes its own release gate off gates, and blocks on
// it. That lets a test hold one prefetch in flight across a close/reopen
// and say exactly which showing's fetch finishes with what — handing out
// the gate while only one goroutine is waiting for one is what makes the
// pairing deterministic.
func newTestObjectDialog(a *App, started chan struct{}, gates chan chan error) *newObjectDialog[testPrefetch] {
	d := &newObjectDialog[testPrefetch]{}
	d.init(a, newObjectConfig[testPrefetch]{
		title: "New Thing",
		noun:  "Thing",
		pages: []string{"General", "Options"},
		fetch: func(context.Context, *db.ServerConn) (*testPrefetch, error) {
			started <- struct{}{}
			if err := <-<-gates; err != nil {
				return nil, err
			}
			return &testPrefetch{}, nil
		},
		build: func(pf *testPrefetch) {
			d.forms = []*propsheet.Form{propsheet.NewForm(), propsheet.NewForm()}
			d.applyFns = []propApply{nil, nil}
			d.objectName = func() string { return "thing" }
			d.preflight = func() error { return nil }
		},
		refresh: func(*db.ServerConn) {},
	})
	return d
}

// A prefetch still in flight when the dialog is closed and reopened must
// not touch the new showing. The per-page seq can't catch this on its own:
// SetPages rebuilds every slot, so the reopened dialog's first load carries
// the same seq the abandoned one did. Before the session check in
// onLoadPage, the stale callback drained the new showing's waiting list and
// failed its pages, and the live fetch then landed with nothing waiting —
// leaving General stuck on the previous showing's cancellation error.
func TestNewObjectDialogReopenDuringPrefetch(t *testing.T) {
	a := newTestApp()
	sc := &db.ServerConn{Opts: config.Connection{Server: "server-one"}}
	started, gates := make(chan struct{}), make(chan chan error)
	first, second := make(chan error, 1), make(chan error, 1)
	d := newTestObjectDialog(a, started, gates)

	d.show(sc) // first showing: page 0 load dispatched
	<-started
	gates <- first // ...and its fetch now blocks on first

	d.Dismiss()
	d.show(sc) // second showing: its own load dispatched
	<-started
	gates <- second // ...and its fetch now blocks on second

	first <- errors.New("context canceled") // the abandoned showing's fetch fails
	waitAndDrain(t, a)
	second <- nil // the live showing's fetch succeeds
	waitAndDrain(t, a)

	if got := d.PageState(0); got != propsheet.PageReady {
		t.Errorf("PageState(0) = %v after the live prefetch landed, want PageReady", got)
	}
	if d.prefetch == nil {
		t.Error("prefetch is nil after a successful fetch — the live result was dropped")
	}
}

// waitAndDrain waits for a fetch goroutine to post its callback (a
// nil-screen App queues it with no event loop to drain it), then runs it on
// this goroutine, the way Run would.
func waitAndDrain(t *testing.T, a *App) {
	t.Helper()
	for range 200 {
		a.pendingMu.Lock()
		n := len(a.pending)
		a.pendingMu.Unlock()
		if n > 0 {
			a.drainPending()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no callback posted within the timeout")
}

// A create dialog's later page acts on what an earlier page created. Under
// Script Changes the earlier page's EXEC was only collected, so resolving
// that object with a real JobByName/AlertByName lookup fails with "not
// found" and no script comes out at all — the bug New Schedule reported as
// `gosmo: schedule "X" not found`. Both helpers must hand back a name-only
// handle instead, and that handle must still script its dependent statement.
func TestScriptSafeLookupsDoNotQueryUnderScriptMode(t *testing.T) {
	// Server is nil on purpose: under WithScript nothing may reach the
	// database, so a helper that still queries panics here rather than
	// quietly passing.
	sc := &db.ServerConn{Opts: config.Connection{Server: "server-one"}}
	ctx, script := gosmo.WithScript(context.Background())

	j, err := scriptSafeJob(ctx, sc, "nightly reindex")
	if err != nil {
		t.Fatalf("scriptSafeJob under WithScript: %v", err)
	}
	if err := j.AttachScheduleContext(ctx, "Nightly"); err != nil {
		t.Fatalf("AttachScheduleContext on the scripted handle: %v", err)
	}

	al, err := scriptSafeAlert(ctx, sc, "sev 19")
	if err != nil {
		t.Fatalf("scriptSafeAlert under WithScript: %v", err)
	}
	if err := al.NotifyContext(ctx, "dba", gosmo.NotifyMethodEmail); err != nil {
		t.Fatalf("NotifyContext on the scripted handle: %v", err)
	}

	if len(script.Statements) != 2 {
		t.Fatalf("Statements = %v, want the attach and the notification", script.Statements)
	}
	if !strings.Contains(script.Statements[0], "sp_attach_schedule") ||
		!strings.Contains(script.Statements[0], "N'nightly reindex'") {
		t.Errorf("Statements[0] = %q, want sp_attach_schedule naming the job", script.Statements[0])
	}
	if !strings.Contains(script.Statements[1], "sp_add_notification") ||
		!strings.Contains(script.Statements[1], "N'sev 19'") {
		t.Errorf("Statements[1] = %q, want sp_add_notification naming the alert", script.Statements[1])
	}
}
