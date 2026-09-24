package tui

import (
	"strconv"
	"strings"
	"testing"
)

// nameMatch's in-place ASCII search must answer exactly what lower-casing the
// name and searching it did — match, and partial (not at the start) — for
// ASCII and non-ASCII names alike.
func TestNameMatchAgreesWithLowerThenIndex(t *testing.T) {
	names := []string{"", "Orders", "CustomerOrders", "ORDERLINES", "orders", "ord", "or",
		"Überweisung", "straße_ORD", "İstanbulOrd", "x_Ord_y", "Z", "[dbo]", "a.b"}
	frags := []string{"", "ord", "o", "orders", "z", "über", "ß", "_ord", "ordz", "a.b", "[d"}
	for _, n := range names {
		for _, f := range frags {
			ref := strings.Index(strings.ToLower(n), f)
			ok, partial := nameMatch(n, f)
			if ok != (ref >= 0) || partial != (ref > 0) {
				t.Errorf("nameMatch(%q, %q) = %v, %v; lower-then-index says %d", n, f, ok, partial, ref)
			}
		}
	}
}

// BenchmarkNameMatch50k is one keystroke's worth of matching against a
// 50,000-object catalog: 15 ms and 50,000 allocations when each name was
// lower-cased first.
func BenchmarkNameMatch50k(b *testing.B) {
	names := make([]string, 50000)
	for i := range names {
		names[i] = "CustomerOrderLines_" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, nm := range names {
			nameMatch(nm, "ord")
		}
	}
}
