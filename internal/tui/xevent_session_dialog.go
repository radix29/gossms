package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// xevent_session_dialog.go is New Session and Session Properties for an
// Extended Events session. Both are four pages, one file each: General
// (xevent_session_general.go), Events (xevent_session_events.go), Data
// Storage (xevent_session_storage.go) and Advanced
// (xevent_session_advanced.go). This file holds what they share —
// the catalog they offer, the edited definition, the async column reads — and
// the two dialogs' assembly.
//
// New Session is a newObjectDialog: its pages share one xeSessionModel, and
// the General page's apply writes the whole CREATE EVENT SESSION from it, the
// statement taking every event, target and option at once.
//
// Session Properties is a PropDialog whose pages load and apply on their own.
// gosmo's Alter takes a whole definition and writes only the difference, so
// each page reads the session afresh, replaces its own part — the events, the
// targets, the options — and hands the result to Alter: the parts it did not
// touch diff to nothing. Pages are therefore independent, and Script Changes
// shows each page's ALTER statements as SSMS's Script button does.

// xeCatalog is what a session editor offers: the server's non-private events,
// global fields (actions), predicate sources and targets, and the maps the
// filter builder needs to tell a map-typed field from a plain one. gosmo caches each
// list per Server, so only the first editor opened on a connection pays for
// the ~0.4 s event read.
type xeCatalog struct {
	events, actions, predSources, targets, maps []gosmo.XEObject
}

// isMap reports whether an XE type is one of the catalog's maps — an integer
// key with a text, compared by its key in a predicate.
func (c *xeCatalog) isMap(typeName string) bool {
	return slices.ContainsFunc(c.maps, func(o gosmo.XEObject) bool { return o.Name == typeName })
}

func loadXECatalog(ctx context.Context, sc *db.ServerConn) (*xeCatalog, error) {
	c := &xeCatalog{}
	for _, k := range []struct {
		kind string
		dst  *[]gosmo.XEObject
	}{
		{gosmo.XEObjectEvent, &c.events},
		{gosmo.XEObjectAction, &c.actions},
		{gosmo.XEObjectPredSource, &c.predSources},
		{gosmo.XEObjectTarget, &c.targets},
		{gosmo.XEObjectMap, &c.maps},
	} {
		objs, err := sc.Server.XEObjects(ctx, k.kind)
		if err != nil {
			return nil, err
		}
		*k.dst = objs
	}
	return c, nil
}

// xeFind returns the object of list named qualified ("package.name"), matched
// as the catalog's collation usually matches, case aside.
func xeFind(list []gosmo.XEObject, qualified string) (gosmo.XEObject, bool) {
	i := slices.IndexFunc(list, func(o gosmo.XEObject) bool { return strings.EqualFold(o.QualifiedName(), qualified) })
	if i < 0 {
		return gosmo.XEObject{}, false
	}
	return list[i], true
}

// xeIsStringType reports whether an XE type is written as N'…' — in a
// predicate, and in a SET on an event or a target — rather than as a number
// in parentheses: unicode_string, ansi_string and their _ptr forms.
func xeIsStringType(typeName string) bool { return strings.Contains(typeName, "string") }

// xeBoolText normalises a boolean column's value — the catalog's default reads
// "true"/"false", a SET value 1/0 — to "1" or "0"; anything else comes back
// unchanged.
func xeBoolText(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1":
		return "1"
	case "false", "0":
		return "0"
	}
	return v
}

// xeHost is what a session page needs from the dialog showing it: the
// connection, and the dialog's own context for the reads a page starts after
// it loaded (an event's or a target's columns). ctx is read on the UI
// goroutine, where both dialogs rewrite theirs.
type xeHost struct {
	app   *App
	sc    *db.ServerConn
	scope xeScope
	ctx   func() context.Context
}

// columns reads an event's or a target's columns off the UI goroutine and
// hands them to done on it — unless run has started a newer read since, the
// selection having moved on (ARCHITECTURE.md § Latest-only loads).
func (h xeHost) columns(run *latest, pkg, name string, done func([]gosmo.XEObjectColumn, error)) {
	parent := h.ctx()
	if parent == nil {
		parent = h.sc.Context()
	}
	ctx, token := run.BeginTimeout(parent, propFetchTimeout)
	sc := h.sc
	h.app.safego("reading Extended Events columns", func() {
		cols, err := sc.Server.XEObjectColumns(ctx, pkg, name)
		h.app.postAndWake(func() {
			if run.Done(token) {
				done(cols, err)
			}
		})
	})
}

