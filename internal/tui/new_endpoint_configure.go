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
	"github.com/radix29/gosmo/tsql"
)

// endpointPeer is one instance's connection and the certificate it presents,
// resolved once so the pairwise exchange doesn't reconnect per pair.
type endpointPeer struct {
	inst    *newEndpointInstance
	server  *gosmo.Server
	master  *gosmo.Database
	cert    *gosmo.Certificate
	encoded []byte

	// ctx is the context every write *to this instance* runs against, and script
	// what it collects on a scripted run. One collector per instance, not per
	// pipeline: the run is three phases over N instances with a skip possible at
	// every step, so no positional mapping leads from a flat statement list back to
	// its instance.
	ctx    context.Context
	script *gosmo.ScriptCollector

	// certPending records that this instance has no certificate yet: under scripting
	// the CREATE was collected, not run, so its public key cannot be read and
	// nothing depending on it can be scripted.
	certPending bool
	// certSkipped names the peers whose certificates could not be imported here for
	// that reason.
	certSkipped []string
}

// endpointScriptGroup is one instance's share of a scripted run: its statements,
// and what the run could not reach.
type endpointScriptGroup struct {
	instance    string
	stmts       []string
	certPending bool
	certSkipped []string
}

// configure is the whole pipeline (see the file comment). Every step is skipped
// when what it would create already exists. Under Script Changes it returns what
// each instance's collector gathered, one group per instance in req's order;
// otherwise nil.
func (d *NewEndpointDialog) configure(ctx context.Context, req endpointRequest) ([]endpointScriptGroup, error) {
	if err := validateNewEndpoint(req.name, req.masterKeyPass, req.instances); err != nil {
		return nil, err
	}

	scripting := gosmo.Scripting(ctx)

	peers := make([]*endpointPeer, 0, len(req.instances))
	for _, inst := range req.instances {
		server, err := d.peerServerFor(ctx, inst)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", inst.name, err)
		}
		p := &endpointPeer{inst: inst, server: server, master: server.DatabaseRef("master"), ctx: ctx}
		if scripting {
			p.ctx, p.script = gosmo.WithScript(ctx)
		}
		// p.ctx, not ctx: every phase below writes to p, and the collector a statement
		// lands in says which instance it runs on. Reads are unaffected (WithScript
		// intercepts only the two exec chokepoints), so a peer's reads still hit the real
		// server, which is what makes a certificate's public key readable at all.
		if err := d.ensureCertificate(p.ctx, p, req.masterKeyPass); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}

	// The exchange proper: every instance gets every other's public certificate,
	// owned by a login that can then be granted CONNECT. The writes land on p, not
	// other, so p's context runs them.
	for _, p := range peers {
		for _, other := range peers {
			if p == other {
				continue
			}
			if err := d.importPeerCertificate(p.ctx, p, other); err != nil {
				return nil, err
			}
		}
	}

	for _, p := range peers {
		if err := d.ensureEndpoint(p.ctx, p, peers, req); err != nil {
			return nil, err
		}
	}
	if scripting {
		return endpointScriptGroupsFrom(peers), nil
	}
	return nil, nil
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
			g.stmts = p.script.Statements()
		}
		groups = append(groups, g)
	}
	return groups
}

// findCertificateIfAny is CertificateByName with absence as a nil certificate
// rather than an error: the pipeline creates or imports one on that branch, and
// under WithScript a certificate whose CREATE was only collected reads as absent
// too.
func findCertificateIfAny(ctx context.Context, d *gosmo.Database, name string) (*gosmo.Certificate, error) {
	c, err := d.CertificateByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, nil
	}
	return c, err
}

// ensureCertificate gives one instance a master key and a certificate of its own
// and reads the public half back. masterKeyPass protects a master key it has to
// create.
func (d *NewEndpointDialog) ensureCertificate(ctx context.Context, p *endpointPeer, masterKeyPass string) error {
	if err := ensureMasterKey(ctx, p.master, masterKeyPass); err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}

	certName := d.certificateName(p.inst.name)
	cert, err := findCertificateIfAny(ctx, p.master, certName)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if cert == nil {
		spec := gosmo.CreateCertificateRequest{Name: certName, Subject: p.inst.name + " database mirroring endpoint"}
		if _, err := p.master.CreateCertificate(ctx, spec); err != nil {
			return fmt.Errorf("%s: %w", p.inst.name, err)
		}
		if cert, err = findCertificateIfAny(ctx, p.master, certName); err != nil {
			return fmt.Errorf("%s: %w", p.inst.name, err)
		}
	}
	if cert == nil {
		// Scripting: the CREATE above was collected, not run, so there is no certificate
		// to read a public key from and nothing depending on one can be scripted.
		// Recorded rather than silently skipped: the script is partial, and saying so
		// avoids endpoints refusing each other with nothing explaining why.
		p.certPending = true
		return nil
	}
	if !cert.HasPrivateKey() {
		return fmt.Errorf("%s already has a certificate named %s without a private key — it cannot present that certificate. Rename or drop it first", p.inst.name, certName)
	}
	p.cert = cert
	if p.encoded, err = cert.Encoded(ctx); err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	return nil
}

