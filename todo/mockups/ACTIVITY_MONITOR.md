# Activity Monitor Work Order

## Purpose

Add an Activity Monitor feature to the existing Go TUI application.

The Activity Monitor must be implemented as a new panel, using the same panel/navigation model as the existing Explorer detail view and Query Editor panels. It should feel like a first-class panel in the application, not a separate program or temporary overlay.

This document is the implementation prompt for the Go project. The PowerShell mockups and PNG files in this directory are visual references only.

## Visual references

Use the existing mockups as the design target:

- `hi.png` - reference for the History dashboard.
- `sa.png` - reference for the Sample dashboard.
- `hi.ps1` and `sa.ps1` - runnable terminal mockups that demonstrate layout, chart density, labels, and general visual behavior.
- `DASHBOARD_HANDOVER.md` - source handover document for the intended Activity Monitor visual language, chart behavior, and metric groupings.

The Go implementation does not need to copy the PowerShell code. It should recreate the same kind of SQL Server Activity Monitor experience using the Go application's existing UI framework and component patterns.

## Visual goals

The Activity Monitor should look like a dense, dark, terminal-native operational dashboard.

Expected visual behavior:

- Use the existing Go TUI theme, layout system, focus model, navigation conventions, and status conventions.
- Use a dark background with clear section headers.
- Show multiple compact panels at once.
- Prefer useful metric density over decorative empty space.
- Use high-contrast, stable series colors across refreshes.
- Keep labels and legends meaningful; color must not be the only way to understand a chart.
- Avoid warning-marker glyphs in normal dashboard chrome.
- Show the selected SQL Server instance and sample time or current time position clearly.
- Support vertical scrolling when terminal height is limited.
- Recalculate layout when terminal size changes.
- Preserve readability at 120 columns and look best at 150 columns or wider.
- At narrower widths, degrade predictably by shortening labels, clipping cleanly, or wrapping legends when space allows.

## Chart glyphs

The Go application is UTF-8, so chart glyphs may be embedded directly in source or centralized in the existing toolkit glyph/theme layer.

Use these glyphs for dashboard charts and chrome:

```text
Full block:                 █
Lower half block:           ▄
Vertical eighth blocks:     ▁ ▂ ▃ ▄ ▅ ▆ ▇ █
Horizontal eighth blocks:   ▏ ▎ ▍ ▌ ▋ ▊ ▉ █
Legend square:              ■
Dotted vertical grid:       ┆
Grid dot:                   ·
Box drawing:                ─ │ ┌ ┐ └ ┘
```

Use ASCII fallbacks only if the existing application already has a terminal capability or fallback mechanism. Otherwise, the Activity Monitor target is UTF-8 block chart rendering.

## Chart rendering behavior

### Smooth bars

Bars should use partial block glyphs where this improves readability.

Required smoothing behavior:

- Use horizontal eighth blocks (`▏ ▎ ▍ ▌ ▋ ▊ ▉ █`) for fractional horizontal bars.
- Use vertical eighth blocks (`▁ ▂ ▃ ▄ ▅ ▆ ▇ █`) for fractional vertical bars.
- Use full blocks (`█`) for the filled interior of larger bars.
- Use partial blocks to make small values visible instead of snapping them to zero or a full cell.
- Do not overuse partial glyphs where a full block is clearer.

### Stacked history columns

Stacked history charts must appear as continuous columns.

Required behavior:

- Each horizontal position represents one time bucket.
- A bucket with multiple series appears as one vertical stack with color segments.
- The stack must not contain blank holes between segments.
- Partial block glyphs should appear only where they make the top edge smoother.
- The visual total height represents the combined value for the bucket.
- Segment colors communicate which metric contributes to the stack.
- This rule is especially important for the waits and memory panels.

### Axes and grid

Charts should include enough axis context for quick reading without overwhelming the data.

Expected behavior:

- Show Y-axis labels for major values, intermediate ticks, and zero.
- A typical chart should use around five Y-axis levels.
- Use muted grid lines.
- Use `┆` for dotted vertical grid lines where appropriate.
- Use `·` for lightweight grid dots where appropriate.
- The right side of a history axis represents the current or selected time.
- Use meaningful time labels when real timestamps are available.
- If deterministic mock data is used, fixed deterministic time labels are acceptable.

### Legends

Legends should make series colors understandable without consuming too much vertical space.

Expected behavior:

- Use a colored legend square (`■`) followed by the label.
- Keep labels readable where possible.
- Use shorter labels before clipping.
- Wrapping is acceptable when the panel has enough height.
- If clipping is unavoidable, keep the clipped state visually clean.

Preferred abbreviations:

| Full label | Short label |
|---|---|
| Transactions | Trans |
| Compiles | Comp |
| Recompiles | Recomp |

## Reusable chart components

