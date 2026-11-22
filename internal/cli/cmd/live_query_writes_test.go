//go:build livedb

// Live check of --max-rows-affected's server-side target check against the
// two cases that commit over the limit without it (docs/cli.md §
// Transactions): an ON DELETE CASCADE foreign key and a SET NOCOUNT ON
// trigger. It creates a throwaway database and drops it.
//
//	go test -tags livedb ./internal/cli/cmd/ -run TestLiveWriteTargets -v \
//	  -live-server win10cli -live-user sa -live-password PASS
//
// Skipped without the flags.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

var (
	liveServer   = flag.String("live-server", "", "instance for the live tests")
	liveUser     = flag.String("live-user", "", "login for -live-server")
	livePassword = flag.String("live-password", "", "password for -live-user")
)

func TestLiveWriteTargets(t *testing.T) {
	if *liveServer == "" || *liveUser == "" {
		t.Skip("no -live-server/-live-user given")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sc, err := db.Connect(ctx, config.Connection{Server: *liveServer, User: *liveUser, Password: *livePassword,
		TrustServerCertificate: true, Encrypt: config.EncryptMandatory}, db.RoleCLI)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	pool := sc.Server.DB()
	name := fmt.Sprintf("zz_gossms_writes_%d", time.Now().UnixNano())
	if _, err := pool.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		drop := context.Background()
		pool.ExecContext(drop, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		if _, err := pool.ExecContext(drop, "DROP DATABASE ["+name+"]"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	}()
	for _, q := range []string{
		"CREATE TABLE dbo.p (id INT PRIMARY KEY, a INT)",
		"CREATE TABLE dbo.c (id INT PRIMARY KEY, p INT CONSTRAINT fk_c_p REFERENCES dbo.p (id) ON DELETE CASCADE)",
		"CREATE TABLE dbo.au (id INT PRIMARY KEY, a INT)",
		"CREATE TABLE dbo.au_log (n INT)",
		"CREATE TRIGGER dbo.tr_au ON dbo.au AFTER UPDATE AS SET NOCOUNT ON; INSERT dbo.au_log SELECT 1",
		"CREATE TABLE dbo.plain (id INT PRIMARY KEY)",
	} {
		if _, err := pool.ExecContext(ctx, "USE ["+name+"]; EXEC (N'"+strings.ReplaceAll(q, "'", "''")+"')"); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	p := safety.Policy{Allow: spec.ClassWrite, Transaction: true, MaxRowsAffected: true}
	for _, tc := range []struct {
		script, want string // want: the refusal's statements, "" for none
	}{
		{"DELETE [" + name + "].dbo.p WHERE id = 1",
			"DELETE [" + name + "].dbo.p (line 1): foreign key [fk_c_p] on [dbo].[c] is ON DELETE CASCADE"},
		{"UPDATE [" + name + "].dbo.au SET a = 1",
			"UPDATE [" + name + "].dbo.au (line 1): [dbo].[au] has trigger [dbo].[tr_au]"},
		{"UPDATE [" + name + "].dbo.p SET a = 1", ""},
		{"INSERT [" + name + "].dbo.au VALUES (1, 1)", ""},
		{"USE [" + name + "]\nDELETE dbo.plain", ""},
		{"USE [" + name + "]\nDELETE dbo.nope", "DELETE dbo.nope (line 2): no table by that name in"},
	} {
		sc2 := safety.Classify(tc.script)
		if b := safety.Check(sc2, p); b != nil {
			t.Fatalf("%q: refused offline: %s", tc.script, b.Message)
		}
		b, err := checkWriteTargets(ctx, sc.Server, sc2, tc.script, p)
		if err != nil {
			t.Fatalf("%q: %v", tc.script, err)
		}
		switch {
		case tc.want == "" && b != nil:
			t.Errorf("%q: refused: %s", tc.script, b.Describe())
		case tc.want != "" && (b == nil || !strings.HasPrefix(b.Describe(), tc.want)):
			t.Errorf("%q: refusal %v, want %q", tc.script, b, tc.want)
		}
	}
}
