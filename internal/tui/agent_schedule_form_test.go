package tui

import (
	"reflect"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// schedule builds a *gosmo.Schedule with just the frequency fields.
func schedule(ft gosmo.ScheduleFreqType, interval, relative, factor int,
	sub gosmo.ScheduleSubdayType, subInterval int) *gosmo.Schedule {
	return &gosmo.Schedule{
		Name:                 "nightly",
		Enabled:              true,
		FreqType:             ft,
		FreqInterval:         interval,
		FreqRelativeInterval: relative,
		FreqRecurrenceFactor: factor,
		FreqSubdayType:       sub,
		FreqSubdayInterval:   subInterval,
		ActiveStartTime:      10000,
		ActiveEndTime:        235959,
		ActiveStartDate:      time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

// populate and readFrequency are separate switches over FreqType sharing
// inverse tables. A schedule opened and OK'd unedited must come back identical,
// or FreqInterval (weekday bitmask, day of month, or relative day code) could
// silently turn "last weekday of the month" into "the 16th".
func TestScheduleFormPopulateReadFrequencyRoundTrips(t *testing.T) {
	weekdays := gosmo.WeekdayMonday | gosmo.WeekdayWednesday | gosmo.WeekdayFriday

	cases := []struct {
		name string
		sch  *gosmo.Schedule
		want gosmo.ScheduleFrequency
	}{{
		name: "daily every 3 days, every 30 minutes",
		sch:  schedule(gosmo.FreqDaily, 3, 0, 0, gosmo.SubdayMinutes, 30),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqDaily, FreqInterval: 3,
			FreqSubdayType: gosmo.SubdayMinutes, FreqSubdayInterval: 30,
		},
	}, {
		// Weekly bitmask: losing it still runs, on the wrong days.
		name: "weekly Mon/Wed/Fri, every 2 weeks",
		sch:  schedule(gosmo.FreqWeekly, weekdays, 0, 2, gosmo.SubdayOnce, 1),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqWeekly, FreqInterval: weekdays,
			FreqRecurrenceFactor: 2,
			FreqSubdayType:       gosmo.SubdayOnce, FreqSubdayInterval: 1,
		},
	}, {
		name: "monthly on the 15th, every 3 months",
		sch:  schedule(gosmo.FreqMonthly, 15, 0, 3, gosmo.SubdayHours, 4),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqMonthly, FreqInterval: 15,
			FreqRecurrenceFactor: 3,
			FreqSubdayType:       gosmo.SubdayHours, FreqSubdayInterval: 4,
		},
	}, {
		// Relative occurrence and relative day come from two Selects; both must
		// survive.
		name: "last weekday of every month",
		sch: schedule(gosmo.FreqMonthlyRelative, gosmo.RelativeDayWeekday,
			gosmo.RelativeLast, 1, gosmo.SubdaySeconds, 45),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqMonthlyRelative, FreqInterval: gosmo.RelativeDayWeekday,
			FreqRelativeInterval: gosmo.RelativeLast, FreqRecurrenceFactor: 1,
			FreqSubdayType: gosmo.SubdaySeconds, FreqSubdayInterval: 45,
		},
	}, {
		// FreqTypes without recurrence: populate fills recurs-every and
		// day-of-month with defaults, which readFrequency must ignore.
		name: "once",
		sch:  schedule(gosmo.FreqOnce, 0, 0, 0, gosmo.SubdayOnce, 0),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqOnce,
			// atLeast1 shows a stored 0 as 1, so 1 comes back; harmless,
			// SubdayOnce ignores it.
			FreqSubdayType: gosmo.SubdayOnce, FreqSubdayInterval: 1,
		},
	}, {
		name: "when SQL Server Agent starts",
		sch:  schedule(gosmo.FreqAutoStart, 0, 0, 0, gosmo.SubdayOnce, 1),
		want: gosmo.ScheduleFrequency{
			FreqType:       gosmo.FreqAutoStart,
			FreqSubdayType: gosmo.SubdayOnce, FreqSubdayInterval: 1,
		},
	}, {
		name: "when CPU becomes idle",
		sch:  schedule(gosmo.FreqOnIdle, 0, 0, 0, gosmo.SubdayOnce, 1),
		want: gosmo.ScheduleFrequency{
			FreqType:       gosmo.FreqOnIdle,
			FreqSubdayType: gosmo.SubdayOnce, FreqSubdayInterval: 1,
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newScheduleFreqForm()
			f.populate(tc.sch)
			if got := f.readFrequency(); got != tc.want {
				t.Errorf("readFrequency() after populate =\n %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// Every row populate touches must be clean, or every OK issues an
// sp_update_schedule (restamping date_modified, and writing substituted
// defaults as fact).
//
// weekdaysGrid is filled via SetRows, not SetValue, so it needs its own
// baseline reset.
func TestScheduleFormPopulateLeavesEveryRowClean(t *testing.T) {
	cases := []*gosmo.Schedule{
		schedule(gosmo.FreqDaily, 3, 0, 0, gosmo.SubdayMinutes, 30),
		schedule(gosmo.FreqWeekly, gosmo.WeekdayTuesday|gosmo.WeekdaySaturday, 0, 2, gosmo.SubdayOnce, 1),
		schedule(gosmo.FreqMonthly, 15, 0, 3, gosmo.SubdayHours, 4),
		schedule(gosmo.FreqMonthlyRelative, gosmo.RelativeDayWeekendDay, gosmo.RelativeThird, 1, gosmo.SubdayOnce, 1),
		schedule(gosmo.FreqOnce, 0, 0, 0, gosmo.SubdayOnce, 0),
	}
	for _, sch := range cases {
		f := newScheduleFreqForm()
		f.populate(sch)
		if f.frequencyDirty() {
			t.Errorf("FreqType %v: frequencyDirty() right after populate — every OK would rewrite the frequency", sch.FreqType)
		}
		if f.rangeDirty() {
			t.Errorf("FreqType %v: rangeDirty() right after populate — every OK would rewrite the active range", sch.FreqType)
		}
	}
}

// Each dirty predicate covers only its own rows; overlap would make a
// start-time edit rewrite the frequency (including substituted defaults).
func TestScheduleFormDirtyPredicatesDoNotOverlap(t *testing.T) {
	freqRows := func(f *scheduleFreqForm) { f.recurEveryField.Paste("7") }
	rangeRows := func(f *scheduleFreqForm) { f.startTimeField.Paste("06:30:00") }

	for _, tc := range []struct {
		name                string
		edit                func(*scheduleFreqForm)
		wantFreq, wantRange bool
	}{
		{"a frequency row", freqRows, true, false},
		{"a duration row", rangeRows, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newScheduleFreqForm()
			f.populate(schedule(gosmo.FreqDaily, 3, 0, 0, gosmo.SubdayMinutes, 30))
			tc.edit(f)
			if got := f.frequencyDirty(); got != tc.wantFreq {
				t.Errorf("frequencyDirty() = %v, want %v", got, tc.wantFreq)
			}
			if got := f.rangeDirty(); got != tc.wantRange {
				t.Errorf("rangeDirty() = %v, want %v", got, tc.wantRange)
			}
		})
	}
}

// readActiveRange handles HHMMSS integers and zero-Time-for-no-end-date. The
// "No end date" checkbox wins over a date still in the field.
func TestScheduleFormActiveRangeRoundTrips(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	t.Run("with an end date", func(t *testing.T) {
		sch := schedule(gosmo.FreqDaily, 1, 0, 0, gosmo.SubdayOnce, 1)
		sch.ActiveStartDate, sch.ActiveEndDate = start, end
		sch.ActiveStartTime, sch.ActiveEndTime = 63000, 224500

		f := newScheduleFreqForm()
		f.populate(sch)
		gotStart, gotEnd, gotStartTime, gotEndTime := f.readActiveRange()

		if !gotStart.Equal(start) || !gotEnd.Equal(end) {
			t.Errorf("dates = %v..%v, want %v..%v", gotStart, gotEnd, start, end)
		}
		if gotStartTime != 63000 || gotEndTime != 224500 {
			t.Errorf("times = %d..%d, want 63000..224500", gotStartTime, gotEndTime)
		}
	})

	t.Run("no end date leaves the end zero", func(t *testing.T) {
		sch := schedule(gosmo.FreqDaily, 1, 0, 0, gosmo.SubdayOnce, 1)
		sch.ActiveStartDate = start // ActiveEndDate stays zero

		f := newScheduleFreqForm()
		f.populate(sch)
		if !f.noEndDateCheck.Checked() {
			t.Fatal(`populate left "No end date" unticked for a schedule with no end date`)
		}
		if _, gotEnd, _, _ := f.readActiveRange(); !gotEnd.IsZero() {
			t.Errorf("end date = %v, want the zero Time", gotEnd)
		}
	})

	t.Run("the checkbox overrides a date still in the field", func(t *testing.T) {
		sch := schedule(gosmo.FreqDaily, 1, 0, 0, gosmo.SubdayOnce, 1)
		sch.ActiveStartDate, sch.ActiveEndDate = start, end

		f := newScheduleFreqForm()
		f.populate(sch)
		f.noEndDateCheck.SetChecked(true) // the field still reads 2026-12-31
		if _, gotEnd, _, _ := f.readActiveRange(); !gotEnd.IsZero() {
			t.Errorf("end date = %v with \"No end date\" ticked, want the zero Time — the schedule would keep expiring", gotEnd)
		}
	})
}

// Each dropdown is parallel label/code slices. Round trips can't see a swap
// (populate and readFrequency agree), yet Monday's box would set Tuesday's bit;
// only naming the pairs pins them.
//
// Lengths must match: a short value slice drops the selection to a zero code, a
// short label slice hides an option.
func TestScheduleDropdownLabelsMatchTheirCodes(t *testing.T) {
	t.Run("occurs", func(t *testing.T) {
		want := map[string]gosmo.ScheduleFreqType{
			"Once": gosmo.FreqOnce, "Daily": gosmo.FreqDaily, "Weekly": gosmo.FreqWeekly,
			"Monthly": gosmo.FreqMonthly, "Monthly (relative)": gosmo.FreqMonthlyRelative,
			"When SQL Server Agent starts": gosmo.FreqAutoStart,
			"When CPU becomes idle":        gosmo.FreqOnIdle,
		}
		checkLen(t, "scheduleOccursItems", len(scheduleOccursItems), len(scheduleOccursFreqTypes))
		for i, label := range scheduleOccursItems {
			if got := scheduleOccursFreqTypes[i]; got != want[label] {
				t.Errorf("%q maps to FreqType %v, want %v", label, got, want[label])
			}
		}
	})

	t.Run("weekdays", func(t *testing.T) {
		want := map[string]int{
			"Sunday": gosmo.WeekdaySunday, "Monday": gosmo.WeekdayMonday,
			"Tuesday": gosmo.WeekdayTuesday, "Wednesday": gosmo.WeekdayWednesday,
			"Thursday": gosmo.WeekdayThursday, "Friday": gosmo.WeekdayFriday,
			"Saturday": gosmo.WeekdaySaturday,
		}
		checkLen(t, "weekdayNames", len(weekdayNames), len(weekdayBits))
		for i, label := range weekdayNames {
			if got := weekdayBits[i]; got != want[label] {
				t.Errorf("the %q checkbox sets bit %d, want %d", label, got, want[label])
			}
		}
	})

	t.Run("relative occurrence", func(t *testing.T) {
		want := map[string]int{
			"First": gosmo.RelativeFirst, "Second": gosmo.RelativeSecond,
			"Third": gosmo.RelativeThird, "Fourth": gosmo.RelativeFourth,
			"Last": gosmo.RelativeLast,
		}
		checkLen(t, "scheduleRelativeItems", len(scheduleRelativeItems), len(scheduleRelativeValues))
		for i, label := range scheduleRelativeItems {
			if got := scheduleRelativeValues[i]; got != want[label] {
				t.Errorf("%q maps to relative interval %d, want %d", label, got, want[label])
			}
		}
	})

	t.Run("relative day", func(t *testing.T) {
		want := map[string]int{
			"Sunday": gosmo.RelativeDaySunday, "Monday": gosmo.RelativeDayMonday,
			"Tuesday": gosmo.RelativeDayTuesday, "Wednesday": gosmo.RelativeDayWednesday,
			"Thursday": gosmo.RelativeDayThursday, "Friday": gosmo.RelativeDayFriday,
			"Saturday": gosmo.RelativeDaySaturday, "Day": gosmo.RelativeDayDay,
			"Weekday": gosmo.RelativeDayWeekday, "Weekend day": gosmo.RelativeDayWeekendDay,
		}
		checkLen(t, "scheduleRelativeDayItems", len(scheduleRelativeDayItems), len(scheduleRelativeDayValues))
		for i, label := range scheduleRelativeDayItems {
			if got := scheduleRelativeDayValues[i]; got != want[label] {
				t.Errorf("%q maps to relative day %d, want %d", label, got, want[label])
			}
		}
	})

	t.Run("daily frequency", func(t *testing.T) {
		want := map[string]gosmo.ScheduleSubdayType{
			"Once": gosmo.SubdayOnce, "Every N seconds": gosmo.SubdaySeconds,
			"Every N minutes": gosmo.SubdayMinutes, "Every N hours": gosmo.SubdayHours,
		}
		checkLen(t, "scheduleSubdayItems", len(scheduleSubdayItems), len(scheduleSubdayTypes))
		for i, label := range scheduleSubdayItems {
			if got := scheduleSubdayTypes[i]; got != want[label] {
				t.Errorf("%q maps to subday type %v, want %v", label, got, want[label])
			}
		}
	})
}

func checkLen(t *testing.T, name string, labels, values int) {
	t.Helper()
	if labels != values {
		t.Fatalf("%s has %d labels but %d codes — the dropdown and its mapping have drifted apart", name, labels, values)
	}
}

// defaultWeekdayMask is written verbatim when switching to Weekly, so it must
// be exactly Mon-Fri.
func TestDefaultWeekdayMaskIsMondayToFriday(t *testing.T) {
	f := newScheduleFreqForm()
	f.setWeekdayGrid(defaultWeekdayMask)
	want := map[string]bool{
		"Sunday": false, "Monday": true, "Tuesday": true, "Wednesday": true,
		"Thursday": true, "Friday": true, "Saturday": false,
	}
	for i, name := range weekdayNames {
		if got := f.weekdaysGrid.Values()[i][0]; got != want[name] {
			t.Errorf("%s checked = %v, want %v", name, got, want[name])
		}
	}
}

// readActiveRange ignores parse errors (00:00:00, no end date), safe only
// because Form.Validate rejects bad fields first.
func TestScheduleFormClockAndDateValidators(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"00:00:00", true}, {"23:59:59", true}, {"6:30:00", true},
		{"24:00:00", false}, // hour out of range
		{"12:60:00", false}, // minute out of range
		{"12:00:60", false}, // second out of range
		{"12:30", false},    // not HH:MM:SS
		{"noon", false}, {"", false},
	} {
		if err := validateAgentClock(tc.in); (err == nil) != tc.ok {
			t.Errorf("validateAgentClock(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}

	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"2026-03-01", true},
		// Empty is valid: parseAgentDate maps it to the zero Time ("no end
		// date").
		{"", true},
		{"2026-13-01", false}, {"01-03-2026", false}, {"tomorrow", false},
	} {
		if err := validateAgentDate(tc.in); (err == nil) != tc.ok {
			t.Errorf("validateAgentDate(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// Every row is either in rows() or placed by the caller. A row in readFrequency
// but not rows() is invisible yet written on every Apply. Reflection, because a
// hand list would be updated in the same edit that forgot the row.
func TestEveryScheduleFormRowIsReachable(t *testing.T) {
	// nameField and enabledCheck are placed by each caller in its identity
	// section.
	placedByCaller := map[string]bool{"nameField": true, "enabledCheck": true}

	f := newScheduleFreqForm()
	// Compared by address: fields are unexported, so reflect refuses
	// Interface() but allows Pointer().
	inRows := make(map[uintptr]bool)
	for _, r := range f.rows() {
		if rv := reflect.ValueOf(r); rv.Kind() == reflect.Ptr {
			inRows[rv.Pointer()] = true
		}
	}

	v := reflect.ValueOf(f).Elem()
	for i := range v.NumField() {
		field, name := v.Field(i), v.Type().Field(i).Name
		if field.Kind() != reflect.Ptr {
			t.Fatalf("%s is not a row pointer — this test assumes every field of scheduleFreqForm is one", name)
		}
		row := field.Pointer()
		switch {
		case placedByCaller[name] && inRows[row]:
			t.Errorf("%s is in rows() as well as being placed by the caller — it would appear twice", name)
		case !placedByCaller[name] && !inRows[row]:
			t.Errorf("%s is missing from rows(): the user can never see or edit it, but readFrequency still writes it", name)
		}
	}
}
