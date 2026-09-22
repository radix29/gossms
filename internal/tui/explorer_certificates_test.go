package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Object Explorer wiring for a database's Certificates folder, on the
// per-family checklist explorer_database_scoped_credentials_test.go runs: the
// folder, the leaf's Script as / Delete / Properties, and the Delete gate
// against the rights table probed live (docs/decisions.md § Keys and
// certificates).

var (
	certValidExpiry   = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	certExpiredExpiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	certStart         = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
)

// certRow is a sys.certificates row in the column order gosmo scans it.
func certRow(name, keyType string, expiry time.Time) []driver.Value {
	return []driver.Value{name, int64(256), int64(1), "CN=" + name, keyType,
		certStart, expiry, []byte{0xAB, 0xCD},
		"dbo", "CN=issuer", "5a1b", int64(3072), true, nil, ""}
}

func certListResponse() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.certificates",
		cols:  15,
		rows: [][]driver.Value{
			certRow("aaa_cert", "ENCRYPTED_BY_MASTER_KEY", certValidExpiry),
			certRow("old_cert", "NO_PRIVATE_KEY", certExpiredExpiry),
		},
	}
}

// An expired certificate still works for an endpoint or a signed module, and
// nothing else in the row says it is expired — so the label does.
func TestCertificatesLoaderMarksExpiredAndCarriesTheDatabase(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), certListResponse())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeCertificates](l,
		&explorerNode{data: nodeData{Type: NodeCertificates, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadCertificatesChildren: %v", err)
	}
	want := []string{"aaa_cert", "old_cert (Expired)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	// Name stays bare: Script as and Delete address the certificate by it.
	if children[1].data.Name != "old_cert" {
		t.Errorf("the expired certificate's Name is %q, want the bare name", children[1].data.Name)
	}
	assertCarriesDatabase(t, children, NodeCertificate)
}

func TestCertificateLabelExpiry(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		expiry time.Time
		want   string
	}{
		{now.Add(time.Hour), "c"},
		{now.Add(-time.Hour), "c (Expired)"},
		{time.Time{}, "c"}, // unknown is not expired
	} {
		if got := certificateLabel(&gosmo.Certificate{Name: "c", ExpiryDate: tc.expiry}, now); got != tc.want {
			t.Errorf("expiry %v: label %q, want %q", tc.expiry, got, tc.want)
		}
	}
}

func TestCertificateLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		if got := objectIcon(NodeCertificate, style.s); got == 0 || got == '•' {
			t.Errorf("%s: NodeCertificate has no glyph of its own (%q)", style.name, got)
		}
	}
	if got := nodeTypeName(NodeCertificate); got != "Certificate" {
		t.Errorf("nodeTypeName = %q", got)
	}
}

// sys.certificates records no creation date, so a Creation Date criterion
// would match on a zero time and reject every row.
func TestCertificatesFilterOffersNameOnly(t *testing.T) {
	var names []string
	for _, p := range filterProps(NodeCertificates) {
		names = append(names, p.name)
	}
	if !slices.Equal(names, []string{"Name"}) {
		t.Errorf("filter properties = %v, want [Name]", names)
	}
}

// The folder offers New Certificate between New Query and Refresh; the leaf
// has Script as, Delete and Properties.
func TestCertificateMenus(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeCertificates, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Certificate...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
	leaf := &explorerNode{data: nodeData{Type: NodeCertificate, DBName: "appdb", Name: "c", conn: sc}}
	got := labelsOf(a.contextMenuItemsForNode(leaf))
	for _, want := range []string{"Script Certificate as", "Delete...", "Properties..."} {
		if !slices.Contains(got, want) {
			t.Errorf("leaf menu %q has no %q", got, want)
		}
	}
	if slices.Contains(got, "Rename...") {
		t.Errorf("leaf menu %q offers a rename SQL Server has no statement for", got)
	}
}

