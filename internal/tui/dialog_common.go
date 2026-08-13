package tui

// dialog_common.go holds the small behaviours shared by the hand-rolled
// dialogs — the ones that lay out widgets directly and drive focus with a flat
// slice, rather than delegating to propsheet.Form's rows. Connect, Backup,
// Restore, Tasks and Query List are all of that kind.
//
// Each dialog keeps its own state (`focusable`, `focusIdx`, `sel`, `scroll`,
// `task`); only the operations over that state live here. That is deliberate:
// the alternative, embedded structs owning the fields, renames roughly forty
// direct `d.focusable[d.focusIdx]` accesses across five files for no
// behavioural gain.

// focusable is satisfied by any tuikit widget that supports keyboard focus.
type focusable interface {
	Focus(bool)
}

// setFocusIn focuses list[i], blurring every other entry, and reports the
// index that ended up focused. An out-of-range i blurs everything and answers
// cur — leaving the caller's index where it was, which is what the three
// dialogs' own setFocus did before this was lifted out of them.
func setFocusIn(list []focusable, i, cur int) int {
	for _, f := range list {
		f.Focus(false)
	}
	if i >= 0 && i < len(list) {
		list[i].Focus(true)
		return i
	}
	return cur
}

// indexOfFocusable returns w's position in list, or -1. Identity comparison,
// not equality: the callers hold the very pointers they put in the list.
func indexOfFocusable(list []focusable, w focusable) int {
	for i, f := range list {
		if f == w {
			return i
		}
	}
	return -1
}

// nextFocus and prevFocus are Tab and Backtab over a list of n widgets.
// n must be positive; every caller builds its list statically and none can be
// empty, and an empty one divides by zero here exactly as it did at the seven
// inline sites these replace.
func nextFocus(idx, n int) int { return (idx + 1) % n }

func prevFocus(idx, n int) int { return (idx - 1 + n) % n }

// scrollToShow returns the scroll offset that brings row sel into a viewport
// dataH rows tall, moving by the least that does it — up when sel is above the
// top, down when it is past the bottom, unchanged when it is already in view.
func scrollToShow(sel, scroll, dataH int) int {
	if sel < scroll {
		return sel
	}
	if sel >= scroll+dataH {
		return sel - dataH + 1
	}
	return scroll
}

// runProgressButton is the Close/Cancel button pair on the Backup and Restore
// progress views. A finished or absent task leaves Close as the only outcome
// whichever button has focus, so a run that completed between the draw and the
// keypress cannot cancel anything.
func runProgressButton(task *Task, btnFocus int, hide func()) {
	if task == nil || task.Done {
		hide()
		return
	}
	switch btnFocus {
	case 0:
		hide()
	case 1:
		task.Cancel()
	}
}

// confirmDiscardChanges asks before throwing away a property page's unsaved
// edits, running proceed only if the user agrees. Shared by PropDialog and
// newObjectDialog, which pass it to propsheet as the same callback.
func (a *App) confirmDiscardChanges(proceed func()) {
	a.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}
