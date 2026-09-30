package xevent

import (
	"time"
)

// Value is one field or action of an event.
type Value struct {
	Name string
	// Value is the raw value: a map-typed field's key, an XML fragment's markup.
	Value string
	// Text is a map-typed field's text (wait_type 179's "PAGEIOLATCH_SH"), empty
	// for every other field.
	Text string
	// IsXML marks a value holding an XML fragment (xml_deadlock_report's
	// xml_report) rather than text.
	IsXML bool
}

// Display is the value as the grid and the details pane show it: a map field's
// text rather than its key, as SSMS shows it.
func (v Value) Display() string {
	if v.Text != "" {
		return v.Text
	}
	return v.Value
}

// Event is one event a target recorded.
type Event struct {
	// ID is the store's own number for the event, assigned by Store.Add in
	// arrival order and never reused. It is what a view holding a subset of
	// the store compares against Store.OldestID to see what has aged out.
	ID uint64

	Name      string
	Package   string
	Timestamp time.Time // UTC

	// Seq is the package0.event_sequence action, 0 when the session does not
	// collect it.
	Seq uint64

	Fields  []Value
	Actions []Value
}

// Field returns the named field and whether the event carries it.
func (e *Event) Field(name string) (Value, bool) { return findValue(e.Fields, name) }

// Action returns the named action and whether the event carries it.
func (e *Event) Action(name string) (Value, bool) { return findValue(e.Actions, name) }

func findValue(vs []Value, name string) (Value, bool) {
	for _, v := range vs {
		if v.Name == name {
			return v, true
		}
	}
	return Value{}, false
}

// TimestampLayout is how a timestamp is shown: local time to the microsecond,
// which is as fine as an event's timestamp is recorded. It sorts as text, so
// the filter's < and > work on it without parsing.
const TimestampLayout = "2006-01-02 15:04:05.000000"

// FormatTimestamp renders t in the viewer's local time.
func FormatTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(TimestampLayout)
}
