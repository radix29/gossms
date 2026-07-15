package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverPropPages builds the page set for Server Properties. General and
// Advanced stay read-only (an info page, and a 100+ row raw config dump
// respectively); every other page is editable wherever gosmo has a
// writer for the field shown.
func serverPropPages(sc *db.ServerConn) []propPage {
	return []propPage{
		pageServerGeneral(sc),
		pageServerMemory(sc),
		pageServerProcessors(sc),
		pageServerSecurity(sc),
		pageServerConnections(sc),
		pageServerDatabaseSettings(sc),
		pageServerAdvanced(sc),
		pageServerPermissions(sc),
	}
}

// findConfig returns the option named name, or nil if it isn't present
// (e.g. an option that doesn't exist on this SQL Server version/edition).
func findConfig(configs []*gosmo.ConfigurationOption, name string) *gosmo.ConfigurationOption {
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// configValue returns the in-use value of the named sp_configure option as
// a string, or "N/A" if the option isn't present on this server.
func configValue(configs []*gosmo.ConfigurationOption, name string) string {
	if c := findConfig(configs, name); c != nil {
		return strconv.FormatInt(c.ValueInUse, 10)
	}
	return "N/A"
}

// configRow pairs an editable Int row with the sys.configurations option
// name it edits, so a page's apply closure can write back only the ones
// that changed.
type configRow struct {
	name string
	row  *propsheet.TextRow
}

// configBoolRow is configRow's Check-row counterpart, for 0/1 options.
type configBoolRow struct {
	name string
	row  *propsheet.CheckRow
}

// newConfigEditor returns a builder that creates an editable Int row for
// a named sp_configure option (range-validated against the option's own
// Minimum/Maximum), appending it to *tracked so the page's apply closure
// can find it later. An option missing on this server/edition renders as
// a disabled "N/A" row instead.
func newConfigEditor(configs []*gosmo.ConfigurationOption, tracked *[]configRow) func(name, label, unit string) *propsheet.TextRow {
	return func(name, label, unit string) *propsheet.TextRow {
		c := findConfig(configs, name)
		if c == nil {
			row := propsheet.Text(label, "N/A", 12)
			row.SetEnabled(false)
			return row
		}
		row := propsheet.Int(label, c.ValueInUse, c.Minimum, c.Maximum, unit)
		*tracked = append(*tracked, configRow{name: name, row: row})
		return row
	}
}

// newConfigBoolEditor is newConfigEditor's Check-row counterpart, for
// options whose value is conventionally 0/1.
func newConfigBoolEditor(configs []*gosmo.ConfigurationOption, tracked *[]configBoolRow) func(name, label string) *propsheet.CheckRow {
	return func(name, label string) *propsheet.CheckRow {
		c := findConfig(configs, name)
		row := propsheet.Check(label, c != nil && c.ValueInUse != 0)
		if c == nil {
			return row
		}
		*tracked = append(*tracked, configBoolRow{name: name, row: row})
		return row
	}
}

// applyConfigRows writes back every dirty row in intRows/boolRows via
// ConfigurationOption.SetValueContext. It does not call Reconfigure —
// callers combine this with any other sp_configure-backed change (e.g.
// the Processors page's affinity bitmasks) and call
// Server.ReconfigureContext once at the end.
func applyConfigRows(ctx context.Context, sc *db.ServerConn, intRows []configRow, boolRows []configBoolRow) (changed bool, err error) {
	for _, cr := range intRows {
		if !cr.row.Dirty() {
			continue
		}
		v, err := cr.row.IntValue()
		if err != nil {
			return changed, err
		}
		opt, err := sc.Server.ConfigurationByNameContext(ctx, cr.name)
		if err != nil {
			return changed, err
		}
		if err := opt.SetValueContext(ctx, v); err != nil {
			return changed, err
		}
		changed = true
	}
	for _, cr := range boolRows {
		if !cr.row.Dirty() {
			continue
		}
		v := int64(0)
		if cr.row.Checked() {
			v = 1
		}
		opt, err := sc.Server.ConfigurationByNameContext(ctx, cr.name)
		if err != nil {
			return changed, err
		}
		if err := opt.SetValueContext(ctx, v); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// configApply returns an apply closure for pages whose only edits are
// plain sp_configure-backed rows: write back every dirty one, then call
// Reconfigure once if anything changed.
func configApply(sc *db.ServerConn, intRows []configRow, boolRows []configBoolRow) propApply {
	return func(ctx context.Context) error {
		changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
		if err != nil {
			return err
		}
		if changed {
			return sc.Server.ReconfigureContext(ctx, false)
		}
		return nil
	}
}

// affinityBits unpacks the low cpuCount bits of mask into a per-CPU bool
// slice (bit i = CPU i). Pure and unit-tested — see server_props_test.go.
func affinityBits(mask int64, cpuCount int) []bool {
	bits := make([]bool, cpuCount)
	for i := 0; i < cpuCount; i++ {
		bits[i] = mask&(1<<uint(i)) != 0
	}
	return bits
}

// bitsToAffinity is affinityBits' inverse.
func bitsToAffinity(bits []bool) int64 {
	var mask int64
	for i, b := range bits {
		if b {
			mask |= 1 << uint(i)
		}
	}
	return mask
}

// numaNodeOf renders logical CPU i's NUMA node from a
// gosmo.ProcessorInfo.CPUNUMANode slice, or "N/A" if the server reported
// fewer online schedulers than affinity-mask CPUs (e.g. a CPU disabled by
// the OS but still counted in cpu_count).
func numaNodeOf(cpuNUMANode []int, i int) string {
	if i < 0 || i >= len(cpuNUMANode) {
		return "N/A"
	}
	return strconv.Itoa(cpuNUMANode[i])
}

func pageServerGeneral(sc *db.ServerConn) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			info := sc.Server.Info()
			sec, err := sc.Server.SecurityInfoContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			mem, err := sc.Server.MemoryStatsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			f := propsheet.NewForm(
				propsheet.Section("Server information"),
				propsheet.Static("Name", sc.Opts.Server),
				propsheet.Static("Product", "Microsoft SQL Server"),
				propsheet.Static("Version", info.ProductVersion),
				propsheet.Static("Edition", info.Edition),
				propsheet.Static("Engine edition", engineEditionName(info.EngineEdition)),
				propsheet.Static("Collation", info.Collation),
				propsheet.Static("Language", "English"),
				propsheet.Static("Platform", info.OSVersion),
				propsheet.Section("Availability"),
				propsheet.Static("Is clustered", boolStr(info.IsClustered)),
				propsheet.Static("HADR enabled", boolStr(info.IsHADREnabled)),
				propsheet.Static("Single-user mode", boolStr(info.IsSingleUser)),
				propsheet.Section("Security"),
				propsheet.Static("Authentication", sec.AuthenticationMode),
				propsheet.Section("Resources"),
				propsheet.Static("CPU count", strconv.Itoa(info.LogicalCPUCount)),
				propsheet.Static("Memory", strconv.FormatInt(mem.PhysicalMemoryMB, 10)+" MB"),
				propsheet.Static("Max worker threads", configValue(configs, "max worker threads")),
			)
			return f, nil, nil
		},
	}
}

