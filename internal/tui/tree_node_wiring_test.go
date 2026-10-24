package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// A1: adding an Object Explorer family means touching a dozen switches and
// maps. nodeKinds (tree_node_kinds.go) holds what a type is, one row per
// type; this test checks every row is filled and agrees with the tables that
// stay in feature files — childLoaders and filterProps. A new NodeType fails
// it by construction: its row is zero.
//
// It does not check what the row *says* — that a queue's glyph is a queue —
// only that each type is wired at all. TestNewFamilyLeavesAreNamed and its
// siblings pin the values for the families they cover.

// filterableFolderSet is every folder whose Filter menu offers something. A
// folder not listed declares no filter properties: every folder of folders,
// and the few object folders that deliberately offer no filter (Columns,
// Keys, Indexes, Statistics, Checks, and the Agent and Always On folders).
var filterableFolderSet = map[NodeType]bool{
	NodeDatabases:                   true,
	NodeSystemDatabases:             true,
	NodeDatabaseSnapshots:           true,
	NodeTables:                      true,
	NodeSystemTables:                true,
	NodeFileTables:                  true,
	NodeExternalTables:              true,
	NodeGraphTables:                 true,
	NodeViews:                       true,
	NodeSystemViews:                 true,
	NodeStoredProcedures:            true,
	NodeSystemProcedures:            true,
	NodeFunctions:                   true,
	NodeSystemFunctions:             true,
	NodeLogins:                      true,
	NodeServerRoles:                 true,
	NodeCredentials:                 true,
	NodeAudits:                      true,
	NodeServerAuditSpecifications:   true,
	NodeBackupDevices:               true,
	NodeServerTriggers:              true,
	NodeEndpoints:                   true,
	NodeEventSessions:               true,
	NodeUsers:                       true,
	NodeDatabaseRoles:               true,
	NodeSchemas:                     true,
	NodeTriggers:                    true,
	NodeDatabaseTriggers:            true,
	NodeSequences:                   true,
	NodeSynonyms:                    true,
	NodeSystemDataTypes:             true,
	NodeUserDefinedDataTypes:        true,
	NodeUserDefinedTableTypes:       true,
	NodeUserDefinedTypes:            true,
	NodeXMLSchemaCollections:        true,
	NodeAssemblies:                  true,
	NodeRules:                       true,
	NodeDefaults:                    true,
	NodePlanGuides:                  true,
	NodeMessageTypes:                true,
	NodeContracts:                   true,
	NodeBrokerQueues:                true,
	NodeBrokerServices:              true,
	NodeRoutes:                      true,
	NodeRemoteServiceBindings:       true,
	NodeBrokerPriorities:            true,
	NodeExternalDataSources:         true,
	NodeExternalFileFormats:         true,
	NodeExternalLibraries:           true,
	NodePartitionFunctions:          true,
	NodePartitionSchemes:            true,
	NodeDatabaseAuditSpecifications: true,
	NodeDatabaseScopedCredentials:   true,
	NodeAsymmetricKeys:              true,
	NodeCertificates:                true,
	NodeSymmetricKeys:               true,
	NodeSecurityPolicies:            true,
	NodeColumnMasterKeys:            true,
	NodeColumnEncryptionKeys:        true,
	NodeResourcePools:               true,
	NodeWorkloadGroups:              true,
	NodeExternalResourcePools:       true,
	NodeFullTextCatalogs:            true,
	NodeFullTextStoplists:           true,
	NodeSearchPropertyLists:         true,
}

// portableBullet is the types whose Portable glyph is the shared bullet on
// purpose: '⁞' is not guaranteed to render everywhere (see objectIconPortable).
var portableBullet = map[NodeType]bool{
	NodeColumn:     true,
	NodeViewColumn: true,
}

// nodeTypeNames reads the constant names out of tree_node.go's iota block, in
// order, so a failure here names the type rather than its number. Parsed
// rather than listed: a hand-written list of 163 names is the very thing this
// test exists to stop relying on.
func nodeTypeNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "tree_node.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing tree_node.go: %v", err)
	}
	var names []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 {
				continue
			}
			if n := vs.Names[0].Name; n == "nodeTypeCount" {
				return names
			} else if len(names) > 0 || n == "NodeServer" {
				names = append(names, n)
			}
		}
	}
	return names
}

