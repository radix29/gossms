package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Object Explorer wiring for a database's Asymmetric Keys folder — the
// checklist explorer_certificates_test.go runs for certificates, whose rights
// the live probe found identical to this family's.

// asymKeyRow is a sys.asymmetric_keys row in the column order gosmo scans it.
func asymKeyRow(name, keyType string) []driver.Value {
	return []driver.Value{name, int64(256), int64(1), "RSA_4096", int64(4096),
		keyType, []byte{0xAB, 0xCD}, "dbo", "", ""}
}

func asymKeyListResponse() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.asymmetric_keys",
		cols:  10,
		rows: [][]driver.Value{
			asymKeyRow("aaa_key", "ENCRYPTED_BY_MASTER_KEY"),
			asymKeyRow("pub_key", "NO_PRIVATE_KEY"),
		},
	}
}

func TestAsymmetricKeysLoaderCarriesTheDatabase(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), asymKeyListResponse())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeAsymmetricKeys](l,
		&explorerNode{data: nodeData{Type: NodeAsymmetricKeys, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadAsymmetricKeysChildren: %v", err)
	}
	if got, want := labelsOfNodes(children), []string{"aaa_key", "pub_key"}; !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeAsymmetricKey)
}

func TestAsymmetricKeyLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		if got := objectIcon(NodeAsymmetricKey, style.s); got == 0 || got == '•' {
			t.Errorf("%s: NodeAsymmetricKey has no glyph of its own (%q)", style.name, got)
		}
	}
	if got := nodeTypeName(NodeAsymmetricKey); got != "Asymmetric Key" {
		t.Errorf("nodeTypeName = %q", got)
	}
}

// sys.asymmetric_keys records no creation date.
func TestAsymmetricKeysFilterOffersNameOnly(t *testing.T) {
	var names []string
	for _, p := range filterProps(NodeAsymmetricKeys) {
		names = append(names, p.name)
	}
	if !slices.Equal(names, []string{"Name"}) {
		t.Errorf("filter properties = %v, want [Name]", names)
	}
}

func TestAsymmetricKeyMenus(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeAsymmetricKeys, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Asymmetric Key...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
	leaf := &explorerNode{data: nodeData{Type: NodeAsymmetricKey, DBName: "appdb", Name: "k", conn: sc}}
	got := labelsOf(a.contextMenuItemsForNode(leaf))
	for _, want := range []string{"Script Asymmetric Key as", "Delete...", "Properties..."} {
		if !slices.Contains(got, want) {
			t.Errorf("leaf menu %q has no %q", got, want)
		}
	}
	if slices.Contains(got, "Rename...") {
		t.Errorf("leaf menu %q offers a rename SQL Server has no statement for", got)
	}
}

func TestAsymmetricKeyScriptsAndDrops(t *testing.T) {
	items := (&App{}).scriptMenuItems(opNode(NodeAsymmetricKey, "", "k", "appdb"))
	if len(items) == 0 {
		t.Fatal("an asymmetric key offers no Script item")
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}
	op, ok := objectOps[NodeAsymmetricKey]
	switch {
	case !ok:
		t.Fatal("NodeAsymmetricKey has no objectOps entry — Delete is not offered")
	case op.drop == nil:
		t.Error("the objectOp has no drop")
	case op.rename != nil:
		t.Error("the objectOp offers a rename")
	case op.warning == "" || !op.solo:
		t.Error("Delete must warn and must be solo")
	}
}

// asymKeyGateConn is certGateConn for an asymmetric key k1.
func asymKeyGateConn(t *testing.T, granted []string, control bool) *explorerNode {
	t.Helper()
	var denied []string
	for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY ASYMMETRIC KEY"} {
		if !slices.Contains(granted, n) {
			denied = append(denied, n)
		}
	}
	responses := withSecurableAnswers(capabilityResponses(true, nil, nil, granted, denied),
		map[string]bool{gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableAsymmetricKey, "", "k1"): control})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return &explorerNode{data: nodeData{Type: NodeAsymmetricKey, DBName: "appdb", Name: "k1", conn: sc}}
}