The Activity Monitor should not keep its chart rendering code as panel-specific one-off logic.

Create reusable chart components in the existing `tuikit` layer so the same chart primitives can be reused by other panels later. If the project structure supports subpackages, prefer a dedicated charts package such as `tuikit/charts`. If the current project uses a different package layout, follow the existing convention but keep the chart code clearly separated from Activity Monitor data collection and SQL Server logic.

Required design direction:

- Chart components belong to the TUI/toolkit layer, not the Activity Monitor data layer.
- The chart package must not import SQL Server, database, or Activity Monitor packages.
- Activity Monitor should transform collected SQL Server metrics into generic chart series/view models, then pass those models to the chart components.
- The chart components should use the existing TUI theme, styles, screen abstraction, clipping behavior, and layout conventions.
- The chart components should tolerate narrow widths, short heights, clipped legends, and empty data without panics.

Recommended reusable components:

- Time-series history chart.
- Stacked time-series history chart.
- Vertical bar chart using eighth-block smoothing.
- Horizontal bar chart using horizontal eighth-block smoothing.
- Stacked horizontal bar chart.
- Compact KPI / numeric value box.
- Legend renderer using `■`.
- Axis and grid renderer using muted labels, `┆`, and `·`.
- Shared scale/tick helpers for linear charts.

Recommended package responsibilities:

- Accept generic series data, labels, colors/styles, and scale options.
- Render into a provided rectangle or layout cell.
- Clip cleanly to the provided bounds.
- Provide deterministic rendering for tests and screenshots.
- Keep smoothing and stacked-column behavior consistent across all dashboards.

The first Activity Monitor implementation may only build the chart components it needs, but those components should be shaped as reusable `tuikit` chart primitives from the start.

## Color expectations

Use colors from the existing TUI theme. Do not hard-code a separate dashboard palette unless the project already has a theme extension mechanism.

Recommended semantic color roles:

| Semantic role | Expected use |
|---|---|
| Background | Main dashboard background |
| Muted gray | Borders, grid lines, secondary labels |
| White or light gray | Axis labels, numeric values, important text |
| Cyan | Primary activity series, section emphasis, network/key lookup style metrics |
| Green | Transactions, CPU-related series, read-oriented positive series |
| Yellow or orange | Disk, write, log, compile, checkpoint style metrics |
| Blue | Memory, backup throughput, cache-related series |
| Red or orange-red | Recompiles, pressure, query grants, high-latency or high-risk series |
| Purple | Page life expectancy or alternate cache/memory series |

Colors should remain stable for the same metric across refreshes and across tabs.

## Entry point and panel behavior

Add a new Activity Monitor panel alongside the existing panels, such as:

- Explorer detail view
- Query Editor
- Activity Monitor

The panel should be opened from the appropriate existing navigation/action pattern in the Go application.

When the Activity Monitor panel is opened:

1. Create an in-memory activity monitor session.
2. Create one database connection per Activity Monitor tab.
3. Start the History collector immediately.
4. Start the Sample collector only when the Sample tab is active.
5. Keep the Activity Monitor session alive until the panel is closed.
6. Close the tab-owned database connections when the panel is closed.
7. Discard all collected in-memory data when the panel is closed.

The collected activity data is scoped to the lifetime of the Activity Monitor panel. It must not be saved to disk, restored on restart, or shared with future Activity Monitor sessions.

## Database connections

Each Activity Monitor tab must own and use its own database connection.

Required tab connection model:

- History tab: dedicated connection used by the History collector.
- Sample tab: dedicated connection used by the Sample collector.
- Sessions tab: dedicated connection reserved for the Sessions view.
- Block tab: dedicated connection reserved for the Block view.

Rationale:

- A slow query in one tab should not block collection or refresh work in another tab.
- The History collector must be able to continue collecting data while the user is looking at another tab.
- Manual refresh work in Sessions or Block should not interrupt History collection.

Connections should be opened when the Activity Monitor panel/session is created, or lazily when the tab first needs them, following the existing Go application's connection-management conventions. In either case, the connections are owned by the Activity Monitor panel and must be closed when the panel closes.

## Toolbar and refresh controls

Refresh controls are per tab, not global.

Only these tabs use auto-refresh:

- History
- Sample

These tabs do not use auto-refresh:

- Sessions
- Block

The Activity Monitor toolbar should expose the refresh controls for the active tab. If the active tab is History or Sample, show that tab's auto-refresh controls. If the active tab is Sessions or Block, show a manual Refresh button instead.

### History refresh controls

The History tab has its own refresh settings and timer.

Required controls:

1. Refresh rate selector
   - Supported values:
     - 2 seconds
     - 3 seconds
     - 5 seconds
     - 10 seconds
   - Changing the refresh rate affects the History collector's next scheduled tick.

