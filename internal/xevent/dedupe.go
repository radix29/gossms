package xevent

import (
	"hash/fnv"
	"time"
)

// Deduper picks the new events out of successive whole reads of a ring_buffer,
// which has no cursor: every read returns everything still in it.
//
// The key is the event_sequence action where the session collects it (unique
// per session). Otherwise it is the timestamp, name and a hash of every field
// and action (the fallback D1 records), and events identical in all of those
// are indistinguishable. They are counted rather than collapsed, so a read
// holding the same event twice where the last held it once yields one new copy.
type Deduper struct {
	prev map[dedupeKey]int

	// Unsequenced is set once a read has held an event without
	// event_sequence: the fallback key is in use, and the status line says so.
	Unsequenced bool
}

type dedupeKey struct {
	seq  uint64
	ts   time.Time
	name string
	hash uint64
}

// New returns the events of batch that were not in the previous batch, in
// batch order, and remembers batch as the previous one. The first call
// returns all of batch.
func (d *Deduper) New(batch []Event) []Event {
	cur := make(map[dedupeKey]int, len(batch))
	var out []Event
	for i := range batch {
		k := d.key(&batch[i])
		cur[k]++
		if d.prev[k] > 0 {
			d.prev[k]--
			continue
		}
		out = append(out, batch[i])
	}
	d.prev = cur
	return out
}

// Reset forgets the previous batch, so the next read counts as a first one
// (after Clear).
func (d *Deduper) Reset() { d.prev = nil }

func (d *Deduper) key(e *Event) dedupeKey {
	if e.Seq != 0 {
		return dedupeKey{seq: e.Seq}
	}
	d.Unsequenced = true
	h := fnv.New64a()
	for _, vs := range [][]Value{e.Fields, e.Actions} {
		for _, v := range vs {
			h.Write([]byte(v.Name))
			h.Write([]byte{0})
			h.Write([]byte(v.Value))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return dedupeKey{ts: e.Timestamp, name: e.Name, hash: h.Sum64()}
}
