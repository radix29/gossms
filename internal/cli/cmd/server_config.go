package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// server config's own options.
const (
	optNonDefault = "non-default"
	optAdvanced   = "advanced"
	optAcceptRisk = "accept-risk"
	optOverride   = "override"
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
			Summary: "List, show, change and reset the sp_configure settings",
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
		{
			Path:    []string{"server", "config", "set"},
			Summary: "Change sp_configure settings in one batch; before/after and restarts",
			Description: "Takes name value pairs; a name is as for server config get, and a negative value " +
				"follows --. " + configWriteText + " A value is a whole number in the setting's own unit; an " +
				"option whose name ends in (MB) also takes an MB, GB or TB suffix, one ending in (KB) a KB or MB " +
				"suffix, ignoring case, as long as the result is whole in that unit (1.5GB = 1536). A value " +
				"outside the option's min and max (its default aside: 0 for the self-configuring ones), a unit " +
				"on another option or a setting named twice is exit 2, before anything runs.",
			Args: []spec.Arg{
				{Name: "name", Help: "A setting's alias or sys.configurations name", Required: true},
				{Name: "value", Help: "Its new value; further name value pairs may follow", Required: true, Repeat: true},
			},
			Options: configWriteOptions(),
			Class:   spec.ClassAdmin,
			Run:     d.serverConfigSet,
			Examples: []string{
				"gossms server config set maxdop 8 -p dev --allow admin --dry-run",
				"gossms server config set max-memory 56GB min-memory 8GB -p dev --allow admin",
			},
		},
		{
			Path:    []string{"server", "config", "reset"},
			Summary: "Set sp_configure settings back to their engine defaults",
			Description: "server config set to each setting's default (as server config list shows it, the " +
				"engine's own, not Setup's). A setting gosmo has no default for is exit 11; set it instead. " +
				configWriteText,
			Args:    []spec.Arg{{Name: "name", Help: "A setting's alias or sys.configurations name", Required: true, Repeat: true}},
			Options: configWriteOptions(),
			Class:   spec.ClassAdmin,
			Run:     d.serverConfigReset,
			Examples: []string{
				"gossms server config reset maxdop cost-threshold -p dev --allow admin --dry-run",
			},
		},
	}
}

// configWriteText is what server config set and reset share in their
// descriptions.
const configWriteText = "Needs --allow admin. Every change runs in one gosmo ApplyConfiguration batch: show advanced " +
	"options turned on only if an advanced option needs it and put back after, each sp_configure, one RECONFIGURE " +
	"(WITH OVERRIDE under --override, for values the plain form refuses). --dry-run prints that batch and a " +
	"planned_change set (current, new, and enables_show_advanced: whether its first step fires here) without " +
	"running it. Otherwise the change set holds before, requested, after_configured and after_running, read back " +
	"from sys.configurations, and restart_required (the running value is not the configured one; min server " +
	"memory 0, which runs as 16, aside). A batch the server failed exits 5 with the change set still read back: " +
	"an sp_configure it refused leaves its row unchanged, and a refused RECONFIGURE leaves every change configured " +
	"but not running, pending until the next RECONFIGURE (the hint says so). " +
	"Blocked (exit 7) unless --accept-risk: max or min server memory above physical memory, max below min " +
	"server memory, max degree of parallelism above the logical CPUs, and any of these when the login cannot read " +
	"the figure (VIEW SERVER STATE); a value that is the option's default is never risky. Warnings: a new max " +
	"server memory below committed memory, and a value that waits for a restart."

// configWriteOptions is server config set's and reset's options.
func configWriteOptions() []spec.Option {
	return slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
		{Name: optAcceptRisk, Type: spec.TypeBool,
			Help: "Apply a value gossms blocks as risky (memory above physical, max below min, MAXDOP above the CPUs)"},
		{Name: optOverride, Type: spec.TypeBool,
			Help: "RECONFIGURE WITH OVERRIDE, for values SQL Server's plain RECONFIGURE refuses"},
	})
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

// --- server config set / reset ----------------------------------------------

// configChange is one setting server config set or reset changes: its row in
// the first read, and the value asked for in its native unit.
type configChange struct {
	opt   *gosmo.ConfigurationOption
	value int64
}

// configUnits is the unit suffixes an option whose name carries a unit takes,
// as multiples of that unit (D10); nil for any other option.
func configUnits(name string) map[string]int64 {
	switch {
	case strings.HasSuffix(name, "(MB)"):
		return map[string]int64{"MB": 1, "GB": 1 << 10, "TB": 1 << 20}
	case strings.HasSuffix(name, "(KB)"):
		return map[string]int64{"KB": 1, "MB": 1 << 10}
	}
	return nil
}