// xeReadColumns reads the columns of every event or target in list, keyed as
// the editors key them — for a Properties page, whose editor is built on the
// load goroutine.
func xeReadColumns[T any](ctx context.Context, h xeHost, list []T, name func(T) (pkg, name string)) (map[string][]gosmo.XEObjectColumn, error) {
	out := make(map[string][]gosmo.XEObjectColumn, len(list))
	for _, v := range list {
		pkg, n := name(v)
		cols, err := h.sc.Server.XEObjectColumns(ctx, pkg, n)
		if err != nil {
			return nil, err
		}
		out[strings.ToLower(pkg+"."+n)] = cols
	}
	return out, nil
}

// xeSessionModel is the part of a session's definition the Events and Data
// Storage pages edit, with the definition they were loaded from. The options
// live in the General and Advanced pages' own rows.
//
// New Session's four pages share one; Session Properties gives each page its
// own, loaded with it. listeners are the pages' redraws, run when something
// other than the page itself replaced the lists — a New Session template.
type xeSessionModel struct {
	events      []gosmo.SessionEvent
	targets     []gosmo.SessionTarget
	origEvents  []gosmo.SessionEvent
	origTargets []gosmo.SessionTarget
	listeners   []func()
}

func newXESessionModel(spec gosmo.EventSessionSpec) *xeSessionModel {
	return &xeSessionModel{
		events: cloneXEEvents(spec.Events), origEvents: cloneXEEvents(spec.Events),
		targets: cloneXETargets(spec.Targets), origTargets: cloneXETargets(spec.Targets),
	}
}

func (m *xeSessionModel) eventsDirty() bool  { return !xeEventsEqual(m.events, m.origEvents) }
func (m *xeSessionModel) targetsDirty() bool { return !xeTargetsEqual(m.targets, m.origTargets) }

// changed runs every page's redraw.
func (m *xeSessionModel) changed() {
	for _, fn := range m.listeners {
		fn()
	}
}

// eventIndex returns the position of the event named qualified, or -1.
func (m *xeSessionModel) eventIndex(qualified string) int {
	return slices.IndexFunc(m.events, func(e gosmo.SessionEvent) bool { return strings.EqualFold(e.QualifiedName(), qualified) })
}

// targetIndex returns the position of the target named qualified, or -1.
func (m *xeSessionModel) targetIndex(qualified string) int {
	return slices.IndexFunc(m.targets, func(t gosmo.SessionTarget) bool { return strings.EqualFold(t.QualifiedName(), qualified) })
}

// cloneXEEvents copies events deeply enough that editing one's actions or
// fields leaves the source alone — the baseline a page reverts to.
func cloneXEEvents(events []gosmo.SessionEvent) []gosmo.SessionEvent {
	out := make([]gosmo.SessionEvent, len(events))
	for i, e := range events {
		e.Actions = slices.Clone(e.Actions)
		e.Fields = slices.Clone(e.Fields)
		out[i] = e
	}
	return out
}

func cloneXETargets(targets []gosmo.SessionTarget) []gosmo.SessionTarget {
	out := make([]gosmo.SessionTarget, len(targets))
	for i, t := range targets {
		t.Fields = slices.Clone(t.Fields)
		out[i] = t
	}
	return out
}

// xeFieldsKey renders SET fields order-free, the way gosmo's Alter compares
// them: a page reordering nothing must not read as an edit.
func xeFieldsKey(fields []gosmo.SessionField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = strings.ToLower(f.Name) + "=" + f.Value
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// xeEventKey is one event's definition as Alter compares it: name, predicate,
// the set of actions and the set of fields.
func xeEventKey(e gosmo.SessionEvent) string {
	acts := make([]string, len(e.Actions))
	for i, a := range e.Actions {
		acts[i] = strings.ToLower(a)
	}
	slices.Sort(acts)
	return strings.ToLower(e.QualifiedName()) + "\x00" + e.Predicate + "\x00" + strings.Join(acts, ",") + "\x00" + xeFieldsKey(e.Fields)
}

func xeEventsEqual(a, b []gosmo.SessionEvent) bool {
	return xeKeysEqual(a, b, xeEventKey)
}

func xeTargetsEqual(a, b []gosmo.SessionTarget) bool {
	return xeKeysEqual(a, b, func(t gosmo.SessionTarget) string {
		return strings.ToLower(t.QualifiedName()) + "\x00" + xeFieldsKey(t.Fields)
	})
}

// xeKeysEqual compares two lists as sets of keys — order is not part of a
// session's definition.
func xeKeysEqual[T any](a, b []T, key func(T) string) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i], kb[i] = key(a[i]), key(b[i])
	}
	slices.Sort(ka)
	slices.Sort(kb)
	return slices.Equal(ka, kb)
}