2. Pause / Continue button
   - Pauses or resumes only the History timer.
   - Pausing History stops History collection.
   - Continuing History resumes History collection using the selected History refresh rate.

The History collector should keep running in the background when the active tab is Sample, Sessions, or Block, unless the History timer is paused.

### Sample refresh controls

The Sample tab has its own refresh settings and timer, independent from History.

Required controls:

1. Refresh rate selector
   - Supported values:
     - 2 seconds
     - 3 seconds
     - 5 seconds
     - 10 seconds
   - Changing the refresh rate affects the Sample collector's next scheduled tick.

2. Pause / Continue button
   - Pauses or resumes only the Sample timer.
   - Pausing Sample must not pause History.
   - Continuing Sample resumes Sample refresh using the selected Sample refresh rate.

The Sample timer should be active only while the Sample tab is active. When the user leaves the Sample tab, stop or suspend Sample auto-refresh work. When the user returns to the Sample tab, resume Sample auto-refresh if it is not paused.

### Sessions and Block refresh controls

Sessions and Block do not use auto-refresh.

Required controls:

- Show a manual Refresh button for the active Sessions or Block tab.
- Pressing Refresh runs one refresh query for that tab using that tab's own database connection.
- Do not start a timer for Sessions or Block.

For the initial implementation, Sessions and Block are placeholder tabs, so their manual Refresh button may simply redraw the placeholder or prepare the command path for future implementation.

## Tabs

The Activity Monitor panel must contain these tabs:

1. History
2. Sample
3. Sessions
4. Block

### History tab

The History tab is the main time-series dashboard.

Use `hi.png` as the visual mockup. The goal is to show recent SQL Server activity over time, using stacked or grouped history charts similar to the mockup.

Required behavior:

- Use the History tab's dedicated database connection.
- Use the History tab's own refresh timer and refresh-rate setting.
- Start collecting History data when the Activity Monitor panel opens.
- Keep collecting History data in the background even when the History tab is not active, unless the History timer is paused.
- Retain data for the last 30 minutes only.
- When data is older than 30 minutes, discard it from memory.
- The history view should render the retained data available at the moment it is drawn.
- If less than 30 minutes of data has been collected, render the available partial history.
- Show recent trends ending at the current sample or selected sample position.
- Make bursts, spikes, sustained load, and cross-metric relationships visible at a glance.

Implementation guidance:

- Use an in-memory rolling buffer or ring buffer per metric/series.
- The maximum retention window is time-based, not count-based.
- The selected History refresh rate determines how often new History samples are collected.
- The chart should adapt to the available panel width and height using the existing Go TUI layout rules.
- Most History values should be collected as cumulative counters and converted into per-second rates by comparing the current sample to the previous sample.

Preferred History layout:

1. Header area with selected instance, host/environment if available, and current or selected sample time.
2. `SQL SERVER ACTIVITY` with three side-by-side panels when width allows:
   - `SQL SERVER ACTIVITY`
   - `Key lookups / Forwarded recs`
   - `BACKUP THROUGHPUT`
3. `SQL SERVER WAITS` with one full-width panel.
4. `SQL SERVER MEMORY` with three side-by-side panels when width allows:
   - `SQL SERVER MEMORY`
   - `CACHE HIT RATIOS / PLE`
   - `PAGES READ / WRITE`
5. `DATABASE IO` with three side-by-side panels when width allows:
   - `DATABASE IO`
   - `LOG FLUSHES`
   - `CHECKPOINTS / LAZY WRITES`

The waits panel should span the full width because wait patterns are easier to read with more horizontal history.

