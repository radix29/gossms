package xevent

// DefaultCapacity is how many events a Store holds before the oldest go; it
// bounds memory.
const DefaultCapacity = 100_000

// Store holds the most recent events a viewer has read, oldest first, up to
// its capacity; past it the oldest are dropped and counted. It also registers
// every column its events have carried, in first-seen order.
//
// Not safe for concurrent use: the viewer owns it on the UI goroutine.
type Store struct {
	capacity int

	// ring holds the events; head is the index of the oldest, n how many
	// there are. Pointers, so a view's subset of them stays valid after the
	// store drops them.
	ring []*Event
	head int
	n    int

	nextID  uint64
	dropped int64

	fields, actions         []string
	seenFields, seenActions map[string]bool
}

// NewStore makes an empty store holding at most capacity events;
// capacity <= 0 means DefaultCapacity.
func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{
		capacity:    capacity,
		seenFields:  map[string]bool{},
		seenActions: map[string]bool{},
	}
}

// Capacity is the most events the store holds.
func (s *Store) Capacity() int { return s.capacity }

// Len is how many events the store holds now.
func (s *Store) Len() int { return s.n }

// At returns the i'th held event, oldest first. i must be in [0, Len()).
func (s *Store) At(i int) *Event { return s.ring[(s.head+i)%len(s.ring)] }

// Dropped is how many events have been pushed out by newer ones since the
// store was made or last cleared.
func (s *Store) Dropped() int64 { return s.dropped }

// OldestID is the ID of the oldest held event, or the ID the next event will
// get when empty; every event with a smaller ID is gone.
func (s *Store) OldestID() uint64 {
	if s.n == 0 {
		return s.nextID + 1
	}
	return s.At(0).ID
}

// Add appends events in order, numbering each, and returns the stored copies
// and whether any carried a field or action no earlier event had (the column
// set grew). An Add beyond capacity keeps only the newest and counts the rest
// as dropped.
func (s *Store) Add(events ...Event) (added []*Event, newColumns bool) {
	added = make([]*Event, 0, len(events))
	for i := range events {
		e := new(events[i])
		s.nextID++
		e.ID = s.nextID
		for _, f := range e.Fields {
			if !s.seenFields[f.Name] {
				s.seenFields[f.Name] = true
				s.fields = append(s.fields, f.Name)
				newColumns = true
			}
		}
		for _, a := range e.Actions {
			if !s.seenActions[a.Name] {
				s.seenActions[a.Name] = true
				s.actions = append(s.actions, a.Name)
				newColumns = true
			}
		}
		s.push(e)
		added = append(added, e)
	}
	// Only what is still held: an oversized Add pushed its own first events
	// out, and a view given those would show rows the store no longer has.
	if len(added) > s.n {
		added = added[len(added)-s.n:]
	}
	return added, newColumns
}

// push appends e, dropping the oldest event when full. The ring grows by
// doubling up to capacity: most sessions hold a few hundred events.
func (s *Store) push(e *Event) {
	if s.n == s.capacity {
		s.ring[s.head] = e
		s.head = (s.head + 1) % len(s.ring)
		s.dropped++
		return
	}
	if s.n == len(s.ring) {
		grown := make([]*Event, min(s.capacity, max(64, 2*len(s.ring))))
		for i := range s.n {
			grown[i] = s.At(i)
		}
		s.ring, s.head = grown, 0
	}
	s.ring[(s.head+s.n)%len(s.ring)] = e
	s.n++
}

// Clear drops every held event and resets the dropped count. The column
// registry is kept (as SSMS's Clear Data keeps the layout).
func (s *Store) Clear() {
	s.ring, s.head, s.n, s.dropped = nil, 0, 0, 0
}

// Columns is every column the store's events have carried: the name and
// timestamp, then the fields and then the actions, each in first-seen order.
// The package is left out: it belongs to the details pane.
func (s *Store) Columns() []Column {
	out := make([]Column, 0, 2+len(s.fields)+len(s.actions))
	out = append(out, NameColumn, TimestampColumn)
	for _, f := range s.fields {
		out = append(out, Column{Kind: ColField, Name: f})
	}
	for _, a := range s.actions {
		out = append(out, Column{Kind: ColAction, Name: a})
	}
	return out
}
