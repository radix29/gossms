package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
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
	return inDB(ctx, sc, dbName, name, facetRead((*gosmo.Database).FullText, gosmo.FullText.CatalogByName))
}

func findFullTextStoplist(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.FullTextStoplist, error) {
	return inDB(ctx, sc, dbName, name, facetRead((*gosmo.Database).FullText, gosmo.FullText.StoplistByName))
}

func findSearchPropertyList(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.SearchPropertyList, error) {
	return inDB(ctx, sc, dbName, name, facetRead((*gosmo.Database).FullText, gosmo.FullText.SearchPropertyListByName))
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
// database's users, roles and application roles — AUTHORIZATION takes any of
// them for all three families.
func fullTextOwnerItems(ctx context.Context, d *gosmo.Database) ([]string, error) {
	principals, err := loadDatabasePrincipals(ctx, d)
	if err != nil {
		return nil, err
	}
	return append([]string{fullTextDefaultOwner}, principals.owners(nil)...), nil
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
				return sc.Server.DatabaseRef(dbName).FullText().CatalogRef(name).SetDefault(ctx)
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
				cat := sc.Server.DatabaseRef(dbName).FullText().CatalogRef(name)
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

// fullTextListGeneralForm is the read-only General page a stoplist and a search
// property list share; note points at the page that edits the list's contents.
func fullTextListGeneralForm(section, name, owner string, created, modified time.Time, note string) *propsheet.Form {
	return propsheet.NewForm(
		propsheet.Section(section),
		propsheet.Static("Name", name),
		propsheet.Static("Owner", owner),
		propsheet.Static("Created", formatSQLDate(created)),
		propsheet.Static("Last modified", formatSQLDate(modified)),
		propsheet.Note(note),
	)
}
