package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// pageDatabaseResourceGovernance is the Azure-only Resource Governance page:
// what the resource governor lets this database have, and what it was using
// when the server last measured.
//
// It exists because the percentages an Azure database reports are percentages
// *of something*, and that something is invisible everywhere else in the UI —
// "CPU 0.6%" against an unnamed limit says nothing, while "0.6% of 4 vCores"
// does. sys.dm_db_resource_stats supplies the reading and
// sys.dm_user_db_resource_governance the scale, so the page pairs them.
//
// Read-only throughout: every value here changes by resizing the instance or
// the database, which is a control-plane operation and not something a T-SQL
// statement from this dialog could do. The page is offered only on an Azure
// engine edition, where both views exist — see databasePropPages.
func pageDatabaseResourceGovernance(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Resource Governance",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			// Neither view is readable by an ordinary login, and the two
			// fail *differently*: sys.dm_db_resource_stats answers Msg 262,
			// "VIEW DATABASE PERFORMANCE STATE permission denied", while
			// sys.dm_user_db_resource_governance simply returns no rows, which
			// gosmo reports as ErrNotFound. Neither is a reason to fail the
			// page — a login that cannot see the numbers should be told why,
			// not handed an error where a property sheet belongs.
			g, govErr := d.ResourceGovernanceContext(ctx)
			latest, statsErr := d.LatestResourceStatsContext(ctx)
			if g == nil && latest == nil {
				return propsheet.NewForm(
					propsheet.Section("Resource governance"),
					propsheet.Note(resourceGovernanceDeniedNote(govErr, statsErr)),
				), nil, nil
			}

			rows := []propsheet.Row{propsheet.Section("Current usage")}
			if latest != nil {
				rows = append(rows,
					propsheet.Static("Measured at", latest.EndTime.Format("2006-01-02 15:04:05")),
					propsheet.Static("CPU", governedPercent(latest.AvgCPUPercent)),
					propsheet.Static("Data I/O", governedPercent(latest.AvgDataIOPercent)),
					propsheet.Static("Log write", governedPercent(latest.AvgLogWritePercent)),
					propsheet.Static("Memory", governedPercent(latest.AvgMemoryUsagePercent)),
					propsheet.Static("Workers (peak)", governedPercent(latest.MaxWorkerPercent)),
					propsheet.Static("Sessions (peak)", governedPercent(latest.MaxSessionPercent)),
					propsheet.Static("Storage used", fmt.Sprintf("%s of %s allocated",
						formatMB(float64(latest.UsedStorageMB)), formatMB(float64(latest.AllocatedStorageMB)))),
					propsheet.Section("Instance, over the same window"),
					// The pair that separates "this database is busy" from
					// "this database is on a busy instance", which is the
					// question a per-database CPU figure cannot answer alone.
					propsheet.Static("Instance CPU", governedPercent(latest.AvgInstanceCPUPercent)),
					propsheet.Static("Instance memory", governedPercent(latest.AvgInstanceMemoryPercent)),
					propsheet.Note("Percentages are of this database's governed limits, over the 15-second window ending at the time above."),
				)
			} else {
				rows = append(rows, propsheet.Note(usageUnavailableNote(statsErr)))
			}

			if g == nil {
				return propsheet.NewForm(append(rows,
					propsheet.Section("Database limits"),
					propsheet.Note(limitsUnavailableNote(govErr)))...), nil, nil
			}

			rows = append(rows,
				propsheet.Section("Database limits"),
				propsheet.Static("Service objective", g.SLOName),
				// cpu_limit, not the instance's vCore count: it is the
				// per-database CPU sub-limit, and reads 0 on a Managed
				// Instance, where a database is governed by the instance's
				// limits rather than one of its own. Labelling it "vCores"
				// made "(not set)" look like a failed read.
				propsheet.Static("Database CPU limit", governedCount(int64(g.CPULimit))),
				propsheet.Static("Max degree of parallelism", governedCount(int64(g.MaxDOP))),
				propsheet.Static("Max concurrent sessions", governedCount(int64(g.MaxSessions))),
				propsheet.Static("Max database size", governedMB(g.MaxDBMaxSizeMB)),
				propsheet.Static("Default database size", governedMB(g.DefaultDBMaxSizeMB)),
				propsheet.Static("File growth increment", governedMB(g.DBFileGrowthMB)),
				propsheet.Static("Max log size", governedMB(g.LogSizeMB)),
				propsheet.Static("Max transaction size", governedMB(g.MaxTransactionSize)),
				propsheet.Section("Instance limits"),
				propsheet.Static("Instance CPU cap", governedPercentInt(g.InstanceCapCPU)),
				propsheet.Static("Instance max workers", governedCount(int64(g.InstanceMaxWorkerThreads))),
				propsheet.Static("Instance max log rate", governedRate(g.InstanceMaxLogRate)),
				propsheet.Static("Local volume IOPS", governedCount(int64(g.LocalIOPS))),
				propsheet.Static("Data directory quota", governedMB(int64(g.DataDirectoryQuotaMB))),
				propsheet.Static("Data directory in use", governedMB(int64(g.DataDirectoryUsageMB))),
				propsheet.Static("Limits last updated (UTC)", governedTime(g.LastUpdatedUTC)),
			)
			return propsheet.NewForm(rows...), nil, nil
		},
	}
}

