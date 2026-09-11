package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// A long server or database name used to fill the autocomplete list's width,
// so the user — what tells two matches for one server apart — was clipped off.
func TestMatchLabelClipsServerAndDatabase(t *testing.T) {
	cases := []struct {
		c    config.Connection
		want string
	}{
		{config.Connection{Server: "srv", Database: "db", User: "sa"}, "srv,1433,db,sa"},
		{config.Connection{Server: "sql01.corp.example.com", Port: 1444, Database: "AdventureWorks2022", User: "sa"},
			"sql01.corp.exa…,1444,Adventure…,sa"},
		{config.Connection{Server: "123456789012345", Database: "1234567890", User: "sa"},
			"123456789012345,1433,1234567890,sa"},
	}
	for _, c := range cases {
		if got := matchLabel(c.c); got != c.want {
			t.Errorf("matchLabel(%q, %q) = %q, want %q", c.c.Server, c.c.Database, got, c.want)
		}
	}
}
