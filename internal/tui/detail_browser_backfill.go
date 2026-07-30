package tui

import (
	"context"
	"sync"

	dbconn "github.com/radix29/gossms/internal/db"
)

// backfillRows runs fetch for each of n rows on its own bounded goroutine and
// returns once every one of them has queued its cell writes on the UI
// goroutine. It is the shared body of the progressive detail loaders
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
// App.postEvent's queue being FIFO: each row's closure is appended before
// its own wg.Done, so a cacheOnly posted after this returns is necessarily
// appended last and drains last.
func (db *DetailBrowser) backfillRows(
	app *App,
	sc *dbconn.ServerConn,
	seq, n int,
	what string,
	fetch func(ctx context.Context, i int) func(),
	markFailed func(i int),
) {
	var wg sync.WaitGroup
	sem := rowFetchSemaphore()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			// Registered before the recovery below so it runs after it. A
			// panicking row has to get markFailed queued while wg.Wait is
			// still blocked: the caller caches rows the moment Wait returns,
			// and a row whose fetch died is otherwise cached still showing
			// its "…" placeholder — permanently, since reselecting the node
			// is a cache hit that never refetches. Covering the panic at all
			// is also this goroutine's own job; a recover on the loader
			// goroutine that spawned it never sees a panic here.
			defer wg.Done()
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				app.postAndWake(func() { markFailed(i) })
				app.reportPanic(r, what)
			}()

			sem <- struct{}{}
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()

			apply := fetch(ctx, i)
			app.postAndWake(func() {
				apply()
				if seq == db.seq {
					db.grid.RefreshColumnWidths()
				}
			})
		}(i)
	}
	wg.Wait()
}
