package cmd

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// sysschobjs is a type-S system base table: ObjectByName finds it in
// sys.all_objects, but sys.tables never lists it.
var sysschobjs = &gosmo.ObjectInfo{ObjectID: 34, Schema: "sys", Name: "sysschobjs", Type: "S",
	TypeDesc: "SYSTEM_TABLE", IsMSShipped: true}

func TestResolveObjectFindsASystemBaseTable(t *testing.T) {
	ctx := context.Background()
	d, err := newCatalogServer(t, "app", sysschobjs).Server().DatabaseByName(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	o, err := resolveObject(ctx, d, ObjectName{Schema: "sys", Name: "sysschobjs"})
	if err != nil || o.Type != "S" {
		t.Fatalf("resolveObject = %+v, %v", o, err)
	}
	if k := kindOfDesc(o.TypeDesc); k != kindSystemTable {
		t.Errorf("kindOfDesc(SYSTEM_TABLE) = %q, want %q", k, kindSystemTable)
	}
}

// TestTableCommandsRefuseASystemBaseTable pins B5: a command that reads a
// table refuses a system base table up front, rather than resolving it and
// then failing TableByName's sys.tables read with a not-found.
func TestTableCommandsRefuseASystemBaseTable(t *testing.T) {
	for _, argv := range []string{
		"object describe sys.sysschobjs",
		"object indexes sys.sysschobjs",
		"object stats sys.sysschobjs",
		"data top sys.sysschobjs",
		"data sample sys.sysschobjs",
		"data count sys.sysschobjs",
		"data profile sys.sysschobjs",
		"maint index-rebuild sys.sysschobjs --allow ddl",
		"maint update-stats sys.sysschobjs --allow ddl",
	} {
		t.Run(argv, func(t *testing.T) {
			s := newCatalogServer(t, "app", sysschobjs)
			d := Deps{
				LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
				Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
					return s.conn(ctx)
				},
			}
			var cmds []*spec.Command
			cmds = append(cmds, ObjectCommands(d)...)
			cmds = append(cmds, DataCommands(d)...)
			cmds = append(cmds, MaintCommands(d)...)
			fields := strings.Fields(argv)
			argv0 := append(fields[:3:3], append([]string{"--server", "srv", "--auth", "windows"}, fields[3:]...)...)
			inv, perr := args.Parse(&spec.Tree{Commands: cmds}, argv0, args.Options{})
			if perr != nil {
				t.Fatalf("parse: %v", perr)
			}
			_, err := inv.Command.Run(context.Background(), inv)
			e, _ := err.(*output.Error)
			if e == nil || e.Code != output.CodeUsageError ||
				e.Message != "[sys].[sysschobjs] is a system base table, which SQL Server exposes only over the dedicated admin connection" {
				t.Errorf("err = %v, want the system-base-table usage error", err)
			}
		})
	}
}
