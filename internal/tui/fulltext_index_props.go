package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// fulltext_index_props.go is a table's Full-Text Index Properties (W15
// read-only, W19 writable) and the column editor it shares with New
// Full-Text Index (new_fulltext_index_dialog.go).
//
// Both pages write through ALTER FULLTEXT INDEX, gated on ALTER on the table
// (gate.ObjectWriteRights(), the cascade's set — explorer_fulltext.go). Naming
// a user stoplist or a property list also needs REFERENCES on it (2026-10-08
// probe, gate.ReferencesOnFullTextStoplist); the General page asks that of the
// cached capabilities when it validates (SelectRow.SetValidate), which refuses
// before anything is sent.

func fullTextIndexPropPages(sc *db.ServerConn, dbName, schema, table string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextIndex, error) {
		return findFullTextIndex(ctx, sc, dbName, schema, table)
	}
	return []propPage{
		withRequiresOn(pageFullTextIndexGeneral(sc, dbName, find), dbName, schema, table, gate.ObjectWriteRights()...),
		withRequiresOn(pageFullTextIndexColumns(sc, dbName, schema, table, find), dbName, schema, table, gate.ObjectWriteRights()...),
	}
}

// fullTextChangeTrackingItems are the General page's change-tracking choices,
// in the DDL's spelling, which is how the index reports them.
var fullTextChangeTrackingItems = []string{
	string(gosmo.FullTextChangeTrackingAuto),
	string(gosmo.FullTextChangeTrackingManual),
	string(gosmo.FullTextChangeTrackingOff),
}

// The stoplist and property-list pickers' fixed items, before the database's
// own: what fullTextStoplistText shows for the two kinds without a name.
const (
	stoplistOffItem    = "<off>"
	stoplistSystemItem = "<system>"
	propertyListNone   = "<none>"
)

// fullTextStoplistChoice reads a stoplist picker back: the kind and, for a
// user stoplist, its name.
func fullTextStoplistChoice(item string) (gosmo.FullTextStoplistKind, string) {
	switch item {
	case stoplistOffItem:
		return gosmo.FullTextStoplistOff, ""
	case stoplistSystemItem:
		return gosmo.FullTextStoplistSystem, ""
	}
	return gosmo.FullTextStoplistUser, item
}

// fullTextPickers reads the database's stoplists and property lists for the
// General page and New Full-Text Index: every one the login can see, since
// sys.fulltext_stoplists and sys.registered_search_property_lists show only
// those.
func fullTextPickers(ctx context.Context, d *gosmo.Database) (stoplists, lists []string, err error) {
	sl, err := d.FullTextStoplists(ctx)
	if err != nil {
		return nil, nil, err
	}
	pl, err := d.SearchPropertyLists(ctx)
	if err != nil {
		return nil, nil, err
	}
	stoplists = []string{stoplistOffItem, stoplistSystemItem}
	for _, s := range sl {
		stoplists = append(stoplists, s.Name)
	}
	lists = []string{propertyListNone}
	for _, p := range pl {
		lists = append(lists, p.Name)
	}
	return stoplists, lists, nil
}

// fullTextReferencesRefusal is what the General page and New Full-Text Index
// say before sending a statement that names a stoplist, property list or
// catalog the login holds no REFERENCES on (or nil). It reads the cached
// capabilities, so it runs on the UI goroutine, and fails open like every
// gate.
func fullTextReferencesRefusal(sc *db.ServerConn, dbName string, right gate.Right, what, name string) error {
	if name == "" || gate.AllowsOn(sc, dbName, "", name, right) {
		return nil
	}
	return fmt.Errorf("naming %s %s in a full-text index needs REFERENCES on it", what, name)
}

