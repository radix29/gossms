package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/propsheet"
)

// database_mail_props_accounts.go is Database Mail Properties ▸ Accounts: the
// accounts in a grid, the selected one's SMTP settings below it, and
// Add/Remove.
//
// # The password
//
// A stored password cannot be read back — the server keeps it in a server
// credential — so the Password field starts blank and blank means unchanged.
// Only an account whose authentication and user name are both untouched can
// keep it: gosmo then sends @no_credential_change = 1. Any change to either
// replaces the credential, and for Basic authentication that needs the
// password typed again (W8: sysmail_update_account_sp would otherwise store
// an empty one). The typed password reaches the server only on the statement
// that runs; Script Changes shows gosmo's PasswordPlaceholder.

// mailAuthItems are the Authentication choices, indexed by
// gosmo.MailAuthentication.
var mailAuthItems = []string{"Anonymous", "Basic", "Windows (Database Engine service credentials)"}

// mailAccountValues is an account's settings as the page edits them.
type mailAccountValues struct {
	name, description, email, display, replyTo, server string
	port, timeout                                      int
	ssl                                                bool
	auth                                               gosmo.MailAuthentication
	user                                               string
}

// mailAccountEdit is one account's row: what the server has, what the page
// says now, the password typed for it (never read, "" when untouched), and
// whether it is new or going.
type mailAccountEdit struct {
	orig, cur         mailAccountValues
	password, confirm string
	pendingState
}

// changed reports an edit to an existing account, a password typed included.
func (e *mailAccountEdit) changed() bool {
	return e.cur != e.orig || e.password != "" || e.confirm != ""
}

// credentials is the credential change for an existing account: nil keeps
// the stored one. It is an error to replace a Basic credential without a
// password, which would store an empty one.
func (e *mailAccountEdit) credentials() (*gosmo.MailCredentials, error) {
	c, o := e.cur, e.orig
	if c.auth == o.auth && (c.auth != gosmo.MailAuthBasic || c.user == o.user && e.password == "") {
		return nil, nil
	}
	if c.auth == gosmo.MailAuthBasic {
		if err := e.checkBasic(); err != nil {
			return nil, err
		}
	}
	return &gosmo.MailCredentials{Authentication: c.auth, UserName: c.user, Password: e.password}, nil
}

// checkBasic is what Basic authentication needs of the page: a user name, a
// password, and the confirmation matching it.
func (e *mailAccountEdit) checkBasic() error {
	switch {
	case e.cur.user == "":
		//lint:ignore ST1005 "Basic" is the Authentication field's option label
		return errors.New("Basic authentication needs a user name")
	case e.password == "" && !e.isNew && e.cur.user != e.orig.user:
		return errors.New("a changed user name needs the password typed again — the stored one cannot be read back")
	case e.password == "":
		//lint:ignore ST1005 "Basic" is the Authentication field's option label
		return errors.New("Basic authentication needs a password")
	case e.password != e.confirm:
		return errors.New("the password and its confirmation differ")
	}
	return nil
}

// credentialRefusal is why applying e needs ALTER ANY CREDENTIAL, "" when it does
// not. A Basic account's password is a server credential (W8, Msg 15247), and the
// sysmail procedures find it by joining sys.credentials, which shows a login
// without that right nothing (W14). Such a login is not refused by the server:
// sysmail_delete_account_sp deletes the account and leaves its credential behind,
// and sysmail_update_account_sp — @no_credential_change = 1 included — writes the
// NULL it read back as the credential id, so a display-name edit leaves the
// account its user name and no password. So an existing Basic account is
// untouchable without the right, not only its credential fields.
func (e *mailAccountEdit) credentialRefusal() string {
	basic := gosmo.MailAuthBasic
	switch {
	case e.removing:
		if e.orig.auth == basic {
			return "deleting it needs ALTER ANY CREDENTIAL, or its credential is left behind"
		}
	case !e.isNew && e.orig.auth == basic:
		if e.changed() {
			return "changing it needs ALTER ANY CREDENTIAL, or the server unlinks its password"
		}
	case e.cur.auth == basic:
		return "Basic authentication stores a server credential, which needs ALTER ANY CREDENTIAL"
	}
	return ""
}

