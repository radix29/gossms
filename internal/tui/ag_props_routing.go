package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ag_props_routing.go is Availability Group Properties' Read-Only Routing
// page: where a replica sends read-intent connections while it is the primary,
// and the address it answers on while it is a readable secondary.
//
// The two halves belong to different roles and are easy to confuse. The
// routing *URL* is a secondary-role property — "reach me here when I am a
// readable secondary" — while the routing *list* is a primary-role property —
// "when I am the primary, send read-intent connections to these, in this
// order". Read-only routing does nothing until both are set.
//
// SSMS builds the list with an available/selected pair of list boxes plus
// Up/Down buttons. There is no equivalent control here, so the list is edited
// as text in the same order-and-parentheses form ALTER AVAILABILITY GROUP
// itself uses — see parseRoutingListText.

// agRoutingEdit is one replica's pending read-only routing state. The list is
// held as its display text and parsed at apply, so a half-typed list is a
// validation error on the row rather than a parse failure buried in Apply.
type agRoutingEdit struct {
	name string

	url     string
	origURL string

	list     string
	origList string
}

func (e *agRoutingEdit) dirty() bool { return e.url != e.origURL || e.list != e.origList }

func pageAGReadOnlyRouting(sc *db.ServerConn, agName string) propPage {
	return propPage{
		title: "Read-Only Routing",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			ag, err := agOnPrimary(ctx, sc, agName)
			if err != nil {
				return nil, nil, err
			}
			replicas, err := ag.ReplicasContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			names := make([]string, len(replicas))
			edits := make([]*agRoutingEdit, len(replicas))
			for i, r := range replicas {
				names[i] = r.ReplicaServerName
				list, err := r.ReadOnlyRoutingListContext(ctx)
				if err != nil {
					return nil, nil, err
				}
				text := formatRoutingListText(list)
				edits[i] = &agRoutingEdit{
					name: r.ReplicaServerName,
					url:  r.ReadOnlyRoutingURL, origURL: r.ReadOnlyRoutingURL,
					list: text, origList: text,
				}
			}

			headers := []string{"Server instance", "Read-only routing URL", "Read-only routing list"}
			gridRows := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.name, e.url, e.list}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(headers, gridRows())
			grid.SetCellCursor(true)

			urlRow := propsheet.Text("Read-only routing URL", "", 44)
			listRow := propsheet.Text("Read-only routing list", "", 44)
			listRow.SetValidate(func(s string) error {
				_, err := parseRoutingListText(s, names)
				return err
			})

			selected := func() *agRoutingEdit {
				i := grid.SelectedRow()
				if i < 0 || i >= len(edits) {
					return nil
				}
				return edits[i]
			}
			var current *agRoutingEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.url = strings.TrimSpace(urlRow.Value())
				current.list = strings.TrimSpace(listRow.Value())
			}
			syncFromSelection := func() {
				current = selected()
				if current == nil {
					return
				}
				urlRow.SetValue(current.url)
				listRow.SetValue(current.list)
			}
			grid.OnSelectRow = func(int) {
				commitCurrent()
				syncFromSelection()
				grid.SetData(headers, gridRows())
			}
			syncFromSelection()

			gridRow := propsheet.NewGridRow(grid, 9)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.dirty() {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.url, e.list = e.origURL, e.origList
				}
				current = nil
				grid.SetData(headers, gridRows())
				syncFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Replicas"),
				gridRow,
				propsheet.Section("Selected replica"),
				urlRow,
				listRow,
				propsheet.Note("The URL is where this replica answers read-intent connections while it is a readable secondary, e.g. TCP://"+firstOr(names, "server")+":1433. Clearing it removes the URL."),
				propsheet.Note("The list is where this replica sends read-intent connections while it is the primary: replica names in priority order, comma separated. Parenthesise names to load-balance between them, as in \"repB, (repC, repD)\". An empty list removes routing. Read-only routing only takes effect once the target replicas have a URL and allow read-only connections."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				ag, err := agOnPrimary(ctx, sc, agName)
				if err != nil {
					return err
				}
				return applyAGRouting(ctx, ag, edits, names)
			}
			return f, apply, nil
		},
	}
}

