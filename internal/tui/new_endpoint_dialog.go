package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/widgets"
)

// new_endpoint_dialog.go is "New Database Mirroring Endpoint..." on the Always
// On High Availability node, the prerequisite New Availability Group needs.
//
// # Why it does not copy files
//
// Replicas with no common domain (every Linux deployment) authenticate with
// certificates, and the documented setup is BACKUP CERTIFICATE to a file on each
// host, a manual copy, then CREATE CERTIFICATE FROM FILE. gossms has no
// filesystem access to either host.
//
// Instead each instance keeps its own key pair and receives its peers' *public*
// certificates as bytes over the two connections gossms already has (gosmo's
// Certificate.Encoded and CreateCertificateRequest.FromBinary). No private key
// is read, transmitted or written, hence certificates are per-instance.
//
// Per instance: a database master key in master if none, a certificate of its
// own if none, then for each peer a login, a user, the peer's public certificate
// owned by that user, and CONNECT on the endpoint granted to that login. Every
// step is skipped when what it creates already exists, so a half-configured pair
// is completed rather than failed.

// endpointDefaultPort is the conventional database mirroring port. Nothing
// requires it, but every replica must reach every other's, and a shared default
// makes that likely.
const endpointDefaultPort = 5022

// endpointDefaultName matches Microsoft's Linux availability group walkthrough,
// so a hand-configured instance and one configured here look the same.
const endpointDefaultName = "Hadr_endpoint"

var endpointAlgorithms = []string{"AES", "RC4", "AES RC4", "RC4 AES"}

// newEndpointInstance is one instance taking part in the exchange.
type newEndpointInstance struct {
	name string

	// local marks the instance the dialog is connected to: always listed, never
	// removable.
	local bool

	// hasEndpoint records what the prefetch found, so the summary can name the
	// instances that will be left alone.
	hasEndpoint bool
	endpointURL string
}

// newEndpointPrefetch is what the dialog reads before building its page.
type newEndpointPrefetch struct {
	localName string

	// blocker is why the exchange cannot be run from this instance at all.
	blocker string

	// existing is the local endpoint, if any. An instance can have only one, so its
	// presence turns the dialog from "create" into "add peers to the one you have".
	existing *gosmo.DatabaseMirroringEndpoint
}

// NewEndpointDialog is the New Database Mirroring Endpoint dialog.
type NewEndpointDialog struct {
	newObjectDialog[newEndpointPrefetch]

	node *explorerNode

	instances []*newEndpointInstance

	certificateName func(instance string) string

	// peerServerFor resolves an instance to the gosmo.Server its half of the
	// exchange runs against: a test seam like certificateName above; the default
	// goes through db.ServerConn.Peer.
	peerServerFor func(ctx context.Context, inst *newEndpointInstance) (*gosmo.Server, error)

	// resolveInstance connects to a typed instance name and answers with what it
	// already has: a test seam like peerServerFor; the default is addInstance.
	// Asynchronous: done runs on the UI goroutine.
	resolveInstance func(name string, done func(*newEndpointInstance, error))
}

// endpointRequest is what the page held when OK, Apply or Script Changes was
// pressed. Built by preflight on the UI goroutine and captured by the apply
// step, so the pipeline's goroutine touches no dialog widgets.
type endpointRequest struct {
	name          string
	port          int
	algorithm     string
	masterKeyPass string
	// instances is the list as it stood: a copy, since Add and Remove Instance edit
	// d.instances. An instance is never edited once added, so pointers can be shared.
	instances []*newEndpointInstance
}

// endpointScriptKey carries the destination for a scripted configure's
// per-instance groups; see withEndpointScript.
type endpointScriptKey struct{}

// withEndpointScript returns ctx carrying dst: a scripted configure stores the
// groups it collected there. Owned by the run (runScript), not the dialog, so
// the pipeline's goroutine writes nothing the UI goroutine reads until the run
// completes, like gosmo.WithScript's own collector.
func withEndpointScript(ctx context.Context, dst *[]endpointScriptGroup) context.Context {
	return context.WithValue(ctx, endpointScriptKey{}, dst)
}

