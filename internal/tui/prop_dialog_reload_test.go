package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// A Properties dialog that can rename its object, or change a state its label
// or icon carries, reloads the folder listing it after an Apply — or the tree
// goes on naming the old object, and every menu action on the node with it.
// The same folder type elsewhere (another database, another table) is left
// alone.
func TestPropertiesApplyReloadsTheFolderListingTheObject(t *testing.T) {
	const dbName = "appdb"
	for _, tc := range []struct {
		name  string
		open  func(a *App, sc *db.ServerConn)
		want  []nodeData // folders that must reload
		other []nodeData // folders that must not
	}{
		{"Login", func(a *App, sc *db.ServerConn) { a.showLoginProperties(sc, "bob") },
			[]nodeData{{Type: NodeLogins}}, []nodeData{{Type: NodeServerRoles}}},
		{"Database User", func(a *App, sc *db.ServerConn) { a.showUserPropertiesFor(sc, dbName, "bob") },
			[]nodeData{{Type: NodeUsers, DBName: dbName}}, []nodeData{{Type: NodeUsers, DBName: "otherdb"}}},
		{"Audit", func(a *App, sc *db.ServerConn) { a.showAuditPropertiesFor(sc, "aud") },
			[]nodeData{{Type: NodeAudits}}, []nodeData{{Type: NodeServerAuditSpecifications}}},
		{"Job", func(a *App, sc *db.ServerConn) { a.showJobPropertiesFor(sc, "job") },
			[]nodeData{{Type: NodeAgentUserJobs}, {Type: NodeAgentSystemJobs}}, []nodeData{{Type: NodeAgentSchedules}}},
		{"Alert", func(a *App, sc *db.ServerConn) { a.showAlertProperties(sc, "al") },
			[]nodeData{{Type: NodeAgentEventAlerts}}, []nodeData{{Type: NodeAgentOperators}}},
		{"Operator", func(a *App, sc *db.ServerConn) { a.showOperatorProperties(sc, "op") },
			[]nodeData{{Type: NodeAgentOperators}}, []nodeData{{Type: NodeAgentEventAlerts}}},
		{"Schedule", func(a *App, sc *db.ServerConn) { a.showScheduleProperties(sc, agentScheduleID, "sch") },
			[]nodeData{{Type: NodeAgentSchedules}}, []nodeData{{Type: NodeAgentUserJobs}}},
		{"Key", func(a *App, sc *db.ServerConn) { a.showKeyPropertiesFor(sc, dbName, "dbo", "T", "PK_T", true) },
			[]nodeData{
				{Type: NodeKeys, DBName: dbName, Schema: "dbo", Name: "T"},
				{Type: NodeIndexes, DBName: dbName, Schema: "dbo", Name: "T"},
			},
			[]nodeData{
				{Type: NodeKeys, DBName: dbName, Schema: "dbo", Name: "U"},
				{Type: NodeIndexes, DBName: dbName, Schema: "sales", Name: "T"},
			}},
		{"Plan Guide", func(a *App, sc *db.ServerConn) { a.showPlanGuidePropertiesFor(sc, dbName, "pg", "", "") },
			[]nodeData{{Type: NodePlanGuides, DBName: dbName}}, []nodeData{{Type: NodePlanGuides, DBName: "otherdb"}}},
		{"Queue", func(a *App, sc *db.ServerConn) { a.showBrokerQueuePropertiesFor(sc, dbName, "dbo", "q") },
			[]nodeData{{Type: NodeBrokerQueues, DBName: dbName}}, []nodeData{{Type: NodeBrokerQueues, DBName: "otherdb"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := newFakeConn(t)
			a := newTestApp()
			a.propDialog = NewPropDialog(a)
			a.connections = append(a.connections, sc)
			a.explorer.AddRoot("testsrv", sc)
			root := a.explorer.Selected()
			// Each folder under a parent of its own, so the search has to
			// descend to find it, holding one object.
			leafOf := map[*explorerNode]nodeData{}
			var parents []*explorerNode
			for _, d := range append(append([]nodeData{}, tc.want...), tc.other...) {
				d.conn = sc
				parents = append(parents, &explorerNode{label: "parent", data: nodeData{Type: NodeSecurity, conn: sc}})
				folder := &explorerNode{label: "folder", data: d}
				leaf := &explorerNode{label: "object", data: nodeData{Type: NodeError, conn: sc}}
				a.explorer.SetChildren(parents[len(parents)-1], []*explorerNode{folder})
				a.explorer.SetChildren(folder, []*explorerNode{leaf})
				leafOf[leaf] = d
			}
			a.explorer.SetChildren(root, parents)

			tc.open(a, sc)
			if a.propDialog.onSaved == nil {
				t.Fatal("the dialog sets no onSaved hook")
			}
			a.propDialog.onSaved()
			for leaf, d := range leafOf {
				want := false
				for _, w := range tc.want {
					w.conn = sc
					want = want || w == d
				}
				if leaf.retired != want {
					t.Errorf("folder %v %s.%s.%s reloaded = %v, want %v", d.Type, d.DBName, d.Schema, d.Name, leaf.retired, want)
				}
			}
		})
	}
}
