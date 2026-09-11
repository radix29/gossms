package tui

import (
	"context"
)

// backfillRows runs fetch for each of n rows on App.fanOut's bounded pool and
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
// backfillRows queues it only for a panic, as fanOut's onPanic — which runs
// before fanOut returns, so the caller's cache sees the placeholder, not "…".
//
// The write a fetch queues is unconditional even when seq has moved on:
// rows belongs to this fetch alone, and whether the caller's cacheOnly caches
// it is decided by pending, not seq. Skipping the write for a stale seq would
// cache a row still showing its "…" placeholder whenever pending still
// matched — permanently, since reselecting is then a cache hit that never
// refetches. Only the redraw is conditional.
//
// backfillRows returning means every row's closure has been *queued*, not
// that it has run, so a caller that caches rows afterwards depends on
// App.postEvent's queue being FIFO: every row's closure is appended before
// fanOut returns, so a cacheOnly posted after this returns is necessarily
// appended last and drains last.
//
// fetchCtx is the loader's fetch context (see DetailBrowser.fetch), and each
// row's timeout is derived from it. Once it is cancelled — the user has moved
// on — the rows not yet started are skipped outright rather than each started
// only to fail: nothing this fetch still produces is shown or cached (see
// cancelInflight), so a skipped row's placeholder is never seen.
func (db *DetailBrowser) backfillRows(
	app *App,
	fetchCtx context.Context,
	seq, n int,
	what string,
	fetch func(ctx context.Context, i int) func(),
	markFailed func(i int),
) {
	app.fanOut(n, what, func(i int) {
		if fetchCtx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(fetchCtx, childFetchTimeout)
		defer cancel()

		apply := fetch(ctx, i)
		app.postAndWake(func() {
			apply()
			if seq == db.seq {
				db.grid.RefreshColumnWidths()
			}
		})
	}, func(i int) {
		app.postAndWake(func() { markFailed(i) })
	})
}
