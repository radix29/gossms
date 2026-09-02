package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the Credentials family. Each of these covers
// one entry on the per-family checklist that a new folder has to satisfy — a
// family added to the tree but missed in one of them reaches the user as a
// folder with no icon, no children, or a context menu that does nothing.

func TestSecurityFolderOffersCredentials(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeSecurity](l, &explorerNode{data: nodeData{Type: NodeSecurity, conn: sc}})
	if err != nil {
		t.Fatalf("loadSecurityChildren: %v", err)
	}
	// SSMS's order: Logins, Server Roles, Credentials, Audits, Server Audit
	// Specifications.
	want := []string{"Logins", "Server Roles", "Credentials", "Audits", "Server Audit Specifications"}
	got := make([]string, len(children))
	for i, c := range children {
		got[i] = c.label
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Security folder children = %v, want %v", got, want)
	}
	if children[2].data.Type != NodeCredentials {
		t.Errorf("the Credentials folder has type %v", children[2].data.Type)
	}
}

// A folder with no childLoaders entry expands to nothing at all, which reads
// as an empty server rather than as missing wiring.
func TestCredentialsFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeCredentials]; !ok {
		t.Fatal("NodeCredentials has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeCredentials) {
		t.Error("NodeCredentials is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeCredential) {
		t.Error("NodeCredential claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

// Both icon sets are separate functions, and a family added to one only draws
// blank in the other.
func TestCredentialLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeCredential, style.s)
		if got == 0 {
			t.Errorf("%s: NodeCredential has no glyph", style.name)
		}
		// '•' is objectIcon's fallback for a type it does not know.
		if got == '•' {
			t.Errorf("%s: NodeCredential fell through to the default bullet", style.name)
		}
	}
}

func TestCredentialTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeCredential); got != "Credential" {
		t.Errorf("nodeTypeName(NodeCredential) = %q, want %q", got, "Credential")
	}
}

// A credential must be scriptable and droppable, or its context menu offers
// nothing but Refresh.
func TestCredentialScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeCredential, "", "app_cred", ""))
	if len(items) == 0 {
		t.Fatal("a credential offers no Script item")
	}
	if items[0].Label != "Script Credential as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	got := labelsOf(items[0].Sub)
	if !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeCredential]
	if !ok {
		t.Fatal("NodeCredential has no objectOps entry — Delete is not offered")
	}
	if op.drop == nil {
		t.Error("the credential objectOp has no drop")
	}
	// A credential cannot be renamed: there is no ALTER CREDENTIAL ... WITH
	// NAME and no sp_rename class for one, so offering it would fail on click.
	if op.rename != nil {
		t.Error("the credential objectOp offers a rename SQL Server has no statement for")
	}
	if op.warning == "" {
		t.Error("the delete confirmation says nothing about what is lost with the secret")
	}
}

// The drop must address the credential the node names, by name.
func TestCredentialDropStatement(t *testing.T) {
	sc, inst := newFakeConn(t)
	if err := objectOps[NodeCredential].drop(t.Context(), sc, nodeData{Type: NodeCredential, Name: "app_cred"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 || stmts[0] != "DROP CREDENTIAL [app_cred]" {
		t.Errorf("got %v, want [DROP CREDENTIAL [app_cred]]", stmts)
	}
}

// The Details pane reads gosmo independently of the tree, so it is its own
// chance to list the wrong thing. It must name every credential and hand back
// one nodeData per row, which is what gives the pane its Delete.
func TestCredentialsFolderDetailListsEveryCredential(t *testing.T) {
	now := time.Now()
	sc, _ := newFakeConn(t, fakeResponse{
		match: "FROM   sys.credentials",
		cols:  7,
		rows: [][]driver.Value{
			{int64(1), "aaa_first_cred", `DOMAIN\svc_first`, now, now, nil, nil},
			{int64(2), "app_cred", `DOMAIN\svc_app`, now, now, nil, nil},
			{int64(4), "ekm_cred", "ekm_user", now, now, "CRYPTOGRAPHIC PROVIDER", "MyEKM"},
		},
	})

	var objs []nodeData
	cols, rows, err := credentialsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeCredentials}}, &objs)
	if err != nil {
		t.Fatalf("credentialsFolderDetail: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if len(objs) != len(rows) {
		t.Fatalf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
	}
	if objs[1].Name != "app_cred" || objs[1].Type != NodeCredential {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	kind := slices.Index(cols, "Type")
	if kind < 0 {
		t.Fatalf("no Type column in %v", cols)
	}
	// TargetType is what distinguishes the two kinds; a page reading the
	// resolved provider name instead reports "Credential" for a provider
	// credential whose provider row this login cannot see.
	if rows[1][kind] != "Credential" || rows[2][kind] != "Cryptographic Provider" {
		t.Errorf("kinds are %q and %q", rows[1][kind], rows[2][kind])
	}
	if !strings.Contains(rows[1][1], `DOMAIN\svc_app`) {
		t.Errorf("row 1 does not carry the identity: %v", rows[1])
	}
}