// options is the MailAccountChanges for every setting cur differs from orig
// in.
func (e *mailAccountEdit) options() (gosmo.MailAccountChanges, error) {
	c, b := e.cur, e.orig
	var o gosmo.MailAccountChanges
	str := func(cur, base string) *string {
		if cur == base {
			return nil
		}
		return new(cur)
	}
	o.Name = str(c.name, b.name)
	o.Description = str(c.description, b.description)
	o.EmailAddress = str(c.email, b.email)
	o.DisplayName = str(c.display, b.display)
	o.ReplyToAddress = str(c.replyTo, b.replyTo)
	o.ServerName = str(c.server, b.server)
	if c.port != b.port {
		o.Port = new(c.port)
	}
	if c.timeout != b.timeout {
		o.Timeout = new(c.timeout)
	}
	if c.ssl != b.ssl {
		o.EnableSSL = new(c.ssl)
	}
	creds, err := e.credentials()
	if err != nil {
		return o, err
	}
	o.Credentials = creds
	return o, nil
}

// createRequest is the request that creates e.
func (e *mailAccountEdit) createRequest() (gosmo.CreateMailAccountRequest, error) {
	c := e.cur
	if c.auth == gosmo.MailAuthBasic {
		if err := e.checkBasic(); err != nil {
			return gosmo.CreateMailAccountRequest{}, err
		}
	}
	return gosmo.CreateMailAccountRequest{
		Name: c.name, EmailAddress: c.email, DisplayName: c.display, ReplyToAddress: c.replyTo,
		Description: c.description, ServerName: c.server, Port: c.port, EnableSSL: c.ssl, Timeout: c.timeout,
		Credentials: gosmo.MailCredentials{Authentication: c.auth, UserName: c.user, Password: e.password},
	}, nil
}

// mailAccountValuesOf is a stored account as the page edits it.
func mailAccountValuesOf(a *gosmo.MailAccount) mailAccountValues {
	return mailAccountValues{
		name: a.Name, description: a.Description, email: a.EmailAddress, display: a.DisplayName,
		replyTo: a.ReplyToAddress, server: a.ServerName, port: a.Port, timeout: a.Timeout,
		ssl: a.EnableSSL, auth: a.Authentication(), user: a.UserName,
	}
}

// mailPublishedAccounts is what the Accounts page publishes to the Profiles
// page — see mailModel.
func mailPublishedAccounts(edits []*mailAccountEdit) []string {
	var out []string
	for _, e := range edits {
		switch {
		case e.removing:
		case e.isNew:
			out = append(out, e.cur.name)
		default:
			out = append(out, e.orig.name)
		}
	}
	return out
}

