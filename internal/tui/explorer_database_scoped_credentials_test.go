package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the Database Scoped Credentials family, on
// the same per-family checklist explorer_credentials_test.go runs for the
// server-level one: a family added to the tree but missed in one of these
// reaches the user as a folder with no icon, no children, or a context menu
// that does nothing.

func dbScopedCredListResponse() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.database_scoped_credentials",
		cols:  5,
		rows: [][]driver.Value{
			{int64(1), "aaa_first_cred", "identity_first", dbScopedCredCreated, dbScopedCredCreated},
			{int64(2), "app_cred", "SHARED ACCESS SIGNATURE", dbScopedCredCreated, dbScopedCredCreated},
			{int64(3), "zzz_last_cred", "identity_last", dbScopedCredCreated, dbScopedCredCreated},
		},
	}
}

func TestDatabaseSecurityFolderOffersScopedCredentials(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeDatabaseSecurity](l,
		&explorerNode{data: nodeData{Type: NodeDatabaseSecurity, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseSecurityChildren: %v", err)
	}
	labels := labelsOfNodes(children)
	i := slices.Index(labels, "Database Scoped Credentials")
	if i < 0 {
		t.Fatalf("a database's Security folder = %v, with no Database Scoped Credentials", labels)
	}
	if children[i].data.Type != NodeDatabaseScopedCredentials {
		t.Errorf("the folder has type %v", children[i].data.Type)
	}
	// The folder has to carry its database, or every read under it runs
	// against the connection's default.
	if children[i].data.DBName != "appdb" {
		t.Errorf("the folder's DBName is %q, want appdb", children[i].data.DBName)
	}
}

// A folder with no childLoaders entry expands to nothing at all, which reads
// as an empty database rather than as missing wiring.
func TestDatabaseScopedCredentialsFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeDatabaseScopedCredentials]; !ok {
		t.Fatal("NodeDatabaseScopedCredentials has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeDatabaseScopedCredentials) {
		t.Error("NodeDatabaseScopedCredentials is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeDatabaseScopedCredential) {
		t.Error("NodeDatabaseScopedCredential claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

// The loader has to name every credential and carry the database onto each
// leaf: without DBName the Properties page and the drop both run against the
// connection's default database.
func TestDatabaseScopedCredentialsLoaderCarriesTheDatabase(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), dbScopedCredListResponse())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeDatabaseScopedCredentials](l,
		&explorerNode{data: nodeData{Type: NodeDatabaseScopedCredentials, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseScopedCredentialsChildren: %v", err)
	}
	want := []string{"aaa_first_cred", "app_cred", "zzz_last_cred"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	for _, n := range children {
		if n.data.Type != NodeDatabaseScopedCredential {
			t.Errorf("%q is typed %v", n.label, n.data.Type)
		}
		if n.data.DBName != "appdb" {
			t.Errorf("%q carries DBName %q, want appdb", n.label, n.data.DBName)
		}
	}
}

// Both icon sets are separate functions, and a family added to one only draws
// blank in the other.
func TestDatabaseScopedCredentialLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeDatabaseScopedCredential, style.s)
		if got == 0 {
			t.Errorf("%s: NodeDatabaseScopedCredential has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeDatabaseScopedCredential fell through to the default bullet", style.name)
		}
	}
}

func TestDatabaseScopedCredentialTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeDatabaseScopedCredential); got != "Database Scoped Credential" {
		t.Errorf("nodeTypeName = %q", got)
	}
}

// The family must be scriptable and droppable, or its context menu offers
// nothing but Refresh. No ALTER verb: the secret is unreadable, so an ALTER
// script would carry a placeholder rather than the credential's own value —
// the same reason the server-level credential has none.
func TestDatabaseScopedCredentialScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeDatabaseScopedCredential, "", "app_cred", ""))
	if len(items) == 0 {
		t.Fatal("a database scoped credential offers no Script item")
	}
	if items[0].Label != "Script Database Scoped Credential as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeDatabaseScopedCredential]
	if !ok {
		t.Fatal("NodeDatabaseScopedCredential has no objectOps entry — Delete is not offered")
	}
	if op.drop == nil {
		t.Error("the objectOp has no drop")
	}
	// There is no ALTER DATABASE SCOPED CREDENTIAL ... WITH NAME and no
	// sp_rename class for one, so offering a rename would fail on click.
	if op.rename != nil {
		t.Error("the objectOp offers a rename SQL Server has no statement for")
	}
	if op.warning == "" {
		t.Error("the delete confirmation says nothing about what is lost with the secret")
	}
}

// The drop must address the credential the node names, in the database the
// node names — StatementsIn, because the bare USE is stripped as plumbing and
// with it the only record of where the DROP landed.
func TestDatabaseScopedCredentialDropStatement(t *testing.T) {
	sc, inst := newFakeConn(t, dbScopedCredDatabaseRow())
	err := objectOps[NodeDatabaseScopedCredential].drop(t.Context(), sc,
		nodeData{Type: NodeDatabaseScopedCredential, DBName: "appdb", Name: "app_cred"})
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "DROP DATABASE SCOPED CREDENTIAL [app_cred]")
	assertNoStatementsIn(t, inst, "master")
}

// The Details pane reads gosmo independently of the tree, so it is its own
// chance to list the wrong thing. It must name every credential and hand back
// one nodeData per row, which is what gives the pane its Delete — each
// carrying the database, or the pane's Delete runs somewhere else.
func TestDatabaseScopedCredentialsFolderDetailListsEveryCredential(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(), dbScopedCredListResponse())

	var objs []nodeData
	cols, rows, err := databaseScopedCredentialsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeDatabaseScopedCredentials, DBName: "appdb"}}, &objs)
	if err != nil {
		t.Fatalf("databaseScopedCredentialsFolderDetail: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if len(objs) != len(rows) {
		t.Fatalf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
	}
	if objs[1].Name != "app_cred" || objs[1].Type != NodeDatabaseScopedCredential {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	if objs[1].DBName != "appdb" {
		t.Errorf("row object 1 carries DBName %q, want appdb", objs[1].DBName)
	}
	ident := slices.Index(cols, "Identity")
	if ident < 0 {
		t.Fatalf("no Identity column in %v", cols)
	}
	if !strings.Contains(rows[1][ident], "SHARED ACCESS SIGNATURE") {
		t.Errorf("row 1 does not carry the identity: %v", rows[1])
	}
	// Nothing on the grid may claim to carry the secret — it cannot be read.
	for _, c := range cols {
		if strings.Contains(strings.ToLower(c), "secret") || strings.Contains(strings.ToLower(c), "password") {
			t.Errorf("the grid has a %q column for a value the catalog never exposes", c)
		}
	}
}

// The folder's Object Explorer filter has to exist in both halves — the tree's
// and the pane's. Declaring no filter properties would leave the folder's
// Filter menu empty while the pane happily applies one.
func TestDatabaseScopedCredentialsFolderIsFilterable(t *testing.T) {
	props := filterProps(NodeDatabaseScopedCredentials)
	if len(props) == 0 {
		t.Fatal("the folder declares no filter properties")
	}
	var names []string
	for _, p := range props {
		names = append(names, p.name)
	}
	if !slices.Contains(names, "Name") {
		t.Errorf("filter properties = %v, with no Name", names)
	}
}
