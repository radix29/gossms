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

// The Object Explorer wiring for a database's Symmetric Keys folder, read
// side only: the folder, Details, filter, Script as and a read-only General
// page — and the write side's Delete and New.

var symKeyCreated = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

// symKeyRow is one (key, encryption) row of gosmo's symmetric-key read, in its
// scan order; desc nil is a key with no encryption row.
func symKeyRow(name string, id int64, desc, encryptor any) []driver.Value {
	return []driver.Value{name, id, int64(1), "dbo", "AES_256", int64(256),
		"0B7E6C1A-3F2D-4E5B-9A8C-1D2E3F4A5B6C", symKeyCreated, symKeyCreated, "",
		desc, []byte{0x01}, encryptor}
}

func symKeyListResponse() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.symmetric_keys",
		cols:  13,
		rows: [][]driver.Value{
			symKeyRow("aaa_key", 256, "ENCRYPTION BY CERTIFICATE OAEP256", "claims_cert"),
			symKeyRow("aaa_key", 256, "ENCRYPTION BY PASSWORD V2", nil),
			symKeyRow("hidden_key", 257, "ENCRYPTION BY ASYMMETRIC KEY", nil),
		},
	}
}

func masterKeyResponse(has bool) fakeResponse {
	n := int64(0)
	if has {
		n = 1
	}
	return fakeResponse{match: "is_master_key_encrypted_by_server", cols: 1, rows: [][]driver.Value{{n}}}
}

// dmkReadResponses answer gosmo's MasterKey read: its sys.symmetric_keys row
// (none when !has) and the database's is_master_key_encrypted_by_server. Placed
// before symKeyListResponse, whose match is a substring of the same query.
func dmkReadResponses(has, byServer bool) []fakeResponse {
	var rows [][]driver.Value
	if has {
		rows = [][]driver.Value{
			symKeyRow("##MS_DatabaseMasterKey##", 101, "ENCRYPTION BY MASTER KEY", nil),
			symKeyRow("##MS_DatabaseMasterKey##", 101, "ENCRYPTION BY PASSWORD V2", nil),
		}
	}
	return []fakeResponse{
		{match: "k.name = N'##MS_DatabaseMasterKey##'", cols: 13, rows: rows},
		{match: "SELECT is_master_key_encrypted_by_server", cols: 1, rows: [][]driver.Value{{byServer}}},
	}
}

func TestSymmetricKeysLoaderCarriesTheDatabaseAndCreateDate(t *testing.T) {
	for _, has := range []bool{false, true} {
		sc, _ := newFakeConn(t, append(append([]fakeResponse{dbScopedCredDatabaseRow()},
			dmkReadResponses(has, true)...), symKeyListResponse())...)
		l := loaderCtx{ctx: context.Background(), sc: sc}

		children, err := childLoaders[NodeSymmetricKeys](l,
			&explorerNode{data: nodeData{Type: NodeSymmetricKeys, DBName: "appdb", conn: sc}})
		if err != nil {
			t.Fatalf("loadSymmetricKeysChildren: %v", err)
		}
		want := []string{"aaa_key", "hidden_key"}
		if has {
			// The master key leads the folder, and only when it exists.
			want = append([]string{masterKeyNodeLabel}, want...)
			if mk := children[0]; mk.data.Type != NodeMasterKey || mk.data.DBName != "appdb" {
				t.Errorf("first child = %+v, want the master key node in appdb", mk.data)
			}
			children = children[1:]
		}
		if got := labelsOfNodes(children); !slices.Equal(got, want[len(want)-2:]) {
			t.Fatalf("has master key %v: children = %v, want %v", has, got, want)
		}
		assertCarriesDatabase(t, children, NodeSymmetricKey)
		for _, c := range children {
			if !c.data.CreateDate.Equal(symKeyCreated) {
				t.Errorf("%s: CreateDate = %v — the Creation Date filter would reject it", c.label, c.data.CreateDate)
			}
		}
	}
}

func TestSymmetricKeyLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		if got := objectIcon(NodeSymmetricKey, style.s); got == 0 || got == '•' {
			t.Errorf("%s: NodeSymmetricKey has no glyph of its own (%q)", style.name, got)
		}
	}
	if got := nodeTypeName(NodeSymmetricKey); got != "Symmetric Key" {
		t.Errorf("nodeTypeName = %q", got)
	}
}

