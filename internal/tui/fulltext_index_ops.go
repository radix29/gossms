package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// fulltext_index_ops.go is what the table menu's Full-Text index cascade does:
// Enable/Disable, Delete, Start Full/Incremental Population, Stop Population,
// Track Changes, Apply Tracked Changes — and the Background Tasks entry that
// follows a population to its end. The cascade itself is tableFullTextMenu
// (explorer_fulltext.go).
//
// Every action reads the index first. The tree does not know whether a table has
// one, and the server answers several of these with an informational message and
// no error — STOP POPULATION under AUTO or MANUAL change tracking ("Stop crawl
// request is ignored"), START … POPULATION while one runs ("is ignored because a
// population is currently active") — which gosmo's exec does not surface. Without
// the read, those report success for a statement that did nothing.

// fullTextIndexOp is one cascade action.
type fullTextIndexOp struct {
	title string // the confirmation's and the progress dialog's title
	doing string // the progress dialog's message, before the table's name
	done  string // the status line on success, before the table's name

	// refuse says why the action makes no sense on i as it stands, or "".
	refuse func(i *gosmo.FullTextIndex) string
	// confirm is the question asked before running, or "" to run at once.
	confirm func(i *gosmo.FullTextIndex, table string) string
	run     func(ctx context.Context, i *gosmo.FullTextIndex) error

	// watch labels the population the action starts, followed in Background
	// Tasks; "" starts none. maybe marks an action that starts one only in
	// some states (ENABLE and a change-tracking switch with tracking on): its
	// task is registered only once a population is seen.
	watch string
	maybe bool
}

// runFullTextIndexOp reads the table's full-text index, refuses or confirms
// as op says, runs op behind the progress dialog, and follows the population
// it starts.
func (a *App) runFullTextIndexOp(sc *db.ServerConn, node *explorerNode, op fullTextIndexOp) {
	if !a.requireConn(sc) {
		return
	}
	dbName, schema, table := node.data.DBName, node.data.Schema, node.data.Name
	display := fqn(schema, table)
	a.safego("reading a full-text index", func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		i, err := findFullTextIndex(ctx, sc, dbName, schema, table)
		a.postAndWake(func() {
			switch {
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed to read the full-text index on %s: %v", display, err))
				return
			case i == nil:
				a.alertDialog.ShowAlert(op.title, display+" has no full-text index.")
				return
			}
			if op.refuse != nil {
				if why := op.refuse(i); why != "" {
					a.alertDialog.ShowAlert(op.title, why)
					return
				}
			}
			run := func() { a.startFullTextIndexOp(sc, dbName, display, i, op) }
			prompt := ""
			if op.confirm != nil {
				prompt = op.confirm(i, display)
			}
			if prompt == "" {
				run()
				return
			}
			a.confirmDialog.ShowConfirm(op.title, prompt, func(confirmed bool) {
				if confirmed {
					run()
				}
			})
		})
	})
}

func (a *App) startFullTextIndexOp(sc *db.ServerConn, dbName, display string, i *gosmo.FullTextIndex, op fullTextIndexOp) {
	before := i.CrawlStart
	a.runWithProgress(progressJob{
		title:   op.title,
		message: op.doing + " " + display + "...",
		what:    "a full-text index action",
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		return op.run(ctx, i)
	}, func(err error, cancelled bool) {
		switch {
		case cancelled:
			a.setStatus(op.title + " on " + display + " cancelled")
		case err != nil:
			a.setStatus(fmt.Sprintf("%s on %s failed: %v", op.title, display, withPermissionAdvice(err)))
		default:
			a.setStatus(op.done + " " + display)
		}
		if err == nil || cancelled {
			a.refreshFullTextDetails(sc, dbName)
		}
		if err == nil && op.watch != "" {
			a.watchFullTextPopulation(sc, dbName, i.Schema, i.Table, before, op.watch, op.maybe)
		}
	})
}

// fullTextPollInterval is how often a followed population is read again.
// A variable so tests need not wait it out.
var fullTextPollInterval = 2 * time.Second

// fullTextNoPopulationPolls is how many idle reads showing no new crawl end a
// follow as "no population ran". The server stamps crawl_start_date before
// ALTER … START returns (seen on 17), so this is a backstop, not a wait.
const fullTextNoPopulationPolls = 3

// fullTextFollowReadRetries is how many reads in a row may fail before the
// follow gives up. The crawl holds locks on the very catalog views the poll
// reads, and the poll can be picked as a deadlock victim (Msg 1205, seen on 17
// against a 1.5M-row population): the population is unharmed, so one lost
// read must not end the follow.
const fullTextFollowReadRetries = 3

