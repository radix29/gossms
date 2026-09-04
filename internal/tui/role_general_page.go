package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// roleWriter is the pair of writes a role's General page makes. Both
// *gosmo.DatabaseRole and *gosmo.ServerRole satisfy it, which is what lets the
// two dialogs share one apply.
//
// It is deliberately this narrow. The apply below re-fetches the role and then
// does exactly two things to it; anything wider would invite a third write into
// a function two dialogs depend on.
type roleWriter interface {
	RenameContext(ctx context.Context, newName string) error
	ChangeOwnerContext(ctx context.Context, newOwner string) error
}

// roleGeneral is one role as its General page needs it: the facts both scopes
// report identically (sys.database_principals and sys.server_principals carry
// the same columns for a role), plus the four things the two scopes answer
// differently.
//
// The facts are copied into a struct rather than read off the gosmo type
// through an interface because they are struct *fields* on both — Go cannot
// reach a field through an interface or a type parameter, so a two-line
// conversion at each call site is the whole cost of sharing the page.
type roleGeneral struct {
	name        string
	owner       string
	isFixedRole bool
	id          int
	sid         []byte
	created     time.Time
	modified    time.Time
	members     int

	// builtin drives the whole read-only half of the page: a fixed role's name
	// and owner are Static rows, it gets the explanatory Note, and its page
	// returns no apply at all.
	builtin bool
	// roleType is the wording for the Role type row, already resolved for
	// builtin ("Fixed database role" / "Server role").
	roleType string
	// ownerNames are the principals the Owner picker offers — database
	// principals for a database role, server principals for a server role.
	// Unread when builtin.
	ownerNames []string
	// summary are the scope-specific rows appended after "Direct members":
	// owned schemas and explicit securables for a database role, explicit
	// permissions for a server role.
	summary []propsheet.Row
}

// roleGeneralPage builds the General page shared by Database Role Properties
// and Server Role Properties. The two differ only in where the facts come from
// and which summary rows they can count, so everything else — the builtin
// split, the Identity block, the rename-and-reown apply, and the ordering rule
// that the rename is the run's last write — lives here once.
//
// Sharing the apply is the point. Its two halves have to stay in step with
// propPage.renames and with commitRename, and as two copies they were two
// places to remember that: the owner change goes first, the rename last, and
// the boxed name is updated only after the server accepted it.
//
// load reads the role; lookup re-fetches it inside the apply, because the apply
// runs against the server as it is at OK time, not as it was when the page was
// drawn.
func roleGeneralPage(roleName *string,
	load func(ctx context.Context) (roleGeneral, error),
	lookup func(ctx context.Context) (roleWriter, error)) propPage {

	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			r, err := load(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := []propsheet.Row{propsheet.Section("Role information")}
			var nameRow *propsheet.TextRow
			var ownerRow *propsheet.SelectRow
			if r.builtin {
				rows = append(rows,
					propsheet.Static("Role name", r.name),
					propsheet.Static("Owner", r.owner),
				)
			} else {
				nameRow = propsheet.Text("Role name", r.name, 24)
				ownerRow = selectPreserving("Owner", r.ownerNames, r.owner, unknownOwnerItem)
				rows = append(rows, nameRow, ownerRow)
			}
			rows = append(rows,
				propsheet.Static("Role type", r.roleType),
				propsheet.Static("Is fixed role", boolStr(r.isFixedRole)),
				propsheet.Section("Identity"),
				propsheet.Static("Principal ID", strconv.Itoa(r.id)),
				propsheet.Static("SID", fmt.Sprintf("0x%X", r.sid)),
				propsheet.Static("Created", formatSQLDate(r.created)),
				propsheet.Static("Modified", formatSQLDate(r.modified)),
				propsheet.Section("Summary"),
				propsheet.Static("Direct members", strconv.Itoa(r.members)),
			)
			rows = append(rows, r.summary...)
			if r.builtin {
				rows = append(rows,
					propsheet.Section("Built-in behavior"),
					propsheet.Note("This is a built-in role. Its name, owner, and implicit permission set can't be changed; only membership is editable (see Members)."),
				)
			}

			f := propsheet.NewForm(rows...)

			var apply propApply
			if !r.builtin {
				apply = func(ctx context.Context) error {
					role, err := lookup(ctx)
					if err != nil {
						return err
					}
					if owner, ok := changedTo(ownerRow, unknownOwnerItem); ok {
						if err := role.ChangeOwnerContext(ctx, owner); err != nil {
							return err
						}
					}
					// Last, and only then committed to the box: every other
					// page on the dialog is addressed by this name, so it must
					// not change until the server has accepted the rename.
					if nameRow.Dirty() {
						if err := role.RenameContext(ctx, nameRow.Value()); err != nil {
							return err
						}
						commitRename(ctx, roleName, nameRow.Value())
					}
					return nil
				}
			}
			return f, apply, nil
		},
	}
}
