package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// database_mail_props_profiles.go is Database Mail Properties ▸ Profiles (a
// profile and its accounts in failover order) and ▸ Profile Security (which
// msdb principals may send through which profile, and each one's default).
// The two use names published by the page before them — see mailModel.

// mailManagedInstanceProfile is the profile SQL Server Agent on a Managed
// Instance sends its notifications through; no other name works there.
const mailManagedInstanceProfile = "AzureManagedInstance_dbmail_profile"

// mailProfileEdit is one profile's row: name, description and accounts as
// the server has them and as the page says now.
type mailProfileEdit struct {
	origName, name         string
	origDesc, desc         string
	origAccounts, accounts []string
	isNew, removing        bool
}

func (e *mailProfileEdit) dirty() bool {
	return e.isNew || e.removing || e.name != e.origName || e.desc != e.origDesc ||
		!slices.Equal(e.accounts, e.origAccounts)
}

// accountsIn is the profile's accounts that will exist when its accounts are
// written: one the Accounts page is about to remove (or never created) is
// left out, which the server would refuse to add and removes anyway.
func (e *mailProfileEdit) accountsIn(available []string) []string {
	return slices.DeleteFunc(slices.Clone(e.accounts), func(a string) bool { return !slices.Contains(available, a) })
}

// mailPublishedProfiles is what the Profiles page publishes to Profile
// Security — see mailModel.
func mailPublishedProfiles(edits []*mailProfileEdit) []string {
	var out []string
	for _, e := range edits {
		switch {
		case e.removing:
		case e.isNew:
			out = append(out, e.name)
		default:
			out = append(out, e.origName)
		}
	}
	return out
}

