package tui

import (
	"context"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

var scheduleOccursItems = []string{
	"Once", "Daily", "Weekly", "Monthly", "Monthly (relative)",
	"When SQL Server Agent starts", "When CPU becomes idle",
}

// scheduleOccursFreqTypes is scheduleOccursItems' index-to-FreqType mapping.
var scheduleOccursFreqTypes = []gosmo.ScheduleFreqType{
	gosmo.FreqOnce, gosmo.FreqDaily, gosmo.FreqWeekly, gosmo.FreqMonthly,
	gosmo.FreqMonthlyRelative, gosmo.FreqAutoStart, gosmo.FreqOnIdle,
}

func scheduleOccursIndex(ft gosmo.ScheduleFreqType) int {
	for i, v := range scheduleOccursFreqTypes {
		if v == ft {
			return i
		}
	}
	return 1 // Daily
}

var scheduleSubdayItems = []string{"Once", "Every N seconds", "Every N minutes", "Every N hours"}
var scheduleSubdayTypes = []gosmo.ScheduleSubdayType{gosmo.SubdayOnce, gosmo.SubdaySeconds, gosmo.SubdayMinutes, gosmo.SubdayHours}

func scheduleSubdayIndex(st gosmo.ScheduleSubdayType) int {
	for i, v := range scheduleSubdayTypes {
		if v == st {
			return i
		}
	}
	return 0
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var weekdayBits = []int{gosmo.WeekdaySunday, gosmo.WeekdayMonday, gosmo.WeekdayTuesday, gosmo.WeekdayWednesday, gosmo.WeekdayThursday, gosmo.WeekdayFriday, gosmo.WeekdaySaturday}

// defaultWeekdayMask is the Mon-Fri default used whenever a Weekly
// schedule's weekday selection isn't otherwise known — a brand-new row,
// or a row being synced/defaulted for a FreqType where weekdays don't
// apply.
var defaultWeekdayMask = gosmo.WeekdayMonday | gosmo.WeekdayTuesday | gosmo.WeekdayWednesday | gosmo.WeekdayThursday | gosmo.WeekdayFriday

var scheduleRelativeItems = []string{"First", "Second", "Third", "Fourth", "Last"}
var scheduleRelativeValues = []int{gosmo.RelativeFirst, gosmo.RelativeSecond, gosmo.RelativeThird, gosmo.RelativeFourth, gosmo.RelativeLast}

func scheduleRelativeIndex(v int) int {
	for i, r := range scheduleRelativeValues {
		if r == v {
			return i
		}
	}
	return 0
}

var scheduleRelativeDayItems = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Day", "Weekday", "Weekend day"}
var scheduleRelativeDayValues = []int{
	gosmo.RelativeDaySunday, gosmo.RelativeDayMonday, gosmo.RelativeDayTuesday, gosmo.RelativeDayWednesday,
	gosmo.RelativeDayThursday, gosmo.RelativeDayFriday, gosmo.RelativeDaySaturday,
	gosmo.RelativeDayDay, gosmo.RelativeDayWeekday, gosmo.RelativeDayWeekendDay,
}

func scheduleRelativeDayIndex(v int) int {
	for i, r := range scheduleRelativeDayValues {
		if r == v {
			return i
		}
	}
	return 0
}

// parseAgentClock parses "HH:MM:SS" into msdb's HHMMSS integer encoding.
func parseAgentClock(s string) (int, error) {
	var h, m, sec int
	if _, err := fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec); err != nil {
		return 0, fmt.Errorf("time must be HH:MM:SS")
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return 0, fmt.Errorf("time must be a valid HH:MM:SS")
	}
	return h*10000 + m*100 + sec, nil
}

// formatAgentClock is parseAgentClock's inverse.
func formatAgentClock(n int) string {
	return fmt.Sprintf("%02d:%02d:%02d", n/10000, (n%10000)/100, n%100)
}

// parseAgentDate parses "YYYY-MM-DD", returning the zero Time for "".
func parseAgentDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

func formatAgentDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// jobScheduleAttach tracks one shared schedule's "is it attached to this
// job" state — mirrors agent_job_props_alerts.go's jobAlertLink, since
// attaching/detaching a shared schedule is the same shape of relationship
// as linking an alert's job response: a toggle against the full list of
// schedules that exist, not a page-local editor of the schedule's own
// definition (that's Schedule Properties, agent_schedule_props.go, reached
// from the Schedules folder).
type jobScheduleAttach struct {
	sch          *gosmo.Schedule
	origAttached bool
	attached     bool
}

// pageJobSchedules is the Schedules page: every shared schedule on the
// server, toggled attached or not to this job, plus a read-only "selected
// schedule" detail panel. Previously this page only listed schedules
// already attached with no way to see what else was available to attach
// (a real gap — SchedulesContext() only returns attached rows) and edited
// a schedule's own definition inline; that edit surface moved to Schedule
// Properties so a shared schedule's definition has exactly one place it's
// edited, matching how Schedule Properties' own read-only Jobs page points
// back here rather than duplicating the attach control.
func pageJobSchedules(sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "Schedules",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			j, err := findAgentJob(ctx, sc, *jobName)
			if err != nil {
				return nil, nil, err
			}
			all, err := sc.Server.SchedulesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			attachedScheds, err := j.SchedulesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			attachedNames := make(map[string]bool, len(attachedScheds))
			for _, sch := range attachedScheds {
				attachedNames[sch.Name] = true
			}

			edits := make([]*jobScheduleAttach, len(all))
			for i, sch := range all {
				a := attachedNames[sch.Name]
				edits[i] = &jobScheduleAttach{sch: sch, origAttached: a, attached: a}
			}

			cols := []string{"Attached", "Name", "Enabled", "Frequency"}
			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{mapCell(e.attached), e.sch.Name, fmt.Sprintf("%v", e.sch.Enabled), e.sch.Description()}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData(cols, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 0 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].attached = !edits[row].attached
				grid.SetData(cols, rowsFor())
				grid.SetSelectedRow(row)
			}

			nameStatic := propsheet.Static("Name", "")
			enabledStatic := propsheet.Static("Enabled", "")
			ownerStatic := propsheet.Static("Owner", "")
			startStatic := propsheet.Static("Start date", "")
			endStatic := propsheet.Static("End date", "")
			descStatic := propsheet.Static("Description", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(edits) {
					nameStatic.SetValue("")
					enabledStatic.SetValue("")
					ownerStatic.SetValue("")
					startStatic.SetValue("")
					endStatic.SetValue("")
					descStatic.SetValue("")
					return
				}
				sch := edits[row].sch
				nameStatic.SetValue(sch.Name)
				enabledStatic.SetValue(fmt.Sprintf("%v", sch.Enabled))
				ownerStatic.SetValue(sch.OwnerLoginName)
				startStatic.SetValue(formatAgentDate(sch.ActiveStartDate))
				endDate := "No end date"
				if !sch.ActiveEndDate.IsZero() {
					endDate = formatAgentDate(sch.ActiveEndDate)
				}
				endStatic.SetValue(endDate)
				descStatic.SetValue(sch.Description())
			}
			grid.OnSelectRow = syncFromSelection
			if len(edits) > 0 {
				syncFromSelection(0)
			}

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.attached != e.origAttached {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.attached = e.origAttached
				}
				grid.SetData(cols, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Attach schedules to this job"),
				gridRow,
				propsheet.Note("Space/Enter (or click) on Attached toggles whether this job runs on that schedule. A schedule may be shared by more than one job. Edit a schedule's own definition, or create a new one, from SQL Server Agent > Schedules > (schedule) > Properties / New Schedule..."),
				propsheet.Section("Selected schedule"),
				nameStatic, enabledStatic, ownerStatic, startStatic, endStatic, descStatic,
			)

			apply := func(ctx context.Context) error {
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					if e.attached == e.origAttached {
						continue
					}
					if e.attached {
						if err := j.AttachScheduleContext(ctx, e.sch.Name); err != nil {
							return err
						}
					} else if err := j.DetachScheduleContext(ctx, e.sch.Name); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// atLeast1 clamps n to at least 1, the minimum every "recurs every" spinner
// (agent_schedule_form.go) accepts — a freshly loaded schedule with a
// stored 0 (unused for its FreqType) would otherwise show a field value
// below the row's own declared minimum.
func atLeast1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
