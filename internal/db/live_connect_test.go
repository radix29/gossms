//go:build livedb

// Live checks that the server actually sees the Connect dialog's settings; DSN
// tests prove only the string.
//
//	go test -tags livedb ./internal/db/ -run TestLiveConnect -v \
//	  -live-server win10cli.fritz.box -live-user sa -live-password PASS
//
// Skipped without the flags.
package db

import (
	"context"
	"errors"
	"flag"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
)

var liveServer = flag.String("live-server", "", "instance for the TestLiveConnect tests")

func liveConnectOpts(t *testing.T) config.Connection {
	t.Helper()
	if *liveServer == "" || *liveUser == "" {
		t.Skip("no -live-server/-live-user given")
	}
	return config.Connection{
		Server: *liveServer, User: *liveUser, Password: *livePassword,
		TrustServerCertificate: true, Encrypt: config.EncryptMandatory,
	}
}

// liveSession reads what the server records about sc's own session.
func liveSession(t *testing.T, sc *ServerConn) (program string, encrypted bool, packetSize int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var enc string
	err := sc.Server.DB().QueryRowContext(ctx, `
		SELECT s.program_name, c.encrypt_option, c.net_packet_size
		FROM sys.dm_exec_sessions s
		JOIN sys.dm_exec_connections c ON c.session_id = s.session_id
		WHERE s.session_id = @@SPID`).Scan(&program, &enc, &packetSize)
	if err != nil {
		t.Fatalf("read the session: %v", err)
	}
	return program, enc == "TRUE", packetSize
}

func TestLiveConnectReportsTheRoleAsProgramName(t *testing.T) {
	opts := liveConnectOpts(t)
	for role, want := range map[Role]string{
		RoleExplorer: "goSSMS", RoleQuery: "goSSMS - Query", RoleActivityMonitor: "goSSMS - Activity Monitor",
	} {
		sc, err := ConnectContext(context.Background(), opts, role)
		if err != nil {
			t.Fatalf("role %d: %v", role, err)
		}
		program, _, _ := liveSession(t, sc)
		sc.Close()
		if program != want {
			t.Errorf("role %d: program_name = %q, want %q", role, program, want)
		}
	}
}

// Optional encrypts the login only, Mandatory the session; encrypt_option is
// the server's record. Azure encrypts every session, so there Optional is only
// checked to connect.
func TestLiveConnectEncryptModeReachesTheServer(t *testing.T) {
	opts := liveConnectOpts(t)
	for mode, want := range map[config.EncryptMode]bool{
		config.EncryptOptional: false, config.EncryptMandatory: true,
	} {
		opts.Encrypt = mode
		sc, err := Connect(opts)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		_, encrypted, _ := liveSession(t, sc)
		azure := sc.Server.Info().IsAzure()
		sc.Close()
		if azure && mode == config.EncryptOptional {
			continue
		}
		if encrypted != want {
			t.Errorf("%s: encrypt_option = %v, want %v", mode, encrypted, want)
		}
	}
}

// BUG-2: an Extra Properties entry reaches the session. The driver defaults to
// 4096, so any other size came from here.
//
// Above 8192 and read allowing for TLS: an encrypted session reports size + 58
// bytes (4154 for 4096, on 17.0 and MI), the server caps 8192 to 8000, and on
// MI go-mssqldb can't finish TLS below 4096.
func TestLiveConnectExtraPropertiesReachTheServer(t *testing.T) {
	opts := liveConnectOpts(t)
	opts.Encrypt = config.EncryptOptional
	opts.ExtraProperties = "packet size=16000"
	sc, err := Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	_, encrypted, size := liveSession(t, sc)
	want := 16000
	if encrypted {
		want += 58
	}
	if size != want {
		t.Errorf("net_packet_size = %d (encrypted %v), want %d from Extra Properties", size, encrypted, want)
	}
}

// ARCH-3: cancelling ctx aborts a dial in flight. 10.255.255.1 is unroutable,
// so the dial hangs until stopped.
func TestLiveConnectCancelAbortsTheDial(t *testing.T) {
	opts := liveConnectOpts(t)
	opts.Server = "10.255.255.1"
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := ConnectContext(ctx, opts, RoleExplorer)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("connected to an unroutable address")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the cancelled dial took %v to return, want well under the %v timeout", elapsed, connectTimeout)
	}
}
