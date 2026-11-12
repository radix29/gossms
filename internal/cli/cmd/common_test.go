package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func TestResolveObject(t *testing.T) {
	ctx := context.Background()
	srv := newCatalogServer(t, "app",
		&gosmo.ObjectInfo{ObjectID: 1, Schema: "Sales", Name: "Customer", Type: "U", TypeDesc: "USER_TABLE"},
		&gosmo.ObjectInfo{ObjectID: 2, Schema: "dbo", Name: "Customer", Type: "U", TypeDesc: "USER_TABLE"},
		&gosmo.ObjectInfo{ObjectID: 3, Schema: "Sales", Name: "Order", Type: "U", TypeDesc: "USER_TABLE"},
		&gosmo.ObjectInfo{ObjectID: 4, Schema: "Sales", Name: "CustomerArchive", Type: "U", TypeDesc: "USER_TABLE"},
	).Server()
	d, err := srv.DatabaseByName(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}

	if o, err := resolveObject(ctx, d, ObjectName{Schema: "Sales", Name: "Customer"}); err != nil || o.ObjectID != 1 {
		t.Errorf("two-part: %+v, %v", o, err)
	}
	if o, err := resolveObject(ctx, d, ObjectName{Name: "Order"}); err != nil || o.ObjectID != 3 {
		t.Errorf("one-part, one schema: %+v, %v", o, err)
	}

	_, err = resolveObject(ctx, d, ObjectName{Name: "Customer"})
	e, _ := err.(*output.Error)
	if e == nil || e.Code != output.CodeUsageError || !strings.Contains(e.Message, "[Sales].[Customer], [dbo].[Customer]") {
		t.Errorf("one-part, two schemas: %v, want a usage error listing both", err)
	}

	// A case-only miss — what a _CS_ database answers — suggests the catalog's
	// spelling.
	_, err = resolveObject(ctx, d, ObjectName{Schema: "Sales", Name: "customer"})
	e, _ = err.(*output.Error)
	if e == nil || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, "[Sales].[Customer]") {
		t.Errorf("case-only miss: %v (hint %q), want NOT_FOUND suggesting [Sales].[Customer]", err, e.Hint)
	}
	if !strings.Contains(e.Message, "[Sales].[customer]") || !strings.Contains(e.Message, "[app]") {
		t.Errorf("not-found message %q, want the name as typed and the database", e.Message)
	}

	_, err = resolveObject(ctx, d, ObjectName{Name: "Nothing"})
	e, _ = err.(*output.Error)
	if e == nil || e.Code != output.CodeNotFound || strings.Contains(e.Hint, "Did you mean") {
		t.Errorf("no match: %v, want NOT_FOUND with no suggestion", err)
	}
}

func TestReadError(t *testing.T) {
	sqlErr := func(number int32) error {
		return fmt.Errorf("gosmo: read: %w", mssql.Error{Number: number, Class: 14, Message: "boom"})
	}
	for _, tt := range []struct {
		name string
		err  error
		want output.Code
	}{
		{"not found", fmt.Errorf("gosmo: x: %w", gosmo.ErrNotFound), output.CodeNotFound},
		{"unsupported", fmt.Errorf("gosmo: x: %w", gosmo.ErrUnsupported), output.CodeUnsupported},
		{"unsupported version", fmt.Errorf("gosmo: x: %w", gosmo.ErrUnsupportedVersion), output.CodeUnsupported},
		{"permission denied", sqlErr(229), output.CodePermissionDenied},
		{"missing or denied", sqlErr(15151), output.CodeNotFound},
		{"other SQL error", sqlErr(8134), output.CodeSQLError},
		{"passes an output error through", output.NewError(output.CodeBlocked, "x", ""), output.CodeBlocked},
		{"anything else", errors.New("odd"), output.CodeGeneralError},
	} {
		if got := readError(tt.err).Code; got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
	// A read's permission error gets a read's hint, not query run's.
	if h := readError(sqlErr(229)).Hint; !strings.Contains(h, "this read needs") || strings.Contains(h, "script") {
		t.Errorf("permission hint %q", h)
	}
}

// connectDatabase reads the connection's database from the server and puts
// its catalog spelling in the envelope.
func TestConnectDatabase(t *testing.T) {
	tree := &spec.Tree{Commands: []*spec.Command{{Path: []string{"x"}, Options: ConnectOptions()}}}
	inv, err := args.Parse(tree, []string{"x", "--server", "srv", "--auth", "windows", "-d", "app"}, args.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The session's database as -d spelt it; the catalog's is App.
	session := "app"
	s := newCatalogServerWith(t, "App", func(c *catalogServer) {
		c.on(func(gosmotest.Query) [][]any { return [][]any{{session}} }, "SELECT DB_NAME()")
	})
	d := Deps{
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			return s.conn(ctx)
		},
	}
	res := &output.Result{}
	sc, dbase, err := d.connectDatabase(context.Background(), inv, res)
	if err != nil {
		t.Fatal(err)
	}
	sc.Close()
	if dbase.Name != "App" || res.Database != "App" || res.Server != "srv" {
		t.Errorf("database %q, envelope %q on %q", dbase.Name, res.Database, res.Server)
	}

	session = "gone"
	if _, _, err := d.connectDatabase(context.Background(), inv, &output.Result{}); readError(err).Code != output.CodeNotFound {
		t.Errorf("missing database: %v, want NOT_FOUND", err)
	}
}
