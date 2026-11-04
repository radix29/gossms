package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// denied is the server refusing a DMV without VIEW SERVER STATE, wrapped as
// gosmo wraps it.
func denied(what string) error {
	return fmt.Errorf("gosmo: %s: %w", what, mssql.Error{Number: 300, Class: 14,
		Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."})
}

// serverInfoCatalog answers server info's and server memory's reads; an err
// field set fails that read.
type serverInfoCatalog struct {
	Catalog
	info     *gosmo.ServerInfo
	startErr error
	procErr  error

	stats      *gosmo.ServerMemoryStats
	statsErr   error
	counters   []gosmo.PerformanceCounter
	counterErr error
	clerks     []gosmo.MemoryClerk
	clerkErr   error
	opts       []*gosmo.ConfigurationOption
}

func (c *serverInfoCatalog) ServerInfo() *gosmo.ServerInfo { return c.info }

func (c *serverInfoCatalog) StartTime(context.Context) (time.Time, time.Duration, error) {
	if c.startErr != nil {
		return time.Time{}, 0, c.startErr
	}
	return time.Date(2026, 10, 20, 7, 30, 0, 0, time.UTC), 26*time.Hour + 1500*time.Millisecond, nil
}

func (c *serverInfoCatalog) ProcessorInfo(context.Context) (*gosmo.ProcessorInfo, error) {
	if c.procErr != nil {
		return nil, c.procErr
	}
	return &gosmo.ProcessorInfo{CPUCount: 8, HyperthreadRatio: 2, NUMANodeCount: 2}, nil
}

func (c *serverInfoCatalog) MemoryStats(context.Context) (*gosmo.ServerMemoryStats, error) {
	return c.stats, c.statsErr
}

func (c *serverInfoCatalog) PerformanceCounters(_ context.Context, counters, instances []string) ([]gosmo.PerformanceCounter, error) {
	if !slices.Equal(counters, []string{"Page life expectancy", "Memory Grants Pending"}) || !slices.Equal(instances, []string{""}) {
		return nil, fmt.Errorf("unexpected counters %q, instances %q", counters, instances)
	}
	return c.counters, c.counterErr
}

func (c *serverInfoCatalog) MemoryClerks(context.Context) ([]gosmo.MemoryClerk, error) {
	return c.clerks, c.clerkErr
}

// Configurations is a copy of opts, as each read of the server is: a write
// made since does not change what an earlier read returned.
func (c *serverInfoCatalog) Configurations(context.Context) ([]*gosmo.ConfigurationOption, error) {
	var opts []*gosmo.ConfigurationOption
	for _, o := range c.opts {
		opts = append(opts, new(*o))
	}
	return opts, nil
}

// newServerInfoCatalog is a 2025 Linux instance a sysadmin reads: 64 GB with
// 1840 MB free, max server memory unlimited, 2 grants pending.
func newServerInfoCatalog() *serverInfoCatalog {
	c := &serverInfoCatalog{
		info: &gosmo.ServerInfo{Name: "ubusql1", ProductVersion: "17.0.1000.7", ProductLevel: "RTM", VersionMajor: 17,
			Edition: "Developer Edition (64-bit)", EngineEdition: 3, Platform: "Linux", Collation: "SQL_Latin1_General_CP1_CI_AS",
			IsHADREnabled: true, PhysicalMemoryMB: 65536, LogicalCPUCount: 8,
			DefaultDataPath: "/var/opt/mssql/data/", DefaultLogPath: "/var/opt/mssql/log/", DefaultBackupPath: "/var/opt/mssql/backup"},
		stats: &gosmo.ServerMemoryStats{PhysicalMemoryMB: 65536, AvailableMemoryMB: 1840, TargetServerMemoryMB: 62004, TotalServerMemoryMB: 61210},
		counters: []gosmo.PerformanceCounter{
			{Object: "Buffer Manager", Counter: "Page life expectancy", Value: 412},
			{Object: "Memory Manager", Counter: "Memory Grants Pending", Value: 2},
		},
		opts: []*gosmo.ConfigurationOption{
			{Name: "cost threshold for parallelism", Value: 50, ValueInUse: 50},
			{Name: "max server memory (MB)", Value: 2147483647, ValueInUse: 2147483647},
			{Name: "min server memory (MB)", Value: 0, ValueInUse: 0},
		},
	}
	for i := range 12 {
		c.clerks = append(c.clerks, gosmo.MemoryClerk{Type: fmt.Sprintf("CLERK_%02d", i), MB: float64(i) * 1.5})
	}
	// A tie with CLERK_11 (16.5 MB), broken by type.
	c.clerks = append(c.clerks, gosmo.MemoryClerk{Type: "AAA_TIE", MB: 16.5})
	return c
}

// runServer runs server <command> over cat with argv after it.
func runServer(t *testing.T, cat Catalog, command, argv string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	tree := &spec.Tree{Commands: ServerCommands(d)}
	inv, err := args.Parse(tree, append([]string{"server", command, "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...), args.Options{})
	if err != nil {
		t.Fatalf("%q: %v", argv, err)
	}
	return inv.Command.Run(context.Background(), inv)
}

// fieldMap is res's record by key; a null field is "<null>".
func fieldMap(res *output.Result) (keys []string, values map[string]string) {
	values = map[string]string{}
	for _, f := range res.Fields {
		keys = append(keys, f.Key)
		values[f.Key] = f.Value
		if f.Null {
			values[f.Key] = "<null>"
		}
	}
	return keys, values
}

func TestServerInfo(t *testing.T) {
	res, err := runServer(t, newServerInfoCatalog(), "info", "")
	if err != nil {
		t.Fatal(err)
	}
	keys, got := fieldMap(res)
	if want := []string{"serverName", "productVersion", "majorVersion", "edition", "productLevel", "engineEdition",
		"platform", "collation", "isClustered", "isHadrEnabled", "startTime", "uptimeSeconds", "logicalCpus",
		"numaNodes", "physicalMemoryMb", "defaultDataPath", "defaultLogPath", "defaultBackupPath"}; !slices.Equal(keys, want) {
		t.Errorf("keys %q, want %q", keys, want)
	}
	for k, want := range map[string]string{
		"serverName": "ubusql1", "majorVersion": "17", "productLevel": "RTM", "engineEdition": "3", "platform": "Linux",
		"isClustered": "false", "isHadrEnabled": "true",
		// The server's wall clock, no zone; uptime by the server, whole seconds.
		"startTime": "2026-10-20 07:30:00.000", "uptimeSeconds": "93601",
		"logicalCpus": "8", "numaNodes": "2", "physicalMemoryMb": "65536", "defaultBackupPath": "/var/opt/mssql/backup",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// agent context reports the same server facts, from the same function.
func TestServerInfoSharesAgentContextFields(t *testing.T) {
	info := newServerInfoCatalog().info
	var keys []string
	for _, f := range serverFields(info) {
		keys = append(keys, f.Key)
	}
	if want := []string{"serverName", "productVersion", "majorVersion", "edition"}; !slices.Equal(keys, want) {
		t.Errorf("shared keys %q, want %q", keys, want)
	}
}

// Without VIEW SERVER STATE the DMV fields are null with one warning naming
// the permission, and the rest of the record stands: exit 0 (D5, P5-5).
func TestServerInfoNoViewServerState(t *testing.T) {
	cat := newServerInfoCatalog()
	cat.info.SysInfoUnavailable, cat.info.PhysicalMemoryMB, cat.info.LogicalCPUCount = true, 0, 0
	cat.startErr, cat.procErr = denied("server start time"), denied("processor info")
	res, err := runServer(t, cat, "info", "")
	if err != nil {
		t.Fatalf("%v, want exit 0", err)
	}
	_, got := fieldMap(res)
	for _, k := range []string{"startTime", "uptimeSeconds", "logicalCpus", "numaNodes", "physicalMemoryMb"} {
		if got[k] != "<null>" {
			t.Errorf("%s = %q, want null", k, got[k])
		}
	}
	if got["serverName"] != "ubusql1" || got["collation"] == "" {
		t.Errorf("the rest of the record is gone: %v", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0],
		"startTime, uptimeSeconds, logicalCpus, physicalMemoryMb and numaNodes were not read") ||
		!strings.Contains(res.Warnings[0], "VIEW SERVER STATE") {
		t.Errorf("warnings %q, want one naming the fields and the permission", res.Warnings)
	}
}

// A read failing for another reason is its own warning, with the error.
func TestServerInfoOtherFailure(t *testing.T) {
	cat := newServerInfoCatalog()
	cat.startErr = errors.New("gosmo: server start time: boom")
	res, err := runServer(t, cat, "info", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, got := fieldMap(res); got["startTime"] != "<null>" || got["numaNodes"] != "2" {
		t.Errorf("startTime %q, numaNodes %q", got["startTime"], got["numaNodes"])
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "startTime and uptimeSeconds were not read: gosmo: server start time: boom") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerMemory(t *testing.T) {
	res, err := runServer(t, newServerInfoCatalog(), "memory", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets) != 2 || res.Sets[0].Name != "memory" || res.Sets[1].Name != "clerks" {
		t.Fatalf("sets %+v", res.Sets)
	}
	mem := res.Sets[0]
	for col, want := range map[string]string{
		"physical_mb": "65536", "available_os_mb": "1840", "max_server_memory_mb": "2147483647",
		"min_server_memory_mb": "0", "committed_mb": "61210", "target_mb": "62004",
		"page_life_expectancy_s": "412", "memory_grants_pending": "2",
	} {
		if got := column(mem, col); !slices.Equal(got, []string{want}) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	clerks := res.Sets[1]
	names := column(clerks, "clerk")
	if want := []string{"AAA_TIE", "CLERK_11", "CLERK_10", "CLERK_09", "CLERK_08", "CLERK_07", "CLERK_06", "CLERK_05",
		"CLERK_04", "CLERK_03"}; !slices.Equal(names, want) {
		t.Errorf("clerks %q, want %q", names, want)
	}
	if got := column(clerks, "size_mb"); got[0] != "16.50" || got[9] != "4.50" {
		t.Errorf("size_mb %q", got)
	}
	w := strings.Join(res.Warnings, "\n")
	for _, want := range []string{
		"max server memory (MB) is at the default (2147483647 = unlimited).",
		"Available OS memory is 1840 MB (2.81 % of physical memory, under 5 %).",
		"2 memory grant(s) pending",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q:\n%s", want, w)
		}
	}
	if len(res.Warnings) != 3 {
		t.Errorf("warnings %q, want 3", res.Warnings)
	}
}

// The columns show the running min and max, without a warning when the
// configured value differs (the engine runs min 0 as 16). Counters the server
// does not publish are null.
func TestServerMemoryRunningValuesAndMissingCounters(t *testing.T) {
	cat := newServerInfoCatalog()
	cat.stats.AvailableMemoryMB = 8000
	cat.counters = nil
	cat.opts[1] = &gosmo.ConfigurationOption{Name: "max server memory (MB)", Value: 57344, ValueInUse: 49152}
	cat.opts[2] = &gosmo.ConfigurationOption{Name: "min server memory (MB)", Value: 0, ValueInUse: 16}
	res, err := runServer(t, cat, "memory", "")
	if err != nil {
		t.Fatal(err)
	}
	mem := res.Sets[0]
	if got := column(mem, "max_server_memory_mb"); !slices.Equal(got, []string{"49152"}) {
		t.Errorf("max_server_memory_mb %q, want the running 49152", got)
	}
	if got := column(mem, "min_server_memory_mb"); !slices.Equal(got, []string{"16"}) {
		t.Errorf("min_server_memory_mb %q, want the running 16", got)
	}
	if mem.Rows[0][6] != nil || mem.Rows[0][7] != nil {
		t.Errorf("counters %v %v, want null", mem.Rows[0][6], mem.Rows[0][7])
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q, want none", res.Warnings)
	}
}

// Every server memory figure but min/max is a DMV: without VIEW SERVER
// STATE the command fails, exit 9, naming the permission (P5-6).
func TestServerMemoryNoViewServerState(t *testing.T) {
	for name, set := range map[string]func(*serverInfoCatalog){
		"stats":    func(c *serverInfoCatalog) { c.stats, c.statsErr = nil, denied("server memory stats") },
		"counters": func(c *serverInfoCatalog) { c.counterErr = denied("performance counters") },
		"clerks":   func(c *serverInfoCatalog) { c.clerkErr = denied("read memory clerks") },
	} {
		cat := newServerInfoCatalog()
		set(cat)
		_, err := runServer(t, cat, "memory", "")
		e, ok := errors.AsType[*output.Error](err)
		if !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "VIEW SERVER STATE") {
			t.Errorf("%s: %v, want PERMISSION_DENIED naming VIEW SERVER STATE", name, err)
		}
	}
	cat := newServerInfoCatalog()
	cat.clerkErr = errors.New("gosmo: read memory clerks: boom")
	if _, err := runServer(t, cat, "memory", ""); err == nil {
		t.Error("a failed read succeeded")
	} else if e, ok := errors.AsType[*output.Error](err); ok && e.Code == output.CodePermissionDenied {
		t.Errorf("another failure reported as PERMISSION_DENIED: %v", e)
	}
}
