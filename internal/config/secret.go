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

	"github.com/radix29/gossms/internal/fileutil"
)

// Password protection
//
// Saved passwords are AES-256-GCM encrypted and base64-encoded on disk
// (encryptPassword/decryptPassword). Load/Save in config.go call these
// transparently, so the rest of the app only sees plaintext
// Connection.Password in memory.
//
// The key is a random 256-bit value generated on first run, stored in its own
// file (keyFileName) next to config.json, 0600 where the OS honors that, so a
// leak of config.json alone doesn't expose the passwords.
//
// This does not protect against full read access to the user's home directory,
// where both files are readable; an OS secret store (Credential Manager,
// Keychain, Secret Service) would close that gap.
//
// It does defend against *write* access becoming exfiltration. Every password
// is sealed with additional authenticated data naming its connection (see
// connectionAAD): the ciphertext is valid only for that server, port, user,
// auth method, transport settings and Entra tenant/client. Otherwise someone
// able to edit config.json could copy the production password onto an entry
// pointing at their own host and have gossms dial out with it, never
// decrypting anything. GCM verifies the AAD on open, so a moved or retargeted
// entry fails to decrypt.

// keyFileName is the file (in the same directory as config.json) holding
// the random AES-256 key used to encrypt saved passwords.
const keyFileName = "gossms.key"

// loadOrCreateKey reads the encryption key from dir, generating and
// persisting a new random 256-bit key on first run.
//
// A key file that exists but isn't 32 bytes is an error, not a reason to
// generate a fresh one: overwriting it would destroy the only thing that can
// decrypt the passwords in config.json. Failing leaves the file in place to be
// restored or removed by hand; Load treats that as "no saved passwords" and
// loads everything else.
func loadOrCreateKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, keyFileName)
	if key, err := readKey(path); !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Atomic and flushed, as for config.json (see fileutil.WriteAtomic), with
	// more at stake: Save creates the key before the ciphertext that depends on
	// it, so a crash leaving config.json durable and gossms.key absent or short
	// hits readKey's "expected 32" refusal next run, and every saved password
	// is lost for good.
	//
	// Create-if-absent, not replace: two instances starting on a fresh profile
	// both get here. A replacing write would leave the later key on disk while
	// the earlier instance keeps sealing with its own, making everything it
	// saves undecryptable. The loser adopts the winner's key.
	created, err := fileutil.CreateAtomic(path, key, 0o600)
	if err != nil {
		return nil, err
	}
	if !created {
		return readKey(path)
	}
	return key, nil
}

// readKey reads an existing key file; see loadOrCreateKey for why a
// wrong-sized one is an error.
func readKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	switch {
	case err != nil:
		return nil, err
	case len(key) != 32:
		return nil, fmt.Errorf("config: key file %s is %d bytes, expected 32 — "+
			"restore it from a backup or delete it to start over (saved passwords will be lost)",
			path, len(key))
	}
	return key, nil
}

// aadPrefix marks a password sealed with the current binding, connectionAAD.
// Values behind aadPrefixV3 or aadPrefixV2, or unprefixed, are earlier formats,
// still readable (see decryptPassword); Save rewrites them under aadPrefix, and
// the app saves at startup when Load opened one (Config.NeedsReseal).
const (
	aadPrefix   = "v4:"
	aadPrefixV3 = "v3:"
	aadPrefixV2 = "v2:"
)

