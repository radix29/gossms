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
