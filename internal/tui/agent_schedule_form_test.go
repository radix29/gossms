package tui

import (
	"reflect"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// schedule builds a *gosmo.Schedule carrying just the frequency fields, so
// each round-trip case below reads as the msdb row it stands for.
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

// Schedule Properties loads a schedule into the form and writes it back
// through readFrequency. The two halves are separate switches over FreqType
// — populate decides which rows a stored value lands in, readFrequency
// decides which rows a written value is read from — and only agree because
// the index/value tables they share are inverses. A schedule opened and
// OK'd without an edit must therefore come back byte-identical, or the page
// silently rewrites the frequency: the classic form is FreqInterval, whose
// meaning is FreqType-dependent (a weekday bitmask, a day of month, a
// relative day code), so a mismatched pair turns "last weekday of the
// month" into "the 16th" with no error anywhere.
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
		// FreqInterval is a weekday bitmask here and nothing else — the one
		// case where losing it produces a schedule that still runs, just on
		// the wrong days.
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
		// Two coupled codes, read from two different Select rows: the
		// relative occurrence and the relative day both have to survive.
		name: "last weekday of every month",
		sch: schedule(gosmo.FreqMonthlyRelative, gosmo.RelativeDayWeekday,
			gosmo.RelativeLast, 1, gosmo.SubdaySeconds, 45),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqMonthlyRelative, FreqInterval: gosmo.RelativeDayWeekday,
			FreqRelativeInterval: gosmo.RelativeLast, FreqRecurrenceFactor: 1,
			FreqSubdayType: gosmo.SubdaySeconds, FreqSubdayInterval: 45,
		},
	}, {
		// The three FreqTypes with no recurrence of their own. populate
		// still fills the recurs-every and day-of-month rows with in-range
		// defaults, and readFrequency must ignore both rather than write
		// those defaults out as a real interval.
		name: "once",
		sch:  schedule(gosmo.FreqOnce, 0, 0, 0, gosmo.SubdayOnce, 0),
		want: gosmo.ScheduleFrequency{
			FreqType: gosmo.FreqOnce,
			// atLeast1: a stored 0 is below the spinner's declared minimum,
			// so populate shows 1 and 1 is what comes back. Deliberate, and
			// harmless — SubdayOnce ignores the interval.
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

// populate is the post-load setter, so every row it touches must come back
// clean. Schedule Properties gates its two writes on frequencyDirty() and
// rangeDirty() precisely so that opening a schedule and pressing OK writes
// nothing; a row whose setter left it dirty would make every OK issue an
// sp_update_schedule, rewriting the frequency from the form's own
// re-encoding of it. That is not a no-op even when the encoding round-trips:
// it restamps date_modified, and on the FreqTypes where populate
// substitutes an in-range default for an irrelevant stored field, it writes
// the default back as fact.
//
// weekdaysGrid is the one to watch — it is filled through SetRows rather
// than a SetValue, so it needs its own baseline reset, and it is the only
// row here that carries a matrix rather than a scalar.
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

// The two dirty predicates gate two independent writes, so each must answer
// only for its own rows. Were either to widen to the whole form, editing a
// start time would also write the frequency — and on a FreqType whose
// stored FreqInterval populate deliberately replaced with a default, that
// write is a real change to the schedule.
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

// readActiveRange is the other half of the load/write pair, over msdb's two
// encodings: HHMMSS-as-an-integer for the times, and a zero Time meaning
// "no end date" for the dates. The end date is the one with a second
// source of truth — the "No end date" checkbox — and the checkbox wins, so
// a schedule that had an end date, then had the box ticked, must write the
// zero Time rather than the date still sitting in the field.
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

// The Occurs/weekday/relative/subday dropdowns are each a pair of parallel
// slices: the labels the user picks from, and the msdb codes at the same
// indices. A round-trip through the form cannot see a fault here, because
// both halves read the same pair — swap two entries in weekdayBits and
// populate/readFrequency still agree, while the checkbox labelled Monday
// now sets Tuesday's bit. Only naming the pairs pins them.
//
// The length checks are the other half: a label added to one slice and not
// the other is silently absorbed. A short value slice makes the bounds
// guards in readFrequency drop the selection and write a zero code, and a
// short label slice hides a real option from the dropdown entirely.
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

// defaultWeekdayMask stands in for a weekday selection that isn't known, on
// every FreqType where weekdays don't apply. It is written out verbatim
// whenever the user switches a schedule to Weekly, so it has to be the
// Mon-Fri its name and comment claim — a mask that quietly included Sunday
// would put a "weekdays only" job on the weekend.
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

// readActiveRange parses its four fields with the error ignored, falling
// back to 00:00:00 for a time and "no end date" for a date. That is only
// safe because Form.Validate has already rejected a malformed field, so
// these validators are the thing standing between a typo and a schedule
// silently rewritten to run at midnight forever.
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
		// Empty is valid on purpose: parseAgentDate maps it to the zero
		// Time, which is how "no end date" is spelled.
		{"", true},
		{"2026-13-01", false}, {"01-03-2026", false}, {"tomorrow", false},
	} {
		if err := validateAgentDate(tc.in); (err == nil) != tc.ok {
			t.Errorf("validateAgentDate(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// Every row scheduleFreqForm holds is either spliced into the form by
// rows() or placed by the caller in its own identity section — there is no
// third option. A field added to the struct and wired into readFrequency
// but forgotten in rows() is invisible and uneditable, yet still written on
// every Apply, so it pins the schedule to whatever the constructor
// defaulted it to. Walking the struct by reflection is the point: a test
// listing the rows by hand would be updated in the same edit that forgot
// the row.
func TestEveryScheduleFormRowIsReachable(t *testing.T) {
	// nameField and enabledCheck are deliberately not in rows() — Schedule
	// Properties and New Schedule each place them in their own identity
	// section, alongside an Owner row that only one of them has.
	placedByCaller := map[string]bool{"nameField": true, "enabledCheck": true}

	f := newScheduleFreqForm()
	// Identity is compared by address, not by interface value: every field
	// of scheduleFreqForm is unexported, and reflect refuses Interface() on
	// those. Pointer() is allowed, and each row is a distinct pointer.
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
