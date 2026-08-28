package tui

import (
	"context"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// drainUntil runs the app's queued UI callbacks until cond holds, or fails.
// A page action reports back through postAndWake, and with no screen the
// wake half is a no-op — so a test plays the event loop's part itself.
func drainUntil(t *testing.T, a *App, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		a.drainPending()
		time.Sleep(time.Millisecond)
	}
}

// A page action's button must ignore a second click while its round trip is
// still out: two goroutines both write the captured result variable and both
// fill the grid, so the picture that survives is whichever finished last —
// which can be the older request. The latch must also clear afterwards, or
// the button is dead for the rest of the dialog's life.
func TestPageActionLatchBlocksSecondClickAndClears(t *testing.T) {
	a := &App{}
	d := &PropDialog{app: a, ctx: context.Background()}

	var busy bool
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	dones := 0

	click := func() {
		d.runPageActionOnce(&busy, func(context.Context) error {
			started <- struct{}{}
			<-release
			return nil
		}, func(error) { dones++ })
	}

	click()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the first click never ran its action")
	}

	click()
	// The second click must not have launched anything. Give a goroutine
	// that should not exist time to signal, so this fails rather than
	// passing on timing.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-started:
		t.Fatal("a second click ran while the first was still in flight")
	default:
	}

	close(release)
	drainUntil(t, a, func() bool { return dones == 1 }, "the first action to report back")
	if busy {
		t.Fatal("the latch survived the action, so the button is now dead")
	}

	// A fresh gesture after the first completed must run.
	release = make(chan struct{})
	close(release)
	click()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("a click after the first action completed did nothing")
	}
	drainUntil(t, a, func() bool { return dones == 2 }, "the second action to report back")
}

// The latch has to clear on the failure path too — an action that errors
// leaves the page showing the error, and the user's next move is to press
// the button again.
func TestPageActionLatchClearsAfterAnError(t *testing.T) {
	a := &App{}
	d := &PropDialog{app: a, ctx: context.Background()}

	var busy bool
	var gotErr error
	d.runPageActionOnce(&busy, func(context.Context) error {
		return context.Canceled
	}, func(err error) { gotErr = err })

	drainUntil(t, a, func() bool { return gotErr != nil }, "the failing action to report back")
	if busy {
		t.Fatal("the latch survived a failed action")
	}
}

// The latch's release lives in onDone, which a panicking action never
// reaches. Without a repair step the button refuses every later click for the
// rest of the dialog's life — Check Syntax, Estimate Rows, Rebuild and the
// rest all go dead with no explanation.
func TestPageActionLatchClearsWhenTheActionPanics(t *testing.T) {
	a := newTestApp()
	d := &PropDialog{app: a, ctx: context.Background()}

	var busy bool
	doneRan := false
	d.runPageActionOnce(&busy, func(context.Context) error { panic("boom") },
		func(error) { doneRan = true })

	drainUntil(t, a, func() bool { return !busy }, "the in-flight latch to clear")
	if doneRan {
		t.Error("onDone ran for an action that panicked")
	}

	// The latch clearing is only worth anything if the button works again.
	ran := make(chan struct{})
	d.runPageActionOnce(&busy, func(context.Context) error { close(ran); return nil }, func(error) {})
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the next click was refused — the latch never really cleared")
	}
}

// TestPropertiesOnAClosedConnectionNeverBuildsItsPages covers the guard that
// used to be copied into all twenty-three Properties entry points and now
// lives in PropDialog.show.
//
// Two halves, and the second is the reason `pages` is a builder: without the
// guard the dialog opens and every page reports its own connection error,
// which is what the copies existed to prevent; without the builder the page
// set is constructed at the call site, so the guard moving inwards would let
// it run against a closed connection.
func TestPropertiesOnAClosedConnectionNeverBuildsItsPages(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "server-one")
	sc.Close()

	built := 0
	a.propDialog.show(sc, "master", "Database Properties", "Database: master", "Server: server-one",
		func() []propPage { built++; return []propPage{{title: "General"}} })

	if built != 0 {
		t.Errorf("the page builder ran %d times against a closed connection, want 0", built)
	}
	if a.propDialog.Visible() {
		t.Error("the dialog opened on a closed connection — every page would load into an error")
	}
	if a.statusText != notConnectedMessage {
		t.Errorf("status = %q, want %q", a.statusText, notConnectedMessage)
	}
}

// TestPropertiesOnAnOpenConnectionBuildsItsPages is the other half: the guard
// must not be so eager that it swallows the normal case. Without it the test
// above passes for a dialog that never opens at all.
func TestPropertiesOnAnOpenConnectionBuildsItsPages(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "server-one")

	built := 0
	a.propDialog.show(sc, "master", "Database Properties", "Database: master", "Server: server-one",
		func() []propPage { built++; return []propPage{{title: "General"}, {title: "Files"}} })

	if built != 1 {
		t.Errorf("the page builder ran %d times, want exactly 1", built)
	}
	if !a.propDialog.Visible() {
		t.Error("the dialog did not open on a live connection")
	}
	if got := len(a.propDialog.pages); got != 2 {
		t.Errorf("the dialog holds %d pages, want the 2 the builder returned", got)
	}
}

// A page load's latch is PropertySheet.startLoad's PageLoading, and only the
// callback onLoadPage posts clears it. A panic in the loader unwinds past that
// callback, and SelectPage will not restart a page whose state is not
// PageNotLoaded — so without a repair step the page reads "Loading..." for the
// rest of the showing and F5 is the only way out of it.
func TestPropertiesPageLoadRecoversWhenTheLoaderPanics(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "server-one")

	d := a.propDialog
	d.show(sc, "master", "Database Properties", "Database: master", "Server: server-one",
		func() []propPage {
			return []propPage{{
				title: "General",
				load: func(context.Context) (*propsheet.Form, propApply, error) {
					panic("boom")
				},
			}}
		})

	drainUntil(t, a, func() bool { return d.PageState(0) != propsheet.PageLoading },
		"the panicking page to leave PageLoading")

	if got := d.PageState(0); got != propsheet.PageError {
		t.Fatalf("page state = %v, want PageError — a panicking load must report, not sit at Loading", got)
	}
}

// The repair is only worth anything if the page can be loaded again afterwards.
// PropertySheet.Refresh is F5's handler, and it is what a user reaches for when
// a page says it failed.
func TestPropertiesPageReloadsAfterALoaderPanic(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "server-one")

	loads := 0
	d := a.propDialog
	d.show(sc, "master", "Database Properties", "Database: master", "Server: server-one",
		func() []propPage {
			return []propPage{{
				title: "General",
				load: func(context.Context) (*propsheet.Form, propApply, error) {
					loads++
					if loads == 1 {
						panic("boom")
					}
					return propsheet.NewForm(), nil, nil
				},
			}}
		})

	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageError },
		"the panicking page to report")

	d.Refresh(0)
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady },
		"the retried page to load")

	if loads != 2 {
		t.Errorf("the loader ran %d times, want 2 — the retry never reached it", loads)
	}
}
