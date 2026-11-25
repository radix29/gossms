//go:build livedb

// Live check of --param's code-page check (checkCodePage) against the
// session database's collation: tempdb's (the server's, CP1252 on the test
// instances) and a throwaway UTF-8 database, which it creates and drops.
//
//	go test -tags livedb ./internal/cli/cmd/ -run TestLiveParamCodePage -v \
//	  -live-server win10cli -live-user sa -live-password PASS
//
// Skipped without the flags (live_query_writes_test.go declares them).
package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func TestLiveParamCodePage(t *testing.T) {
	if *liveServer == "" || *liveUser == "" {
		t.Skip("no -live-server/-live-user given")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connect := func(database string) *db.ServerConn {
		sc, err := db.Connect(ctx, config.Connection{Server: *liveServer, User: *liveUser, Password: *livePassword,
			Database: database, TrustServerCertificate: true, Encrypt: config.EncryptMandatory}, db.RoleCLI)
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	check := func(sc *db.ServerConn, raw, want string) {
		t.Helper()
		p, err := parseParam(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		err = checkCodePage(ctx, sc.Server, []param{p})
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", raw, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: got %v, want a refusal containing %q", raw, err, want)
		}
	}

	temp := connect("tempdb")
	defer temp.Close()
	var collation string
	if err := temp.Server.DB().QueryRowContext(ctx,
		"SELECT CAST(DATABASEPROPERTYEX('tempdb', 'Collation') AS nvarchar(128))").Scan(&collation); err != nil {
		t.Fatal(err)
	}
	if cp, err := temp.Server.CollationCodePage(ctx, collation); err != nil || cp != 1252 {
		t.Skipf("tempdb's collation %s is code page %d (%v), not 1252", collation, cp, err)
	}
	check(temp, "v=Жук:varchar(10)", "'Ж' (U+0416) is not in the database's code page 1252")
	check(temp, "v=é:varchar(10)", "")
	check(temp, "v=Café:varchar(4)", "")

	name := fmt.Sprintf("zz_gossms_param_%d", time.Now().UnixNano())
	pool := temp.Server.DB()
	if _, err := pool.ExecContext(ctx, "CREATE DATABASE ["+name+"] COLLATE Latin1_General_100_CI_AS_SC_UTF8"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		drop := context.Background()
		pool.ExecContext(drop, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		if _, err := pool.ExecContext(drop, "DROP DATABASE ["+name+"]"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	}()
	utf8db := connect(name)
	defer utf8db.Close()
	check(utf8db, "v=éé:varchar(3)", "4 bytes in code page 65001 do not fit varchar(3)")
	check(utf8db, "v=éé:varchar(4)", "")
	check(utf8db, "v=Жук:varchar(10)", "")
}
