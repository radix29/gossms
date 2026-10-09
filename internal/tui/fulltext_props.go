package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// fulltext_props.go is the Properties for a full-text catalog, a stoplist and a
// search property list, and the helpers the four full-text families share; a
// table's full-text index is fulltext_index_props.go. The New dialogs are the
// new_fulltext_*_dialog.go files.
//
// What each page writes, and the right that gates it (probed on majors 14 and 17,
// identical — gate.AlterOnFullTextCatalog's comment):
//   - Catalog General: AS DEFAULT, which needs ALTER ANY FULLTEXT CATALOG; ALTER on
//     the catalog alone is refused it.
//   - Catalog Maintenance: REORGANIZE (SSMS's Optimize) and REBUILD, with the
//     accent sensitivity a rebuild can change — ALTER on the catalog.
//   - Stoplist Stopwords and Property List Properties: ADD/DROP — ALTER on the
//     stoplist or list.
//
// Owners are shown, not changed: ALTER AUTHORIZATION is a different right (TAKE
// OWNERSHIP, or IMPERSONATE on the new owner), and the New dialogs set the owner
// the object starts with.
//
// The finders are the ones the Details pane uses (detail_browser_fulltext.go), so
// the pane and the dialog cannot disagree about which object a node names.

func findFullTextCatalog(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.FullTextCatalog, error) {
	return inDB(ctx, sc, dbName, name, (*gosmo.Database).FullTextCatalogByName)
}

func findFullTextStoplist(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.FullTextStoplist, error) {
	return inDB(ctx, sc, dbName, name, (*gosmo.Database).FullTextStoplistByName)
}

func findSearchPropertyList(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.SearchPropertyList, error) {
	return inDB(ctx, sc, dbName, name, (*gosmo.Database).SearchPropertyListByName)
}

// findFullTextIndex resolves a table's full-text index. FullTextIndex needs
// the loaded table (its object id), so this is TableByName, never TableRef.
//
// A table without one is an ordinary answer, not a failure: (nil, nil), which
// each page shows as noFullTextIndexForm. Only the index read's not-found is
// that — a table that is itself gone stays an error.
func findFullTextIndex(ctx context.Context, sc *db.ServerConn, dbName, schema, table string) (*gosmo.FullTextIndex, error) {
	t, err := findTable(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, err
	}
	i, err := t.FullTextIndex(ctx)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, nil
	}
	return i, err
}

// noFullTextIndexForm is an index page for a table that has none.
func noFullTextIndexForm() *propsheet.Form {
	return propsheet.NewForm(propsheet.Note("This table has no full-text index."))
}

// fullTextDate is a catalog or index time as the pages show it: "Never" for
// the zero time, which gosmo reads for "not yet".
func fullTextDate(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return formatSQLDate(t)
}

// fullTextStoplistText is an index's STOPLIST setting as SSMS's General page
// words it: off, the system stoplist, or the user stoplist's name.
func fullTextStoplistText(i *gosmo.FullTextIndex) string {
	switch i.StoplistKind {
	case gosmo.FullTextStoplistOff:
		return "<off>"
	case gosmo.FullTextStoplistSystem:
		return "<system>"
	}
	return i.Stoplist
}

// fullTextColumnNames is an index's columns, comma-joined, for a grid cell.
func fullTextColumnNames(i *gosmo.FullTextIndex) string {
	names := make([]string, len(i.Columns))
	for k, c := range i.Columns {
		names[k] = c.Name
	}
	return strings.Join(names, ", ")
}

// fullTextColumnRows is an index's columns as the Columns grids show them.
var fullTextColumnHeaders = []string{"Column", "Type Column", "Language", "Statistical Semantics"}

func fullTextColumnRows(i *gosmo.FullTextIndex) [][]string {
	rows := make([][]string, len(i.Columns))
	for k, c := range i.Columns {
		lang := c.Language
		if lang == "" {
			lang = strconv.Itoa(c.LanguageID)
		}
		rows[k] = []string{c.Name, c.TypeColumn, lang, boolStr(c.StatisticalSemantics)}
	}
	return rows
}

// -- Owners and languages --------------------------------------------------------

