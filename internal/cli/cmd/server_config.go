package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// server config's own options.
const (
	optNonDefault = "non-default"
	optAdvanced   = "advanced"
)

// configAlias is one short name an agent may use for an sp_configure option
// (§4.2's table, D9).
type configAlias struct {
	Alias, Name string
}

// configAliases is every alias, in §4.2's order. A name resolves as an alias
// first, then as a sys.configurations name; both ignore case.
var configAliases = []configAlias{
	{"maxdop", "max degree of parallelism"},
	{"cost-threshold", "cost threshold for parallelism"},
	{"max-memory", maxServerMemory},
	{"min-memory", minServerMemory},
	{"adhoc", "optimize for ad hoc workloads"},
	{"backup-compression", "backup compression default"},
	{"backup-checksum", "backup checksum default"},
	{"remote-dac", "remote admin connections"},
	{"clr", "clr enabled"},
	{"xp-cmdshell", "xp_cmdshell"},
	{"contained-auth", "contained database authentication"},
	{"blocked-threshold", "blocked process threshold (s)"},
}

// aliasOf is name's alias, "" when it has none.
func aliasOf(name string) string {
	for _, a := range configAliases {
		if strings.EqualFold(a.Name, name) {
			return a.Alias
		}
	}
	return ""
}

// The options whose server config get carries context: the CPU layout for
// the parallelism ones, memory for the server memory ones.
var (
	cpuContextOptions    = []string{"max degree of parallelism", "cost threshold for parallelism"}
	memoryContextOptions = []string{maxServerMemory, minServerMemory}
)

// versionParts are the engine-written options gosmo has no default for: they
// are the build number, not settings, so the missing-default warning leaves
// them out.
var versionParts = []string{"version high part of SQL Server", "version low part of SQL Server"}

// maxConfigMatches bounds the names an unknown setting's hint suggests (D9).
const maxConfigMatches = 3

const configDefaultsText = "default is the engine's own default on this major version (gosmo's table, from Microsoft's " +
	"sp_configure reference), not what Setup or mssql-conf writes (Setup recommends MAXDOP and max server memory); " +
	"it is null where gosmo has none (an option a CU added, Azure), with a warning naming them."

func serverConfigCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"server", "config"},
			Summary: "List and show the sp_configure settings with their defaults",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"server", "config", "list"},
			Summary: "List the sp_configure settings: configured, running, default, range",
			Description: "One row per sys.configurations option, by name, advanced ones included whatever show " +
				"advanced options is set to. configured is value, running is value_in_use; " + configDefaultsText +
				" --name keeps the names containing the text (ignoring case), --advanced the advanced options, " +
				"--non-default those whose configured value differs from the default (an option with no known " +
				"default is left out). alias is the short name server config get takes, where there is one.",
			Options: append(ConnectOptions(),
				spec.Option{Name: optName, Type: spec.TypeString, Help: "Only names containing this text"},
				spec.Option{Name: optNonDefault, Type: spec.TypeBool, Help: "Only settings whose configured value differs from the default"},
				spec.Option{Name: optAdvanced, Type: spec.TypeBool, Help: "Only advanced settings"},
			),
			Class: spec.ClassRead,
			Run:   d.serverConfigList,
			Examples: []string{
				"gossms server config list -p dev --non-default",
				"gossms server config list -p dev --name backup -o json",
			},
		},
		{
			Path:    []string{"server", "config", "get"},
			Summary: "Show settings by name or alias, with the CPU or memory context to judge them",
			Description: "The list's row for each setting named, in the order given. A name is an alias " +
				"(maxdop, cost-threshold, max-memory, min-memory, adhoc, backup-compression, backup-checksum, " +
				"remote-dac, clr, xp-cmdshell, contained-auth, blocked-threshold) or a full sys.configurations " +
				"name, ignoring case; an unknown one is exit 8 naming up to 3 that contain it. " + configDefaultsText +
				" The record beside the set is context: logicalCpus, numaNodes and logicalCpusPerNumaNode for " +
				"max degree of parallelism and cost threshold for parallelism; physicalMemoryMb and " +
				"committedMemoryMb (Total Server Memory) for max and min server memory. They need " +
				serverStateHint + ": without it they are null with one warning (exit 0).",
			Args:    []spec.Arg{{Name: "name", Help: "A setting's alias or sys.configurations name", Required: true, Repeat: true}},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.serverConfigGet,
			Examples: []string{
				"gossms server config get maxdop -p dev",
				"gossms server config get max-memory min-memory -p dev -o json",
				`gossms server config get "cost threshold for parallelism" -p dev`,
			},
		},
	}
}

