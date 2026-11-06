package cmd

import (
	"errors"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// runServer runs server <command> <argv> over s.
func runServer(t *testing.T, s *instanceServer, command, argv string) (*output.Result, error) {
	t.Helper()
	res, _, err := runServerCmd(t, s, command+" "+argv)
	return res, err
}

func TestServerInfo(t *testing.T) {
	res, err := runServer(t, newInstanceServer(t), "info", "")
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
	info := newInstanceServer(t).Server().Info()
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
	cat := newInstanceServer(t)
	cat.noSysInfo = true
	cat.startErr, cat.procErr = denied(), denied()
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
	cat := newInstanceServer(t)
	cat.startErr = errors.New("boom")
	res, err := runServer(t, cat, "info", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, got := fieldMap(res); got["startTime"] != "<null>" || got["numaNodes"] != "2" {
		t.Errorf("startTime %q, numaNodes %q", got["startTime"], got["numaNodes"])
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "startTime and uptimeSeconds were not read: ") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerMemory(t *testing.T) {
	res, err := runServer(t, newInstanceServer(t), "memory", "")
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
	cat := newInstanceServer(t)
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
	for name, set := range map[string]func(*instanceServer){
		"stats":    func(c *instanceServer) { c.stats, c.statsErr = nil, denied() },
		"counters": func(c *instanceServer) { c.counterErr = denied() },
		"clerks":   func(c *instanceServer) { c.clerkErr = denied() },
	} {
		cat := newInstanceServer(t)
		set(cat)
		_, err := runServer(t, cat, "memory", "")
		e, ok := errors.AsType[*output.Error](err)
		if !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "VIEW SERVER STATE") {
			t.Errorf("%s: %v, want PERMISSION_DENIED naming VIEW SERVER STATE", name, err)
		}
	}
	cat := newInstanceServer(t)
	cat.clerkErr = errors.New("boom")
	if _, err := runServer(t, cat, "memory", ""); err == nil {
		t.Error("a failed read succeeded")
	} else if e, ok := errors.AsType[*output.Error](err); ok && e.Code == output.CodePermissionDenied {
		t.Errorf("another failure reported as PERMISSION_DENIED: %v", e)
	}
}