// parseConfigValue is s as o's value in its native unit: a whole number, or,
// for an option whose name carries a unit, a decimal with a unit suffix whose
// result is whole. The range check is the caller's.
func parseConfigValue(o *gosmo.ConfigurationOption, s string) (int64, error) {
	bad := func(why string) error {
		return usageError(fmt.Sprintf("value %q for %s: %s", s, o.Name, why), "")
	}
	num, unit := s, ""
	if i := strings.IndexFunc(s, unicode.IsLetter); i >= 0 {
		num, unit = s[:i], strings.ToUpper(s[i:])
	}
	if unit == "" {
		v, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			return 0, bad("not a whole number")
		}
		return v, nil
	}
	units := configUnits(o.Name)
	factor, ok := units[unit]
	if !ok {
		if units == nil {
			return 0, bad("this setting takes no unit")
		}
		return 0, bad("the unit is one of " + strings.Join(slices.Sorted(maps.Keys(units)), ", "))
	}
	r, ok := new(big.Rat).SetString(num)
	if !ok || !decimalNumber(num) {
		return 0, bad("not a number")
	}
	r.Mul(r, new(big.Rat).SetInt64(factor))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, bad(fmt.Sprintf("not a whole number of %s", nameUnit(o.Name)))
	}
	return r.Num().Int64(), nil
}

// decimalNumber reports whether s is digits with an optional sign and
// decimal part — what big.Rat accepts less its fractions and exponents.
func decimalNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	whole, frac, dot := strings.Cut(s, ".")
	digits := func(t string) bool {
		return t != "" && strings.IndexFunc(t, func(r rune) bool { return r < '0' || r > '9' }) < 0
	}
	return digits(whole) && (!dot || digits(frac))
}

// nameUnit is the unit an option's name ends in, "MB" for "(MB)".
func nameUnit(name string) string {
	i := strings.LastIndexByte(name, '(')
	return strings.TrimSuffix(name[i+1:], ")")
}

// inRange reports whether v is a value o accepts: within its min and max, or
// its default — a self-configuring option's 0 lies below the minimum
// sys.configurations reports and sp_configure takes it all the same.
func inRange(o *gosmo.ConfigurationOption, v int64) bool {
	return v >= o.Minimum && v <= o.Maximum || o.HasDefault && v == o.Default
}

// configChanges is the changes args ask for, checked against opts: name
// value pairs for set, names for reset (each to its default). Every error is
// one before anything runs.
func configChanges(opts []*gosmo.ConfigurationOption, args []string, reset bool) ([]configChange, error) {
	var changes []configChange
	named := func(o *gosmo.ConfigurationOption) bool {
		return slices.ContainsFunc(changes, func(c configChange) bool { return c.opt == o })
	}
	if reset {
		for _, name := range args {
			o, err := resolveConfig(opts, name)
			if err != nil {
				return nil, err
			}
			if !o.HasDefault {
				return nil, output.NewError(output.CodeUnsupported,
					fmt.Sprintf("gosmo has no default for %s on this server", o.Name),
					"Set it to the value you want with `gossms server config set` instead.")
			}
			if !named(o) {
				changes = append(changes, configChange{o, o.Default})
			}
		}
		return changes, nil
	}
	for i := 0; i+1 < len(args); i += 2 {
		o, err := resolveConfig(opts, args[i])
		if err != nil {
			return nil, err
		}
		if named(o) {
			return nil, usageError(fmt.Sprintf("%s is set twice", o.Name), "Name each setting once.")
		}
		v, err := parseConfigValue(o, args[i+1])
		if err != nil {
			return nil, err
		}
		if !inRange(o, v) {
			return nil, usageError(fmt.Sprintf("value %d for %s is outside its range %d to %d", v, o.Name, o.Minimum, o.Maximum), "")
		}
		changes = append(changes, configChange{o, v})
	}
	return changes, nil
}

func (d Deps) serverConfigSet(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	if len(inv.Args)%2 != 0 {
		return nil, usageError(fmt.Sprintf("%q has no value", inv.Args[len(inv.Args)-1]),
			"Give name value pairs: gossms server config set maxdop 8 cost-threshold 50.")
	}
	return d.applyConfig(ctx, inv, false)
}

func (d Deps) serverConfigReset(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	return d.applyConfig(ctx, inv, true)
}

