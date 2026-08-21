package tui

import (
	"context"
	"database/sql/driver"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Memory, Processors, Advanced, Connections, Database Settings and
// Security pages are one mechanism seen six times: a label, an sp_configure
// option name stored next to it, and a tracked-rows slice apply walks. Nothing
// on screen shows the option name, so a label paired with the wrong one is
// invisible until the server changes.
//
// The blast radius is what makes these worth pinning ahead of the other forty
// pages. "max server memory (MB)" set to a small number makes an instance
// unable to start a query; "affinity mask" set from the wrong checkbox column
// pins SQL Server to CPUs it does not own; "xp_cmdshell" ticked from a
// neighbouring label is a security change nobody made deliberately.

// configOptionRows is the label-to-option-name pairing the three pages
// promise, written out rather than derived from them.
var configOptionRows = []struct {
	page   string
	label  string
	option string
	isBool bool
}{
	{"Memory", "Minimum server memory", "min server memory (MB)", false},
	{"Memory", "Maximum server memory", "max server memory (MB)", false},
	{"Memory", "Index creation memory", "index create memory (KB)", false},
	{"Memory", "Minimum memory per query", "min memory per query (KB)", false},

	{"Processors", "Maximum worker threads", "max worker threads", false},
	{"Processors", "Boost SQL Server priority", "priority boost", true},
	{"Processors", "Use Windows fibers", "lightweight pooling", true},
	{"Processors", "Max degree of parallelism", "max degree of parallelism", false},
	{"Processors", "Cost threshold for parallelism", "cost threshold for parallelism", false},

	{"Advanced", "Optimize for ad hoc workloads", "optimize for ad hoc workloads", true},
	{"Advanced", "Blocked process threshold", "blocked process threshold (s)", false},
	{"Advanced", "Cursor threshold", "cursor threshold", false},
	{"Advanced", "In-doubt xact resolution", "in-doubt xact resolution", false},
	{"Advanced", "Scan for startup procs", "scan for startup procs", true},
	{"Advanced", "Common criteria compliance enabled", "common criteria compliance enabled", true},
	{"Advanced", "Default trace enabled", "default trace enabled", true},
	{"Advanced", "Ad Hoc Distributed Queries", "Ad Hoc Distributed Queries", true},
	{"Advanced", "Agent XPs", "Agent XPs", true},
	{"Advanced", "CLR enabled", "clr enabled", true},
	{"Advanced", "CLR strict security", "clr strict security", true},
	{"Advanced", "Database Mail XPs", "Database Mail XPs", true},
	{"Advanced", "External scripts enabled", "external scripts enabled", true},
	{"Advanced", "Ole Automation Procedures", "Ole Automation Procedures", true},
	{"Advanced", "Remote admin connections", "remote admin connections", true},
	{"Advanced", "Show advanced options", "show advanced options", true},
	{"Advanced", "xp_cmdshell", "xp_cmdshell", true},

	{"Connections", "Maximum concurrent connections", "user connections", false},
	{"Connections", "Allow remote connections to this server", "remote access", true},
	{"Connections", "Require distributed transactions", "remote proc trans", true},
	{"Connections", "Remote query timeout", "remote query timeout (s)", false},
	{"Connections", "Remote login timeout", "remote login timeout (s)", false},
	{"Connections", "Estimated query cost limit", "query governor cost limit", false},
	{"Connections", "Network packet size", "network packet size (B)", false},
	{"Connections", "Default language", "default language", false},
	{"Connections", "Default full-text language", "default full-text language", false},
	{"Connections", "Two digit year cutoff", "two digit year cutoff", false},

	{"Database Settings", "Default index fill factor", "fill factor (%)", false},
	{"Database Settings", "Backup media retention", "media retention", false},
	{"Database Settings", "Backup compression default", "backup compression default", true},
	{"Database Settings", "Backup checksum default", "backup checksum default", true},
	{"Database Settings", "Recovery interval", "recovery interval (min)", false},

	{"Security", "Allow contained database authentication", "contained database authentication", true},
	{"Security", "Cross database ownership chaining", "cross db ownership chaining", true},
	{"Security", "Enable C2 audit tracing", "c2 audit mode", true},
}

// configPage returns the page a row belongs to, so the table above can be
// walked in one loop.
func configPage(t *testing.T, sc *db.ServerConn, name string) propPage {
	t.Helper()
	switch name {
	case "Memory":
		return pageServerMemory(sc)
	case "Processors":
		return pageServerProcessors(sc)
	case "Advanced":
		return pageServerAdvanced(sc)
	case "Connections":
		return pageServerConnections(sc)
	case "Database Settings":
		return pageServerDatabaseSettings(sc)
	case "Security":
		return pageServerSecurity(sc)
	}
	t.Fatalf("no page named %q", name)
	return propPage{}
}

// configResponses scripts sys.configurations with every option the three pages
// edit, all at 0, plus the two affinity masks and the DMV reads Memory and
// Processors make.
func configResponses() []fakeResponse {
	var rows [][]driver.Value
	add := func(name string) {
		rows = append(rows, []driver.Value{
			int64(len(rows) + 1), name, int64(0), int64(0),
			int64(0), int64(2147483647), true, true, name + " description",
		})
	}
	for _, o := range configOptionRows {
		add(o.option)
	}
	add("affinity mask")
	add("affinity I/O mask")
	add("filestream access level")

	// The by-name read the Processors page makes for the two affinity masks,
	// ahead of the list read: the query contains "FROM   sys.configurations"
	// too, so behind it the list answer serves it and every by-name lookup
	// resolves to whichever option sorts first.
	byName := func(name string) fakeResponse {
		return fakeResponse{match: "FROM   sys.configurations", arg: name, cols: 9, rows: [][]driver.Value{
			{int64(900), name, int64(0), int64(0), int64(0), int64(2147483647), true, true, name},
		}}
	}

	return []fakeResponse{
		byName("affinity mask"),
		byName("affinity I/O mask"),
		byName("filestream access level"),
		{match: "FROM   sys.configurations", cols: 9, rows: rows},
		{match: "sys.dm_os_sys_memory", cols: 4, rows: [][]driver.Value{
			{int64(16384), int64(8192), int64(4096), int64(2048)},
		}},
		{match: "cpu_count, hyperthread_ratio", cols: 2, rows: [][]driver.Value{
			{int64(8), int64(2)},
		}},
		{match: "FROM   sys.dm_os_schedulers", cols: 2, rows: [][]driver.Value{
			{int64(0), int64(0)}, {int64(1), int64(0)}, {int64(2), int64(0)}, {int64(3), int64(0)},
			{int64(4), int64(1)}, {int64(5), int64(1)}, {int64(6), int64(1)}, {int64(7), int64(1)},
		}},
		// Database Settings' disk list and Security's authentication mode.
		{match: "sys.dm_os_volume_stats", cols: 5, rows: [][]driver.Value{
			{`C:\`, "OS", `C:\Data\appdb.mdf`, float64(500000), float64(120000)},
		}},
		{match: "IsIntegratedSecurityOnly", cols: 1, rows: [][]driver.Value{{"MIXED"}}},
	}
}

// TestEverySpConfigureRowWritesTheOptionItIsLabelled walks the whole table,
// one page load per row, so the single statement that comes out can only have
// come from the row under test.
func TestEverySpConfigureRowWritesTheOptionItIsLabelled(t *testing.T) {
	for _, o := range configOptionRows {
		t.Run(o.page+"/"+o.label, func(t *testing.T) {
			sc, inst := newFakeConn(t, configResponses()...)
			form, apply := loadPage(t, configPage(t, sc, o.page), inst)

			if o.isBool {
				editCheck(t, form, o.label, true)
			} else {
				editText(t, form, o.label, "17")
			}
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}

			stmts := inst.Statements()
			// Two: the sp_configure for this option, then the single
			// RECONFIGURE the page issues once anything changed.
			if len(stmts) != 2 {
				t.Fatalf("want two statements (the option and RECONFIGURE), got %d:\n%s",
					len(stmts), strings.Join(stmts, "\n"))
			}
			if !strings.Contains(stmts[0], o.option) {
				t.Errorf("editing %q wrote:\n%s\nwant it to name option %q", o.label, stmts[0], o.option)
			}
			want := "17"
			if o.isBool {
				want = "1"
			}
			if !strings.Contains(stmts[0], want) {
				t.Errorf("editing %q wrote:\n%s\nwant it to carry the value %s", o.label, stmts[0], want)
			}
			if !strings.Contains(strings.ToUpper(stmts[1]), "RECONFIGURE") {
				t.Errorf("second statement was %q, want RECONFIGURE", stmts[1])
			}
		})
	}
}

// TestSpConfigurePagesWriteNothingWhenUntouched. RECONFIGURE is the tell here:
// the pages call it only when something changed, so a page that dirtied a row
// on load would reconfigure an instance whose Properties were opened to look
// at, which applies every *other* pending sp_configure change with it.
func TestSpConfigurePagesWriteNothingWhenUntouched(t *testing.T) {
	for _, page := range []string{"Memory", "Processors", "Advanced",
		"Connections", "Database Settings", "Security"} {
		t.Run(page, func(t *testing.T) {
			sc, inst := newFakeConn(t, configResponses()...)
			_, apply := loadPage(t, configPage(t, sc, page), inst)
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if stmts := inst.Statements(); len(stmts) != 0 {
				t.Fatalf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
			}
		})
	}
}

// TestSpConfigureOffersNoControlForAnOptionTheServerDoesNotHave.
// sys.configurations is edition- and version-dependent, so several of the
// options the Advanced page lists are simply absent on a lesser instance.
// Before this was fixed the boolean ones still rendered as a live checkbox
// left out of the page's tracked list: ticking xp_cmdshell and pressing OK
// reported success and sent nothing.
//
// Asserted through the widget, not the statement log — the old behaviour wrote
// nothing either, so "no statement" passes on the bug.
func TestSpConfigureOffersNoControlForAnOptionTheServerDoesNotHave(t *testing.T) {
	resp := configResponses()
	list := -1
	for i, r := range resp {
		if r.match == "FROM   sys.configurations" && r.arg == "" {
			list = i
		}
	}
	var kept [][]driver.Value
	for _, r := range resp[list].rows {
		if r[1] == "xp_cmdshell" || r[1] == "cursor threshold" {
			continue
		}
		kept = append(kept, r)
	}
	resp[list].rows = kept

	sc, inst := newFakeConn(t, resp...)
	form, apply := loadPage(t, pageServerAdvanced(sc), inst)

	for _, label := range []string{"xp_cmdshell", "Cursor threshold"} {
		row := textRow(t, form, label)
		if row.Value() != "N/A" {
			t.Errorf("%q shows %q for a missing option, want N/A", label, row.Value())
		}
		row.Edit("1")
		if row.Dirty() {
			t.Errorf("%q accepted an edit for an option this server does not have", label)
		}
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

const (
	affinityCol   = 0 // the affinity grid's Affinity toggle column
	ioAffinityCol = 1 // ... and its I/O Affinity one
)

// TestProcessorAffinityBitFollowsTheProcessorItIsLabelled is the one pairing on
// these pages that a round-trip test cannot reach. affinityBits and
// bitsToAffinity are inverses and are unit-tested as such, which proves
// nothing about *which* CPU a checkbox is over: shifting the grid by one row
// leaves the round trip intact and pins SQL Server to the wrong processors.
// Naming the row and asserting the bit is what closes that.
func TestProcessorAffinityBitFollowsTheProcessorItIsLabelled(t *testing.T) {
	for _, cpu := range []int{0, 3, 7} {
		t.Run("Processor "+strconv.Itoa(cpu), func(t *testing.T) {
			sc, inst := newFakeConn(t, configResponses()...)
			form, apply := loadPage(t, pageServerProcessors(sc), inst)

			// The scripted mask is 0, so the page opens with "automatically
			// set ... for all processors" ticked, and that overrides the grid
			// outright. Unticking it is what a user does before choosing CPUs.
			editCheck(t, form, "Automatically set processor affinity mask for all processors", false)
			toggleByName(t, toggleGrid(t, form), "Processor "+strconv.Itoa(cpu), affinityCol)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 2 {
				t.Fatalf("want the affinity write and RECONFIGURE, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
			}
			if !strings.Contains(stmts[0], "affinity mask") || strings.Contains(stmts[0], "affinity I/O mask") {
				t.Fatalf("wrote:\n%s\nwant it to set 'affinity mask'", stmts[0])
			}
			want := strconv.FormatInt(1<<uint(cpu), 10)
			if !strings.Contains(stmts[0], want) {
				t.Errorf("ticking Processor %d wrote:\n%s\nwant the mask %s", cpu, stmts[0], want)
			}
		})
	}
}

// TestProcessorIOAffinityIsADifferentColumnAndADifferentOption. The grid has
// two toggle columns over one row of CPUs, and they write two different
// sp_configure options. Reading the columns back in the wrong order sets I/O
// affinity from the CPU column and vice versa — both statements look right in
// isolation.
func TestProcessorIOAffinityIsADifferentColumnAndADifferentOption(t *testing.T) {
	sc, inst := newFakeConn(t, configResponses()...)
	form, apply := loadPage(t, pageServerProcessors(sc), inst)

	editCheck(t, form, "Automatically set I/O affinity mask for all processors", false)
	toggleByName(t, toggleGrid(t, form), "Processor 2", ioAffinityCol)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want the I/O affinity write and RECONFIGURE, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "affinity I/O mask") {
		t.Fatalf("wrote:\n%s\nwant it to set 'affinity I/O mask'", stmts[0])
	}
	if !strings.Contains(stmts[0], "4") {
		t.Errorf("ticking Processor 2's I/O column wrote:\n%s\nwant the mask 4", stmts[0])
	}
}

// TestAutomaticProcessorAffinityOverridesTheGrid pins the precedence the two
// checkboxes claim. Leaving "automatically set ..." ticked has to send mask 0
// whatever the grid says — and here that means sending nothing at all, since 0
// is what the server already has.
func TestAutomaticProcessorAffinityOverridesTheGrid(t *testing.T) {
	sc, inst := newFakeConn(t, configResponses()...)
	form, apply := loadPage(t, pageServerProcessors(sc), inst)

	tg := toggleGrid(t, form)
	toggleByName(t, tg, "Processor 1", affinityCol)
	toggleByName(t, tg, "Processor 5", ioAffinityCol)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("ticking CPUs with automatic affinity still on wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// FILESTREAM is the one control on these six pages that is not a configRow:
// a three-item Select whose *index* is the value sp_configure takes. Nothing
// else on the page works that way, so a wrong index here writes a level the
// user did not choose — and level 2 grants file-system access to the share,
// which is not a setting to arrive at by accident.
func TestFilestreamSelectWritesItsLevelNotItsLabel(t *testing.T) {
	for level, label := range filestreamLevels {
		if level == 0 {
			continue // the scripted starting value; selecting it is not an edit
		}
		t.Run(label, func(t *testing.T) {
			sc, inst := newFakeConn(t, configResponses()...)
			form, apply := loadPage(t, pageServerDatabaseSettings(sc), inst)

			editSelect(t, form, "FILESTREAM access level", label)
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}

			stmts := inst.Statements()
			if len(stmts) != 2 {
				t.Fatalf("want the sp_configure and a RECONFIGURE, got %d:\n%s",
					len(stmts), strings.Join(stmts, "\n"))
			}
			if !strings.Contains(stmts[0], "filestream access level") {
				t.Errorf("wrote:\n%s\nwant it to name the filestream option", stmts[0])
			}
			if !strings.Contains(stmts[0], strconv.Itoa(level)) {
				t.Errorf("selecting %q wrote:\n%s\nwant level %d", label, stmts[0], level)
			}
			if !strings.Contains(strings.ToUpper(stmts[1]), "RECONFIGURE") {
				t.Errorf("second statement was %q, want RECONFIGURE", stmts[1])
			}
		})
	}
}

// An instance with no FILESTREAM option at all must not offer a live control
// for it: the page swaps the list for a single "N/A" item, and apply has no
// configuration to write to. Same failure the sp_configure rows had — a
// widget that accepts an edit nothing sends.
func TestFilestreamOffersNoControlWhenTheServerHasNone(t *testing.T) {
	resp := configResponses()
	for i := range resp {
		if resp[i].arg == "filestream access level" {
			resp[i].rows = nil
		}
		if resp[i].match == "FROM   sys.configurations" && resp[i].arg == "" {
			var kept [][]driver.Value
			for _, r := range resp[i].rows {
				if r[1] != "filestream access level" {
					kept = append(kept, r)
				}
			}
			resp[i].rows = kept
		}
	}

	sc, inst := newFakeConn(t, resp...)
	form, apply := loadPage(t, pageServerDatabaseSettings(sc), inst)

	row := selectRow(t, form, "FILESTREAM access level")
	if got := row.Items(); len(got) != 1 || got[0] != "N/A" {
		t.Errorf("FILESTREAM offers %q for a server without it, want [N/A]", got)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// The authentication mode is read-only here — changing it needs a registry
// write and a service restart, which this build does not do. It has to stay a
// Static row: a live-looking editor for it would report a change that never
// happened.
func TestServerSecurityAuthenticationModeIsReadOnly(t *testing.T) {
	sc, inst := newFakeConn(t, configResponses()...)
	form, _ := loadPage(t, pageServerSecurity(sc), inst)

	for _, r := range form.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == sheetLabel("Authentication mode") {
			t.Fatal("Authentication mode is an editable text row; this build cannot write it")
		}
	}
	if _, ok := findStatic(form, "Authentication mode"); !ok {
		t.Error("Authentication mode is not shown at all")
	}
}

// findStatic returns the value of a Static row by label.
func findStatic(f *propsheet.Form, label string) (string, bool) {
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == label {
			return sr.Value(), true
		}
	}
	return "", false
}
