package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// log_viewer_load.go is the read: which files the panel is pointed at, the
// concurrent read of each one, and the recycle that renumbers them all. The
// rows it produces are shaped for the grid in log_viewer_rows.go.

// ShowLog points the panel at a single log file and reads it. Reopening from
// another tree node comes through here, so an already-open viewer switches
// files instead of a second one being created — and lands on the one-file view
// however many files were merged before.
func (lv *LogViewer) ShowLog(logType gosmo.ErrorLogType, logNum int) {
	lv.ShowLogs(logType, []logFileRef{{Type: logType, Num: logNum}})
}

// ShowLogs points the panel at a set of files and reads them. The set may span
// both families; logType is what the selectors fall back to when it does.
// An empty set is the current log of logType: a selection the user emptied
// would otherwise leave the grid with nothing to describe and no way back.
func (lv *LogViewer) ShowLogs(logType gosmo.ErrorLogType, refs []logFileRef) {
	if len(refs) == 0 {
		refs = []logFileRef{{Type: logType, Num: 0}}
	}
	lv.sel = slices.Clone(refs)
	// By family first, then by archive number. The merge order is the
	// selection's order (see sortLogRowsDesc), so a set the checklist built by
	// ticking rows in whatever order the user reached them has to be brought
	// back to one canonical order — otherwise the same two files merge
	// differently depending on which was ticked first.
	slices.SortFunc(lv.sel, func(a, b logFileRef) int {
		if a.Type != b.Type {
			return int(a.Type) - int(b.Type)
		}
		return a.Num - b.Num
	})
	// The selectors still address exactly one family, since Recycle and the
	// single-file picker can only mean one: the selection's own family when it
	// has one, and whatever was on screen before when it is mixed.
	lv.logType = selectionFamily(lv.sel, logType)
	lv.detailScroll = 0
	lv.Load()
}

// Refresh re-reads the current file and re-enumerates both families (F5 or the
// toolbar). The enumeration is dropped rather than refreshed: a cycled log
// renumbers every archive, so only a fresh read corrects the cached list.
func (lv *LogViewer) Refresh() {
	lv.files = make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile)
	lv.Load()
}

// Load reads the selected files in the background and applies the merged
// result on the UI goroutine. The family's enumeration rides along, so the file
// selector has its list without a second round trip.
func (lv *LogViewer) Load() {
	if !lv.app.isConnected(lv.conn) {
		lv.entries, lv.shown, lv.readErrs = nil, nil, nil
		lv.grid.SetData(lv.gridColumns(), nil)
		lv.setStatus("Not connected")
		return
	}
	// Begin supersedes and cancels whatever read is out: this one replaces it.
	// One cancel for the panel to pull, but a fresh deadline per file read
	// below: sharing one logReadTimeout lets a slow sp_enumerrorlogs eat the
	// read's half of it, timing out the file the user asked for because the
	// *list* was slow. The context is released on the UI goroutine, by the
	// Done in the callback or in readPanicked — never from the read goroutine,
	// which must not touch lv.
	ctx, seq := lv.read.Begin(lv.conn.Context())
	lv.busy = true
	lv.setStatus(fmt.Sprintf("Reading %s%s...", lv.scopeLabel(), lv.searchSuffix()))
	lv.refreshToolLabels()

	search := lv.search
	// Snapshotted, not read from lv on the goroutine: the selection can be
	// changed again while this read is out, and the result has to describe the
	// files it actually asked for.
	refs := slices.Clone(lv.sel)
	sc := lv.conn
	// safegoRepair, not safego: busy is cleared in the callback below, which a
	// panic on the read goroutine never reaches, and toolsEnabled gates the
	// whole toolbar on it — Refresh, Export and both selectors would sit inert
	// until the panel was closed.
	lv.app.safegoRepair("reading an error log", func() { lv.readPanicked(seq) }, func() {
		// Both families, not only the one on screen: the file checklist offers
		// a cross-family selection, so it needs the other family's archive
		// numbering before the user opens it — and fetching that lazily would
		// put a round trip behind a menu keypress. A family that cannot be
		// enumerated (an instance with no Agent) is simply left out of the
		// checklist, exactly as it is today.
		//
		// The enumeration runs alongside the reads rather than ahead of them:
		// nothing in the read depends on it, and the two families cost ~50 ms
		// that came straight off a ~150 ms load on 2016 and 2017. (2025 shows
		// no gain — it appears to serialise the two server-side — and no loss.)
		enums := make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile, len(logFamilies))
		var enumerated sync.WaitGroup
		enumerated.Add(1)
		lv.app.safego("enumerating error logs", func() {
			defer enumerated.Done()
			for _, t := range logFamilies {
				enumCtx, enumCancel := context.WithTimeout(ctx, logReadTimeout)
				files, err := sc.Server.EnumErrorLogs(enumCtx, t)
				enumCancel()
				if err == nil {
					enums[t] = files
				}
			}
		})
		rows, readErrs := readLogFiles(lv.app, ctx, sc, refs, search)
		enumerated.Wait()
		lv.app.postAndWake(func() {
			if !lv.read.Done(seq) {
				return
			}
			lv.busy = false
			for t, files := range enums {
				lv.files[t] = files
			}
			lv.refreshToolLabels()
			// Only a selection where *nothing* could be read is an error: with
			// one archive unreadable out of four, the grid holds the other
			// three and summary says how many landed.
			if len(readErrs) == len(refs) && len(readErrs) > 0 {
				lv.entries, lv.shown, lv.readErrs = nil, nil, nil
				lv.grid.SetError(displayError(readErrs[0].err))
				return
			}
			lv.readErrs = readErrs
			lv.entries = sortLogRowsDesc(rows)
			lv.applyFilter() // resets detailScroll itself
		})
	})
}

