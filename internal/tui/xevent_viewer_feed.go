package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_feed.go is the Extended Events viewer's reads: the panel's
// own connection, the poller behind Watch Live Data and the one-shot read
// behind View Target Data. The reader runs on a goroutine and owns its cursor
// while it runs; what it reads is applied on the UI goroutine by
// applyBatch, which is in xevent_viewer_rows.go with the rest of what the
// events become on screen.

const (
	// xePollFast is the interval while events are arriving, xePollIdle the
	// one it backs off to over successive empty reads. With the session's
	// dispatch latency (the Profiler sessions use 3 s — D2) that bounds the
	// lag at a few seconds.
	xePollFast = time.Second
	xePollIdle = 5 * time.Second

	// xeReadChunk caps one event_file read. A first read of a big file set
	// is otherwise all of it in one go; capped, it arrives a chunk per read
	// with the grid updating between, and a read that returned a full chunk
	// is followed at once rather than after the poll interval.
	xeReadChunk = 5000

	// xeReadTimeout bounds one read.
	xeReadTimeout = 60 * time.Second
)

// xeFeedState is the reader's side of the panel, owned by the UI goroutine.
type xeFeedState struct {
	// run is the read in flight: Stop cancels it, and a result from a run
	// superseded by a Start or Refresh is dropped. See latest.
	run  latest
	task *Task

	// running is set while a reader goroutine is out; paused while the grid
	// is frozen (the reader keeps reading into the store); autoScroll while
	// the grid follows the newest event.
	running    bool
	paused     bool
	autoScroll bool

	// reader is the cursor state carried across a Stop and Start of the live
	// feed, so a restart picks up where the last left off rather than re-reading
	// the files. Touched only by the goroutine while running is set, and by
	// the UI goroutine otherwise.
	reader *xeReader

	// connecting is set from panel creation until the connection is up or
	// has failed; connErr is why it failed.
	connecting bool
	connErr    error
	// err is why the last read failed, cleared by the next good one.
	err error

	// source is the target being read, for the status line. missing counts
	// ring_buffer events that aged out between two reads; truncated is set
	// when target_data came back cut; unsequenced when the dedupe fell back
	// to comparing content. pending counts events stored while paused.
	source      string
	missing     int64
	truncated   bool
	unsequenced bool
	pending     int

	// progress is the newest-first read's progress, shown until its events
	// arrive; skipped counts the older files it did not need to read, and
	// rolledOver is set once a live event_file lost its place to rollover.
	progress   string
	skipped    int
	rolledOver bool
	// discarded counts the events a merge read past twice the capacity and
	// dropped as the oldest before they reached the store.
	discarded int
}

// connectXEventViewer opens the panel's connection and starts its read — the
// Activity Monitor pattern (connectForActivityMonitor).
func (a *App) connectXEventViewer(v *XEventViewer) {
	opts := v.host.Opts
	parent := v.host.Server.Context()
	v.feed.connecting = true
	v.updateStatus()
	a.safego("connecting the Extended Events viewer", func() {
		conn, err := db.ConnectContext(parent, opts, db.RoleXEventProfiler)
		a.postAndWake(func() {
			v.feed.connecting = false
			if err != nil {
				v.feed.connErr = err
				v.updateStatus()
				return
			}
			if !a.panelHosted(v) {
				conn.Close()
				return
			}
			conn.SetPeerCredentials(a.peerCredentialsFor)
			v.conn = conn
			v.startFeed()
		})
	})
}

// toggleFeed is the live toolbar's Stop / Start Data Feed.
func (v *XEventViewer) toggleFeed() {
	if v.feed.running {
		v.stopFeed()
		return
	}
	v.startFeed()
}

// stopFeed stops the reader. Its goroutine sees the cancel, posts its last
// batch and ends; feedEnded then clears running.
func (v *XEventViewer) stopFeed() {
	v.feed.run.Cancel()
	if v.feed.task != nil {
		v.feed.task.Cancel()
	}
}

// refreshTarget is View Target Data's Refresh: the target read again from its
// start, into an emptied grid.
func (v *XEventViewer) refreshTarget() {
	if v.feed.running {
		return
	}
	v.feed.reader = nil
	v.store.Clear()
	v.feed.missing, v.feed.truncated, v.feed.pending = 0, false, 0
	v.feed.skipped, v.feed.rolledOver, v.feed.discarded = 0, false, 0
	v.bookmarks = map[uint64]bool{}
	v.rebuildRows()
	v.startFeed()
}

