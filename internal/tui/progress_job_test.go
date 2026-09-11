package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
)

func escapeKey() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone) }

// waitForStatement polls until a write containing want has reached the fake
// server — the moment a blockExec write is genuinely in flight.
func waitForStatement(t *testing.T, inst *fakeInstance, want string) {
	t.Helper()
	for range 400 {
		if strings.Contains(strings.Join(inst.Statements(), "\n"), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never reached the server:\n%s", want, strings.Join(inst.Statements(), "\n"))
}

// Cancel during a batch delete stops the drop in flight and the batch with it:
// the next object is never attempted, the dialog stays up until the drop has
// returned, and the status line says it was cancelled and how many went. The
// folder is still refreshed — a cancel can land after a drop committed.
func TestCancelStopsADeleteBatchAtTheObjectInFlight(t *testing.T) {
	a := newTestApp()
	hold := make(chan struct{})
	defer close(hold)
	sc, inst := newFakeConn(t, fakeResponse{match: "DROP TABLE [sales].[Beta]", blockExec: hold})
	beta := opTestNode(sc, NodeTable, "sales", "Beta", "").data
	gamma := opTestNode(sc, NodeTable, "sales", "Gamma", "").data
	op := objectOpFor(NodeTable)
	refreshed := false

	a.runDeletes(sc, []nodeData{beta, gamma}, []*objectOp{op, op}, false, func() { refreshed = true })
	if !a.progressDialog.Visible() {
		t.Fatal("no progress dialog while the drops run")
	}
	waitForStatement(t, inst, "DROP TABLE [sales].[Beta]")

	a.progressDialog.HandleKey(escapeKey())
	if !a.progressDialog.Visible() {
		t.Fatal("Cancel closed the dialog before the drop in flight returned")
	}
	waitAndDrain(t, a)

	if a.progressDialog.Visible() || a.progressBusy {
		t.Errorf("dialog visible = %v, busy = %v after the batch returned, want both false", a.progressDialog.Visible(), a.progressBusy)
	}
	if joined := strings.Join(inst.Statements(), "\n"); strings.Contains(joined, "[sales].[Gamma]") {
		t.Errorf("the batch ran on past the cancel:\n%s", joined)
	}
	if !strings.Contains(a.statusText, "cancelled") || !strings.Contains(a.statusText, "0 of 2") {
		t.Errorf("status = %q, want it to say the delete was cancelled with 0 of 2 deleted", a.statusText)
	}
	if !refreshed {
		t.Error("the folder was not refreshed after a cancelled batch")
	}
}

// A batch reports each object it reaches, and the report lands on the dialog
// while it is up.
func TestDeleteBatchReportsTheObjectInFlight(t *testing.T) {
	a := newTestApp()
	hold := make(chan struct{})
	sc, _ := newFakeConn(t, fakeResponse{match: "DROP TABLE [sales].[Gamma]", blockExec: hold})
	beta := opTestNode(sc, NodeTable, "sales", "Beta", "").data
	gamma := opTestNode(sc, NodeTable, "sales", "Gamma", "").data
	op := objectOpFor(NodeTable)

	a.runDeletes(sc, []nodeData{beta, gamma}, []*objectOp{op, op}, false, func() {})
	for range 400 {
		a.drainPending()
		if strings.Contains(a.progressDialog.Message(), "2 of 2") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if msg := a.progressDialog.Message(); !strings.Contains(msg, "2 of 2") || !strings.Contains(msg, "Gamma") {
		t.Errorf("dialog message = %q, want it on object 2 of 2, Gamma", msg)
	}
	close(hold)
	waitAndDrain(t, a)
	if a.statusText != "Deleted 2 objects" {
		t.Errorf("status = %q, want %q", a.statusText, "Deleted 2 objects")
	}
}

// An uninterruptible job ignores Cancel: its context is never cancelled, the
// dialog stays up, and the job's own outcome is what done reports.
func TestUninterruptibleJobIgnoresCancel(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	release := make(chan struct{})
	started := make(chan context.Context, 1)
	var gotErr error
	gotCancelled, finished := false, false

	a.runWithProgress(progressJob{title: "Fail Over", message: "Failing over...", what: "test", sc: sc,
		uninterruptible: uninterruptibleFailover},
		func(ctx context.Context, _ progressReport) error {
			started <- ctx
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		func(err error, cancelled bool) { gotErr, gotCancelled, finished = err, cancelled, true })

	ctx := <-started
	a.progressDialog.HandleKey(escapeKey())
	a.progressDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if ctx.Err() != nil {
		t.Fatalf("the uninterruptible job's context was cancelled: %v", ctx.Err())
	}
	if !a.progressDialog.Visible() {
		t.Fatal("the dialog closed while the uninterruptible job ran")
	}
	close(release)
	waitAndDrain(t, a)
	if !finished || gotErr != nil || gotCancelled {
		t.Errorf("done: finished=%v err=%v cancelled=%v, want true, nil, false", finished, gotErr, gotCancelled)
	}
}

// A cancel that reaches a job only after its work succeeded is not reported
// as a cancel: the operation happened, and saying otherwise sends the user to
// retry something already done.
func TestCancelAfterTheWorkSucceededReportsSuccess(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	release := make(chan struct{})
	started := make(chan struct{})
	var gotErr error
	gotCancelled := true

	a.runWithProgress(progressJob{title: "T", message: "m", what: "test", sc: sc},
		func(context.Context, progressReport) error {
			close(started)
			<-release // ignores the context, like a statement already committed
			return nil
		},
		func(err error, cancelled bool) { gotErr, gotCancelled = err, cancelled })

	<-started
	a.progressDialog.HandleKey(escapeKey())
	close(release)
	waitAndDrain(t, a)
	if gotErr != nil || gotCancelled {
		t.Errorf("done: err=%v cancelled=%v, want nil, false", gotErr, gotCancelled)
	}
}

// A job whose work panics releases the dialog and the busy latch — without
// that the modal dialog would sit over the whole application for the rest of
// the process — runs the caller's repair, and the next job runs.
func TestProgressJobLatchClearsWhenTheWorkPanics(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	repaired := false
	a.runWithProgress(progressJob{title: "T", message: "m", what: "test", sc: sc, repair: func() { repaired = true }},
		func(context.Context, progressReport) error { panic("boom") },
		func(error, bool) { t.Error("done ran for a job that panicked") })
	waitAndDrain(t, a)
	if a.progressBusy || a.progressDialog.Visible() || !repaired {
		t.Fatalf("after the panic: busy=%v visible=%v repaired=%v, want false, false, true",
			a.progressBusy, a.progressDialog.Visible(), repaired)
	}

	var gotErr error
	ran := false
	a.runWithProgress(progressJob{title: "T", message: "m", what: "test", sc: sc},
		func(context.Context, progressReport) error { return errors.New("second") },
		func(err error, _ bool) { gotErr, ran = err, true })
	waitAndDrain(t, a)
	if !ran || gotErr == nil || gotErr.Error() != "second" {
		t.Errorf("the job after the panic: ran=%v err=%v, want it run to its own error", ran, gotErr)
	}
}