func pageServerMemory(sc *db.ServerConn) propPage {
	return propPage{
		title: "Memory",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			mem, err := sc.Server.MemoryStatsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			cfgInt := newConfigEditor(configs, &intRows)

			f := propsheet.NewForm(
				propsheet.Section("Server memory options"),
				cfgInt("min server memory (MB)", "Minimum server memory", "MB"),
				cfgInt("max server memory (MB)", "Maximum server memory", "MB"),
				cfgInt("index create memory (KB)", "Index creation memory", "KB"),
				cfgInt("min memory per query (KB)", "Minimum memory per query", "KB"),
				propsheet.Section("Current values"),
				propsheet.Static("Physical memory (MB)", strconv.FormatInt(mem.PhysicalMemoryMB, 10)),
				propsheet.Static("Available memory (MB)", strconv.FormatInt(mem.AvailableMemoryMB, 10)),
				propsheet.Static("Target server memory (MB)", strconv.FormatInt(mem.TargetServerMemoryMB, 10)),
				propsheet.Static("Total server memory (MB)", strconv.FormatInt(mem.TotalServerMemoryMB, 10)),
				propsheet.Note("Max server memory should leave memory for the OS, agents, backups, linked components, and monitoring tools."),
			)
			return f, configApply(sc, intRows, nil), nil
		},
	}
}