// applyAGRouting writes every changed URL and routing list, in three phases.
//
// The order is load-bearing, and getting it wrong is a server error rather
// than a silent one: SQL Server refuses a routing list naming a replica that
// has no routing URL ("An availability replica ... specified in the read-only
// routing list ..."), so a URL being *set* has to land before any list that
// points at it. Clearing runs the other way round — a URL still referenced by
// a list cannot be removed — so cleared URLs go last, after the lists that
// referenced them have dropped the reference.
func applyAGRouting(ctx context.Context, ag *gosmo.AvailabilityGroup, edits []*agRoutingEdit, names []string) error {
	changed := false
	for _, e := range edits {
		if e.dirty() {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	replicas, err := ag.ReplicasContext(ctx)
	if err != nil {
		return err
	}
	byName := agReplicasByName(replicas)
	replicaFor := func(e *agRoutingEdit) (*gosmo.AvailabilityReplica, error) {
		r := byName[strings.ToLower(e.name)]
		if r == nil {
			return nil, agMissingReplicaErr(e.name)
		}
		return r, nil
	}

	for _, op := range planAGRoutingOps(edits) {
		r, err := replicaFor(op.edit)
		if err != nil {
			return err
		}
		if !op.isList {
			if err := r.SetReadOnlyRoutingURLContext(ctx, op.edit.url); err != nil {
				return err
			}
			continue
		}
		list, err := parseRoutingListText(op.edit.list, names)
		if err != nil {
			return fmt.Errorf("read-only routing list for %s: %w", op.edit.name, err)
		}
		if err := r.SetReadOnlyRoutingListContext(ctx, list); err != nil {
			return err
		}
	}
	return nil
}

// agRoutingOp is one write in applyAGRouting's plan: either the edit's URL or
// its routing list.
type agRoutingOp struct {
	edit   *agRoutingEdit
	isList bool
}

// planAGRoutingOps orders the pending routing writes as applyAGRouting's
// doc comment describes: URLs being set, then every list, then URLs being
// cleared. Split out from the writing so the ordering — the part a server
// error is the only other way to discover — can be tested directly.
func planAGRoutingOps(edits []*agRoutingEdit) []agRoutingOp {
	var ops []agRoutingOp
	for _, e := range edits {
		if e.url != e.origURL && e.url != "" {
			ops = append(ops, agRoutingOp{edit: e})
		}
	}
	for _, e := range edits {
		if e.list != e.origList {
			ops = append(ops, agRoutingOp{edit: e, isList: true})
		}
	}
	for _, e := range edits {
		if e.url != e.origURL && e.url == "" {
			ops = append(ops, agRoutingOp{edit: e})
		}
	}
	return ops
}

// formatRoutingListText renders a routing list as the text the page edits:
// priority order, comma separated, with load-balanced sets parenthesised.
func formatRoutingListText(list [][]string) string {
	parts := make([]string, 0, len(list))
	for _, set := range list {
		switch len(set) {
		case 0:
		case 1:
			parts = append(parts, set[0])
		default:
			parts = append(parts, "("+strings.Join(set, ", ")+")")
		}
	}
	return strings.Join(parts, ", ")
}

// parseRoutingListText is formatRoutingListText's inverse, resolving each name
// against the group's replicas so a typo is reported here rather than as a
// server error at Apply — and so the names written back are the replicas' own
// spelling rather than whatever case the user typed.
//
// A repeated replica is rejected: SQL Server takes the routing list as a set
// of priorities, and a name in two places has no meaning to give it.
func parseRoutingListText(s string, replicas []string) ([][]string, error) {
	canonical := make(map[string]string, len(replicas))
	for _, r := range replicas {
		canonical[strings.ToLower(r)] = r
	}
	seen := map[string]bool{}

	i, n := 0, len(s)
	skipSpace := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	readName := func() (string, error) {
		start := i
		for i < n && s[i] != ',' && s[i] != ')' && s[i] != '(' {
			i++
		}
		raw := strings.TrimSpace(s[start:i])
		if raw == "" {
			return "", fmt.Errorf("empty replica name")
		}
		key := strings.ToLower(raw)
		name, ok := canonical[key]
		if !ok {
			return "", fmt.Errorf("%q is not a replica of this availability group", raw)
		}
		if seen[key] {
			return "", fmt.Errorf("replica %q is listed more than once", name)
		}
		seen[key] = true
		return name, nil
	}

	var list [][]string
	for {
		skipSpace()
		if i >= n {
			break
		}
		if s[i] == '(' {
			i++
			var set []string
			for {
				skipSpace()
				if i < n && s[i] == ')' {
					i++
					break
				}
				name, err := readName()
				if err != nil {
					return nil, err
				}
				set = append(set, name)
				skipSpace()
				if i < n && s[i] == ',' {
					i++
					continue
				}
				if i < n && s[i] == ')' {
					i++
					break
				}
				return nil, fmt.Errorf("missing %q after a load-balanced set", ")")
			}
			if len(set) == 0 {
				return nil, fmt.Errorf("empty load-balanced set %q", "()")
			}
			list = append(list, set)
		} else {
			name, err := readName()
			if err != nil {
				return nil, err
			}
			list = append(list, []string{name})
		}
		skipSpace()
		if i >= n {
			break
		}
		if s[i] != ',' {
			return nil, fmt.Errorf("unexpected %q — separate entries with commas", string(s[i]))
		}
		i++
	}
	return list, nil
}

// firstOr returns the first element of ss, or def when ss is empty — for
// building an example string out of whatever replica names are to hand.
func firstOr(ss []string, def string) string {
	if len(ss) == 0 {
		return def
	}
	return ss[0]
}