// applyConfig is server config set and reset (P5-3): one read, every check,
// the one batch, and a second read for what the server now has.
func (d Deps) applyConfig(ctx context.Context, inv *spec.Invocation, reset bool) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	opts, err := cat.Configurations(ctx)
	if err != nil {
		return res, readError(err)
	}
	changes, err := configChanges(opts, inv.Args, reset)
	if err != nil {
		return res, err
	}
	if risks := riskyValues(cat.ServerInfo(), opts, changes); len(risks) > 0 && !inv.Values[optAcceptRisk].(bool) {
		return res, output.NewError(output.CodeBlocked, strings.Join(risks, " "),
			"Choose another value, or re-run with --accept-risk if it is intended (it lifts only gossms's own checks).")
	}
	res.Warnings = append(res.Warnings, committedWarning(ctx, cat, changes)...)

	batch := make([]gosmo.ConfigChange, len(changes))
	for i, c := range changes {
		batch[i] = gosmo.ConfigChange{Name: c.opt.Name, Value: c.value}
	}
	apply := func(ctx context.Context) error {
		return w.ApplyConfiguration(ctx, batch, gosmo.ConfigApplyOptions{Override: inv.Values[optOverride].(bool)})
	}
	if dryRun(inv) {
		res.Sets = []output.Set{plannedSet(opts, changes)}
		for _, c := range changes {
			if !c.opt.IsDynamic && c.value != c.opt.ValueInUse {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not dynamic: it runs %d only after the SQL Server service restarts.",
					c.opt.Name, c.value))
			}
		}
		return res, applyWrite(ctx, inv, res, apply)
	}

	// A failed batch may still have installed some changes (it has no
	// TRY/CATCH), so the second read runs either way and the change set shows
	// what landed.
	werr := applyWrite(ctx, inv, res, apply)
	after, err := cat.Configurations(ctx)
	if err != nil {
		if werr != nil {
			return res, werr
		}
		e := readError(err)
		e.Message = "the change ran, but sys.configurations could not be read back: " + e.Message
		return res, e
	}
	set, warnings := changeSet(after, changes)
	res.Sets, res.Warnings = []output.Set{set}, append(res.Warnings, warnings...)
	if e, ok := errors.AsType[*output.Error](werr); ok && pending(after, changes) {
		// A refused RECONFIGURE (a value outside the recommended range)
		// leaves every sp_configure of the batch configured but not
		// running, found live on 17.
		e.Hint = "The changes are configured but not running (the change set): the next RECONFIGURE installs them. " +
			"Re-run with --override to install them, or set them back to their before values."
	}
	return res, werr
}

// pending reports whether a change's option is configured but not running
// although it is dynamic: RECONFIGURE did not install it.
func pending(after []*gosmo.ConfigurationOption, changes []configChange) bool {
	return slices.ContainsFunc(after, func(o *gosmo.ConfigurationOption) bool {
		return o.IsDynamic && restartRequired(o) &&
			slices.ContainsFunc(changes, func(c configChange) bool { return c.opt.Name == o.Name })
	})
}

// riskyValues is D10's blocks for changes: what a mistyped unit or a value
// for another server would most likely do. A value that is the option's
// default is never one, and a figure the login cannot read blocks (fail
// closed). Values the same call also sets count for the max/min comparison.
func riskyValues(info *gosmo.ServerInfo, opts []*gosmo.ConfigurationOption, changes []configChange) []string {
	known := info != nil && !info.SysInfoUnavailable
	unknown := func(what string) string {
		return fmt.Sprintf("%s is unknown, so the value cannot be checked: reading it needs %s.", what, serverStateHint)
	}
	var risks []string
	newValue := func(name string) (int64, bool, bool) {
		for _, c := range changes {
			if c.opt.Name == name {
				return c.value, true, c.opt.HasDefault && c.value == c.opt.Default
			}
		}
		for _, o := range opts {
			if o.Name == name {
				return o.Value, false, false
			}
		}
		return 0, false, false
	}
	for _, name := range []string{maxServerMemory, minServerMemory} {
		v, changed, isDefault := newValue(name)
		switch {
		case !changed || isDefault:
		case !known:
			risks = append(risks, unknown("physical memory"))
		case v > info.PhysicalMemoryMB:
			risks = append(risks, fmt.Sprintf("%s (%d MB) is above physical memory (%d MB).", strings.TrimSuffix(name, " (MB)"), v, info.PhysicalMemoryMB))
		}
	}
	maxV, maxChanged, _ := newValue(maxServerMemory)
	minV, minChanged, _ := newValue(minServerMemory)
	if (maxChanged || minChanged) && maxV < minV {
		risks = append(risks, fmt.Sprintf("max server memory (%d MB) is below min server memory (%d MB).", maxV, minV))
	}
	if v, changed, isDefault := newValue("max degree of parallelism"); changed && !isDefault {
		switch {
		case !known:
			risks = append(risks, unknown("the logical CPU count"))
		case v > int64(info.LogicalCPUCount):
			risks = append(risks, fmt.Sprintf("max degree of parallelism (%d) is above the logical CPUs (%d).", v, info.LogicalCPUCount))
		}
	}
	return slices.Compact(risks)
}