// --- server config list -----------------------------------------------------

func (d Deps) serverConfigList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	opts, err := cat.Configurations(ctx)
	if err != nil {
		return res, readError(err)
	}
	name := strings.ToLower(inv.Values[optName].(string))
	nonDefault, advanced := inv.Values[optNonDefault].(bool), inv.Values[optAdvanced].(bool)
	var rows []*gosmo.ConfigurationOption
	for _, o := range sortedOptions(opts) {
		if !strings.Contains(strings.ToLower(o.Name), name) || advanced && !o.IsAdvanced {
			continue
		}
		rows = append(rows, o)
	}
	res.Warnings = append(res.Warnings, missingDefaults(rows, opts)...)
	if nonDefault {
		rows = slices.DeleteFunc(rows, func(o *gosmo.ConfigurationOption) bool { return !o.HasDefault || o.Value == o.Default })
	}
	res.Sets = []output.Set{configSet(rows)}
	return res, nil
}

// sortedOptions is opts by name, ignoring case (sys.configurations has no
// order of its own).
func sortedOptions(opts []*gosmo.ConfigurationOption) []*gosmo.ConfigurationOption {
	opts = slices.Clone(opts)
	slices.SortFunc(opts, func(a, b *gosmo.ConfigurationOption) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	return opts
}

// missingDefaults is the warning for the options in rows gosmo has no default
// for, the version parts aside; none when every one has one. A server where
// no option in all has one (Azure, a major newer than gosmo's table) is one
// sentence, not a list of them all.
func missingDefaults(rows, all []*gosmo.ConfigurationOption) []string {
	var names []string
	for _, o := range rows {
		if !o.HasDefault && !slices.Contains(versionParts, o.Name) {
			names = append(names, o.Name)
		}
	}
	switch {
	case len(names) == 0:
		return nil
	case !slices.ContainsFunc(all, func(o *gosmo.ConfigurationOption) bool { return o.HasDefault }):
		return []string{"default is null for every setting: gosmo has no defaults for this server (Azure, or a major version newer than its table)."}
	}
	return []string{fmt.Sprintf("default is null for %s: gosmo has no default for %s (a cumulative update may have added %s); "+
		"--non-default leaves %s out.", strings.Join(names, ", "), them(len(names)), them(len(names)), them(len(names)))}
}

// them is "it" or "them" for n things.
func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// configSet is rows as server config list's and get's result set: §10.1's
// columns.
func configSet(rows []*gosmo.ConfigurationOption) output.Set {
	bigint := func(name string, nullable bool) output.Column {
		return output.Column{Name: name, Type: "bigint", Nullable: nullable}
	}
	set := output.Set{Columns: []output.Column{
		{Name: "name", Type: "nvarchar(35)"}, {Name: "alias", Type: "varchar(20)", Nullable: true},
		bigint("configured", false), bigint("running", false), bigint("default", true),
		bigint("min", false), bigint("max", false),
		{Name: "dynamic", Type: "bit"}, {Name: "advanced", Type: "bit"},
	}}
	num := func(v int64) *string { return ptr(strconv.FormatInt(v, 10)) }
	for _, o := range rows {
		var alias, def *string
		if a := aliasOf(o.Name); a != "" {
			alias = ptr(a)
		}
		if o.HasDefault {
			def = num(o.Default)
		}
		set.Rows = append(set.Rows, []*string{ptr(o.Name), alias, num(o.Value), num(o.ValueInUse), def,
			num(o.Minimum), num(o.Maximum), bitCell(o.IsDynamic), bitCell(o.IsAdvanced)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// --- server config get ------------------------------------------------------

// resolveConfig is the option in opts that name means: an alias, else a
// sys.configurations name, ignoring case (D9). Anything else is NOT_FOUND
// naming up to maxConfigMatches options or aliases that contain it.
func resolveConfig(opts []*gosmo.ConfigurationOption, name string) (*gosmo.ConfigurationOption, error) {
	want := name
	for _, a := range configAliases {
		if strings.EqualFold(a.Alias, name) {
			want = a.Name
			break
		}
	}
	for _, o := range opts {
		if strings.EqualFold(o.Name, want) {
			return o, nil
		}
	}
	var matches []string
	lower := strings.ToLower(name)
	for _, o := range sortedOptions(opts) {
		if len(matches) == maxConfigMatches {
			break
		}
		a := aliasOf(o.Name)
		switch {
		case strings.Contains(strings.ToLower(o.Name), lower):
			matches = append(matches, strconv.Quote(o.Name))
		case a != "" && strings.Contains(a, lower):
			matches = append(matches, a)
		}
	}
	hint := "Run `gossms server config list --name <text>` to find the setting's name."
	if len(matches) > 0 {
		hint = "Did you mean " + strings.Join(matches, ", ") + "?"
	}
	return nil, output.NewError(output.CodeNotFound, fmt.Sprintf("no server setting %q", name), hint)
}

func (d Deps) serverConfigGet(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	opts, err := cat.Configurations(ctx)
	if err != nil {
		return res, readError(err)
	}
	var rows []*gosmo.ConfigurationOption
	for _, name := range inv.Args {
		o, err := resolveConfig(opts, name)
		if err != nil {
			return res, err
		}
		if !slices.Contains(rows, o) {
			rows = append(rows, o)
		}
	}
	res.Warnings = append(res.Warnings, missingDefaults(rows, opts)...)
	names := func(want []string) bool {
		return slices.ContainsFunc(rows, func(o *gosmo.ConfigurationOption) bool { return slices.Contains(want, o.Name) })
	}
	fields, warnings := configContext(ctx, cat, names(cpuContextOptions), names(memoryContextOptions))
	res.Fields = fields
	res.Warnings = append(res.Warnings, warnings...)
	res.Sets = []output.Set{configSet(rows)}
	return res, nil
}

// configContext is server config get's record: the CPU layout when cpu, the
// memory figures when memory. A figure the login may not read is null, and
// all of them join one warning naming the permission; any other failed read
// is its own warning, as in server info.
func configContext(ctx context.Context, cat Catalog, cpu, memory bool) (fields []output.Field, warnings []string) {
	info := cat.ServerInfo()
	var denied []string
	unread := func(err error, keys ...string) {
		if gosmo.IsPermissionDenied(err) {
			denied = append(denied, keys...)
			return
		}
		warnings = append(warnings, fmt.Sprintf("%s not read: %v", keysWere(keys), err))
	}
	if cpu {
		cpus := output.Field{Key: "logicalCpus", Null: true}
		numa := output.Field{Key: "numaNodes", Null: true}
		perNode := output.Field{Key: "logicalCpusPerNumaNode", Null: true}
		var nCPU, nNUMA int
		if info == nil || info.SysInfoUnavailable {
			denied = append(denied, cpus.Key)
		} else {
			nCPU = info.LogicalCPUCount
			cpus = output.Field{Key: cpus.Key, Value: strconv.Itoa(nCPU)}
		}
		if p, err := cat.ProcessorInfo(ctx); err != nil {
			unread(err, numa.Key)
		} else {
			nNUMA = p.NUMANodeCount
			numa = output.Field{Key: numa.Key, Value: strconv.Itoa(nNUMA)}
		}
		if nCPU > 0 && nNUMA > 0 {
			v := strconv.Itoa(nCPU / nNUMA)
			if nCPU%nNUMA != 0 {
				v = mbf(float64(nCPU) / float64(nNUMA))
			}
			perNode = output.Field{Key: perNode.Key, Value: v}
		}
		fields = append(fields, cpus, numa, perNode)
	}
	if memory {
		physical := output.Field{Key: "physicalMemoryMb", Null: true}
		committed := output.Field{Key: "committedMemoryMb", Null: true}
		if info == nil || info.SysInfoUnavailable {
			denied = append(denied, physical.Key)
		} else {
			physical = output.Field{Key: physical.Key, Value: strconv.FormatInt(info.PhysicalMemoryMB, 10)}
		}
		if s, err := cat.MemoryStats(ctx); err != nil {
			unread(err, committed.Key)
		} else {
			committed = output.Field{Key: committed.Key, Value: strconv.FormatInt(s.TotalServerMemoryMB, 10)}
		}
		fields = append(fields, physical, committed)
	}
	if len(denied) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s not read: the sys.dm_os_* views need %s.", keysWere(denied), serverStateHint))
	}
	return fields, warnings
}
