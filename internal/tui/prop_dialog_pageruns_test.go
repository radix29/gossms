package tui

import (
	"context"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ctxPage is a one-page set whose load publishes the context it was handed and
// then blocks until release, so a test can watch what happens to a load that is
// still out. Like blockingPage it ignores its own context deliberately: what is
// being asserted is that the *context* was cancelled, not that a cooperative
// loader noticed.
func ctxPage(seen chan<- context.Context, release <-chan struct{}) func() []propPage {
	return func() []propPage {
		return []propPage{{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				seen <- ctx
				<-release
				return propsheet.NewForm(), nil, nil
			},
		}}
	}
}

// awaitCtx takes the next context a load published, or fails.
func awaitCtx(t *testing.T, seen <-chan context.Context, what string) context.Context {
	t.Helper()
	select {
	case ctx := <-seen:
		return ctx
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// awaitDone waits for ctx to be cancelled, or fails.
func awaitDone(t *testing.T, ctx context.Context, what string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("%s is still live, so its query keeps its pool connection until propFetchTimeout", what)
	}
}

// F5 on a page whose load is still out must not start a second one. Without
// the guard each press dispatched another OnLoadPage, so holding F5 down while
// a slow page (sys.dm_db_index_physical_stats) loads put one read per press in
// flight, each holding a pooled connection for up to propFetchTimeout, with
// the read the user is actually waiting for queued behind all of them.
func TestPropDialogRefreshDuringALoadStartsNothing(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)

	seen := make(chan context.Context, 4)
	release := make(chan struct{})
	defer close(release)
	d.show(sc, "", "Properties", "", "", ctxPage(seen, release))
	awaitCtx(t, seen, "the first load to start")

	d.Refresh(0)
	d.Refresh(0)
	// A load that should not exist needs time to reach the channel, so this
	// fails rather than passing on timing.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-seen:
		t.Fatal("Refresh started a second load for a page that was already loading")
	default:
	}
	if st := d.PageState(0); st != propsheet.PageLoading {
		t.Fatalf("PageState(0) = %v, want still PageLoading", st)
	}
}

// The cancel half of the latest-only contract: a load that supersedes another
// for the same page has to stop it, not merely ignore its result. InvalidateAll
// (after a successful Apply) is the reachable supersede — it reloads the
// current page whatever state it is in.
func TestPropDialogSupersededPageLoadIsCancelled(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)

	seen := make(chan context.Context, 4)
	release := make(chan struct{})
	defer close(release)
	d.show(sc, "", "Properties", "", "", ctxPage(seen, release))
	first := awaitCtx(t, seen, "the first load to start")

	d.InvalidateAll()
	second := awaitCtx(t, seen, "the reload to start")

	awaitDone(t, first, "the superseded page load's context")
	if second.Err() != nil {
		t.Fatalf("the replacement load's context is already done: %v", second.Err())
	}
}

// Closing the dialog stops what is in flight, and leaves nothing behind that
// the next showing would trip over.
func TestPropDialogCloseCancelsThePageLoadInFlight(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)

	seen := make(chan context.Context, 4)
	release := make(chan struct{})
	defer close(release)
	d.show(sc, "", "Properties", "", "", ctxPage(seen, release))
	ctx := awaitCtx(t, seen, "the load to start")

	d.Dismiss()
	awaitDone(t, ctx, "the closed showing's page load context")
	if n := len(d.pageRuns); n != 0 {
		t.Fatalf("%d page run(s) left registered after close, want 0", n)
	}
}

// A completed load releases its own context and clears its entry, so the map
// does not grow a dead run per page for the life of the dialog.
func TestPropDialogCompletedPageLoadClearsItsRun(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)

	seen := make(chan context.Context, 4)
	release := make(chan struct{})
	d.show(sc, "", "Properties", "", "", ctxPage(seen, release))
	ctx := awaitCtx(t, seen, "the load to start")

	close(release)
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady },
		"the page to load")
	if n := len(d.pageRuns); n != 0 {
		t.Fatalf("%d page run(s) left registered after the load completed, want 0", n)
	}
	awaitDone(t, ctx, "the completed load's context")
}