// importPeerCertificate gives p the login, user and public certificate it needs
// to authenticate other.
func (d *NewEndpointDialog) importPeerCertificate(ctx context.Context, p, other *endpointPeer) error {
	if len(other.encoded) == 0 {
		// Scripting, and other's certificate does not exist yet (ensureCertificate).
		// Recorded on p, whose script is missing the import.
		p.certSkipped = append(p.certSkipped, other.inst.name)
		return nil
	}
	login := endpointPrincipalBase(other.inst.name) + "_login"
	user := endpointPrincipalBase(other.inst.name) + "_user"
	certName := d.certificateName(other.inst.name)

	// Only an actual absence means "create it". Treating every lookup failure as
	// absence would report the CREATE LOGIN error instead of the permission or
	// connection error that really stopped the pipeline.
	_, err := p.server.LoginByName(ctx, login)
	switch {
	case err == nil:
		// Already there; nothing to create.
	case errors.Is(err, gosmo.ErrNotFound):
		// The login is never signed in as (it owns the certificate and is the CONNECT
		// grantee), so its password is random and never shown or stored.
		password, perr := randomPassword()
		if perr != nil {
			return perr
		}
		// "Absent" can also mean "there but not visible": SQL Server hides a principal
		// the caller lacks VIEW ANY DEFINITION on by returning no rows, not an error.
		// Tolerate the collision, as the CreateUser call below does.
		if _, err := p.server.CreateLogin(ctx, gosmo.CreateLoginRequest{Name: login, Password: password}); err != nil && !gosmo.IsAlreadyExists(err) {
			return fmt.Errorf("%s: create login %s: %w", p.inst.name, login, err)
		}
	default:
		return fmt.Errorf("%s: look up login %s: %w", p.inst.name, login, err)
	}
	// Looked up first rather than created-and-tolerated, like the login above, so an
	// unexpected failure is reported as itself. It also keeps a scripted run
	// runnable: CREATE USER is not idempotent and the tolerate-the-error path never
	// runs under WithScript, so an existing user would be emitted as a failing
	// statement.
	_, err = p.master.UserByName(ctx, user)
	switch {
	case err == nil:
		// Already there; nothing to create.
	case errors.Is(err, gosmo.ErrNotFound):
		if _, err := p.master.CreateUser(ctx, gosmo.CreateUserRequest{Name: user, Login: login}); err != nil && !gosmo.IsAlreadyExists(err) {
			return fmt.Errorf("%s: create user %s: %w", p.inst.name, user, err)
		}
	default:
		return fmt.Errorf("%s: look up user %s: %w", p.inst.name, user, err)
	}

	existing, err := findCertificateIfAny(ctx, p.master, certName)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if existing != nil {
		// Same name is not the same certificate. A reinstalled peer generates a fresh key
		// pair under its old name, and skipping the import on name alone would report
		// success while the endpoint refuses the peer's connection. Thumbprints are
		// already loaded on both rows, so this costs no round trip.
		//
		// other.cert is dereferenced unguarded on purpose: ensureCertificate sets cert
		// and encoded together and an empty encoded returned above, so a nil here
		// means that invariant broke. A nil check would silently skip this check.
		if !bytes.Equal(existing.Thumbprint, other.cert.Thumbprint) {
			return fmt.Errorf("%s already has a different certificate named %s than the one %s presents — drop it there and run this again",
				p.inst.name, certName, other.inst.name)
		}
		return nil
	}
	spec := gosmo.CreateCertificateRequest{Name: certName, Authorization: user, FromBinary: other.encoded}
	if _, err := p.master.CreateCertificate(ctx, spec); err != nil {
		return fmt.Errorf("%s: import %s's certificate: %w", p.inst.name, other.inst.name, err)
	}
	return nil
}

