// Command plandemo hosts a single planview.PlanView full-screen, for
// visually checking the execution-plan viewer control against real plan
// files without needing the full gossms application. Not part of the
// release build (see .github/workflows/release.yml, which only builds
// cmd/gossms).
package main

import (
	"fmt"
	"os"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: plandemo <plan.xml|plan.sqlplan>")
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "plandemo:", err)
		os.Exit(1)
	}
	plan, err := showplan.Parse(data)
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
	view.SetPlan(plan)
	view.SetActive(true)

	w, h := s.Size()
	view.SetBounds(0, 0, w, h)
	s.Show()

	for ev := range s.EventQ() {
		switch e := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
			w, h := s.Size()
			view.SetBounds(0, 0, w, h)
		case *tcell.EventKey:
			// 'q' quits the demo harness itself — not a PlanView concern,
			// since a reusable control shouldn't own an app-lifecycle key.
			if e.Key() == tcell.KeyCtrlQ || (core.EvRune(e) == 'q' && e.Modifiers() == 0) {
				return
			}
			view.HandleKey(e)
		case *tcell.EventMouse:
			view.HandleMouse(e)
		}
		view.Draw(s)
		s.Show()
	}
}