// NewNewEndpointDialog creates the dialog and wires its callbacks.
func NewNewEndpointDialog(app *App) *NewEndpointDialog {
	d := &NewEndpointDialog{}
	d.init(app, newObjectConfig[newEndpointPrefetch]{
		title:   "New Database Mirroring Endpoint",
		noun:    "Database mirroring endpoint",
		verb:    "configured",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, sameNodeAs(d.node)) },
	})
	d.certificateName = func(instance string) string { return endpointPrincipalBase(instance) + "_Cert" }
	d.peerServerFor = d.defaultPeerServer
	d.resolveInstance = d.addInstance
	// The shell's Script Changes emits every statement as one batch, which is wrong
	// here: the statements belong to two or more instances, and run whole against
	// this one the certificate imports fail and the GRANTs land on the wrong
	// endpoint. See runScript.
	d.OnScript = d.runScript
	return d
}

// defaultPeerServer is peerServerFor's real implementation: this connection for
// the local instance, a peer connection for every other.
func (d *NewEndpointDialog) defaultPeerServer(ctx context.Context, inst *newEndpointInstance) (*gosmo.Server, error) {
	if inst.local {
		return d.sc.Server, nil
	}
	peer, err := d.sc.Peer(ctx, inst.name)
	if err != nil {
		return nil, err
	}
	return peer.Server, nil
}

