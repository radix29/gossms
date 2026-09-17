//go:build livedb

// Live verification of the Service Broker object ops, against a real SQL
// Server.
//
// What no unit test settles: whether each generated CREATE actually runs, and
// whether each Delete's statement is accepted for the object the tree names.
// Both are strings the fake driver accepts whatever they say.
//
//	go test -tags livedb ./internal/tui/ -run TestLiveServiceBrokerOps -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
//
// Skipped entirely without -livedb. Creates and drops its own throwaway
// database; touches nothing else.
package tui

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
)

func TestLiveServiceBrokerOps(t *testing.T) {
	sc, ctx := livePropConn(t)

	const dbName = "gossms_sbops_live"

	raw, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	mustExec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("exec %.60q: %v", q, err)
		}
	}
	inDB := func(stmt string) {
		t.Helper()
		mustExec(`USE [` + dbName + `]; EXEC sp_executesql N'` + strings.ReplaceAll(stmt, "'", "''") + `'`)
	}

	mustExec(`IF DB_ID('` + dbName + `') IS NOT NULL ALTER DATABASE [` + dbName + `] SET SINGLE_USER WITH ROLLBACK IMMEDIATE`)
	mustExec(`IF DB_ID('` + dbName + `') IS NOT NULL DROP DATABASE [` + dbName + `]`)
	mustExec(`CREATE DATABASE [` + dbName + `]`)
	defer func() {
		c := context.Background()
		raw.ExecContext(c, `ALTER DATABASE [`+dbName+`] SET SINGLE_USER WITH ROLLBACK IMMEDIATE`)
		if _, err := raw.ExecContext(c, `DROP DATABASE [`+dbName+`]`); err != nil {
			t.Errorf("drop %s: %v", dbName, err)
		}
	}()

	// Dependency order — message type, contract, queue, service, route,
	// binding, priority — which is also the order the drops have to come back
	// in. The names carry the "//" a real Service Broker name has, which is
	// what a missing quoteIdent fails on.
	for _, stmt := range []string{
		`CREATE SCHEMA sbsrc AUTHORIZATION dbo`,
		`CREATE SCHEMA sbtgt AUTHORIZATION dbo`,
		`CREATE MESSAGE TYPE [//live/Submit] VALIDATION = WELL_FORMED_XML`,
		`CREATE CONTRACT [//live/Contract] ([//live/Submit] SENT BY ANY)`,
		`CREATE QUEUE sbsrc.LiveQueue WITH STATUS = ON, RETENTION = OFF`,
		`CREATE SERVICE [//live/Service] ON QUEUE sbsrc.LiveQueue ([//live/Contract])`,
		`CREATE ROUTE [//live/Route] WITH SERVICE_NAME = '//live/Service', ADDRESS = 'TCP://remote:4022'`,
		`CREATE USER live_binding_user WITHOUT LOGIN`,
		`CREATE BROKER PRIORITY [//live/Priority] FOR CONVERSATION
		   SET (CONTRACT_NAME = [//live/Contract], PRIORITY_LEVEL = 8)`,
	} {
		inDB(stmt)
	}

	// The remote service binding is created apart from the rest, and its half
	// of the test dropped where the edition refuses one: Managed Instance
	// refuses CREATE REMOTE SERVICE BINDING with Msg 41906 at compile time,
	// which is what edition_gate.go withholds the verb for there. That is an
	// edition answer, not a failure of anything under test.
	bindingCreated := true
	if _, err := raw.ExecContext(ctx, `USE [`+dbName+`]; EXEC sp_executesql N'
		CREATE REMOTE SERVICE BINDING [//live/Binding] TO SERVICE ''//live/Remote''
		  WITH USER = live_binding_user, ANONYMOUS = OFF'`); err != nil {
		bindingCreated = false
		t.Logf("CREATE REMOTE SERVICE BINDING refused (expected on Managed Instance): %v", err)
	}

	leaves := []struct {
		typ    NodeType
		schema string
		name   string
	}{
		{NodeBrokerPriority, "", "//live/Priority"},
		{NodeRemoteServiceBinding, "", "//live/Binding"},
		{NodeRoute, "", "//live/Route"},
		{NodeBrokerService, "", "//live/Service"},
		{NodeBrokerQueue, "sbsrc", "LiveQueue"},
		{NodeContract, "", "//live/Contract"},
		{NodeMessageType, "", "//live/Submit"},
	}
	if !bindingCreated {
		leaves = slices.DeleteFunc(leaves, func(c struct {
			typ    NodeType
			schema string
			name   string
		}) bool {
			return c.typ == NodeRemoteServiceBinding
		})
	}

	// The generated CREATE of each family, taken before anything is dropped.
	// Running them is the last step: a DROP And CREATE round trip per object
	// cannot work here, because the server refuses to drop an object another
	// one is bound to (Msg 3716) — which is the whole reason the drops below
	// go in dependency order.
	creates := map[NodeType]string{}
	for _, c := range leaves {
		n := nodeData{Type: c.typ, DBName: dbName, Schema: c.schema, Name: c.name}
		text, err := scriptables[c.typ].verbs[0].gen(ctx, sc, n) // CREATE To
		if err != nil {
			t.Fatalf("%s: script CREATE: %v", c.name, err)
		}
		creates[c.typ] = text
		t.Logf("%s scripts as:\n%s", c.name, text)
	}

	// Move to Schema, the one transfer in the subtree.
	t.Run("the queue moves between schemas", func(t *testing.T) {
		n := nodeData{Type: NodeBrokerQueue, DBName: dbName, Schema: "sbsrc", Name: "LiveQueue"}
		if err := objectOps[NodeBrokerQueue].transfer(ctx, sc, n, "sbtgt"); err != nil {
			t.Fatalf("transfer to sbtgt: %v", err)
		}
		moved := n
		moved.Schema = "sbtgt"
		if err := objectOps[NodeBrokerQueue].transfer(ctx, sc, moved, "sbsrc"); err != nil {
			t.Fatalf("transfer back to sbsrc: %v", err)
		}
	})

	// Every Delete, in dependency order. A drop out of order is refused
	// Msg 3716 by design, which is what the confirmations warn about.
	t.Run("every Delete drops its object", func(t *testing.T) {
		for _, c := range leaves {
			n := nodeData{Type: c.typ, DBName: dbName, Schema: c.schema, Name: c.name}
			if err := objectOps[c.typ].drop(ctx, sc, n); err != nil {
				t.Errorf("drop %s: %v", c.name, err)
			}
		}
		var left int
		row := raw.QueryRowContext(ctx, `USE [`+dbName+`]; SELECT
			  (SELECT COUNT(*) FROM sys.service_message_types WHERE message_type_id >= 65536)
			+ (SELECT COUNT(*) FROM sys.service_contracts WHERE service_contract_id >= 65536)
			+ (SELECT COUNT(*) FROM sys.service_queues WHERE is_ms_shipped = 0)
			+ (SELECT COUNT(*) FROM sys.services WHERE service_id >= 65536)
			+ (SELECT COUNT(*) FROM sys.routes WHERE name <> 'AutoCreatedLocal')
			+ (SELECT COUNT(*) FROM sys.remote_service_bindings)
			+ (SELECT COUNT(*) FROM sys.conversation_priorities)`)
		if err := row.Scan(&left); err != nil {
			t.Fatalf("count what is left: %v", err)
		}
		if left != 0 {
			t.Errorf("%d user Service Broker objects survived the drops", left)
		}
	})

	// Every generated CREATE is run, in the order the objects depend on each
	// other — the reverse of the drops. A script that does not parse, or that
	// names a setting the server spells differently, fails here and nowhere
	// else.
	t.Run("every generated CREATE runs", func(t *testing.T) {
		for i := len(leaves) - 1; i >= 0; i-- {
			for _, batch := range strings.Split(creates[leaves[i].typ], "\nGO\n") {
				batch = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(batch), "GO"))
				if batch == "" {
					continue
				}
				inDB(batch)
			}
		}
	})
}
