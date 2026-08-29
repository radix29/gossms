package tui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_endpoint_dialog.go is "New Database Mirroring Endpoint..." on the Always
// On High Availability node, the prerequisite New Availability Group needs.
//
// # Why it does not copy files
//
// Replicas with no domain in common — every Linux deployment — authenticate to
// each other with certificates, and the documented setup is BACKUP CERTIFICATE
// to a file on each host, copy the files across by hand, then CREATE
// CERTIFICATE FROM FILE. gossms has no filesystem access to either host, so
// that route is closed to it.
//
// It takes the other one: each instance keeps its own key pair and gets its
// peers' *public* certificates, moved as bytes over the two connections gossms
// already has (gosmo's Certificate.Encoded and CertificateSpec.FromBinary). No
// private key is read, transmitted or written anywhere — which is also why the
// certificates are per-instance rather than one shared certificate, as the file
// recipe produces: sharing one requires moving its private key.
//
// Per instance the flow is: a database master key in master if there is none, a
// certificate of its own if there is none, then for each peer a login, a user,
// the peer's public certificate owned by that user, and CONNECT on the endpoint
// granted to that login. Every step is skipped when what it creates is already
// there, so a half-configured pair is completed rather than failed.

// endpointDefaultPort is the conventional database mirroring port. Nothing
// requires it, but every replica must reach every other one's, and a shared
// default is what makes that likely.
const endpointDefaultPort = 5022

// endpointDefaultName matches Microsoft's Linux availability group
// walkthrough, so an instance configured by hand from those docs and one
// configured here look the same.
const endpointDefaultName = "Hadr_endpoint"

var endpointAlgorithms = []string{"AES", "RC4", "AES RC4", "RC4 AES"}

// newEndpointInstance is one instance taking part in the exchange.
type newEndpointInstance struct {
	name string

	// local marks the instance the dialog is connected to — always in the list
	// and never removable.
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

	// existing is the local endpoint, if there is one. An instance can have only
	// one, so its presence turns the dialog from "create" into "add peers to the
	// one you have".
	existing *gosmo.DatabaseMirroringEndpoint
}

// NewEndpointDialog is the New Database Mirroring Endpoint dialog.
type NewEndpointDialog struct {
	newObjectDialog[newEndpointPrefetch]

	node *explorerNode

	instances []*newEndpointInstance

	// Values the page owns, read when the pipeline runs.
	endpointName    string
	port            int
	algorithm       string
	masterKeyPass   string
	commitInputs    func()
	certificateName func(instance string) string

	// peerServerFor resolves an instance to the gosmo.Server its half of the
	// exchange runs against — a test seam like certificateName above; the
	// default goes through db.ServerConn.Peer.
	peerServerFor func(ctx context.Context, inst *newEndpointInstance) (*gosmo.Server, error)

	// resolveInstance connects to a typed instance name and answers with what
	// it already has — a test seam like peerServerFor; the default is
	// addInstance. Asynchronous: done runs on the UI goroutine.
	resolveInstance func(name string, done func(*newEndpointInstance, error))

	// scriptedGroups is what the last scripted configure collected, one entry
	// per instance. Read by runScript, which runs after the pipeline.
	scriptedGroups []endpointScriptGroup
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
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	d.certificateName = func(instance string) string { return endpointPrincipalBase(instance) + "_Cert" }
	d.peerServerFor = d.defaultPeerServer
	d.resolveInstance = d.addInstance
	// The shell's Script Changes emits every statement as one undifferentiated
	// batch, which here is worse than useless: the statements belong to two or
	// more instances, and run whole against this one the certificate imports
	// fail and the GRANTs land on the wrong endpoint. See runScript.
	d.OnScript = d.runScript
	return d
}

