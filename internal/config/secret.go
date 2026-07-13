package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
)

// Password protection
//
// Saved connections' passwords are AES-256-GCM encrypted and base64-encoded
// before ever touching disk (see encryptPassword/decryptPassword below).
// Load/Save in config.go call these transparently, so the rest of the app
// — the Connect dialog, autofill from a server-match, BuildConnectionString
// — only ever sees plaintext Connection.Password in memory; only the JSON
// file itself is encrypted.
//
// The encryption key is a random 256-bit value generated on first run and
// stored in its own file (keyFileName) next to config.json, 0600
// (owner-only) where the OS honors that — so a leak of config.json alone
// doesn't expose the passwords.
//
// This does not protect against an attacker with full read access to this
// user's profile/home directory, since both files are readable there; an
// OS-backed secret store (Windows Credential Manager, macOS Keychain,
// Linux Secret Service) would close that gap.

// keyFileName is the file (in the same directory as config.json) holding
// the random AES-256 key used to encrypt saved passwords.
const keyFileName = "gossms.key"

// loadOrCreateKey reads the encryption key from dir, generating and
// persisting a new random 256-bit key on first run.
func loadOrCreateKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, keyFileName)
	if key, err := os.ReadFile(path); err == nil && len(key) == 32 {
		return key, nil
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptPassword AES-256-GCM-encrypts plaintext under key, prepends the
// random nonce GCM needs for decryption, and base64-encodes the result
// for JSON storage. An empty plaintext encrypts to "".
func encryptPassword(key []byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptPassword reverses encryptPassword. Anything that doesn't decode
// and authenticate cleanly — a blank value, unencrypted legacy data,
// hand-edited JSON, or a replaced key file — decrypts to "" rather than
// erroring or returning garbage.
func decryptPassword(key []byte, encoded string) string {
	if encoded == "" {
		return ""
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	if len(sealed) < gcm.NonceSize() {
		return ""
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ""
	}
	return string(plaintext)
}
