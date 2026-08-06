package tui

import (
	"context"
	"testing"
	"time"
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