// defaultPeerServer is peerServerFor's real implementation: this connection for
// the local instance, and a peer connection for every other one.
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
// makes the raw name unusable: [HOST\INST_login] is the spelling of a Windows
// principal, so CREATE LOGIN ... FROM CERTIFICATE on it is a name SQL Server
// also accepts from an authentication path unrelated to this certificate. It
// becomes HOST$INST, the convention SQL Server's own service accounts use
// (MSSQL$INSTANCE).
//
// Not truncated to the host the way gosmo's endpointURL does — right for a TCP
// host, wrong here, since two named instances on one machine would then share
// every principal name in the exchange. A default instance has no backslash and
// is unchanged.
func endpointPrincipalBase(instance string) string {
	return strings.ReplaceAll(instance, `\`, "$")
}

func (d *NewEndpointDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.node = node
	d.instances = nil
	d.commitInputs = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("Database mirroring endpoints", "Server: "+sc.Opts.Server)
}

func (d *NewEndpointDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*newEndpointPrefetch, error) {
	pf := &newEndpointPrefetch{localName: sc.Server.Name()}

	if info := sc.Server.Info(); info != nil && !info.IsHADREnabled {
		pf.blocker = fmt.Sprintf("Always On availability groups are not enabled on %s. Enable the feature and restart the instance first — on Linux, `mssql-conf set hadr.hadrenabled 1`.", sc.Opts.Server)
		return pf, nil
	}
	ep, err := sc.Server.DatabaseMirroringEndpointContext(ctx)
	if err != nil {
		return nil, err
	}
	pf.existing = ep
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

	d.commitInputs = func() {
		commitInstances()
		d.endpointName = strings.TrimSpace(nameRow.Value())
		d.algorithm = algorithmRow.Value()
		d.masterKeyPass = passRow.Value()
		if n, err := portRow.IntValue(); err == nil {
			d.port = int(n)
		}
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

	d.objectName = func() string { return strings.TrimSpace(nameRow.Value()) }
	d.preflight = func() error {
		d.commitInputs()
		return validateNewEndpoint(d.endpointName, d.masterKeyPass, d.instances)
	}
	d.applyFns[0] = d.configure
}

// validateNewEndpoint rejects what would otherwise fail partway through the
// pipeline, on an instance the user cannot see.
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
			// The typed name was checked against the list above, but addInstance
			// answers with the instance's own @@SERVERNAME — so an alias, an
			// address, or a hostname for an instance already listed gets past
			// that check and lands here as a second entry for one instance.
			// configure's pairwise loop skips only on pointer identity, so the
			// instance then imports its own certificate: <inst>_login and
			// <inst>_user are created in its own master and granted CONNECT on
			// its own endpoint, and a scripted run emits two groups for it.
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
		// The row that took the removed one's place, so a run of removals works
		// from one key instead of throwing the cursor back to the top each time
		// — and, unlike SetData, keeping any column the user dragged wider.
		d.instances = append(d.instances[:i], d.instances[i+1:]...)
		rows := rowsFor()
		resetGrid(grid, headers, rows, min(i, len(rows)-1))
	})

	// The instance list the page opened with — this connection's own instance
	// alone. Without a RevertFn, Ctrl+Z clears the name and password fields,
	// says "Reverted to the loaded values", and leaves every added instance
	// still in the exchange. A shallow clone is the whole snapshot: an instance
	// is built once by addInstance and never edited, so only the list changes.
	baseline := slices.Clone(d.instances)

	gridRow := propsheet.NewGridRow(grid, 5)
	gridRow.DirtyFn = func() bool { return len(d.instances) > 1 }
	gridRow.RevertFn = func() {
		d.instances = slices.Clone(baseline)
		hint.Set("")
		// resetGrid, not redrawGrid: Revert changes the row set, so the cursor
		// it would have kept points at a row that is no longer there.
		resetGrid(grid, headers, rowsFor(), 0)
	}

	// The same rows re-rendered, not a changed set — redrawGrid, which keeps
	// the cursor where the user left it.
	commit := func() { redrawGrid(grid, headers, rowsFor()) }

	return []propsheet.Row{
		propsheet.Section("Instances"),
		gridRow,
		propsheet.Section("Add an instance"),
		addNameRow,
		propsheet.Buttons(addBtn, removeBtn),
		hint,
		propsheet.Note("An instance is reached with its own saved connection if you have one, otherwise with this connection's credentials — connect to it once via File > Connect to give it different ones. Each keeps its own certificate; only the public half of each is exchanged, and no private key is read or transmitted."),
		propsheet.Note("Certificates are named <instance>_Cert, and each peer gets a login and user named <instance>_login and <instance>_user to own the certificate it presents. A named instance contributes HOST$INSTANCE, not HOST\\INSTANCE. Anything already present is left alone."),
	}, commit
}

// addInstance connects to a named instance and reads what it already has, so
// the grid can say what will be created before anything is. Asynchronous — the
// connect is a round trip, and Peer may have to open a new one.
func (d *NewEndpointDialog) addInstance(name string, done func(*newEndpointInstance, error)) {
	sc := d.sc
	ctx := d.ctx
	d.app.safego("connecting to an instance for the endpoint exchange", func() {
		inst := &newEndpointInstance{name: name}
		peer, err := sc.Peer(ctx, name)
		if err != nil {
			d.app.postAndWake(func() {
				done(nil, fmt.Errorf("connect to %s: %w", name, err))
			})
			return
		}
		// The name as the instance reports it, not as typed: the certificate,
		// login and user names derive from it and must match on both sides.
		if reported := peer.Server.Name(); reported != "" {
			inst.name = reported
		}
		ep, err := peer.Server.DatabaseMirroringEndpointContext(ctx)
		if err != nil {
			d.app.postAndWake(func() {
				done(nil, fmt.Errorf("read %s's endpoint: %w", name, err))
			})
			return
		}
		if ep != nil {
			inst.hasEndpoint = true
			inst.endpointURL = ep.URL()
		}
		d.app.postAndWake(func() { done(inst, nil) })
	})
}

// endpointPeer is one instance's connection and the certificate it presents,
// resolved once so the pairwise exchange below doesn't reconnect per pair.
type endpointPeer struct {
	inst    *newEndpointInstance
	server  *gosmo.Server
	master  *gosmo.Database
	cert    *gosmo.Certificate
	encoded []byte

	// ctx is the context every write *to this instance* runs against, and script
	// what it collects on a scripted run. One collector per instance rather than
	// one for the pipeline: the run is three phases over N instances with a skip
	// possible at every step, so no positional mapping leads from a flat
	// statement list back to the instance each statement belongs to.
	ctx    context.Context
	script *gosmo.ScriptCollector

	// certPending records that this instance has no certificate yet: under
	// scripting the CREATE was collected, not run, so its public key cannot be
	// read and nothing depending on it can be scripted.
	certPending bool
	// certSkipped names the peers whose certificates could not be imported
	// here for that reason.
	certSkipped []string
}

// endpointScriptGroup is one instance's share of a scripted run: the
// statements that belong to it, and what the run could not reach.
type endpointScriptGroup struct {
	instance    string
	stmts       []string
	certPending bool
	certSkipped []string
}

// configure is the whole pipeline — see the file comment for its shape. Every
// step is skipped when what it would create already exists.
func (d *NewEndpointDialog) configure(ctx context.Context) error {
	d.commitInputs()
	if err := validateNewEndpoint(d.endpointName, d.masterKeyPass, d.instances); err != nil {
		return err
	}

	d.scriptedGroups = nil
	scripting := gosmo.Scripting(ctx)

	peers := make([]*endpointPeer, 0, len(d.instances))
	for _, inst := range d.instances {
		server, err := d.peerServerFor(ctx, inst)
		if err != nil {
			return fmt.Errorf("connect to %s: %w", inst.name, err)
		}
		p := &endpointPeer{inst: inst, server: server, master: server.Database("master"), ctx: ctx}
		if scripting {
			p.ctx, p.script = gosmo.WithScript(ctx)
		}
		// p.ctx, not ctx: every phase below writes to p, and the collector a
		// statement lands in is what says which instance it runs on. Reads are
		// unaffected either way — WithScript intercepts only the two exec
		// chokepoints — so a peer's reads still hit the real server, which is
		// what makes a certificate's public key readable at all.
		if err := d.ensureCertificate(p.ctx, p); err != nil {
			return err
		}
		peers = append(peers, p)
	}

	// The exchange proper: every instance gets every other one's public
	// certificate, owned by a login that can then be granted CONNECT. The writes
	// land on p, not other, so p's context runs them.
	for _, p := range peers {
		for _, other := range peers {
			if p == other {
				continue
			}
			if err := d.importPeerCertificate(p.ctx, p, other); err != nil {
				return err
			}
		}
	}

	for _, p := range peers {
		if err := d.ensureEndpoint(p.ctx, p, peers); err != nil {
			return err
		}
	}
	if scripting {
		d.scriptedGroups = endpointScriptGroupsFrom(peers)
	}
	return nil
}

// endpointScriptGroupsFrom flattens the finished peers into what
// annotateEndpointScript renders, in the order the instances are listed.
func endpointScriptGroupsFrom(peers []*endpointPeer) []endpointScriptGroup {
	groups := make([]endpointScriptGroup, 0, len(peers))
	for _, p := range peers {
		g := endpointScriptGroup{
			instance:    p.inst.name,
			certPending: p.certPending,
			certSkipped: p.certSkipped,
		}
		if p.script != nil {
			g.stmts = p.script.Statements
		}
		groups = append(groups, g)
	}
	return groups
}

// ensureCertificate gives one instance a master key and a certificate of its
// own, and reads the public half back.
func (d *NewEndpointDialog) ensureCertificate(ctx context.Context, p *endpointPeer) error {
	has, err := p.master.HasMasterKeyContext(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if !has {
		if err := p.master.CreateMasterKeyContext(ctx, d.masterKeyPass); err != nil {
			return fmt.Errorf("%s: %w", p.inst.name, err)
		}
	}

	certName := d.certificateName(p.inst.name)
	cert, err := p.master.CertificateByNameContext(ctx, certName)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if cert == nil {
		spec := gosmo.CertificateSpec{Name: certName, Subject: p.inst.name + " database mirroring endpoint"}
		if err := p.master.CreateCertificateContext(ctx, spec); err != nil {
			return fmt.Errorf("%s: %w", p.inst.name, err)
		}
		if cert, err = p.master.CertificateByNameContext(ctx, certName); err != nil {
			return fmt.Errorf("%s: %w", p.inst.name, err)
		}
	}
	if cert == nil {
		// Scripting: the CREATE above was collected, not run, so there is no
		// certificate to read a public key from and nothing depending on one can
		// be scripted. Recorded rather than silently skipped — the script is
		// genuinely partial, and saying so is the difference between running it
		// and being done, and finding the endpoints refuse each other with
		// nothing explaining why.
		p.certPending = true
		return nil
	}
	if !cert.HasPrivateKey() {
		return fmt.Errorf("%s already has a certificate named %s without a private key — it cannot present that certificate. Rename or drop it first", p.inst.name, certName)
	}
	p.cert = cert
	if p.encoded, err = cert.EncodedContext(ctx); err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	return nil
}

// importPeerCertificate gives p the login, user and public certificate it needs
// to authenticate other.
func (d *NewEndpointDialog) importPeerCertificate(ctx context.Context, p, other *endpointPeer) error {
	if len(other.encoded) == 0 {
		// Scripting, and other's certificate does not exist yet — see
		// ensureCertificate. Recorded on p, whose script is missing the
		// import.
		p.certSkipped = append(p.certSkipped, other.inst.name)
		return nil
	}
	login := endpointPrincipalBase(other.inst.name) + "_login"
	user := endpointPrincipalBase(other.inst.name) + "_user"
	certName := d.certificateName(other.inst.name)

	// Only an actual absence means "create it". Treating every lookup failure as
	// absence reports the CREATE LOGIN error instead of the permission or
	// connection error that really stopped the pipeline, on a dialog where that
	// distinction is the whole diagnosis.
	_, err := p.server.LoginByNameContext(ctx, login)
	switch {
	case err == nil:
		// Already there; nothing to create.
	case errors.Is(err, gosmo.ErrNotFound):
		// The login is never signed in as — it exists to own the certificate and
		// be the grantee of CONNECT — so its password is random and deliberately
		// never shown or stored.
		password, perr := randomPassword()
		if perr != nil {
			return perr
		}
		// "Absent" can also mean "there but not visible from here": SQL Server
		// hides a principal the caller lacks VIEW ANY DEFINITION on by returning
		// no rows, not an error, so the lookup above cannot tell the two apart.
		// Tolerate the collision, as the CreateUser call below does.
		if err := p.server.CreateLoginContext(ctx, login, password, nil); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("%s: create login %s: %w", p.inst.name, login, err)
		}
	default:
		return fmt.Errorf("%s: look up login %s: %w", p.inst.name, login, err)
	}
	// Looked up first rather than created-and-tolerated, for the same reason the
	// login above is: an unexpected failure has to be reported as itself. It
	// also keeps a scripted run runnable — CREATE USER is not idempotent, and
	// the tolerate-the-error path never runs under WithScript, so an existing
	// user would be emitted as a statement that fails.
	_, err = p.master.UserByNameContext(ctx, user)
	switch {
	case err == nil:
		// Already there; nothing to create.
	case errors.Is(err, gosmo.ErrNotFound):
		if err := p.master.CreateUserContext(ctx, user, login, ""); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("%s: create user %s: %w", p.inst.name, user, err)
		}
	default:
		return fmt.Errorf("%s: look up user %s: %w", p.inst.name, user, err)
	}

	existing, err := p.master.CertificateByNameContext(ctx, certName)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if existing != nil {
		// Same name is not the same certificate. A reinstalled peer generates a
		// fresh key pair under its old name, and skipping the import on the name
		// alone makes the pipeline report success while the endpoint then
		// refuses the peer's connection with nothing saying why. Thumbprints are
		// already loaded on both rows, so this costs no round trip.
		//
		// other.cert is dereferenced unguarded on purpose: ensureCertificate
		// sets cert and encoded together and an empty encoded already returned
		// above, so a nil here means that invariant broke. A nil check would
		// turn the break into this check silently not running — the one outcome
		// it exists to prevent.
		if !bytes.Equal(existing.Thumbprint, other.cert.Thumbprint) {
			return fmt.Errorf("%s already has a different certificate named %s than the one %s presents — drop it there and run this again",
				p.inst.name, certName, other.inst.name)
		}
		return nil
	}
	spec := gosmo.CertificateSpec{Name: certName, Authorization: user, FromBinary: other.encoded}
	if err := p.master.CreateCertificateContext(ctx, spec); err != nil {
		return fmt.Errorf("%s: import %s's certificate: %w", p.inst.name, other.inst.name, err)
	}
	return nil
}

// ensureEndpoint creates p's endpoint if it has none, then grants every peer's
// login CONNECT on it.
func (d *NewEndpointDialog) ensureEndpoint(ctx context.Context, p *endpointPeer, all []*endpointPeer) error {
	ep, err := p.server.DatabaseMirroringEndpointContext(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if ep == nil {
		spec := gosmo.EndpointSpec{
			Name:                d.endpointName,
			Port:                d.port,
			Role:                "ALL",
			Authentication:      "CERTIFICATE " + quoteBracket(d.certificateName(p.inst.name)),
			Encryption:          "REQUIRED",
			EncryptionAlgorithm: d.algorithm,
		}
		if ep, err = p.server.CreateDatabaseMirroringEndpointContext(ctx, spec); err != nil {
			return fmt.Errorf("%s: create endpoint: %w", p.inst.name, err)
		}
	}
	if !strings.EqualFold(ep.State, "STARTED") {
		if err := ep.StartContext(ctx); err != nil {
			return fmt.Errorf("%s: start endpoint %s: %w", p.inst.name, ep.Name, err)
		}
	}
	for _, other := range all {
		if other == p {
			continue
		}
		if slices.Contains(p.certSkipped, other.inst.name) {
			// Scripting, and importPeerCertificate skipped this peer, so the
			// login this would grant to was never created here either. Emitting
			// the GRANT anyway makes the script fail on a login that does not
			// exist — and, run as one batch, fail before the endpoint statements
			// above it take effect. The script's note says the grant is missing
			// along with the login.
			continue
		}
		if err := ep.GrantConnectContext(ctx, endpointPrincipalBase(other.inst.name)+"_login"); err != nil {
			return fmt.Errorf("%s: grant %s connect: %w", p.inst.name, other.inst.name, err)
		}
	}
	return nil
}

// runScript replaces the shell's, which emits every collected statement as one
// batch. Here that batch spans two or more instances: run whole against this
// one, the certificate imports fail (each is FROM BINARY of a key belonging
// elsewhere) and the GRANTs land on the wrong endpoint.
//
// The statements are already grouped by instance — each peer collects its own,
// see configure — so nothing here maps a flat list back onto a list of targets
// the way NewAGDialog.annotateScript does.
func (d *NewEndpointDialog) runScript() {
	scriptCtx, _ := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", annotateEndpointScript(d.scriptedGroups))
	})
}

// annotateEndpointScript renders one scripted run: a header, then each
// instance's statements under a comment naming it, then a note wherever the run
// could not reach something.
//
// Pure, so it can be tested without a server — which matters here, since what
// it has to get right is the case a live run on already-configured instances
// never produces.
func annotateEndpointScript(groups []endpointScriptGroup) string {
	var b strings.Builder
	b.WriteString("-- New Database Mirroring Endpoint: these statements do NOT all run on the same instance.\n")
	b.WriteString("-- Run each block against the instance named above it.\n")
	b.WriteString("--\n")
	b.WriteString("-- Each login below is created with a random password and is never signed in as:\n")
	b.WriteString("-- it exists only to own a peer's certificate and to be the grantee of CONNECT on\n")
	b.WriteString("-- the endpoint. The password is not recorded anywhere, here or in gossms.\n")

	partial := slices.ContainsFunc(groups, func(g endpointScriptGroup) bool {
		return g.certPending || len(g.certSkipped) > 0
	})
	if partial {
		b.WriteString("--\n")
		b.WriteString("-- THIS SCRIPT IS INCOMPLETE. See the notes below: a certificate has to exist\n")
		b.WriteString("-- before its public key can be read, and the exchange is built from those keys.\n")
	}

	for _, g := range groups {
		if len(g.stmts) == 0 && !g.certPending && len(g.certSkipped) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n-- on %s\n", g.instance)
		for _, stmt := range g.stmts {
			fmt.Fprintf(&b, "%s\nGO\n", stmt)
		}
		if g.certPending {
			fmt.Fprintf(&b, "-- %s has no certificate yet. Its public key is what every other instance\n"+
				"-- imports FROM BINARY, and it can only be read from an instance that actually has\n"+
				"-- one — so nothing that depends on it is in this script.\n", g.instance)
		}
		if len(g.certSkipped) > 0 {
			fmt.Fprintf(&b, "-- Missing here: the certificate, login and user for %s, and the CONNECT grant\n"+
				"-- that goes with them.\n", strings.Join(g.certSkipped, ", "))
		}
	}

	if partial {
		b.WriteString("\n-- Run the statements above on their instances, then press Script Changes again:\n")
		b.WriteString("-- with the certificates in place the next script contains the exchange in full.\n")
	}
	return b.String()
}

// randomPassword generates a password for a login that exists only to own a
// certificate. Nothing ever authenticates as it, so the value is never shown.
func randomPassword() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate a password for the certificate's login: %w", err)
	}
	// A leading letter and a trailing punctuation mark, so the result satisfies
	// a complexity policy whatever the random bytes encode to.
	return "E" + base64.RawURLEncoding.EncodeToString(buf[:]) + "!9", nil
}

// isAlreadyExists reports whether err is the server complaining that the
// principal is already there — the case this pipeline treats as success, every
// step being skippable.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "15023")
}

// quoteBracket bracket-quotes an identifier for a clause gosmo passes through
// verbatim: the AUTHENTICATION clause is a small grammar, not one keyword, so
// the certificate name inside it is quoted here.
func quoteBracket(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// showNewEndpointDialog opens New Database Mirroring Endpoint — the Object
// Explorer context menu's entry point on the Always On node.
func (a *App) showNewEndpointDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newEndpointDialog.show(sc, node)
}
