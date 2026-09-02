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

// The Object Explorer wiring for the Audits and Server Audit Specifications
// families — the same per-family checklist the earlier families cover.

func TestAuditFoldersHaveLoaders(t *testing.T) {
	for _, tc := range []struct {
		folder, leaf NodeType
		name         string
	}{
		{NodeAudits, NodeAudit, "Audits"},
		{NodeServerAuditSpecifications, NodeServerAuditSpecification, "Server Audit Specifications"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := childLoaders[tc.folder]; !ok {
				t.Fatal("the folder has no childLoaders entry — it would expand to nothing")
			}
			if !isContainerNode(tc.folder) {
				t.Error("the folder is not a container node — it would draw an object icon and refuse to expand")
			}
			if hasChildren(tc.leaf) {
				t.Error("the leaf claims children — it would draw an expand arrow that leads nowhere")
			}
		})
	}
}

func TestAuditLeavesHaveIconsInEveryStyle(t *testing.T) {
	for _, leaf := range []NodeType{NodeAudit, NodeServerAuditSpecification} {
		for _, style := range []struct {
			name string
			s    config.IconStyle
		}{
			{"Emoji", config.IconStyleEmoji},
			{"Symbols", config.IconStyleSymbols},
			{"Portable", config.IconStylePortable},
		} {
			got := objectIcon(leaf, style.s)
			if got == 0 {
				t.Errorf("%s: %v has no glyph", style.name, nodeTypeName(leaf))
			}
			if got == '•' {
				t.Errorf("%s: %v fell through to the default bullet", style.name, nodeTypeName(leaf))
			}
		}
	}
}

func TestAuditTypesAreNamed(t *testing.T) {
	for leaf, want := range map[NodeType]string{
		NodeAudit:                    "Audit",
		NodeServerAuditSpecification: "Server Audit Specification",
	} {
		if got := nodeTypeName(leaf); got != want {
			t.Errorf("nodeTypeName = %q, want %q", got, want)
		}
	}
}

func TestAuditsScriptAndDrop(t *testing.T) {
	a := &App{}
	for _, tc := range []struct {
		leaf  NodeType
		label string
	}{
		{NodeAudit, "Script Audit as"},
		{NodeServerAuditSpecification, "Script Server Audit Specification as"},
	} {
		items := a.scriptMenuItems(opNode(tc.leaf, "", "HIPAA", ""))
		if len(items) == 0 {
			t.Fatalf("%s offers no Script item", nodeTypeName(tc.leaf))
		}
		if items[0].Label != tc.label {
			t.Errorf("Script item is labelled %q, want %q", items[0].Label, tc.label)
		}
		want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
		if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
			t.Errorf("%s script verbs = %v, want %v", nodeTypeName(tc.leaf), got, want)
		}

		op, ok := objectOps[tc.leaf]
		if !ok {
			t.Fatalf("%s has no objectOps entry — Delete is not offered", nodeTypeName(tc.leaf))
		}
		if op.drop == nil {
			t.Errorf("%s's objectOp has no drop", nodeTypeName(tc.leaf))
		}
		// Neither object has a usable rename from the tree: a specification
		// has no MODIFY NAME statement at all, and an audit's exists only on a
		// disabled audit, so offering one would silently stop auditing.
		if op.rename != nil {
			t.Errorf("%s's objectOp offers a rename", nodeTypeName(tc.leaf))
		}
	}
}

// -- the loaders ---------------------------------------------------------------

var auditCreated = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

