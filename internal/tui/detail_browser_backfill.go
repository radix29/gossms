package tui

import (
	"context"
	"sync"

	dbconn "github.com/radix29/gossms/internal/db"
)

// backfillRows runs fetch for each of n rows across a bounded pool of
// goroutines and returns once every one of them has queued its cell writes on
// the UI goroutine. It is the shared body of the progressive detail loaders
// (loadDatabasesFolderDetails, loadTablesFolderDetails): show the fast
// identity columns immediately, then fill the slow per-row ones as they land,
// so one slow row doesn't hold up the rest.
//
// fetch runs on a background goroutine with its own per-row context and must
// not touch DetailBrowser or any other UI state. It returns the closure that
// writes that row's cells, which backfillRows posts — so every write to the
// caller's rows slice lands on the UI goroutine, the same one Draw runs on,
// rather than racing another row's completion.
//
// markFailed fills the row's backfilled cells with placeholders for a row
// that produced nothing. The caller uses it for an ordinary fetch error too;
// backfillRows calls it only for a panic (see below).
//
// The write a fetch queues is unconditional even when the user has since
// selected a different node: rows belongs to this fetch alone, and the
// caller caches it once backfillRows returns. Skipping the write for a stale
// seq would cache a row still showing its "…" placeholder — permanently,
// since reselecting is then a cache hit that never refetches. Only the
// redraw is conditional.
//
// backfillRows returning means every row's closure has been *queued*, not
// that it has run, so a caller that caches rows afterwards depends on
// App.postEvent's queue being FIFO: every row's closure is appended before
// any worker's wg.Done, so a cacheOnly posted after this returns is
// necessarily appended last and drains last.
//
// The pool is a fixed maxRowFetchConcurrency workers pulling row indices off a
// channel, rather than n goroutines each waiting on a token: both bound the
// work, but the token form parks hundreds of idle goroutines on a folder with
// hundreds of entries.
func (db *DetailBrowser) backfillRows(
	app *App,
	sc *dbconn.ServerConn,
	seq, n int,
	what string,
	fetch func(ctx context.Context, i int) func(),
	markFailed func(i int),
) {
	if n <= 0 {
		return
	}
	// The queue is filled and closed before a worker exists, so no send can
	// ever block on one. Handing the indices out from this goroutine instead
	// would make it depend on a worker still being alive to receive them: if
	// a panic escaped backfillRow and took every worker with it, the send
	// would block forever and hang the loader — a whole folder stuck, rather
	// than the rows that worker owed. n ints is a few KB at the largest
	// folder sizes this runs on.
	rowIdx := make(chan int, n)
	for i := range n {
		rowIdx <- i
	}
	close(rowIdx)

	var wg sync.WaitGroup
	for range min(n, maxRowFetchConcurrency) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// backfillRow recovers a panicking fetch itself; this is the belt
			// under it. A panic that escapes it anyway is on a background
			// goroutine, where nothing else can catch it — the process dies
			// and Run's screen.Fini() never restores the terminal. Every other
			// goroutine in this package gets that cover from App.safego; this
			// one is spawned directly, so it has to ask for it.
			defer app.recoverPanic(what)
			for i := range rowIdx {
				db.backfillRow(app, sc, seq, i, what, fetch, markFailed)
			}
		}()
	}
	wg.Wait()
}

// backfillRow fetches one row and queues its write. Its recovery is what
// keeps a panicking row from taking its worker — and the n-i rows that worker
// still owes — down with it, and it has to queue markFailed while wg.Wait is
// still blocked: the caller caches rows the moment Wait returns, and a row
// whose fetch died is otherwise cached still showing its "…" placeholder,
// permanently, since reselecting the node is a cache hit that never
// refetches. Covering the panic at all is this goroutine's own job; a recover
// on the loader goroutine that spawned it never sees a panic here.
func (db *DetailBrowser) backfillRow(
	app *App,
	sc *dbconn.ServerConn,
	seq, i int,
	what string,
	fetch func(ctx context.Context, i int) func(),
	markFailed func(i int),
) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		app.postAndWake(func() { markFailed(i) })
		app.reportPanic(r, what)
	}()

	ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
	defer cancel()

	apply := fetch(ctx, i)
	app.postAndWake(func() {
		apply()
		if seq == db.seq {
			db.grid.RefreshColumnWidths()
		}
	})
}
