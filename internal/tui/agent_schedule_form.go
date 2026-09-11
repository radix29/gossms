package tui

import (
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_schedule_form.go is the schedule-frequency UI shared by New Schedule
// and Schedule Properties (Occurs, Recurs every, Day of month, Weekdays,
// Relative, Daily frequency, Duration). Its tables and helpers
// (scheduleOccursItems, weekdayNames, defaultWeekdayMask, parseAgentClock,
// atLeast1, ...) are in agent_job_props_schedules.go.

// scheduleFreqForm bundles a schedule definition's rows and the read/write
// helpers.
type scheduleFreqForm struct {
	nameField           *propsheet.TextRow
	enabledCheck        *propsheet.CheckRow
	occursSelect        *propsheet.SelectRow
	recurEveryField     *propsheet.TextRow
	dayOfMonthField     *propsheet.TextRow
	weekdaysGrid        *propsheet.ToggleGridRow
	relativeSelect      *propsheet.SelectRow
	relativeDaySelect   *propsheet.SelectRow
	subdaySelect        *propsheet.SelectRow
	subdayIntervalField *propsheet.TextRow
	startTimeField      *propsheet.TextRow
	endTimeField        *propsheet.TextRow
	startDateField      *propsheet.TextRow
	noEndDateCheck      *propsheet.CheckRow
	endDateField        *propsheet.TextRow
}

// newScheduleFreqForm builds rows defaulted for a new schedule (Daily, Mon-Fri,
// 01:00:00-23:59:59, no end date); Schedule Properties overwrites them via
// populate.
func newScheduleFreqForm() *scheduleFreqForm {
	f := &scheduleFreqForm{
		nameField:           propsheet.Text("Name", "", 30),
		enabledCheck:        propsheet.Check("Enabled", true),
		occursSelect:        propsheet.Select("Occurs", scheduleOccursItems, 1),
		recurEveryField:     propsheet.Int("Recurs every (day/week/month)", 1, 1, 999, ""),
		dayOfMonthField:     propsheet.Int("Day of month", 1, 1, 31, ""),
		weekdaysGrid:        propsheet.NewToggleGrid([]string{"Selected", "Day"}, []int{0}, 9),
		relativeSelect:      propsheet.Select("Relative occurrence", scheduleRelativeItems, 0),
		relativeDaySelect:   propsheet.Select("Relative day", scheduleRelativeDayItems, 0),
		subdaySelect:        propsheet.Select("Daily frequency", scheduleSubdayItems, 0),
		subdayIntervalField: propsheet.Int("Every N (per Daily frequency)", 1, 1, 9999, ""),
		startTimeField:      propsheet.Text("Start time (HH:MM:SS)", "01:00:00", 10),
		endTimeField:        propsheet.Text("End time (HH:MM:SS)", "23:59:59", 10),
		startDateField:      propsheet.Text("Start date (YYYY-MM-DD)", formatAgentDate(time.Now()), 12),
		noEndDateCheck:      propsheet.Check("No end date", true),
		endDateField:        propsheet.Text("End date (YYYY-MM-DD)", "", 12),
	}
	f.setWeekdayGrid(defaultWeekdayMask)
	f.startTimeField.SetValidate(validateAgentClock)
	f.endTimeField.SetValidate(validateAgentClock)
	f.startDateField.SetValidate(validateAgentDate)
	// The end date is ignored when "No end date" is checked, so its validator
	// defers too.
	f.endDateField.SetValidate(func(s string) error {
		if f.noEndDateCheck.Checked() {
			return nil
		}
		return validateAgentDate(s)
	})
	return f
}

// validateAgentClock and validateAgentDate adapt the parsers for
// TextRow.SetValidate, so Form.Validate rejects bad input instead of
// readActiveRange silently using 00:00:00 or "no end date".
func validateAgentClock(s string) error {
	_, err := parseAgentClock(s)
	return err
}

func validateAgentDate(s string) error {
	_, err := parseAgentDate(s)
	return err
}

// rows returns the Frequency, Daily frequency and Duration rows. Callers place
// nameField/enabledCheck (and Schedule Properties' Owner) in their own identity
// section.
func (f *scheduleFreqForm) rows() []propsheet.Row {
	return []propsheet.Row{
		propsheet.Section("Frequency"),
		f.occursSelect, f.recurEveryField, f.dayOfMonthField, f.weekdaysGrid,
		f.relativeSelect, f.relativeDaySelect,
		propsheet.Note("Only the fields for the selected Occurs value apply — e.g. weekdays only matter when Occurs is Weekly."),
		propsheet.Section("Daily frequency"),
		f.subdaySelect, f.subdayIntervalField, f.startTimeField, f.endTimeField,
		propsheet.Section("Duration"),
		f.startDateField, f.noEndDateCheck, f.endDateField,
	}
}

func (f *scheduleFreqForm) setWeekdayGrid(mask int) {
	text := make([][]string, len(weekdayNames))
	vals := make([][]bool, len(weekdayNames))
	for i, name := range weekdayNames {
		text[i] = []string{name}
		vals[i] = []bool{mask&weekdayBits[i] != 0}
	}
	f.weekdaysGrid.SetRows(text, vals)
}

func (f *scheduleFreqForm) weekdayMask() int {
	mask := 0
	for i, row := range f.weekdaysGrid.Values() {
		if i < len(weekdayBits) && row[0] {
			mask |= weekdayBits[i]
		}
	}
	return mask
}

func (f *scheduleFreqForm) name() string  { return strings.TrimSpace(f.nameField.Value()) }
func (f *scheduleFreqForm) enabled() bool { return f.enabledCheck.Checked() }

// populate fills the rows from an existing schedule. Fields irrelevant to
// sch.FreqType get safe in-range defaults rather than raw FreqInterval, whose
// meaning depends on FreqType (weekly bitmask, day of month, relative day code,
// or unused).
func (f *scheduleFreqForm) populate(sch *gosmo.Schedule) {
	f.nameField.SetValue(sch.Name)
	f.enabledCheck.SetChecked(sch.Enabled)
	f.occursSelect.SetSelected(scheduleOccursIndex(sch.FreqType))
	switch sch.FreqType {
	case gosmo.FreqDaily:
		f.recurEveryField.SetValue(strconv.Itoa(atLeast1(sch.FreqInterval)))
		f.dayOfMonthField.SetValue("1")
		f.setWeekdayGrid(defaultWeekdayMask)
		f.relativeSelect.SetSelected(0)
		f.relativeDaySelect.SetSelected(0)
	case gosmo.FreqWeekly:
		f.recurEveryField.SetValue(strconv.Itoa(atLeast1(sch.FreqRecurrenceFactor)))
		f.dayOfMonthField.SetValue("1")
		f.setWeekdayGrid(sch.FreqInterval)
		f.relativeSelect.SetSelected(0)
		f.relativeDaySelect.SetSelected(0)
	case gosmo.FreqMonthly:
		f.recurEveryField.SetValue(strconv.Itoa(atLeast1(sch.FreqRecurrenceFactor)))
		f.dayOfMonthField.SetValue(strconv.Itoa(atLeast1(sch.FreqInterval)))
		f.setWeekdayGrid(defaultWeekdayMask)
		f.relativeSelect.SetSelected(0)
		f.relativeDaySelect.SetSelected(0)
	case gosmo.FreqMonthlyRelative:
		f.recurEveryField.SetValue(strconv.Itoa(atLeast1(sch.FreqRecurrenceFactor)))
		f.dayOfMonthField.SetValue("1")
		f.setWeekdayGrid(defaultWeekdayMask)
		f.relativeSelect.SetSelected(scheduleRelativeIndex(sch.FreqRelativeInterval))
		f.relativeDaySelect.SetSelected(scheduleRelativeDayIndex(sch.FreqInterval))
	default: // FreqOnce, FreqAutoStart, FreqOnIdle
		f.recurEveryField.SetValue("1")
		f.dayOfMonthField.SetValue("1")
		f.setWeekdayGrid(defaultWeekdayMask)
		f.relativeSelect.SetSelected(0)
		f.relativeDaySelect.SetSelected(0)
	}
	f.subdaySelect.SetSelected(scheduleSubdayIndex(sch.FreqSubdayType))
	f.subdayIntervalField.SetValue(strconv.Itoa(atLeast1(sch.FreqSubdayInterval)))
	f.startTimeField.SetValue(formatAgentClock(sch.ActiveStartTime))
	f.endTimeField.SetValue(formatAgentClock(sch.ActiveEndTime))
	f.startDateField.SetValue(formatAgentDate(sch.ActiveStartDate))
	f.noEndDateCheck.SetChecked(sch.ActiveEndDate.IsZero())
	f.endDateField.SetValue(formatAgentDate(sch.ActiveEndDate))
}

// readFrequency builds a gosmo.ScheduleFrequency from the fields relevant to
// the selected Occurs.
func (f *scheduleFreqForm) readFrequency() gosmo.ScheduleFrequency {
	freq := gosmo.ScheduleFrequency{}
	idx := f.occursSelect.Selected()
	if idx >= 0 && idx < len(scheduleOccursFreqTypes) {
		freq.FreqType = scheduleOccursFreqTypes[idx]
	}
	switch freq.FreqType {
	case gosmo.FreqDaily:
		freq.FreqInterval = intRowValue0(f.recurEveryField.IntValue())
	case gosmo.FreqWeekly:
		freq.FreqInterval = f.weekdayMask()
		freq.FreqRecurrenceFactor = intRowValue0(f.recurEveryField.IntValue())
	case gosmo.FreqMonthly:
		freq.FreqInterval = intRowValue0(f.dayOfMonthField.IntValue())
		freq.FreqRecurrenceFactor = intRowValue0(f.recurEveryField.IntValue())
	case gosmo.FreqMonthlyRelative:
		ri := f.relativeSelect.Selected()
		if ri >= 0 && ri < len(scheduleRelativeValues) {
			freq.FreqRelativeInterval = scheduleRelativeValues[ri]
		}
		di := f.relativeDaySelect.Selected()
		if di >= 0 && di < len(scheduleRelativeDayValues) {
			freq.FreqInterval = scheduleRelativeDayValues[di]
		}
		freq.FreqRecurrenceFactor = intRowValue0(f.recurEveryField.IntValue())
	}
	si := f.subdaySelect.Selected()
	if si >= 0 && si < len(scheduleSubdayTypes) {
		freq.FreqSubdayType = scheduleSubdayTypes[si]
	}
	freq.FreqSubdayInterval = intRowValue0(f.subdayIntervalField.IntValue())
	return freq
}

// readActiveRange parses the Duration fields into
// SetActiveRangeContext's/CreateScheduleRequest's shape.
func (f *scheduleFreqForm) readActiveRange() (startDate, endDate time.Time, startTime, endTime int) {
	if t, err := parseAgentClock(f.startTimeField.Value()); err == nil {
		startTime = t
	}
	if t, err := parseAgentClock(f.endTimeField.Value()); err == nil {
		endTime = t
	}
	if d, err := parseAgentDate(f.startDateField.Value()); err == nil {
		startDate = d
	}
	if !f.noEndDateCheck.Checked() {
		if d, err := parseAgentDate(f.endDateField.Value()); err == nil {
			endDate = d
		}
	}
	return startDate, endDate, startTime, endTime
}

// frequencyDirty reports whether any readFrequency field changed, so Schedule
// Properties writes the frequency only when its own rows changed.
func (f *scheduleFreqForm) frequencyDirty() bool {
	return f.occursSelect.Dirty() || f.recurEveryField.Dirty() || f.dayOfMonthField.Dirty() ||
		f.weekdaysGrid.Dirty() || f.relativeSelect.Dirty() || f.relativeDaySelect.Dirty() ||
		f.subdaySelect.Dirty() || f.subdayIntervalField.Dirty()
}

// rangeDirty reports whether any readActiveRange field changed.
func (f *scheduleFreqForm) rangeDirty() bool {
	return f.startTimeField.Dirty() || f.endTimeField.Dirty() || f.startDateField.Dirty() ||
		f.noEndDateCheck.Dirty() || f.endDateField.Dirty()
}
