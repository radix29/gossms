package tui

import (
	"context"
	"errors"
	"slices"
	"sync"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// database_mail_props.go is Database Mail Properties: the whole Database Mail
// configuration in one paged dialog: General (status and 'Database Mail XPs'),
// Accounts (database_mail_props_accounts.go), Profiles and Profile Security
// (database_mail_props_profiles.go), and System Parameters. It replaces SSMS's
// Configure Database Mail wizard (docs/decisions.md): accounts and profiles
// have no tree nodes, so this is where they are edited.
//
// # One Apply, in dependency order
//
// The pages refer to each other's objects (a profile lists accounts, a grant
// names a profile) and either may be new in the same Apply. So the dialog is
// planned (applyPlan) and statements run in mailPhase order: creations first,
// then what refers to them, then renames, then drops. Renames follow the
// profile-account and grant writes because those address existing objects by
// the names the server still has.
//
// The msdb writes are one transaction (gosmo's InTransaction, N2): every
// sysmail_* procedure, the credential an account's password creates included,
// runs and rolls back cleanly inside one (probed on 13, 14 and 17), so a
// failure part-way stores nothing and the pages keep their edits. 'Database
// Mail XPs' is sp_configure plus RECONFIGURE, which refuses to run inside a
// user transaction (Msg 574), so it follows the COMMIT; a failure there leaves
// the msdb writes stored, and the dialog reloads every page. None of the
// configuration procedures needs the option on.
//
// # Pages that see each other's edits
//
// A new account has to be offered on the Profiles page, and a new profile on
// Profile Security, before Apply: configuring from nothing is one account, one
// profile and one grant, and three Applies would be the dialog getting in the
// way. mailModel carries the names across: the owning page publishes, the
// using page listens. Resource Governor Properties does the same (rgModel).

// The pages of Database Mail Properties, in order.
const (
	mailPageGeneral = iota
	mailPageAccounts
	mailPageProfiles
	mailPageSecurity
	mailPageParameters
)

// The phases a Database Mail Apply runs in. Profile accounts and grants use the
// names the server has now (a page shows an existing account or profile by its
// stored name until Apply), so renames run after them; drops run after
// anything that might still name the dropped object, and cascade over its links
// and grants anyway. 'Database Mail XPs' is last, outside the transaction
// (runDatabaseMailPlan).
const (
	mailPhaseCreateAccounts = iota
	mailPhaseCreateProfiles
	mailPhaseProfileAccounts
	mailPhaseGrants
	mailPhaseAlterAccounts
	mailPhaseAlterProfiles
	mailPhaseDropProfiles
	mailPhaseDropAccounts
	mailPhaseParameters
	mailPhaseXPs
)

// mailXPsOption is the sp_configure option Database Mail's procedures check.
const mailXPsOption = "Database Mail XPs"

// mailModel is what the pages of one showing share: the account names the
// Accounts page will leave in place (for Profiles) and the profile names the
// Profiles page will (for Profile Security). A list is nil until its page has
// loaded; the using page then falls back to its own read.
//
// Published names are the ones valid while profile accounts and grants are
// written: an existing object's stored name, even if the page renames it, and a
// new object's name. Removed objects are left out.
//
// Pages load on background goroutines and edit on the UI goroutine, hence the
// lock. A page publishes its initial names from its load without notifying
// (they are what the server has, which every page read itself) and notifies
// listeners only from the UI goroutine, after an edit. A listener is
// registered at the end of its page's load, once the rows it touches are built,
// keyed by page so a reload replaces it.
type mailModel struct {
	mu         sync.Mutex
	accounts   []string
	profiles   []string
	onAccounts func()
	onProfiles func()
}

func (m *mailModel) accountNames(fallback []string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accounts == nil {
		return fallback
	}
	return slices.Clone(m.accounts)
}

func (m *mailModel) profileNames(fallback []string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profiles == nil {
		return fallback
	}
	return slices.Clone(m.profiles)
}

// setAccounts publishes names, and reports whether they changed.
func (m *mailModel) setAccounts(names []string) (changed bool, listener func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed = m.accounts == nil || !slices.Equal(m.accounts, names)
	// Never nil once published, even when empty: nil is "not published",
	// and removing the last account would hand the other page its own
	// stale read again.
	m.accounts = append([]string{}, names...)
	return changed, m.onAccounts
}

func (m *mailModel) setProfiles(names []string) (changed bool, listener func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed = m.profiles == nil || !slices.Equal(m.profiles, names)
	// Never nil once published, even when empty: nil is "not published",
	// and removing the last account would hand the other page its own
	// stale read again.
	m.profiles = append([]string{}, names...)
	return changed, m.onProfiles
}

// publishAccounts is setAccounts from the UI goroutine: listeners hear of a
// change.
func (m *mailModel) publishAccounts(names []string) {
	if changed, fn := m.setAccounts(names); changed && fn != nil {
		fn()
	}
}

func (m *mailModel) publishProfiles(names []string) {
	if changed, fn := m.setProfiles(names); changed && fn != nil {
		fn()
	}
}

func (m *mailModel) listenAccounts(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAccounts = fn
}

func (m *mailModel) listenProfiles(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onProfiles = fn
}

// showDatabaseMailPropertiesFor opens Database Mail Properties on sc, at page.
func (a *App) showDatabaseMailPropertiesFor(sc *db.ServerConn, page int) {
	d := a.propDialog
	opened := d.showPlanned(sc, "", "Database Mail Properties", "Database Mail", "Server: "+sc.Opts.Server,
		func() []propPage { return databaseMailPropPages(d, sc) },
		func(ctx context.Context, plan *applyPlan) error { return runDatabaseMailPlan(ctx, sc, plan) })
	if !opened {
		return
	}
	// The node's label carries the state 'Database Mail XPs' decides.
	d.onSaved = func() { a.refreshDatabaseMailLabel(sc) }
	if page != mailPageGeneral {
		d.SelectPage(page)
	}
}

// databaseMailPropPages builds the page set. Configuration is ordinary msdb
// permission (db_owner there, or CONTROL SERVER), not sysadmin (W8); 'Database
// Mail XPs' is sp_configure, ALTER SETTINGS. The Accounts page asks ALTER ANY
// CREDENTIAL itself, for the credential fields alone.
func databaseMailPropPages(_ *PropDialog, sc *db.ServerConn) []propPage {
	model := &mailModel{}
	configure := gate.DatabaseMailConfigRights()
	return []propPage{
		withRequires(pageMailGeneral(sc), "", gate.AlterSettings),
		withRequires(pageMailAccounts(sc, model), "", configure...),
		withRequires(pageMailProfiles(sc, model), "", configure...),
		withRequires(pageMailSecurity(sc, model), "", configure...),
		withRequires(pageMailParameters(sc), "", configure...),
	}
}

// runDatabaseMailPlan carries out a Database Mail Apply: every msdb write in
// phase order as one transaction, then 'Database Mail XPs', which a
// transaction refuses.
func runDatabaseMailPlan(ctx context.Context, sc *db.ServerConn, plan *applyPlan) error {
	err := sc.Server.InTransaction(ctx, func(ctx context.Context) error {
		return plan.runPhases(ctx, func(phase int) bool { return phase != mailPhaseXPs })
	})
	if err != nil {
		return err
	}
	return plan.runPhases(ctx, func(phase int) bool { return phase == mailPhaseXPs })
}

// mailPlanFrom is applyPlanFrom for a Database Mail page, as an error when the
// page was applied without one.
func mailPlanFrom(ctx context.Context) (*applyPlan, error) {
	if plan := applyPlanFrom(ctx); plan != nil {
		return plan, nil
	}
	return nil, errNotPlanned
}

// mailNotVisibleForm is a page whose configuration read was refused: Msg 229
// on msdb's sysmail objects, which a DatabaseMailUserRole member or a login
// with no msdb rights gets (W8). The page says so instead of failing.
func mailNotVisibleForm(section string) *propsheet.Form {
	return propsheet.NewForm(
		propsheet.Section(section),
		propsheet.Note(mailConfigNotVisible+" to read or change the Database Mail configuration."),
	)
}

// -- General -----------------------------------------------------------------

// pageMailGeneral is Database Mail's state and its sp_configure switch. With
// the switch off the rest of the dialog still works (every configuration
// procedure runs with 'Database Mail XPs' at 0, W8), but nothing is sent and
// Database Mail cannot be started.
func pageMailGeneral(sc *db.ServerConn) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			opt, err := sc.Server.ConfigurationByName(ctx, mailXPsOption)
			if err != nil {
				return nil, nil, err
			}
			status := "Disabled"
			switch st, err := sc.Server.MailStatus(ctx); {
			case err == nil:
				status = st.String()
			case isRefusal(err):
				// Shorter than the Details pane's wording: a Static value
				// hard-clips at the form's edge.
				status = "Not visible — needs DatabaseMailUserRole"
			default:
				return nil, nil, err
			}
			xpsRow := propsheet.Check(mailXPsOption, opt.Value != 0)
			rows := []propsheet.Row{
				propsheet.Section("Database Mail"),
				propsheet.Static("Status", status),
				xpsRow,
			}
			if opt.Value != opt.ValueInUse {
				rows = append(rows, propsheet.Note("The configured value differs from the one in use; Apply runs RECONFIGURE."))
			}
			rows = append(rows,
				propsheet.Note("With Database Mail XPs off, accounts, profiles and parameters can still be configured, but no mail is sent and Database Mail cannot be started. Turning it on starts nothing: the first message sent does."),
				propsheet.Note("The same option is under Server Properties ▸ Advanced; changing it needs ALTER SETTINGS."),
			)
			apply := func(ctx context.Context) error {
				plan, err := mailPlanFrom(ctx)
				if err != nil {
					return err
				}
				if !xpsRow.Dirty() {
					return nil
				}
				v := int64(0)
				if xpsRow.Checked() {
					v = 1
				}
				plan.add(mailPhaseXPs, func(ctx context.Context) error {
					return sc.Server.ApplyConfiguration(ctx, []gosmo.ConfigChange{{Name: mailXPsOption, Value: v}}, gosmo.ConfigApplyOptions{})
				})
				return nil
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}

// -- System Parameters -------------------------------------------------------

// mailLoggingItems are LoggingLevel's choices, 1 to 3 in order.
var mailLoggingItems = []string{
	gosmo.MailLoggingNormal.String(), gosmo.MailLoggingExtended.String(), gosmo.MailLoggingVerbose.String(),
}

// pageMailParameters is sysmail_configuration's seven values.
func pageMailParameters(sc *db.ServerConn) propPage {
	return propPage{
		title: "System Parameters",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			cfg, err := sc.Server.MailConfiguration(ctx)
			if isRefusal(err) {
				return mailNotVisibleForm("System parameters"), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			const maxInt = 2147483647
			retries := propsheet.Int("Account retry attempts", int64(cfg.AccountRetryAttempts), 0, maxInt, "")
			delay := propsheet.Int("Account retry delay", int64(cfg.AccountRetryDelay), 0, maxInt, "sec")
			maxSize := propsheet.Int("Maximum file size", int64(cfg.MaxFileSize), 1, maxInt, "bytes")
			prohibited := propsheet.Text("Prohibited extensions", cfg.ProhibitedExtensions, 40)
			lifetime := propsheet.Int("Exe minimum lifetime", int64(cfg.DatabaseMailExeMinimumLifeTime), 0, maxInt, "sec")
			encoding := propsheet.Text("Default attachment encoding", cfg.DefaultAttachmentEncoding, 16)
			items, level := preservingItems(mailLoggingItems, cfg.LoggingLevel.String())
			logging := propsheet.Select("Logging level", items, level)

			rows := []propsheet.Row{
				propsheet.Section("System parameters"),
				retries, delay, maxSize, prohibited, lifetime, encoding, logging,
				propsheet.Note("Retry attempts and delay are per account: a message tries each account of its profile in turn, and the whole list this many times. Prohibited extensions is a comma-separated list. The executable stays running this long after the queue empties."),
				propsheet.Note("Logging level: Normal logs errors only, Extended adds warnings and information, Verbose adds success messages."),
			}
			apply := func(ctx context.Context) error {
				plan, err := mailPlanFrom(ctx)
				if err != nil {
					return err
				}
				var o gosmo.MailConfigurationOptions
				intOpt := func(row *propsheet.TextRow) (*int, error) {
					if !row.Dirty() {
						return nil, nil
					}
					n, err := row.IntValue()
					if err != nil {
						return nil, errors.New(row.Label() + ": " + err.Error())
					}
					return new(int(n)), nil
				}
				if o.AccountRetryAttempts, err = intOpt(retries); err != nil {
					return err
				}
				if o.AccountRetryDelay, err = intOpt(delay); err != nil {
					return err
				}
				if o.MaxFileSize, err = intOpt(maxSize); err != nil {
					return err
				}
				if o.DatabaseMailExeMinimumLifeTime, err = intOpt(lifetime); err != nil {
					return err
				}
				if prohibited.Dirty() {
					o.ProhibitedExtensions = new(prohibited.Value())
				}
				if encoding.Dirty() {
					o.DefaultAttachmentEncoding = new(encoding.Value())
				}
				if logging.Dirty() {
					// An unknown stored level is listed after the three, and is
					// not one to write back.
					i := logging.Selected()
					if i < 0 || i >= len(mailLoggingItems) {
						//lint:ignore ST1005 the capital is the "Logging level" field's label
						return errors.New("Logging level: choose Normal, Extended or Verbose")
					}
					o.LoggingLevel = new(gosmo.MailLoggingLevel(i + 1))
				}
				plan.add(mailPhaseParameters, func(ctx context.Context) error {
					return sc.Server.SetMailConfiguration(ctx, o)
				})
				return nil
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}