func pageFullTextIndexGeneral(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.FullTextIndex, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			i, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			if i == nil {
				return noFullTextIndexForm(), nil, nil
			}
			stoplists, lists, err := fullTextPickers(ctx, i.Database())
			if err != nil {
				return nil, nil, err
			}
			tracking := selectPreserving("Change tracking", fullTextChangeTrackingItems, string(i.ChangeTracking), unknownOwnerItem)
			stoplist := selectPreserving("Stoplist", stoplists, fullTextStoplistText(i), unknownOwnerItem)
			stoplist.SetFitItems(true)
			list := selectPreserving("Search property list", lists, orDefault(i.SearchPropertyList, propertyListNone), unknownOwnerItem)
			list.SetFitItems(true)
			// Asked only of a change: the loaded choice is the server's own.
			stoplist.SetValidate(func(v string) error {
				kind, name := fullTextStoplistChoice(v)
				if !stoplist.Dirty() || kind != gosmo.FullTextStoplistUser {
					return nil
				}
				return fullTextReferencesRefusal(sc, dbName, gate.ReferencesOnFullTextStoplist, "stoplist", name)
			})
			list.SetValidate(func(v string) error {
				if !list.Dirty() || v == propertyListNone {
					return nil
				}
				return fullTextReferencesRefusal(sc, dbName, gate.ReferencesOnSearchPropertyList, "search property list", v)
			})
			f := propsheet.NewForm(
				propsheet.Section("Index"),
				propsheet.Static("Table", articleObject(i.Schema, i.Table)),
				propsheet.Static("Unique index", i.KeyIndex),
				propsheet.Static("Full-text catalog", i.Catalog),
				propsheet.Static("Filegroup", orNone(i.FileGroup)),
				propsheet.Static("Enabled", boolStr(i.IsEnabled)),
				tracking,
				stoplist,
				list,
			)
			// index_version exists from 2025 only; 0 below it.
			if i.IndexVersion > 0 {
				f.Add(propsheet.Static("Index version", strconv.Itoa(i.IndexVersion)))
			}
			f.Add(propsheet.Section("Last population"))
			crawl := i.CrawlType
			if crawl == "" {
				crawl = "None"
			}
			f.Add(propsheet.Static("Type", crawl))
			f.Add(propsheet.Static("Completed", boolStr(i.CrawlCompleted)))
			f.Add(propsheet.Static("Started", fullTextDate(i.CrawlStart)))
			f.Add(propsheet.Static("Ended", fullTextDate(i.CrawlEnd)))
			f.Add(propsheet.Section("Counters"))
			f.Add(propsheet.Static("Population status", i.PopulateStatus.String()))
			f.Add(propsheet.Static("Item count", strconv.Itoa(i.ItemCount)))
			f.Add(propsheet.Static("Documents processed", strconv.Itoa(i.DocsProcessed)))
			f.Add(propsheet.Static("Documents failed", strconv.Itoa(i.FailCount)))
			f.Add(propsheet.Static("Pending changes", strconv.Itoa(i.PendingChanges)))
			f.Add(propsheet.Section("Running populations"))
			for _, r := range fullTextPopulationRows(ctx, i) {
				f.Add(propsheet.Static(r[0], r[1]))
			}
			f.Add(propsheet.Note("A new stoplist or property list repopulates the index in full. " +
				"Enable, Disable and populations are on the table's Full-Text index menu."))

			pending := i.PendingChanges
			f.SetApplyConfirm(func() string {
				var warn []string
				if tracking.Dirty() && tracking.Value() == string(gosmo.FullTextChangeTrackingOff) && pending > 0 {
					warn = append(warn, fmt.Sprintf("turning change tracking off discards the %d changes not yet applied", pending))
				}
				if stoplist.Dirty() || list.Dirty() {
					warn = append(warn, "the new stoplist or property list starts a full population of the index")
				}
				if len(warn) == 0 {
					return ""
				}
				msg := strings.Join(warn, "; and ")
				return strings.ToUpper(msg[:1]) + msg[1:] + "."
			})

			ref := i.Database().TableRef(i.Schema, i.Table).FullTextIndexRef()
			return f, func(ctx context.Context) error {
				if v, ok := changedTo(tracking, unknownOwnerItem); ok {
					if err := ref.SetChangeTracking(ctx, gosmo.FullTextChangeTracking(v)); err != nil {
						return err
					}
				}
				if v, ok := changedTo(stoplist, unknownOwnerItem); ok {
					kind, name := fullTextStoplistChoice(v)
					if err := ref.SetStoplist(ctx, kind, name); err != nil {
						return err
					}
				}
				if v, ok := changedTo(list, unknownOwnerItem); ok {
					if v == propertyListNone {
						v = ""
					}
					if err := ref.SetSearchPropertyList(ctx, v); err != nil {
						return err
					}
				}
				return nil
			}, nil
		},
	}
}