// endpointPrincipalBase is the instance name as it appears in the certificate,
// login and user names the exchange creates.
//
// A named instance reports @@SERVERNAME as HOST\INSTANCE, and the backslash
// spells a Windows principal, so CREATE LOGIN [HOST\INST_login] WITH PASSWORD is
// refused (Msg 15006). It becomes HOST$INST, as in SQL Server's own service
// accounts (MSSQL$INSTANCE).
//
// Not truncated to the host as gosmo's endpointURL does: two named instances on
// one machine would then share every principal name. A default instance has no
// backslash and is unchanged.
func endpointPrincipalBase(instance string) string {
	return strings.ReplaceAll(instance, `\`, "$")
}

func (d *NewEndpointDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.node = node
	d.instances = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("Database mirroring endpoints", "Server: "+sc.Opts.Server)
}

func (d *NewEndpointDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*newEndpointPrefetch, error) {
	pf := &newEndpointPrefetch{localName: sc.Server.Name()}

	if info := sc.Server.Info(); info != nil && !info.IsHADREnabled {
		pf.blocker = fmt.Sprintf("Always On availability groups are not enabled on %s. Enable the feature and restart the instance first — on Linux, `mssql-conf set hadr.hadrenabled 1`.", sc.Opts.Server)
		return pf, nil
	}
	ep, err := sc.Server.DatabaseMirroringEndpoint(ctx)
	if err != nil && !errors.Is(err, gosmo.ErrNotFound) {
		return nil, err
	}
	pf.existing = ep // nil when there is none
	return pf, nil
}

func (d *NewEndpointDialog) buildPages(pf *newEndpointPrefetch) {
	if pf.blocker != "" {
		d.forms[0] = propsheet.NewForm(
			propsheet.Section("Database mirroring endpoint"),
			propsheet.Note(pf.blocker),
		)
		d.preflight = func() error { return fmt.Errorf("%s", pf.blocker) }
		return
	}

	local := &newEndpointInstance{name: pf.localName, local: true}
	if pf.existing != nil {
		local.hasEndpoint = true
		local.endpointURL = pf.existing.URL()
	}
	d.instances = []*newEndpointInstance{local}

	name, port, algorithm := endpointDefaultName, endpointDefaultPort, endpointAlgorithms[0]
	if pf.existing != nil {
		name, port = pf.existing.Name, pf.existing.Port
		if pf.existing.EncryptionAlgorithm != "" {
			algorithm = strings.ToUpper(pf.existing.EncryptionAlgorithm)
		}
	}

	nameRow := propsheet.Text("Endpoint name", name, 30)
	portRow := propsheet.Int("Port", int64(port), 1, 65535, "")
	algorithmRow := propsheet.Select("Encryption algorithm", endpointAlgorithms, indexOf(endpointAlgorithms, algorithm))
	passRow := propsheet.Text("Master key password", "", 30)

	instanceRows, commitInstances := d.instanceRows()

	request := func() endpointRequest {
		req := endpointRequest{
			name:          strings.TrimSpace(nameRow.Value()),
			algorithm:     algorithmRow.Value(),
			masterKeyPass: passRow.Value(),
			instances:     slices.Clone(d.instances),
		}
		if n, err := portRow.IntValue(); err == nil {
			req.port = int(n)
		}
		return req
	}

	rows := []propsheet.Row{
		propsheet.Section("Endpoint"),
		nameRow, portRow, algorithmRow,
	}
	if pf.existing != nil {
		rows = append(rows, propsheet.Note(fmt.Sprintf(
			"%s already has an endpoint, %q on port %d. An instance can have only one, so it is left as it is — adding a peer below still exchanges certificates with it and grants the peer CONNECT.",
			d.sc.Opts.Server, pf.existing.Name, pf.existing.Port)))
	}
	rows = append(rows,
		propsheet.Section("Master key"),
		passRow,
		propsheet.Note("Used only where an instance has no database master key yet. The key is also protected by the instance's service master key, so nothing has to type this password again — keep it anyway: it is the only way back in if the service master key is ever lost."),
	)
	rows = append(rows, instanceRows...)
	d.forms[0] = propsheet.NewForm(rows...)
	d.forms[0].SetCommit(commitInstances)

	d.objectName = func() string { return strings.TrimSpace(nameRow.Value()) }
	// The request is built here on the UI goroutine and the step captures it; the
	// step runs on the pipeline's goroutine.
	var req endpointRequest
	d.preflight = func() error {
		req = request()
		return validateNewEndpoint(req.name, req.masterKeyPass, req.instances)
	}
	d.applyFns[0] = func(ctx context.Context) error {
		groups, err := d.configure(ctx, req)
		if dst, ok := ctx.Value(endpointScriptKey{}).(*[]endpointScriptGroup); ok {
			*dst = groups
		}
		return err
	}
}

// validateNewEndpoint rejects what would otherwise fail partway through the
// pipeline on an instance the user cannot see.
func validateNewEndpoint(name, masterKeyPassword string, instances []*newEndpointInstance) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if strings.TrimSpace(masterKeyPassword) == "" {
		return fmt.Errorf("a master key password is required — an instance with no database master key cannot protect a certificate's private key without one")
	}
	if len(instances) < 2 {
		return fmt.Errorf("add at least one other instance — an endpoint is only useful once another instance can authenticate to it")
	}
	return nil
}

// instanceRows builds the participating-instances grid and the field that adds
// to it.
func (d *NewEndpointDialog) instanceRows() ([]propsheet.Row, func()) {
	headers := []string{"Instance", "Role", "Existing endpoint"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(d.instances))
		for i, inst := range d.instances {
			role := "Peer"
			if inst.local {
				role = "This connection"
			}
			existing := "None — will be created"
			if inst.hasEndpoint {
				existing = inst.endpointURL + " — left as it is"
			}
			rows[i] = []string{inst.name, role, existing}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	addNameRow := propsheet.Text("Instance name", "", 30)
	hint := propsheet.Hint()

	addBtn := widgets.NewButton("Add Instance", func() {
		name := strings.TrimSpace(addNameRow.Value())
		if name == "" {
			hint.Set("Type an instance name first.")
			return
		}
		for _, inst := range d.instances {
			if strings.EqualFold(inst.name, name) {
				hint.Set(name + " is already in the list.")
				return
			}
		}
		hint.Set("Connecting to " + name + "...")
		d.resolveInstance(name, func(added *newEndpointInstance, err error) {
			if err != nil {
				hint.Set(err.Error())
				return
			}
			// The typed name was checked against the list above, but addInstance answers
			// with the instance's own @@SERVERNAME, so an alias, address or hostname for a
			// listed instance passes that check and lands here as a second entry for one
			// instance. configure's pairwise loop skips only on pointer identity, so the
			// instance would import its own certificate: <inst>_login and <inst>_user
			// created in its own master and granted CONNECT on its own endpoint.
			for _, inst := range d.instances {
				if strings.EqualFold(inst.name, added.name) {
					hint.Set(fmt.Sprintf("%s answers as %s, which is already in the list.", name, added.name))
					return
				}
			}
			d.instances = append(d.instances, added)
			addNameRow.SetValue("")
			rows := rowsFor()
			resetGrid(grid, headers, rows, len(rows)-1)
			if added.hasEndpoint {
				hint.Set(fmt.Sprintf("Added %s, which already has %s.", added.name, added.endpointURL))
				return
			}
			hint.Set(fmt.Sprintf("Added %s, which has no endpoint yet.", added.name))
		})
	})
	removeBtn := widgets.NewButton("Remove Instance", func() {
		i := grid.SelectedRow()
		if i < 0 || i >= len(d.instances) {
			return
		}
		if d.instances[i].local {
			hint.Set("This connection's own instance is always part of the exchange.")
			return
		}
		// The row that took the removed one's place, so a run of removals works from one
		// key instead of resetting the cursor, and unlike SetData keeps any column the
		// user dragged wider.
		d.instances = append(d.instances[:i], d.instances[i+1:]...)
		rows := rowsFor()
		resetGrid(grid, headers, rows, min(i, len(rows)-1))
	})

	// The instance list the page opened with: this connection's own instance alone.
	// Without a RevertFn, Ctrl+Z clears the name and password fields, says "Reverted
	// to the loaded values", and leaves every added instance in the exchange. A
	// shallow clone suffices: an instance is never edited after addInstance.
	baseline := slices.Clone(d.instances)

	gridRow := propsheet.NewGridRow(grid, 5)
	gridRow.DirtyFn = func() bool { return len(d.instances) > 1 }
	gridRow.RevertFn = func() {
		d.instances = slices.Clone(baseline)
		hint.Set("")
		// resetGrid, not redrawGrid: Revert changes the row set, so the cursor
		// redrawGrid would keep points at a row that is gone.
		resetGrid(grid, headers, rowsFor(), 0)
	}

	// The same rows re-rendered, not a changed set: redrawGrid keeps the cursor.
	commit := func() { redrawGrid(grid, headers, rowsFor()) }

	return []propsheet.Row{
		propsheet.Section("Instances"),
		gridRow,
		propsheet.Section("Add an instance"),
		addNameRow,
		propsheet.Buttons(addBtn, removeBtn),
		hint,
		propsheet.Note("An instance is reached with its own saved connection; one without is offered a Connect prompt pre-filled from this connection. Each keeps its own certificate; only the public half of each is exchanged, and no private key is read or transmitted."),
		propsheet.Note("Certificates are named <instance>_Cert, and each peer gets a login and user named <instance>_login and <instance>_user to own the certificate it presents. A named instance contributes HOST$INSTANCE, not HOST\\INSTANCE. Anything already present is left alone."),
	}, commit
}

// addInstance connects to a named instance and reads what it already has, so the
// grid can say what will be created before anything is. Asynchronous (the
// connect is a round trip, and Peer may open a new one). Through probe, so it is
// bounded by a timeout and a result for an earlier showing is dropped.
func (d *NewEndpointDialog) addInstance(name string, done func(*newEndpointInstance, error)) {
	sc := d.sc
	inst := &newEndpointInstance{name: name}
	d.probe("connecting to an instance for the endpoint exchange", func(ctx context.Context) error {
		peer, err := sc.Peer(ctx, name)
		if err != nil {
			return fmt.Errorf("connect to %s: %w", name, err)
		}
		// The name as the instance reports it, not as typed: the certificate, login and
		// user names derive from it and must match on both sides.
		if reported := peer.Server.Name(); reported != "" {
			inst.name = reported
		}
		ep, err := peer.Server.DatabaseMirroringEndpoint(ctx)
		if err != nil && !errors.Is(err, gosmo.ErrNotFound) {
			return fmt.Errorf("read %s's endpoint: %w", name, err)
		}
		if err == nil {
			inst.hasEndpoint = true
			inst.endpointURL = ep.URL()
		}
		return nil
	}, func(err error) {
		if err != nil {
			done(nil, err)
			return
		}
		done(inst, nil)
	})
}

// showNewEndpointDialog opens New Database Mirroring Endpoint — the Object
// Explorer context menu's entry point on the Always On node.
func (a *App) showNewEndpointDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newEndpointDialog.show(sc, node)
}