// togglePause freezes or unfreezes the grid. The reader keeps reading while
// paused — nothing is lost, the store takes it — and Resume brings the grid up
// to date in one rebuild.
func (v *XEventViewer) togglePause() {
	v.feed.paused = !v.feed.paused
	if !v.feed.paused {
		v.feed.pending = 0
		v.rebuildRows()
	}
	v.refreshToolLabels()
	v.updateStatus()
}

// clearData empties the store and the grid; a live feed carries on from where
// it is.
func (v *XEventViewer) clearData() {
	v.store.Clear()
	v.feed.missing, v.feed.truncated, v.feed.pending = 0, false, 0
	v.feed.rolledOver = false
	v.bookmarks = map[uint64]bool{}
	v.rebuildRows()
}

// startFeed starts the reader on the panel's connection: a poller for Watch
// Live Data, one pass to the end of the target for View Target Data. It is
// listed in Background Tasks, where it can be cancelled like any other.
func (v *XEventViewer) startFeed() {
	if v.conn == nil || v.feed.running {
		return
	}
	if v.feed.reader == nil {
		v.feed.reader = &xeReader{sc: v.conn, scope: v.scope, session: v.session, target: v.target, live: v.live,
			files: v.files, capacity: v.store.Capacity()}
	}
	label := "Watch Live Data — " + v.session
	switch {
	case v.files != "":
		label = "Merge Extended Event Files — " + v.files
	case !v.live:
		label = "View Target Data — " + v.session + " " + v.target
	}
	task, taskCtx := v.app.startTask(v.conn.Server.Context(), label)
	ctx, seq := v.feed.run.Begin(taskCtx)
	v.feed.task = task
	v.feed.running = true
	v.feed.err = nil
	v.refreshToolLabels()
	v.updateStatus()

	r := v.feed.reader
	v.app.safegoRepair("reading Extended Events", func() { v.feedEnded(seq, task, errTaskPanicked) }, func() {
		err := r.run(ctx, func(b xeBatch) {
			v.app.postAndWake(func() {
				if v.feed.run.Current(seq) {
					v.applyBatch(b)
				}
			})
		})
		v.app.postAndWake(func() { v.feedEnded(seq, task, err) })
	})
}

// feedEnded is the reader's last word: running is released and its task
// finished. A cancel — Stop, the Tasks dialog, Close, a disconnect — is not a
// failure.
func (v *XEventViewer) feedEnded(seq int, task *Task, err error) {
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	v.app.markTaskDone(task, err)
	if !v.feed.run.Done(seq) {
		return
	}
	v.feed.running = false
	v.feed.task = nil
	v.feed.progress = ""
	if err != nil {
		v.feed.err = err
	}
	v.refreshToolLabels()
	v.updateStatus()
	if errors.Is(err, errXENoTarget) && v.live {
		v.offerTarget()
	}
}

// xeBatch is one read's result, posted to the UI goroutine.
type xeBatch struct {
	events      []xevent.Event
	source      string
	missing     int64
	truncated   bool
	unsequenced bool
	progress    string // the newest-first read's progress, for a batch with no events yet
	skipped     int    // older event_files the newest-first read left unread
	rolledOver  bool   // a live event_file's cursor file was gone; reading resumed at the oldest left
	discarded   int    // merged events dropped as the oldest before reaching the store
}

// xeReader reads one session's target. It is not safe for concurrent use:
// startFeed hands it to exactly one goroutine at a time.
type xeReader struct {
	sc      *db.ServerConn
	scope   xeScope
	session string
	target  string // "" for Watch Live Data, which picks one
	live    bool
	// files is Merge Extended Event Files' pattern, read with no session;
	// mergeFiles how many files it listed. See xevent_merge.go.
	files      string
	mergeFiles int
	// capacity is the viewer's store capacity: the newest-first read stops
	// going back once it holds that many events.
	capacity int

	// es is the session as last read by name; source the target being read
	// (event_file or ring_buffer), pattern the event_file's file pattern.
	es      *gosmo.EventSession
	source  string
	pattern string

	// cursor is the event_file position; positioned is set once a live read
	// has found where the session is writing now.
	cursor     gosmo.EventFileCursor
	positioned bool

	// View Target Data on an event_file set: planned once the files have been
	// listed; backlog the files still to read, oldest first and consumed from
	// the end; fileEvents the events of the file being read, and stack the
	// finished files' events, newest file first.
	planned    bool
	backlog    []string
	fileEvents []xevent.Event
	stack      [][]xevent.Event
	stacked    int

	// dedupe and processed are the ring_buffer's: the previous read, to tell
	// new events from held ones, and its processed count, to see events that
	// aged out between two reads.
	dedupe    xevent.Deduper
	processed int64
	primed    bool
}