// readLogFiles reads every ref and returns the rows in ref order together with
// whichever files failed. It runs on the read goroutine, off the UI one.
//
// Each read gets its own logReadTimeout deadline under the panel's one
// cancellable ctx: sharing a single deadline across N files would let the first
// slow archive eat the budget of the ones behind it. The pool is bounded for
// the reason the Databases folder's is — an instance can be configured to keep
// 99 archives, and xp_readerrorlog parses the file server-side.
//
// Results are collected into a slice indexed by ref rather than appended as
// they finish, so the merge order is the selection's order however the reads
// interleave — which is what makes a timestamp tie break the same way twice.
func readLogFiles(app *App, ctx context.Context, sc *db.ServerConn, refs []logFileRef, search gosmo.LogSearch) ([]logRow, []logFileError) {
	per := make([][]logRow, len(refs))
	errs := make([]error, len(refs))
	// Seeded failed and cleared on success, not the other way round: a worker
	// whose read panicked never reaches either assignment, and a file that was
	// never read must be reported as unread rather than passing for an empty
	// one — an empty archive and an archive nobody read look identical here.
	for i := range errs {
		errs[i] = errLogFileNotRead
	}

	// No onPanic: the errLogFileNotRead seed already reports a panicked read.
	app.fanOut(len(refs), "reading an error log", func(i int) {
		readCtx, cancel := context.WithTimeout(ctx, logReadTimeout)
		defer cancel()
		entries, err := sc.Server.ReadLogFiltered(readCtx, refs[i].Type, refs[i].Num, search)
		if err != nil {
			errs[i] = err
			return
		}
		rows := make([]logRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, logRow{entry: e, ref: refs[i]})
		}
		per[i], errs[i] = rows, nil
	}, nil)

	total := 0
	for _, rows := range per {
		total += len(rows)
	}
	out := make([]logRow, 0, total)
	var failed []logFileError
	for i := range refs {
		if errs[i] != nil {
			failed = append(failed, logFileError{ref: refs[i], err: errs[i]})
			continue
		}
		out = append(out, per[i]...)
	}
	return out, failed
}

// errLogFileNotRead is readLogFiles' seed for a file whose read never
// finished — it panicked, and fanOut recovered it and moved on, leaving the
// seed. It is never the reason a read *failed*, only the reason one is missing.
var errLogFileNotRead = errors.New("the read did not finish")

// readPanicked releases the busy latch after a panic on the read goroutine —
// Load's safegoRepair step. Guarded by seq like the normal completion path: a
// newer Load set busy for itself, and clearing it here would re-enable a
// toolbar whose read is still out.
func (lv *LogViewer) readPanicked(seq int) {
	if !lv.read.Done(seq) {
		return
	}
	lv.busy = false
	lv.refreshToolLabels()
	lv.setStatus("Read stopped unexpectedly — see the log for details")
}

