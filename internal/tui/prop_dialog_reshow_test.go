package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// blockingPage is a one-page set whose load waits for release, then returns
// form and an apply that records marker into *applied. The load ignores its
// context on purpose: a driver that is slow to notice a cancel is exactly what
// lets a closed showing's result arrive after the next one has opened.
func blockingPage(release <-chan struct{}, form *propsheet.Form, err error, marker string, applied *string) func() []propPage {
	return func() []propPage {
		return []propPage{{
			title: "General",
			load: func(context.Context) (*propsheet.Form, propApply, error) {
				<-release
				if err != nil {
					return nil, nil, err
				}
				return form, func(context.Context) error { *applied = marker; return nil }, nil
			},
		}}
	}
}

// A load left in flight by a closed showing must not reach the next showing's
// page of the same index. The sheet's seq used to restart with every SetPages,
// so the old load's seq matched the new showing's first load and its form —
// for a different object — was installed, with its apply in the new applyFn
// map beside it.
func TestPropDialogReshowIgnoresThePreviousShowingsLoad(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)
	var applied string

	releaseX := make(chan struct{})
	formX := propsheet.NewForm(propsheet.Static("Name", "X"))
	d.show(sc, "", "Properties - X", "", "", blockingPage(releaseX, formX, nil, "X", &applied))
	d.Dismiss()

	releaseY := make(chan struct{})
	formY := propsheet.NewForm(propsheet.Static("Name", "Y"))
	d.show(sc, "", "Properties - Y", "", "", blockingPage(releaseY, formY, nil, "Y", &applied))

	close(releaseX)
	waitAndDrain(t, a)
	if d.PageForm(0) == formX {
		t.Fatal("the previous showing's form was installed on the new showing's page")
	}
	if st := d.PageState(0); st != propsheet.PageLoading {
		t.Fatalf("PageState(0) after the stale result = %v, want still PageLoading", st)
	}
	if d.applyFn[0] != nil {
		t.Fatal("the previous showing's apply was stored in the new showing's applyFn")
	}

	close(releaseY)
	waitAndDrain(t, a)
	if d.PageForm(0) != formY {
		t.Fatal("the new showing's own form was not installed")
	}
	if fn := d.applyFn[0]; fn == nil {
		t.Fatal("applyFn[0] is nil after the new showing's load")
	} else if _ = fn(context.Background()); applied != "Y" {
		t.Fatalf("applyFn[0] is showing %q's apply, want Y's", applied)
	}
}

// The usual form of the same bug: closing cancels the old showing's context,
// its load fails with "context canceled", and that error lands on the new
// showing's first page until the page's own load arrives.
func TestPropDialogReshowIgnoresThePreviousShowingsError(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)
	var applied string

	releaseX := make(chan struct{})
	d.show(sc, "", "Properties - X", "", "", blockingPage(releaseX, nil, context.Canceled, "X", &applied))
	d.Dismiss()

	releaseY := make(chan struct{})
	defer close(releaseY)
	d.show(sc, "", "Properties - Y", "", "", blockingPage(releaseY, nil, nil, "Y", &applied))

	close(releaseX)
	waitAndDrain(t, a)
	if st := d.PageState(0); st != propsheet.PageLoading {
		t.Fatalf("PageState(0) after the previous showing's error = %v, want still PageLoading", st)
	}
}

// A page action still running when the dialog is reopened belongs to the
// showing that started it: it must be cancelled with that showing, not carry
// on under the new one's context. pageActionBody used to read d.ctx on the
// action's goroutine, which is also a data race with show() rewriting it — so
// run this under -race.
func TestPropDialogPageActionKeepsItsShowingsContext(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)
	var applied string
	release := make(chan struct{})
	defer close(release)
	pages := blockingPage(release, nil, nil, "", &applied)

	d.show(sc, "", "Properties - X", "", "", pages)
	got := make(chan error, 1)
	d.runPageAction(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			got <- ctx.Err()
		case <-time.After(2 * time.Second):
			got <- errors.New("still live")
		}
		return nil
	}, func(error) {})

	// Reopening cancels the previous showing's context.
	d.show(sc, "", "Properties - Y", "", "", pages)
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("the action's context after a reshow: %v, want context.Canceled", err)
	}
}
