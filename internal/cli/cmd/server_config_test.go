package cmd

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// newServerConfigCatalog is newServerInfoCatalog with a sys.configurations
// read: out of name order, an option a CU added (no default), the version
// parts, min server memory configured 0 and running 16 as the engine runs it.
func newServerConfigCatalog() *serverInfoCatalog {
	c := newServerInfoCatalog()
	opt := func(name string, value, inUse, def int64, hasDef, dynamic, advanced bool) *gosmo.ConfigurationOption {
		return &gosmo.ConfigurationOption{Name: name, Value: value, ValueInUse: inUse, Minimum: 0, Maximum: 32767,
			Default: def, HasDefault: hasDef, IsDynamic: dynamic, IsAdvanced: advanced}
	}
	c.opts = []*gosmo.ConfigurationOption{
		opt("max degree of parallelism", 8, 8, 0, true, true, true),
		opt("backup compression default", 0, 0, 0, true, true, false),
		opt("Backup checksum default", 1, 1, 0, true, true, false),
		opt("cost threshold for parallelism", 5, 5, 5, true, true, true),
		opt(maxServerMemory, 2147483647, 2147483647, 2147483647, true, true, true),
		opt(minServerMemory, 0, 16, 0, true, true, true),
		opt("fill factor (%)", 90, 0, 0, true, false, true),
		opt("tiered memory enabled", 0, 0, 0, false, false, true),
		opt("version high part of SQL Server", 1114112, 1114112, 0, false, true, true),
	}
	return c
}

