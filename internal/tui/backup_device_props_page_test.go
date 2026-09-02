package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Backup Device Properties, driven through fakedb_test.go.
//
// The device under test is the second of three, so a page that ignored its
// selection and read whichever row sorts first cannot pass. The by-name read
// is scoped with arg: and placed before the list read, because gosmo's
// BackupDeviceByNameContext query also contains "FROM   sys.backup_devices"
// and responses match by substring in order.

const backupDeviceUnderTest = "NightlyDev"

func backupDeviceResponses() []fakeResponse {
	return []fakeResponse{
		{
			match: "FROM   sys.backup_devices",
			arg:   backupDeviceUnderTest,
			cols:  3,
			rows: [][]driver.Value{
				{backupDeviceUnderTest, "DISK", `C:\Backups\nightly.bak`},
			},
		},
		{
			match: "FROM   sys.backup_devices",
			cols:  3,
			rows: [][]driver.Value{
				{"AAA_First", "DISK", `C:\Backups\first.bak`},
				{backupDeviceUnderTest, "DISK", `C:\Backups\nightly.bak`},
				{"ZZZ_Last", "DISK", `C:\Backups\last.bak`},
			},
		},
	}
}

// headerOnlyResponse scripts RESTORE HEADERONLY. Its columns are named
// because gosmo scans that result set by name, not by position.
func headerOnlyResponse(rows ...[]driver.Value) fakeResponse {
	names := []string{
		"BackupName", "BackupType", "Position", "DatabaseName", "ServerName",
		"BackupFinishDate", "BackupSize",
	}
	return fakeResponse{
		match:    "RESTORE HEADERONLY",
		cols:     len(names),
		colNames: names,
		rows:     rows,
	}
}

func staticValue(t *testing.T, f *propsheet.Form, label string) string {
	t.Helper()
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == sheetLabel(label) {
			return sr.Value()
		}
	}
	t.Fatalf("no static row labelled %q on this page", label)
	return ""
}

func firstGrid(t *testing.T, f *propsheet.Form) *controls.DataGrid {
	t.Helper()
	for _, r := range f.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			return gr.Grid
		}
	}
	t.Fatalf("no grid row on this page")
	return nil
}

// The page must show the device it was opened on, not the first row in
// sys.backup_devices.
func TestBackupDeviceGeneralLoadsTheSelectedDevice(t *testing.T) {
	sc, inst := newFakeConn(t, backupDeviceResponses()...)
	form, apply := loadPage(t, pageBackupDeviceGeneral(sc, backupDeviceUnderTest), inst)

	if got := staticValue(t, form, "Device name"); got != backupDeviceUnderTest {
		t.Errorf("Device name is %q, want the selected device's %q", got, backupDeviceUnderTest)
	}
	if got := staticValue(t, form, "Destination"); got != `C:\Backups\nightly.bak` {
		t.Errorf("Destination is %q", got)
	}
	if got := staticValue(t, form, "Type"); got != "DISK" {
		t.Errorf("Type is %q", got)
	}
	// A backup device has no ALTER at all; an apply here could only build a
	// statement SQL Server has no form of.
	if apply != nil {
		t.Error("the General page has an apply, but sp_addumpdevice/sp_dropdevice are a device's whole write surface")
	}
}

// Media Contents must read the backup sets through the *logical device*.
// Reading by physical path goes behind the alias the page is about, and a
// device whose path this login cannot resolve then reports an empty medium.
func TestBackupDeviceMediaContentsReadsTheLogicalDevice(t *testing.T) {
	finish := time.Date(2026, 9, 1, 2, 30, 0, 0, time.UTC)
	sc, inst := newFakeConn(t, append(backupDeviceResponses(),
		headerOnlyResponse(
			[]driver.Value{"Nightly full", int64(1), int64(1), "HealthClinic", "win10cli", finish, int64(4354048)},
			[]driver.Value{"Nightly log", int64(2), int64(2), "HealthClinic", "win10cli", finish, int64(65536)},
		))...)

	form, _ := loadPage(t, pageBackupDeviceMediaContents(sc, backupDeviceUnderTest), inst)

	reads := inst.Reads("RESTORE HEADERONLY")
	if len(reads) != 1 {
		t.Fatalf("the page issued %d media reads, want 1: %v", len(reads), reads)
	}
	if want := "RESTORE HEADERONLY FROM [" + backupDeviceUnderTest + "]"; reads[0] != want {
		t.Errorf("media read is %q, want %q", reads[0], want)
	}

	grid := firstGrid(t, form)
	var rows [][]string
	for i := 0; grid.Row(i) != nil; i++ {
		rows = append(rows, grid.Row(i))
	}
	if len(rows) != 2 {
		t.Fatalf("the grid holds %d backup sets, want 2", len(rows))
	}
	if got := staticValue(t, form, "Backup sets"); got != "2" {
		t.Errorf("Backup sets says %q", got)
	}
	// BackupType 2 is a log backup; a page reporting every set as "Database"
	// would still fill the grid.
	if !strings.Contains(strings.Join(rows[1], "|"), "Transaction Log") {
		t.Errorf("the log backup is not named as one: %v", rows[1])
	}
	if !strings.Contains(strings.Join(rows[0], "|"), "HealthClinic") {
		t.Errorf("row 0 does not name the database it came from: %v", rows[0])
	}
}

// Opening the media is the one thing on this page that touches the outside
// world: a missing file, an offline tape or an unreachable share must not
// fail the whole page, because none of that makes the device unreadable.
func TestBackupDeviceMediaContentsSurvivesAnUnreadableMedium(t *testing.T) {
	// No RESTORE HEADERONLY response is scripted, so the read fails.
	sc, inst := newFakeConn(t, backupDeviceResponses()...)

	page := pageBackupDeviceMediaContents(sc, backupDeviceUnderTest)
	form, _, err := page.load(t.Context())
	if err != nil {
		t.Fatalf("an unreadable medium failed the page: %v", err)
	}
	if got := staticValue(t, form, "Media"); got != `C:\Backups\nightly.bak` {
		t.Errorf("Media is %q — the page lost what the catalog knows", got)
	}
	// And the page says so rather than showing an empty grid, which reads as a
	// device holding no backups.
	for _, r := range form.Rows() {
		if _, ok := r.(*propsheet.GridRow); ok {
			t.Error("the page drew a backup-set grid for media it could not read")
		}
	}
	_ = inst
}

// The two pages are read-only by nature, not by omission, and
// prop_page_requires_test.go's pagesThatOnlyRead only permits that for a page
// with no apply at all.
func TestBackupDevicePagesDoNotWrite(t *testing.T) {
	sc, inst := newFakeConn(t, append(backupDeviceResponses(),
		headerOnlyResponse([]driver.Value{"Nightly full", int64(1), int64(1), "HealthClinic", "win10cli", time.Now(), int64(1024)}))...)

	for _, page := range backupDevicePropPages(sc, backupDeviceUnderTest) {
		_, apply := loadPage(t, page, inst)
		if apply != nil {
			t.Errorf("page %q has an apply", page.title)
		}
	}
	for _, q := range inst.Statements() {
		if strings.Contains(q, "sp_addumpdevice") || strings.Contains(q, "sp_dropdevice") {
			t.Errorf("loading Backup Device Properties wrote: %q", q)
		}
	}
}
