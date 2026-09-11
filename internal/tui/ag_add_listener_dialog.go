package tui

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ag_add_listener_dialog.go is "Add Listener..." on an availability group (and
// its listeners folder): SSMS's New Availability Group Listener dialog.
//
// A group allows one listener (error 19477 otherwise); the prefetch checks
// first so the dialog can name the existing one.

// aglistenerPrefetch records what the group has, so the dialog can refuse up
// front.
type aglistenerPrefetch struct {
	existing string
}

// agListenerModes are the two address modes, in radio-row order.
var agListenerModes = []string{"Static IP address", "DHCP"}

const (
	agListenerModeStatic = 0
	agListenerModeDHCP   = 1
)

// AGAddListenerDialog is the Add Listener dialog.
type AGAddListenerDialog struct {
	newObjectDialog[aglistenerPrefetch]

	agName string
	node   *explorerNode

	// addrs are static addresses, one per subnet. A multi-subnet listener needs
	// each subnet's address, at creation or later via Listener Properties.
	addrs []gosmo.AvailabilityListenerIPSpec
}

// NewAGAddListenerDialog creates the dialog and wires its callbacks.
func NewAGAddListenerDialog(app *App) *AGAddListenerDialog {
	d := &AGAddListenerDialog{}
	d.init(app, newObjectConfig[aglistenerPrefetch]{
		title:   "New Availability Group Listener",
		noun:    "Listener",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

func (d *AGAddListenerDialog) show(sc *db.ServerConn, agName string, node *explorerNode) {
	d.agName = agName
	d.node = node
	d.addrs = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("Availability group: "+agName, "Server: "+sc.Opts.Server)
}

func (d *AGAddListenerDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*aglistenerPrefetch, error) {
	ag, err := agOnPrimary(ctx, sc, d.agName)
	if err != nil {
		return nil, err
	}
	listeners, err := ag.ListenersContext(ctx)
	if err != nil {
		return nil, err
	}
	pf := &aglistenerPrefetch{}
	if len(listeners) > 0 {
		pf.existing = listeners[0].DNSName
	}
	return pf, nil
}

func (d *AGAddListenerDialog) buildPages(pf *aglistenerPrefetch) {
	sc := d.sc
	agName := d.agName

	nameRow := propsheet.Text("DNS name", "", 30)
	portRow := propsheet.Int("Port", 1433, 1, 65535, "")
	modeRow := propsheet.Radio("Address", agListenerModes, agListenerModeStatic)
	ipRow := propsheet.Text("IP address", "", 40)
	maskRow := propsheet.Text("Subnet mask", "", 20)

	addrGridRow, addrButtons := d.addressRows(ipRow, maskRow)

	rows := []propsheet.Row{
		propsheet.Section("Listener"),
		nameRow, portRow,
		propsheet.Section("Network address"),
		modeRow,
		addrGridRow,
		propsheet.Section("Add an address"),
		ipRow, maskRow, addrButtons,
		propsheet.Note("Leave the subnet mask empty for an IPv6 address. DHCP ignores both fields and takes no address list."),
		propsheet.Note("A multi-subnet listener needs one address per subnet. Addresses can also be added afterwards through the listener's Properties."),
	}
	if pf.existing != "" {
		rows = append([]propsheet.Row{
			propsheet.Note(fmt.Sprintf("This group already has a listener, %q. Remove it before adding another — a group can only have one.", pf.existing)),
		}, rows...)
	}
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return strings.TrimSpace(nameRow.Value()) }
	// The typed address counts without pressing Add Address: single-subnet is
	// the common case, and requiring the press would fail with "listener has
	// neither DHCP nor a static address".
	spec := func() (gosmo.AvailabilityListenerSpec, error) {
		return agListenerSpecFrom(d.objectName(), portRow.Value(), modeRow.Selected(),
			d.addrs, ipRow.Value(), maskRow.Value())
	}
	d.preflight = func() error {
		if pf.existing != "" {
			return fmt.Errorf("availability group %q already has a listener named %q; a group can have only one", agName, pf.existing)
		}
		_, err := spec()
		return err
	}
	d.applyFns[0] = func(ctx context.Context) error {
		spec, err := spec()
		if err != nil {
			return err
		}
		ag, err := agOnPrimary(ctx, sc, agName)
		if err != nil {
			return err
		}
		return ag.AddListenerContext(ctx, spec)
	}
}

// addressRows builds the added-addresses list and its two buttons
// (grid-plus-detail rows, as other Always On pages). The typed IP/mask rows are
// shared with the spec builder.
func (d *AGAddListenerDialog) addressRows(ipRow, maskRow *propsheet.TextRow) (propsheet.Row, propsheet.Row) {
	headers := []string{"IP address", "Subnet mask"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(d.addrs))
		for i, a := range d.addrs {
			rows[i] = []string{a.IPAddress, orDefault(a.SubnetMask, "(IPv6 — none)")}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	addBtn := widgets.NewButton("Add Address", func() {
		ip, err := agListenerIPFrom(ipRow.Value(), maskRow.Value())
		if err != nil {
			d.SetMessage(err.Error(), true)
			return
		}
		for _, a := range d.addrs {
			if strings.EqualFold(a.IPAddress, ip.IPAddress) {
				d.SetMessage(fmt.Sprintf("%s is already in the list.", ip.IPAddress), true)
				return
			}
		}
		d.addrs = append(d.addrs, ip)
		ipRow.SetValue("")
		maskRow.SetValue("")
		rows := rowsFor()
		resetGrid(grid, headers, rows, len(rows)-1)
	})
	removeBtn := widgets.NewButton("Remove Address", func() {
		i := grid.SelectedRow()
		if i < 0 || i >= len(d.addrs) {
			return
		}
		d.addrs = append(d.addrs[:i], d.addrs[i+1:]...)
		// Keep the cursor on the row that took the removed one's place, so
		// repeated removals work from one key; SetData would reset it and drop
		// dragged widths.
		rows := rowsFor()
		resetGrid(grid, headers, rows, min(i, len(rows)-1))
	})

	gridRow := propsheet.NewGridRow(grid, 5)
	return gridRow, propsheet.Buttons(addBtn, removeBtn)
}

// agListenerIPFrom validates one address. The mask distinguishes the families
// (IPv6 takes none), so a mistyped IPv4 without one would pass as IPv6 syntax
// and fail on the server.
func agListenerIPFrom(ipAddress, subnetMask string) (gosmo.AvailabilityListenerIPSpec, error) {
	var ip gosmo.AvailabilityListenerIPSpec

	ipAddress = strings.TrimSpace(ipAddress)
	subnetMask = strings.TrimSpace(subnetMask)
	addr := net.ParseIP(ipAddress)
	if addr == nil {
		return ip, fmt.Errorf("%q is not a valid IP address", ipAddress)
	}
	if addr.To4() == nil {
		if subnetMask != "" {
			return ip, fmt.Errorf("an IPv6 listener address takes no subnet mask")
		}
	} else {
		if subnetMask == "" {
			return ip, fmt.Errorf("an IPv4 listener address needs a subnet mask")
		}
		if mask := net.ParseIP(subnetMask); mask == nil || mask.To4() == nil {
			return ip, fmt.Errorf("%q is not a valid IPv4 subnet mask", subnetMask)
		}
	}
	return gosmo.AvailabilityListenerIPSpec{IPAddress: ipAddress, SubnetMask: subnetMask}, nil
}

// agListenerSpecFrom turns the form into a gosmo listener spec, rejecting what
// gosmo would only learn from the server.
//
// added is the Add Address list; ipAddress/subnetMask are still-typed fields,
// folded in so single-subnet needs no button. Add Address clears the fields, so
// nothing duplicates.
func agListenerSpecFrom(dnsName, portText string, mode int, added []gosmo.AvailabilityListenerIPSpec, ipAddress, subnetMask string) (gosmo.AvailabilityListenerSpec, error) {
	var spec gosmo.AvailabilityListenerSpec

	dnsName = strings.TrimSpace(dnsName)
	if dnsName == "" {
		return spec, fmt.Errorf("listener DNS name is required")
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port < 1 || port > 65535 {
		return spec, fmt.Errorf("listener port must be a number from 1 to 65535")
	}
	spec.DNSName = dnsName
	spec.Port = port

	if mode == agListenerModeDHCP {
		spec.DHCP = true
		return spec, nil
	}

	spec.IPAddresses = append(spec.IPAddresses, added...)
	if strings.TrimSpace(ipAddress) != "" || strings.TrimSpace(subnetMask) != "" {
		ip, err := agListenerIPFrom(ipAddress, subnetMask)
		if err != nil {
			return spec, err
		}
		for _, a := range added {
			if strings.EqualFold(a.IPAddress, ip.IPAddress) {
				return spec, fmt.Errorf("%s appears twice in the address list", ip.IPAddress)
			}
		}
		spec.IPAddresses = append(spec.IPAddresses, ip)
	}
	if len(spec.IPAddresses) == 0 {
		return spec, fmt.Errorf("a static listener needs at least one IP address")
	}
	return spec, nil
}

// showAGAddListenerDialog opens Add Listener for a group, from Object
// Explorer's menu on the group or its listeners folder.
func (a *App) showAGAddListenerDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddListenerDialog.show(sc, agName, node)
}
