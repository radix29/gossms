package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// Resource Governor's tree commands (W6): Delete applied at once without
// changing whether the governor is on, Script showing that apply, the node's
// own Enable/Disable/Reconfigure/Reset Statistics, the New items, and the
// Details pane's rows carrying their objects.

// rgLeaf is a pool, group or external pool leaf under a folder, on sc.
func rgLeaf(sc *db.ServerConn, typ NodeType, name string, system bool) *explorerNode {
	parent := &explorerNode{label: "folder", data: nodeData{conn: sc}}
	n := &explorerNode{label: name, parent: parent, data: nodeData{Type: typ, Name: name, IsSystem: system, conn: sc}}
	parent.children = []*explorerNode{n}
	return n
}

func TestDeletingAResourceGovernorObjectAppliesItAndKeepsTheGovernorState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     NodeType
		obj     string
		enabled bool
		want    []string
	}{
		{"pool, enabled", NodeResourcePool, "reports", true,
			[]string{"DROP RESOURCE POOL [reports]", "ALTER RESOURCE GOVERNOR RECONFIGURE"}},
		// RECONFIGURE would enable a disabled governor (W1).
		{"pool, disabled", NodeResourcePool, "reports", false,
			[]string{"DROP RESOURCE POOL [reports]", "ALTER RESOURCE GOVERNOR DISABLE"}},
		{"group", NodeWorkloadGroup, "adhoc", true,
			[]string{"DROP WORKLOAD GROUP [adhoc]", "ALTER RESOURCE GOVERNOR RECONFIGURE"}},
		{"external pool", NodeExternalResourcePool, "ml", true,
			[]string{"DROP EXTERNAL RESOURCE POOL [ml]", "ALTER RESOURCE GOVERNOR RECONFIGURE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t, rgConfig(tc.enabled))
			a.deleteObject(rgLeaf(sc, tc.typ, tc.obj, false))
			answerConfirm(t, a, false)
			waitAndDrain(t, a)
			if got := inst.Statements(); !slices.Equal(got, tc.want) {
				t.Errorf("statements:\n  got  %q\n  want %q", got, tc.want)
			}
		})
	}
}

// The Script answer shows the apply too: it is part of what the Yes runs.
func TestScriptingAResourcePoolDeleteShowsTheApplyAndRunsNothing(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t, rgConfig(false))
	before := a.panels.Count()
	a.deleteObject(rgLeaf(sc, NodeResourcePool, "reports", false))
	answerScript(t, a, false)
	waitAndDrain(t, a)
	text := scriptedText(t, a, before)
	if !strings.Contains(text, "DROP RESOURCE POOL [reports]") || !strings.Contains(text, "ALTER RESOURCE GOVERNOR DISABLE") {
		t.Errorf("script = %q, want the DROP and the DISABLE that keeps the governor off", text)
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("Script ran %q", got)
	}
}

// A refused apply (Msg 10904: sessions still in a dropped group) leaves the
// drop stored. The status says both, and the folder is still reloaded.
func TestARefusedApplyStillReportsTheDrop(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t, rgConfig(true),
		fakeResponse{match: "ALTER RESOURCE GOVERNOR RECONFIGURE", err: errors.New("Msg 10904, there are active sessions in workload groups being dropped")})
	a.deleteObject(rgLeaf(sc, NodeWorkloadGroup, "adhoc", false))
	answerConfirm(t, a, false)
	waitAndDrain(t, a)
	if !strings.Contains(a.statusText, `"adhoc" deleted, but applying the change failed`) || !strings.Contains(a.statusText, "10904") {
		t.Errorf("status = %q", a.statusText)
	}
}

func TestResourceGovernorBuiltInsOfferNoDelete(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	has := func(n *explorerNode) bool {
		return slices.ContainsFunc(a.objectOpsMenuItems(n), func(it controls.MenuItem) bool { return it.Label == "Delete..." })
	}
	if !has(rgLeaf(sc, NodeResourcePool, "reports", false)) {
		t.Error("a user pool offers no Delete")
	}
	for _, typ := range []NodeType{NodeResourcePool, NodeWorkloadGroup, NodeExternalResourcePool} {
		if has(rgLeaf(sc, typ, "default", true)) {
			t.Errorf("%v: the built-in default offers Delete", typ)
		}
	}
}

