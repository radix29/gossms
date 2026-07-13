package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptPasswordRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	cases := []string{"s3cr3t!", "with spaces and 'quotes'", "unicode: пароль 密码"}
	for _, plaintext := range cases {
		enc, err := encryptPassword(key, plaintext)
		if err != nil {
			t.Fatalf("encryptPassword(%q): %v", plaintext, err)
		}
		if enc == plaintext {
			t.Errorf("encryptPassword(%q) returned the plaintext unchanged", plaintext)
		}
		got := decryptPassword(key, enc)
		if got != plaintext {
			t.Errorf("decryptPassword(encryptPassword(%q)) = %q, want %q", plaintext, got, plaintext)
		}
	}
}

func TestEncryptEmptyPassword(t *testing.T) {
	key := make([]byte, 32)
	enc, err := encryptPassword(key, "")
	if err != nil {
		t.Fatalf("encryptPassword(\"\"): %v", err)
	}
	if enc != "" {
		t.Errorf("encryptPassword(\"\") = %q, want \"\"", enc)
	}
}

func TestDecryptEmptyString(t *testing.T) {
	key := make([]byte, 32)
	if got := decryptPassword(key, ""); got != "" {
		t.Errorf("decryptPassword(key, \"\") = %q, want \"\"", got)
	}
}

func TestDecryptGarbageReturnsEmpty(t *testing.T) {
	key := make([]byte, 32)
	cases := []string{"not base64!!", "aGVsbG8=", "AAAA"}
	for _, garbage := range cases {
		if got := decryptPassword(key, garbage); got != "" {
			t.Errorf("decryptPassword(key, %q) = %q, want \"\" (garbage should fail closed)", garbage, got)
		}
	}
}

func TestDecryptWithWrongKeyReturnsEmpty(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = 0xFF
	}

	enc, err := encryptPassword(key1, "s3cr3t!")
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	if got := decryptPassword(key2, enc); got != "" {
		t.Errorf("decryptPassword with wrong key = %q, want \"\" (GCM auth should fail)", got)
	}
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
