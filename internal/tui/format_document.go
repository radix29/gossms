package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/radix29/gossms/internal/format"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqledit"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/controls/editor"
)

// canFormatActiveDocument gates Edit > Format Document (and Alt+F): a query
// panel whose editor is writable, whose language has a formatter, and with no
// block selection — a column span has no lines to rewrite.
func (a *App) canFormatActiveDocument() bool {
	qp := a.activeQueryPanel()
	return qp != nil && !qp.editor.ReadOnly() && !qp.editor.HasBlockSelection() &&
		len(format.Styles(qp.lang)) > 0
}

// formatSyncBytes is the largest text Format Document formats on the UI
// goroutine. SQL, the slowest formatter, takes about 55 ms on 256 KB and 230
// ms on 1 MB (i5-2500K); a 5 MB script froze every key and redraw for 1.7 s.
// Below this a "Formatting..." flash would cost more than it saves.
var formatSyncBytes = 256 << 10

// formatText is format.Format; a variable so a test can make it slow.
var formatText = format.Format

// formatActiveDocument is Alt+Shift+F (and Edit > Format Document for a
// language with one style): it lays out the active query panel's text in its
// language's default style (formatStyleID), as one undo step. With a
// selection it formats the whole lines the selection touches, re-indented at
// the first line's indent; otherwise the whole document. A parse failure or a
// guard refusal leaves the text untouched and says why on the status bar.
//
// A selection is guarded twice: by Format on its own, and in the whole
// document (formatJob.verifyInDocument). Lines inside a multi-line literal,
// comment or CDATA section lex as code when cut out, so the first check alone
// let their text be re-laid-out and upper-cased — a silent change to the value.
//
// A document over formatSyncBytes is formatted off the UI goroutine. Its result
// lands only if the panel's formatRun is still current (a repeat supersedes
// it, closing the panel abandons it) and the text and selection are as they
// were when it started; otherwise the typing done meanwhile wins. A superseded
// or abandoned run stops where it is (internal/format checks the context every
// line), and so does one whose text changed (abandonStaleFormat): its result
// could no longer land, and a 5 MB script is seconds of a core.
func (a *App) formatActiveDocument() {
	if qp := a.activeQueryPanel(); qp != nil {
		a.formatActiveDocumentAs(a.formatStyleID(qp.lang))
	}
}

// formatActiveDocumentAs is formatActiveDocument in the style styleID names
// (an empty or stale ID is the language's first style) — a pick from Edit >
// Format Document's cascade, which formats once in that style and leaves the
// configured default alone.
func (a *App) formatActiveDocumentAs(styleID string) {
	qp := a.activeQueryPanel()
	if qp == nil {
		return
	}
	e := qp.editor
	st, ok := format.StyleByID(qp.lang, styleID)
	switch {
	case e.ReadOnly():
		a.setStatus("Format Document: the editor is read-only")
		return
	case !ok:
		a.setStatus(fmt.Sprintf("Format Document: no formatter for %s", qp.lang))
		return
	case e.HasBlockSelection():
		a.setStatus("Format Document: not available for a block selection")
		return
	}
	j := formatJob{qp: qp, st: st, lang: qp.lang, doc: e.Document(), ver: e.Document().Version(),
		sel: e.HasSelection()}
	o := format.Options{IndentWidth: e.IndentWidth()}
	if j.sel {
		j.top, j.bottom, _ = e.SelectedLineRange() // !ok only for a block selection, refused above
		// Refused here for SQL with a clearer message than verifyInDocument's,
		// which still covers XML and anything this misses.
		if j.lang == format.LangSQL && sqledit.LineState(e.Document(), j.top).Mode != sqltext.ModeNormal {
			a.setStatus("Format failed: the selection starts inside a string, comment or quoted identifier")
			return
		}
		j.src, _ = e.SelectedLinesText()
		j.full = e.Text()
		// The formatter indents the lines it lays out, not those inside a
		// multi-line literal or text value, before its guard checks the result.
		o.LineIndent = leadingIndent(j.src)
	} else {
		j.src = e.Text()
		j.full = j.src
	}

	// Sized by the whole document, not the selection: verifyInDocument lexes
	// all of it.
	if len(j.full) <= formatSyncBytes {
		qp.formatRun.Abandon() // this one is newer than any still running
		qp.formatJob = nil
		out, err := j.format(context.Background(), o)
		a.applyFormatted(j, out, err)
		return
	}
	ctx, token := qp.formatRun.Begin(context.Background())
	qp.formatJob = &j
	a.setStatus(fmt.Sprintf("Formatting as %s...", j.lang))
	repair := func() {
		if qp.formatRun.Done(token) {
			qp.formatJob = nil
		}
	}
	a.safegoRepair("Format Document", repair, func() {
		out, err := j.format(ctx, o)
		a.postAndWake(func() {
			if !qp.formatRun.Done(token) {
				return // superseded or abandoned: a cancel's error included
			}
			qp.formatJob = nil
			if !j.current() {
				a.setStatus("Format Document: the text changed while formatting; not applied")
				return
			}
			a.applyFormatted(j, out, err)
		})
	})
}

// abandonStaleFormat stops the active panel's background Format Document once
// an edit (or a moved selection) means its result could not land: without it
// the run went on to the end only for the result to be dropped. Run after
// every event (handleEvent), so typing during a large format stops it at
// once; it costs nothing while no format is in flight.
func (a *App) abandonStaleFormat() {
	qp := a.activeQueryPanel()
	if qp == nil || qp.formatJob == nil {
		return
	}
	if qp.formatRun.Idle() {
		qp.formatJob = nil // finished, or abandoned elsewhere
		return
	}
	if qp.formatJob.current() {
		return
	}
	qp.formatRun.Abandon()
	qp.formatJob = nil
	a.setStatus("Format Document: the text changed while formatting; stopped")
}