func TestResourceGovernorDeleteNeedsControlServer(t *testing.T) {
	a := newTestApp()
	sc := probedConn(t, "", nil, []string{"CONTROL SERVER"}, nil, nil)
	for _, it := range a.objectOpsMenuItems(rgLeaf(sc, NodeWorkloadGroup, "adhoc", false)) {
		if it.Label == "Delete..." && itemEnabled(it) {
			t.Error("Delete offered without CONTROL SERVER")
		}
	}
}

// rgNodeOn is the Resource Governor node in the given state.
func rgNodeOn(sc *db.ServerConn, enabled, pending bool) *explorerNode {
	return &explorerNode{label: "Resource Governor", data: nodeData{Type: NodeResourceGovernor,
		IsEnabled: enabled, RGPending: pending, conn: sc}}
}

func TestResourceGovernorNodeMenuFollowsTheState(t *testing.T) {
	sc, _ := newFakeConn(t)
	for _, tc := range []struct {
		name             string
		enabled, pending bool
		toggle           string
		reconfigure      bool
	}{
		{"enabled", true, false, "Disable", false},
		{"enabled, pending", true, true, "Disable", true},
		// Pending on a disabled governor is not "(Reconfiguration pending)",
		// and RECONFIGURE there is Enable.
		{"disabled", false, true, "Enable", false},
	} {
		node := rgNodeOn(sc, tc.enabled, tc.pending)
		if !itemEnabled(itemNamed(t, node, tc.toggle)) {
			t.Errorf("%s: %s withheld", tc.name, tc.toggle)
		}
		if got := itemEnabled(itemNamed(t, node, "Reconfigure")); got != tc.reconfigure {
			t.Errorf("%s: Reconfigure offered = %v, want %v", tc.name, got, tc.reconfigure)
		}
		if !itemEnabled(itemNamed(t, node, "Reset Statistics")) {
			t.Errorf("%s: Reset Statistics withheld", tc.name)
		}
	}
}

func TestResourceGovernorWriteItemsNeedControlServer(t *testing.T) {
	sc := probedConn(t, "", nil, []string{"CONTROL SERVER"}, nil, nil)
	for _, tc := range []struct {
		node  *explorerNode
		label string
	}{
		{rgNodeOn(sc, true, true), "Disable"},
		{rgNodeOn(sc, true, true), "Reconfigure"},
		{rgNodeOn(sc, true, true), "Reset Statistics"},
		{rgNodeOn(sc, false, false), "Enable"},
		{folderNode(NodeResourcePools, sc), "New Resource Pool..."},
		{folderNode(NodeExternalResourcePools, sc), "New External Resource Pool..."},
		{rgLeaf(sc, NodeResourcePool, "reports", false), "New Workload Group..."},
	} {
		it := itemNamed(t, tc.node, tc.label)
		if itemEnabled(it) || it.Note != "needs CONTROL SERVER" {
			t.Errorf("%v %s: offered %v, note %q — want withheld for CONTROL SERVER", tc.node.data.Type, tc.label, itemEnabled(it), it.Note)
		}
	}
	// Properties stays: the dialog shows read-only to such a login.
	if !itemEnabled(itemNamed(t, rgNodeOn(sc, true, false), "Properties...")) {
		t.Error("Properties withheld without CONTROL SERVER")
	}
}