// CREATE and DROP only: every ALTER CERTIFICATE is a private-key operation,
// which nothing a script reads back could reproduce.
func TestCertificateScriptsAndDrops(t *testing.T) {
	items := (&App{}).scriptMenuItems(opNode(NodeCertificate, "", "c", "appdb"))
	if len(items) == 0 {
		t.Fatal("a certificate offers no Script item")
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}
	op, ok := objectOps[NodeCertificate]
	switch {
	case !ok:
		t.Fatal("NodeCertificate has no objectOps entry — Delete is not offered")
	case op.drop == nil:
		t.Error("the objectOp has no drop")
	case op.rename != nil:
		t.Error("the objectOp offers a rename")
	case op.warning == "" || !op.solo:
		t.Error("Delete must warn and must be solo")
	}
}

// certGateConn is a probed connection to appdb holding exactly the
// database-scope rights in granted, the other two of the set read 0, and
// CONTROL on certificate c1 as control says.
func certGateConn(t *testing.T, granted []string, control bool) *explorerNode {
	t.Helper()
	var denied []string
	for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY CERTIFICATE"} {
		if !slices.Contains(granted, n) {
			denied = append(denied, n)
		}
	}
	responses := withSecurableAnswers(capabilityResponses(true, nil, nil, granted, denied),
		map[string]bool{gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableCertificate, "", "c1"): control})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return &explorerNode{data: nodeData{Type: NodeCertificate, DBName: "appdb", Name: "c1", conn: sc}}
}

// TestCertificateDeleteGateMatchesWhatTheServerAllowed is the probed DROP
// column, each row one WITHOUT LOGIN user as gosmo's probe reads it back.
// ALTER on the certificate and VIEW DEFINITION on it read CONTROL 0 and hold
// nothing wider — the "ALTER on the object" row, refused Msg 15151.
func TestCertificateDeleteGateMatchesWhatTheServerAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		granted []string
		control bool
		want    bool
	}{
		{"nothing, db_securityadmin, or ALTER/VIEW DEFINITION on it", nil, false, false},
		{"CREATE CERTIFICATE, someone else's certificate", nil, false, false},
		{"CREATE CERTIFICATE, its own certificate (owner)", nil, true, true},
		{"CONTROL on the certificate", nil, true, true},
		{"ALTER ANY CERTIFICATE, or db_ddladmin", []string{"ALTER ANY CERTIFICATE"}, false, true},
		{"ALTER on the database", []string{"ALTER", "ALTER ANY CERTIFICATE"}, false, true},
		{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY CERTIFICATE"}, true, true},
	} {
		node := certGateConn(t, tc.granted, tc.control)
		var found bool
		for _, it := range (&App{}).objectOpsMenuItems(node) {
			if it.Label != "Delete..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: Delete enabled = %v, want %v", tc.name, got, tc.want)
			}
			if !tc.want && it.Note != "needs ALTER ANY CERTIFICATE" {
				t.Errorf("%s: withheld Delete's note = %q", tc.name, it.Note)
			}
		}
		if !found {
			t.Fatalf("%s: no Delete item", tc.name)
		}
	}
}

// General's one write is the owner: an untouched page writes nothing, and a
// changed Owner is one ALTER AUTHORIZATION. A certificate cannot be owned by a
// role (Msg 15345), so none is offered.
func TestCertificateGeneralPageChangesOnlyTheOwner(t *testing.T) {
	sc, inst := newFakeConn(t, append([]fakeResponse{dbScopedCredDatabaseRow(), {
		match: "FROM   sys.certificates", arg: "aaa_cert", cols: 15,
		rows: [][]driver.Value{certRow("aaa_cert", "ENCRYPTED_BY_MASTER_KEY", certExpiredExpiry)},
	}}, keyOwnerResponses()...)...)
	pages := certificatePropPages(sc, "appdb", "aaa_cert")
	form := assertOwnerChange(t, pages[0], inst, "reporting", "app_admin",
		"ALTER AUTHORIZATION ON CERTIFICATE::[aaa_cert] TO [reporting]")
	got := map[string]string{}
	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok {
			got[sr.Label()] = sr.Value()
		}
	}
	for label, want := range map[string]string{
		"Key length":     "3072",
		"Expired":        boolStr(true),
		"Protection":     "Encrypted by master key",
		"Last backed up": "Never",
	} {
		if v := got[sheetLabel(label)]; v != want {
			t.Errorf("%s = %q, want %q", label, v, want)
		}
	}
}

