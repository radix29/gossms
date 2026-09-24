package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// An Apply that fails part-way has changed the server for every page whose
// statements executed, and for none of the others. The pages that landed
// reload, so the next Apply does not re-send them — a duplicate for an ADD
// FILE or a job step — and the page that never reached the server keeps its
// edits for the user to correct and retry. gosmo's statement observer is what
// tells the two apart; these drive real gosmo writes through the fake driver
// so it has something to observe.

// errRefused is what the fake answers a write scripted to fail.
var errRefused = errors.New("refused")

// partialApplyConn is a fake instance on which every DROP LOGIN succeeds but
// the one naming [refused].
func partialApplyConn(t *testing.T) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, fakeResponse{match: "DROP LOGIN [refused]", err: errRefused})
	return sc
}

// dropLogins is an apply closure issuing one DROP LOGIN per name, in order.
func dropLogins(sc *db.ServerConn, names ...string) propApply {
	return func(ctx context.Context) error {
		for _, n := range names {
			if err := sc.Server.LoginRef(n).Drop(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// partialDialog is a two-page dirty dialog over applies, with page 1 current.
func partialDialog(t *testing.T, applies map[int]propApply) *PropDialog {
	t.Helper()
	d := newSheetDialog(t, []propPage{{title: "General"}, {title: "Files"}}, applies, nil)
	d.app = &App{}
	d.ctx = context.Background()
	return d
}

func runAndWait(t *testing.T, d *PropDialog, run func()) {
	t.Helper()
	run()
	drainUntil(t, d.app, func() bool { return !d.Applying() }, "the pipeline to finish")
}

func TestPartlyAppliedPagesReload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		applies func(sc *db.ServerConn) map[int]propApply
	}{
		// General's first statement lands, its second is refused; Files never
		// runs.
		{"fails midway through a page", func(sc *db.ServerConn) map[int]propApply {
			return map[int]propApply{0: dropLogins(sc, "a", "refused"), 1: dropLogins(sc, "b")}
		}},
		// General lands whole; Files fails before sending anything.
		{"fails on a later page before it writes", func(sc *db.ServerConn) map[int]propApply {
			return map[int]propApply{
				0: dropLogins(sc, "a"),
				1: func(context.Context) error { return errRefused },
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := partialDialog(t, tc.applies(partialApplyConn(t)))

			runAndWait(t, d, func() { d.runApply(false) })

			if !strings.Contains(d.Message(), "refused") {
				t.Errorf("message = %q, want the failure", d.Message())
			}
			if got := d.PageState(0); got != propsheet.PageNotLoaded {
				t.Errorf("General is %v, want it invalidated — its edits reached the server", got)
			}
			if got := d.DirtyPages(); !slices.Equal(got, []int{1}) {
				t.Errorf("dirty pages = %v, want [1] — Files never reached the server and keeps its edits", got)
			}
		})
	}
}

// A page that fails before any of its statements run is the ordinary failure:
// nothing reloads.
func TestAFailureBeforeAnyWriteReloadsNothing(t *testing.T) {
	sc := partialApplyConn(t)
	d := partialDialog(t, map[int]propApply{0: dropLogins(sc, "refused"), 1: dropLogins(sc, "b")})

	runAndWait(t, d, func() { d.runApply(false) })

	if got := d.DirtyPages(); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("dirty pages = %v, want both — nothing reached the server", got)
	}
}

// Script Changes executes nothing, so however far it got, no page's edits are
// on the server and none may be discarded.
func TestAFailedScriptChangesReloadsNothing(t *testing.T) {
	sc := partialApplyConn(t)
	d := partialDialog(t, map[int]propApply{
		0: dropLogins(sc, "a"),
		1: func(context.Context) error { return errRefused },
	})

	runAndWait(t, d, d.runScript)

	if got := d.DirtyPages(); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("dirty pages = %v, want both — a script changes nothing", got)
	}
}

// runApplySteps' own account: which step stopped the run, and whether it had
// written by then.
func TestRunApplyStepsReportsWhatReachedTheServer(t *testing.T) {
	sc := partialApplyConn(t)
	for _, tc := range []struct {
		name string
		fns  []propApply
		want applyProgress
	}{
		{"all ran", []propApply{dropLogins(sc, "a"), nil, dropLogins(sc, "b")},
			applyProgress{completed: 2, stopped: 3}},
		{"second wrote then failed", []propApply{dropLogins(sc, "a"), nil, dropLogins(sc, "b", "refused")},
			applyProgress{completed: 1, stopped: 2, wrote: true}},
		{"first failed before writing", []propApply{dropLogins(sc, "refused"), dropLogins(sc, "b")},
			applyProgress{completed: 0, stopped: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runApplySteps(context.Background(), tc.fns)
			if got != tc.want {
				t.Errorf("progress = %+v, want %+v", got, tc.want)
			}
		})
	}
	// Under WithScript every statement is collected, not run: the step that
	// completed reached no server.
	ctx, _ := gosmo.WithScript(context.Background())
	got, err := runApplySteps(ctx, []propApply{
		dropLogins(sc, "a"),
		func(context.Context) error { return errRefused },
	})
	if err == nil || got.anyCommitted() || got.committed(0) || got.stopped != 1 {
		t.Errorf("scripted progress = %+v, err = %v; want stopped at 1 with nothing written", got, err)
	}
}

// newPartialObjectDialog is a two-page create dialog over sc whose pages run
// general and membership, and which counts its folder refreshes.
func newPartialObjectDialog(t *testing.T, sc *db.ServerConn, general, membership propApply) (*newObjectDialog[testPrefetch], *App, *int) {
	t.Helper()
	a := newTestApp()
	refreshes := 0
	d := &newObjectDialog[testPrefetch]{}
	d.init(a, newObjectConfig[testPrefetch]{
		title: "New User",
		noun:  "User",
		pages: []string{"General", "Membership"},
		fetch: func(context.Context, *db.ServerConn) (*testPrefetch, error) { return &testPrefetch{}, nil },
		build: func(*testPrefetch) {
			d.forms = []*propsheet.Form{propsheet.NewForm(), propsheet.NewForm()}
			d.applyFns = []propApply{general, membership}
			d.objectName = func() string { return "bob" }
			d.preflight = func() error { return nil }
		},
		refresh: func(*db.ServerConn) { refreshes++ },
	})
	d.show(sc)
	waitAndDrain(t, a)
	if d.prefetch == nil {
		t.Fatal("setup: the prefetch never landed")
	}
	return d, a, &refreshes
}

// The CREATE landed and a later page was refused: the object exists, so the
// dialog must say so, show it in Object Explorer, and not send the CREATE again
// into "already exists".
func TestNewObjectPartialFailureCountsAsCreated(t *testing.T) {
	sc := partialApplyConn(t)
	d, a, refreshes := newPartialObjectDialog(t, sc,
		dropLogins(sc, "a"),
		func(context.Context) error { return errRefused })

	d.runApply(false)
	drainUntil(t, a, func() bool { return !d.Applying() }, "the create to finish")

	if !d.created {
		t.Error("created = false after the CREATE landed — the next Apply re-sends it")
	}
	if *refreshes != 1 {
		t.Errorf("folder refreshed %d times, want 1", *refreshes)
	}
	msg := d.Message()
	for _, want := range []string{`User "bob" was created`, "Membership failed", "Properties", "refused"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
}

// Nothing reached the server: the ordinary failure, and Apply may be retried.
func TestNewObjectFailureBeforeAnyWriteIsNotCreated(t *testing.T) {
	sc := partialApplyConn(t)
	d, a, refreshes := newPartialObjectDialog(t, sc,
		dropLogins(sc, "refused"),
		dropLogins(sc, "b"))

	d.runApply(false)
	drainUntil(t, a, func() bool { return !d.Applying() }, "the create to finish")

	if d.created || *refreshes != 0 {
		t.Errorf("created = %v, refreshes = %d; want false, 0", d.created, *refreshes)
	}
	if strings.Contains(d.Message(), "was created") {
		t.Errorf("message %q claims a create that never ran", d.Message())
	}
}

// Script Changes reports the error as it always did: nothing was created.
func TestNewObjectScriptFailureIsNotCreated(t *testing.T) {
	sc := partialApplyConn(t)
	d, a, refreshes := newPartialObjectDialog(t, sc,
		dropLogins(sc, "a"),
		func(context.Context) error { return errRefused })

	d.runScript()
	drainUntil(t, a, func() bool { return !d.Applying() }, "the script to finish")

	if d.created || *refreshes != 0 || strings.Contains(d.Message(), "was created") {
		t.Errorf("created = %v, refreshes = %d, message = %q; want an uncreated dialog and the plain error",
			d.created, *refreshes, d.Message())
	}
}