// fullTextDefaultOwner is the New dialogs' first Owner item: no AUTHORIZATION
// clause, so the object belongs to whoever creates it.
const fullTextDefaultOwner = "(current user)"

// fullTextOwnerItems is a New dialog's Owner list: the default, then the
// database's users and roles — AUTHORIZATION takes either for all three
// families.
func fullTextOwnerItems(ctx context.Context, d *gosmo.Database) ([]string, error) {
	users, err := d.Users(ctx)
	if err != nil {
		return nil, err
	}
	roles, err := d.DatabaseRoles(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string{fullTextDefaultOwner}, principalNames(users, roles)...), nil
}

// fullTextOwnerValue is an Owner row as a request's Owner: "" for the default.
func fullTextOwnerValue(row *propsheet.SelectRow) string {
	if v := row.Value(); v != fullTextDefaultOwner {
		return v
	}
	return ""
}

// fullTextLanguages is a language picker's items and, index for index, the
// LCID each sends — gosmo takes an LCID as a string. withDefault leads with an
// item sending "", the instance's default full-text language (a column's
// LANGUAGE clause left out); without it, English is preselected where the
// instance has it, as the stoplist's ADD needs a language.
type fullTextLanguages struct {
	items []string
	lcids []string
	def   int
}

const fullTextDefaultLanguage = "(default full-text language)"

func newFullTextLanguages(langs []gosmo.FullTextLanguage, withDefault bool) fullTextLanguages {
	var l fullTextLanguages
	if withDefault {
		l.items, l.lcids = []string{fullTextDefaultLanguage}, []string{""}
	}
	for _, g := range langs {
		if !withDefault && g.LCID == 1033 {
			l.def = len(l.items)
		}
		l.items = append(l.items, g.Name+" ("+strconv.Itoa(g.LCID)+")")
		l.lcids = append(l.lcids, strconv.Itoa(g.LCID))
	}
	return l
}

// lcid is the LCID item i sends.
func (l fullTextLanguages) lcid(i int) string {
	if i < 0 || i >= len(l.lcids) {
		return ""
	}
	return l.lcids[i]
}

// row is an untracked picker over the languages: it chooses what an Add
// button adds, so moving it is not an edit of the page.
func (l fullTextLanguages) row(label string) *propsheet.SelectRow {
	r := propsheet.Select(label, l.items, l.def)
	r.SetDirtyTracked(false)
	r.SetFitItems(true)
	return r
}

// readFullTextLanguages is the instance's word-breaker languages. They are
// what LANGUAGE accepts, so a picker built from them offers nothing the
// server refuses.
func readFullTextLanguages(ctx context.Context, sc *db.ServerConn) ([]gosmo.FullTextLanguage, error) {
	info, err := sc.Server.FullTextInfo(ctx)
	if err != nil {
		return nil, err
	}
	return info.Languages, nil
}

// -- Catalog -------------------------------------------------------------------

func fullTextCatalogPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextCatalog, error) {
		return findFullTextCatalog(ctx, sc, dbName, name)
	}
	return []propPage{
		withRequires(pageFullTextCatalogGeneral(sc, dbName, find), dbName, gate.AlterAnyFullTextCatalog),
		withRequiresOn(pageFullTextCatalogMaintenance(sc, dbName, find), dbName, "", name, gate.AlterOnFullTextCatalog),
		pageFullTextCatalogTables(find),
	}
}

func fullTextAccentText(sensitive bool) string {
	if sensitive {
		return "Sensitive"
	}
	return "Insensitive"
}