func TestCertificatesFolderDetail(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), certListResponse())
	node := &explorerNode{data: nodeData{Type: NodeCertificates, DBName: "appdb", conn: sc}}

	var objs []nodeData
	cols, rows, err := certificatesFolderDetail(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("certificatesFolderDetail: %v", err)
	}
	if want := []string{"Name", "Subject", "Expiry", "Expired", "Private key"}; !slices.Equal(cols, want) {
		t.Errorf("columns = %v, want %v", cols, want)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if got := rows[0][3] + "/" + rows[0][4]; got != "No/Encrypted by master key" {
		t.Errorf("aaa_cert Expired/Private key = %q", got)
	}
	if got := rows[1][3] + "/" + rows[1][4]; got != "Yes/None" {
		t.Errorf("old_cert Expired/Private key = %q", got)
	}
	for _, o := range objs {
		if o.Type != NodeCertificate || o.DBName != "appdb" {
			t.Errorf("detail object %+v", o)
		}
	}
}

func TestCertificateDetail(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), fakeResponse{
		match: "FROM   sys.certificates", arg: "aaa_cert", cols: 15,
		rows: [][]driver.Value{certRow("aaa_cert", "ENCRYPTED_BY_PASSWORD", certValidExpiry)},
	})
	node := &explorerNode{data: nodeData{Type: NodeCertificate, DBName: "appdb", Name: "aaa_cert", conn: sc}}

	_, rows, err := certificateDetail(context.Background(), sc, node)
	if err != nil {
		t.Fatalf("certificateDetail: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0]] = r[1]
	}
	for k, want := range map[string]string{
		"Owner":                      "dbo",
		"Key length":                 "3072",
		"Thumbprint":                 "0xABCD",
		"Private key":                "Encrypted by password",
		"Private key last backed up": "Never",
		"Active for BEGIN_DIALOG":    "Yes",
		"Expired":                    "No",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// CertificateByName answers ErrNotFound for a missing certificate — one
// dropped since the tree was read. The detail pane must say so, not panic.
func TestCertificateDetailOfADroppedCertificate(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), fakeResponse{
		match: "FROM   sys.certificates", arg: "gone", cols: 15,
	})
	node := &explorerNode{data: nodeData{Type: NodeCertificate, DBName: "appdb", Name: "gone", conn: sc}}

	_, _, err := certificateDetail(context.Background(), sc, node)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v, want a no-longer-exists error", err)
	}
}

// keyOwnerResponses are the principal reads an Owner row draws from: users,
// and the roles a symmetric key's row also offers.
func keyOwnerResponses() []fakeResponse {
	return []fakeResponse{
		databaseUsersResponse(),
		{match: "WHERE  r.type = 'R'", cols: 5, rows: [][]driver.Value{
			{"app_admin", int64(11), false, "dbo", nil},
		}},
	}
}

// assertOwnerChange loads a key family's General page, checks the Owner row
// shows the current owner and offers wantOffered (a role only where one may
// own the object), checks an untouched page writes nothing, then changes the
// owner and checks the one statement that lands in appdb.
func assertOwnerChange(t *testing.T, page propPage, inst *fakeInstance, wantOffered, wantAbsent, want string) *propsheet.Form {
	t.Helper()
	form, apply := loadPage(t, page, inst)
	owner := selectRow(t, form, "Owner")
	if owner.Value() != "dbo" {
		t.Errorf("Owner = %q, want dbo", owner.Value())
	}
	if !slices.Contains(owner.Items(), wantOffered) {
		t.Errorf("Owner offers %q, missing %q", owner.Items(), wantOffered)
	}
	if wantAbsent != "" && slices.Contains(owner.Items(), wantAbsent) {
		t.Errorf("Owner offers %q, which cannot own this", wantAbsent)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertNoStatementsIn(t, inst, "appdb")
	editSelect(t, form, "Owner", wantOffered)
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, []string{want}) {
		t.Errorf("statements = %q, want %q", got, want)
	}
	return form
}
