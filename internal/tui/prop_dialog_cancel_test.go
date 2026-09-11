package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
)

// Cancel while an Apply is in flight stops the run and leaves the dialog open
// to say so: the page whose write was running sees its context cancelled, the
// pages after it never run, and the message counts the pages that had already
// been saved. Once the run has reported back, Cancel closes the dialog again.
func TestPropDialogCancelStopsTheApplyInFlight(t *testing.T) {
	a := &App{}
	pages := []propPage{{title: "General"}, {title: "Options"}, {title: "Files"}}
	started := make(chan struct{}, 1)
	var ranFiles bool
	applies := map[int]propApply{
		0: func(context.Context) error { return nil },
		1: func(ctx context.Context) error {
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		},
		2: func(context.Context) error { ranFiles = true; return nil },
	}
	d := newSheetDialog(t, pages, applies, nil)
	d.app = a
	d.ctx = context.Background()
	closes := 0
	d.OnClose = func() { closes++ }
	d.OnCancelApply = d.run.cancel

	d.runApply(true)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the Options page's apply never started")
	}
	escape := func() { d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone)) }
	escape()
	drainUntil(t, a, func() bool { return !d.Applying() }, "the cancelled run to report back")

	if ranFiles {
		t.Error("the page after the cancelled one still ran")
	}
	if !d.Visible() || closes != 0 {
		t.Fatalf("visible=%v closes=%d after the cancel, want the dialog left open", d.Visible(), closes)
	}
	if msg := d.Message(); !strings.Contains(msg, "cancelled after 1 of 3 pages") {
		t.Errorf("message = %q, want it to count the one page saved", msg)
	}

	escape()
	if d.Visible() || closes != 1 {
		t.Errorf("visible=%v closes=%d after Escape on an idle dialog, want it closed", d.Visible(), closes)
	}
}

// A step that ignores its context still cannot carry the pipeline past a
// cancel: runApplySteps checks between steps.
func TestRunApplyStepsStopsBetweenStepsAfterACancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran []int
	fns := []propApply{
		func(context.Context) error { ran = append(ran, 0); cancel(); return nil },
		nil,
		func(context.Context) error { ran = append(ran, 2); return nil },
	}
	completed, err := runApplySteps(ctx, fns)
	if err == nil || completed != 1 || len(ran) != 1 {
		t.Errorf("completed=%d err=%v ran=%v, want 1, context.Canceled, [0]", completed, err, ran)
	}
}