// resourceGovernanceDeniedNote is the whole page when neither view answered.
// It names the permission rather than the error, because "denied" without the
// right to ask for is the least actionable thing a property sheet can say.
func resourceGovernanceDeniedNote(govErr, statsErr error) string {
	note := "Resource governance figures are not available to this login. " +
		"sys.dm_db_resource_stats needs VIEW DATABASE PERFORMANCE STATE on the database, " +
		"and sys.dm_user_db_resource_governance returns no rows without VIEW SERVER PERFORMANCE STATE."
	if govErr != nil {
		note += "\n\nLimits: " + govErr.Error()
	}
	if statsErr != nil {
		note += "\n\nUsage: " + statsErr.Error()
	}
	return note
}

// usageUnavailableNote covers the half of the page sys.dm_db_resource_stats
// feeds. An empty view and a refused one are different facts, and the login
// can act on only one of them.
func usageUnavailableNote(statsErr error) string {
	if statsErr != nil && !errors.Is(statsErr, gosmo.ErrNotFound) {
		return "Current usage could not be read — sys.dm_db_resource_stats needs VIEW DATABASE PERFORMANCE STATE on this database: " + statsErr.Error()
	}
	return "No usage has been recorded for this database yet. sys.dm_db_resource_stats keeps about an hour and holds nothing for a database that has been idle since the instance restarted."
}

// limitsUnavailableNote covers the half sys.dm_user_db_resource_governance
// feeds. That view does not refuse an ordinary login, it returns no rows, so
// ErrNotFound here usually means a permission and not a missing database.
func limitsUnavailableNote(govErr error) string {
	note := "The governed limits are not available to this login: " +
		"sys.dm_user_db_resource_governance returns no rows without VIEW SERVER PERFORMANCE STATE."
	if govErr != nil && !errors.Is(govErr, gosmo.ErrNotFound) {
		note += " " + govErr.Error()
	}
	return note
}

// governedPercent renders one of the stats view's percentages. They arrive as
// decimals with more precision than anyone reads, and a database doing nothing
// legitimately reports 0.
func governedPercent(v float64) string {
	return fmt.Sprintf("%.2f %%", v)
}

// governedPercentInt is governedPercent for a whole-number cap.
func governedPercentInt(v int) string {
	return fmt.Sprintf("%d %%", v)
}

// governedCount renders a plain limit, and says so when the governor reports
// none rather than printing a bare 0 that reads as "no capacity".
func governedCount(v int64) string {
	if v <= 0 {
		return unsetItem
	}
	return core.FormatThousands(v)
}

// governedMB renders a size limit. Zero is a real answer on the "Shared"
// service objective the instance's internal databases run under, where the
// size ceilings genuinely are not set — so it reads as unset, not as zero MB.
func governedMB(mb int64) string {
	if mb <= 0 {
		return unsetItem
	}
	return formatMB(float64(mb))
}

// governedRate renders a bytes-per-second ceiling.
func governedRate(bps int64) string {
	if bps <= 0 {
		return unsetItem
	}
	return formatBytes(bps) + "/sec"
}

// governedTime renders the governor's own last-updated stamp, which is zero
// on a row the view has never refreshed.
func governedTime(t time.Time) string {
	if t.IsZero() {
		return unsetItem
	}
	return t.Format("2006-01-02 15:04:05")
}
