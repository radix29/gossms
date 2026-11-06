package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// instanceServer is real gosmo over canned rows for the server group (info,
// memory, config, traceflag): a catalogServer with the instance's connect
// reads (memory and CPUs, or a refusal), its start time, processor layout,
// memory figures, counters, clerks, sys.configurations (opts) and trace
// flags. A write changes opts or flags as the server would, from the T-SQL
// (applyWrite), so the read after it sees it.
//
// Every refusal is the server's: an err field fails that read with it.
type instanceServer struct {
	*catalogServer

	// The connect-time sys.dm_os_sys_info read: physical memory and CPUs,
	// or noSysInfo for the refusal gosmo reports as SysInfoUnavailable.
	physicalMB, cpus int64
	noSysInfo        bool

	start    time.Time
	uptime   time.Duration
	startErr error

	// hyperthread and cpuNodes (the NUMA node of each CPU) are
	// ProcessorInfo's reads; procErr fails the first.
	hyperthread int64
	cpuNodes    []int
	procErr     error

	stats      *gosmo.ServerMemoryStats
	statsErr   error
	counters   []gosmo.PerformanceCounter
	counterErr error
	clerks     []gosmo.MemoryClerk
	clerkErr   error

	// opts is sys.configurations; every read answers a snapshot.
	opts []*gosmo.ConfigurationOption

	// flags is DBCC TRACESTATUS(-1). flagReads counts its reads; flagErr
	// fails every one after the first flagOK.
	flags     []gosmo.TraceFlag
	flagReads int
	flagErr   error
	flagOK    int

	// writes is each write sent, as sent. writeErr fails a write: for a
	// configuration batch after the first applied changes (everything, with
	// refused, which leaves them configured and not running as a refused
	// RECONFIGURE does); for a trace flag at failFlag. startup lists the
	// flags the server skips without an error.
	writes    []string
	writeErr  error
	applied   int
	refused   bool
	failFlag  int
	startup   []int
	overrides []bool // each batch's WITH OVERRIDE
}

var instanceAt = time.Date(2026, 10, 20, 7, 30, 0, 0, time.UTC)

// newInstanceServer is a 2025 Linux instance a sysadmin reads: 64 GB with
// 1840 MB free, 8 CPUs on 2 NUMA nodes, max server memory unlimited, 2
// grants pending, up 26 hours.
func newInstanceServer(t *testing.T) *instanceServer {
	s := &instanceServer{
		physicalMB: 65536, cpus: 8,
		start: instanceAt, uptime: 26*time.Hour + 1500*time.Millisecond,
		hyperthread: 2, cpuNodes: []int{0, 0, 0, 0, 1, 1, 1, 1},
		stats: &gosmo.ServerMemoryStats{PhysicalMemoryMB: 65536, AvailableMemoryMB: 1840, TargetServerMemoryMB: 62004, TotalServerMemoryMB: 61210},
		counters: []gosmo.PerformanceCounter{
			{Object: "Buffer Manager", Counter: "Page life expectancy", Value: 412},
			{Object: "Memory Manager", Counter: "Memory Grants Pending", Value: 2},
		},
		opts: []*gosmo.ConfigurationOption{
			{Name: "cost threshold for parallelism", Value: 50, ValueInUse: 50},
			{Name: maxServerMemory, Value: 2147483647, ValueInUse: 2147483647},
			{Name: minServerMemory, Value: 0, ValueInUse: 0},
		},
	}
	for i := range 12 {
		s.clerks = append(s.clerks, gosmo.MemoryClerk{Type: fmt.Sprintf("CLERK_%02d", i), MB: float64(i) * 1.5})
	}
	// A tie with CLERK_11 (16.5 MB), broken by type.
	s.clerks = append(s.clerks, gosmo.MemoryClerk{Type: "AAA_TIE", MB: 16.5})
	newCatalogServerWith(t, "master", func(c *catalogServer) {
		s.catalogServer = c
		c.Info.Name, c.Info.ProductVersion, c.Info.ProductLevel = "ubusql1", "17.0.1000.7", "RTM"
		c.Info.Version = "Microsoft SQL Server 2025 (RTM) - 17.0.1000.7 (X64) Developer Edition (64-bit) on Linux (Ubuntu 24.04) <X64>"
		c.Info.HADREnabled = true
		c.Info.DataPath, c.Info.LogPath, c.Info.BackupPath = "/var/opt/mssql/data/", "/var/opt/mssql/log/", "/var/opt/mssql/backup"
		s.rules()
	})
	return s
}

