package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// xevent_session_storage.go is the Data Storage page of New Session and
// Session Properties (see xevent_session_dialog.go): the session's targets,
// and each target's parameters from the catalog, with their defaults and the
// ones the target cannot do without.
//
// On an Azure engine edition an event_file cannot be a path (Msg 40538): its
// filename is a blob URL, and the server writes and reads it with the SAS of
// the credential named after the container — a server credential on a Managed
// Instance, a database-scoped one on Azure SQL Database. The page offers those
// credentials as the containers to write to, and refuses a filename that is
// not an https:// URL before anything is sent.

var (
	xeTargetColumns = []string{"Target", "Settings"}
	xeParamColumns  = []string{"Parameter", "Value", "Default", "Type", "Required", "Description"}
)

// xeStorageEditor is the Data Storage page's state and rows.
type xeStorageEditor struct {
	h           xeHost
	cat         *xeCatalog
	m           *xeSessionModel
	sessionName func() string // for an event_file's filename, when there is one yet
	// azure is an Azure engine edition, where an event_file is a blob;
	// containers are the credential names that are container URLs there.
	azure      bool
	containers []string

	sel      int                    // m.targets index shown below, -1 for none
	params   []gosmo.XEObjectColumn // paramGrid's rows: the target's parameters
	paramSel int
	cols     map[string][]gosmo.XEObjectColumn // a target's columns, by qualified name
	colRun   latest

	tgtGrid, paramGrid *controls.DataGrid
	tgtRow             *validatedGridRow
	typeRow            *propsheet.SelectRow
	containerRow       *propsheet.SelectRow // nil off Azure
	valueRow           *propsheet.TextRow
	section            *propsheet.SectionRow
	hint, paramHint    *propsheet.HintRow
}

// newXEStorageEditor builds the page. cols holds target columns already read,
// by lower-cased qualified name; a page built off the UI goroutine must hold
// every target of m there, since reading one later is the UI goroutine's.
// azure and containers are the blob side (xeBlobContainers).
func newXEStorageEditor(h xeHost, cat *xeCatalog, m *xeSessionModel, sessionName func() string,
	cols map[string][]gosmo.XEObjectColumn, azure bool, containers []string) *xeStorageEditor {
	if cols == nil {
		cols = map[string][]gosmo.XEObjectColumn{}
	}
	s := &xeStorageEditor{h: h, cat: cat, m: m, sessionName: sessionName, sel: -1, paramSel: -1, cols: cols,
		azure: azure, containers: containers}
	if len(m.targets) > 0 {
		s.sel = 0
	}
	types := make([]string, len(cat.targets))
	for i, o := range cat.targets {
		types[i] = o.QualifiedName()
	}
	s.typeRow = propsheet.Select("Target type", types, max(slices.Index(types, "package0.event_file"), 0))
	s.typeRow.SetDirtyTracked(false)

	s.tgtGrid = controls.NewDataGrid()
	s.tgtGrid.SetCellCursor(true)
	s.tgtGrid.SetData(xeTargetColumns, s.targetRows())
	s.tgtGrid.OnSelectRow = func(row int) {
		if row >= 0 && row < len(s.m.targets) {
			s.sel = row
			s.syncParams()
		}
	}
	s.tgtRow = &validatedGridRow{GridRow: propsheet.NewGridRow(s.tgtGrid, 6), validate: s.validate}
	s.tgtRow.DirtyFn = m.targetsDirty
	s.tgtRow.RevertFn = func() {
		m.targets = cloneXETargets(m.origTargets)
		s.reset()
	}

	s.paramGrid = controls.NewDataGrid()
	s.paramGrid.SetCellCursor(true)
	s.paramGrid.OnSelectRow = func(row int) {
		s.paramSel = row
		s.syncValue()
	}
	s.paramGrid.OnActivateCell = func(row, _ int) { s.flipParam(row) }

	s.section = propsheet.Section("Target properties")
	s.valueRow = propsheet.Text("Selected parameter's value", "", 44)
	s.valueRow.SetDirtyTracked(false)
	s.valueRow.SetOnChange(func(v string) { s.setParam(strings.TrimSpace(v)) })
	s.hint = propsheet.Hint()
	s.paramHint = propsheet.Hint()
	if azure {
		items := make([]string, len(containers))
		for i, c := range containers {
			items[i] = xeContainerLabel(c)
		}
		if len(items) == 0 {
			items = []string{xeNoContainer}
		}
		s.containerRow = propsheet.Select("Blob container (credential)", items, 0)
		s.containerRow.SetDirtyTracked(false)
		s.containerRow.SetFitItems(true)
		s.containerRow.SetOnChange(func(string) { s.useContainer() })
	}

	m.listeners = append(m.listeners, s.reset)
	s.syncParams()
	return s
}

