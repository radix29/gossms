package activity

import (
	"context"
	"database/sql"
)

// MemoryComponent is one slice of the memory-composition bar, in megabytes.
type MemoryComponent struct {
	Name string
	MB   float64
}

// Memory clerk groups, in the order they are stacked. The clerk list runs
// to dozens of types, most of them tiny; these are the ones worth naming,
// and everything else lands in "Other".
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

// clerkGroups maps a clerk type to its display group. A clerk not listed
// here is grouped as Other rather than dropped, so the components always
// add up to the server's total memory.
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

// collectMemory reads the memory clerks grouped for the composition bar.
// The result is already in display order, so a group with no clerks is
// simply absent rather than a zero-height slice.
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