// recycle closes the current log of the family on screen and starts a new one,
// after confirming. On success it reloads, replacing the archive numbering the
// file selector draws from — the cycle renumbered all of it.
func (lv *LogViewer) recycle() {
	if !lv.app.requireConn(lv.conn) {
		return
	}
	sc, logType := lv.conn, lv.logType
	// Latched before the question, not in the answer: busy is what stops a read
	// starting underneath the cycle, and the confirm dialog doesn't stop F5
	// reaching the panel — a Load begun while the question was up would clear
	// busy from under the cycle it knows nothing about.
	lv.busy = true
	lv.app.confirmDialog.ShowConfirm("Recycle Log", cycleLogMessage(logType, sc.Opts.Server), func(confirmed bool) {
		if !confirmed {
			lv.busy = false
			return
		}
		lv.setStatus(fmt.Sprintf("Recycling the %s error log...", logType))
		// The job's repair for the same reason Load uses safegoRepair: busy is
		// cleared in the completion, which a panic never reaches, and
		// toolsEnabled gates the whole toolbar on it.
		lv.app.runWithProgress(progressJob{
			title:   "Recycle Log",
			message: fmt.Sprintf("Recycling the %s error log...", logType),
			what:    "cycling an error log",
			sc:      sc,
			timeout: logReadTimeout,
			repair:  lv.recyclePanicked,
		}, func(ctx context.Context, _ progressReport) error {
			return sc.Server.CycleLog(ctx, logType)
		}, func(err error, cancelled bool) {
			lv.busy = false
			switch {
			case cancelled:
				// Reloaded anyway: the cycle may have landed before the
				// cancel reached the server.
				lv.app.setStatus(fmt.Sprintf("Recycling the %s error log cancelled", logType))
				lv.reanchorAfterCycle(logType)
				lv.Refresh()
			case err != nil:
				lv.setStatus(fmt.Sprintf("Recycle failed: %v", withPermissionAdvice(err)))
			default:
				lv.reanchorAfterCycle(logType)
				lv.Refresh()
			}
		})
	})
}

// reanchorAfterCycle drops a multi-file selection back to the current log when
// a family it draws from has just been cycled.
//
// A cycle renumbers every archive one higher and deletes the oldest, so the
// numbers a set was chosen by no longer name the files it was chosen from —
// re-reading them would silently hand back a different set, one file of which
// may not exist any more. A single-file view keeps its number, which is the
// behaviour it has always had: the user asked for "Archive #1" and gets
// whatever is now Archive #1.
//
// A mixed selection is re-anchored by *any* family in it being cycled, not
// only the one the selectors address: half a merged set going stale is the
// same silent lie as all of it, and the cycled family is the one the user was
// just looking at.
func (lv *LogViewer) reanchorAfterCycle(logType gosmo.ErrorLogType) {
	if !lv.multiFile() || !slices.ContainsFunc(lv.sel, func(r logFileRef) bool { return r.Type == logType }) {
		return
	}
	lv.logType = logType
	lv.sel = []logFileRef{{Type: logType, Num: 0}}
}

// recyclePanicked releases the busy latch after a panic on the cycle goroutine
// — recycle's safegoRepair step. No seq guard, unlike readPanicked: busy was
// held across the whole cycle, so nothing else can have started.
func (lv *LogViewer) recyclePanicked() {
	lv.busy = false
	lv.setStatus("Recycle stopped unexpectedly — see the log for details")
}

// cycleLogMessage is the confirmation question for recycling a log, shared by
// the toolbar and the Object Explorer folder's menu. It names what is lost: the
// archives are renumbered, and the instance drops the oldest once holding as
// many as it is configured to keep.
func cycleLogMessage(logType gosmo.ErrorLogType, server string) string {
	return fmt.Sprintf(
		"Close the current %s error log on %s and start a new one?\n\n"+
			"Each archive is renumbered one higher, and the oldest is deleted once "+
			"the instance holds as many archives as it is configured to keep.",
		logType, server)
}