func (s *xeStorageEditor) form() *propsheet.Form {
	rows := []propsheet.Row{
		propsheet.Section("Targets"),
		s.tgtRow,
		s.typeRow,
	}
	if s.containerRow != nil {
		rows = append(rows, s.containerRow)
	}
	rows = append(rows,
		propsheet.Buttons(widgets.NewButton("Add Target", s.add), widgets.NewButton("Remove Target", s.remove)),
		s.hint,
		s.section,
		propsheet.NewGridRow(s.paramGrid, 8),
		s.valueRow,
		s.paramHint,
	)
	note := "An event_file's filename is a path on the SQL Server host — a bare name lands in the error-log directory — and the viewer reads it with Watch Live Data and View Target Data; a ring_buffer keeps the newest events in memory. Enter on a true/false parameter flips it; an empty value is the server's default. A session may have no target at all, in which case Watch Live Data offers to add one."
	if s.azure {
		note = xeAzureStorageNote
	}
	return propsheet.NewForm(append(rows, propsheet.Note(note))...)
}

// xeNoContainer is the container row's one item when no credential names a
// container.
const xeNoContainer = "(no credential names a blob container)"

// xeAzureStorageNote is the page's note on an Azure engine edition.
const xeAzureStorageNote = "Here an event_file is a blob: its filename is https://account.blob.core.windows.net/container/name.xel, " +
	"and the server writes it with the shared access signature of the credential named after the container " +
	"(CREATE CREDENTIAL [https://account.blob.core.windows.net/container] WITH IDENTITY = 'SHARED ACCESS SIGNATURE'; " +
	"a DATABASE SCOPED CREDENTIAL on Azure SQL Database). Picking a container above points the selected event_file at it. " +
	"Without a valid signature the session is created but refuses to start. A ring_buffer needs none of this. " +
	"Enter on a true/false parameter flips it; an empty value is the server's default."