// committedWarning is the warning for a new max server memory below the
// memory SQL Server has committed (§10.4): a fact, not a block.
func committedWarning(ctx context.Context, cat Catalog, changes []configChange) []string {
	i := slices.IndexFunc(changes, func(c configChange) bool { return c.opt.Name == maxServerMemory })
	if i < 0 {
		return nil
	}
	s, err := cat.MemoryStats(ctx)
	switch {
	case err != nil && gosmo.IsPermissionDenied(err):
		return []string{fmt.Sprintf("committed memory not read, so the new max server memory was not compared with it: it needs %s.", serverStateHint)}
	case err != nil:
		return []string{fmt.Sprintf("committed memory not read: %v", err)}
	case changes[i].value < s.TotalServerMemoryMB:
		return []string{fmt.Sprintf("New value is below currently committed memory (%d MB). SQL Server will release memory after RECONFIGURE.",
			s.TotalServerMemoryMB)}
	}
	return nil
}

// plannedSet is a dry run's planned_change set. enables_show_advanced is
// whether the batch's first step fires here: the option is advanced and show
// advanced options does not run 1, as the batch's own test reads it.
func plannedSet(opts []*gosmo.ConfigurationOption, changes []configChange) output.Set {
	showAdvancedOff := slices.ContainsFunc(opts, func(o *gosmo.ConfigurationOption) bool {
		return strings.EqualFold(o.Name, "show advanced options") && o.ValueInUse == 0
	})
	set := output.Set{Name: "planned_change", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(35)"},
		{Name: "current", Type: "bigint"}, {Name: "new", Type: "bigint"},
		{Name: "dynamic", Type: "bit"}, {Name: "advanced", Type: "bit"}, {Name: "enables_show_advanced", Type: "bit"},
	}}
	num := func(v int64) *string { return ptr(strconv.FormatInt(v, 10)) }
	for _, c := range changes {
		set.Rows = append(set.Rows, []*string{ptr(c.opt.Name), num(c.opt.Value), num(c.value),
			bitCell(c.opt.IsDynamic), bitCell(c.opt.IsAdvanced), bitCell(c.opt.IsAdvanced && showAdvancedOff)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// restartRequired reports whether o waits for a restart: its running value
// is not its configured one. min server memory configured 0 runs as 16 on
// every version (found live in 5.c) and needs none.
func restartRequired(o *gosmo.ConfigurationOption) bool {
	return o.Value != o.ValueInUse && !(o.Name == minServerMemory && o.Value == 0)
}

// changeSet is a real run's change set, from the second read, and its
// warnings: a value the server did not take, and one waiting for a restart.
func changeSet(after []*gosmo.ConfigurationOption, changes []configChange) (output.Set, []string) {
	set := output.Set{Name: "change", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(35)"},
		{Name: "before", Type: "bigint"}, {Name: "requested", Type: "bigint"},
		{Name: "after_configured", Type: "bigint", Nullable: true}, {Name: "after_running", Type: "bigint", Nullable: true},
		{Name: "restart_required", Type: "bit", Nullable: true},
	}}
	num := func(v int64) *string { return ptr(strconv.FormatInt(v, 10)) }
	var warnings []string
	for _, c := range changes {
		row := []*string{ptr(c.opt.Name), num(c.opt.Value), num(c.value), nil, nil, nil}
		i := slices.IndexFunc(after, func(o *gosmo.ConfigurationOption) bool { return o.Name == c.opt.Name })
		if i < 0 {
			warnings = append(warnings, fmt.Sprintf("%s is gone from sys.configurations after the change.", c.opt.Name))
			set.Rows = append(set.Rows, row)
			continue
		}
		o := after[i]
		row[3], row[4], row[5] = num(o.Value), num(o.ValueInUse), bitCell(restartRequired(o))
		switch {
		case o.Value != c.value:
			warnings = append(warnings, fmt.Sprintf("%s is configured %d, not the %d requested.", o.Name, o.Value, c.value))
		case restartRequired(o) && o.IsDynamic:
			warnings = append(warnings, fmt.Sprintf("%s is configured %d but runs %d: RECONFIGURE did not install it, and the "+
				"next RECONFIGURE on this server, anyone's, will.", o.Name, o.Value, o.ValueInUse))
		case restartRequired(o):
			warnings = append(warnings, fmt.Sprintf("%s is configured %d but runs %d until the SQL Server service restarts.",
				o.Name, o.Value, o.ValueInUse))
		}
		set.Rows = append(set.Rows, row)
	}
	set.TotalRowCount = len(set.Rows)
	return set, warnings
}