// newConfigServer is newInstanceServer with a sys.configurations read: out
// of name order, an option a CU added (no default), the version parts, min
// server memory configured 0 and running 16 as the engine runs it. The
// defaults are gosmo's own table's.
func newConfigServer(t *testing.T) *instanceServer {
	s := newInstanceServer(t)
	opt := func(name string, value, inUse int64, dynamic, advanced bool) *gosmo.ConfigurationOption {
		return &gosmo.ConfigurationOption{Name: name, Value: value, ValueInUse: inUse, Minimum: 0, Maximum: 32767,
			IsDynamic: dynamic, IsAdvanced: advanced}
	}
	s.opts = []*gosmo.ConfigurationOption{
		opt("max degree of parallelism", 8, 8, true, true),
		opt("backup compression default", 0, 0, true, false),
		opt("Backup checksum default", 1, 1, true, false),
		opt("cost threshold for parallelism", 5, 5, true, true),
		opt(maxServerMemory, 2147483647, 2147483647, true, true),
		opt(minServerMemory, 0, 16, true, true),
		opt("fill factor (%)", 90, 0, false, true),
		opt("tiered memory enabled", 0, 0, false, true),
		opt("version high part of SQL Server", 1114112, 1114112, true, true),
	}
	return s
}

// newConfigSetServer is newConfigServer with the memory options' real
// ranges, show advanced options off, and locks, a self-configuring option
// whose default lies below its minimum.
func newConfigSetServer(t *testing.T) *instanceServer {
	s := newConfigServer(t)
	for _, o := range s.opts {
		switch o.Name {
		case maxServerMemory:
			o.Minimum, o.Maximum = 128, 2147483647
		case minServerMemory:
			o.Maximum = 2147483647
		}
	}
	s.opts = append(s.opts,
		&gosmo.ConfigurationOption{Name: "show advanced options", Maximum: 1, IsDynamic: true},
		&gosmo.ConfigurationOption{Name: "locks", Value: 5000, ValueInUse: 5000, Minimum: 5000, Maximum: 2147483647, IsAdvanced: true},
		&gosmo.ConfigurationOption{Name: "min memory per query (KB)", Value: 1024, ValueInUse: 1024, Minimum: 512,
			Maximum: 2147483647, IsDynamic: true, IsAdvanced: true},
	)
	return s
}

// opt is the option named name in s, exactly.
func (s *instanceServer) opt(name string) *gosmo.ConfigurationOption {
	i := slices.IndexFunc(s.opts, func(o *gosmo.ConfigurationOption) bool { return o.Name == name })
	if i < 0 {
		s.t.Fatalf("no option %q", name)
	}
	return s.opts[i]
}

// reads is how many queries s was sent containing every one of subs.
func (s *instanceServer) reads(subs ...string) int {
	n := 0
	for _, q := range s.Queries() {
		if has(q, subs...) {
			n++
		}
	}
	return n
}

// sqlErr is the server's error number n.
func sqlErr(n int32, class uint8, msg string) error {
	return mssql.Error{Number: n, Class: class, Message: msg}
}

// denied is the server refusing a DMV without VIEW SERVER STATE.
func denied() error {
	return sqlErr(300, 14, "VIEW SERVER STATE permission was denied on object 'server', database 'master'.")
}

// The writes the server takes.
var (
	spConfigure = regexp.MustCompile(`(?m)^EXEC sys\.sp_configure N'((?:[^']|'')*)', (-?\d+);`)
	dbccTrace   = regexp.MustCompile(`^DBCC (TRACEON|TRACEOFF)\((\d+), -1\) WITH NO_INFOMSGS$`)
)

