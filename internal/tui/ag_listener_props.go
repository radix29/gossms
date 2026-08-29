package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ag_listener_props.go is Availability Group Listener Properties — SSMS's
// listener properties dialog, and the only place a listener can be changed
// after it exists.
//
// # What ALTER ... MODIFY LISTENER can and cannot do
//
// Two things, one per statement: change the port, or bind another address.
// There is no form that removes an address and no form that renames the
// listener, so an address added here is permanent for the life of the listener
// — correcting a typo'd one means removing the listener and adding it back.
// The page says so rather than offering a Remove button that would only work
// on rows not yet written.

// agListenerPropPages builds the property pages for one listener.
func agListenerPropPages(sc *db.ServerConn, agName, dnsName string) []propPage {
	return []propPage{withRequires(pageAGListenerGeneral(sc, agName, dnsName), "", rightAlterAnyAG)}
}

// agFindListener reads one listener by name through the group's primary.
func agFindListener(ctx context.Context, sc *db.ServerConn, agName, dnsName string) (*gosmo.AvailabilityGroupListener, error) {
	ag, err := agOnPrimary(ctx, sc, agName)
	if err != nil {
		return nil, err
	}
	listeners, err := ag.ListenersContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range listeners {
		if strings.EqualFold(l.DNSName, dnsName) {
			return l, nil
		}
	}
	return nil, fmt.Errorf("availability group %q has no listener named %q", agName, dnsName)
}

func pageAGListenerGeneral(sc *db.ServerConn, agName, dnsName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			listener, err := agFindListener(ctx, sc, agName, dnsName)
			if err != nil {
				return nil, nil, err
			}

			portRow := propsheet.Int("Port", int64(listener.Port), 1, 65535, "")

			// pending are addresses typed on this page and not yet written.
			// They are shown in the same grid as the existing ones, marked, so
			// the list reads as what the listener will have rather than as two
			// separate things.
			var pending []gosmo.AvailabilityListenerIPSpec

			headers := []string{"IP address", "Subnet mask", "State"}
			rowsFor := func() [][]string {
				rows := make([][]string, 0, len(listener.IPAddresses)+len(pending))
				for _, ip := range listener.IPAddresses {
					state := orDefault(titleWord(ip.State), "—")
					if ip.IsDHCP {
						state += " (DHCP)"
					}
					rows = append(rows, []string{ip.IPAddress, orDefault(ip.SubnetMask, "—"), state})
				}
				for _, ip := range pending {
					rows = append(rows, []string{ip.IPAddress, orDefault(ip.SubnetMask, "—"), "To be added"})
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(headers, rowsFor())
			grid.SetCellCursor(true)

			ipRow := propsheet.Text("IP address", "", 40)
			maskRow := propsheet.Text("Subnet mask", "", 20)

			hint := propsheet.Hint()

			gridRow := propsheet.NewGridRow(grid, 6)
			gridRow.DirtyFn = func() bool { return len(pending) > 0 }
			gridRow.RevertFn = func() {
				pending = nil
				hint.Clear()
				resetGrid(grid, headers, rowsFor(), 0)
			}
			addBtn := widgets.NewButton("Add Address", func() {
				ip, err := agListenerIPFrom(ipRow.Value(), maskRow.Value())
				if err != nil {
					hint.Set(err.Error())
					return
				}
				for _, existing := range listener.IPAddresses {
					if strings.EqualFold(existing.IPAddress, ip.IPAddress) {
						hint.Set(fmt.Sprintf("%s is already bound to this listener.", ip.IPAddress))
						return
					}
				}
				for _, p := range pending {
					if strings.EqualFold(p.IPAddress, ip.IPAddress) {
						hint.Set(fmt.Sprintf("%s is already in the list.", ip.IPAddress))
						return
					}
				}
				pending = append(pending, ip)
				ipRow.SetValue("")
				maskRow.SetValue("")
				rows := rowsFor()
				resetGrid(grid, headers, rows, len(rows)-1)
				hint.Set(fmt.Sprintf("%s will be added when you apply.", ip.IPAddress))
			})

			conformance := "Created through SQL Server."
			if !listener.IsConformant {
				conformance = "Created outside SQL Server (directly in the cluster manager). SQL Server reports its configuration but does not fully validate it."
			}

			form := propsheet.NewForm(
				propsheet.Section("Listener"),
				propsheet.Static("DNS name", listener.DNSName),
				propsheet.Static("Availability group", agName),
				portRow,
				propsheet.Note(conformance),
				propsheet.Section("Network addresses"),
				gridRow,
				propsheet.Section("Add an address"),
				ipRow, maskRow,
				propsheet.Buttons(addBtn),
				hint,
				propsheet.Note("Leave the subnet mask empty for an IPv6 address. One address per subnet."),
				propsheet.Note("An address cannot be removed and the listener cannot be renamed — ALTER AVAILABILITY GROUP has no statement for either. Remove the listener and add it back to change those."),
			)

			apply := func(ctx context.Context) error {
				ag, err := agOnPrimary(ctx, sc, agName)
				if err != nil {
					return err
				}
				// Addresses first, port last: a client that reconnects between
				// the two statements is better off finding the old port than
				// finding the new port on a listener still missing a subnet.
				for _, ip := range pending {
					if err := ag.AddListenerIPContext(ctx, dnsName, ip); err != nil {
						return err
					}
				}
				if portRow.Dirty() {
					n, err := portRow.IntValue()
					if err != nil {
						return err
					}
					if err := ag.SetListenerPortContext(ctx, dnsName, int(n)); err != nil {
						return err
					}
				}
				pending = nil
				return nil
			}
			return form, apply, nil
		},
	}
}

// showAGListenerPropertiesFor opens Listener Properties — the Object Explorer
// context menu's "Properties..." on a listener.
func (a *App) showAGListenerPropertiesFor(sc *db.ServerConn, agName, dnsName string) {
	a.propDialog.show(sc, "", "Availability Group Listener Properties",
		"Listener: "+dnsName, "Availability group: "+agName,
		func() []propPage { return agListenerPropPages(sc, agName, dnsName) })
}