func pageFullTextCatalogGeneral(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.FullTextCatalog, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Catalog"),
				propsheet.Static("Name", c.Name),
				propsheet.Static("Owner", c.Owner),
			)
			// There is no statement that unsets the default: another catalog
			// becomes it instead, so the default one shows it, and the others
			// offer to take it.
			var isDefault *propsheet.CheckRow
			if c.IsDefault {
				f.Add(propsheet.Static("Default catalog", "Yes"))
			} else {
				isDefault = propsheet.Check("Set as default catalog", false)
				f.Add(isDefault)
			}
			f.Add(
				propsheet.Static("Accent sensitivity", fullTextAccentText(c.AccentSensitive)),
				propsheet.Static("Importing", boolStr(c.IsImporting)),
				propsheet.Section("Population"),
				propsheet.Static("Population status", c.PopulateStatus.String()),
				propsheet.Static("Last population date", fullTextDate(c.LastPopulated)),
				propsheet.Static("Master merge in progress", boolStr(c.MergeInProgress)),
				propsheet.Section("Size"),
				propsheet.Static("Full-text indexes", strconv.Itoa(c.IndexCount)),
				propsheet.Static("Item count", strconv.Itoa(c.ItemCount)),
				propsheet.Static("Unique key count", strconv.Itoa(c.UniqueKeyCount)),
				propsheet.Static("Catalog size (MB)", strconv.Itoa(c.SizeMB)),
				propsheet.Note("The default catalog is the one a full-text index goes into when it names none. "+
					"Accent sensitivity changes by a rebuild, on the Maintenance page."),
			)
			if isDefault == nil {
				return f, nil, nil
			}
			name := c.Name
			return f, func(ctx context.Context) error {
				if !isDefault.Dirty() || !isDefault.Checked() {
					return nil
				}
				return sc.Server.DatabaseRef(dbName).FullTextCatalogRef(name).SetDefault(ctx)
			}, nil
		},
	}
}

// The Maintenance page's actions, in its radio's order.
const (
	catalogActionNone = iota
	catalogActionReorganize
	catalogActionRebuild
)

// fullTextRebuildAccentItems is the Maintenance page's accent choice, read by
// fullTextAccentChoice after its first item ("keep") is skipped.
var fullTextRebuildAccentItems = []string{"Keep current", "Sensitive", "Insensitive"}

// pageFullTextCatalogMaintenance is SSMS's catalog action: Optimize
// (REORGANIZE, which merges the index fragments) or Rebuild (which empties and
// repopulates every index in the catalog, and is the only way to change its
// accent sensitivity). The two rows steer each other — picking an accent
// selects Rebuild, and leaving Rebuild puts the accent back — so a change of
// accent never sits on the page without the rebuild that applies it.
func pageFullTextCatalogMaintenance(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.FullTextCatalog, error)) propPage {
	return propPage{
		title: "Maintenance",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			action := propsheet.Radio("Catalog action", []string{
				"None", "Optimize (reorganize)", "Rebuild",
			}, catalogActionNone)
			accent := propsheet.Select("Accent sensitivity", fullTextRebuildAccentItems, 0)
			accent.SetOnChange(func(string) {
				if accent.Selected() != 0 && action.Selected() != catalogActionRebuild {
					action.Edit(catalogActionRebuild)
				}
			})
			action.SetOnChange(func(int) {
				if action.Selected() != catalogActionRebuild && accent.Selected() != 0 {
					accent.Edit(0)
				}
			})
			f := propsheet.NewForm(
				propsheet.Section("Catalog"),
				propsheet.Static("Name", c.Name),
				propsheet.Static("Accent sensitivity now", fullTextAccentText(c.AccentSensitive)),
				propsheet.Static("Population status", c.PopulateStatus.String()),
				propsheet.Section("Action"),
				action,
				accent,
				propsheet.Note("Optimize merges each index's fragments into one, which makes queries faster; "+
					"the indexes stay usable meanwhile. Rebuild deletes every index's contents and repopulates them, "+
					"so queries find nothing until the population finishes."),
			)
			f.SetApplyConfirm(func() string {
				if action.Selected() != catalogActionRebuild {
					return ""
				}
				return fmt.Sprintf("Rebuilding full-text catalog %q empties and repopulates every index in it; "+
					"full-text queries on its tables return nothing until that finishes.", c.Name)
			})
			name := c.Name
			return f, func(ctx context.Context) error {
				cat := sc.Server.DatabaseRef(dbName).FullTextCatalogRef(name)
				switch action.Selected() {
				case catalogActionReorganize:
					return cat.Reorganize(ctx)
				case catalogActionRebuild:
					return cat.Rebuild(ctx, fullTextAccentChoice(accent.Selected()))
				}
				return nil
			}, nil
		},
	}
}

