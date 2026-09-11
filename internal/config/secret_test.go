package config

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testConn is a saved connection with pwd as its password; its identity is the
// AAD.
func testConn(pwd string) Connection {
	return Connection{
		Name: "prod", Server: "sql-prod", Port: 1433, Database: "app",
		AuthMethod: AuthSQLServer, User: "sa", Password: pwd,
		Encrypt: EncryptMandatory,
	}
}

func TestEncryptDecryptPasswordRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	cases := []string{"s3cr3t!", "with spaces and 'quotes'", "unicode: пароль 密码"}
	for _, plaintext := range cases {
		c := testConn(plaintext)
		enc, err := encryptPassword(key, c)
		if err != nil {
			t.Fatalf("encryptPassword(%q): %v", plaintext, err)
		}
		if enc == plaintext {
			t.Errorf("encryptPassword(%q) returned the plaintext unchanged", plaintext)
		}
		c.Password = enc
		got, ok := decryptPassword(key, c)
		if !ok || got != plaintext {
			t.Errorf("decryptPassword(encryptPassword(%q)) = (%q, %v), want (%q, true)",
				plaintext, got, ok, plaintext)
		}
	}
}

func TestEncryptEmptyPassword(t *testing.T) {
	key := make([]byte, 32)
	enc, err := encryptPassword(key, testConn(""))
	if err != nil {
		t.Fatalf("encryptPassword(\"\"): %v", err)
	}
	if enc != "" {
		t.Errorf("encryptPassword(\"\") = %q, want \"\"", enc)
	}
}

func TestDecryptEmptyString(t *testing.T) {
	key := make([]byte, 32)
	// An empty stored password is not a failed open: ok is true, so Save
	// re-seals normally.
	got, ok := decryptPassword(key, testConn(""))
	if !ok || got != "" {
		t.Errorf("decryptPassword(key, \"\") = (%q, %v), want (\"\", true)", got, ok)
	}
}

func TestDecryptGarbageReturnsEmpty(t *testing.T) {
	key := make([]byte, 32)
	cases := []string{"not base64!!", "aGVsbG8=", "AAAA", aadPrefix + "not base64!!", aadPrefix + "AAAA"}
	for _, garbage := range cases {
		got, ok := decryptPassword(key, testConn(garbage))
		if ok || got != "" {
			t.Errorf("decryptPassword(key, %q) = (%q, %v), want (\"\", false) — garbage should fail closed and report it",
				garbage, got, ok)
		}
	}
}

func TestDecryptWithWrongKeyReturnsEmpty(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = 0xFF
	}

	c := testConn("s3cr3t!")
	enc, err := encryptPassword(key1, c)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	c.Password = enc
	got, ok := decryptPassword(key2, c)
	if ok || got != "" {
		t.Errorf("decryptPassword with wrong key = (%q, %v), want (\"\", false) (GCM auth should fail)", got, ok)
	}
}

// A sealed password must not open under a different connection identity;
// otherwise someone with write access to config.json could move it onto an
// entry aimed at their own host.
func TestPasswordCiphertextIsBoundToItsConnection(t *testing.T) {
	key := make([]byte, 32)
	orig := testConn("s3cr3t!")
	enc, err := encryptPassword(key, orig)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}

	for _, c := range []struct {
		label  string
		mutate func(Connection) Connection
	}{
		{"retargeted server", func(c Connection) Connection { c.Server = "attacker-host"; return c }},
		{"different user", func(c Connection) Connection { c.User = "other"; return c }},
		{"different auth method", func(c Connection) Connection { c.AuthMethod = AuthWindows; return c }},
		{"different port", func(c Connection) Connection { c.Port = 14330; return c }},
		// Transport settings: changing any downgrades or redirects the
		// connection.
		{"encryption turned down", func(c Connection) Connection { c.Encrypt = EncryptOptional; return c }},
		{"certificate trusted", func(c Connection) Connection { c.TrustServerCertificate = true; return c }},
		{"host name in certificate", func(c Connection) Connection { c.HostNameInCertificate = "attacker-host"; return c }},
		{"extra properties", func(c Connection) Connection { c.ExtraProperties = "failoverpartner=attacker-host"; return c }},
	} {
		t.Run(c.label, func(t *testing.T) {
			moved := c.mutate(orig)
			moved.Password = enc
			got, ok := decryptPassword(key, moved)
			if ok || got != "" {
				t.Errorf("decryptPassword after %s = (%q, %v), want (\"\", false) — the ciphertext was not bound to its connection",
					c.label, got, ok)
			}
		})
	}
}

// Renaming a connection or pointing it at another database on the same server
// is an ordinary edit; neither is in the AAD, so the password survives both.
// (Port is bound since v3: it picks the instance.)
func TestPasswordSurvivesNameAndDatabaseEdits(t *testing.T) {
	key := make([]byte, 32)
	orig := testConn("s3cr3t!")
	enc, err := encryptPassword(key, orig)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}

	edited := orig
	edited.Name = "production (renamed)"
	edited.Database = "reporting"
	edited.Password = enc
	got, ok := decryptPassword(key, edited)
	if !ok || got != "s3cr3t!" {
		t.Errorf("decryptPassword after a rename/database edit = (%q, %v), want (%q, true)", got, ok, "s3cr3t!")
	}
}

