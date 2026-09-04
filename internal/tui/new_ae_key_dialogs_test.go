package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"
)

// The two Always Encrypted create dialogs, driven through their real prefetch,
// preflight and apply closures against the scripted fake instance.
//
// What is worth pinning here is where the write lands and what it carries: a
// key created in the wrong database is a key nobody finds, and a signature or
// encrypted value that loses its bytes on the way through the hex field
// produces a key the server accepts and no client can ever use.

// aeKeyResponses scripts one database holding two column master keys and one
// column encryption key.
func aeKeyResponses() []fakeResponse {
	return []fakeResponse{
		// Before the list read, so the by-name read behind it resolves to
		// appdb rather than to whichever row the list answer sorts first.
		{match: "FROM sys.databases", arg: "appdb", cols: 8, rows: [][]driver.Value{
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		// The encryption-key answer goes first: its query joins
		// sys.column_master_keys, so behind the master-key answer it is served
		// six columns and the read fails with a scan error rather than a miss.
		{match: "sys.column_encryption_keys", cols: 5, rows: [][]driver.Value{
			{"CEK_existing", int64(1), "CMK_old", "RSA_OAEP", []byte{0x01}},
		}},
		{match: "sys.column_master_keys", cols: 6, rows: [][]driver.Value{
			{"CMK_old", int64(1), "MSSQL_CERTIFICATE_STORE", "CurrentUser/My/AAAA", false, nil},
			{"CMK_new", int64(2), "MSSQL_CERTIFICATE_STORE", "CurrentUser/My/BBBB", false, nil},
		}},
	}
}

func TestNewColumnMasterKeyDialogCreatesTheKey(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnMasterKeyDialog(a)
	sc, inst := newFakeConn(t, aeKeyResponses()...)
	node := &explorerNode{data: nodeData{Type: NodeColumnMasterKeys, DBName: "appdb"}}

	d.show(sc, node)
	waitAndDrain(t, a)
	form := d.forms[0]
	if form == nil {
		t.Fatal("the prefetch did not build the General page")
	}

	editText(t, form, "Name", "CMK_test")
	editText(t, form, "Key store provider", "AZURE_KEY_VAULT")
	editText(t, form, "Key path", "https://vault/keys/k1/v1")

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// StatementsIn, not Statements: which database the key was created in is
	// half of what the statement means, and the bare USE that says so is
	// stripped from Statements.
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements in appdb = %q, want exactly the CREATE", stmts)
	}
	got := stmts[0]
	for _, want := range []string{
		"CREATE COLUMN MASTER KEY [CMK_test]",
		"KEY_STORE_PROVIDER_NAME = N'AZURE_KEY_VAULT'",
		"KEY_PATH = N'https://vault/keys/k1/v1'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("statement is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ENCLAVE_COMPUTATIONS") {
		t.Errorf("the enclave clause appeared with the box unticked:\n%s", got)
	}
	assertNoStatementsIn(t, inst, "master")
}

// The signature is the one field whose bytes cannot be recovered if the hex
// field mangles them, so the enclave path asserts the literal that reached the
// server rather than only that a statement ran.
func TestNewColumnMasterKeyDialogCarriesTheEnclaveSignature(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnMasterKeyDialog(a)
	sc, inst := newFakeConn(t, aeKeyResponses()...)
	d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnMasterKeys, DBName: "appdb"}})
	waitAndDrain(t, a)
	form := d.forms[0]

	editText(t, form, "Name", "CMK_enclave")
	editText(t, form, "Key path", "CurrentUser/My/CCCC")
	checkRow(t, form, "Allow enclave computations").Edit(true)
	editText(t, form, "Signature (hex)", "0x0aFF10")

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements in appdb = %q, want exactly the CREATE", stmts)
	}
	if !strings.Contains(stmts[0], "ENCLAVE_COMPUTATIONS (SIGNATURE = 0x0AFF10)") {
		t.Errorf("signature did not survive the hex field:\n%s", stmts[0])
	}
}