// formatStyleID is the style Format Document uses for l unless told another:
// Tools > Options' SQL Format Style for SQL, the first registered style for the
// rest. Empty or stale IDs resolve in format.StyleByID.
func (a *App) formatStyleID(l format.Language) string {
	if l == format.LangSQL {
		return a.cfg.SQLFormatStyle
	}
	return ""
}

// formatDocumentMenuItem is Edit > Format Document. For a language with more
// than one style it is a cascade listing them, the default (the one
// Alt+Shift+F uses) marked "(default)" and carrying the shortcut; otherwise —
// one style, no formatter, no query panel — a plain item running the default.
// The Edit menu is rebuilt each time it opens, so the cascade follows the
// active panel's language.
func (a *App) formatDocumentMenuItem() controls.MenuItem {
	const label, shortcut = "Format Document", "Alt+Shift+F"
	item := controls.MenuItem{Label: label, Shortcut: shortcut,
		Action: func() { a.formatActiveDocument() }, Enabled: a.canFormatActiveDocument}
	qp := a.activeQueryPanel()
	if qp == nil {
		return item
	}
	all := format.Styles(qp.lang)
	if len(all) < 2 {
		return item
	}
	def, _ := format.StyleByID(qp.lang, a.formatStyleID(qp.lang))
	for _, st := range all {
		sub := controls.MenuItem{Label: st.Name, Action: func() { a.formatActiveDocumentAs(st.ID) }}
		if st.ID == def.ID {
			sub.Label += " (default)"
			sub.Shortcut = shortcut
		}
		item.Sub = append(item.Sub, sub)
	}
	item.Shortcut, item.Action = "", nil
	return item
}

// formatJob is what one Format Document run read from its panel, enough to
// apply its result and to tell whether the editor still holds that text.
type formatJob struct {
	qp   *QueryPanel
	st   format.Style
	lang format.Language
	src  string // the text formatted: the whole document or the selected lines
	full string // the whole document
	doc  *editor.Document
	ver  uint64
	sel  bool
	// The selection's rows, inclusive (Editor.SelectedLineRange): the lines
	// j.src holds. Zero without a selection.
	top, bottom int
}

// format formats j.src and, for a selection, checks the result in the whole
// document.
func (j formatJob) format(ctx context.Context, o format.Options) (string, error) {
	out, err := formatText(ctx, j.lang, j.st.ID, j.src, o)
	if err == nil && j.sel {
		err = j.verifyInDocument(ctx, out)
	}
	return out, err
}

// verifyInDocument runs the formatter's guard on the whole document with the
// selected lines replaced by out. It refuses what the selection's own guard
// cannot see: a selection starting or ending inside a string, comment or CDATA
// section, whose lines lex as code once cut out of it.
func (j formatJob) verifyInDocument(ctx context.Context, out string) error {
	start := 0 // byte offset of line j.top in j.full
	for range j.top {
		start += strings.IndexByte(j.full[start:], '\n') + 1
	}
	after := j.full[:start] + out + j.full[start+len(j.src):]
	if err := format.Verify(ctx, j.lang, j.full, after); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return errors.New("the selection starts or ends inside a string, comment or CDATA section")
	}
	return nil
}

// current reports whether j's editor still holds the text j formatted, at the
// same place and selected the same way, so applying the result replaces
// exactly what was formatted. The document version catches any edit; a moved
// selection leaves it alone, so its row range is compared too. With the
// document and version unchanged the same rows hold the same text, so the
// selected text is never rebuilt: abandonStaleFormat runs this after every
// event, and SelectedLinesText on a multi-megabyte selection was megabytes of
// garbage per mouse move on the UI goroutine.
func (j formatJob) current() bool {
	e := j.qp.editor
	if e.ReadOnly() || j.qp.lang != j.lang || e.Document() != j.doc || e.Document().Version() != j.ver ||
		e.HasSelection() != j.sel {
		return false
	}
	if !j.sel {
		return true
	}
	top, bottom, ok := e.SelectedLineRange()
	return ok && top == j.top && bottom == j.bottom
}

// applyFormatted puts a run's result into its editor, or reports its error.
func (a *App) applyFormatted(j formatJob, out string, err error) {
	e := j.qp.editor
	if err != nil {
		// Positions count from the selection's first line; report document lines.
		if se, ok := errors.AsType[*format.SyntaxError](err); ok && j.sel {
			what := fmt.Sprintf("is not a complete %s value", se.Lang)
			if se.Lang == format.LangSQL {
				what = "does not lex as SQL" // any run of statements is a whole SQL "value"
			}
			a.setStatus(fmt.Sprintf("Format failed: selection %s: line %d col %d: %s",
				what, se.Line+j.top, se.Col, se.Msg))
			return
		}
		a.setStatus("Format failed: " + err.Error())
		return
	}
	if j.sel {
		a.reportFormatted(j.lang, j.st, e.ReplaceSelectedLines(out))
		return
	}
	a.reportFormatted(j.lang, j.st, e.ReplaceText(out))
}

// reportFormatted is the status line after a successful format; changed is
// false when the text was already in the style (no undo step was pushed).
func (a *App) reportFormatted(l format.Language, st format.Style, changed bool) {
	if !changed {
		a.setStatus(fmt.Sprintf("Already formatted as %s (%s)", l, st.Name))
		return
	}
	a.setStatus(fmt.Sprintf("Formatted as %s (%s)", l, st.Name))
}

// leadingIndent is the run of spaces and tabs s starts with.
func leadingIndent(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
