package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/widgets"
)

func fullTextStoplistPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextStoplist, error) {
		return findFullTextStoplist(ctx, sc, dbName, name)
	}
	return []propPage{
		{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				l, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				return fullTextListGeneralForm("Stoplist", l.Name, l.Owner, l.CreateDate, l.ModifyDate,
					"Its words are edited on the Stopwords page."), nil, nil
			},
		},
		withRequiresOn(pageFullTextStopwords(sc, dbName, find), dbName, "", name, gate.AlterOnFullTextStoplist),
	}
}

var stopwordHeaders = []string{"Stopword", "Language", "LCID"}

func stopwordRows(words []gosmo.FullTextStopword) [][]string {
	rows := make([][]string, len(words))
	for k, w := range words {
		rows[k] = []string{w.Word, w.Language, strconv.Itoa(w.LanguageID)}
	}
	return rows
}

// stopwordEdit is one row of the Stopwords page: a word in one language,
// loaded or added.
type stopwordEdit struct {
	word gosmo.FullTextStopword
	pendingState
}

// sameStopword reports whether two words are one entry of a stoplist: the
// same language and the same word, compared as the server does — case is
// not part of a stopword (Msg 30033 for "The" beside "the").
func sameStopword(a, b gosmo.FullTextStopword) bool {
	return a.LanguageID == b.LanguageID && strings.EqualFold(a.Word, b.Word)
}

// pageFullTextStopwords is the stoplist's words, master-detail over pendingEdits:
// a word and language to Add, Remove for the selected word, and Remove Language
// for every word of the selected word's language.
//
// Apply drops before it adds (a removed word can be added back in the same
// Apply), and sends the narrowest statement that does the whole job: DROP ALL when
// every loaded word goes, DROP ALL LANGUAGE when every word of a language does —
// a copy of the system stoplist holds thousands of words, which one DROP each
// would take minutes to send.
func pageFullTextStopwords(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.FullTextStoplist, error)) propPage {
	return propPage{
		title: "Stopwords",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			words, err := l.Stopwords(ctx)
			if err != nil {
				return nil, nil, err
			}
			langs, err := readFullTextLanguages(ctx, sc)
			if err != nil {
				return nil, nil, err
			}
			loaded := make([]*stopwordEdit, len(words))
			for k, w := range words {
				loaded[k] = &stopwordEdit{word: w}
			}
			edits := newPendingEdits(databaseCollation(l.Database()), loaded, nil, nil, nil)
			rowsFor := func() [][]string {
				vis := edits.visible()
				out := make([]gosmo.FullTextStopword, len(vis))
				for k, e := range vis {
					out[k] = e.word
				}
				return stopwordRows(out)
			}
			grid := controls.NewDataGrid()
			grid.SetData(stopwordHeaders, rowsFor())
			selected := func() *stopwordEdit {
				vis := edits.visible()
				if i := grid.SelectedRow(); i >= 0 && i < len(vis) {
					return vis[i]
				}
				return nil
			}

			languages := newFullTextLanguages(langs, false)
			wordField := propsheet.Text("Stopword", "", 30)
			wordField.SetDirtyTracked(false)
			langRow := languages.row("Language")
			hint := propsheet.Hint()
			redraw := func(row int) { resetGrid(grid, stopwordHeaders, rowsFor(), row) }

			addBtn := widgets.NewButton("Add", func() {
				w := strings.TrimSpace(wordField.Value())
				if w == "" {
					hint.Set("Type a word first.")
					return
				}
				lcid := languages.lcid(langRow.Selected())
				id, _ := strconv.Atoi(lcid)
				nw := gosmo.FullTextStopword{Word: w, LanguageID: id, Language: langs[langRow.Selected()].Name}
				for _, e := range edits.all() {
					if !sameStopword(e.word, nw) {
						continue
					}
					if e.removing {
						// Re-adding a word marked for removal keeps it.
						edits.restore(e)
						hint.Clear()
						wordField.SetValue("")
						redraw(len(edits.visible()) - 1)
						return
					}
					hint.SetError(fmt.Sprintf("%q is already a %s stopword.", w, nw.Language))
					return
				}
				hint.Clear()
				edits.add(&stopwordEdit{word: nw})
				wordField.SetValue("")
				redraw(len(edits.visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				e := selected()
				if e == nil {
					hint.Set("Select a word in the grid to remove it.")
					return
				}
				hint.Clear()
				edits.remove(e)
				redraw(max(0, grid.SelectedRow()-1))
			})
			removeLangBtn := widgets.NewButton("Remove Language", func() {
				e := selected()
				if e == nil {
					hint.Set("Select a word of the language to remove.")
					return
				}
				id := e.word.LanguageID
				n := 0
				for _, x := range edits.visible() {
					if x.word.LanguageID == id {
						edits.remove(x)
						n++
					}
				}
				hint.Set(fmt.Sprintf("%d %s words will be removed on Apply.", n, e.word.Language))
				redraw(0)
			})

			gridRow := propsheet.NewGridRow(grid, 12)
			gridRow.DirtyFn = edits.dirty
			gridRow.RevertFn = func() {
				edits.revert()
				redraw(0)
			}
			f := propsheet.NewForm(
				propsheet.Section("Stopwords by language"),
				gridRow,
				propsheet.Section("Add a stopword"),
				wordField,
				langRow,
				propsheet.Buttons(addBtn, removeBtn, removeLangBtn),
				hint,
				propsheet.Note("A stopword is left out of every full-text index using this stoplist. "+
					"An index picks up a change at its next full population."),
			)
			if len(langs) == 0 {
				f.Add(propsheet.Note("The instance lists no full-text language, so no word can be added."))
				addBtn.SetEnabled(false)
			}
			name := l.Name
			return f, func(ctx context.Context) error {
				return applyStopwords(ctx, sc.Server.DatabaseRef(dbName).FullText().StoplistRef(name), edits.all())
			}, nil
		},
	}
}

// applyStopwords writes the Stopwords page's pending rows: drops first, as
// few statements as cover them, then adds.
func applyStopwords(ctx context.Context, l *gosmo.FullTextStoplist, rows []*stopwordEdit) error {
	var loaded, removing int
	langLoaded := map[int]int{}
	langRemoving := map[int]int{}
	for _, e := range rows {
		if e.isNew {
			continue
		}
		loaded++
		langLoaded[e.word.LanguageID]++
		if e.removing {
			removing++
			langRemoving[e.word.LanguageID]++
		}
	}
	switch {
	case removing > 0 && removing == loaded:
		if err := l.DropAllStopwords(ctx); err != nil {
			return err
		}
	case removing > 0:
		whole := map[int]bool{}
		for id, n := range langRemoving {
			if n == langLoaded[id] {
				whole[id] = true
				if err := l.DropLanguageStopwords(ctx, strconv.Itoa(id)); err != nil {
					return err
				}
			}
		}
		for _, e := range rows {
			if e.removing && !e.isNew && !whole[e.word.LanguageID] {
				if err := l.DropStopword(ctx, e.word.Word, strconv.Itoa(e.word.LanguageID)); err != nil {
					return err
				}
			}
		}
	}
	for _, e := range rows {
		if e.isNew {
			if err := l.AddStopword(ctx, e.word.Word, strconv.Itoa(e.word.LanguageID)); err != nil {
				return err
			}
		}
	}
	return nil
}