// Step 0's DROP column, which was the same for asymmetric keys as for
// certificates.
func TestAsymmetricKeyDeleteGateMatchesWhatTheServerAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		granted []string
		control bool
		want    bool
	}{
		{"nothing, db_securityadmin, or ALTER/VIEW DEFINITION on it", nil, false, false},
		{"CREATE ASYMMETRIC KEY, someone else's key", nil, false, false},
		{"CREATE ASYMMETRIC KEY, its own key (owner)", nil, true, true},
		{"ALTER ANY ASYMMETRIC KEY, or db_ddladmin", []string{"ALTER ANY ASYMMETRIC KEY"}, false, true},
		{"ALTER on the database", []string{"ALTER", "ALTER ANY ASYMMETRIC KEY"}, false, true},
		{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY ASYMMETRIC KEY"}, true, true},
	} {
		node := asymKeyGateConn(t, tc.granted, tc.control)
		var found bool
		for _, it := range (&App{}).objectOpsMenuItems(node) {
			if it.Label != "Delete..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: Delete enabled = %v, want %v", tc.name, got, tc.want)
			}
			if !tc.want && it.Note != "needs ALTER ANY ASYMMETRIC KEY" {
				t.Errorf("%s: withheld Delete's note = %q", tc.name, it.Note)
			}
		}
		if !found {
			t.Fatalf("%s: no Delete item", tc.name)
		}
	}
}

// General's one write is the owner, as a certificate's — and like one, an
// asymmetric key cannot be owned by a role.
func TestAsymmetricKeyGeneralPageChangesOnlyTheOwner(t *testing.T) {
	sc, inst := newFakeConn(t, append([]fakeResponse{dbScopedCredDatabaseRow(), {
		match: "FROM   sys.asymmetric_keys", arg: "aaa_key", cols: 10,
		rows: [][]driver.Value{asymKeyRow("aaa_key", "ENCRYPTED_BY_PASSWORD")},
	}}, keyOwnerResponses()...)...)
	pages := asymmetricKeyPropPages(sc, "appdb", "aaa_key")
	form := assertOwnerChange(t, pages[0], inst, "reporting", "app_admin",
		"ALTER AUTHORIZATION ON ASYMMETRIC KEY::[aaa_key] TO [reporting]")
	got := map[string]string{}
	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok {
			got[sr.Label()] = sr.Value()
		}
	}
	for label, want := range map[string]string{
		"Algorithm":  "RSA_4096",
		"Key length": "4096",
		"Protection": "Encrypted by password",
		"Provider":   "SQL Server",
	} {
		if v := got[sheetLabel(label)]; v != want {
			t.Errorf("%s = %q, want %q", label, v, want)
		}
	}
}

func TestAsymmetricKeysFolderDetail(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), asymKeyListResponse())
	node := &explorerNode{data: nodeData{Type: NodeAsymmetricKeys, DBName: "appdb", conn: sc}}

	var objs []nodeData
	cols, rows, err := asymmetricKeysFolderDetail(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("asymmetricKeysFolderDetail: %v", err)
	}
	if want := []string{"Name", "Algorithm", "Length", "Private key"}; !slices.Equal(cols, want) {
		t.Errorf("columns = %v, want %v", cols, want)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if got := strings.Join(rows[1], "/"); got != "pub_key/RSA_4096/4096/None" {
		t.Errorf("pub_key row = %q", got)
	}
	for _, o := range objs {
		if o.Type != NodeAsymmetricKey || o.DBName != "appdb" {
			t.Errorf("detail object %+v", o)
		}
	}
}

// AsymmetricKeyByName answers (nil, nil) for a key dropped since the tree was
// read; the detail pane must say so, not panic.
func TestAsymmetricKeyDetailOfADroppedKey(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), fakeResponse{
		match: "FROM   sys.asymmetric_keys", arg: "gone", cols: 10,
	})
	node := &explorerNode{data: nodeData{Type: NodeAsymmetricKey, DBName: "appdb", Name: "gone", conn: sc}}

	_, _, err := asymmetricKeyDetail(context.Background(), sc, node)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v, want a no-longer-exists error", err)
	}
}
