// Command plandemo shows a single planview.PlanView full-screen, for checking
// the execution-plan viewer against real plan files without the full app. Not
// in the release build.
//
//	plandemo <plan.xml|plan.sqlplan>
//	plandemo -live <recording.json>
//
// -live replays a recorded Live Query Statistics run (see replay.go and
// testdata/live_crossjoin.json): the in-flight plan with each recorded
// sys.dm_exec_query_profiles snapshot in turn, then the actual plan. Space
// pauses and resumes, F5 restarts.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func main() {
	var (
		plan *showplan.Plan
		rp   *replay
		err  error
	)
	switch {
	case len(os.Args) == 3 && os.Args[1] == "-live":
		rp, err = loadReplay(os.Args[2])
	case len(os.Args) == 2:
		plan, err = loadPlan(os.Args[1])
	default:
		fmt.Fprintln(os.Stderr, "usage: plandemo <plan.xml|plan.sqlplan>\n       plandemo -live <recording.json>")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "plandemo:", err)
		os.Exit(1)
	}

	s, err := core.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "plandemo:", err)
		os.Exit(1)
	}
	defer s.Fini()

	view := planview.New()
	view.SetActive(true)
	// tick stays nil — a channel that never fires — unless a replay is
	// running.
	var tick <-chan time.Time
	if rp != nil {
		rp.restart(view)
		ticker := time.NewTicker(rp.interval)
		defer ticker.Stop()
		tick = ticker.C
	} else {
		view.SetPlan(plan)
	}

	w, h := s.Size()
	view.SetBounds(0, 0, w, h)
	view.Draw(s)
	s.Show()

	for {
		select {
		case <-tick:
			if !rp.step(view) {
				continue
			}
		case ev := <-s.EventQ():
			switch e := ev.(type) {
			case *tcell.EventResize:
				s.Sync()
				w, h := s.Size()
				view.SetBounds(0, 0, w, h)
			case *tcell.EventKey:
				// 'q' quits the harness; a reusable control shouldn't own an
				// app-lifecycle key.
				if e.Key() == tcell.KeyCtrlQ || (core.EvRune(e) == 'q' && e.Modifiers() == 0) {
					return
				}
				switch {
				case rp != nil && core.EvRune(e) == ' ':
					rp.paused = !rp.paused
				case rp != nil && e.Key() == tcell.KeyF5:
					rp.restart(view)
				default:
					view.HandleKey(e)
				}
			case *tcell.EventMouse:
				view.HandleMouse(e)
			}
		}
		view.Draw(s)
		s.Show()
	}
}

func loadPlan(path string) (*showplan.Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return showplan.Parse(data)
}