// ensureEndpoint creates p's endpoint if it has none, then grants every peer's
// login CONNECT on it. req says what a created endpoint is like.
func (d *NewEndpointDialog) ensureEndpoint(ctx context.Context, p *endpointPeer, all []*endpointPeer, req endpointRequest) error {
	ep, err := p.server.DatabaseMirroringEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.inst.name, err)
	}
	if ep == nil {
		// gosmo passes the AUTHENTICATION clause through verbatim (it is a small
		// grammar, not one keyword), so the certificate name inside it is quoted here.
		spec := gosmo.CreateDatabaseMirroringEndpointRequest{
			Name:                req.name,
			Port:                req.port,
			Role:                "ALL",
			Authentication:      "CERTIFICATE " + gosmo.QuoteName(d.certificateName(p.inst.name)),
			Encryption:          "REQUIRED",
			EncryptionAlgorithm: req.algorithm,
		}
		if ep, err = p.server.CreateDatabaseMirroringEndpoint(ctx, spec); err != nil {
			return fmt.Errorf("%s: create endpoint: %w", p.inst.name, err)
		}
	}
	if ep.State != gosmo.EndpointStarted {
		if err := ep.Start(ctx); err != nil {
			return fmt.Errorf("%s: start endpoint %s: %w", p.inst.name, ep.Name, err)
		}
	}
	for _, other := range all {
		if other == p {
			continue
		}
		if slices.Contains(p.certSkipped, other.inst.name) {
			// Scripting, and importPeerCertificate skipped this peer, so the login this
			// would grant to was never created here either. Emitting the GRANT would make
			// the script fail on a missing login and, run as one batch, fail before the
			// endpoint statements above take effect. The script's note says the grant is
			// missing along with the login.
			continue
		}
		if err := ep.GrantConnect(ctx, endpointPrincipalBase(other.inst.name)+"_login"); err != nil {
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
// Statements are already grouped by instance (each peer collects its own, see
// configure) and each group carries notes about certificates the script could
// not include, so this does not render one collector with gosmo's per-instance
// labels as NewAGDialog.runScript does.
func (d *NewEndpointDialog) runScript() {
	scriptCtx, _ := gosmo.WithScript(d.ctx)
	var groups []endpointScriptGroup
	scriptCtx = withEndpointScript(scriptCtx, &groups)
	sc := d.sc
	d.runPipeline(scriptCtx, func(string) {
		d.app.openQueryWithText(sc, "", annotateEndpointScript(groups))
	})
}

// annotateEndpointScript renders one scripted run: a header, then each
// instance's statements under a comment naming it, then a note wherever the run
// could not reach something.
//
// Pure, so it is testable without a server; a live run on already-configured
// instances never produces the case it must get right.
func annotateEndpointScript(groups []endpointScriptGroup) string {
	var b strings.Builder
	b.WriteString("-- New Database Mirroring Endpoint: these statements do NOT all run on the same instance.\n")
	b.WriteString("-- Run each block against the instance named above it.\n")
	b.WriteString("--\n")
	b.WriteString("-- Each login below is never signed in as: it exists only to own a peer's\n")
	b.WriteString("-- certificate and to be the grantee of CONNECT on the endpoint.\n")

	placeholder := slices.ContainsFunc(groups, func(g endpointScriptGroup) bool {
		return slices.ContainsFunc(g.stmts, func(stmt string) bool {
			return strings.Contains(stmt, gosmo.PasswordPlaceholder)
		})
	})
	if placeholder {
		b.WriteString("--\n")
		fmt.Fprintf(&b, "-- Passwords are not scripted: replace each %s before running,\n", gosmo.PasswordPlaceholder)
		b.WriteString("-- in CREATE MASTER KEY with the master key password and in CREATE LOGIN with any\n")
		b.WriteString("-- strong password (Apply gives each login a random one and records it nowhere).\n")
	}

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
		fmt.Fprintf(&b, "\n-- on %s\n", tsql.CommentSafe(g.instance))
		for _, stmt := range g.stmts {
			fmt.Fprintf(&b, "%s\nGO\n", stmt)
		}
		if g.certPending {
			fmt.Fprintf(&b, "-- %s has no certificate yet. Its public key is what every other instance\n"+
				"-- imports FROM BINARY, and it can only be read from an instance that actually has\n"+
				"-- one — so nothing that depends on it is in this script.\n", tsql.CommentSafe(g.instance))
		}
		if len(g.certSkipped) > 0 {
			fmt.Fprintf(&b, "-- Missing here: the certificate, login and user for %s, and the CONNECT grant\n"+
				"-- that goes with them.\n", tsql.CommentSafe(strings.Join(g.certSkipped, ", ")))
		}
	}

	if partial {
		b.WriteString("\n-- Run the statements above on their instances, then press Script Changes again:\n")
		b.WriteString("-- with the certificates in place the next script contains the exchange in full.\n")
	}
	return b.String()
}

// randomPassword generates a password for a login that exists only to own a
// certificate. Nothing authenticates as it, so the value is never shown.
func randomPassword() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate a password for the certificate's login: %w", err)
	}
	// A leading letter and a trailing punctuation mark, so the result satisfies a
	// complexity policy whatever the random bytes encode to.
	return "E" + base64.RawURLEncoding.EncodeToString(buf[:]) + "!9", nil
}