func TestEveryNodeTypeIsWired(t *testing.T) {
	if nodeTypeCount == 0 {
		t.Fatal("nodeTypeCount is 0; the sentinel is not last in the const block")
	}
	names := nodeTypeNames(t)
	if len(names) != int(nodeTypeCount) {
		t.Fatalf("tree_node.go declares %d NodeType constants before nodeTypeCount, which is %d — "+
			"the sentinel is not last, or a constant is declared elsewhere", len(names), nodeTypeCount)
	}
	// Safe to index: the count check above pins len(names) to nodeTypeCount.
	name := func(nt NodeType) string { return names[nt] }
	objectNames := map[string]NodeType{}
	for nt := range nodeTypeCount {
		k := nodeKinds[nt]
		if k.class == 0 {
			t.Errorf("%s has no nodeKinds row — a new type is wired nowhere until it has one; "+
				"add it, and check every table its class implies", name(nt))
			continue
		}
		if k.name == "" {
			t.Errorf("%s has no name in nodeKinds — the Details pane's Type row would be blank", name(nt))
		}
		_, hasLoader := childLoaders[nt]
		props := filterProps(nt)
		if k.class != kindFolder {
			if filterableFolderSet[nt] {
				t.Errorf("%s is listed in filterableFolderSet but is not a folder", name(nt))
			}
			if len(props) != 0 {
				t.Errorf("%s declares filter properties but is not a folder", name(nt))
			}
			// Two object types sharing a noun read as one type in the Details pane;
			// folders legitimately share labels ("Triggers", "Security").
			if prev, dup := objectNames[k.name]; dup {
				t.Errorf("%s and %s are both named %q", name(prev), name(nt), k.name)
			}
			objectNames[k.name] = nt
		}
		switch k.class {
		case kindFolder:
			if !hasLoader {
				t.Errorf("%s is a folder with no childLoaders entry — it expands to nothing", name(nt))
			}
			if k.emoji != 0 || k.symbol != 0 {
				t.Errorf("%s is a folder with object glyphs — nodeIcon never draws them", name(nt))
			}
			if filterableFolderSet[nt] {
				if len(props) == 0 {
					t.Errorf("%s is listed filterable and declares no filter properties — "+
						"its Filter menu offers nothing", name(nt))
					break
				}
				var propNames []string
				for _, p := range props {
					propNames = append(propNames, p.name)
				}
				if !slices.Contains(propNames, "Name") {
					t.Errorf("%s filter properties = %v, with no Name", name(nt), propNames)
				}
			} else if len(props) != 0 {
				t.Errorf("%s declares filter properties but is not in filterableFolderSet — "+
					"add it so the Name check applies to it", name(nt))
			}
		case kindLeaf, kindExpandable:
			if expandable := k.class == kindExpandable; hasLoader != expandable {
				t.Errorf("%s: childLoaders entry = %v, expandable = %v — an "+
					"expandable object with no loader shows an arrow that leads nowhere, and a "+
					"leaf with one never shows its children", name(nt), hasLoader, expandable)
			}
			assertGlyphs(t, name(nt), nt)
		case kindPlaceholder:
			if hasLoader {
				t.Errorf("%s is a placeholder with a childLoaders entry", name(nt))
			}
			assertGlyphs(t, name(nt), nt)
		default:
			t.Errorf("%s has class %d, which is none of the four", name(nt), k.class)
		}
	}
}

// assertGlyphs checks every icon style answers for nt with its own glyph, not
// the shared bullet objectIcon falls back to — which is a glyph, and so
// invisible to a nil check.
func assertGlyphs(t *testing.T, name string, nt NodeType) {
	t.Helper()
	styles := []struct {
		name   string
		s      config.IconStyle
		bullet bool // whether the shared bullet is allowed here
	}{
		{"Emoji", config.IconStyleEmoji, false},
		{"Symbols", config.IconStyleSymbols, false},
		{"Portable", config.IconStylePortable, portableBullet[nt]},
	}
	for _, style := range styles {
		got := objectIcon(nt, style.s)
		if got == 0 {
			t.Errorf("%s: %s has no glyph", style.name, name)
			continue
		}
		if got == '•' && !style.bullet {
			t.Errorf("%s: %s fell through to the default bullet", style.name, name)
		}
	}
}