// applyWrite is the server taking q: recorded, then a configuration batch's
// sp_configure calls or a DBCC TRACEON/TRACEOFF change opts or flags.
func (s *instanceServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	s.writes = append(s.writes, q.Text)
	if m := dbccTrace.FindStringSubmatch(strings.TrimSpace(q.Text)); m != nil {
		flag, _ := strconv.Atoi(m[2])
		if s.writeErr != nil && flag == s.failFlag {
			return gosmotest.Result{Err: s.writeErr}
		}
		if slices.Contains(s.startup, flag) {
			return gosmotest.Result{}
		}
		s.flags = slices.DeleteFunc(s.flags, func(f gosmo.TraceFlag) bool { return f.Flag == flag })
		if m[1] == "TRACEON" {
			s.flags = append(s.flags, gosmo.TraceFlag{Flag: flag, Global: true})
		}
		return gosmotest.Result{}
	}
	changes := spConfigure.FindAllStringSubmatch(q.Text, -1)
	if changes == nil {
		s.t.Errorf("instanceServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	}
	s.overrides = append(s.overrides, strings.Contains(q.Text, "RECONFIGURE WITH OVERRIDE"))
	for i, m := range changes {
		if s.writeErr != nil && !s.refused && i >= s.applied {
			break
		}
		v, _ := strconv.ParseInt(m[2], 10, 64)
		o := s.opt(strings.ReplaceAll(m[1], "''", "'"))
		o.Value = v
		if o.IsDynamic && !s.refused {
			o.ValueInUse = v
			if o.Name == minServerMemory && v == 0 {
				o.ValueInUse = 16
			}
		}
	}
	return gosmotest.Result{Err: s.writeErr}
}

func (s *instanceServer) rules() {
	fail := func(err error) gosmotest.Result { return gosmotest.Result{Err: err} }
	rows := func(rows ...[]any) gosmotest.Result { return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}} }
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)
	// Connect's read of memory and CPUs.
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "osi.physical_memory_kb") }, func(gosmotest.Query) gosmotest.Result {
		if s.noSysInfo {
			return fail(denied())
		}
		return rows([]any{s.physicalMB, s.cpus})
	})
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "sqlserver_start_time") }, func(gosmotest.Query) gosmotest.Result {
		if s.startErr != nil {
			return fail(s.startErr)
		}
		return rows([]any{s.start, s.uptime.Milliseconds()})
	})
	s.OnFunc(func(q gosmotest.Query) bool {
		return has(q, "SELECT cpu_count, hyperthread_ratio FROM sys.dm_os_sys_info")
	},
		func(gosmotest.Query) gosmotest.Result {
			if s.procErr != nil {
				return fail(s.procErr)
			}
			return rows([]any{int64(len(s.cpuNodes)), s.hyperthread})
		})
	s.on(func(gosmotest.Query) [][]any {
		var out [][]any
		for cpu, node := range s.cpuNodes {
			out = append(out, []any{int64(cpu), int64(node)})
		}
		return out
	}, "FROM   sys.dm_os_schedulers", "GROUP  BY cpu_id, parent_node_id")
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM sys.dm_os_sys_memory") }, func(gosmotest.Query) gosmotest.Result {
		if s.statsErr != nil {
			return fail(s.statsErr)
		}
		return rows([]any{s.stats.PhysicalMemoryMB, s.stats.AvailableMemoryMB, s.stats.TargetServerMemoryMB, s.stats.TotalServerMemoryMB})
	})
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.dm_os_performance_counters") }, func(q gosmotest.Query) gosmotest.Result {
		if want := []any{"Page life expectancy", "Memory Grants Pending", ""}; !slices.Equal(q.Args, want) {
			s.t.Errorf("instanceServer: counters %q, want %q", q.Args, want)
		}
		if s.counterErr != nil {
			return fail(s.counterErr)
		}
		var out [][]any
		for _, c := range s.counters {
			out = append(out, []any{"SQLServer:" + c.Object, c.Counter, c.Instance, c.Value, int64(c.Type)})
		}
		return rows(out...)
	})
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.dm_os_memory_clerks") }, func(gosmotest.Query) gosmotest.Result {
		if s.clerkErr != nil {
			return fail(s.clerkErr)
		}
		var out [][]any
		for _, c := range s.clerks {
			out = append(out, []any{c.Type, c.MB})
		}
		return rows(out...)
	})
	s.on(func(gosmotest.Query) [][]any {
		opts := slices.Clone(s.opts)
		// ORDER BY name, in the server's case-insensitive collation.
		slices.SortStableFunc(opts, func(a, b *gosmo.ConfigurationOption) int {
			return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		var out [][]any
		for i, o := range opts {
			out = append(out, []any{int64(i + 1), o.Name, o.Value, o.ValueInUse, o.Minimum, o.Maximum, o.IsDynamic, o.IsAdvanced, nil})
		}
		return out
	}, "FROM   sys.configurations", "ORDER  BY name")
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "DBCC TRACESTATUS(-1)") }, func(gosmotest.Query) gosmotest.Result {
		s.flagReads++
		if s.flagErr != nil && s.flagReads > s.flagOK {
			return fail(s.flagErr)
		}
		flags := slices.Clone(s.flags)
		slices.SortFunc(flags, func(a, b gosmo.TraceFlag) int { return a.Flag - b.Flag })
		var out [][]any
		for _, f := range flags {
			out = append(out, []any{int64(f.Flag), int64(1), boolInt64(f.Global), boolInt64(f.Session)})
		}
		if out == nil {
			return gosmotest.Result{} // no flag on: the DBCC returns no result set
		}
		return rows(out...)
	})
}

func boolInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// serverDeps is Deps over s, an ad-hoc connection counted in dials.
func serverDeps(s *instanceServer, dials *int) Deps {
	return Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			*dials++
			return s.conn(ctx)
		},
		Catalog: ServerCatalog,
	}
}

// runServerCmd runs server <argv> over s (argv starts with the command and,
// for config and traceflag, its subcommand); dials counts the connects. The
// connection is --server srv --auth windows.
func runServerCmd(t *testing.T, s *instanceServer, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := serverDeps(s, &dials)
	fields := strings.Fields(argv)
	n := 1
	if fields[0] == "config" || fields[0] == "traceflag" {
		n = 2
	}
	args0 := append([]string{"server"}, fields[:n]...)
	args0 = append(append(args0, "--server", "srv", "--auth", "windows"), fields[n:]...)
	inv, perr := args.Parse(&spec.Tree{Commands: ServerCommands(d)}, args0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
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

func errCode(err error) output.Code {
	if e, ok := errors.AsType[*output.Error](err); ok {
		return e.Code
	}
	return ""
}