// setRows is set's rows as strings, "NULL" for a NULL, as column has them.
func setRows(set output.Set) [][]string {
	var rows [][]string
	for _, r := range set.Rows {
		var row []string
		for _, c := range r {
			if c == nil {
				row = append(row, "NULL")
			} else {
				row = append(row, *c)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func TestServerConfigList(t *testing.T) {
	res, err := runServer(t, newServerConfigCatalog(), "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var cols []string
	for _, c := range set.Columns {
		cols = append(cols, c.Name)
	}
	if want := []string{"name", "alias", "configured", "running", "default", "min", "max", "dynamic", "advanced"}; !slices.Equal(cols, want) {
		t.Errorf("columns %q, want %q", cols, want)
	}
	// By name, ignoring case.
	if got, want := column(set, "name"), []string{"Backup checksum default", "backup compression default",
		"cost threshold for parallelism", "fill factor (%)", "max degree of parallelism", maxServerMemory, minServerMemory,
		"tiered memory enabled", "version high part of SQL Server"}; !slices.Equal(got, want) {
		t.Errorf("names %q, want %q", got, want)
	}
	rows := setRows(set)
	if want := []string{"max degree of parallelism", "maxdop", "8", "8", "0", "0", "32767", "1", "1"}; !slices.Equal(rows[4], want) {
		t.Errorf("maxdop row %q, want %q", rows[4], want)
	}
	// The alias matches whatever case the server spells the name in.
	if rows[0][1] != "backup-checksum" || rows[3][1] != "NULL" {
		t.Errorf("aliases %q, %q", rows[1][1], rows[3][1])
	}
	if rows[7][4] != "NULL" {
		t.Errorf("an option with no default shows %q, want null", rows[7][4])
	}
	// One warning, naming the option a CU added and not the version part.
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "tiered memory enabled") ||
		strings.Contains(res.Warnings[0], "version") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerConfigListFilters(t *testing.T) {
	for _, tc := range []struct {
		argv string
		want []string
	}{
		{"--name BACKUP", []string{"Backup checksum default", "backup compression default"}},
		{"--advanced --name memory", []string{maxServerMemory, minServerMemory, "tiered memory enabled"}},
		// Configured against the default: min server memory runs 16 but is
		// configured 0, its default; fill factor waits for a restart but is
		// configured 90. An option with no default is left out.
		{"--non-default", []string{"Backup checksum default", "fill factor (%)", "max degree of parallelism"}},
		{"--non-default --advanced", []string{"fill factor (%)", "max degree of parallelism"}},
		{"--name nothing-like-it", nil},
	} {
		res, err := runServer(t, newServerConfigCatalog(), "config", "list "+tc.argv)
		if err != nil {
			t.Fatalf("%s: %v", tc.argv, err)
		}
		if got := column(res.Sets[0], "name"); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.argv, got, tc.want)
		}
	}
	// --non-default still says which options it could not judge.
	res, _ := runServer(t, newServerConfigCatalog(), "config", "list --non-default")
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "--non-default leaves it out") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// A filter that keeps only defaulted options warns about nothing.
	if res, _ := runServer(t, newServerConfigCatalog(), "config", "list --name parallelism"); len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// A server gosmo has no defaults for at all is one sentence, not every name.
func TestServerConfigListNoDefaults(t *testing.T) {
	cat := newServerConfigCatalog()
	for _, o := range cat.opts {
		o.HasDefault, o.Default = false, 0
	}
	res, err := runServer(t, cat, "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "every setting") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := column(res.Sets[0], "default"); slices.ContainsFunc(got, func(v string) bool { return v != "NULL" }) {
		t.Errorf("defaults %q, want all null", got)
	}
}

func TestServerConfigGet(t *testing.T) {
	// An alias in another case, and a repeat, in the order given.
	res, err := runServer(t, newServerConfigCatalog(), "config", "get COST-THRESHOLD maxdop MAXDOP")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := column(res.Sets[0], "name"), []string{"cost threshold for parallelism", "max degree of parallelism"}; !slices.Equal(got, want) {
		t.Errorf("names %q, want %q", got, want)
	}
	keys, got := fieldMap(res)
	if want := []string{"logicalCpus", "numaNodes", "logicalCpusPerNumaNode"}; !slices.Equal(keys, want) {
		t.Errorf("context %q, want %q", keys, want)
	}
	if got["logicalCpus"] != "8" || got["numaNodes"] != "2" || got["logicalCpusPerNumaNode"] != "4" {
		t.Errorf("context %v", got)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerConfigGetContext(t *testing.T) {
	for _, tc := range []struct {
		names string
		want  []string
	}{
		{"max-memory", []string{"physicalMemoryMb", "committedMemoryMb"}},
		{"min-memory maxdop", []string{"logicalCpus", "numaNodes", "logicalCpusPerNumaNode", "physicalMemoryMb", "committedMemoryMb"}},
		{"backup-checksum", nil},
	} {
		res, err := runServer(t, newServerConfigCatalog(), "config", "get "+tc.names)
		if err != nil {
			t.Fatalf("%s: %v", tc.names, err)
		}
		if keys, _ := fieldMap(res); !slices.Equal(keys, tc.want) {
			t.Errorf("%s: context %q, want %q", tc.names, keys, tc.want)
		}
	}
	res, _ := runServer(t, newServerConfigCatalog(), "config", "get max-memory")
	if _, got := fieldMap(res); got["physicalMemoryMb"] != "65536" || got["committedMemoryMb"] != "61210" {
		t.Errorf("memory context %v", got)
	}

	// CPUs that do not divide evenly over the nodes.
	cat := newServerConfigCatalog()
	cat.info.LogicalCPUCount = 7
	res, _ = runServer(t, cat, "config", "get maxdop")
	if _, got := fieldMap(res); got["logicalCpusPerNumaNode"] != "3.50" {
		t.Errorf("per node %q, want 3.50", got["logicalCpusPerNumaNode"])
	}
}

// A full sys.configurations name resolves ignoring case. (runServer splits
// argv on spaces, so this calls resolveConfig directly.)
func TestServerConfigGetFullName(t *testing.T) {
	o, err := resolveConfig(newServerConfigCatalog().opts, "FILL FACTOR (%)")
	if err != nil || o.Name != "fill factor (%)" {
		t.Errorf("%v, %v", o, err)
	}
}

// Without VIEW SERVER STATE the context is null with one warning naming the
// permission, and the set stands: exit 0.
func TestServerConfigGetNoViewServerState(t *testing.T) {
	cat := newServerConfigCatalog()
	cat.info.SysInfoUnavailable, cat.info.PhysicalMemoryMB, cat.info.LogicalCPUCount = true, 0, 0
	cat.procErr, cat.statsErr = denied("processor info"), denied("memory stats")
	res, err := runServer(t, cat, "config", "get maxdop max-memory")
	if err != nil {
		t.Fatalf("%v, want exit 0", err)
	}
	_, got := fieldMap(res)
	for _, k := range []string{"logicalCpus", "numaNodes", "logicalCpusPerNumaNode", "physicalMemoryMb", "committedMemoryMb"} {
		if got[k] != "<null>" {
			t.Errorf("%s = %q, want null", k, got[k])
		}
	}
	if len(res.Sets[0].Rows) != 2 {
		t.Errorf("the settings are gone: %d rows", len(res.Sets[0].Rows))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "logicalCpus, numaNodes, physicalMemoryMb and committedMemoryMb were not read") ||
		!strings.Contains(res.Warnings[0], "VIEW SERVER STATE") {
		t.Errorf("warnings %q, want one naming the fields and the permission", res.Warnings)
	}
}

// An unknown setting is NOT_FOUND (exit 8) naming up to 3 that contain it.
func TestServerConfigGetUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, hint string
	}{
		{"memory", `Did you mean "max server memory (MB)", "min server memory (MB)", "tiered memory enabled"?`},
		{"backup-", "Did you mean backup-checksum, backup-compression?"},
		{"nothing", "gossms server config list --name"},
	} {
		_, err := runServer(t, newServerConfigCatalog(), "config", "get maxdop "+tc.name)
		e, ok := err.(*output.Error)
		if !ok || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %v, want NOT_FOUND hinting %q", tc.name, err, tc.hint)
		}
	}
}

// No alias or name repeats, and an alias is lower case, as §4.2 spells them.
func TestConfigAliasesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range configAliases {
		if seen[a.Alias] || seen[a.Name] || a.Alias != strings.ToLower(a.Alias) {
			t.Errorf("alias %q → %q repeats or is not lower case", a.Alias, a.Name)
		}
		seen[a.Alias], seen[a.Name] = true, true
	}
}