// fullTextPopulationRows is an index's running populations as label/value
// pairs, shared with the Details pane. The DMV needs VIEW SERVER STATE, which
// the index's own read does not, so a refusal costs only these rows and says
// which right is missing.
func fullTextPopulationRows(ctx context.Context, i *gosmo.FullTextIndex) [][]string {
	pops, err := i.Populations(ctx)
	if err != nil {
		if denied := accessDeniedText(err); denied != "" {
			return [][]string{{"Populations", "not visible — " + denied}}
		}
		return [][]string{{"Populations", err.Error()}}
	}
	if len(pops) == 0 {
		return [][]string{{"Populations", "None (idle)"}}
	}
	rows := make([][]string, 0, len(pops))
	for k, p := range pops {
		v := p.Type + ", " + p.Status + ", " + strconv.Itoa(p.RangesDone) + " of " +
			strconv.Itoa(p.RangeCount) + " ranges, started " + formatSQLDate(p.StartTime)
		if p.QueuedType != "" {
			v += ", " + p.QueuedType + " queued"
		}
		rows = append(rows, []string{"Population " + strconv.Itoa(k+1), v})
	}
	return rows
}

func pageFullTextIndexColumns(sc *db.ServerConn, dbName, schema, table string, find func(context.Context) (*gosmo.FullTextIndex, error)) propPage {
	return propPage{
		title: "Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			i, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			if i == nil {
				return noFullTextIndexForm(), nil, nil
			}
			// The loaded table, not a TableRef: Columns reads by object id.
			t, err := findTable(ctx, sc, dbName, schema, table)
			if err != nil {
				return nil, nil, err
			}
			cols, err := t.Columns(ctx)
			if err != nil {
				return nil, nil, err
			}
			langs, err := readFullTextLanguages(ctx, sc)
			if err != nil {
				return nil, nil, err
			}
			ed := newFullTextColumnEditor(databaseCollation(i.Database()), cols, langs, i.Columns)
			f := propsheet.NewForm(ed.rows(
				"Adding or removing a column repopulates the index in full. " +
					"To change a column's language or type column, remove it and add it back.")...)
			ref := i.Database().TableRef(i.Schema, i.Table).FullTextIndexRef()
			return f, func(ctx context.Context) error {
				for _, e := range ed.edits.all() {
					if e.removing && !e.isNew {
						if err := ref.DropColumn(ctx, e.spec.Name, false); err != nil {
							return err
						}
					}
				}
				for _, e := range ed.edits.all() {
					if e.isNew {
						if err := ref.AddColumn(ctx, e.spec, false); err != nil {
							return err
						}
					}
				}
				return nil
			}, nil
		},
	}
}

// -- Column editor -------------------------------------------------------------

// fullTextColEdit is one full-text indexed column, loaded or added. The
// language it shows is kept beside the LCID the spec sends.
type fullTextColEdit struct {
	spec     gosmo.FullTextIndexColumnSpec
	language string
	pendingState
}

// fullTextColumnEditor is the column grid of Full-Text Index Properties and
// New Full-Text Index: the indexed columns, and Add/Remove over the table's
// eligible ones with a language, a type column and statistical semantics.
// Loaded columns are only removed, never edited in place — ALTER FULLTEXT
// INDEX has no form that changes one.
type fullTextColumnEditor struct {
	edits    *pendingEdits[*fullTextColEdit]
	eligible []string
	// binary names the eligible columns holding documents (varbinary, image),
	// which need a TYPE COLUMN.
	binary map[string]bool
	// typeCols are the columns a TYPE COLUMN can name.
	typeCols []string
	langs    fullTextLanguages
}

// fullTextIndexableTypes are the column types CREATE FULLTEXT INDEX accepts.
var fullTextIndexableTypes = map[string]bool{
	"char": true, "varchar": true, "nchar": true, "nvarchar": true, "text": true, "ntext": true,
	"xml": true, "varbinary": true, "image": true,
}

// fullTextTypeColumnTypes are the types a TYPE COLUMN may have.
var fullTextTypeColumnTypes = map[string]bool{"char": true, "varchar": true, "nchar": true, "nvarchar": true}

func newFullTextColumnEditor(collation string, cols []*gosmo.Column, langs []gosmo.FullTextLanguage, loaded []gosmo.FullTextIndexColumn) *fullTextColumnEditor {
	ed := &fullTextColumnEditor{binary: map[string]bool{}, langs: newFullTextLanguages(langs, true)}
	for _, c := range cols {
		t := strings.ToLower(string(c.DataType))
		if c.IsUserDefinedType {
			continue // an alias type's base type is not read here; offering it would be a guess
		}
		if fullTextIndexableTypes[t] {
			ed.eligible = append(ed.eligible, c.Name)
			if t == "varbinary" || t == "image" {
				ed.binary[c.Name] = true
			}
		}
		if fullTextTypeColumnTypes[t] {
			ed.typeCols = append(ed.typeCols, c.Name)
		}
	}
	rows := make([]*fullTextColEdit, len(loaded))
	for k, c := range loaded {
		lang := c.Language
		if lang == "" {
			lang = strconv.Itoa(c.LanguageID)
		}
		rows[k] = &fullTextColEdit{
			spec: gosmo.FullTextIndexColumnSpec{Name: c.Name, TypeColumn: c.TypeColumn,
				Language: strconv.Itoa(c.LanguageID), StatisticalSemantics: c.StatisticalSemantics},
			language: lang,
		}
	}
	ed.edits = newPendingEdits(collation, rows, func(e *fullTextColEdit) string { return e.spec.Name }, nil, nil)
	return ed
}

