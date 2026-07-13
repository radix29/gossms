// Package propsheet provides a reusable, multi-page, editable "properties
// dialog" — a modal with a page list on the left and a scrollable form on
// the right (OK/Cancel/Apply/Script Changes below), the pattern SQL Server
// Management Studio uses for Server/Database/Login Properties.
//
// propsheet is presentation only. It never runs a query, never spawns a
// goroutine, and never imports internal/tui or gosmo — like every other
// tuikit package it communicates outward exclusively through callbacks
// (OnLoadPage, OnApply, OnOK, OnScript, OnClose, ConfirmDiscard) and
// expects its caller to drive the async parts:
//
//   - When a page needs data, PropertySheet calls OnLoadPage(page, seq) and
//     marks that page's state Loading. The caller is expected to fetch the
//     data — typically on a background goroutine — and report the result by
//     calling SetPageForm(page, seq, form) or SetPageError(page, seq, err).
//   - seq is a per-page monotonic counter. If the page has since been
//     refreshed (seq bumped again) or the sheet has been hidden, a call
//     with a stale seq is silently ignored — this is what makes a slow,
//     superseded fetch harmless. SetPageForm/SetPageError must be called
//     from the same goroutine that calls Draw/HandleKey/HandleMouse (the UI
//     goroutine); PropertySheet does no locking of its own.
//
// A page's Form (see form.go) holds the actual editable rows; dirty state
// is tracked per row and rolled up per page and for the whole sheet. Apply
// semantics — which rows map to which backend calls — are entirely up to
// the caller; propsheet only tracks and reports what changed.
//
// Script Changes (OnScript) is not a distinct code path: propsheet expects
// the caller to reuse the exact same per-page apply logic it uses for
// OnApply/OnOK, just invoked under gosmo.WithScript so every write it
// makes is captured as SQL text instead of executed — see
// internal/tui/prop_dialog.go's runScript for the app-layer half of this.
package propsheet