// The configuration row is invisible without VIEW ANY DEFINITION, which VIEW
// SERVER STATE does not imply: the Script cascade is withheld with that
// reason rather than failing "not visible" in the status line (N10).
func TestScriptResourceGovernorNeedsViewAnyDefinition(t *testing.T) {
	a := newTestApp()
	for _, tc := range []struct {
		name            string
		granted, denied []string
		want            bool
	}{
		{"VIEW SERVER STATE only", []string{"VIEW SERVER STATE"}, []string{"VIEW ANY DEFINITION"}, false},
		{"VIEW ANY DEFINITION", []string{"VIEW ANY DEFINITION"}, nil, true},
		{"unprobed", nil, nil, true},
	} {
		sc := probedConn(t, "", tc.granted, tc.denied, nil, nil)
		items := a.scriptMenuItems(rgNodeOn(sc, true, false))
		if len(items) != 1 || items[0].Label != "Script Resource Governor as" {
			t.Fatalf("%s: script items = %+v", tc.name, items)
		}
		it := items[0]
		if got := itemEnabled(it); got != tc.want {
			t.Errorf("%s: offered = %v, want %v", tc.name, got, tc.want)
		}
		if !tc.want && it.Note != "needs VIEW ANY DEFINITION" {
			t.Errorf("%s: note %q", tc.name, it.Note)
		}
	}
}

func TestResourceGovernorItemsOnAnUnsupportedEdition(t *testing.T) {
	sc := newFakeConnEdition(t, 4, "16.0.4085.2")
	for _, label := range []string{"Disable", "Reconfigure", "Reset Statistics", "Properties..."} {
		if it := itemNamed(t, rgNodeOn(sc, true, true), label); itemEnabled(it) || it.Note != "N/A" {
			t.Errorf("Express: %s offered %v, note %q", label, itemEnabled(it), it.Note)
		}
	}
}

func TestNewWorkloadGroupIsWithheldOnTheInternalPool(t *testing.T) {
	sc, _ := newFakeConn(t)
	if !itemEnabled(itemNamed(t, rgLeaf(sc, NodeResourcePool, "default", true), "New Workload Group...")) {
		t.Error("withheld on default, which takes user groups")
	}
	folder := &explorerNode{data: nodeData{Type: NodeWorkloadGroups, RGPool: "internal", conn: sc}}
	if itemEnabled(itemNamed(t, folder, "New Workload Group...")) {
		t.Error("offered on internal's Workload Groups folder")
	}
}

func TestResourceGovernorCommandsRunTheirStatement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     func(a *App, sc *db.ServerConn, n *explorerNode)
		confirm bool
		want    string
	}{
		{"enable", (*App).enableResourceGovernor, false, "ALTER RESOURCE GOVERNOR RECONFIGURE"},
		{"disable", (*App).disableResourceGovernor, true, "ALTER RESOURCE GOVERNOR DISABLE"},
		{"reconfigure", (*App).reconfigureResourceGovernor, false, "ALTER RESOURCE GOVERNOR RECONFIGURE"},
		{"reset statistics", (*App).resetResourceGovernorStatistics, true, "ALTER RESOURCE GOVERNOR RESET STATISTICS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t)
			tc.run(a, sc, rgNodeOn(sc, true, true))
			if tc.confirm {
				answerConfirm(t, a, false)
			}
			waitAndDrain(t, a)
			if got := inst.Statements(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("statements = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every row of the three folder views carries its object, built-ins marked,
// so the pane's Delete lines up with the rows and refuses a built-in.
func TestResourceGovernorFolderDetailsCarryTheirObjects(t *testing.T) {
	ctx := context.Background()
	sc, _ := newFakeConn(t, append(rgPools(), rgGroups())...)
	var objs []nodeData
	_, rows, err := resourcePoolsFolderDetail(ctx, sc, &explorerNode{data: nodeData{Type: NodeResourcePools}}, &objs)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != len(rows) {
		t.Fatalf("%d objects for %d rows", len(objs), len(rows))
	}
	for i, o := range objs {
		if o.Type != NodeResourcePool || !strings.HasPrefix(rows[i][0], o.Name) || o.IsSystem != (o.Name != "reports") {
			t.Errorf("row %d %q: object %+v", i, rows[i][0], o)
		}
	}

	objs = nil
	_, rows, err = workloadGroupsFolderDetail(ctx, sc, &explorerNode{data: nodeData{Type: NodeWorkloadGroups, RGPool: "reports"}}, &objs)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || len(rows) != 2 || objs[1].Name != "nightly" || objs[1].Type != NodeWorkloadGroup || objs[1].IsSystem {
		t.Errorf("groups of reports: rows %q objects %+v", rows, objs)
	}
}
