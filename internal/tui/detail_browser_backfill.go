package tui

import (
	"context"
)

// backfillRows runs fetch for each of n rows on App.fanOut's bounded pool and
// returns once every row has queued its cell writes on the UI goroutine. It is the
// shared body of the progressive detail loaders (loadDatabasesFolderDetails,
// loadTablesFolderDetails): show the fast identity columns at once, then fill the
// slow per-row ones as they land, so one slow row doesn't hold up the rest.
//
// fetch runs on a background goroutine with its own per-row context and must not
// touch DetailBrowser or other UI state. It returns the closure that writes that
// row's cells; backfillRows posts it, so every write to the caller's rows slice
// lands on the UI goroutine.
//
// markFailed fills a row's backfilled cells with placeholders. backfillRows queues
// it only for a panic (fanOut's onPanic, which runs before fanOut returns, so the
// caller's cache sees the placeholder rather than "…"); the caller uses it for
// ordinary fetch errors.
//
// The queued write is unconditional even when seq has moved on: rows belongs to
// this fetch alone, and whether cacheOnlyObjects caches it is decided by pending,
// not seq. Skipping a stale write would cache a row still showing "…" for good,
// since reselecting is then a cache hit that never refetches. Only the redraw is
// conditional.
//
// Returning means the closures are *queued*, not run; a caller that caches rows
// afterwards relies on App.postEvent's queue being FIFO, so a cacheOnlyObjects
// posted after this returns drains last.
//
// Each row's timeout derives from fetchCtx (see DetailBrowser.fetch). Once it is
// cancelled the rows not yet started are skipped: nothing this fetch still
// produces is shown or cached (see cancelInflight).
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
			if db.run.Current(seq) {
				db.grid.RefreshColumnWidths()
			}
		})
	}, func(i int) {
		app.postAndWake(func() { markFailed(i) })
	})
}