Suggested History dashboard sections and data sources are listed in [History data sources and counters](#history-data-sources-and-counters).

### Sample tab

The Sample tab is the current snapshot dashboard.

Use `sa.png` as the visual mockup. The goal is to show the current state of SQL Server activity at the selected refresh interval.

Required behavior:

- Use the Sample tab's dedicated database connection.
- Use the Sample tab's own refresh timer and refresh-rate setting.
- Display the latest point-in-time activity sample.
- Refresh the Sample tab only while the Sample tab is active and the Sample timer is not paused.
- Do not spend work rendering, querying, or refreshing the Sample tab while another tab is active.
- Pausing Sample must not pause History.
- The Activity Monitor may still collect History data while the Sample tab is inactive.
- Show one coherent current sample, not a mix of unrelated refresh moments.
- Keep the selected instance and sample time visible at the top.

Implementation guidance:

- The Sample tab can use the most recent collected data where possible, but it should query any Sample-specific details only while the Sample tab is active.
- If it needs extra snapshot-specific queries, run them only while the Sample tab is active and the Sample timer is not paused.
- Keep the Sample tab data in memory only for the lifetime of the Activity Monitor panel.
- Use compact labels where space is limited.

Preferred Sample layout:

1. Header area with selected instance, host/environment if available, and sample time.
2. `SQL SERVER ACTIVITY`.
3. `SQL SERVER WAITS`.
4. `SQL SERVER MEMORY`.
5. `DATABASE IO`.

Suggested Sample dashboard sections and data sources are listed in [Sample data sources and counters](#sample-data-sources-and-counters).

### Sessions tab

The Sessions tab is required, but for this work order it should be an empty stub.

Required behavior:

- Use the Sessions tab's dedicated database connection when future implementation needs data.
- The tab must exist and be selectable.
- It should render a clear placeholder such as `Sessions view is not implemented yet.`
- It should have a manual Refresh button in the toolbar.
- It should not start any sessions-specific timer or background data collection yet.

### Block tab

The Block tab is required, but for this work order it should be an empty stub.

Required behavior:

- Use the Block tab's dedicated database connection when future implementation needs data.
- The tab must exist and be selectable.
- It should render a clear placeholder such as `Blocking view is not implemented yet.`
- It should have a manual Refresh button in the toolbar.
- It should not start any blocking-specific timer or background data collection yet.

## Data collection and retention

All Activity Monitor data must be collected in memory.

Requirements:

- Start History collection when the Activity Monitor panel opens.
- Start Sample collection only when the Sample tab is active and its timer is not paused.
- Stop all timers and close all tab-owned database connections when the Activity Monitor panel closes.
- Do not persist collected data.
- Keep only the last 30 minutes of History data.
- Discard History samples older than 30 minutes.
- Continue collecting History data even if the active tab is Sample, Sessions, or Block, unless the History timer is paused.
- Do not refresh the Sample tab while it is inactive.
- Respect the selected History refresh interval for History collection.
- Respect the selected Sample refresh interval for Sample refresh.
- Respect the independent paused/running state for each auto-refresh tab.
- Sessions and Block refresh only through manual user action.

Recommended model:

- One Activity Monitor controller/session owned by the panel.
- One tab state object per tab.
- One database connection per tab.
- One timer/ticker for History.
- One timer/ticker for Sample.
- No timers for Sessions or Block.
- One in-memory History store with timestamped samples and per-series rolling buffers.
- A pruning step after each History collection tick to remove data older than 30 minutes.
- Tab renderers that read from their own tab state and connection-owned results.

## Refresh behavior

### History auto-refresh

When the History timer is running:

1. The History collector wakes up at the selected History refresh rate.
2. It queries SQL Server using the History tab's database connection.
3. It collects the current activity sample.
4. It computes per-second rates and deltas from the previous History sample where needed.
5. It appends the sample to the in-memory History store.
6. It prunes samples older than 30 minutes.
7. It requests a redraw of the Activity Monitor panel.
8. If the active tab is not History, collection still continues, but only the active tab needs to be redrawn.

When the History timer is paused:

- Do not collect new History samples.
- Keep the History data that has already been collected in memory.
- Allow the user to switch tabs and inspect the retained History data.
- Do not affect Sample auto-refresh.

### Sample auto-refresh

When the Sample tab is active and the Sample timer is running:

1. The Sample collector wakes up at the selected Sample refresh rate.
2. It queries SQL Server using the Sample tab's database connection.
3. It stores the latest point-in-time Sample data in memory.
4. It requests a redraw of the Sample tab.

When the Sample tab is inactive:

- Do not run Sample refresh queries.
- Do not update Sample-only state.
- Preserve the latest Sample data until the Activity Monitor panel closes or until the next active refresh replaces it.
- Do not affect History collection.

When the Sample timer is paused:

- Do not run Sample refresh queries even if the Sample tab is active.
- Keep the latest Sample data visible.
- Do not affect History collection.

### Manual refresh for Sessions and Block

When the active tab is Sessions or Block:

- Show a manual Refresh button.
- Pressing the button performs one refresh operation for that tab.
- Do not create recurring timers for these tabs.
- For this work order, the tabs are placeholders, so the refresh operation may only update the placeholder timestamp or no-op safely.

## Metric source rules

Use metrics provided by SQL Server and cross-platform runtime/host information only.

Allowed sources:

- SQL Server dynamic management views exposed through T-SQL.
- SQL Server catalog views and built-in functions exposed through T-SQL.
- SQL Server performance counters exposed through T-SQL, such as `sys.dm_os_performance_counters`, where available.
- Cross-platform Go runtime or operating-system information if the project already has a cross-platform abstraction for it.

Disallowed sources:

- WMI.
- COM.
- Windows Registry.
- Windows Performance Counter APIs outside SQL Server.
- Shelling out to platform-specific commands such as `typeperf`, `wmic`, `powershell`, `Get-Counter`, `top`, `vmstat`, or `iostat`.
- Any Windows-only API that would make the Activity Monitor unavailable on Linux or macOS clients.

If host CPU usage is needed, prefer SQL Server-provided CPU information from DMVs where available. If the Go project already has a cross-platform process/host metrics package, it may be used as a secondary source, but the dashboard must not depend on Windows-only APIs.

## History data sources and counters

The History dashboard should favor lightweight, repeatable queries that return cumulative counters or current gauges. Convert cumulative counters into rates by storing the previous sample and dividing the delta by elapsed seconds.

Recommended History groups:

### Activity / throughput

Purpose: show how busy the server is over time.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `SQL Statistics: Batch Requests/sec`
  - `SQL Statistics: SQL Compilations/sec`
  - `SQL Statistics: SQL Re-Compilations/sec`
  - `General Statistics: User Connections`
  - `General Statistics: Logins/sec`
  - `General Statistics: Logouts/sec`
  - `Databases: Transactions/sec` per database or `_Total`
  - `Access Methods: Full Scans/sec`
  - `Access Methods: Index Searches/sec`
  - `Access Methods: Page Splits/sec`
  - `Access Methods: Forwarded Records/sec`
  - `Access Methods: Workfiles Created/sec`
  - `Access Methods: Worktables Created/sec`
  - backup throughput counters when available for the SQL Server version/configuration
- `sys.dm_exec_requests`
  - count of active requests
  - count by `status`, such as running, runnable, suspended

Display ideas:

- Batch requests/sec as the primary activity series.
- Transactions/sec should generally follow workload shape and usually be lower than batches.
- Compilations/sec and recompilations/sec as smaller stacked or overlay series with occasional spikes.
- Key lookups/index-search style activity and forwarded records in a separate panel.
- Backup MB/sec as a mostly-zero series with isolated spikes.
- Active requests and user connections as gauges or small side counters.

### Waits

Purpose: show what SQL Server is waiting on over time.

Suggested SQL Server sources:

- `sys.dm_os_wait_stats`
  - `wait_time_ms`
  - `signal_wait_time_ms`
  - `waiting_tasks_count`
- `sys.dm_exec_requests`
  - current `wait_type`
  - current `wait_time`
  - `blocking_session_id`

Implementation guidance:

- Treat `sys.dm_os_wait_stats` as cumulative and compute deltas between samples.
- Exclude common benign/background wait types so the chart is useful.
- The History waits panel should be full-width.
- Group waits into practical categories, for example:
  - Network
  - CPU / signal waits
  - Memory waits
  - Disk I/O waits
  - Lock waits
  - Latch waits
  - Log waits
  - Other waits

Display ideas:

- Stacked wait-time-per-second history columns.
- Top current wait categories in a small legend.
- Network, CPU, Memory, Disk, and Other should be the default compact visual categories when space is limited.

### CPU and schedulers

Purpose: show SQL Server CPU pressure using SQL Server-visible indicators.

Suggested SQL Server sources:

- `sys.dm_os_schedulers`
  - `runnable_tasks_count`
  - `current_tasks_count`
  - `active_workers_count`
  - `work_queue_count`
  - filter visible online schedulers where appropriate
- `sys.dm_os_ring_buffers`, if accessible and appropriate
  - recent `SQLProcessUtilization`
  - recent `SystemIdle`
  - recent system CPU signal from `RING_BUFFER_SCHEDULER_MONITOR`

Implementation guidance:

- `sys.dm_os_schedulers` is generally safer for lightweight pressure indicators.
- Ring buffer CPU data may require permissions and XML parsing; use it only if it fits the project's data access style.
- Do not use WMI or platform-specific OS CPU counters.

Display ideas:

- Runnable tasks and workers as CPU pressure indicators.
- SQL process CPU percent only if available from SQL Server DMVs.

### Memory and cache

Purpose: show SQL Server memory pressure and cache health over time.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Buffer Manager: Page life expectancy`
  - `Buffer Manager: Buffer cache hit ratio` and `Buffer cache hit ratio base`
  - `Buffer Manager: Page reads/sec`
  - `Buffer Manager: Page writes/sec`
  - `Buffer Manager: Lazy writes/sec`
  - `Buffer Manager: Checkpoint pages/sec`
  - `Memory Manager: Total Server Memory (KB)`
  - `Memory Manager: Target Server Memory (KB)`
  - `Memory Manager: Memory Grants Pending`
  - `Memory Manager: Memory Grants Outstanding`
  - plan cache, columnstore, and memory component counters where exposed by the target SQL Server version
- `sys.dm_os_process_memory`
  - process physical memory usage
  - low memory indicators where available
- `sys.dm_os_memory_clerks`
  - memory clerk usage grouped into buffer, stolen buffer, free, plan cache, columnstore, query grants, and other where practical

Display ideas:

- Memory composition panel with Buffer / PLE sec, In-Mem OLTP, Stolen Buffer, Free, Plan (SQL), Plan (Objects), Columnstore, Query Grants, and Other when available.
- Cache hit ratios / PLE panel with buffer cache hit ratio, procedure cache hit ratio, and page life expectancy.
- Total vs target server memory.
- Page life expectancy trend.
- Memory grants pending as a pressure indicator.
- Page reads/writes/lazy writes as activity rates.

### Page activity

Purpose: show buffer/page churn.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Buffer Manager: Page lookups/sec`
  - `Buffer Manager: Page reads/sec`
  - `Buffer Manager: Page writes/sec`
  - `Buffer Manager: Readahead pages/sec`
  - `Buffer Manager: Lazy writes/sec`
  - `Buffer Manager: Checkpoint pages/sec`

Display ideas:

- Stacked or grouped page activity rates.
- Separate physical reads/writes from lookups if the visual density allows.
- The History page should include a `PAGES READ / WRITE` panel.

### Database I/O

Purpose: show read/write throughput and latency by database or total.

Suggested SQL Server sources:

- `sys.dm_io_virtual_file_stats(NULL, NULL)`
  - `num_of_reads`
  - `num_of_bytes_read`
  - `io_stall_read_ms`
  - `num_of_writes`
  - `num_of_bytes_written`
  - `io_stall_write_ms`
  - `io_stall_queued_read_ms`
  - `io_stall_queued_write_ms`
- `sys.databases`
  - database names for grouping file stats
- `sys.master_files` or `sys.database_files`
  - file type, data vs log

Implementation guidance:

- Compute read/write bytes per second from deltas.
- Compute approximate read/write latency from stall delta divided by operation-count delta.
- Aggregate by database for the chart, and keep a total series for the main history view.
- The Database IO section may eventually support a file or database selector such as `File: Total`.

Display ideas:

- Read MB/sec and write MB/sec history.
- Average read/write latency side counters.
- `ms/Read` and `ms/Write` in the main Database IO latency panel.
- Top databases by I/O in a compact list if space allows.

### Log activity

Purpose: show transaction log throughput and pressure.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Databases: Log Bytes Flushed/sec`
  - `Databases: Log Flushes/sec`
  - `Databases: Log Flush Waits/sec`
  - `Databases: Log Flush Wait Time`
  - `Databases: Log Growths`
  - `Databases: Percent Log Used`, if available for the SQL Server version
- `sys.dm_io_virtual_file_stats(NULL, NULL)`
  - log file write bytes and write stalls by file type

Display ideas:

- Log bytes flushed/sec.
- Log flushes/sec.
- Log flush waits/sec.
- Log write latency.
- The History page should include a `LOG FLUSHES` panel.

### Checkpoints

Purpose: show checkpoint write pressure.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Buffer Manager: Checkpoint pages/sec`
  - `Buffer Manager: Page writes/sec`
  - `Buffer Manager: Lazy writes/sec`
- `sys.dm_io_virtual_file_stats(NULL, NULL)`
  - write bytes/stalls for data files

Display ideas:

- Checkpoint pages/sec history.
- Lazy writes history.
- Data-file write throughput and latency.
- The History page should include a `CHECKPOINTS / LAZY WRITES` panel.

## Sample data sources and counters

The Sample dashboard should show a richer point-in-time view than History. It can combine current gauges with short deltas from the Sample timer. It should run only while the Sample tab is active.

Recommended Sample groups:

### SQL Server activity summary

Purpose: show the current operational state at a glance.

Suggested SQL Server sources:

- `sys.dm_exec_sessions`
  - total user sessions
  - active user sessions
  - sleeping sessions
  - host/program/login breakdowns if needed
- `sys.dm_exec_requests`
  - active requests
  - running/runnable/suspended requests
  - blocked requests
  - open transaction count where available
  - CPU time, elapsed time, reads, writes, logical reads
- `sys.dm_os_performance_counters`
  - `Batch Requests/sec`
  - `User Connections`
  - `Transactions/sec`
  - `SQL Compilations/sec`
  - `SQL Re-Compilations/sec`
  - access-method counters for key lookups/index-search activity and forwarded records
  - backup throughput counters when available

Display ideas:

- Digital KPI boxes for active requests, user connections, blocked processes, batch requests/sec, and transactions/sec.
- Small bar charts for Batches, Trans, Comp, and Recomp.
- Separate bars for key lookups and forwarded records.
- Backup MB/sec as a compact value or bar.
- Use the shortened labels `Trans`, `Comp`, and `Recomp` so the legend fits.

### Current waits

Purpose: show immediate wait and blocking pressure.

Suggested SQL Server sources:

- `sys.dm_exec_requests`
  - `wait_type`
  - `wait_time`
  - `last_wait_type`
  - `blocking_session_id`
- `sys.dm_os_waiting_tasks`
  - current waiting tasks
  - wait duration
  - blocking session/task address where available
- `sys.dm_os_wait_stats`
  - recent deltas if the Sample timer has previous sample data

Display ideas:

- Top current wait types.
- Current blocked request count.
- CPU percent of total waits when it can be derived from wait deltas.
- Resource vs signal wait bars.
- Wait category bars similar to the snapshot mockup, including Disk IO, Extended Events, Latches: Buffer, Latches: Buffer IO, Latches: Non-Buffer, Locking, and Memory where practical.

### Memory snapshot

Purpose: show current SQL Server memory state.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Total Server Memory (KB)`
  - `Target Server Memory (KB)`
  - `Memory Grants Pending`
  - `Memory Grants Outstanding`
  - `Page life expectancy`
  - `Buffer cache hit ratio` and base
- `sys.dm_os_process_memory`
  - SQL Server process memory usage
  - process physical memory low indicator
  - system low memory signal where available
- `sys.dm_os_memory_clerks`
  - memory clerk usage grouped into buffer, stolen buffer, In-Mem OLTP, free, plan cache, columnstore, query grants, and other where practical
- `sys.dm_exec_query_memory_grants`
  - current requested/granted memory
  - waiting grants

Display ideas:

- Total vs target memory bar.
- Memory composition stacked bar with Buffer, Stolen Buffer, In-Mem OLTP, Free, Plan (SQL), Plan (Objects), Columnstore, Query Grants, and Other where available.
- Page life expectancy KPI.
- Memory grants pending/outstanding.
- Buffer cache hit ratio.
- Pages read/write bars.

### CPU / scheduler snapshot

Purpose: show current CPU pressure without platform-specific APIs.

Suggested SQL Server sources:

- `sys.dm_os_schedulers`
  - runnable tasks
  - active workers
  - current tasks
  - work queue count
- `sys.dm_exec_requests`
  - current CPU time for active requests
- `sys.dm_os_ring_buffers`, if available and permitted
  - SQL process CPU percent
  - system idle percent

Display ideas:

- Runnable tasks gauge.
- Active workers gauge.
- SQL CPU percent only when available from SQL Server.

### Database I/O snapshot

Purpose: show current database read/write behavior and latency.

Suggested SQL Server sources:

- `sys.dm_io_virtual_file_stats(NULL, NULL)`
  - reads/writes
  - bytes read/written
  - stalls
- `sys.databases`
  - database names
- `sys.master_files`
  - data/log file classification

Implementation guidance:

- Use Sample timer deltas to calculate current read/write MB/sec.
- Use stall deltas to estimate current read/write latency.
- Aggregate by database for display.

Display ideas:

- `ms/Read` and `ms/Write` by selected database file or total.
- Top databases by read/write throughput.
- Data vs log write split.
- Read/write latency KPIs.

### Log snapshot

Purpose: show current transaction log activity.

Suggested SQL Server sources:

- `sys.dm_os_performance_counters`
  - `Log Bytes Flushed/sec`
  - `Log Flushes/sec`
  - `Log Flush Waits/sec`
  - `Log Flush Wait Time`
  - `Percent Log Used`, where available
- `sys.dm_io_virtual_file_stats(NULL, NULL)` for log file write activity

Display ideas:

- Log flush throughput.
- Log flushes count/rate.
- Log flush waits.
- Log write latency.
- Percent log used for the busiest databases if available.

### Expensive or active requests

Purpose: make the snapshot useful for immediate troubleshooting.

Suggested SQL Server sources:

- `sys.dm_exec_requests`
  - `session_id`
  - `status`
  - `command`
  - `wait_type`
  - `blocking_session_id`
  - `cpu_time`
  - `total_elapsed_time`
  - `reads`
  - `writes`
  - `logical_reads`
  - `row_count`
- `sys.dm_exec_sessions`
  - login name, host name, program name
- `sys.dm_exec_sql_text(sql_handle)`
  - current SQL text, if permissions allow

Display ideas:

- Compact list of top active requests by elapsed time, CPU, logical reads, or waits.
- Keep text clipped and safe for narrow terminal widths.

## Suggested chart scales for mock data and initial visual testing

Live data may require adaptive or configurable scales, but these ranges are useful for mock data and initial visual validation against the provided screenshots.

| Section | Panel | Suggested scale |
|---|---|---:|
| SQL SERVER ACTIVITY | SQL SERVER ACTIVITY | 0 to 30000 |
| SQL SERVER ACTIVITY | Key lookups / Forwarded recs | 0 to 120000 |
| SQL SERVER ACTIVITY | BACKUP THROUGHPUT | 0.0 to 1.2 MB/sec |
| SQL SERVER WAITS | SQL SERVER WAITS | 0 to 12000 |
| SQL SERVER MEMORY | SQL SERVER MEMORY | 0 to 140000 |
| SQL SERVER MEMORY | CACHE HIT RATIOS / PLE | 0 to 100 |
| SQL SERVER MEMORY | PAGES READ / WRITE | 0 to 14000 |
| DATABASE IO | DATABASE IO | 0 to 250 ms |
| DATABASE IO | LOG FLUSHES | 0 to 1000 |
| DATABASE IO | CHECKPOINTS / LAZY WRITES | 0 to 4 |

## Mock data expectations

The dashboard should support both mock and live data if the existing Go project has a mock/demo mode.

Expected behavior:

- Mock data should be deterministic for screenshots, demos, and tests.
- Mock data should use the same metric labels and panel grouping as live data.
- Mock history should show related metrics moving together.
- Live data should preserve the same visual grouping and metric semantics.
- If a metric is unavailable, the panel should degrade gracefully instead of breaking the whole dashboard.

Mock data should not look purely random. It should contain recognizable operational patterns:

- A general workload wave raises batches, transactions, key lookups, memory usage, and log activity.
- A reporting window raises transactions, key lookups, memory pressure, pages read, and query grants.
- A batch or maintenance window raises compiles, recompiles, writes, log flushes, checkpoints, and lazy writes.
- An I/O burst raises disk waits, page reads/writes, and I/O latency.
- A CPU/wait spike raises CPU wait contribution and visible wait stack height.

## Data sources

Use the SQL Server connection context already available in the Go application.

The implementation should prefer lightweight queries suitable for a periodically refreshing dashboard. Avoid long-running or blocking queries in the UI path.

Likely SQL Server sources include dynamic management views and performance counters, for example:

- Current requests and sessions
- Current waiting tasks
- Wait statistics or recent wait deltas
- Memory counters
- SQL Server process memory indicators
- Scheduler pressure indicators
- Database I/O counters
- Log activity counters
- Page activity counters

The exact queries can be adapted to the existing project's database access layer and permissions model.

## Error handling

The Activity Monitor panel should remain usable if a collection tick fails.

Expected behavior:

- Show a non-fatal error message in the panel or toolbar.
- Keep previously collected data visible.
- Retry on the next refresh tick while that tab's refresh timer is running.
- Do not close the panel because one sample failed.
- A failure in one tab should not stop another tab's timer or connection.
- If one metric is unavailable because of SQL Server version, edition, or permissions, show the remaining metrics and mark the unavailable value cleanly.

## Accessibility and readability

The dashboard should remain useful when colors are imperfect or terminal fonts vary.

Expected behavior:

- Metric labels should identify every series shown in a chart.
- Important values should be displayed as text where possible.
- Colors should be consistent but not required for basic understanding.
- Section headers should be visually obvious.
- Grid lines should stay muted so they do not overpower data.
- Dense panels should prefer clear labels over decorative detail.
- Keyboard handling must not conflict with global application navigation.

## Acceptance criteria

The work is complete when:

- A new Activity Monitor panel exists in the Go application.
- The panel contains History, Sample, Sessions, and Block tabs.
- Each Activity Monitor tab owns and uses its own database connection.
- The History tab has its own refresh-rate selector with 2, 3, 5, and 10 second options.
- The Sample tab has its own refresh-rate selector with 2, 3, 5, and 10 second options.
- History and Sample have independent pause/continue controls.
- Pausing Sample does not pause History.
- Pausing History does not pause Sample when Sample is active.
- Sessions and Block do not use auto-refresh.
- Sessions and Block have a manual Refresh button.
- The History tab renders a dashboard inspired by `hi.png`.
- The History tab includes activity, waits, memory, cache/PLE, pages read/write, database I/O, log flushes, and checkpoints/lazy writes panels.
- The History waits panel is full-width when layout permits.
- The Sample tab renders a dashboard inspired by `sa.png`.
- The Sample tab includes current activity, waits, memory, and database I/O sections.
- Sessions and Block are selectable placeholder tabs.
- History data collection starts when the panel opens.
- History collection continues even when the History tab is not active.
- Sample refresh work runs only while the Sample tab is active.
- Data collection stops and in-memory data is discarded when the panel closes.
- History data is retained for only the last 30 minutes.
- The implementation uses SQL Server-provided metrics and avoids WMI, COM, and Windows-only counter APIs.
- Charts use the specified UTF-8 block, grid, legend, and box-drawing glyphs.
- Horizontal and vertical bars use eighth-block smoothing where useful.
- Stacked history columns have no internal vertical gaps.
- Legends fit or degrade predictably.
- The page works at 120 columns and looks good at 150 columns or wider.
- The page supports vertical scrolling when terminal height is limited.
- Missing or unavailable metrics degrade gracefully.
- Collection failures are displayed without crashing or closing the panel.
