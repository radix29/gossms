package tui

import (
	"context"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// pageJobHistory is the read-only History page: the job's own run-level
// history (step_id = 0 rows — one per execution, not per step, matching
// the mockup's "Recent job history" list) plus the selected run's detail.
// "Invoked by" from the mockup isn't a separate field here — SQL Server
// Agent already writes that into the step_id=0 row's own Message text
// (e.g. "...The Job was invoked by Schedule 5..."), so Message alone
// carries it without inventing a field sysjobhistory doesn't have.
// Ctrl+C on the Message row copies it (StaticRow's built-in CopyText) —
// no separate "Copy Message" button needed. There's nothing to Apply, so
// this page's apply is nil.
func pageJobHistory(d *PropDialog, sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "History",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			j, err := findAgentJob(ctx, sc, *jobName)
			if err != nil {
				return nil, nil, err
			}
			history, err := j.HistoryContext(ctx, 0)
			if err != nil {
				return nil, nil, err
			}
			runs := make([]*jobHistoryRun, 0, len(history))
			for _, h := range history {
				if h.StepID != 0 {
					continue // per-step detail row, not a run-level outcome
				}
				runs = append(runs, &jobHistoryRun{
					date: formatSQLDate(h.RunDate), outcome: formatJobOutcome(h.Outcome),
					duration: formatHMS(h.Duration), message: h.Message,
				})
			}

			cols := []string{"Date/Time", "Outcome", "Duration"}
			rows := make([][]string, len(runs))
			for i, r := range runs {
				rows[i] = []string{r.date, r.outcome, r.duration}
			}
			grid := controls.NewDataGrid()
			grid.SetData(cols, rows)

			dateStatic := propsheet.Static("Run date", "")
			outcomeStatic := propsheet.Static("Outcome", "")
			durationStatic := propsheet.Static("Duration", "")
			messageStatic := propsheet.Static("Message", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(runs) {
					dateStatic.SetValue("")
					outcomeStatic.SetValue("")
					durationStatic.SetValue("")
					messageStatic.SetValue("")
					return
				}
				r := runs[row]
				dateStatic.SetValue(r.date)
				outcomeStatic.SetValue(r.outcome)
				durationStatic.SetValue(r.duration)
				messageStatic.SetValue(r.message)
			}
			grid.OnSelectRow = syncFromSelection
			if len(runs) > 0 {
				syncFromSelection(0)
			}

			viewFullBtn := widgets.NewButton("View Full History", func() {
				d.app.showAgentJobHistory(sc, *jobName)
			})

			f := propsheet.NewForm(
				propsheet.Section("Recent job history"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Section("Selected run details"),
				dateStatic, outcomeStatic, durationStatic, messageStatic,
				propsheet.Buttons(viewFullBtn),
			)
			return f, nil, nil
		},
	}
}

// jobHistoryRun is one already-formatted row for the History page's grid
// and detail panel.
type jobHistoryRun struct {
	date, outcome, duration, message string
}
