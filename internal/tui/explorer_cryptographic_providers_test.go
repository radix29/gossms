package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the Cryptographic Providers folder. It is
// read-only — registering a provider needs a DLL path on the server's own
// filesystem, which SSMS answers with a file browser this build has no way to
// offer — so the checklist here is the read half of the one
// explorer_credentials_test.go runs, plus the assertions that the write half
// really is absent.

func cryptoProviderRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.cryptographic_providers",
		cols:  6,
		rows: [][]driver.Value{
			{int64(65536), "AlphaEKM", "11111111-1111-1111-1111-111111111111",
				"1.0.0.0", `C:\ekm\alpha.dll`, true},
			{int64(65537), "BetaEKM", "22222222-2222-2222-2222-222222222222",
				"2.1.0.0", `C:\ekm\beta.dll`, false},
		},
	}
}

func TestSecurityFolderOffersCryptographicProviders(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeSecurity](l, &explorerNode{data: nodeData{Type: NodeSecurity, conn: sc}})
	if err != nil {
		t.Fatalf("loadSecurityChildren: %v", err)
	}
	labels := labelsOfNodes(children)
	i := slices.Index(labels, "Cryptographic Providers")
	if i < 0 {
		t.Fatalf("Security folder = %v, with no Cryptographic Providers", labels)
	}
	// SSMS puts it directly after Credentials, which is where a reader looking
	// for a credential's provider binding goes next.
	if labels[i-1] != "Credentials" {
		t.Errorf("Cryptographic Providers follows %q, want Credentials", labels[i-1])
	}
	if children[i].data.Type != NodeCryptographicProviders {
		t.Errorf("the folder has type %v", children[i].data.Type)
	}
}

func TestCryptographicProvidersFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeCryptographicProviders]; !ok {
		t.Fatal("NodeCryptographicProviders has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeCryptographicProviders) {
		t.Error("NodeCryptographicProviders is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeCryptographicProvider) {
		t.Error("NodeCryptographicProvider claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

// A disabled provider decrypts nothing, and nothing else in the row says so.
func TestCryptographicProvidersLoaderLabelsADisabledProvider(t *testing.T) {
	sc, _ := newFakeConn(t, cryptoProviderRows())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeCryptographicProviders](l,
		&explorerNode{data: nodeData{Type: NodeCryptographicProviders, conn: sc}})
	if err != nil {
		t.Fatalf("loadCryptographicProvidersChildren: %v", err)
	}
	want := []string{"AlphaEKM", "BetaEKM (Disabled)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	// The label carries the decoration; the name the pane and any later read
	// address the provider by must not.
	if children[1].data.Name != "BetaEKM" {
		t.Errorf("the disabled provider's Name is %q, want BetaEKM", children[1].data.Name)
	}
	if children[1].data.IsEnabled {
		t.Error("the disabled provider's node claims to be enabled")
	}
}

// A server with no EKM provider registered is the ordinary case: the folder
// comes up empty, not with an error.
func TestCryptographicProvidersFolderIsEmptyWithoutAProvider(t *testing.T) {
	sc, _ := newFakeConn(t, fakeResponse{match: "FROM   sys.cryptographic_providers", cols: 6})
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeCryptographicProviders](l,
		&explorerNode{data: nodeData{Type: NodeCryptographicProviders, conn: sc}})
	if err != nil {
		t.Fatalf("a server with no provider registered is an error: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("children = %v, want none", labelsOfNodes(children))
	}
}

func TestCryptographicProviderLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeCryptographicProvider, style.s)
		if got == 0 {
			t.Errorf("%s: NodeCryptographicProvider has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeCryptographicProvider fell through to the default bullet", style.name)
		}
	}
}

func TestCryptographicProviderTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeCryptographicProvider); got != "Cryptographic Provider" {
		t.Errorf("nodeTypeName = %q", got)
	}
}

// The read-only half of the checklist, asserted rather than assumed: an
// objectOps entry would put Delete on the menu for a statement this build
// never sends, and a scriptable entry would offer a CREATE CRYPTOGRAPHIC
// PROVIDER gosmo has no writer for.
func TestCryptographicProviderOffersNoWrites(t *testing.T) {
	if _, ok := objectOps[NodeCryptographicProvider]; ok {
		t.Error("NodeCryptographicProvider has an objectOps entry — Delete would be offered")
	}
	if _, ok := scriptables[NodeCryptographicProvider]; ok {
		t.Error("NodeCryptographicProvider is scriptable — the verbs have no gosmo writer behind them")
	}
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, nt := range []NodeType{NodeCryptographicProviders, NodeCryptographicProvider} {
		node := &explorerNode{data: nodeData{Type: nt, conn: sc}}
		labels := labelsOf(a.nodeMenuItems(node))
		if slices.Contains(labels, "Properties...") {
			t.Errorf("%v offers Properties, a dialog this family has no page set for: %v", nt, labels)
		}
		// The menu still has to be more than nothing, or the node reads as
		// broken wiring rather than as a read-only family.
		if !slices.Contains(labels, refreshMenuLabel) {
			t.Errorf("%v has no Refresh: %v", nt, labels)
		}
	}
}

// The Details pane reads gosmo independently of the tree, so it is its own
// chance to list the wrong thing — including claiming a state the row does not
// have.
func TestCryptographicProvidersFolderDetailListsEveryProvider(t *testing.T) {
	sc, _ := newFakeConn(t, cryptoProviderRows())

	var objs []nodeData
	cols, rows, err := cryptographicProvidersFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeCryptographicProviders}}, &objs)
	if err != nil {
		t.Fatalf("cryptographicProvidersFolderDetail: %v", err)
	}
	if len(rows) != 2 || len(objs) != 2 {
		t.Fatalf("got %d rows and %d row objects, want 2 of each", len(rows), len(objs))
	}
	state := slices.Index(cols, "State")
	if state < 0 {
		t.Fatalf("no State column in %v", cols)
	}
	if rows[0][state] == rows[1][state] {
		t.Errorf("the enabled and disabled providers report the same state %q", rows[0][state])
	}
	if objs[1].Name != "BetaEKM" || objs[1].Type != NodeCryptographicProvider {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	path := slices.Index(cols, "DLL path")
	if path < 0 || rows[0][path] != `C:\ekm\alpha.dll` {
		t.Errorf("the DLL path is missing from %v / %v", cols, rows[0])
	}
}

// The object view answers from the same list read — there is no by-name read
// for a provider — so it has to pick out the row it was opened on rather than
// the first.
func TestCryptographicProviderDetailShowsTheSelectedProvider(t *testing.T) {
	sc, _ := newFakeConn(t, cryptoProviderRows())

	_, rows, err := cryptographicProviderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeCryptographicProvider, Name: "BetaEKM"}})
	if err != nil {
		t.Fatalf("cryptographicProviderDetail: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r[0] == "Version" {
			found = true
			if r[1] != "2.1.0.0" {
				t.Errorf("Version = %q, want the selected provider's 2.1.0.0", r[1])
			}
		}
	}
	if !found {
		t.Errorf("no Version row in %v", rows)
	}

	// A provider dropped between the tree read and this one is an error
	// naming it, not an empty grid that reads as a provider with no
	// properties.
	if _, _, err := cryptographicProviderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeCryptographicProvider, Name: "GoneEKM"}}); err == nil {
		t.Error("a provider that no longer exists produced no error")
	}
}