// watchFullTextPopulation follows the population an action started on the table: a
// Background Tasks entry whose message tracks the index's populate status and
// counters, finished when sys.fulltext_indexes shows a crawl that started after
// before has completed — a stop and a DISABLE complete it too (has_crawl_completed
// = 1), so "finished" is all it can say.
//
// maybe registers the task only once the first read shows a population: an ENABLE
// or a change-tracking switch starts one only in some states.
//
// Cancel stops following, not the population: STOP POPULATION is ignored under
// AUTO and MANUAL change tracking, so a Cancel that sent it would claim to stop
// what it did not.
func (a *App) watchFullTextPopulation(sc *db.ServerConn, dbName, schema, table string, before time.Time, label string, maybe bool) {
	label = label + " — " + fqn(schema, table)
	follow := func(task *Task, ctx context.Context, tbl *gosmo.Table) {
		a.safegoRepair("following a full-text population", func() { a.markTaskDone(task, errTaskPanicked) }, func() {
			a.followFullTextPopulation(ctx, task, tbl, before)
		})
	}
	if !maybe {
		task, ctx := a.startTask(sc.Server.Context(), label)
		a.safegoRepair("following a full-text population", func() { a.markTaskDone(task, errTaskPanicked) }, func() {
			tbl, err := findTable(ctx, sc, dbName, schema, table)
			if err != nil {
				a.postTaskDone(task, err)
				return
			}
			a.followFullTextPopulation(ctx, task, tbl, before)
		})
		return
	}
	a.safego("checking for a full-text population", func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		tbl, err := findTable(ctx, sc, dbName, schema, table)
		if err != nil {
			return
		}
		cur, err := tbl.FullTextIndex(ctx)
		if err != nil || (cur.CrawlStart.Equal(before) && cur.PopulateStatus == gosmo.FullTextTableIdle) {
			return
		}
		a.postAndWake(func() {
			task, ctx := a.startTask(sc.Server.Context(), label)
			follow(task, ctx, tbl)
		})
	})
}

