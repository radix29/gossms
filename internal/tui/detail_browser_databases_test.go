package tui

import "testing"

func TestFormatMB(t *testing.T) {
	cases := []struct {
		mb   float64
		want string
	}{
		{0, "0 MB"},
		{8, "8 MB"},
		{123456.7, "123,457 MB"},
		{100423.90234375, "100,424 MB"},
		{7.523437, "8 MB"},
		{0.4, "0 MB"},
	}
	for _, c := range cases {
		if got := formatMB(c.mb); got != c.want {
			t.Errorf("formatMB(%v) = %q, want %q", c.mb, got, c.want)
		}
	}
}

// The Audits folder's Maximum file size spelled its MB with a bare FormatInt
// ("10240 MB") while every other size column used formatMB ("10,240 MB") —
// review 2026-11-12 S2. 0 is the catalog's UNLIMITED.
func TestAuditFileSizeTextUsesFormatMB(t *testing.T) {
	for _, c := range []struct {
		mb   int64
		want string
	}{
		{0, "Unlimited"},
		{10240, "10,240 MB"},
		{2, "2 MB"},
	} {
		if got := auditFileSizeText(c.mb); got != c.want {
			t.Errorf("auditFileSizeText(%d) = %q, want %q", c.mb, got, c.want)
		}
	}
}