// connectionAAD is the additional authenticated data binding a sealed password
// to its connection: the fields deciding where it is sent and how it is
// protected.
//   - Server, Port, User, AuthMethod pick the destination.
//   - Encrypt, TrustServerCertificate, HostNameInCertificate and
//     ExtraProperties decide whether TLS is present and checked. Binding them
//     stops write access to config.json from turning Encrypt off (or
//     Trust-certificate on) and dialling with the saved password intact, a
//     downgrade needing no decryption. ExtraProperties reaches the driver: a
//     "protocol" or "failoverpartner" there can redirect the dial.
//   - TenantID and ClientID are the destination for Entra methods that send
//     the secret to Entra (Password, Service Principal). Other methods leave
//     them empty, so binding them for every method is free.
//
// Name and Database are excluded: relabelling or retargeting the database is an
// ordinary edit and must not invalidate the password.
//
// NUL-separated so ("a","bc") and ("ab","c") differ.
func connectionAAD(c Connection) []byte {
	return fmt.Appendf(nil, "gossms-connection-v4\x00%s\x00%d\x00%s\x00%d\x00%s\x00%t\x00%s\x00%s\x00%s\x00%s",
		c.Server, c.Port, c.User, int(c.AuthMethod),
		boundEncrypt(c), c.TrustServerCertificate, c.HostNameInCertificate, c.ExtraProperties,
		c.TenantID, c.ClientID)
}

// boundEncrypt is c.Encrypt as the AAD binds it. "" is what an entry built in
// memory carries and UnmarshalJSON reads back as Optional; binding the two
// differently would seal a password the next Load cannot open.
func boundEncrypt(c Connection) EncryptMode {
	if c.Encrypt == "" {
		return EncryptOptional
	}
	return c.Encrypt
}

// connectionAADv3 is the AAD passwords were sealed with before the Entra
// tenant and client were bound (aadPrefixV3). Only ever used to open one.
func connectionAADv3(c Connection) []byte {
	return fmt.Appendf(nil, "gossms-connection-v3\x00%s\x00%d\x00%s\x00%d\x00%s\x00%t\x00%s\x00%s",
		c.Server, c.Port, c.User, int(c.AuthMethod),
		boundEncrypt(c), c.TrustServerCertificate, c.HostNameInCertificate, c.ExtraProperties)
}

// connectionAADv2 is the AAD passwords were sealed with before transport
// settings were bound (aadPrefixV2). Only ever used to open one.
func connectionAADv2(c Connection) []byte {
	return fmt.Appendf(nil, "gossms-connection-v2\x00%s\x00%s\x00%d",
		c.Server, c.User, int(c.AuthMethod))
}

// encryptPassword AES-256-GCM-encrypts c's password under key, bound to c via
// connectionAAD, with the random nonce prepended, base64-encoded behind
// aadPrefix. An empty password encrypts to "".
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
// that doesn't decode and authenticate (hand-edited JSON, a replaced key file,
// a ciphertext moved from another connection) returns ("", false). A password
// stored empty is ("", true): nothing failed.
//
// The bool prevents data loss: with "" alone, Save re-encrypted the empty
// string over the ciphertext that failed to open, destroying a copy a restored
// key file or undone hand-edit could still recover. Load keeps the original
// bytes for a false (Connection.sealed) and Save writes them back untouched.
//
// aadPrefixV3 predates tenant/client binding and opens with the v3 AAD;
// aadPrefixV2 predates transport binding and opens with the v2 AAD; no prefix
// predates connection binding and opens without AAD. Existing configs thus
// survive each upgrade and the startup save (Config.NeedsReseal) rewrites them
// under the current binding. Those values are what the newer binding defends
// against, so the fallback is the price of not discarding every saved password
// once; it narrows as configs are rewritten. Downgrades don't work: an older
// release reads a newer prefix as unprefixed, fails to open it and keeps it
// sealed (Connection.sealed), so the password is re-entered until the newer
// release runs again.
func decryptPassword(key []byte, c Connection) (string, bool) {
	encoded := c.Password
	if encoded == "" {
		return "", true
	}
	var aad []byte
	if rest, ok := strings.CutPrefix(encoded, aadPrefix); ok {
		encoded, aad = rest, connectionAAD(c)
	} else if rest, ok := strings.CutPrefix(encoded, aadPrefixV3); ok {
		encoded, aad = rest, connectionAADv3(c)
	} else if rest, ok := strings.CutPrefix(encoded, aadPrefixV2); ok {
		encoded, aad = rest, connectionAADv2(c)
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