// run reads until ctx ends — or, for View Target Data, until the target is
// read to its end — handing every read to post.
func (r *xeReader) run(ctx context.Context, post func(xeBatch)) error {
	interval := xePollFast
	for {
		b, more, err := r.read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		post(b)
		switch {
		case more:
			continue
		case !r.live:
			return nil
		case len(b.events) > 0:
			interval = xePollFast
		default:
			interval = min(xePollIdle, interval*2)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// errXENoTarget is a session with nothing this viewer can read.
var errXENoTarget = errors.New("the session has no event_file or ring_buffer target to read")

// read does one read, and reports whether more is waiting right now (a full
// event_file chunk).
func (r *xeReader) read(ctx context.Context) (xeBatch, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, xeReadTimeout)
	defer cancel()
	if r.files != "" {
		r.pattern, r.source = r.files, gosmo.XETargetEventFile
		return r.readMerged(ctx)
	}
	if r.source == "" {
		if err := r.resolve(ctx); err != nil {
			return xeBatch{}, false, err
		}
	}
	if r.source == gosmo.XETargetRingBuffer {
		return r.readRingBuffer(ctx)
	}
	return r.readEventFile(ctx)
}

// resolve reads the session and picks the target: the one asked for, or for
// Watch Live Data the event_file, failing that the ring_buffer.
func (r *xeReader) resolve(ctx context.Context) error {
	es, err := r.scope.byName(ctx, r.sc, r.session)
	if err != nil {
		return err
	}
	if r.live && !es.IsRunning {
		return fmt.Errorf("event session %q is not running — start it first", r.session)
	}
	source := r.target
	if source == "" {
		for _, t := range []string{gosmo.XETargetEventFile, gosmo.XETargetRingBuffer} {
			if _, ok := es.Target(t); ok {
				source = t
				break
			}
		}
	}
	switch source {
	case gosmo.XETargetEventFile:
		p, err := es.EventFilePattern()
		if err != nil {
			return err
		}
		r.pattern = p
	case gosmo.XETargetRingBuffer:
	case "":
		return errXENoTarget
	default:
		return fmt.Errorf("the %s target keeps aggregated data, not events — only event_file and ring_buffer can be viewed yet", source)
	}
	r.es, r.source = es, source
	return nil
}

// xeReadsOnlyByPattern reports whether sc refuses to read the local event
// file path by name. A Managed Instance reads a local .xel only through the
// wildcard of a session writing there — system_health*.xel, since a session
// anyone creates writes a blob — and answers a full path, a bare file name or
// any other wildcard with Msg 40538 ("a valid URL beginning with https://"),
// sysadmin or not (t-qmi-01, 2026-09-30). A cursor naming a file by its full
// path is accepted beside that wildcard, so only the first read is affected.
// A URL is read as given.
func xeReadsOnlyByPattern(sc *db.ServerConn, path string) bool {
	return serverIsAzure(sc) && !strings.Contains(path, "://")
}

// readEventFile reads the next chunk of the event_file.
//
// Watch Live Data starts at the file the session is writing now, not at the
// oldest file on disk: system_health keeps hundreds of megabytes, and a live
// view that first replays days of it is not live. The current file is read
// whole — the minutes of context before the view opened — and its cursor
// carries on into the rollover files after it. Where the file cannot be read
// by its path (xeReadsOnlyByPattern) the first read is the pattern's from the
// start instead — every file the session has left, which on a Managed
// Instance is system_health's one or few.
func (r *xeReader) readEventFile(ctx context.Context) (xeBatch, bool, error) {
	if !r.live && !r.planned {
		r.plan(ctx)
	}
	if r.backlog != nil {
		return r.readNewestFirst(ctx)
	}
	b := xeBatch{source: r.source}
	pattern := r.pattern
	if r.live && !r.positioned {
		st, err := r.es.Status(ctx)
		if err != nil {
			return b, false, err
		}
		for _, ts := range st.Targets {
			if ts.Name == gosmo.XETargetEventFile && ts.CurrentFile != "" && !xeReadsOnlyByPattern(r.sc, ts.CurrentFile) {
				pattern = ts.CurrentFile
			}
		}
	}
	evs, next, err := r.sc.Server.ReadEventFile(ctx, pattern, r.cursor, xeReadChunk)
	if r.live && errors.Is(err, gosmo.ErrEventFileGone) {
		// Rollover deleted the file the cursor was in before the reader got
		// to its end — a session writing faster than one read a poll. The
		// zero cursor with the wildcard is the oldest file still there;
		// what was in the deleted file's tail is lost, and the status line
		// says so.
		r.cursor, r.positioned = gosmo.EventFileCursor{}, true
		b.rolledOver = true
		return b, true, nil
	}
	if err != nil {
		return b, false, err
	}
	// Positioned once the current file has handed back a buffer: until then
	// the cursor is still zero, and a read with the wildcard from zero would
	// be the replay this avoids.
	if !next.IsZero() {
		r.positioned = true
	}
	r.cursor = next
	b.events = convertXEvents(evs)
	return b, len(evs) >= xeReadChunk, nil
}

// plan lists the event_file's files for View Target Data. With more than
// one, the target is read newest file first (readNewestFirst): the store keeps
// only the newest capacity events, and reading an older set oldest-first
// spent most of its time on events it then dropped (2016's system_health:
// 229 800 events read in 102 s to keep 100 000). A listing that fails or is
// empty — a login xp_dirtree answers with nothing on 2016, a blob URL — falls
// back to the pattern read from the start, as does a server that refuses to
// read a file by its path.
func (r *xeReader) plan(ctx context.Context) {
	r.planned = true
	if xeReadsOnlyByPattern(r.sc, r.pattern) {
		return
	}
	files, err := r.sc.Server.EventFiles(ctx, r.pattern)
	if err != nil || len(files) < 2 {
		return
	}
	r.backlog = files
}

// readNewestFirst reads the newest unread file whole, a chunk per call, and
// goes back a file at a time until the files read hold the store's capacity
// or none is left. Nothing is posted until then — the store appends, so the
// events have to reach it oldest first — beyond a progress line per chunk.
//
// Files are read by exact path, which reads that file alone; a count of
// events per file first would cost most of a full read (the server parses
// every buffer to count it), and a buffer offset cannot be guessed (the
// server refuses one that is not a buffer's, Msg 25722).
func (r *xeReader) readNewestFirst(ctx context.Context) (xeBatch, bool, error) {
	b := xeBatch{source: r.source}
	file := r.backlog[len(r.backlog)-1]
	evs, next, err := r.sc.Server.ReadEventFile(ctx, file, r.cursor, xeReadChunk)
	if err != nil {
		if r.stacked == 0 && len(r.fileEvents) == 0 {
			return b, false, err
		}
		// An older file gone under the read (rollover of a running session)
		// or unreadable: what is stacked is the newest part, which is what
		// the read is for.
		r.fileEvents = nil
		return r.flush(b), false, nil
	}
	r.fileEvents = append(r.fileEvents, convertXEvents(evs)...)
	if len(evs) >= xeReadChunk {
		r.cursor = next
		b.progress = r.progress()
		return b, true, nil
	}
	r.stack = append(r.stack, r.fileEvents)
	r.stacked += len(r.fileEvents)
	r.fileEvents, r.cursor = nil, gosmo.EventFileCursor{}
	r.backlog = r.backlog[:len(r.backlog)-1]
	if r.stacked < r.capacity && len(r.backlog) > 0 {
		b.progress = r.progress()
		return b, true, nil
	}
	return r.flush(b), false, nil
}

// progress is the newest-first read's status line.
func (r *xeReader) progress() string {
	return fmt.Sprintf("reading newest files first: %d read, %d events so far", len(r.stack), r.stacked+len(r.fileEvents))
}

// flush hands the stacked files' events over oldest first, with the count of
// older files left unread, and ends the newest-first read.
func (r *xeReader) flush(b xeBatch) xeBatch {
	b.events = make([]xevent.Event, 0, r.stacked)
	for i := len(r.stack) - 1; i >= 0; i-- {
		b.events = append(b.events, r.stack[i]...)
	}
	// The file a failed read stopped on is still in backlog: it was not
	// read either.
	b.skipped = len(r.backlog)
	r.stack, r.stacked, r.backlog = nil, 0, nil
	return b
}

// readRingBuffer reads the whole ring_buffer and keeps the events not held
// from the last read. Events the target processed beyond those it handed back
// aged out of the buffer between the two reads — Risk 3 — and are counted.
func (r *xeReader) readRingBuffer(ctx context.Context) (xeBatch, bool, error) {
	b := xeBatch{source: r.source}
	data, err := r.es.ReadRingBuffer(ctx)
	if errors.Is(err, gosmo.ErrNotFound) {
		return b, false, fmt.Errorf("event session %q is not running — its ring_buffer went with it", r.session)
	}
	if err != nil {
		return b, false, err
	}
	fresh := r.dedupe.New(convertXEvents(data.Events))
	if r.primed {
		if gap := data.TotalEventsProcessed - r.processed - int64(len(fresh)); gap > 0 {
			b.missing = gap
		}
	}
	b.truncated = data.Truncated
	r.processed, r.primed = data.TotalEventsProcessed, true
	b.events = fresh
	b.unsequenced = r.dedupe.Unsequenced
	return b, false, nil
}

// convertXEvents turns gosmo's events into the store's.
func convertXEvents(evs []gosmo.XEvent) []xevent.Event {
	out := make([]xevent.Event, len(evs))
	for i, e := range evs {
		out[i] = xevent.Event{
			Name: e.Name, Package: e.Package, Timestamp: e.Timestamp, Seq: e.Seq,
			Fields: convertXEValues(e.Fields), Actions: convertXEValues(e.Actions),
		}
	}
	return out
}

func convertXEValues(vs []gosmo.XEValue) []xevent.Value {
	out := make([]xevent.Value, len(vs))
	for i, v := range vs {
		out[i] = xevent.Value{Name: v.Name, Value: v.Value, Text: v.Text, IsXML: v.IsXML}
	}
	return out
}

// offerTarget is Watch Live Data on a session with no event_file or
// ring_buffer target (D1): the offer to add one and start watching. An
// event_file named after the session, relative so it lands in the error-log
// directory as SSMS's New Session puts one — the service account owns that
// directory (Risk 4); a ring_buffer on a Managed Instance, which refuses a
// local file path. Not offered to a login that could not add it: the status
// line already says what is missing.
func (v *XEventViewer) offerTarget() {
	a := v.app
	if !a.isConnected(v.host) || !gate.Allows(v.host, v.scope.db, v.scope.right(gate.EventSessionAddTarget)) {
		return
	}
	t, what := gosmo.EventFileTarget(v.session, 0, 0), "an event_file target ("+v.session+"*.xel in the error-log directory)"
	if v.host.Server.Info().IsAzure() {
		t, what = gosmo.RingBufferTarget(0), "a ring_buffer target"
	}
	a.confirmDialog.ShowConfirm("Add Target",
		fmt.Sprintf("%s has no event_file or ring_buffer target, so there is nothing to watch. Add %s to it and start watching?", v.session, what),
		func(yes bool) {
			if !yes || !a.panelHosted(v) {
				return
			}
			sc := v.host
			a.runWithProgress(progressJob{
				title:   "Add Target",
				message: fmt.Sprintf("Adding %s to %q...", t.QualifiedName(), v.session),
				what:    "adding an event session target",
				sc:      sc,
			}, func(ctx context.Context, _ progressReport) error {
				return v.scope.ref(sc, v.session).AddTarget(ctx, t)
			}, func(err error, cancelled bool) {
				a.refreshXESessions(sc, v.scope)
				switch {
				case cancelled:
					a.setStatus("Add Target cancelled")
				case err != nil:
					a.setStatus(fmt.Sprintf("Failed to add a target to %q: %v", v.session, withPermissionAdvice(err)))
				case a.panelHosted(v):
					v.feed.reader, v.feed.err = nil, nil
					v.startFeed()
				}
			})
		})
}