func pageMailProfiles(sc *db.ServerConn, model *mailModel) propPage {
	return propPage{
		title: "Profiles",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			profiles, err := sc.Server.MailProfiles(ctx)
			if isRefusal(err) {
				return mailNotVisibleForm("Profiles"), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			accounts, err := sc.Server.MailAccounts(ctx)
			if err != nil {
				return nil, nil, err
			}
			loadedAccounts := make([]string, len(accounts))
			for i, a := range accounts {
				loadedAccounts[i] = a.Name
			}
			// available is read on the UI goroutine only; the apply asks the
			// model itself.
			available := model.accountNames(loadedAccounts)

			loaded := make([]*mailProfileEdit, len(profiles))
			for i, p := range profiles {
				names := make([]string, len(p.Accounts))
				for j, a := range p.Accounts {
					names[j] = a.AccountName
				}
				loaded[i] = &mailProfileEdit{origName: p.Name, name: p.Name, origDesc: p.Description, desc: p.Description,
					origAccounts: names, accounts: slices.Clone(names)}
			}
			edits := slices.Clone(loaded)
			model.setProfiles(mailPublishedProfiles(edits))
			publish := func() { model.publishProfiles(mailPublishedProfiles(edits)) }

			visible := func() []*mailProfileEdit {
				out := make([]*mailProfileEdit, 0, len(edits))
				for _, e := range edits {
					if !e.removing {
						out = append(out, e)
					}
				}
				return out
			}
			accountText := func(a string) string {
				if !slices.Contains(available, a) {
					return a + " (removed)"
				}
				return a
			}
			headers := []string{"Name", "Accounts", "Description"}
			gridRows := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					accts := make([]string, len(e.accounts))
					for j, a := range e.accounts {
						accts[j] = accountText(a)
					}
					rows[i] = []string{mailEditName(e.origName, e.name, e.isNew), strings.Join(accts, ", "), e.desc}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(headers, gridRows())
			grid.SetCellCursor(true)

			nameRow := propsheet.Text("Profile name", "", 30)
			descRow := propsheet.Text("Description", "", 40)

			var current *mailProfileEdit
			acctHeaders := []string{"Priority", "Account"}
			acctRows := func() [][]string {
				if current == nil {
					return nil
				}
				rows := make([][]string, len(current.accounts))
				for i, a := range current.accounts {
					rows[i] = []string{strconv.Itoa(i + 1), accountText(a)}
				}
				return rows
			}
			acctGrid := controls.NewDataGrid()
			acctGrid.SetData(acctHeaders, nil)
			acctGrid.SetCellCursor(true)
			addable := func() []string {
				if current == nil {
					return nil
				}
				return slices.DeleteFunc(slices.Clone(available), func(a string) bool { return slices.Contains(current.accounts, a) })
			}
			addRow := propsheet.Select("Account to add", nil, 0)
			addRow.SetDirtyTracked(false)
			addRow.SetFitItems(true)

			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name, current.desc = nameRow.Value(), descRow.Value()
			}
			syncAccounts := func(row int) {
				resetGrid(acctGrid, acctHeaders, acctRows(), row)
				addRow.SetItems(addable())
			}
			syncFromSelection := func() {
				vis := visible()
				current = nil
				if i := grid.SelectedRow(); i >= 0 && i < len(vis) {
					current = vis[i]
				}
				nameRow.SetReadOnly(current == nil)
				descRow.SetReadOnly(current == nil)
				if current == nil {
					nameRow.SetValue("")
					descRow.SetValue("")
				} else {
					nameRow.SetValue(current.name)
					descRow.SetValue(current.desc)
				}
				syncAccounts(0)
			}
			reload := wireGridEditor(grid, headers, gridRows, commitCurrent, syncFromSelection)
			onSelect := grid.OnSelectRow
			grid.OnSelectRow = func(row int) {
				onSelect(row)
				publish()
			}
			reselect := func(row int) {
				current = nil
				resetGrid(grid, headers, gridRows(), row)
				syncFromSelection()
				publish()
			}
			nameRow.SetOnChange(func(string) {
				if current != nil && current.isNew {
					commitCurrent()
					redrawGrid(grid, headers, gridRows())
					publish()
				}
			})

			hint := propsheet.Hint()
			// moveAccount moves the selected account by delta in the failover
			// order.
			moveAccount := func(delta int) {
				if current == nil {
					return
				}
				i := acctGrid.SelectedRow()
				j := i + delta
				if i < 0 || i >= len(current.accounts) || j < 0 || j >= len(current.accounts) {
					return
				}
				current.accounts[i], current.accounts[j] = current.accounts[j], current.accounts[i]
				syncAccounts(j)
				redrawGrid(grid, headers, gridRows())
			}
			upBtn := widgets.NewButton("Move Up", func() { moveAccount(-1) })
			downBtn := widgets.NewButton("Move Down", func() { moveAccount(1) })
			dropAcctBtn := widgets.NewButton("Remove Account", func() {
				if current == nil {
					return
				}
				i := acctGrid.SelectedRow()
				if i < 0 || i >= len(current.accounts) {
					hint.Set("Select an account of the profile to remove it.")
					return
				}
				hint.Clear()
				current.accounts = slices.Delete(current.accounts, i, i+1)
				syncAccounts(min(i, len(current.accounts)-1))
				redrawGrid(grid, headers, gridRows())
			})
			addAcctBtn := widgets.NewButton("Add Account", func() {
				switch {
				case current == nil:
					hint.Set("Select or add a profile first.")
					return
				case addRow.Value() == "":
					hint.Set("Every account is already in this profile — add one on the Accounts page.")
					return
				}
				hint.Clear()
				current.accounts = append(current.accounts, addRow.Value())
				syncAccounts(len(current.accounts) - 1)
				redrawGrid(grid, headers, gridRows())
			})

			gridRow := propsheet.NewGridRow(grid, min(len(edits)+4, 7))
			gridRow.DirtyFn = func() bool { return slices.ContainsFunc(edits, (*mailProfileEdit).dirty) }
			gridRow.RevertFn = func() {
				edits = edits[:0]
				for _, e := range loaded {
					e.name, e.desc, e.accounts, e.removing = e.origName, e.origDesc, slices.Clone(e.origAccounts), false
					edits = append(edits, e)
				}
				current = nil
				reload()
				publish()
			}

			newName := propsheet.Text("New profile name", "", 30)
			addBtn := widgets.NewButton("Add", func() {
				commitCurrent()
				name := newName.Value()
				if name == "" {
					hint.Set("Type a name for the new profile first.")
					return
				}
				if pendingNameTaken(serverCollation(sc), visible(), func(e *mailProfileEdit) string { return e.name }, name) {
					hint.Set("A profile named " + name + " is already listed.")
					return
				}
				hint.Set("Add the accounts " + name + " sends through below, first choice first.")
				edits = append(edits, &mailProfileEdit{name: name, isNew: true})
				newName.SetValue("")
				reselect(len(visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				commitCurrent()
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					hint.Set("Select a profile in the grid above to remove it.")
					return
				}
				e := vis[i]
				if e.isNew {
					edits = slices.DeleteFunc(edits, func(x *mailProfileEdit) bool { return x == e })
					hint.Clear()
				} else {
					e.removing = true
					hint.Set(e.origName + " is deleted on Apply with its grants; its queued mail is marked failed.")
				}
				reselect(min(i, len(visible())-1))
			})

			// The Accounts page's edits change what may be added, and mark
			// an account it removes wherever a profile lists it.
			model.listenAccounts(func() {
				available = model.accountNames(loadedAccounts)
				i := acctGrid.SelectedRow()
				redrawGrid(grid, headers, gridRows())
				syncAccounts(max(i, 0))
			})

			rows := []propsheet.Row{
				propsheet.Section("Profiles"),
				gridRow,
				propsheet.Section("Selected profile"),
				nameRow, descRow,
				propsheet.Section("SMTP accounts, in failover order"),
				propsheet.NewGridRow(acctGrid, 5),
				propsheet.Buttons(upBtn, downBtn, dropAcctBtn),
				addRow,
				propsheet.Buttons(addAcctBtn),
				propsheet.Section("Add or remove"),
				newName,
				propsheet.Buttons(addBtn, removeBtn),
				hint,
				propsheet.Note("A message goes through the first account; the next is tried only when one fails. Accounts added on the Accounts page are offered here at once; a profile added here is offered on Profile Security at once. A renamed profile or account keeps its old name on the other pages until Apply."),
			}
			if info := sc.Server.Info(); info != nil && info.EngineEdition == int(gosmo.EngineAzureManagedInst) {
				rows = append(rows, propsheet.Note("Managed Instance: SQL Server Agent sends its notifications only through a profile named "+mailManagedInstanceProfile+"."))
			}

			apply := func(ctx context.Context) error {
				plan, err := mailPlanFrom(ctx)
				if err != nil {
					return err
				}
				avail := model.accountNames(loadedAccounts)
				var names []string
				for _, e := range edits {
					if e.removing {
						continue
					}
					if slices.Contains(names, e.name) {
						return fmt.Errorf("two profiles are named %s", e.name)
					}
					names = append(names, e.name)
				}
				for _, e := range edits {
					name, accts := e.origName, e.accountsIn(avail)
					switch {
					case e.isNew:
						name, desc := e.name, e.desc
						plan.add(mailPhaseCreateProfiles, func(ctx context.Context) error {
							_, err := sc.Server.CreateMailProfile(ctx, gosmo.CreateMailProfileRequest{Name: name, Description: desc})
							return err
						})
						if len(accts) > 0 {
							plan.add(mailPhaseProfileAccounts, func(ctx context.Context) error {
								return sc.Server.MailProfileRef(name).SetAccounts(ctx, accts)
							})
						}
					case e.removing:
						plan.add(mailPhaseDropProfiles, func(ctx context.Context) error {
							return sc.Server.MailProfileRef(name).Drop(ctx)
						})
					default:
						if !slices.Equal(accts, e.origAccounts) {
							plan.add(mailPhaseProfileAccounts, func(ctx context.Context) error {
								return sc.Server.MailProfileRef(name).SetAccounts(ctx, accts)
							})
						}
						var o gosmo.MailProfileOptions
						if e.name != e.origName {
							o.Name = new(e.name)
						}
						if e.desc != e.origDesc {
							o.Description = new(e.desc)
						}
						if o.Name != nil || o.Description != nil {
							plan.add(mailPhaseAlterProfiles, func(ctx context.Context) error {
								return sc.Server.MailProfileRef(name).Alter(ctx, o)
							})
						}
					}
				}
				return nil
			}
			form := propsheet.NewForm(rows...)
			form.SetCommit(commitCurrent)
			return form, apply, nil
		},
	}
}

