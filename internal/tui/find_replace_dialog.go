package tui

import (
	"fmt"

	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/dialogs"
)

// FindReplaceDialog is the query editor's Find and Replace dialog: tuikit's
// dialogs.FindReplaceDialog (both modes, the options, the status line, the
// rescope and no-recompile rules) aimed at the active query panel's editor.
// What stays here is goSSMS's routing: which editor a Find targets, the
// Extended Events viewer's own Find, and the F3/Shift+F3/Ctrl+F3 entry points
// that run with the dialog closed.
type FindReplaceDialog struct {
	*dialogs.FindReplaceDialog
	app *App
}

// NewFindReplaceDialog builds the dialog. Fields and options persist across
// showings, so reopening offers the previous search back.
func NewFindReplaceDialog(app *App) *FindReplaceDialog {
	return &FindReplaceDialog{FindReplaceDialog: dialogs.NewFindReplaceDialog(), app: app}
}

// ShowFind opens the dialog in Find mode against the active query panel's
// editor. ShowReplace opens it with the Replace field and buttons shown.
func (d *FindReplaceDialog) ShowFind()    { d.show(false) }
func (d *FindReplaceDialog) ShowReplace() { d.show(true) }

func (d *FindReplaceDialog) show(replace bool) {
	// An Extended Events viewer finds events, with a Find of its own
	// (xevent_viewer_find.go); it has nothing to replace.
	if v := d.app.activeXEventViewer(); v != nil && !replace {
		v.showFind()
		return
	}
	qp := d.app.activeQueryPanel()
	if qp == nil {
		d.app.setStatus(noActiveQueryPanelMessage)
		return
	}
	if replace {
		d.FindReplaceDialog.ShowReplace(qp.editor)
	} else {
		d.FindReplaceDialog.ShowFind(qp.editor)
	}
}

// findNextInEditor repeats the active search in the active query panel's editor
// with the dialog closed: F3 (dir 1) and Shift+F3 (dir -1). With no search set
// it opens the dialog.
func (a *App) findNextInEditor(dir int) {
	if v := a.activeXEventViewer(); v != nil {
		v.findNext(dir)
		return
	}
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	if !qp.editor.HasSearch() {
		a.findDialog().ShowFind()
		return
	}
	if !qp.editor.FindNext(dir) {
		a.setStatus(fmt.Sprintf(dialogs.Strings.NoMatches, qp.editor.SearchOpts().Query))
		return
	}
	a.setStatus(dialogs.MatchStatus(qp.editor))
}

// findWordAtCursor is Ctrl+F3: search for the identifier the caret is on,
// without opening the dialog.
func (a *App) findWordAtCursor() {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	word := qp.editor.WordAtCursor()
	if word == "" {
		a.setStatus("No word at the cursor")
		return
	}
	// Whole-word, so Ctrl+F3 on "id" doesn't stop inside every "identity". Replace
	// comes from the dialog: it doesn't affect matching, and clearing it would
	// discard typed text. No error is possible; the term is literal and escaped
	// before compiling.
	opts := editor.SearchOptions{
		Query:     word,
		Replace:   a.findDialog().Fields().Replace,
		WholeWord: true,
	}
	_ = qp.editor.SetSearch(opts)
	a.findDialog().AdoptSearch(opts)
	// The word under the caret is the first match, not the next occurrence after
	// it (FindNext would skip it: it searches from the caret).
	if !qp.editor.FindAtCursor() {
		a.setStatus(fmt.Sprintf(dialogs.Strings.NoMatches, word))
		return
	}
	a.setStatus(dialogs.MatchStatus(qp.editor))
}

// hasEditorSearch reports whether Find Next/Previous have a search to repeat —
// the Enabled gate on both menu items.
func (a *App) hasEditorSearch() bool {
	if v := a.activeXEventViewer(); v != nil {
		return v.find != ""
	}
	qp := a.activeQueryPanel()
	return qp != nil && qp.editor.HasSearch()
}