// xeSetField sets name to value in fields, or removes it for an empty value —
// an unset field is the server's default, which is what an empty box means.
func xeSetField(fields []gosmo.SessionField, name, value string, isString bool) []gosmo.SessionField {
	i := slices.IndexFunc(fields, func(f gosmo.SessionField) bool { return strings.EqualFold(f.Name, name) })
	if value == "" {
		if i >= 0 {
			return slices.Delete(slices.Clone(fields), i, i+1)
		}
		return fields
	}
	f := gosmo.SessionField{Name: name, Value: value, IsString: isString}
	if i >= 0 {
		out := slices.Clone(fields)
		out[i] = f
		return out
	}
	return append(slices.Clone(fields), f)
}

// xeFieldValue returns the value fields sets name to, or "".
func xeFieldValue(fields []gosmo.SessionField, name string) string {
	for _, f := range fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value
		}
	}
	return ""
}

// xeFieldsSummary is a grid cell's "name=value, …" for a list of SET fields.
func xeFieldsSummary(fields []gosmo.SessionField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.Name + "=" + f.Value
	}
	return strings.Join(parts, ", ")
}

// validatedGridRow is a GridRow whose Validate checks the page's edits as a
// whole — that a session keeps an event, that every target has its mandatory
// parameters — which no single cell can. The sheet validates the rows that
// report dirty, and this is the row that reports the page's dirty state.
type validatedGridRow struct {
	*propsheet.GridRow
	validate func() error
}

func (r *validatedGridRow) Validate() error {
	if r.validate == nil {
		return nil
	}
	return r.validate()
}

// alterXESession brings the session to its current definition with edit
// applied: read it, let edit replace the page's part, and let gosmo's Alter
// write the difference. The read is the server's definition now, not the one
// the page loaded — a sibling page applied a moment earlier is part of it.
func alterXESession(ctx context.Context, sc *db.ServerConn, scope xeScope, name string, edit func(*gosmo.EventSessionSpec)) error {
	es, err := scope.byName(ctx, sc, name)
	if err != nil {
		return err
	}
	spec := es.Spec()
	edit(&spec)
	return es.Alter(ctx, spec)
}

// xeRunningAlterWarning is what a page changing options a running session
// refuses (Msg 25707) asks before Apply: gosmo stops and restarts the session
// around them, and the stop empties the in-memory targets.
const xeRunningAlterWarning = "The session is running: changing these options stops and restarts it, which discards what its ring_buffer, histogram and pair_matching targets hold."

// -- Session Properties ---------------------------------------------------------

// eventSessionPropPages builds Session Properties' page set. There is no
// rename — ALTER EVENT SESSION has none — so every page addresses the session
// by the name it opened with.
//
// Each page's right is the wide name or the 2022 granular ones for its verbs,
// as the menus gate them (gate.EventSession*). The Events page adds and drops
// events, and either granular right alone lets it do part of that, so either
// makes it editable; the server refuses the half the login lacks. The same
// holds for the targets. An option change on a running session also stops and
// starts it, which on 2022+ a login holding only the OPTION right is refused
// — also the server's to say.
//
// A database-scoped session (Azure SQL Database) has the one right for all
// four, ALTER ANY DATABASE EVENT SESSION — see xeScope.right.
func eventSessionPropPages(d *PropDialog, sc *db.ServerConn, scope xeScope, name string) []propPage {
	host := xeHost{sc: sc, scope: scope, ctx: func() context.Context { return d.ctx }}
	if d != nil {
		host.app = d.app
	}
	return []propPage{
		withRequires(pageXESessionGeneral(sc, scope, name), scope.db, scope.right(gate.EventSessionOption)),
		withRequires(pageXESessionEvents(host, name), scope.db, scope.right(gate.EventSessionEvents)),
		withRequires(pageXESessionStorage(host, name), scope.db, scope.right(gate.EventSessionTargets)),
		withRequires(pageXESessionAdvanced(sc, scope, name), scope.db, scope.right(gate.EventSessionOption)),
	}
}

// showEventSessionPropertiesFor opens Session Properties — the session node's
// menu and a Sessions folder Details row's.
func (a *App) showEventSessionPropertiesFor(sc *db.ServerConn, scope xeScope, name string) {
	a.propDialog.show(sc, scope.db, "Session Properties", "Session: "+name, scope.label(sc),
		func() []propPage { return eventSessionPropPages(a.propDialog, sc, scope, name) })
	// Data Storage adds and drops targets, which are the session node's
	// children: without this they stay as they were until a Refresh.
	a.propDialog.onSaved = func() { a.refreshXESession(sc, scope, name) }
}

// -- New Session ----------------------------------------------------------------