// followFullTextPopulation is the follow loop, on its own goroutine: each
// read posts progress until fullTextPopulationPoll says the population is
// over.
func (a *App) followFullTextPopulation(ctx context.Context, task *Task, tbl *gosmo.Table, before time.Time) {
	ticker := time.NewTicker(fullTextPollInterval)
	defer ticker.Stop()
	idle, failed := 0, 0
	for {
		rctx, cancel := context.WithTimeout(ctx, childFetchTimeout)
		cur, err := tbl.FullTextIndex(rctx)
		cancel()
		if ctx.Err() != nil {
			a.postAndWake(func() { a.endFullTextFollow(task, ctx.Err(), "") })
			return
		}
		dropped := errors.Is(err, gosmo.ErrNotFound)
		if dropped {
			err = fmt.Errorf("the full-text index on %s was dropped", tbl.FullName())
		}
		if err != nil {
			if failed++; dropped || failed > fullTextFollowReadRetries {
				a.postTaskDone(task, err)
				return
			}
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
			continue
		}
		failed = 0
		done, msg := fullTextPopulationPoll(before, cur, &idle)
		if done {
			a.postAndWake(func() { a.endFullTextFollow(task, nil, msg) })
			return
		}
		a.postProgress(task, -1, msg)
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

// endFullTextFollow finishes the task on the UI goroutine. summary replaces
// markTaskDone's bare "completed" with the counters; a Cancel says the
// population itself goes on.
func (a *App) endFullTextFollow(task *Task, err error, summary string) {
	a.markTaskDone(task, err)
	switch {
	case task.Cancelled:
		a.setStatus("Stopped following " + task.Label + " — the population itself goes on")
	case err == nil && summary != "":
		a.setStatus(task.Label + " " + summary)
	}
}

// fullTextPopulationPoll reads one poll of a followed population: done when
// a crawl that started after before has completed, or when idle reads with
// no new crawl reach fullTextNoPopulationPolls; msg is the task's progress
// line, or the finished summary when done.
func fullTextPopulationPoll(before time.Time, cur *gosmo.FullTextIndex, idle *int) (done bool, msg string) {
	newCrawl := !cur.CrawlStart.Equal(before)
	counts := fullTextCounts(cur)
	switch {
	case newCrawl && cur.CrawlCompleted:
		return true, "finished — " + counts
	case !newCrawl && cur.PopulateStatus == gosmo.FullTextTableIdle:
		*idle++
		if *idle >= fullTextNoPopulationPolls {
			return true, "— no population ran"
		}
		return false, "waiting for the population to start"
	}
	// The task's label already names the population; only a state it does
	// not imply is worth the words.
	switch cur.PopulateStatus {
	case gosmo.FullTextTableBackgroundUpdate, gosmo.FullTextTableThrottledOrPaused:
		return false, cur.PopulateStatus.String() + " — " + counts
	}
	return false, counts
}

// fullTextCounts is the OBJECTPROPERTYEX counters a progress line shows.
func fullTextCounts(i *gosmo.FullTextIndex) string {
	s := countOf(i.ItemCount, "row") + " indexed"
	if i.PendingChanges > 0 {
		s += ", " + countOf(i.PendingChanges, "change") + " pending"
	}
	if i.FailCount > 0 {
		s += ", " + strconv.Itoa(i.FailCount) + " failed"
	}
	return s
}

// countOf is n with noun, pluralised.
func countOf(n int, noun string) string {
	return strconv.Itoa(n) + " " + noun + pluralSuffix(n)
}

// -- The actions ---------------------------------------------------------------

// fullTextNotEnabled refuses a population on a disabled index, which the
// server refuses with Msg 7660.
func fullTextNotEnabled(i *gosmo.FullTextIndex) string {
	if !i.IsEnabled {
		return "The full-text index on " + i.FullName() + " is disabled. Enable it first."
	}
	return ""
}

// fullTextBusy refuses a START while a population runs, which the server
// ignores with only a warning.
func fullTextBusy(i *gosmo.FullTextIndex) string {
	if i.PopulateStatus != gosmo.FullTextTableIdle {
		return fmt.Sprintf("A population is already running on %s (%s). The server ignores a second one; wait for it, or stop it first.",
			i.FullName(), i.PopulateStatus)
	}
	return ""
}

func fullTextEnableOp() fullTextIndexOp {
	return fullTextIndexOp{
		title: "Enable Full-Text Index", doing: "Enabling the full-text index on", done: "Enabled the full-text index on",
		refuse: func(i *gosmo.FullTextIndex) string {
			if i.IsEnabled {
				return "The full-text index on " + i.FullName() + " is already enabled."
			}
			return ""
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.Enable(ctx) },
		// With change tracking on, ENABLE starts a full population.
		watch: "Population", maybe: true,
	}
}

func fullTextDisableOp() fullTextIndexOp {
	return fullTextIndexOp{
		title: "Disable Full-Text Index", doing: "Disabling the full-text index on", done: "Disabled the full-text index on",
		refuse: func(i *gosmo.FullTextIndex) string {
			if !i.IsEnabled {
				return "The full-text index on " + i.FullName() + " is already disabled."
			}
			return ""
		},
		// Full-text queries still read a disabled index (CONTAINS answered
		// on 17); what stops is its upkeep.
		confirm: func(i *gosmo.FullTextIndex, table string) string {
			return "Disable the full-text index on " + table + "?\n\n" +
				"It keeps its data and full-text queries still read it, but changes to the table stop reaching it until it is enabled again, and a running population ends."
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.Disable(ctx) },
	}
}

func fullTextDeleteOp() fullTextIndexOp {
	return fullTextIndexOp{
		title: "Delete Full-Text Index", doing: "Deleting the full-text index on", done: "Deleted the full-text index on",
		confirm: func(i *gosmo.FullTextIndex, table string) string {
			return "Delete the full-text index on " + table + " from catalog " + gosmo.QuoteName(i.Catalog) + "?\n\n" +
				"Full-text queries on the table (CONTAINS, FREETEXT) fail until an index is defined again."
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.Drop(ctx) },
	}
}

func fullTextStartOp(kind gosmo.FullTextPopulationKind) fullTextIndexOp {
	title, watch, done := "Start Full Population", "Full population", "Started a full population of"
	if kind == gosmo.FullTextPopulationIncremental {
		title, watch, done = "Start Incremental Population", "Incremental population", "Started an incremental population of"
	}
	return fullTextIndexOp{
		title: title, doing: "Starting a population of", done: done,
		refuse: func(i *gosmo.FullTextIndex) string {
			if why := fullTextNotEnabled(i); why != "" {
				return why
			}
			// Under AUTO the DMV always lists an AUTO population ("Starting")
			// while TableFulltextPopulateStatus reads idle, and the server
			// answers a START with only "ignored because a population is
			// currently active" (14 and 17, W20). MANUAL starts it.
			if i.ChangeTracking == gosmo.FullTextChangeTrackingAuto {
				return fmt.Sprintf("Change tracking on %s is Automatic, and the server ignores %s while it is: changes reach the index as they happen.\n\n"+
					"To repopulate it anyway, set Track Changes to Manual first.",
					i.FullName(), title)
			}
			return fullTextBusy(i)
		},
		run:   func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.StartPopulation(ctx, kind) },
		watch: watch,
	}
}

func fullTextStopOp() fullTextIndexOp {
	return fullTextIndexOp{
		title: "Stop Population", doing: "Stopping the population of", done: "Stopped the population of",
		refuse: func(i *gosmo.FullTextIndex) string {
			if i.PopulateStatus == gosmo.FullTextTableIdle {
				return "No population is running on " + i.FullName() + "."
			}
			// Under AUTO the server answers "Full-text auto propagation is
			// on. Stop crawl request is ignored."; under MANUAL "The ongoing
			// population is necessary to ensure an up-to-date index" — both
			// warnings, the population untouched.
			if i.ChangeTracking != gosmo.FullTextChangeTrackingOff {
				return fmt.Sprintf("Change tracking on %s is %s, and the server ignores Stop Population while it tracks changes.\n\n"+
					"Set Track Changes to Off first: the running population carries on, and Stop Population then ends it.",
					i.FullName(), fullTextTrackingWord(i.ChangeTracking))
			}
			return ""
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.StopPopulation(ctx) },
	}
}

// fullTextTrackChangesOp switches change tracking. Turning it on from OFF
// starts a full population (seen for MANUAL on 17); MANUAL to AUTO does not.
func fullTextTrackChangesOp(ct gosmo.FullTextChangeTracking) fullTextIndexOp {
	word := fullTextTrackingWord(ct)
	op := fullTextIndexOp{
		title: "Track Changes", doing: "Setting change tracking to " + word + " on", done: "Change tracking is now " + word + " on",
		refuse: func(i *gosmo.FullTextIndex) string {
			if i.ChangeTracking == ct {
				return "Change tracking on " + i.FullName() + " is already " + word + "."
			}
			return ""
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error { return i.SetChangeTracking(ctx, ct) },
	}
	if ct == gosmo.FullTextChangeTrackingOff {
		// The server deletes what MANUAL tracked and had not applied.
		op.confirm = func(i *gosmo.FullTextIndex, table string) string {
			if i.PendingChanges == 0 {
				return ""
			}
			return fmt.Sprintf("Turn change tracking off on %s?\n\n"+
				"The %s tracked but not yet applied are discarded, and from then on only a full or incremental population brings the index up to date.",
				table, countOf(i.PendingChanges, "change"))
		}
	} else {
		op.watch, op.maybe = "Population", true
	}
	return op
}

func fullTextApplyTrackedChangesOp() fullTextIndexOp {
	return fullTextIndexOp{
		title: "Apply Tracked Changes", doing: "Applying tracked changes to", done: "Applying tracked changes to",
		refuse: func(i *gosmo.FullTextIndex) string {
			if why := fullTextNotEnabled(i); why != "" {
				return why
			}
			switch i.ChangeTracking {
			case gosmo.FullTextChangeTrackingOff:
				return "Change tracking on " + i.FullName() + " is Off, so there are no tracked changes to apply."
			case gosmo.FullTextChangeTrackingAuto:
				return "Change tracking on " + i.FullName() + " is Automatic: changes reach the index as they happen, with nothing to apply by hand."
			}
			if why := fullTextBusy(i); why != "" {
				return why
			}
			if i.PendingChanges == 0 {
				return "No tracked changes are waiting to be applied to " + i.FullName() + "."
			}
			return ""
		},
		run: func(ctx context.Context, i *gosmo.FullTextIndex) error {
			return i.StartPopulation(ctx, gosmo.FullTextPopulationUpdate)
		},
		watch: "Apply tracked changes",
	}
}

// fullTextTrackingWord is a CHANGE_TRACKING value as SSMS's Track Changes
// cascade words it.
func fullTextTrackingWord(ct gosmo.FullTextChangeTracking) string {
	switch ct {
	case gosmo.FullTextChangeTrackingAuto:
		return "Automatic"
	case gosmo.FullTextChangeTrackingManual:
		return "Manual"
	}
	return "Off"
}

// refreshFullTextDetails drops the cached Details of dbName's catalogs, which
// show each index's state and counters — the index has no node of its own.
func (a *App) refreshFullTextDetails(sc *db.ServerConn, dbName string) {
	a.detailBrowser.InvalidateWhere(a, func(n *explorerNode) bool {
		return resolveConn(n) == sc && n.data.DBName == dbName &&
			(n.data.Type == NodeFullTextCatalogs || n.data.Type == NodeFullTextCatalog)
	})
}
