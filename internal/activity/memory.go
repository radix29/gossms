package activity

import (
	"context"
	"database/sql"
)

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

const clerkQuery = `
SELECT type, SUM(pages_kb) / 1024.0
FROM sys.dm_os_memory_clerks
GROUP BY type`

// collectMemory reads memory clerks grouped for the composition bar, in display
// order; an empty group is absent.
func collectMemory(ctx context.Context, db *sql.DB) ([]MemoryComponent, error) {
	rows, err := db.QueryContext(ctx, clerkQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	totals := map[string]float64{}
	for rows.Next() {
		var clerk string
		var mb float64
		if err := rows.Scan(&clerk, &mb); err != nil {
			return nil, err
		}
		group, ok := clerkGroups[clerk]
		if !ok {
			group = memOther
		}
		totals[group] += mb
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]MemoryComponent, 0, len(memoryOrder))
	for _, name := range memoryOrder {
		if mb := totals[name]; mb > 0 {
			out = append(out, MemoryComponent{Name: name, MB: mb})
		}
	}
	return out, nil
}
