package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

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

// affinityMaskText renders an affinity mask for the read-only rendering the
// page falls back to when the per-CPU grid cannot be built. 0 is how SQL
// Server stores "automatic", and printing it as a number reads as a mask that
// pins the instance to no CPU at all.
func affinityMaskText(mask int64) string {
	if mask == 0 {
		return "Automatic"
	}
	return "0x" + strconv.FormatInt(mask, 16)
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

func pageServerProcessors(sc *db.ServerConn) propPage {
	return propPage{
		title: "Processors",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			info := sc.Server.Info()
			// Not fatal — see server_props_general.go. sys.dm_os_sys_info is
			// refused for a login without VIEW SERVER PERFORMANCE STATE, which
			// leaves the three counts unreadable and the affinity grid empty
			// (LogicalCPUCount is zero for the same reason), but the Threads
			// and Parallelism rows below are still editable.
			proc, procErr := sc.Server.ProcessorInfoContext(ctx)
			if procErr != nil {
				proc = &gosmo.ProcessorInfo{}
			}
			procInt := func(v int) string {
				if procErr != nil {
					return unreadableValue
				}
				return strconv.Itoa(v)
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

			// The grid is sized from sys.dm_os_sys_info's CPU count, so a login
			// refused that DMV gets zero rows — and bitsToAffinity over zero
			// rows is 0, which apply would then write over a non-zero
			// 'affinity mask' as if the user had cleared it. The masks
			// themselves come from sp_configure and are still readable, so the
			// fallback shows them and takes both writes out of apply entirely,
			// rather than leaving controls that silently mean "clear it".
			affinityEditable := procErr == nil && cpuCount > 0
			affinityRows := []propsheet.Row{autoAffinity, autoIOAffinity, affinityGrid}
			if !affinityEditable {
				affinityRows = []propsheet.Row{
					propsheet.Static("Processor affinity mask", affinityMaskText(affMask)),
					propsheet.Static("I/O affinity mask", affinityMaskText(ioMask)),
					propsheet.Note("Processor affinity cannot be edited here: listing the instance's CPUs requires " + viewServerStateAdvice + "."),
				}
			}

			rows := []propsheet.Row{
				propsheet.Section("Processor information"),
				propsheet.Static("Processors", procInt(proc.CPUCount)),
				propsheet.Static("NUMA nodes", procInt(proc.NUMANodeCount)),
				propsheet.Static("Hyperthread ratio", procInt(proc.HyperthreadRatio)),
				propsheet.Section("Processor affinity"),
			}
			rows = append(rows, affinityRows...)
			rows = append(rows, []propsheet.Row{
				propsheet.Section("Threads"),
				cfgInt("max worker threads", "Maximum worker threads", ""),
				cfgBool("priority boost", "Boost SQL Server priority"),
				cfgBool("lightweight pooling", "Use Windows fibers"),
				propsheet.Section("Parallelism"),
				cfgInt("max degree of parallelism", "Max degree of parallelism", ""),
				cfgInt("cost threshold for parallelism", "Cost threshold for parallelism", ""),
			}...)
			if procErr != nil {
				rows = append(rows, deniedReadNote(viewServerStateAdvice))
			}
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
				if err != nil {
					return err
				}
				if affinityEditable {
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