// -- Profile Security --------------------------------------------------------

// mailGrant is one principal's access to one profile.
type mailGrant struct{ granted, isDefault bool }

// mailGrants is every principal's access, by principal then profile. A
// missing entry is no access.
type mailGrants map[string]map[string]mailGrant

func (g mailGrants) get(principal, profile string) mailGrant { return g[principal][profile] }

func (g mailGrants) set(principal, profile string, v mailGrant) {
	if g[principal] == nil {
		g[principal] = map[string]mailGrant{}
	}
	g[principal][profile] = v
}

func (g mailGrants) clone() mailGrants {
	out := mailGrants{}
	for p, m := range g {
		for prof, v := range m {
			out.set(p, prof, v)
		}
	}
	return out
}

// differs reports whether any of profiles is granted differently in g and h.
func (g mailGrants) differs(h mailGrants, profiles []string) bool {
	for _, p := range g.principals(h) {
		for _, prof := range profiles {
			if g.get(p, prof) != h.get(p, prof) {
				return true
			}
		}
	}
	return false
}

// principals is every principal named in g or h, public first.
func (g mailGrants) principals(h mailGrants) []string {
	var out []string
	for _, m := range []mailGrants{g, h} {
		for p := range m {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == gosmo.MailPublicPrincipal:
			return -1
		case b == gosmo.MailPublicPrincipal:
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}

// toggle changes principal's access to profile in the column the user
// activated, keeping what the server enforces: a default is a granted
// profile, and a principal has one default at most — the procedures clear
// the previous one themselves (W8), so the page mirrors that rather than
// refusing it.
func (g mailGrants) toggle(principal, profile string, defaultColumn bool, profiles []string) {
	v := g.get(principal, profile)
	switch {
	case !defaultColumn && v.granted:
		v = mailGrant{}
	case !defaultColumn:
		v.granted = true
	case v.isDefault:
		v.isDefault = false
	default:
		v = mailGrant{granted: true, isDefault: true}
		for _, other := range profiles {
			if o := g.get(principal, other); other != profile && o.isDefault {
				o.isDefault = false
				g.set(principal, other, o)
			}
		}
	}
	g.set(principal, profile, v)
}

// mailGrantWrites is the statements turning orig into cur for profiles:
// revokes first, then grants and default changes that clear a default, then
// those that set one — so a principal's new default is set after its old
// one is cleared, and the procedure's own clearing is not undone.
func mailGrantWrites(sc *db.ServerConn, orig, cur mailGrants, profiles []string) []propApply {
	var revokes, offs, ons []propApply
	for _, p := range orig.principals(cur) {
		for _, prof := range profiles {
			o, c := orig.get(p, prof), cur.get(p, prof)
			ref := sc.Server.MailProfileRef(prof)
			switch {
			case o == c:
			case o.granted && !c.granted:
				revokes = append(revokes, func(ctx context.Context) error { return ref.Revoke(ctx, p) })
			case !o.granted:
				w := func(ctx context.Context) error { return ref.Grant(ctx, p, c.isDefault) }
				if c.isDefault {
					ons = append(ons, w)
				} else {
					offs = append(offs, w)
				}
			default:
				w := func(ctx context.Context) error { return ref.SetGrantDefault(ctx, p, c.isDefault) }
				if c.isDefault {
					ons = append(ons, w)
				} else {
					offs = append(offs, w)
				}
			}
		}
	}
	return slices.Concat(revokes, offs, ons)
}

// mailPrincipalCandidates is who Private profiles offers: msdb's users, less
// the ones a grant cannot name — guest is public's SID (W8: adding it beside
// public breaks the primary key) — and the server's own ##…## certificate
// users, which never send mail; plus anyone already granted, so a grant the
// user list does not show still has a row.
func mailPrincipalCandidates(users []*gosmo.User, granted []string) []string {
	var out []string
	for _, u := range users {
		switch strings.ToLower(u.Name) {
		case "guest", "sys", "information_schema":
			continue
		}
		if strings.HasPrefix(u.Name, "##") {
			continue
		}
		out = append(out, u.Name)
	}
	for _, p := range granted {
		if p != gosmo.MailPublicPrincipal && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func pageMailSecurity(sc *db.ServerConn, model *mailModel) propPage {
	return propPage{
		title: "Profile Security",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			profiles, err := sc.Server.MailProfiles(ctx)
			if isRefusal(err) {
				return mailNotVisibleForm("Profile security"), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			grants, err := sc.Server.MailPrincipalProfiles(ctx)
			if err != nil {
				return nil, nil, err
			}
			// A login that can read the grants can read msdb's users; a
			// refusal still leaves the granted principals to list.
			users, _ := sc.Server.DatabaseRef("msdb").Users(ctx)

			loadedProfiles := make([]string, len(profiles))
			for i, p := range profiles {
				loadedProfiles[i] = p.Name
			}
			shown := model.profileNames(loadedProfiles)
			orig := mailGrants{}
			var grantedNames []string
			for _, g := range grants {
				name := g.PrincipalName
				if g.IsPublic() {
					name = gosmo.MailPublicPrincipal
				}
				orig.set(name, g.ProfileName, mailGrant{granted: true, isDefault: g.IsDefault})
				grantedNames = append(grantedNames, name)
			}
			cur := orig.clone()
			principals := mailPrincipalCandidates(users, grantedNames)

			publicHeaders := []string{"Profile", "Public", "Default"}
			privateHeaders := []string{"Profile", "Access", "Default"}
			defaultText := func(v mailGrant) string {
				if v.isDefault {
					return "Yes"
				}
				return ""
			}
			rowsFor := func(principal string) [][]string {
				rows := make([][]string, len(shown))
				for i, prof := range shown {
					v := cur.get(principal, prof)
					rows[i] = []string{prof, yesNo(v.granted), defaultText(v)}
				}
				return rows
			}
			principalRow := propsheet.Select("Principal (msdb user)", principals, 0)
			principalRow.SetDirtyTracked(false)
			principalRow.SetFitItems(true)

			publicRows := func() [][]string { return rowsFor(gosmo.MailPublicPrincipal) }
			privateRows := func() [][]string {
				if principalRow.Value() == "" {
					return nil
				}
				return rowsFor(principalRow.Value())
			}
			// toggleIn wires a grid's Access and Default cells to principal's
			// grants.
			toggleIn := func(grid *controls.DataGrid, headers []string, principal func() string, rows func() [][]string) {
				grid.OnActivateCell = func(row, col int) {
					p := principal()
					if p == "" || row < 0 || row >= len(shown) || col < 1 || col > 2 {
						return
					}
					cur.toggle(p, shown[row], col == 2, shown)
					redrawGrid(grid, headers, rows())
				}
			}
			publicGrid := controls.NewDataGrid()
			publicGrid.SetData(publicHeaders, publicRows())
			publicGrid.SetCellCursor(true)
			toggleIn(publicGrid, publicHeaders, func() string { return gosmo.MailPublicPrincipal }, publicRows)
			privateGrid := controls.NewDataGrid()
			privateGrid.SetData(privateHeaders, privateRows())
			privateGrid.SetCellCursor(true)
			toggleIn(privateGrid, privateHeaders, principalRow.Value, privateRows)
			principalRow.SetOnChange(func(string) { resetGrid(privateGrid, privateHeaders, privateRows(), 0) })

			publicRow := propsheet.NewGridRow(publicGrid, min(len(shown)+3, 7))
			publicRow.DirtyFn = func() bool { return cur.differs(orig, shown) }
			publicRow.RevertFn = func() {
				cur = orig.clone()
				redrawGrid(publicGrid, publicHeaders, publicRows())
				redrawGrid(privateGrid, privateHeaders, privateRows())
			}
			// A profile added or removed on the Profiles page joins or leaves
			// both grids; its grants here are kept, and written only for a
			// profile that will exist.
			model.listenProfiles(func() {
				shown = model.profileNames(loadedProfiles)
				resetGrid(publicGrid, publicHeaders, publicRows(), max(publicGrid.SelectedRow(), 0))
				resetGrid(privateGrid, privateHeaders, privateRows(), max(privateGrid.SelectedRow(), 0))
			})

			rows := []propsheet.Row{
				propsheet.Section("Public profiles"),
				publicRow,
				propsheet.Note("A public profile can be used by every msdb user allowed to send mail. The public default is used by anyone with no default of their own."),
				propsheet.Section("Private profiles"),
				principalRow,
				propsheet.NewGridRow(privateGrid, min(len(shown)+3, 7)),
				propsheet.Note("Activate an Access or Default cell (Enter or Space) to change it. One default per principal: choosing one clears the other. A principal is an msdb database user, not a login, and it needs DatabaseMailUserRole in msdb to send at all."),
			}
			if len(principals) == 0 {
				rows = append(rows, propsheet.Note("msdb has no users to grant a private profile to. Map a login into msdb (Security ▸ Logins ▸ User Mapping) first."))
			}

			apply := func(ctx context.Context) error {
				plan, err := mailPlanFrom(ctx)
				if err != nil {
					return err
				}
				for _, w := range mailGrantWrites(sc, orig, cur, model.profileNames(loadedProfiles)) {
					plan.add(mailPhaseGrants, w)
				}
				return nil
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}
