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
// on its Availability Group Listeners folder) — SSMS's New Availability Group
// Listener dialog.
//
// A group can have exactly one listener; a second is rejected with error 19477.
// That is checked in the prefetch rather than left to the server, because the
// answer ("you already have one, called X") is more useful than the error.

// aglistenerPrefetch records what the group already has, so the dialog can
// refuse before anything is typed.
type aglistenerPrefetch struct {
	existing string
}

// agListenerModes are the two ways a listener gets its address, in the order
// they appear on the radio row.
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

	// addrs are the static addresses to bind, one per subnet. A multi-subnet
	// listener needs every subnet's address at creation or added afterwards
	// through Listener Properties; only one of them can be reached from any
	// given subnet, which is the point.
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
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
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
	// The typed address counts even when Add Address was never pressed: a
	// single-subnet listener is the common case, and making the user press a
	// button to commit the one address they typed is a trap that surfaces as
	// "listener has neither DHCP nor a static address".
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

// addressRows builds the list of addresses already added and the two buttons
// that edit it, the same grid-plus-detail-rows idiom the other Always On pages
// use. The typed IP/mask rows are shared with the spec builder, so a
// single-address listener needs no button press at all.
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
		grid.SetData(headers, rowsFor())
	})
	removeBtn := widgets.NewButton("Remove Address", func() {
		i := grid.SelectedRow()
		if i < 0 || i >= len(d.addrs) {
			return
		}
		d.addrs = append(d.addrs[:i], d.addrs[i+1:]...)
		grid.SetData(headers, rowsFor())
	})

	gridRow := propsheet.NewGridRow(grid, 5)
	return gridRow, propsheet.Buttons(addBtn, removeBtn)
}

// agListenerIPFrom validates one typed address.
//
// The mask is what distinguishes the two address families in the statement — an
// IPv6 address takes no mask at all — so a typo'd IPv4 address left without one
// would otherwise be emitted as valid IPv6 syntax and fail on the server.
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

// agListenerSpecFrom turns the form's fields into a gosmo listener spec,
// rejecting what gosmo would only find out about from the server.
//
// added is the list built with Add Address; ipAddress/subnetMask are whatever
// is still typed in the two fields. The typed one is folded in so that the
// single-subnet case — by far the common one — needs no button press, and
// pressing Add Address for it does not then duplicate the entry, because Add
// Address clears the fields.
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

// showAGAddListenerDialog opens Add Listener for a group — the Object Explorer
// context menu's entry point on an availability group and on its Availability
// Group Listeners folder.
func (a *App) showAGAddListenerDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddListenerDialog.show(sc, agName, node)
}
