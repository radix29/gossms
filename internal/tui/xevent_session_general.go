package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// xevent_session_general.go is the General page of New Session and Session
// Properties (see xevent_session_dialog.go): the name, a New Session's
// template, when the session starts, and causality tracking.

// xeNoTemplate is the Template dropdown's first item, and xeNoTemplateNote
// what the description under it says while it is picked.
const (
	xeNoTemplate     = "(none)"
	xeNoTemplateNote = "No template: the session starts empty. A template fills in the events, targets and options on the other pages, and everything it sets can still be changed."
)

// xeNewGeneral is New Session's General page.
type xeNewGeneral struct {
	nameRow   *propsheet.TextRow
	template  *propsheet.SelectRow
	desc      *propsheet.NoteRow
	startup   *propsheet.CheckRow
	start     *propsheet.CheckRow
	watch     *propsheet.CheckRow
	causality *propsheet.CheckRow
	mayStart  bool
}

func newXENewGeneral(pf *nxeSessionPrefetch, m *xeSessionModel, adv *xeOptionRows) *xeNewGeneral {
	templates := gosmo.XESessionTemplates()
	items := []string{xeNoTemplate}
	for _, t := range templates {
		items = append(items, t.Name)
	}
	g := &xeNewGeneral{
		nameRow:   propsheet.Text("Session name", "", 40),
		template:  propsheet.Select("Template", items, 0),
		desc:      propsheet.DynamicNote(xeNoTemplateNote),
		startup:   propsheet.Check("Start at server startup", false),
		start:     propsheet.Check("Start the session after creation", false),
		watch:     propsheet.Check("Watch live data after creation", false),
		causality: propsheet.Check("Track how events are related", false),
		mayStart:  pf.mayStart,
	}
	if !pf.mayStart {
		// Offered, the box would create the session and then fail its START
		// (2022's CREATE ANY EVENT SESSION without ALTER ANY EVENT SESSION
		// ENABLE), leaving a stopped session behind a failed dialog.
		g.start.SetReadOnly(true)
		g.watch.SetReadOnly(true)
	}
	g.template.SetOnChange(func(string) {
		i := g.template.Selected() - 1
		if i < 0 || i >= len(templates) {
			g.desc.SetText(xeNoTemplateNote)
			return
		}
		spec := templates[i].Spec()
		left := xeApplyTemplate(m, spec, pf.cat)
		adv.set(spec)
		g.causality.Edit(spec.TrackCausality)
		msg := templates[i].Description
		if len(left) > 0 {
			msg += " Not on this server, left out: " + strings.Join(left, ", ") + "."
		}
		g.desc.SetText(msg)
		m.changed()
	})
	return g
}

// xeApplyTemplate replaces m's events and targets with spec's, less whatever
// the server's catalog lacks — an event or a global field an older version
// does not have would fail the whole CREATE. It returns what it left out.
func xeApplyTemplate(m *xeSessionModel, spec gosmo.EventSessionSpec, cat *xeCatalog) []string {
	var left []string
	var events []gosmo.SessionEvent
	for _, e := range cloneXEEvents(spec.Events) {
		if _, ok := xeFind(cat.events, e.QualifiedName()); !ok {
			left = append(left, e.QualifiedName())
			continue
		}
		e.Actions = slices.DeleteFunc(e.Actions, func(a string) bool {
			if _, ok := xeFind(cat.actions, a); ok {
				return false
			}
			if !slices.Contains(left, a) {
				left = append(left, a)
			}
			return true
		})
		events = append(events, e)
	}
	m.events = events
	m.targets = cloneXETargets(spec.Targets)
	return left
}

func (g *xeNewGeneral) name() string { return strings.TrimSpace(g.nameRow.Value()) }

// startNow and watchNow read the two boxes as the create will act on them:
// watching needs a running session, so the second box counts only with the
// first.
func (g *xeNewGeneral) startNow() bool { return g.mayStart && g.start.Checked() }
func (g *xeNewGeneral) watchNow() bool { return g.startNow() && g.watch.Checked() }

func (g *xeNewGeneral) applyTo(spec *gosmo.EventSessionSpec) {
	spec.StartupState = g.startup.Checked()
	spec.TrackCausality = g.causality.Checked()
}

func (g *xeNewGeneral) form() *propsheet.Form {
	startNote := "Watching live data needs the session started, so the last box counts only with the one above it. A session with no target gets the offer of one (an event_file, or a ring_buffer on Azure) when the viewer opens."
	if !g.mayStart {
		startNote = "This login may create a session but not start one (it needs ALTER ANY EVENT SESSION, or ALTER ANY EVENT SESSION ENABLE on 2022 and later), so the session is created stopped."
	}
	return propsheet.NewForm(
		propsheet.Section("Session"),
		g.nameRow,
		g.template,
		g.desc,
		propsheet.Section("Schedule"),
		g.startup,
		g.start,
		g.watch,
		propsheet.Note(startNote),
		propsheet.Section("Causality tracking"),
		g.causality,
		propsheet.Note("Causality tracking adds an activity id to every event, so the events one task fires can be followed in order across the session."),
	)
}

// pageXESessionGeneral is Session Properties > General.
func pageXESessionGeneral(sc *db.ServerConn, scope xeScope, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			es, err := scope.byName(ctx, sc, name)
			if err != nil {
				return nil, nil, err
			}
			state := "Stopped"
			if es.IsRunning {
				state = "Running"
			}
			targets := make([]string, len(es.Targets))
			for i, t := range es.Targets {
				targets[i] = t.QualifiedName()
			}
			startup := propsheet.Check("Start at server startup", es.StartupState)
			causality := propsheet.Check("Track how events are related", es.TrackCausality)
			f := propsheet.NewForm(
				propsheet.Section("Session"),
				propsheet.Static("Session name", es.Name),
				propsheet.Static("State", state),
				propsheet.Static("Events", fmt.Sprint(len(es.Events))),
				propsheet.Static("Targets", orDefault(strings.Join(targets, ", "), "(none)")),
				propsheet.Section("Schedule"),
				startup,
				propsheet.Section("Causality tracking"),
				causality,
				propsheet.Note("A session cannot be renamed. Start and Stop are on the session's menu; changing causality tracking on a running session stops and restarts it."),
			)
			running := es.IsRunning
			f.SetApplyConfirm(func() string {
				if running && causality.Dirty() {
					return xeRunningAlterWarning
				}
				return ""
			})
			apply := func(ctx context.Context) error {
				if !startup.Dirty() && !causality.Dirty() {
					return nil
				}
				return alterXESession(ctx, sc, scope, name, func(s *gosmo.EventSessionSpec) {
					s.StartupState = startup.Checked()
					s.TrackCausality = causality.Checked()
				})
			}
			return f, apply, nil
		},
	}
}
