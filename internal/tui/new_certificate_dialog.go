package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_certificate_dialog.go is the New Certificate dialog (a database's
// Security > Certificates folder), built on newObjectDialog.
//
// Generated certificates only (WITH SUBJECT). Every import form — FROM FILE,
// EXECUTABLE FILE, ASSEMBLY, WITH PRIVATE KEY (FILE = …) — reads the server's
// own filesystem, which gossms cannot browse, and is left to a query window.
//
// The private key is protected by the database master key or by a password.
// The first needs a master key, which SQL Server will not create implicitly
// (Msg 15581), so when the database has none the dialog asks for its password
// and creates it first — ensureMasterKey, shared with the endpoint dialog. The
// private-key and master-key fields are keyProtectionFields, shared with New
// Asymmetric Key.

// certDateLayout is what the two date fields take. EXPIRY_DATE and START_DATE
// are dates to SQL Server's parser as gosmo emits them (yyyymmdd), so a time
// of day would be a promise the statement does not keep.
const certDateLayout = "2006-01-02"

// ncertPrefetch is what the dialog reads before it opens.
type ncertPrefetch struct {
	existingNames map[string]bool
	hasMasterKey  bool
}

func fetchNewCertificatePrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*ncertPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	certs, err := dbObj.CertificatesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(certs))
	for _, c := range certs {
		existing[strings.ToLower(c.Name)] = true
	}
	has, err := dbObj.HasMasterKeyContext(ctx)
	if err != nil {
		return nil, err
	}
	return &ncertPrefetch{existingNames: existing, hasMasterKey: has}, nil
}

// NewCertificateDialog is the New Certificate dialog.
type NewCertificateDialog struct {
	newObjectDialog[ncertPrefetch]

	// dbName is the database the certificate is created in, and node the
	// folder to refresh afterwards. Both are set by show, before the embedded
	// dialog's own show runs the prefetch that reads them.
	dbName string
	node   *explorerNode
}

// NewNewCertificateDialog creates the dialog and wires its callbacks.
func NewNewCertificateDialog(app *App) *NewCertificateDialog {
	d := &NewCertificateDialog{}
	d.init(app, newObjectConfig[ncertPrefetch]{
		title: "New Certificate",
		noun:  "Certificate",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*ncertPrefetch, error) {
			return fetchNewCertificatePrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Certificates folder.
func (d *NewCertificateDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewCertificateDialog) buildPages(pf *ncertPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Certificate name", "", 30)
	subjectField := propsheet.Text("Subject", "", 40)
	startField := propsheet.Text("Start date (yyyy-mm-dd)", "", 12)
	expiryField := propsheet.Text("Expiry date (yyyy-mm-dd)", "", 12)

	protection := newKeyProtectionFields(dbName, pf.hasMasterKey)

	rows := []propsheet.Row{
		propsheet.Section("Certificate"),
		nameField,
		propsheet.Static("Database", dbName),
		subjectField,
		startField, expiryField,
		propsheet.Note("Leave the dates blank for SQL Server's defaults: valid from now, for one year."),
	}
	rows = append(rows, protection.rows("A certificate that has to be usable without anyone typing a password — a login or user mapped to it, a signed module, a Service Broker dialog — needs the master key. A password-protected private key is opened with that password each time it is used.")...)
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		return validateNewCertificate(newCertificateInput{
			name:               d.objectName(),
			subject:            subjectField.Value(),
			start:              startField.Value(),
			expiry:             expiryField.Value(),
			keyProtectionInput: protection.input(),
			existingNames:      pf.existingNames,
		}, time.Now())
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef, not DatabaseByName: every statement here addresses the
		// database by name, and the by-name read would not work under Script
		// Changes.
		dbObj := sc.Server.DatabaseRef(dbName)
		spec := gosmo.CertificateSpec{
			Name:    d.objectName(),
			Subject: strings.TrimSpace(subjectField.Value()),
		}
		// Already validated by the preflight; blank parses to the zero time,
		// which gosmo omits.
		spec.StartDate, _ = parseCertDate(startField.Value())
		spec.ExpiryDate, _ = parseCertDate(expiryField.Value())
		var err error
		if spec.EncryptionPassword, err = protection.apply(ctx, dbObj); err != nil {
			return err
		}
		return dbObj.CreateCertificateContext(ctx, spec)
	}
}

// newCertificateInput is the page's values, gathered for validateNewCertificate.
type newCertificateInput struct {
	name, subject, start, expiry string
	keyProtectionInput
	existingNames map[string]bool
}

// validateNewCertificate refuses what the server would, with a message that
// names the field — Msg 15581 for a missing master key password says nothing
// about the dialog it came from.
func validateNewCertificate(in newCertificateInput, now time.Time) error {
	if in.name == "" {
		return fmt.Errorf("certificate name is required")
	}
	if in.existingNames[strings.ToLower(in.name)] {
		return fmt.Errorf("a certificate named %q already exists in %s", in.name, in.dbName)
	}
	if strings.TrimSpace(in.subject) == "" {
		return fmt.Errorf("subject is required")
	}
	start, err := parseCertDate(in.start)
	if err != nil {
		return fmt.Errorf("start date: use yyyy-mm-dd")
	}
	expiry, err := parseCertDate(in.expiry)
	if err != nil {
		return fmt.Errorf("expiry date: use yyyy-mm-dd")
	}
	if !expiry.IsZero() {
		from := start
		if from.IsZero() {
			from = now
		}
		if !expiry.After(from) {
			return fmt.Errorf("the expiry date must be after the start date (today, if none is given)")
		}
	}
	return validateKeyProtection(in.keyProtectionInput)
}

// parseCertDate parses a certDateLayout date, returning the zero Time for a
// blank field.
func parseCertDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(certDateLayout, s)
}

// showNewCertificateDialog is the Certificates folder's entry point.
func (a *App) showNewCertificateDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newCertificateDialog.show(sc, node)
}
