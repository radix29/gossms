package tui

import (
	"fmt"
	"math/bits"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// resource_governor_props_affinity.go is the affinity half of a pool page in
// Resource Governor Properties: an Automatic box and a grid of the instance's
// schedulers (a resource pool) or CPUs (an external pool), one tick each.
//
// # Processor group 0 only
//
// The catalog stores affinity as a mask per processor group and the DDL takes
// ids; in group 0 bit n is id n, beyond it the ids depend on each group's actual
// size (gosmo's scripter_resource_governor.go). So only group 0's schedulers are
// offered, and a pool whose stored affinity reaches past them (another group, or
// an id the instance no longer lists) is shown and not edited: ticking what the
// grid can show would silently drop the rest on Apply.
//
// # No NUMA node picker
//
// AFFINITY NUMANODE is stored as the node's schedulers, so it would read back as
// scheduler ticks anyway. The grid's NUMA node column is how a node is chosen:
// tick its schedulers.

// rgAffinity is a pool's affinity as the page edits it: Automatic, or the ids
// it may run on, sorted.
type rgAffinity struct {
	auto bool
	ids  []int
}

func (a rgAffinity) equal(b rgAffinity) bool { return a.auto == b.auto && slices.Equal(a.ids, b.ids) }

// option is the gosmo write for a: the zero PoolAffinity is AUTO.
func (a rgAffinity) option() *gosmo.PoolAffinity {
	if a.auto {
		return &gosmo.PoolAffinity{}
	}
	return &gosmo.PoolAffinity{Schedulers: slices.Clone(a.ids)}
}

// text is a as the pool grid's Affinity column shows it: "Auto", or the ids
// as ranges, "0-3, 6".
func (a rgAffinity) text() string {
	if a.auto {
		return "Auto"
	}
	var parts []string
	for i := 0; i < len(a.ids); {
		j := i
		for j+1 < len(a.ids) && a.ids[j+1] == a.ids[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(a.ids[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", a.ids[i], a.ids[j]))
		}
		i = j + 1
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, ", ")
}

// rgAffinityFromMasks reads a pool's stored masks as ids, ok false when one
// reaches past processor group 0. No mask at all is Automatic.
func rgAffinityFromMasks(groups []int, masks []int64) (a rgAffinity, ok bool) {
	for i, g := range groups {
		if masks[i] == 0 {
			continue
		}
		if g != 0 {
			return rgAffinity{}, false
		}
		for m := uint64(masks[i]); m != 0; m &= m - 1 {
			a.ids = append(a.ids, bits.TrailingZeros64(m))
		}
	}
	a.auto = len(a.ids) == 0
	slices.Sort(a.ids)
	return a, true
}

// rgAffinityTarget is one row of the affinity grid: an id the pool kind's
// AFFINITY takes, and where it is.
type rgAffinityTarget struct {
	id     int
	node   int
	online bool
}

// rgSchedulerTargets are a resource pool's: the schedulers of group 0.
func rgSchedulerTargets(scheds []gosmo.Scheduler) []rgAffinityTarget {
	var out []rgAffinityTarget
	for _, s := range scheds {
		if s.ProcessorGroup == 0 {
			out = append(out, rgAffinityTarget{s.ID, s.NUMANode, s.IsOnline})
		}
	}
	return out
}

// rgCPUTargets are an external pool's: group 0's CPUs, each once (a CPU hosts at
// most one visible scheduler, but nothing promises it).
func rgCPUTargets(scheds []gosmo.Scheduler) []rgAffinityTarget {
	var out []rgAffinityTarget
	for _, s := range scheds {
		if s.ProcessorGroup == 0 && !slices.ContainsFunc(out, func(t rgAffinityTarget) bool { return t.id == s.CPUID }) {
			out = append(out, rgAffinityTarget{s.CPUID, s.NUMANode, s.IsOnline})
		}
	}
	slices.SortFunc(out, func(a, b rgAffinityTarget) int { return a.id - b.id })
	return out
}

// rgAffinityEditor is the Automatic box and the grid, shared by every pool on the
// page as the limit rows are: show loads the selected pool into them, commit folds
// them back.
type rgAffinityEditor struct {
	word    string // "scheduler" or "CPU", as a label says it
	targets []rgAffinityTarget
	err     error // the scheduler read's, when it was refused
	auto    *propsheet.CheckRow
	grid    *propsheet.ToggleGridRow
}

// header is word as the grid's column heading says it.
func newRGAffinityEditor(word, header string, targets []rgAffinityTarget, err error) *rgAffinityEditor {
	a := &rgAffinityEditor{
		word:    word,
		targets: targets,
		err:     err,
		auto:    propsheet.Check("Automatic "+word+" affinity", true),
		grid: propsheet.NewToggleGrid([]string{header, "Use", "NUMA node"}, []int{1},
			min(len(targets)+3, 10)),
	}
	// Ticking one is choosing ids, so Automatic goes off with it.
	a.grid.OnToggle = func(_, _ int, on bool) {
		if on && a.auto.Checked() {
			a.auto.Edit(false)
		}
	}
	return a
}

// rows are the editor's rows, under their own section.
func (a *rgAffinityEditor) rows() []propsheet.Row {
	rows := []propsheet.Row{propsheet.Section("Affinity"), a.auto, a.grid}
	if a.err != nil {
		rows = append(rows, propsheet.Note("Affinity cannot be edited here: listing the "+a.word+"s requires "+viewServerStateAdvice+"."))
	}
	return rows
}

// fix marks a pool whose stored affinity the grid cannot show; see the file
// comment.
func (a *rgAffinityEditor) fix(e *rgIntEdit) {
	if e.affFixed || e.origAff.auto || a.err != nil {
		return
	}
	for _, id := range e.origAff.ids {
		if !slices.ContainsFunc(a.targets, func(t rgAffinityTarget) bool { return t.id == id }) {
			e.affFixed = true
			return
		}
	}
}

func (a *rgAffinityEditor) editable(e *rgIntEdit) bool {
	return e != nil && !e.locked && !e.affFixed && a.err == nil
}

// show loads e (nil: none selected) into the box and the grid.
func (a *rgAffinityEditor) show(e *rgIntEdit) {
	var cur rgAffinity
	if e != nil {
		cur = e.curAff
	}
	a.auto.SetChecked(e == nil || cur.auto)
	text := make([][]string, len(a.targets))
	values := make([][]bool, len(a.targets))
	for i, t := range a.targets {
		id := strconv.Itoa(t.id)
		if !t.online {
			id += " (offline)"
		}
		text[i] = []string{id, strconv.Itoa(t.node)}
		values[i] = []bool{!cur.auto && slices.Contains(cur.ids, t.id)}
	}
	a.grid.SetRows(text, values)
	ro := !a.editable(e)
	a.auto.SetReadOnly(ro)
	a.grid.SetReadOnly(ro)
}

// commit folds the box and the grid into e. Automatic wins over ticks left
// behind it.
func (a *rgAffinityEditor) commit(e *rgIntEdit) {
	if !a.editable(e) {
		return
	}
	if a.auto.Checked() {
		e.curAff = rgAffinity{auto: true}
		return
	}
	ids := []int{}
	for i, v := range a.grid.Values() {
		if v[0] {
			ids = append(ids, a.targets[i].id)
		}
	}
	e.curAff = rgAffinity{ids: ids}
}
