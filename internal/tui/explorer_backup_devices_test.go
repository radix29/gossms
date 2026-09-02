package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the Backup Devices family — the same
// per-family checklist the Credentials tests cover, one test per entry.

func TestServerObjectsFolderOffersBackupDevices(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeServerObjects](l, &explorerNode{data: nodeData{Type: NodeServerObjects, conn: sc}})
	if err != nil {
		t.Fatalf("loadServerObjectsChildren: %v", err)
	}
	// SSMS's order: Backup Devices, Endpoints, Linked Servers, Triggers.
	want := []string{"Backup Devices", "Endpoints", "Linked Servers", "Triggers"}
	got := make([]string, len(children))
	for i, c := range children {
		got[i] = c.label
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Server Objects folder children = %v, want %v", got, want)
	}
	if children[0].data.Type != NodeBackupDevices {
		t.Errorf("the Backup Devices folder has type %v", children[0].data.Type)
	}
}

func TestBackupDevicesFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeBackupDevices]; !ok {
		t.Fatal("NodeBackupDevices has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeBackupDevices) {
		t.Error("NodeBackupDevices is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeBackupDevice) {
		t.Error("NodeBackupDevice claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

func TestBackupDeviceLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeBackupDevice, style.s)
		if got == 0 {
			t.Errorf("%s: NodeBackupDevice has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeBackupDevice fell through to the default bullet", style.name)
		}
	}
}

func TestBackupDeviceTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeBackupDevice); got != "Backup Device" {
		t.Errorf("nodeTypeName(NodeBackupDevice) = %q, want %q", got, "Backup Device")
	}
}

func TestBackupDeviceScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeBackupDevice, "", "NightlyDev", ""))
	if len(items) == 0 {
		t.Fatal("a backup device offers no Script item")
	}
	if items[0].Label != "Script Backup Device as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	got := labelsOf(items[0].Sub)
	if !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeBackupDevice]
	if !ok {
		t.Fatal("NodeBackupDevice has no objectOps entry — Delete is not offered")
	}
	// The whole point of the entry is the @delfile choice: with drop set and
	// dropOption empty, deleting the device could never take the file with it
	// and the option would be unreachable from either surface.
	if op.dropWithOption == nil || op.dropOption == "" {
		t.Error("the backup device objectOp offers no 'delete the file too' option")
	}
	if op.drop != nil {
		t.Error("the backup device objectOp sets both drop and dropWithOption — deleteObject picks one path")
	}
	// A backup device cannot be renamed: sp_addumpdevice/sp_dropdevice are its
	// whole write surface, so a rename would fail on click.
	if op.rename != nil {
		t.Error("the backup device objectOp offers a rename SQL Server has no statement for")
	}
}

// @delfile is not a detail: without it the alias goes and the backup file
// stays, with it the file is deleted and nothing here can undo that. The
// checkbox has to reach the statement.
func TestBackupDeviceDropStatement(t *testing.T) {
	for _, tc := range []struct {
		name       string
		deleteFile bool
		want       string
	}{
		{"keep the file", false, `EXEC sp_dropdevice @logicalname = N'NightlyDev'`},
		{"delete the file", true, `EXEC sp_dropdevice @logicalname = N'NightlyDev', @delfile = N'DELFILE'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t)
			err := objectOps[NodeBackupDevice].dropWithOption(t.Context(), sc,
				nodeData{Type: NodeBackupDevice, Name: "NightlyDev"}, tc.deleteFile)
			if err != nil {
				t.Fatalf("drop: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 || stmts[0] != tc.want {
				t.Errorf("got %v, want [%s]", stmts, tc.want)
			}
		})
	}
}

// The Details pane reads gosmo independently of the tree, so it is its own
// chance to list the wrong thing.
func TestBackupDevicesFolderDetailListsEveryDevice(t *testing.T) {
	sc, _ := newFakeConn(t, fakeResponse{
		match: "FROM   sys.backup_devices",
		cols:  3,
		rows: [][]driver.Value{
			{"AAA_First", "DISK", `C:\Backups\first.bak`},
			{"NightlyDev", "DISK", `C:\Backups\nightly.bak`},
			{"OldTape", "TAPE", `\\.\tape0`},
		},
	})

	var objs []nodeData
	cols, rows, err := backupDevicesFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeBackupDevices}}, &objs)
	if err != nil {
		t.Fatalf("backupDevicesFolderDetail: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if len(objs) != len(rows) {
		t.Fatalf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
	}
	if objs[1].Name != "NightlyDev" || objs[1].Type != NodeBackupDevice {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	kind := slices.Index(cols, "Type")
	where := slices.Index(cols, "Physical Location")
	if kind < 0 || where < 0 {
		t.Fatalf("columns are %v", cols)
	}
	if rows[2][kind] != "TAPE" {
		t.Errorf("the tape device reports type %q", rows[2][kind])
	}
	// The path is the whole reason a device exists; a row without it names an
	// alias for nowhere.
	if !strings.Contains(rows[1][where], "nightly.bak") {
		t.Errorf("row 1 does not carry the physical path: %v", rows[1])
	}
}

// The suggested path is joined with the *server's* separator, not the client's
// — a Linux gossms naming a file for a Windows instance still writes a
// backslash, and a path with the wrong one is one SQL Server cannot open.
func TestBackupDevicePathUsesTheServersSeparator(t *testing.T) {
	cases := []struct {
		dir, name, want string
	}{
		{`C:\Backups`, "NightlyDev", `C:\Backups\NightlyDev.bak`},
		{`C:\Backups\`, "NightlyDev", `C:\Backups\NightlyDev.bak`},
		{"/var/opt/mssql/backup", "NightlyDev", "/var/opt/mssql/backup/NightlyDev.bak"},
		{"/var/opt/mssql/backup/", "NightlyDev", "/var/opt/mssql/backup/NightlyDev.bak"},
		{"", "NightlyDev", ""},
		{`C:\Backups`, "", ""},
	}
	for _, c := range cases {
		if got := backupDevicePath(c.dir, c.name); got != c.want {
			t.Errorf("backupDevicePath(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
}