func TestNewColumnMasterKeyDialogPreflight(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnMasterKeyDialog(a)
	sc, inst := newFakeConn(t, aeKeyResponses()...)
	d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnMasterKeys, DBName: "appdb"}})
	waitAndDrain(t, a)
	form := d.forms[0]

	set := func(name, path, sig string, enclave bool) {
		textRow(t, form, "Name").Edit(name)
		textRow(t, form, "Key path").Edit(path)
		textRow(t, form, "Signature (hex)").Edit(sig)
		checkRow(t, form, "Allow enclave computations").Edit(enclave)
	}
	for _, c := range []struct {
		name               string
		keyName, path, sig string
		enclave            bool
		want               string
	}{
		{"no name", "", "p", "", false, "name is required"},
		// The prefetch's names are matched case-insensitively, the way SQL
		// Server's default collation compares them.
		{"duplicate name", "cmk_NEW", "p", "", false, "already exists"},
		{"no key path", "CMK_x", "", "", false, "key path is required"},
		{"enclave without a signature", "CMK_x", "p", "", true, "signature"},
		{"unparseable signature", "CMK_x", "p", "0xZZ", true, "hexadecimal"},
		{"odd-length signature", "CMK_x", "p", "0x0AF", true, "missing"},
		// A pasted signature with the box unticked must not be silently
		// dropped — the key created would not be the one being set up.
		{"signature without enclave", "CMK_x", "p", "0x0AFF", false, "tick that box"},
	} {
		t.Run(c.name, func(t *testing.T) {
			set(c.keyName, c.path, c.sig, c.enclave)
			err := d.preflight()
			if err == nil {
				t.Fatalf("preflight accepted it")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused dialog still executed %q", stmts)
	}
}

func TestNewColumnEncryptionKeyDialogCreatesTheKey(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnEncryptionKeyDialog(a)
	sc, inst := newFakeConn(t, aeKeyResponses()...)
	d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnEncryptionKeys, DBName: "appdb"}})
	waitAndDrain(t, a)
	form := d.forms[0]
	if form == nil {
		t.Fatal("the prefetch did not build the General page")
	}

	editText(t, form, "Name", "CEK_test")
	// The second master key, not the first: a dialog that ignored the
	// selection entirely would still pass on row 0.
	editSelect(t, form, "Column master key", "CMK_new")
	editText(t, form, "Encrypted value (hex)", "0x0102Ff")

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements in appdb = %q, want exactly the CREATE", stmts)
	}
	got := stmts[0]
	for _, want := range []string{
		"CREATE COLUMN ENCRYPTION KEY [CEK_test]",
		"COLUMN_MASTER_KEY = [CMK_new]",
		"ALGORITHM = 'RSA_OAEP'",
		"ENCRYPTED_VALUE = 0x0102FF",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("statement is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "CMK_old") {
		t.Errorf("the create used the first master key, not the selected one:\n%s", got)
	}
}

func TestNewColumnEncryptionKeyDialogPreflight(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnEncryptionKeyDialog(a)
	sc, inst := newFakeConn(t, aeKeyResponses()...)
	d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnEncryptionKeys, DBName: "appdb"}})
	waitAndDrain(t, a)
	form := d.forms[0]

	for _, c := range []struct {
		name, keyName, value, want string
	}{
		{"no name", "", "0x01", "name is required"},
		{"duplicate name", "cek_EXISTING", "0x01", "already exists"},
		{"no encrypted value", "CEK_x", "", "encrypted value is required"},
		{"unparseable value", "CEK_x", "0xZZ", "hexadecimal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			textRow(t, form, "Name").Edit(c.keyName)
			textRow(t, form, "Encrypted value (hex)").Edit(c.value)
			err := d.preflight()
			if err == nil {
				t.Fatal("preflight accepted it")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused dialog still executed %q", stmts)
	}
}

// A database with no column master key cannot hold a column encryption key at
// all, and the dropdown would otherwise be empty with no explanation.
func TestNewColumnEncryptionKeyDialogRefusesWithoutAMasterKey(t *testing.T) {
	a := newTestApp()
	d := NewNewColumnEncryptionKeyDialog(a)
	sc, _ := newFakeConn(t,
		fakeResponse{match: "FROM sys.databases", arg: "appdb", cols: 8, rows: [][]driver.Value{
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		fakeResponse{match: "sys.column_encryption_keys", cols: 5},
		fakeResponse{match: "sys.column_master_keys", cols: 6},
	)
	d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnEncryptionKeys, DBName: "appdb"}})
	waitAndDrain(t, a)

	editText(t, d.forms[0], "Name", "CEK_x")
	editText(t, d.forms[0], "Encrypted value (hex)", "0x01")
	err := d.preflight()
	if err == nil || !strings.Contains(err.Error(), "no column master key") {
		t.Errorf("preflight = %v, want a refusal naming the missing master key", err)
	}
}

