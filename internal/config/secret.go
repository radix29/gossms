package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
//
// It does defend against *write* access being turned into exfiltration.
// Every password is sealed with additional authenticated data naming the
// connection it belongs to (see connectionAAD), so a ciphertext is only valid
// for that server/user/auth-method triple. Without it, the blobs are freely
// transplantable: someone able to edit config.json could copy the production
// password onto an entry pointing at a host they control and have gossms dial
// out with it, never needing to decrypt anything. GCM verifies the AAD on
// open, so a moved or retargeted entry fails to decrypt instead.

// keyFileName is the file (in the same directory as config.json) holding
// the random AES-256 key used to encrypt saved passwords.
const keyFileName = "gossms.key"

// loadOrCreateKey reads the encryption key from dir, generating and
// persisting a new random 256-bit key on first run.
//
// A key file that exists but isn't 32 bytes is an error, not a reason to
// generate a fresh one: overwriting it would permanently destroy the only
// thing that can decrypt the passwords already in config.json, turning a
// recoverable truncated write into unrecoverable data loss. Failing here
// instead leaves the file in place to be restored or removed by hand —
// Load treats that as "no saved passwords" and still loads everything else.
func loadOrCreateKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, keyFileName)
	switch key, err := os.ReadFile(path); {
	case err == nil && len(key) == 32:
		return key, nil
	case err == nil:
		return nil, fmt.Errorf("config: key file %s is %d bytes, expected 32 — "+
			"restore it from a backup or delete it to start over (saved passwords will be lost)",
			path, len(key))
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
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

// aadPrefix marks a stored password sealed with connection-binding AAD.
// Values without it are the original unbound format and are still readable
// (see decryptPassword) — Save rewrites them bound on the next write.
const aadPrefix = "v2:"

// connectionAAD is the additional authenticated data binding a sealed
// password to the connection it belongs to: the fields that decide where the
// password would be sent. Name and Database are deliberately excluded —
// relabelling a connection or pointing it at a different database on the same
// server is an ordinary edit and must not invalidate the stored password.
//
// NUL-separated so a value ending where the next begins can't be confused
// with a different split of the same bytes ("a" + "bc" vs "ab" + "c").
func connectionAAD(c Connection) []byte {
	return []byte(fmt.Sprintf("gossms-connection-v2\x00%s\x00%s\x00%d",
		c.Server, c.User, int(c.AuthMethod)))
}

// encryptPassword AES-256-GCM-encrypts c's password under key, binding it to
// c via connectionAAD, prepends the random nonce GCM needs for decryption,
// and base64-encodes the result for JSON storage behind aadPrefix. An empty
// password encrypts to "".
func encryptPassword(key []byte, c Connection) (string, error) {
	if c.Password == "" {
		return "", nil
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(c.Password), connectionAAD(c))
	return aadPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptPassword reverses encryptPassword for c's stored password. Anything
// that doesn't decode and authenticate cleanly — hand-edited JSON, a replaced
// key file, or a ciphertext moved here from a different connection — comes
// back as ("", false) rather than erroring or returning garbage. A password
// that was stored empty in the first place is ("", true): nothing failed.
//
// The bool is what stops a failed open from becoming permanent data loss.
// Returning only "" made Save re-encrypt the empty string over the very
// ciphertext that failed, destroying the one copy of a password that a
// restored key file or an undone hand-edit could still have recovered. Load
// keeps the original bytes for a false (see Connection.sealed) and Save
// writes them back untouched.
//
// A value without aadPrefix predates connection binding and is opened without
// AAD, so an existing config keeps working across the upgrade; the next Save
// rewrites it bound. Such a value is exactly what binding defends against, so
// this fallback is the cost of not silently discarding every saved password
// once — it narrows as configs get rewritten.
func decryptPassword(key []byte, c Connection) (string, bool) {
	encoded := c.Password
	if encoded == "" {
		return "", true
	}
	var aad []byte
	if rest, ok := strings.CutPrefix(encoded, aadPrefix); ok {
		encoded, aad = rest, connectionAAD(c)
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", false
	}
	if len(sealed) < gcm.NonceSize() {
		return "", false
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", false
	}
	return string(plaintext), true
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
