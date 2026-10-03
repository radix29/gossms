package activity

import "context"

// MemoryComponent is one slice of the memory-composition bar, in MB.
type MemoryComponent struct {
	Name string
	MB   float64
}

// Memory clerk groups, in stacking order. Most clerk types are tiny; the rest
// go to "Other".
const (
	memBuffer      = "Buffer"
	memStolen      = "Stolen Buffer"
	memInMemOLTP   = "In-Mem OLTP"
	memPlanSQL     = "Plan (SQL)"
	memPlanObjects = "Plan (Objects)"
	memColumnstore = "Columnstore"
	memQueryGrants = "Query Grants"
	memOther       = "Other"
)

// memoryOrder is the stacking order of the composition bar.
var memoryOrder = []string{
	memBuffer, memStolen, memInMemOLTP, memPlanSQL, memPlanObjects,
	memColumnstore, memQueryGrants, memOther,
}

// clerkGroups maps a clerk type to its display group. Unlisted clerks go to
// Other, so components sum to total memory.
var clerkGroups = map[string]string{
	"MEMORYCLERK_SQLBUFFERPOOL":    memBuffer,
	"MEMORYCLERK_SQLGENERAL":       memStolen,
	"MEMORYCLERK_SQLCLR":           memStolen,
	"MEMORYCLERK_SQLOPTIMIZER":     memStolen,
	"MEMORYCLERK_SOSNODE":          memStolen,
	"MEMORYCLERK_XTP":              memInMemOLTP,
	"CACHESTORE_SQLCP":             memPlanSQL,
	"CACHESTORE_OBJCP":             memPlanObjects,
	"CACHESTORE_PHDR":              memPlanObjects,
	"MEMORYCLERK_SQLQUERYPLAN":     memPlanSQL,
	"MEMORYCLERK_COLUMNSTOREOBJEC": memColumnstore,
	"MEMORYCLERK_SQLQERESERVATION": memQueryGrants,
}

// collectMemory reads memory clerks grouped for the composition bar, in display
// order; an empty group is absent.
func collectMemory(ctx context.Context, src Source) ([]MemoryComponent, error) {
	clerks, err := src.MemoryClerks(ctx)
	if err != nil {
		return nil, err
	}

	totals := map[string]float64{}
	for _, c := range clerks {
		group, ok := clerkGroups[c.Type]
		if !ok {
			group = memOther
		}
		totals[group] += c.MB
	}

	out := make([]MemoryComponent, 0, len(memoryOrder))
	for _, name := range memoryOrder {
		if mb := totals[name]; mb > 0 {
			out = append(out, MemoryComponent{Name: name, MB: mb})
		}
	}
	return out, nil
}