func (ed *fullTextColumnEditor) gridRows() [][]string {
	vis := ed.edits.visible()
	out := make([][]string, len(vis))
	for k, e := range vis {
		out[k] = []string{e.spec.Name, e.spec.TypeColumn, e.language, boolStr(e.spec.StatisticalSemantics)}
	}
	return out
}

// specs is the columns the index will have: what a create sends.
func (ed *fullTextColumnEditor) specs() []gosmo.FullTextIndexColumnSpec {
	vis := ed.edits.visible()
	out := make([]gosmo.FullTextIndexColumnSpec, len(vis))
	for k, e := range vis {
		out[k] = e.spec
	}
	return out
}

// rows builds the editor's form rows, note closing them.
func (ed *fullTextColumnEditor) rows(note string) []propsheet.Row {
	grid := controls.NewDataGrid()
	grid.SetData(fullTextColumnHeaders, ed.gridRows())
	redraw := func(row int) { resetGrid(grid, fullTextColumnHeaders, ed.gridRows(), row) }

	untracked := func(r *propsheet.SelectRow) *propsheet.SelectRow {
		r.SetDirtyTracked(false)
		r.SetFitItems(true)
		return r
	}
	colRow := untracked(propsheet.Select("Column", ed.eligible, 0))
	typeRow := untracked(propsheet.Select("Type column", append([]string{propertyListNone}, ed.typeCols...), 0))
	langRow := ed.langs.row("Language")
	semRow := untracked(propsheet.Select("Statistical semantics", []string{"No", "Yes"}, 0))
	hint := propsheet.Hint()

	addBtn := widgets.NewButton("Add", func() {
		name := colRow.Value()
		if name == "" {
			hint.Set("This table has no column a full-text index can hold.")
			return
		}
		for _, e := range ed.edits.all() {
			if e.spec.Name != name {
				continue
			}
			if e.removing {
				hint.SetError(name + " is being removed — Apply that first, then add it back with its new settings.")
				return
			}
			hint.SetError(name + " is already indexed.")
			return
		}
		spec := gosmo.FullTextIndexColumnSpec{
			Name:                 name,
			Language:             ed.langs.lcid(langRow.Selected()),
			StatisticalSemantics: semRow.Selected() == 1,
		}
		if t := typeRow.Value(); t != propertyListNone {
			spec.TypeColumn = t
		}
		switch {
		case ed.binary[name] && spec.TypeColumn == "":
			hint.SetError(name + " holds documents: choose the type column naming each row's file type (.docx, .pdf).")
			return
		case !ed.binary[name] && spec.TypeColumn != "":
			hint.SetError("A type column applies only to a varbinary or image column.")
			return
		case spec.TypeColumn == name:
			hint.SetError("A column cannot be its own type column.")
			return
		}
		lang := langRow.Value()
		if spec.Language == "" {
			lang = "(default)"
		}
		hint.Clear()
		ed.edits.add(&fullTextColEdit{spec: spec, language: lang})
		redraw(len(ed.edits.visible()) - 1)
	})
	removeBtn := widgets.NewButton("Remove", func() {
		vis := ed.edits.visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			hint.Set("Select a column in the grid to remove it.")
			return
		}
		hint.Clear()
		ed.edits.remove(vis[i])
		redraw(max(0, i-1))
	})

	gridRow := propsheet.NewGridRow(grid, 8)
	gridRow.DirtyFn = ed.edits.dirty
	gridRow.RevertFn = func() {
		ed.edits.revert()
		redraw(0)
	}
	rows := []propsheet.Row{
		propsheet.Section("Full-text indexed columns"),
		gridRow,
		propsheet.Section("Add a column"),
		colRow, typeRow, langRow, semRow,
		propsheet.Buttons(addBtn, removeBtn),
		hint,
	}
	if len(ed.eligible) == 0 {
		rows = append(rows, propsheet.Note("This table has no char, varchar, nchar, nvarchar, text, ntext, xml, varbinary or image column."))
	}
	return append(rows, propsheet.Note(note))
}