// A pre-binding value (no aadPrefix) must still decrypt, or upgrading empties
// every saved password.
func TestLegacyUnboundPasswordStillDecrypts(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 3)
	}
	const plaintext = "legacy-s3cr3t"

	legacy, err := sealLegacyForTest(key, plaintext)
	if err != nil {
		t.Fatalf("sealLegacyForTest: %v", err)
	}
	if strings.HasPrefix(legacy, aadPrefix) {
		t.Fatalf("legacy fixture should not carry %q", aadPrefix)
	}

	c := testConn(legacy)
	got, ok := decryptPassword(key, c)
	if !ok || got != plaintext {
		t.Errorf("decryptPassword(legacy) = (%q, %v), want (%q, true)", got, ok, plaintext)
	}

	// Re-sealing moves it to the bound format.
	c.Password = plaintext
	reSealed, err := encryptPassword(key, c)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	if !strings.HasPrefix(reSealed, aadPrefix) {
		t.Errorf("re-sealed password = %q, want the %q prefix", reSealed, aadPrefix)
	}
}

// A v2 value (bound to server/user/auth method) must open and re-seal as v3. v2
// didn't bind transport settings; that closes at the next Save.
func TestV2PasswordStillDecryptsAndResealsAsV3(t *testing.T) {
	key := make([]byte, 32)
	c := testConn("")
	sealed, err := sealV2ForTest(key, c, "v2-s3cr3t")
	if err != nil {
		t.Fatalf("sealV2ForTest: %v", err)
	}
	c.Password = sealed
	if got, ok := decryptPassword(key, c); !ok || got != "v2-s3cr3t" {
		t.Fatalf("decryptPassword(v2) = (%q, %v), want (v2-s3cr3t, true)", got, ok)
	}

	// Still bound to what v2 bound.
	moved := c
	moved.Server = "attacker-host"
	if got, ok := decryptPassword(key, moved); ok || got != "" {
		t.Errorf("v2 value opened under another server: (%q, %v)", got, ok)
	}

	// A v2 ciphertext relabelled "v3:" must not open: the prefix picks the AAD.
	relabelled := c
	relabelled.Password = aadPrefix + strings.TrimPrefix(sealed, aadPrefixV2)
	if got, ok := decryptPassword(key, relabelled); ok || got != "" {
		t.Errorf("v2 ciphertext opened as v3: (%q, %v)", got, ok)
	}

	c.Password = "v2-s3cr3t"
	resealed, err := encryptPassword(key, c)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	if !strings.HasPrefix(resealed, aadPrefix) {
		t.Errorf("re-sealed = %q, want the %q prefix", resealed, aadPrefix)
	}
}

// An entry with no Encrypt reads back from disk as Optional; seal and open must
// agree, or the password is unreadable after one round trip.
func TestEmptyEncryptModeSealsAsOptional(t *testing.T) {
	key := make([]byte, 32)
	c := testConn("s3cr3t!")
	c.Encrypt = ""
	enc, err := encryptPassword(key, c)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	c.Password, c.Encrypt = enc, EncryptOptional
	if got, ok := decryptPassword(key, c); !ok || got != "s3cr3t!" {
		t.Errorf("decryptPassword with Encrypt read back as Optional = (%q, %v), want (s3cr3t!, true)", got, ok)
	}
}

// sealV2ForTest produces the v2 format: "v2:" + base64(nonce||ciphertext) with
// the server/user/auth-method AAD.
func sealV2ForTest(key []byte, c Connection, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), connectionAADv2(c))
	return aadPrefixV2 + base64.StdEncoding.EncodeToString(sealed), nil
}

// sealLegacyForTest produces the pre-binding format: base64(nonce||ciphertext),
// no AAD or prefix.
func sealLegacyForTest(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

func TestLoadOrCreateKeyGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()

	key1, err := loadOrCreateKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("len(key1) = %d, want 32", len(key1))
	}

	if _, err := os.Stat(filepath.Join(dir, keyFileName)); err != nil {
		t.Fatalf("key file was not created: %v", err)
	}

	key2, err := loadOrCreateKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateKey (second call): %v", err)
	}
	if string(key1) != string(key2) {
		t.Error("loadOrCreateKey returned a different key on the second call; it should reuse the persisted one")
	}
}

func TestLoadOrCreateKeyDifferentDirsGetDifferentKeys(t *testing.T) {
	key1, err := loadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}
	key2, err := loadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}
	if string(key1) == string(key2) {
		t.Error("two independently generated keys collided; RNG or generation logic is broken")
	}
}

// The key is written atomically so a crash can't leave a short key that the
// next run rejects with passwords already encrypted under it. Atomicity isn't
// observable here; what is: no temp file left behind and owner-only
// permissions.
func TestLoadOrCreateKeyLeavesOnlyTheKeyFileBehind(t *testing.T) {
	dir := t.TempDir()

	if _, err := loadOrCreateKey(dir); err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != keyFileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory holds %v, want just [%s] — a temp file was left behind", names, keyFileName)
	}

	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", info.Mode().Perm())
	}
	if info.Size() != 32 {
		t.Errorf("key file is %d bytes, want 32", info.Size())
	}
}

// A malformed existing key file is an error, not a reason to generate a new one
// — overwriting it would destroy the only way to decrypt saved passwords.
func TestLoadOrCreateKeyRejectsWrongSizedKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, keyFileName)
	truncated := []byte("only-sixteen-byt")
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadOrCreateKey(dir); err == nil {
		t.Fatal("loadOrCreateKey() = nil error, want a refusal for a wrong-sized key file")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(truncated) {
		t.Errorf("key file was rewritten (%d bytes), want it left untouched for manual recovery", len(after))
	}
}
