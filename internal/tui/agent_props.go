package tui

import (
	"context"
	"errors"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_props.go builds SQL Server Agent Properties, opened from the SQL
// Server Agent node. One page so far, Alert System's mail profile — the
// setting operator and job notification mail cannot be sent without. SSMS's
// other pages (General, Advanced, Job System, Connection, History) are not
// here yet.

// agentRootMenuItems builds the SQL Server Agent node's menu. No Start/Stop:
// that is service control, outside the SQL-only scope.
func agentRootMenuItems(a *App, sc *db.ServerConn, _ *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() { a.showAgentProperties(sc) })
}

// showAgentProperties opens SQL Server Agent Properties. Nothing it writes is
// in the tree, so plain show.
func (a *App) showAgentProperties(sc *db.ServerConn) {
	a.propDialog.show(sc, "", "SQL Server Agent Properties", agentRootLabel, "Server: "+sc.Opts.Server,
		func() []propPage { return agentPropPages(sc) })
}

// agentPropPages builds the page set.
func agentPropPages(sc *db.ServerConn) []propPage {
	return []propPage{
		withRequires(pageAgentAlertSystem(sc), "", gate.AgentPropertiesRights()...),
	}
}

// pageAgentAlertSystem is the mail profile Agent sends notifications through:
// whether it sends at all, and which Database Mail profile. The list is
// msdb's profiles; a login that may not read them (Msg 229) still sees the
// configured one, which selectPreserving keeps whether listed or not — a
// profile deleted since is still what Agent tries, and shows so.
func pageAgentAlertSystem(sc *db.ServerConn) propPage {
	return propPage{
		title: "Alert System",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			mail, err := sc.Server.AgentMailSettings(ctx)
			if errors.Is(err, gosmo.ErrAgentSettingsInMssqlConf) {
				return propsheet.NewForm(
					propsheet.Section("Mail session"),
					propsheet.Note("On Linux, SQL Server Agent reads its mail profile from mssql.conf, which no SQL connection can change. Set it on the host: mssql-conf set sqlagent.databasemailprofile <profile>, then restart SQL Server."),
				), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			var names []string
			profiles, err := sc.Server.MailProfiles(ctx)
			listed := err == nil
			switch {
			case listed:
				for _, p := range profiles {
					names = append(names, p.Name)
				}
			case !isRefusal(err):
				return nil, nil, err
			}

			enabled := propsheet.Check("Enable mail profile", mail.Enabled)
			profile := selectPreserving("Mail profile", append([]string{noneItem}, names...), mail.Profile, noneItem)
			rows := []propsheet.Row{
				propsheet.Section("Mail session"),
				enabled,
				propsheet.Static("Mail system", "Database Mail"),
				profile,
			}
			if mail.Profile != "" && listed && !hasMailProfile(profiles, mail.Profile) {
				rows = append(rows, propsheet.Note("The configured profile no longer exists in Database Mail; Agent cannot send through it."))
			}
			rows = append(rows, propsheet.Note("Agent reads these settings when it starts and when they are changed here. With no profile, or the profile unchecked, operators get no e-mail."))

			apply := func(ctx context.Context) error {
				if !enabled.Dirty() && !profile.Dirty() {
					return nil
				}
				name := preservedValue(profile, noneItem)
				if enabled.Checked() && name == "" {
					return errors.New("choose a mail profile to enable, or uncheck Enable mail profile")
				}
				var ch gosmo.AgentMailChanges
				if enabled.Dirty() {
					ch.Enabled = new(enabled.Checked())
				}
				if profile.Dirty() {
					ch.Profile = new(name)
				}
				return sc.Server.SetAgentMailSettings(ctx, ch)
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}

// hasMailProfile reports whether profiles holds one named name.
func hasMailProfile(profiles []*gosmo.MailProfile, name string) bool {
	for _, p := range profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}