// pageFullTextCatalogTables is SSMS's Tables/Views page: the catalog's
// indexes, and the selected one's columns below. It is read here; an index
// is changed from its table's Full-Text index menu.
func pageFullTextCatalogTables(find func(context.Context) (*gosmo.FullTextCatalog, error)) propPage {
	return propPage{
		title: "Tables/Views",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			idx, err := c.Indexes(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(idx))
			for k, i := range idx {
				rows[k] = []string{articleObject(i.Schema, i.Table), i.KeyIndex, boolStr(i.IsEnabled),
					string(i.ChangeTracking), strconv.Itoa(i.ItemCount)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Table/View", "Unique Index", "Enabled", "Change Tracking", "Items"}, rows)
			cols := controls.NewDataGrid()
			cols.SetData(fullTextColumnHeaders, nil)
			// The column grid follows the selection only: a read-only
			// page's grid browses (docs/ui-rules.md).
			sync := func(row int) {
				if row < 0 || row >= len(idx) {
					resetGrid(cols, fullTextColumnHeaders, nil, 0)
					return
				}
				resetGrid(cols, fullTextColumnHeaders, fullTextColumnRows(idx[row]), 0)
			}
			grid.OnSelectRow = sync
			if len(idx) > 0 {
				sync(0)
			}
			f := propsheet.NewForm(
				propsheet.Section("Full-text indexed tables and views"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Columns of the selected table"),
				propsheet.NewGridRow(cols, 6),
			)
			if len(idx) == 0 {
				f.Add(propsheet.Note("No full-text index is in this catalog yet."))
			}
			return f, nil, nil
		},
	}
}

// -- Stoplist ------------------------------------------------------------------

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
				return propsheet.NewForm(
					propsheet.Section("Stoplist"),
					propsheet.Static("Name", l.Name),
					propsheet.Static("Owner", l.Owner),
					propsheet.Static("Created", formatSQLDate(l.CreateDate)),
					propsheet.Static("Last modified", formatSQLDate(l.ModifyDate)),
					propsheet.Note("Its words are edited on the Stopwords page."),
				), nil, nil
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
				return applyStopwords(ctx, sc.Server.DatabaseRef(dbName).FullTextStoplistRef(name), edits.all())
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

// -- Search property list ------------------------------------------------------

func searchPropertyListPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.SearchPropertyList, error) {
		return findSearchPropertyList(ctx, sc, dbName, name)
	}
	return []propPage{
		{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				p, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				return propsheet.NewForm(
					propsheet.Section("Search property list"),
					propsheet.Static("Name", p.Name),
					propsheet.Static("Owner", p.Owner),
					propsheet.Static("Created", formatSQLDate(p.CreateDate)),
					propsheet.Static("Last modified", formatSQLDate(p.ModifyDate)),
					propsheet.Note("Its properties are registered on the Properties page."),
				), nil, nil
			},
		},
		withRequiresOn(pageSearchProperties(sc, dbName, find), dbName, "", name, gate.AlterOnSearchPropertyList),
	}
}

var searchPropertyHeaders = []string{"Property Name", "Property Set GUID", "Int ID", "Internal ID", "Description"}

func searchPropertyRows(props []gosmo.SearchProperty) [][]string {
	rows := make([][]string, len(props))
	for k, p := range props {
		id := strconv.Itoa(p.ID)
		if p.ID == 0 {
			id = "" // not registered yet: the server assigns it
		}
		rows[k] = []string{p.Name, p.SetGUID, strconv.Itoa(p.IntID), id, p.Description}
	}
	return rows
}

// searchPropertyEdit is one row of the Properties page.
type searchPropertyEdit struct {
	prop gosmo.SearchProperty
	pendingState
}

// guidPattern is PROPERTY_SET_GUID's shape; the server refuses anything else
// with a conversion error that does not name the field.
var guidPattern = regexp.MustCompile(`^\{?[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}?$`)