// xeBlobContainers returns the blob containers an Azure event_file can be
// written to: the names of the credentials that are container URLs — the
// server's on a Managed Instance, the database's on Azure SQL Database. Off
// Azure it is nil, and a login that may not list credentials gets none: the
// URL can still be typed.
func xeBlobContainers(ctx context.Context, h xeHost) []string {
	if !serverIsAzure(h.sc) {
		return nil
	}
	var names []string
	if h.scope.db != "" {
		creds, err := h.sc.Server.DatabaseRef(h.scope.db).DatabaseScopedCredentials(ctx)
		if err != nil {
			return nil
		}
		for _, c := range creds {
			names = append(names, c.Name)
		}
	} else {
		creds, err := h.sc.Server.Credentials(ctx)
		if err != nil {
			return nil
		}
		for _, c := range creds {
			names = append(names, c.Name)
		}
	}
	var out []string
	for _, n := range names {
		if xeIsBlobURL(n) {
			out = append(out, strings.TrimRight(n, "/"))
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// xeIsBlobURL reports whether an event_file filename (or a credential name)
// is the https:// URL an Azure engine edition requires.
func xeIsBlobURL(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "https://")
}

// xeBlobFilename is the event_file filename for file in container: the
// container URL, then the file's name — its last path segment, whichever
// separator it was written with — with .xel added when missing.
func xeBlobFilename(container, file string) string {
	if i := strings.LastIndexAny(file, `/\`); i >= 0 {
		file = file[i+1:]
	}
	if !strings.EqualFold(file[max(0, len(file)-4):], ".xel") {
		file += ".xel"
	}
	return strings.TrimRight(container, "/") + "/" + file
}

// container is the container row's pick, or "" when there is none.
func (s *xeStorageEditor) container() string {
	if s.containerRow == nil {
		return ""
	}
	if i := s.containerRow.Selected(); i >= 0 && i < len(s.containers) {
		return s.containers[i]
	}
	return ""
}

// xeContainerLabel is a container URL as the picker shows it, account/container:
// the full URL outruns the control, and what tells two apart is at its end.
// A URL on another host (a sovereign cloud, an emulator) is shown whole.
func xeContainerLabel(url string) string {
	rest, ok := strings.CutPrefix(strings.ToLower(url), "https://")
	if !ok {
		return url
	}
	host, path, _ := strings.Cut(url[len("https://"):], "/")
	account, found := strings.CutSuffix(strings.ToLower(host), ".blob.core.windows.net")
	if !found || rest == "" {
		return url
	}
	return host[:len(account)] + "/" + path
}

// useContainer points the selected event_file at the container just picked,
// keeping its file name.
func (s *xeStorageEditor) useContainer() {
	t, c := s.current(), s.container()
	if t == nil || t.Name != gosmo.XETargetEventFile || c == "" {
		return
	}
	file := xeFieldValue(t.Fields, "filename")
	if file == "" && s.sessionName != nil {
		file = s.sessionName()
	}
	if file == "" {
		return
	}
	t.Fields = xeSetField(t.Fields, "filename", xeBlobFilename(c, file), true)
	s.redraw()
	s.syncValue()
}

func (s *xeStorageEditor) targetRows() [][]string {
	rows := make([][]string, len(s.m.targets))
	for i, t := range s.m.targets {
		rows[i] = []string{t.QualifiedName(), xeFieldsSummary(t.Fields)}
	}
	return rows
}

func (s *xeStorageEditor) current() *gosmo.SessionTarget {
	if s.sel < 0 || s.sel >= len(s.m.targets) {
		return nil
	}
	return &s.m.targets[s.sel]
}

// add is Add Target. A session holds at most one target of each kind.
func (s *xeStorageEditor) add() {
	o, ok := xeFind(s.cat.targets, s.typeRow.Value())
	if !ok {
		return
	}
	if s.m.targetIndex(o.QualifiedName()) >= 0 {
		s.hint.SetError("The session already has a " + o.QualifiedName() + " target.")
		return
	}
	s.hint.Clear()
	t := gosmo.SessionTarget{Package: o.Package, Name: o.Name}
	if o.Name == gosmo.XETargetEventFile && s.sessionName != nil {
		n := s.sessionName()
		// On Azure a bare name is refused; the picked container makes it a
		// blob, and with none the filename is left for the user to type.
		if s.azure {
			if c := s.container(); c != "" && n != "" {
				n = xeBlobFilename(c, n)
			} else {
				n = ""
			}
		}
		if n != "" {
			t.Fields = []gosmo.SessionField{{Name: "filename", Value: n, IsString: true}}
		}
	}
	s.m.targets = append(s.m.targets, t)
	s.sel = len(s.m.targets) - 1
	resetGrid(s.tgtGrid, xeTargetColumns, s.targetRows(), s.sel)
	s.syncParams()
}

// remove is Remove Target.
func (s *xeStorageEditor) remove() {
	if s.current() == nil {
		s.hint.SetError("Select a target to remove it.")
		return
	}
	s.hint.Clear()
	s.m.targets = slices.Delete(s.m.targets, s.sel, s.sel+1)
	s.sel = min(s.sel, len(s.m.targets)-1)
	resetGrid(s.tgtGrid, xeTargetColumns, s.targetRows(), max(s.sel, 0))
	s.syncParams()
}

// reset re-renders from a model replaced wholesale — a template, a Revert.
func (s *xeStorageEditor) reset() {
	s.sel = -1
	if len(s.m.targets) > 0 {
		s.sel = 0
	}
	s.hint.Clear()
	resetGrid(s.tgtGrid, xeTargetColumns, s.targetRows(), 0)
	s.syncParams()
}

// syncParams shows the selected target's parameters, reading its columns
// first if they are not in hand.
func (s *xeStorageEditor) syncParams() {
	t := s.current()
	s.params, s.paramSel = nil, -1
	resetGrid(s.paramGrid, xeParamColumns, nil, 0)
	s.syncValue()
	if t == nil {
		s.section.SetTitle("Target properties (no target selected)")
		s.paramHint.Clear()
		return
	}
	key := t.QualifiedName()
	s.section.SetTitle("Properties of " + key)
	if cols, ok := s.cols[strings.ToLower(key)]; ok {
		s.showParams(cols)
		return
	}
	s.paramHint.Set("Reading " + key + "'s parameters...")
	s.h.columns(&s.colRun, t.Package, t.Name, func(cols []gosmo.XEObjectColumn, err error) {
		if err != nil {
			s.paramHint.SetError("Could not read " + key + "'s parameters: " + err.Error())
			return
		}
		s.cols[strings.ToLower(key)] = cols
		if cur := s.current(); cur != nil && strings.EqualFold(cur.QualifiedName(), key) {
			s.showParams(cols)
		}
	})
}

func (s *xeStorageEditor) showParams(cols []gosmo.XEObjectColumn) {
	s.paramHint.Clear()
	s.params = nil
	for _, c := range cols {
		if c.ColumnType == gosmo.XEColumnCustomizable {
			s.params = append(s.params, c)
		}
	}
	if len(s.params) > 0 {
		s.paramSel = 0
	}
	resetGrid(s.paramGrid, xeParamColumns, s.paramRows(), 0)
	s.syncValue()
}

func (s *xeStorageEditor) paramRows() [][]string {
	t := s.current()
	rows := make([][]string, len(s.params))
	for i, c := range s.params {
		v, req := "", ""
		if t != nil {
			v = xeFieldValue(t.Fields, c.Name)
		}
		if c.Mandatory {
			req = "Yes"
		}
		rows[i] = []string{c.Name, v, c.Value, c.TypeName, req, c.Description}
	}
	return rows
}

func (s *xeStorageEditor) syncValue() {
	t := s.current()
	if t == nil || s.paramSel < 0 || s.paramSel >= len(s.params) {
		s.valueRow.SetValue("")
		s.valueRow.SetEnabled(false)
		return
	}
	s.valueRow.SetEnabled(true)
	s.valueRow.SetValue(xeFieldValue(t.Fields, s.params[s.paramSel].Name))
	s.valueRow.ShowFromStart()
}

func (s *xeStorageEditor) redraw() {
	redrawGrid(s.paramGrid, xeParamColumns, s.paramRows())
	redrawGrid(s.tgtGrid, xeTargetColumns, s.targetRows())
}

// setParam is the typed value of the selected parameter.
func (s *xeStorageEditor) setParam(v string) {
	t := s.current()
	if t == nil || s.paramSel < 0 || s.paramSel >= len(s.params) {
		return
	}
	c := s.params[s.paramSel]
	if c.TypeName == "boolean" {
		v = xeBoolText(v)
	}
	t.Fields = xeSetField(t.Fields, c.Name, v, xeIsStringType(c.TypeName))
	s.redraw()
}

// flipParam turns a true/false parameter over; back at its default, the SET
// goes.
func (s *xeStorageEditor) flipParam(row int) {
	t := s.current()
	if t == nil || row < 0 || row >= len(s.params) || s.params[row].TypeName != "boolean" {
		return
	}
	c := s.params[row]
	def := xeBoolText(c.Value)
	now := xeBoolText(orDefault(xeFieldValue(t.Fields, c.Name), def))
	next := "1"
	if now == "1" {
		next = "0"
	}
	if next == def {
		next = ""
	}
	t.Fields = xeSetField(t.Fields, c.Name, next, false)
	s.paramSel = row
	s.redraw()
	s.syncValue()
}

// origTarget is the target of t's kind the session was loaded with, or the
// zero target.
func (s *xeStorageEditor) origTarget(t gosmo.SessionTarget) gosmo.SessionTarget {
	for _, o := range s.m.origTargets {
		if strings.EqualFold(o.QualifiedName(), t.QualifiedName()) {
			return o
		}
	}
	return gosmo.SessionTarget{}
}

// validate refuses a target missing a parameter it cannot do without — the
// catalog's mandatory ones, where they have been read, and an event_file's
// filename always, which is the case that matters and is known without a
// read.
func (s *xeStorageEditor) validate() error {
	for _, t := range s.m.targets {
		var required []string
		if t.Name == gosmo.XETargetEventFile {
			required = append(required, "filename")
		}
		for _, c := range s.cols[strings.ToLower(t.QualifiedName())] {
			if c.Mandatory && c.ColumnType == gosmo.XEColumnCustomizable && !slices.Contains(required, c.Name) {
				required = append(required, c.Name)
			}
		}
		for _, name := range required {
			if strings.TrimSpace(xeFieldValue(t.Fields, name)) == "" {
				return fmt.Errorf("%s needs a value for %s", t.QualifiedName(), name)
			}
		}
		for _, f := range t.Fields {
			if err := f.Validate(); err != nil {
				return fmt.Errorf("%s: %w", t.QualifiedName(), err)
			}
		}
		// Msg 40538 otherwise, and only after the dialog has closed on it.
		// A target the session already had is left alone: a Managed
		// Instance's own system_health writes a local file, which the server
		// may do and a user may not.
		if s.azure && t.Name == gosmo.XETargetEventFile && !xeIsBlobURL(xeFieldValue(t.Fields, "filename")) &&
			!xeTargetsEqual([]gosmo.SessionTarget{t}, []gosmo.SessionTarget{s.origTarget(t)}) {
			return fmt.Errorf("%s: on Azure the filename is a blob URL, https://account.blob.core.windows.net/container/name.xel", t.QualifiedName())
		}
	}
	return nil
}

// pageXESessionStorage is Session Properties > Data Storage.
func pageXESessionStorage(h xeHost, name string) propPage {
	return propPage{
		title: "Data Storage",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			es, err := h.scope.byName(ctx, h.sc, name)
			if err != nil {
				return nil, nil, err
			}
			cat, err := loadXECatalog(ctx, h.sc)
			if err != nil {
				return nil, nil, err
			}
			// The session's own targets' parameters are read with the page:
			// the editor is built here, off the UI goroutine, where it must not
			// start a read of its own, and validate then knows every target's
			// mandatory parameters.
			cols, err := xeReadColumns(ctx, h, es.Targets, func(t gosmo.SessionTarget) (string, string) { return t.Package, t.Name })
			if err != nil {
				return nil, nil, err
			}
			m := newXESessionModel(es.Spec())
			s := newXEStorageEditor(h, cat, m, func() string { return name }, cols, serverIsAzure(h.sc), xeBlobContainers(ctx, h))
			apply := func(ctx context.Context) error {
				if !m.targetsDirty() {
					return nil
				}
				if err := s.validate(); err != nil {
					return err
				}
				targets := cloneXETargets(m.targets)
				return alterXESession(ctx, h.sc, h.scope, name, func(sp *gosmo.EventSessionSpec) { sp.Targets = targets })
			}
			return s.form(), apply, nil
		},
	}
}