func pageMailAccounts(sc *db.ServerConn, model *mailModel) propPage {
	return propPage{
		title: "Accounts",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			accounts, err := sc.Server.Mail().Accounts(ctx)
			if isRefusal(err) {
				return mailNotVisibleForm("Accounts"), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			// A Basic credential is a server credential: ALTER ANY
			// CREDENTIAL, which CONTROL SERVER implies and msdb db_owner
			// lacks (W8, Msg 15247). Asked here, for these fields alone.
			credsAllowed := gate.RightsAllow(sc.Capabilities(), func(name string) *gosmo.DatabaseCapabilities {
				return sc.DatabaseCapabilities(ctx, name)
			}, "", "", "", gate.AlterAnyCredential)

			loaded := make([]*mailAccountEdit, len(accounts))
			for i, a := range accounts {
				v := mailAccountValuesOf(a)
				loaded[i] = &mailAccountEdit{orig: v, cur: v}
			}
			edits := newPendingEdits(msdbCollation(ctx, sc), loaded,
				func(e *mailAccountEdit) string { return e.cur.name }, (*mailAccountEdit).changed,
				func(e *mailAccountEdit) { e.cur, e.password, e.confirm = e.orig, "", "" })
			model.setAccounts(mailPublishedAccounts(edits.all()))
			publish := func() { model.publishAccounts(mailPublishedAccounts(edits.all())) }
			visible := edits.visible
			headers := []string{"Name", "E-mail address", "SMTP server", "Port", "SSL", "Authentication"}

			nameRow := propsheet.Text("Account name", "", 30)
			descRow := propsheet.Text("Description", "", 40)
			emailRow := propsheet.Text("E-mail address", "", 40)
			displayRow := propsheet.Text("Display name", "", 40)
			replyRow := propsheet.Text("Reply e-mail", "", 40)
			serverRow := propsheet.Text("SMTP server", "", 40)
			portRow := propsheet.Int("Port", 25, 1, 65535, "")
			timeoutRow := propsheet.Int("SMTP timeout", 0, 0, 2147483647, "sec")
			sslRow := propsheet.Check("Secure connection (SSL)", false)
			authRow := propsheet.Radio("Authentication", mailAuthItems, 0)
			userRow := propsheet.Text("User name", "", 30)
			passwordRow := propsheet.Password("Password", 30)
			confirmRow := propsheet.Password("Confirm password", 30)
			textRows := []*propsheet.TextRow{nameRow, descRow, emailRow, displayRow, replyRow, serverRow, portRow, timeoutRow}
			credRows := []*propsheet.TextRow{userRow, passwordRow, confirmRow}

			commit := func(e *mailAccountEdit) {
				v := &e.cur
				v.name, v.description, v.email = nameRow.Value(), descRow.Value(), emailRow.Value()
				v.display, v.replyTo, v.server = displayRow.Value(), replyRow.Value(), serverRow.Value()
				if n, err := portRow.IntValue(); err == nil && portRow.Validate() == nil {
					v.port = int(n)
				}
				if n, err := timeoutRow.IntValue(); err == nil && timeoutRow.Validate() == nil {
					v.timeout = int(n)
				}
				v.ssl = sslRow.Checked()
				v.auth = gosmo.MailAuthentication(authRow.Selected())
				if credsAllowed {
					v.user = userRow.Value()
					e.password, e.confirm = passwordRow.Value(), confirmRow.Value()
				}
			}
			// credFieldsFor makes the user name and password editable only
			// where they mean something and may be written.
			credFieldsFor := func(selected bool, auth gosmo.MailAuthentication) {
				for _, row := range credRows {
					row.SetReadOnly(!selected || !credsAllowed || auth != gosmo.MailAuthBasic)
				}
			}
			show := func(e *mailAccountEdit) {
				for _, row := range textRows {
					row.SetReadOnly(e == nil)
				}
				sslRow.SetReadOnly(e == nil)
				if e == nil {
					for _, row := range append(slices.Clone(textRows), credRows...) {
						row.SetValue("")
					}
					sslRow.SetChecked(false)
					authRow.SetSelected(int(gosmo.MailAuthAnonymous))
					credFieldsFor(false, gosmo.MailAuthAnonymous)
					return
				}
				v := e.cur
				nameRow.SetValue(v.name)
				descRow.SetValue(v.description)
				emailRow.SetValue(v.email)
				displayRow.SetValue(v.display)
				replyRow.SetValue(v.replyTo)
				serverRow.SetValue(v.server)
				portRow.SetValue(strconv.Itoa(v.port))
				timeoutRow.SetValue(strconv.Itoa(v.timeout))
				sslRow.SetChecked(v.ssl)
				authRow.SetSelected(int(v.auth))
				userRow.SetValue(v.user)
				passwordRow.SetValue(e.password)
				confirmRow.SetValue(e.confirm)
				credFieldsFor(true, v.auth)
			}

			list := propsheet.NewListEditor(propsheet.ListEditorSpec[*mailAccountEdit]{
				Noun:       "account",
				NameLabel:  "New account name",
				NameWidth:  30,
				Headers:    headers,
				GridHeight: min(len(loaded)+4, 8),
				Items:      visible,
				Cells: func(e *mailAccountEdit) []string {
					v := e.cur
					return []string{mailEditName(e.orig.name, v.name, e.isNew), v.email, v.server,
						strconv.Itoa(v.port), yesNo(v.ssl), mailAuthItems[v.auth]}
				},
				Show:   show,
				Commit: commit,
				Add: func(name string) (string, bool) {
					if edits.listed(name) {
						return "An account named " + name + " is already listed.", false
					}
					v := mailAccountValues{name: name, port: 25}
					edits.add(&mailAccountEdit{orig: v, cur: v})
					return "Fill in the e-mail address and SMTP server of " + name + " below.", true
				},
				Remove: func(e *mailAccountEdit) (string, bool) {
					if !credsAllowed && !e.isNew && e.orig.auth == gosmo.MailAuthBasic {
						return "Deleting " + e.orig.name + " needs ALTER ANY CREDENTIAL (Basic authentication).", false
					}
					edits.remove(e)
					if e.isNew {
						return "", true
					}
					// The server removes it from every profile without a
					// word (W8), so this page says it instead.
					return e.orig.name + " is deleted on Apply, and leaves every profile that uses it.", true
				},
				Dirty: edits.dirty,
				// A new account's orig is its first values, not a stored row;
				// refusal leaves it no stored name.
				Validate: func() error {
					return edits.refusal("account", func(e *mailAccountEdit) string { return e.orig.name })
				},
				Revert: edits.revert,
				// The Profiles page hears of a new account's rename from a
				// selection move as well.
				Changed: publish,
			})
			authRow.SetOnChange(func(i int) { credFieldsFor(list.Current() != nil, gosmo.MailAuthentication(i)) })
			// A new account's name is what the Profiles page lists it by, so
			// a rename of one is published as typed. An existing account is
			// listed there by its stored name until Apply.
			nameRow.SetOnChange(func(string) {
				if cur := list.Current(); cur != nil && cur.isNew {
					list.Commit()
					list.Redraw()
					publish()
				}
			})

			rows := []propsheet.Row{
				propsheet.Section("Accounts"),
				list.GridRow(),
				propsheet.Section("Selected account"),
				nameRow, descRow,
				propsheet.Section("Outgoing mail (SMTP) server"),
				emailRow, displayRow, replyRow, serverRow, portRow, sslRow, timeoutRow,
				propsheet.Section("SMTP authentication"),
				authRow, userRow, passwordRow, confirmRow,
				propsheet.Note("The stored password cannot be read back: leave Password blank to keep it. Changing the authentication or the user name replaces the credential, and Basic authentication then needs the password typed again. Leading and trailing spaces are trimmed by the server. SMTP timeout 0 leaves the server's default."),
			}
			if !credsAllowed {
				rows = append(rows, propsheet.Note("User name and password are read-only: an account with Basic authentication stores its password as a server credential, which needs ALTER ANY CREDENTIAL (CONTROL SERVER implies it). Without it an existing Basic account cannot be changed or deleted at all — the server would unlink or orphan its credential. Other accounts can still be changed."))
			}
			rows = append(rows, list.AddRemoveRows()...)
			rows = append(rows,
				propsheet.Note("Removing an account also removes it from every profile that uses it; the server does not refuse it. A new account is offered on the Profiles page at once."),
			)

			apply := func(ctx context.Context) error {
				plan, err := mailPlanFrom(ctx)
				if err != nil {
					return err
				}
				for _, e := range edits.all() {
					if why := e.credentialRefusal(); !credsAllowed && why != "" {
						return fmt.Errorf("account %s: %s", e.orig.name, why)
					}
					name := e.orig.name
					switch {
					case e.isNew:
						req, err := e.createRequest()
						if err != nil {
							return fmt.Errorf("account %s: %w", e.cur.name, err)
						}
						plan.add(mailPhaseCreateAccounts, func(ctx context.Context) error {
							_, err := sc.Server.Mail().CreateAccount(ctx, req)
							return err
						})
					case e.removing:
						plan.add(mailPhaseDropAccounts, func(ctx context.Context) error {
							return sc.Server.Mail().AccountRef(name).Drop(ctx)
						})
					case e.cur != e.orig || e.password != "" || e.confirm != "":
						o, err := e.options()
						if err != nil {
							return fmt.Errorf("account %s: %w", name, err)
						}
						plan.add(mailPhaseAlterAccounts, func(ctx context.Context) error {
							return sc.Server.Mail().AccountRef(name).Alter(ctx, o)
						})
					}
				}
				return nil
			}
			return list.NewForm(rows...), apply, nil
		},
	}
}

// mailEditName is an account's or profile's name as the grids show it: a new
// one marked new, a renamed one with the name it has until Apply.
func mailEditName(orig, cur string, isNew bool) string {
	switch {
	case isNew:
		return cur + " (new)"
	case cur != orig:
		return orig + " → " + cur
	}
	return cur
}