// pageSearchProperties is the list's registered properties, master-detail
// over pendingEdits: the four fields to Add, Remove for the selected row. A
// property is identified by name within the list (ALTER … DROP names it), so
// Apply drops before it adds and a removed name can be registered again.
func pageSearchProperties(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.SearchPropertyList, error)) propPage {
	return propPage{
		title: "Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			props, err := p.Properties(ctx)
			if err != nil {
				return nil, nil, err
			}
			loaded := make([]*searchPropertyEdit, len(props))
			for k, x := range props {
				loaded[k] = &searchPropertyEdit{prop: x}
			}
			edits := newPendingEdits(databaseCollation(p.Database()), loaded,
				func(e *searchPropertyEdit) string { return e.prop.Name }, nil, nil)
			rowsFor := func() [][]string {
				vis := edits.visible()
				out := make([]gosmo.SearchProperty, len(vis))
				for k, e := range vis {
					out[k] = e.prop
				}
				return searchPropertyRows(out)
			}
			grid := controls.NewDataGrid()
			grid.SetData(searchPropertyHeaders, rowsFor())
			redraw := func(row int) { resetGrid(grid, searchPropertyHeaders, rowsFor(), row) }

			nameField := propsheet.Text("Property name", "", 30)
			guidField := propsheet.Text("Property set GUID", "", 38)
			intField := propsheet.Text("Property int ID", "", 10)
			descField := propsheet.Text("Description", "", 40)
			for _, r := range []*propsheet.TextRow{nameField, guidField, intField, descField} {
				r.SetDirtyTracked(false)
			}
			hint := propsheet.Hint()

			addBtn := widgets.NewButton("Add", func() {
				prop, err := searchPropertyInput(nameField.Value(), guidField.Value(), intField.Value(), descField.Value())
				if err != nil {
					hint.SetError(err.Error())
					return
				}
				if edits.listed(prop.Name) {
					hint.SetError("A property named " + prop.Name + " is already listed.")
					return
				}
				hint.Clear()
				edits.add(&searchPropertyEdit{prop: prop})
				for _, r := range []*propsheet.TextRow{nameField, guidField, intField, descField} {
					r.SetValue("")
				}
				redraw(len(edits.visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				vis := edits.visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					hint.Set("Select a property in the grid to remove it.")
					return
				}
				hint.Clear()
				edits.remove(vis[i])
				redraw(max(0, i-1))
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = edits.dirty
			gridRow.RevertFn = func() {
				edits.revert()
				redraw(0)
			}
			f := propsheet.NewForm(
				propsheet.Section("Registered search properties"),
				gridRow,
				propsheet.Section("Register a property"),
				nameField, guidField, intField, descField,
				propsheet.Buttons(addBtn, removeBtn),
				hint,
				propsheet.Note("A property is the set GUID and integer ID its document filter publishes it under "+
					"(Title is F29F85E0-4FF9-1068-AB91-08002B27B3D9, 2). An index using this list picks a change up "+
					"at its next full population."),
			)
			name := p.Name
			return f, func(ctx context.Context) error {
				l := sc.Server.DatabaseRef(dbName).SearchPropertyListRef(name)
				rows := edits.all()
				for _, e := range rows {
					if e.removing && !e.isNew {
						if err := l.DropProperty(ctx, e.prop.Name); err != nil {
							return err
						}
					}
				}
				for _, e := range rows {
					if e.isNew {
						if err := l.AddProperty(ctx, e.prop); err != nil {
							return err
						}
					}
				}
				return nil
			}, nil
		},
	}
}

// searchPropertyInput validates the Add fields into a property: a name, a
// GUID in its usual spelling (braces optional, dropped), and an integer ID.
func searchPropertyInput(name, guid, intID, desc string) (gosmo.SearchProperty, error) {
	name, guid, intID = strings.TrimSpace(name), strings.TrimSpace(guid), strings.TrimSpace(intID)
	if name == "" {
		return gosmo.SearchProperty{}, fmt.Errorf("type a property name first")
	}
	if !guidPattern.MatchString(guid) {
		return gosmo.SearchProperty{}, fmt.Errorf("the property set GUID must look like F29F85E0-4FF9-1068-AB91-08002B27B3D9")
	}
	id, err := strconv.Atoi(intID)
	if err != nil || id < 0 {
		return gosmo.SearchProperty{}, fmt.Errorf("the property int ID must be a whole number, 0 or more")
	}
	return gosmo.SearchProperty{
		Name:        name,
		SetGUID:     strings.Trim(guid, "{}"),
		IntID:       id,
		Description: strings.TrimSpace(desc),
	}, nil
}