// sys.symmetric_keys is the one key-management view with a create_date.
func TestSymmetricKeysFilterOffersNameAndCreationDate(t *testing.T) {
	var names []string
	for _, p := range filterProps(NodeSymmetricKeys) {
		names = append(names, p.name)
	}
	if !slices.Equal(names, []string{"Name", "Creation Date"}) {
		t.Errorf("filter properties = %v, want [Name Creation Date]", names)
	}
}

// New on the folder, Delete on the leaf, and never a Rename, which SQL Server
// has no statement for.
func TestSymmetricKeyMenus(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeSymmetricKeys, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Symmetric Key...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
	leaf := &explorerNode{data: nodeData{Type: NodeSymmetricKey, DBName: "appdb", Name: "k", conn: sc}}
	got := labelsOf(a.contextMenuItemsForNode(leaf))
	for _, want := range []string{"Script Symmetric Key as", "Delete...", "Properties..."} {
		if !slices.Contains(got, want) {
			t.Errorf("leaf menu %q has no %q", got, want)
		}
	}
	if slices.Contains(got, "Rename...") {
		t.Errorf("leaf menu %q offers a rename SQL Server has no statement for", got)
	}
}

func TestSymmetricKeyDropsThroughTheRef(t *testing.T) {
	op, ok := objectOps[NodeSymmetricKey]
	switch {
	case !ok:
		t.Fatal("NodeSymmetricKey has no objectOps entry — Delete is not offered")
	case op.drop == nil:
		t.Fatal("the objectOp has no drop")
	case op.rename != nil:
		t.Error("the objectOp offers a rename")
	case !op.solo:
		t.Error("Delete must be solo")
	case !strings.Contains(op.warning, "cannot be decrypted again") || !strings.Contains(op.warning, "KEY_SOURCE"):
		t.Errorf("warning %q does not say the data is lost, or name the KEY_SOURCE exception", op.warning)
	}
	sc, rec := newFakeConn(t)
	if err := op.drop(context.Background(), sc, nodeData{Type: NodeSymmetricKey, DBName: "appdb", Name: "k]1"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got := rec.StatementsIn("appdb"); !slices.Equal(got, []string{"DROP SYMMETRIC KEY [k]]1]"}) {
		t.Errorf("drop ran %q in appdb", got)
	}
}

// symKeyGateConn is asymKeyGateConn for a symmetric key k1.
func symKeyGateConn(t *testing.T, granted []string, control bool) *explorerNode {
	t.Helper()
	var denied []string
	for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY SYMMETRIC KEY"} {
		if !slices.Contains(granted, n) {
			denied = append(denied, n)
		}
	}
	responses := withSecurableAnswers(capabilityResponses(true, nil, nil, granted, denied),
		map[string]bool{gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableSymmetricKey, "", "k1"): control})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return &explorerNode{data: nodeData{Type: NodeSymmetricKey, DBName: "appdb", Name: "k1", conn: sc}}
}

// Step 0's DROP column, which was the same for all three families.
func TestSymmetricKeyDeleteGateMatchesWhatTheServerAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		granted []string
		control bool
		want    bool
	}{
		{"nothing, db_securityadmin, or ALTER/VIEW DEFINITION on it", nil, false, false},
		{"CREATE SYMMETRIC KEY, someone else's key", nil, false, false},
		{"CREATE SYMMETRIC KEY, its own key (owner)", nil, true, true},
		{"ALTER ANY SYMMETRIC KEY, or db_ddladmin", []string{"ALTER ANY SYMMETRIC KEY"}, false, true},
		{"ALTER on the database", []string{"ALTER", "ALTER ANY SYMMETRIC KEY"}, false, true},
		{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY SYMMETRIC KEY"}, true, true},
	} {
		node := symKeyGateConn(t, tc.granted, tc.control)
		var found bool
		for _, it := range (&App{}).objectOpsMenuItems(node) {
			if it.Label != "Delete..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: Delete enabled = %v, want %v", tc.name, got, tc.want)
			}
			if !tc.want && it.Note != "needs ALTER ANY SYMMETRIC KEY" {
				t.Errorf("%s: withheld Delete's note = %q", tc.name, it.Note)
			}
		}
		if !found {
			t.Fatalf("%s: no Delete item", tc.name)
		}
	}
}