func TestParseHexBytes(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    []byte
		wantErr string
	}{
		{in: "0x0AFF", want: []byte{0x0a, 0xff}},
		{in: "0X0aff", want: []byte{0x0a, 0xff}},
		{in: " 0aff ", want: []byte{0x0a, 0xff}},
		{in: "", wantErr: "no value"},
		{in: "0x", wantErr: "no value"},
		{in: "0x0af", wantErr: "missing"},
		{in: "0xzz", wantErr: "hexadecimal"},
	} {
		got, err := parseHexBytes(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("parseHexBytes(%q) error = %v, want it to mention %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHexBytes(%q): %v", c.in, err)
			continue
		}
		if string(got) != string(c.want) {
			t.Errorf("parseHexBytes(%q) = %x, want %x", c.in, got, c.want)
		}
	}
}

// B2: ENCLAVE_COMPUTATIONS is SQL Server 2019 syntax and the parser rejects the
// whole CREATE below that, so the dialog must not offer the two controls on an
// older instance — an ungated control that can only fail on Apply is what
// CLAUDE.md § Application rules rules out. The apply is exercised, not just the
// row list: a hidden checkbox that the apply still reads would send the clause
// anyway.
func TestNewColumnMasterKeyDialogHidesEnclavesBelow2019(t *testing.T) {
	for _, c := range []struct {
		name, version string
		wantRows      bool
	}{
		{"2016", "13.0.4001.0", false},
		{"2017", "14.0.2120.1", false},
		{"2019", "15.0.4123.1", true},
		{"2022", "16.0.4085.2", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			d := NewNewColumnMasterKeyDialog(a)
			sc, inst := newFakeConnAtVersion(t, c.version, aeKeyResponses()...)
			d.show(sc, &explorerNode{data: nodeData{Type: NodeColumnMasterKeys, DBName: "appdb"}})
			waitAndDrain(t, a)
			form := d.forms[0]
			if form == nil {
				t.Fatal("the prefetch did not build the General page")
			}

			labels := rowLabels(form)
			for _, l := range []string{"Allow enclave computations", "Signature (hex)"} {
				if got := slices.Contains(labels, sheetLabel(l)); got != c.wantRows {
					t.Errorf("%s draws %q = %v, want %v", c.name, l, got, c.wantRows)
				}
			}
			if !c.wantRows {
				// The Check row draws at full width, so its label is not padded.
				if slices.Contains(labels, "Allow enclave computations") {
					t.Errorf("%s draws the enclave checkbox", c.name)
				}
			}

			editText(t, form, "Name", "CMK_v")
			editText(t, form, "Key path", "CurrentUser/My/DDDD")
			if err := d.preflight(); err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if err := d.applyFns[0](context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.StatementsIn("appdb")
			if len(stmts) != 1 {
				t.Fatalf("statements in appdb = %q, want exactly the CREATE", stmts)
			}
			if strings.Contains(stmts[0], "ENCLAVE_COMPUTATIONS") {
				t.Errorf("%s sent the enclave clause with the box untouched:\n%s", c.name, stmts[0])
			}
		})
	}
}