// auditRows scripts the three-row answer the audits loader and Details pane
// read. The disabled audit is neither first nor last, and one audit targets a
// Windows log, so a loader that reads row 0 or assumes a file target fails.
func auditRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.server_audits",
		cols:  16,
		rows: [][]driver.Value{
			{int64(65536), "AppLogAudit", "11111111-1111-1111-1111-111111111111", "APPLICATION LOG",
				"CONTINUE", int64(1000), nil, true, auditCreated, auditCreated,
				nil, nil, nil, nil, nil, nil},
			{int64(65537), "HIPAA", "22222222-2222-2222-2222-222222222222", "FILE",
				"SHUTDOWN SERVER INSTANCE", int64(2000), "([server_principal_name]<>N'sa')", false,
				auditCreated, auditCreated,
				`C:\audit\`, "HIPAA_x.sqlaudit", int64(20), int64(2147483647), int64(7), true},
			{int64(65538), "Rollover", "33333333-3333-3333-3333-333333333333", "FILE",
				"CONTINUE", int64(0), nil, true, auditCreated, auditCreated,
				`C:\audit\`, "Rollover_x.sqlaudit", int64(0), int64(5), int64(0), false},
		},
	}
}

func specRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.server_audit_specifications",
		cols:  8,
		rows: [][]driver.Value{
			{int64(65536), "AppSpec", "11111111-1111-1111-1111-111111111111", "AppLogAudit",
				true, auditCreated, auditCreated, "BACKUP_RESTORE_GROUP"},
			{int64(65537), "HIPAA_spec", "22222222-2222-2222-2222-222222222222", "HIPAA",
				false, auditCreated, auditCreated, "DATABASE_CHANGE_GROUP,LOGIN_CHANGE_PASSWORD_GROUP"},
			// An orphan: SQL Server allows an audit to be dropped out from
			// under a specification, which leaves the join returning NULL.
			{int64(65538), "Orphan", "44444444-4444-4444-4444-444444444444", nil,
				false, auditCreated, auditCreated, nil},
		},
	}
}

func TestAuditsFolderLabelsDisabledAudits(t *testing.T) {
	sc, _ := newFakeConn(t, auditRows())
	l := loaderCtx{ctx: context.Background(), sc: sc}
	children, err := loadAuditsChildren(l, &explorerNode{data: nodeData{Type: NodeAudits, conn: sc}})
	if err != nil {
		t.Fatalf("loadAuditsChildren: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}
	// A disabled audit records nothing, and nothing else on the row says so.
	if children[1].label != "HIPAA (Disabled)" || children[1].data.IsEnabled {
		t.Errorf("the disabled audit is labelled %q / IsEnabled=%v", children[1].label, children[1].data.IsEnabled)
	}
	if children[0].label != "AppLogAudit" || !children[0].data.IsEnabled {
		t.Errorf("the enabled audit is labelled %q / IsEnabled=%v", children[0].label, children[0].data.IsEnabled)
	}
	if children[1].data.Name != "HIPAA" {
		t.Errorf("the node addresses the audit as %q — the label is not the name", children[1].data.Name)
	}
}

func TestSpecificationsFolderLabelsDisabledSpecs(t *testing.T) {
	sc, _ := newFakeConn(t, specRows())
	l := loaderCtx{ctx: context.Background(), sc: sc}
	children, err := loadServerAuditSpecificationsChildren(l,
		&explorerNode{data: nodeData{Type: NodeServerAuditSpecifications, conn: sc}})
	if err != nil {
		t.Fatalf("loadServerAuditSpecificationsChildren: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}
	if children[1].label != "HIPAA_spec (Disabled)" || children[1].data.Name != "HIPAA_spec" {
		t.Errorf("the disabled specification is labelled %q / addressed as %q",
			children[1].label, children[1].data.Name)
	}
	if children[0].label != "AppSpec" {
		t.Errorf("the enabled specification is labelled %q", children[0].label)
	}
}

// -- the Details pane ----------------------------------------------------------

func TestAuditsFolderDetailReadsTheFileBlock(t *testing.T) {
	sc, _ := newFakeConn(t, auditRows())
	var objs []nodeData
	cols, rows, err := auditsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeAudits}}, &objs)
	if err != nil {
		t.Fatalf("auditsFolderDetail: %v", err)
	}
	if len(rows) != 3 || len(objs) != 3 {
		t.Fatalf("got %d rows and %d row objects", len(rows), len(objs))
	}
	if objs[1].Name != "HIPAA" || objs[1].Type != NodeAudit || objs[1].IsEnabled {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	target := slices.Index(cols, "Target")
	state := slices.Index(cols, "State")
	if target < 0 || state < 0 {
		t.Fatalf("columns are %v", cols)
	}
	// A Windows-log audit has no file path; showing a stale one would name a
	// file nothing writes.
	if rows[0][target] != "" {
		t.Errorf("the application-log audit reports target %q", rows[0][target])
	}
	if rows[1][target] != `C:\audit` {
		t.Errorf("the file audit reports target %q", rows[1][target])
	}
	if rows[1][state] != "Disabled" {
		t.Errorf("the disabled audit's state column is %q", rows[1][state])
	}
}

// The two file-count settings are mutually exclusive and the catalog leaves
// max_rollover_files at its UNLIMITED sentinel even when max_files is the one
// in force — so reading rollover first reports "Unlimited" for an audit capped
// at seven files.
func TestAuditDetailReadsTheRightFileCount(t *testing.T) {
	for _, tc := range []struct {
		name, want string
	}{
		{"HIPAA", "7 (maximum files)"},
		{"Rollover", "5 (rollover)"},
		{"AppLogAudit", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := newFakeConn(t, auditByName(tc.name), auditRows())
			_, rows, err := auditDetail(context.Background(), sc,
				&explorerNode{data: nodeData{Type: NodeAudit, Name: tc.name}})
			if err != nil {
				t.Fatalf("auditDetail: %v", err)
			}
			got := ""
			for _, r := range rows {
				if r[0] == "Files to retain" {
					got = r[1]
				}
			}
			if got != tc.want {
				t.Errorf("Files to retain = %q, want %q", got, tc.want)
			}
		})
	}
}

// An orphaned specification is a real state, not a failed read: SQL Server
// allows the audit behind one to be dropped.
func TestSpecificationDetailNamesAMissingAudit(t *testing.T) {
	sc, _ := newFakeConn(t, specByName("Orphan"), specRows())
	_, rows, err := serverAuditSpecificationDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeServerAuditSpecification, Name: "Orphan"}})
	if err != nil {
		t.Fatalf("serverAuditSpecificationDetail: %v", err)
	}
	got := ""
	for _, r := range rows {
		if r[0] == "Audit" {
			got = r[1]
		}
	}
	if !strings.Contains(got, "no longer exists") {
		t.Errorf("the orphan's Audit row is %q", got)
	}
}

// -- the drops -----------------------------------------------------------------

// The enabled-state read is what the off-then-drop dance turns on, so the drop
// must land on the object the menu named, not on whichever row is listed
// first. Both fakes answer by arg for that reason.
func TestAuditDropDisablesThenDrops(t *testing.T) {
	sc, inst := newFakeConn(t, auditEnabled("HIPAA", true))
	if err := objectOps[NodeAudit].drop(t.Context(), sc,
		nodeData{Type: NodeAudit, Name: "HIPAA"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := []string{
		"ALTER SERVER AUDIT [HIPAA] WITH ( STATE = OFF )",
		"DROP SERVER AUDIT [HIPAA]",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSpecificationDropDisablesThenDrops(t *testing.T) {
	sc, inst := newFakeConn(t, specEnabled("HIPAA_spec", true))
	if err := objectOps[NodeServerAuditSpecification].drop(t.Context(), sc,
		nodeData{Type: NodeServerAuditSpecification, Name: "HIPAA_spec"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := []string{
		"ALTER SERVER AUDIT SPECIFICATION [HIPAA_spec] WITH ( STATE = OFF )",
		"DROP SERVER AUDIT SPECIFICATION [HIPAA_spec]",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A disabled object must not be switched on by its own delete.
func TestDroppingADisabledAuditDoesNotToggleIt(t *testing.T) {
	sc, inst := newFakeConn(t, auditEnabled("HIPAA", false))
	if err := objectOps[NodeAudit].drop(t.Context(), sc,
		nodeData{Type: NodeAudit, Name: "HIPAA"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := []string{"DROP SERVER AUDIT [HIPAA]"}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// -- scripted answers ----------------------------------------------------------

// auditByName and specByName scope a by-name read with arg:, and each must be
// placed *before* the matching list response: the fake matches by substring in
// order, and ServerAuditByNameContext's query also contains
// "FROM   sys.server_audits", so without it every audit resolves to whichever
// row is listed first — which makes a by-name test pass whatever name it is
// given.
func auditByName(name string) fakeResponse {
	return fakeResponse{match: "FROM   sys.server_audits", arg: name, cols: 16,
		rows: rowsNamed(auditRows().rows, name, 1)}
}

func specByName(name string) fakeResponse {
	return fakeResponse{match: "FROM   sys.server_audit_specifications", arg: name, cols: 8,
		rows: rowsNamed(specRows().rows, name, 1)}
}

func rowsNamed(rows [][]driver.Value, name string, col int) [][]driver.Value {
	for _, r := range rows {
		if r[col] == name {
			return [][]driver.Value{r}
		}
	}
	return nil
}

// auditEnabled and specEnabled answer the one-column is_state_enabled read the
// off-then-apply dance opens with. Its query says "FROM sys.server_audits"
// with a single space, so it never collides with the list read's match above.
func auditEnabled(name string, on bool) fakeResponse {
	return fakeResponse{match: "SELECT is_state_enabled FROM sys.server_audits", arg: name,
		cols: 1, rows: [][]driver.Value{{on}}}
}

func specEnabled(name string, on bool) fakeResponse {
	return fakeResponse{match: "SELECT is_state_enabled FROM sys.server_audit_specifications", arg: name,
		cols: 1, rows: [][]driver.Value{{on}}}
}
