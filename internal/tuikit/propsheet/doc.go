// Package propsheet provides a reusable, multi-page, editable "properties
// dialog": a modal with a page list on the left and a scrollable form on the
// right (OK/Cancel/Apply/Script Changes below), the pattern SSMS uses for
// Server/Database/Login Properties.
//
// propsheet is presentation only. It never runs a query, spawns a goroutine, or
// imports internal/tui or gosmo; like every tuikit package it communicates
// outward only through callbacks (OnLoadPage, OnApply, OnOK, OnScript, OnClose,
// ConfirmDiscard) and expects its caller to drive the async parts:
//
//   - When a page needs data, PropertySheet calls OnLoadPage(page, seq) and
//     marks the page Loading. The caller fetches the data, typically on a
//     background goroutine, and reports via SetPageForm(page, seq, form) or
//     SetPageError(page, seq, err).
//   - seq is a sheet-wide monotonic counter that SetPages never resets. A call
//     with a stale seq (page refreshed, sheet hidden, or reopened with a new
//     page set) is silently ignored, which makes a slow, superseded fetch
//     harmless. SetPageForm reports whether it accepted the form.
//     SetPageForm/SetPageError must be called from the UI goroutine that calls
//     Draw/HandleKey/HandleMouse; PropertySheet does no locking.
//
// A page's Form (see form.go) holds the editable rows; dirty state is tracked
// per row and rolled up per page and for the sheet. Apply semantics (which rows
// map to which backend calls) are entirely the caller's; propsheet only reports
// what changed. A form whose editor fields stand in for one selected item of a
// list registers a commit hook (Form.SetCommit), and the caller runs
// PropertySheet.Commit on the UI goroutine before reading Dirty or Validate for
// an Apply, so the apply, usually on another goroutine, never touches a widget.
//
// Script Changes (OnScript) is not a distinct code path: the caller reuses the
// same per-page apply logic as OnApply/OnOK, invoked under gosmo.WithScript so
// every write is captured as SQL text instead of executed (see
// internal/tui/prop_apply.go's runScript for the app-layer half).
package propsheet
