package cmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// volumesCatalog answers server volumes' one read.
type volumesCatalog struct {
	Catalog
	vols   []gosmo.FileVolume
	hidden bool
	err    error
}

func (c *volumesCatalog) FileVolumes(context.Context) ([]gosmo.FileVolume, bool, error) {
	if c.err != nil {
		return nil, false, c.err
	}
	if c.hidden {
		return nil, false, nil
	}
	return c.vols, true, nil
}

const gib = 1024 * 1024 * 1024

func newVolumesCatalog() *volumesCatalog {
	return &volumesCatalog{vols: []gosmo.FileVolume{
		{DatabaseID: 1, FileID: 1, PhysicalName: "/var/opt/mssql/data/master.mdf", MountPoint: "/var/opt/mssql/data",
			TotalBytes: 500 * gib, AvailableBytes: 92 * gib},
		{DatabaseID: 1, FileID: 2, PhysicalName: "/var/opt/mssql/log/mastlog.ldf", MountPoint: "/var/opt/mssql/log",
			TotalBytes: 200 * gib, AvailableBytes: 10 * gib},
		// The same volume read a moment later: free moved, still one row
		// with the first file's figures.
		{DatabaseID: 5, FileID: 1, PhysicalName: "/var/opt/mssql/data/app.mdf", MountPoint: "/var/opt/mssql/data",
			TotalBytes: 500 * gib, AvailableBytes: 91 * gib},
		// A label only.
		{DatabaseID: 5, FileID: 2, PhysicalName: `D:\Data\app_log.ldf`, VolumeName: "Data",
			TotalBytes: 100 * gib, AvailableBytes: 50 * gib},
		// Named neither way: grouped by figures, shown by the lowest path
		// case-insensitively, as the TUI's MIN(physical_name).
		{DatabaseID: 6, FileID: 1, PhysicalName: "/x/HR.mdf", TotalBytes: 10 * gib, AvailableBytes: 5 * gib},
		{DatabaseID: 6, FileID: 2, PhysicalName: "/x/b.mdf", TotalBytes: 10 * gib, AvailableBytes: 5 * gib},
		{DatabaseID: 6, FileID: 3, PhysicalName: "/x/a.ldf", TotalBytes: 10 * gib, AvailableBytes: 5 * gib},
		// Azure MI's nonsense figures: left out with a warning.
		{DatabaseID: 7, FileID: 1, PhysicalName: `C:\x.mdf`, MountPoint: `C:\`, TotalBytes: 192 << 20, AvailableBytes: 65344 << 20},
		{DatabaseID: 7, FileID: 2, PhysicalName: `C:\x.ldf`, MountPoint: `C:\`},
	}}
}

// runServerVolumes runs server volumes over cat with argv after it.
func runServerVolumes(t *testing.T, cat Catalog, argv string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	tree := &spec.Tree{Commands: ServerCommands(d)}
	inv, err := args.Parse(tree, append([]string{"server", "volumes", "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...), args.Options{})
	if err != nil {
		t.Fatalf("%q: %v", argv, err)
	}
	return inv.Command.Run(context.Background(), inv)
}

func TestServerVolumes(t *testing.T) {
	res, err := runServerVolumes(t, newVolumesCatalog(), "--unit gb")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var names []string
	for _, c := range set.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"volume", "total_gb", "free_gb", "free_pct", "db_files"}; !slices.Equal(names, want) {
		t.Errorf("columns %q, want %q", names, want)
	}
	for col, want := range map[string][]string{
		"volume":   {"/var/opt/mssql/data", "/var/opt/mssql/log", "/x/a.ldf", "Data"},
		"total_gb": {"500.00", "200.00", "10.00", "100.00"},
		"free_gb":  {"92.00", "10.00", "5.00", "50.00"},
		"free_pct": {"18.40", "5.00", "50.00", "50.00"},
		"db_files": {"2", "1", "3", "1"},
	} {
		if got := column(set, col); !slices.Equal(got, want) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	w := strings.Join(res.Warnings, "\n")
	if len(res.Warnings) != 2 || !strings.Contains(w, "2 database file(s) left out") ||
		!strings.Contains(w, "volume /var/opt/mssql/log: 5.00 % free") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// --unit kb prints integer KB; the default is MB.
func TestServerVolumesUnit(t *testing.T) {
	for argv, want := range map[string][2]string{
		"":          {"total_mb", "512000.00"},
		"--unit kb": {"total_kb", "524288000"},
	} {
		res, err := runServerVolumes(t, newVolumesCatalog(), argv)
		if err != nil {
			t.Fatal(err)
		}
		if got := column(res.Sets[0], want[0]); len(got) == 0 || got[0] != want[1] {
			t.Errorf("%q: %s %q, want %s first", argv, want[0], got, want[1])
		}
	}
}

// Without VIEW SERVER STATE — asked first, or refused by the DMV — the
// command fails, exit 9, with a hint naming the permission (D7).
func TestServerVolumesNoViewServerState(t *testing.T) {
	hidden := newVolumesCatalog()
	hidden.hidden = true
	refused := newVolumesCatalog()
	refused.err = mssql.Error{Number: 300, Class: 14, Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}
	for name, cat := range map[string]*volumesCatalog{"hidden": hidden, "refused": refused} {
		_, err := runServerVolumes(t, cat, "")
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "VIEW SERVER STATE") {
			t.Errorf("%s: %v, want PERMISSION_DENIED naming VIEW SERVER STATE", name, err)
		}
	}
	other := newVolumesCatalog()
	other.err = errors.New("gosmo: file volumes: boom")
	if _, err := runServerVolumes(t, other, ""); err == nil {
		t.Error("a failed read succeeded")
	}
}

// No database file on a plausible volume: an empty set, no error.
func TestServerVolumesEmpty(t *testing.T) {
	res, err := runServerVolumes(t, &volumesCatalog{}, "")
	if err != nil || len(res.Sets[0].Rows) != 0 || len(res.Warnings) != 0 {
		t.Errorf("rows %v, warnings %q, err %v", res.Sets[0].Rows, res.Warnings, err)
	}
}
