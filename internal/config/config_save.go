package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/fileutil"
)

// Save writes the config. Passwords are AES-256-GCM encrypted and base64-encoded
// (see secret.go) on disk only; c keeps plaintext.
//
// It re-reads the file and applies this process's own changes to it (connections
// added or removed, settings changed since Load) rather than writing c whole,
// since another gossms instance may have saved meanwhile. Afterwards
// c.Connections is the merged list. Settings in c are not replaced by the file's:
// a changed setting takes effect when the Options dialog applies it.
//
// An entry whose password could not be opened keeps its ciphertext
// (Connection.sealed). An unusable key file does not stop the save: stored
// ciphertexts are written back as they are, and only a password that would need
// the key to seal is left out, named in the returned error.
//
// Save is BeginSave, Run and EndSave on one goroutine. The UI goroutine uses the
// three halves so the lock wait, key read and fsync run off it.
func (c *Config) Save() error {
	j := c.BeginSave()
	err := j.Run()
	c.EndSave(j)
	return err
}

// SaveJob is one Save split so its file work can run on another goroutine than
// the one that owns the Config: BeginSave snapshots what to write, Run does the
// re-read, merge and write touching nothing in the Config, EndSave adopts the
// result. Only one job per Config may be between BeginSave and EndSave —
// EndSave drops the ops the job carried by position.
type SaveJob struct {
	path       string
	unreadable error
	// settings is the Config's settings when the job began, and base the baseline
	// they are compared with; the maps they share with the Config are replaced
	// there, never written into (see XEventHiddenColumns).
	settings, base *Config
	ops            []connOp

	// merged is the connection list written, set once Run has written the
	// file; adopted is set by EndSave.
	merged           []Connection
	written, adopted bool
}

// BeginSave snapshots c for a SaveJob. Call it on the goroutine that owns c.
func (c *Config) BeginSave() *SaveJob {
	return &SaveJob{
		path:       configPath(),
		unreadable: c.unreadable,
		settings:   c.settingsSnapshot(),
		base:       c.base,
		ops:        slices.Clone(c.ops),
	}
}

// Run writes the job's changes to the file, as Save describes. It reads
// nothing from the Config it came from, so it may run on any goroutine.
func (j *SaveJob) Run() error {
	if j.unreadable != nil {
		// Load never saw this file's contents; writing c would replace them
		// with emptiness.
		return fmt.Errorf("config: not saving over %s — it could not be read at startup: %w", j.path, j.unreadable)
	}
	dir := filepath.Dir(j.path)
	// 0700, matching loadOrCreateKey's MkdirAll (secret.go). MkdirAll doesn't
	// chmod an existing directory, so whichever runs first decides; both ask for
	// owner-only.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	key, keyErr := loadOrCreateKey(dir)

	// Locked from the re-read to the write: the merge is only sound if no
	// other instance writes in between.
	return fileutil.WithLock(j.path, func() error { return j.mergeAndWrite(key, keyErr) })
}

// EndSave adopts a job Run has finished, on the goroutine that owns c: the
// merged connection list (with any AddOrUpdate or RemoveConnection made since
// BeginSave replayed onto it and still pending) and the settings the job wrote
// as the new baseline. A job that wrote nothing changes nothing, so its ops stay
// pending; ending a job twice is a no-op.
func (c *Config) EndSave(j *SaveJob) {
	if !j.written || j.adopted {
		return
	}
	j.adopted = true
	later := slices.Clone(c.ops[min(len(j.ops), len(c.ops)):])
	conns := j.merged
	for _, op := range later {
		conns = op.applyTo(conns)
	}
	c.Connections = conns
	c.ops = later
	c.base = j.settings
	// mergeAndWrite re-sealed every password it could open under aadPrefix.
	c.legacySealed = false
}

// NeedsReseal reports whether Load opened a password sealed under an earlier,
// weaker binding than the current one (secret.go's aadPrefixV3, aadPrefixV2 or
// no prefix), which only a save rewrites. An unprefixed one carries no AAD at
// all: write access to config.json can transplant it onto an entry pointing
// elsewhere, the attack connectionAAD exists to stop. A config that is loaded
// and never saved would keep it indefinitely, so the app saves once at startup
// when this is true. A password that failed to open is not counted: Save
// writes it back as it was.
func (c *Config) NeedsReseal() bool { return c.legacySealed }

// applyTo replays op on a connection list.
func (op connOp) applyTo(list []Connection) []Connection {
	if op.add != nil {
		return addOrUpdate(list, *op.add)
	}
	list, _ = removeConnection(list, op.remove)
	return list
}

// mergeAndWrite is Run's re-read, merge and write, run under the file lock.
// key is nil when keyErr says why there is none.
func (j *SaveJob) mergeAndWrite(key []byte, keyErr error) error {
	path := j.path
	var merged *Config
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, fs.ErrNotExist):
		merged = defaultConfig()
	case err != nil:
		// As at Load: what's there is unknown, so writing would destroy it.
		return fmt.Errorf("config: not saving over %s — it could not be re-read: %w", path, err)
	default:
		if merged, err = parseConfig(path, data); err != nil {
			return fmt.Errorf("config: not saving over %s — %w", path, err)
		}
		if key != nil {
			merged.openPasswords(key, path)
		} else {
			merged.keepSealed()
		}
	}
	mergeSettings(j.settings, j.base, merged)
	for _, op := range j.ops {
		if op.add != nil && key == nil && op.add.Password != "" && op.add.sealed == "" {
			// The new password can't be sealed, so the entry it replaces
			// keeps its old ciphertext rather than losing it.
			add := *op.add
			if i := slices.IndexFunc(merged.Connections, func(e Connection) bool {
				return e.Name == add.Name || e.GeneratedName() == add.Name
			}); i >= 0 {
				add.sealed = merged.Connections[i].sealed
			}
			op = connOp{add: &add}
		}
		merged.Connections = op.applyTo(merged.Connections)
	}

	onDisk := *merged
	onDisk.Connections = make([]Connection, len(merged.Connections))
	var unsealed []string
	for i, conn := range merged.Connections {
		if key == nil && conn.Password != "" {
			unsealed = append(unsealed, conn.Name)
			conn.Password = ""
		}
		if conn.Password == "" && conn.sealed != "" {
			// Couldn't open this one; write the original bytes back.
			conn.Password = conn.sealed
			onDisk.Connections[i] = conn
			continue
		}
		enc, err := encryptPassword(key, conn)
		if err != nil {
			return err
		}
		conn.Password = enc
		onDisk.Connections[i] = conn
	}

	data, err := json.MarshalIndent(&onDisk, "", "  ")
	if err != nil {
		return err
	}
	if err := fileutil.WriteAtomic(path, data, 0o600); err != nil {
		return err
	}
	j.merged = merged.Connections
	j.written = true
	if len(unsealed) > 0 {
		return fmt.Errorf("config: saved, but not the password for %s: %w", strings.Join(unsealed, ", "), keyErr)
	}
	return nil
}