// nxeSessionPrefetch is what New Session reads before it opens: the names
// taken, the catalog, and whether the login may start what it creates.
type nxeSessionPrefetch struct {
	existing *nameSet
	cat      *xeCatalog
	mayStart bool
	azure    bool
	major    int
	// containers are the blob containers an event_file may write to on
	// Azure (xeBlobContainers).
	containers []string
}

func fetchNewXESessionPrefetch(ctx context.Context, sc *db.ServerConn, scope xeScope) (*nxeSessionPrefetch, error) {
	sessions, err := scope.list(ctx, sc)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(serverCollation(sc))
	for _, es := range sessions {
		existing.Add(es.Name)
	}
	cat, err := loadXECatalog(ctx, sc)
	if err != nil {
		return nil, err
	}
	return &nxeSessionPrefetch{
		existing:   existing,
		cat:        cat,
		mayStart:   gate.Allows(sc, scope.db, scope.right(gate.EventSessionStart)),
		azure:      sc.Server.Info().IsAzure(),
		major:      serverMajor(sc),
		containers: xeBlobContainers(ctx, xeHost{sc: sc, scope: scope}),
	}, nil
}

// NewXESessionDialog is New Session.
type NewXESessionDialog struct {
	newObjectDialog[nxeSessionPrefetch]
	// scope is where the session is created: the server, or an Azure SQL
	// Database's database. Set by show, before the prefetch reads it.
	scope xeScope
}

// Its pages, in order; buildPages fills forms and applyFns by these.
const (
	nxePageGeneral = iota
	nxePageEvents
	nxePageStorage
	nxePageAdvanced
)

// NewNewXESessionDialog creates the dialog and wires its callbacks.
func NewNewXESessionDialog(app *App) *NewXESessionDialog {
	d := &NewXESessionDialog{}
	d.init(app, newObjectConfig[nxeSessionPrefetch]{
		title: "New Session",
		noun:  "Event session",
		pages: []string{"General", "Events", "Data Storage", "Advanced"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nxeSessionPrefetch, error) {
			return fetchNewXESessionPrefetch(ctx, sc, d.scope)
		},
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.refreshXESessions(sc, d.scope) },
	})
	return d
}

// showNewXESessionDialog opens New Session — the Sessions folder's menu.
func (a *App) showNewXESessionDialog(sc *db.ServerConn, scope xeScope) {
	if !a.requireConn(sc) {
		return
	}
	a.newXESessionDialog.scope = scope
	a.newXESessionDialog.show(sc)
	if scope.db != "" {
		a.newXESessionDialog.SetHeader("Database: "+scope.db, "Connected: yes")
	}
}

func (d *NewXESessionDialog) buildPages(pf *nxeSessionPrefetch) {
	sc, scope := d.sc, d.scope
	host := xeHost{app: d.app, sc: sc, scope: scope, ctx: func() context.Context { return d.ctx }}
	m := newXESessionModel(gosmo.EventSessionSpec{})

	adv := newXEOptionRows(xeNewSessionDefaults(), pf.major, pf.azure)
	gen := newXENewGeneral(pf, m, adv)
	events := newXEEventsEditor(host, pf.cat, m, nil)
	storage := newXEStorageEditor(host, pf.cat, m, gen.name, nil, pf.azure, pf.containers)

	d.forms[nxePageGeneral] = gen.form()
	d.forms[nxePageEvents] = events.form()
	d.forms[nxePageStorage] = storage.form()
	d.forms[nxePageAdvanced] = adv.form(nil)

	d.objectName = gen.name
	d.preflight = func() error {
		name := gen.name()
		switch {
		case name == "":
			return fmt.Errorf("session name is required")
		case pf.existing.Has(name):
			return fmt.Errorf("an event session named %q already exists", name)
		case len(m.events) == 0:
			//lint:ignore ST1005 the capital is the "Events" page's title
			return fmt.Errorf("Events: a session needs at least one event — add one from the event library")
		}
		if err := storage.validate(); err != nil {
			//lint:ignore ST1005 the capital is the "Data Storage" page's title
			return fmt.Errorf("Data Storage: %w", err)
		}
		return adv.validate()
	}
	d.applyFns[nxePageGeneral] = func(ctx context.Context) error {
		spec := gosmo.EventSessionSpec{
			Name:    gen.name(),
			Events:  cloneXEEvents(m.events),
			Targets: cloneXETargets(m.targets),
		}
		adv.apply(&spec)
		gen.applyTo(&spec)
		if _, err := scope.create(ctx, sc, spec); err != nil {
			return err
		}
		if gen.startNow() {
			return scope.ref(sc, spec.Name).Start(ctx)
		}
		return nil
	}
	d.afterCreate = func() {
		if gen.watchNow() {
			d.app.showXEventViewerFor(sc, scope, gen.name(), "", true)
		}
	}
}