func pageServerProcessors(sc *db.ServerConn) propPage {
	return propPage{
		title: "Processors",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			info := sc.Server.Info()
			proc, err := sc.Server.ProcessorInfoContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			// 'affinity mask'/'affinity I/O mask' only cover the first 32
			// CPUs; a 64-CPU server needs 'affinity64 mask' too, which
			// this page doesn't yet edit.
			cpuCount := min(info.LogicalCPUCount, 32)
			affMask, ioMask := int64(0), int64(0)
			if c := findConfig(configs, "affinity mask"); c != nil {
				affMask = c.ValueInUse
			}
			if c := findConfig(configs, "affinity I/O mask"); c != nil {
				ioMask = c.ValueInUse
			}
			cpuAff := affinityBits(affMask, cpuCount)
			ioAff := affinityBits(ioMask, cpuCount)
			autoAffinity := propsheet.Check("Automatically set processor affinity mask for all processors", affMask == 0)
			autoIOAffinity := propsheet.Check("Automatically set I/O affinity mask for all processors", ioMask == 0)

			text := make([][]string, cpuCount)
			values := make([][]bool, cpuCount)
			for i := 0; i < cpuCount; i++ {
				text[i] = []string{"Processor " + strconv.Itoa(i), numaNodeOf(proc.CPUNUMANode, i)}
				values[i] = []bool{cpuAff[i], ioAff[i]}
			}
			affinityGrid := propsheet.NewToggleGrid([]string{"CPU", "Affinity", "I/O Affinity", "NUMA"}, []int{1, 2}, min(cpuCount+3, 12))
			affinityGrid.SetRows(text, values)

			f := propsheet.NewForm(
				propsheet.Section("Processor information"),
				propsheet.Static("Processors", strconv.Itoa(proc.CPUCount)),
				propsheet.Static("NUMA nodes", strconv.Itoa(proc.NUMANodeCount)),
				propsheet.Static("Hyperthread ratio", strconv.Itoa(proc.HyperthreadRatio)),
				propsheet.Section("Processor affinity"),
				autoAffinity,
				autoIOAffinity,
				affinityGrid,
				propsheet.Section("Threads"),
				cfgInt("max worker threads", "Maximum worker threads", ""),
				cfgBool("priority boost", "Boost SQL Server priority"),
				cfgBool("lightweight pooling", "Use Windows fibers"),
				propsheet.Section("Parallelism"),
				cfgInt("max degree of parallelism", "Max degree of parallelism", ""),
				cfgInt("cost threshold for parallelism", "Cost threshold for parallelism", ""),
			)

			apply := func(ctx context.Context) error {
				changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
				if err != nil {
					return err
				}
				newCPUAff := make([]bool, cpuCount)
				newIOAff := make([]bool, cpuCount)
				for i, v := range affinityGrid.Values() {
					newCPUAff[i], newIOAff[i] = v[0], v[1]
				}
				wantAffMask := bitsToAffinity(newCPUAff)
				if autoAffinity.Checked() {
					wantAffMask = 0
				}
				if wantAffMask != affMask {
					opt, err := sc.Server.ConfigurationByNameContext(ctx, "affinity mask")
					if err != nil {
						return err
					}
					if err := opt.SetValueContext(ctx, wantAffMask); err != nil {
						return err
					}
					changed = true
				}
				wantIOMask := bitsToAffinity(newIOAff)
				if autoIOAffinity.Checked() {
					wantIOMask = 0
				}
				if wantIOMask != ioMask {
					opt, err := sc.Server.ConfigurationByNameContext(ctx, "affinity I/O mask")
					if err != nil {
						return err
					}
					if err := opt.SetValueContext(ctx, wantIOMask); err != nil {
						return err
					}
					changed = true
				}
				if changed {
					return sc.Server.ReconfigureContext(ctx, false)
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageServerSecurity(sc *db.ServerConn) propPage {
	return propPage{
		title: "Security",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sec, err := sc.Server.SecurityInfoContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var boolRows []configBoolRow
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			f := propsheet.NewForm(
				propsheet.Section("Server authentication"),
				propsheet.Static("Authentication mode", sec.AuthenticationMode),
				propsheet.Section("Options"),
				cfgBool("contained database authentication", "Allow contained database authentication"),
				cfgBool("cross db ownership chaining", "Cross database ownership chaining"),
				cfgBool("c2 audit mode", "Enable C2 audit tracing"),
				propsheet.Note("Login auditing and the server proxy account are read from the registry, which this build does not access — see SQL Server's own security policy tooling for those."),
			)
			return f, configApply(sc, nil, boolRows), nil
		},
	}
}

func pageServerConnections(sc *db.ServerConn) propPage {
	return propPage{
		title: "Connections",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			f := propsheet.NewForm(
				propsheet.Section("Connections"),
				cfgInt("user connections", "Maximum concurrent connections", ""),
				propsheet.Section("Remote server connections"),
				cfgBool("remote access", "Allow remote connections to this server"),
				cfgBool("remote proc trans", "Require distributed transactions"),
				cfgInt("remote query timeout (s)", "Remote query timeout", "sec"),
				cfgInt("remote login timeout (s)", "Remote login timeout", "sec"),
				propsheet.Section("Query governor"),
				cfgInt("query governor cost limit", "Estimated query cost limit", ""),
				propsheet.Section("Default connection options"),
				cfgInt("network packet size (B)", "Network packet size", "bytes"),
				propsheet.Section("User defaults"),
				cfgInt("default language", "Default language", ""),
				cfgInt("default full-text language", "Default full-text language", ""),
				cfgInt("two digit year cutoff", "Two digit year cutoff", ""),
				propsheet.Note("A value of 0 for maximum connections means SQL Server automatically manages the limit."),
			)
			return f, configApply(sc, intRows, boolRows), nil
		},
	}
}

var filestreamLevels = []string{"Disabled", "Transact-SQL access enabled", "Full access enabled"}

func pageServerDatabaseSettings(sc *db.ServerConn) propPage {
	return propPage{
		title: "Database Settings",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			info := sc.Server.Info()
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			filestream := propsheet.Select("FILESTREAM access level", filestreamLevels, 0)
			var filestreamCfg *gosmo.ConfigurationOption
			if c := findConfig(configs, "filestream access level"); c != nil {
				filestreamCfg = c
				filestream.SetSelected(int(c.ValueInUse))
			} else {
				filestream = propsheet.Select("FILESTREAM access level", []string{"N/A"}, 0)
			}

			f := propsheet.NewForm(
				propsheet.Section("Default database settings"),
				cfgInt("fill factor (%)", "Default index fill factor", "%"),
				propsheet.Section("Backup and restore"),
				cfgInt("media retention", "Backup media retention", "days"),
				cfgBool("backup compression default", "Backup compression default"),
				cfgBool("backup checksum default", "Backup checksum default"),
				propsheet.Section("Default locations"),
				propsheet.Static("Data", info.DefaultDataPath),
				propsheet.Static("Log", info.DefaultLogPath),
				propsheet.Static("Backup", info.DefaultBackupPath),
				propsheet.Section("Recovery"),
				cfgInt("recovery interval (min)", "Recovery interval", "min"),
				propsheet.Section("FILESTREAM"),
				filestream,
				propsheet.Note("A fill factor of 0 uses the server default."),
			)

			apply := func(ctx context.Context) error {
				changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
				if err != nil {
					return err
				}
				if filestreamCfg != nil && filestream.Dirty() {
					opt, err := sc.Server.ConfigurationByNameContext(ctx, "filestream access level")
					if err != nil {
						return err
					}
					if err := opt.SetValueContext(ctx, int64(filestream.Selected())); err != nil {
						return err
					}
					changed = true
				}
				if changed {
					return sc.Server.ReconfigureContext(ctx, false)
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageServerAdvanced groups the sp_configure options that have no more
// specific home elsewhere (Memory/Processors/Connections/Database
// Settings/Security already cover the rest) into editable rows, then
// lists every remaining option — including ones this build doesn't expose
// individually — in a read-only grid underneath for full visibility.
func pageServerAdvanced(sc *db.ServerConn) propPage {
	return propPage{
		title: "Advanced",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			rows := make([][]string, len(configs))
			for i, c := range configs {
				rows[i] = []string{c.Name, strconv.FormatInt(c.ValueInUse, 10), c.Description}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Option", "Value", "Description"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Miscellaneous"),
				cfgBool("optimize for ad hoc workloads", "Optimize for ad hoc workloads"),
				cfgInt("blocked process threshold (s)", "Blocked process threshold", "sec"),
				cfgInt("cursor threshold", "Cursor threshold", ""),
				cfgInt("in-doubt xact resolution", "In-doubt xact resolution", ""),
				cfgBool("scan for startup procs", "Scan for startup procs"),
				propsheet.Section("Security"),
				cfgBool("common criteria compliance enabled", "Common criteria compliance enabled"),
				cfgBool("default trace enabled", "Default trace enabled"),
				propsheet.Section("Server Configuration"),
				cfgBool("Ad Hoc Distributed Queries", "Ad Hoc Distributed Queries"),
				cfgBool("Agent XPs", "Agent XPs"),
				cfgBool("clr enabled", "CLR enabled"),
				cfgBool("clr strict security", "CLR strict security"),
				cfgBool("Database Mail XPs", "Database Mail XPs"),
				cfgBool("external scripts enabled", "External scripts enabled"),
				cfgBool("Ole Automation Procedures", "Ole Automation Procedures"),
				cfgBool("remote admin connections", "Remote admin connections"),
				cfgBool("show advanced options", "Show advanced options"),
				cfgBool("xp_cmdshell", "xp_cmdshell"),
				propsheet.Section("All server configuration options (sys.configurations)"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("The grid above is read-only — edit an option from its group above, or from its own page (Memory, Processors, Connections, Database Settings, Security), if it has one."),
			)
			return f, configApply(sc, intRows, boolRows), nil
		},
	}
}

func pageServerPermissions(sc *db.ServerConn) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := sc.Server.ServerRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			entries := make([]permEntry, len(perms))
			for i, p := range perms {
				entries[i] = permEntry{
					Principal: p.Principal, PrincipalType: p.PrincipalType,
					Grantor: p.Grantor, Permission: p.Permission, State: p.State,
				}
			}
			principals := make([]permPrincipal, 0, len(logins)+len(roles))
			for _, l := range logins {
				principals = append(principals, permPrincipal{Name: l.Name, Type: l.LoginType})
			}
			for _, r := range roles {
				principals = append(principals, permPrincipal{Name: r.Name, Type: "SERVER_ROLE"})
			}

			f, apply := buildPermissionsMatrix(principals, gosmo.ServerPermissionNames(), entries, 8, 12,
				sc.Server.GrantServerPermissionContext,
				sc.Server.DenyServerPermissionContext,
				sc.Server.RevokeServerPermissionContext,
			)
			return f, apply, nil
		},
	}
}