func TestSymmetricKeyScriptVerbs(t *testing.T) {
	items := (&App{}).scriptMenuItems(opNode(NodeSymmetricKey, "", "k", "appdb"))
	if len(items) == 0 {
		t.Fatal("a symmetric key offers no Script item")
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}
}

// General is read-only; the encryptions are the Encryption page's
// (symmetric_key_props_encryption_page_test.go), so General no longer lists them.
// General's one write is the owner; unlike a certificate, a symmetric key may
// be owned by a role, so the roles are offered too.
func TestSymmetricKeyGeneralPageChangesOnlyTheOwner(t *testing.T) {
	sc, inst := newFakeConn(t, append([]fakeResponse{dbScopedCredDatabaseRow(), {
		match: "FROM   sys.symmetric_keys", arg: "aaa_key", cols: 13,
		rows: symKeyListResponse().rows[:2],
	}}, keyOwnerResponses()...)...)
	pages := symmetricKeyPropPages(sc, "appdb", "aaa_key")
	if titles := []string{pages[0].title, pages[1].title}; !slices.Equal(titles, []string{"General", "Encryption"}) {
		t.Fatalf("pages = %q", titles)
	}
	form := assertOwnerChange(t, pages[0], inst, "app_admin", "",
		"ALTER AUTHORIZATION ON SYMMETRIC KEY::[aaa_key] TO [app_admin]")
	got := map[string]string{}
	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok {
			got[sr.Label()] = sr.Value()
		}
	}
	for label, want := range map[string]string{
		"Algorithm":  "AES_256",
		"Key length": "256",
		"Provider":   "SQL Server",
	} {
		if v := got[sheetLabel(label)]; v != want {
			t.Errorf("%s = %q, want %q", label, v, want)
		}
	}
	if _, ok := got[sheetLabel("Encrypted by")]; ok {
		t.Error("General still lists the encryptions; they moved to the Encryption page")
	}
}

// The folder's first row is the master-key line, and it is not an object:
// its rowObjs entry must be one no Delete or Script can act on, or step 11's
// batch Delete would treat it as a key.
func TestSymmetricKeysFolderDetail(t *testing.T) {
	for _, has := range []bool{true, false} {
		sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), symKeyListResponse(), masterKeyResponse(has))
		node := &explorerNode{data: nodeData{Type: NodeSymmetricKeys, DBName: "appdb", conn: sc}}

		var objs []nodeData
		cols, rows, err := symmetricKeysFolderDetail(context.Background(), sc, node, &objs)
		if err != nil {
			t.Fatalf("symmetricKeysFolderDetail: %v", err)
		}
		if want := []string{"Name", "Algorithm", "Length", "Created", "Encrypted by"}; !slices.Equal(cols, want) {
			t.Errorf("columns = %v, want %v", cols, want)
		}
		if len(rows) != 3 || len(objs) != 3 {
			t.Fatalf("rows = %v, objs = %v", rows, objs)
		}
		wantDMK := "Database master key: absent"
		if has {
			wantDMK = "Database master key: present"
		}
		if rows[0][0] != wantDMK || objs[0] != (nodeData{}) || objectOpFor(objs[0].Type) != nil {
			t.Errorf("master-key row = %q / %+v", rows[0], objs[0])
		}
		if got := rows[1][4]; got != "Certificate claims_cert, Password" {
			t.Errorf("aaa_key Encrypted by = %q", got)
		}
		if got := rows[2][4]; got != "Asymmetric key (not visible)" {
			t.Errorf("hidden_key Encrypted by = %q", got)
		}
		for _, o := range objs[1:] {
			if o.Type != NodeSymmetricKey || o.DBName != "appdb" {
				t.Errorf("detail object %+v", o)
			}
		}
	}
}

// SymmetricKeyByName answers ErrNotFound for a key dropped since the tree was
// read; the detail pane shows that error rather than an empty view.
func TestSymmetricKeyDetailOfADroppedKey(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), fakeResponse{
		match: "FROM   sys.symmetric_keys", arg: "gone", cols: 13,
	})
	node := &explorerNode{data: nodeData{Type: NodeSymmetricKey, DBName: "appdb", Name: "gone", conn: sc}}

	_, _, err := symmetricKeyDetail(context.Background(), sc, node)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want a not-found error", err)
	}
}
