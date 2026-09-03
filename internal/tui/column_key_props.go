package tui

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// column_key_props.go is the Properties for the two Always Encrypted keys.
// The master key's page is read-only — SQL Server has no ALTER COLUMN MASTER
// KEY, and a master key is rotated by creating the new one and re-encrypting
// the column encryption keys under it. That re-encryption is what the column
// encryption key's page does: ALTER ... ADD VALUE puts the key under a second
// master key, and ALTER ... DROP VALUE retires the first once every client
// has the new one.

func findColumnMasterKey(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ColumnMasterKey, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ColumnMasterKeyByNameContext(ctx, name)
}

func findColumnEncryptionKey(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ColumnEncryptionKey, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ColumnEncryptionKeyByNameContext(ctx, name)
}

func columnMasterKeyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			k, err := findColumnMasterKey(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			rows := []propsheet.Row{
				propsheet.Section("Column master key"),
				propsheet.Static("Name", k.Name),
				propsheet.Static("Key store provider", k.KeyStoreProviderName),
				propsheet.Static("Key path", k.KeyPath),
				propsheet.Static("Allow enclave computations", boolStr(k.AllowEnclaveComputations)),
			}
			if k.AllowEnclaveComputations {
				rows = append(rows, propsheet.Static("Signature", hexPreview(k.Signature)))
			}
			rows = append(rows,
				propsheet.Section("Note"),
				propsheet.Note("The key itself lives in the key store named above — SQL Server holds only this metadata and never sees the key."),
			)
			return propsheet.NewForm(rows...), nil, nil
		},
	}}
}

// noRotation is the "leave this alone" item both rotation dropdowns open on.
// A Select row has no unset state, so the first item has to mean nothing —
// without it, opening the page and pressing OK would rotate the key.
const noRotation = "(none)"

func columnEncryptionKeyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{withRequires(propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			k, err := dbObj.ColumnEncryptionKeyByNameContext(ctx, name)
			if err != nil {
				return nil, nil, err
			}
			masters, err := dbObj.ColumnMasterKeysContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(k.Values))
			under := make(map[string]bool, len(k.Values))
			dropItems := []string{noRotation}
			for i, v := range k.Values {
				rows[i] = []string{v.MasterKeyName, v.EncryptionAlgorithm, hexPreview(v.EncryptedValue)}
				under[strings.ToLower(v.MasterKeyName)] = true
				dropItems = append(dropItems, v.MasterKeyName)
			}
			// A master key the key is already encrypted under is not offered:
			// ADD VALUE for one that already has a value is an error, and the
			// only other thing the user could mean is a re-encryption, which
			// is a drop and an add.
			addItems := []string{noRotation}
			for _, m := range masters {
				if !under[strings.ToLower(m.Name)] {
					addItems = append(addItems, m.Name)
				}
			}

			grid := controls.NewDataGrid()
			grid.SetData([]string{"Column master key", "Algorithm", "Encrypted value"}, rows)
			grid.SetCellCursor(true)

			addRow := propsheet.Select("Encrypt under master key", addItems, 0)
			valueRow := propsheet.Text("New encrypted value (hex)", "", 40)
			dropRow := propsheet.Select("Drop value under master key", dropItems, 0)

			formRows := []propsheet.Row{
				propsheet.Section("Column encryption key"),
				propsheet.Static("Name", k.Name),
				propsheet.Static("Encrypted values", fmt.Sprintf("%d", len(k.Values))),
				propsheet.Section("Values"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Rotate the master key"),
				addRow, valueRow, dropRow,
				propsheet.Note("Add the value first, let every client pick up the new master key, then reopen this page and drop the old value. The new value is this key's material encrypted under the master key chosen above, produced by SSMS or the SqlColumnEncryptionKey cmdlets — SQL Server stores it without checking it."),
			}
			if len(k.Values) > 1 {
				formRows = append(formRows,
					propsheet.Section("Note"),
					propsheet.Note("Two values mean a master-key rotation is in progress. Dropping either one makes the data unreadable to any client that can only reach that master key."),
				)
			}
			if len(addItems) == 1 {
				formRows = append(formRows,
					propsheet.Section("Note"),
					propsheet.Note("Every column master key in this database already has a value on this key. Create the new master key first — a rotation adds a value under one this key is not yet encrypted under."))
			}

			apply := func(ctx context.Context) error {
				add, value, drop := addRow.Value(), strings.TrimSpace(valueRow.Value()), dropRow.Value()
				addWanted, dropWanted := add != noRotation, drop != noRotation
				if err := checkRotation(len(k.Values), addWanted, value, dropWanted, add, drop); err != nil {
					return err
				}
				// Add before drop, in one apply as in two: the key is never
				// left with fewer values than it started with, so a failure
				// after the add leaves the rotation half done rather than the
				// data unreadable.
				if addWanted {
					blob, err := parseHexBytes(value)
					if err != nil {
						return fmt.Errorf("encrypted value: %w", err)
					}
					if err := k.AddValueContext(ctx, gosmo.ColumnEncryptionKeyValue{
						MasterKeyName:       add,
						EncryptionAlgorithm: cekAlgorithm,
						EncryptedValue:      blob,
					}); err != nil {
						return err
					}
				}
				if dropWanted {
					return k.DropValueContext(ctx, drop)
				}
				return nil
			}
			return propsheet.NewForm(formRows...), apply, nil
		},
	}, dbName, rightAlterAnyCEK)}
}

// checkRotation refuses the rotations that are not one — a master key named
// with no value to go under it, the reverse, and a drop that would leave the
// key with no value at all. The server refuses that last one too (Msg 33275,
// verified live), so this is about saying which value cannot go and why
// before the round trip, not about a hole in the server's own rule.
func checkRotation(existing int, addWanted bool, value string, dropWanted bool, add, drop string) error {
	if addWanted && value == "" {
		return fmt.Errorf("a value encrypted under %s is required — only a client holding that master key can produce it", add)
	}
	if value != "" && !addWanted {
		return fmt.Errorf("choose the column master key the new value is encrypted under")
	}
	if !addWanted && !dropWanted {
		return nil
	}
	remaining := existing
	if addWanted {
		remaining++
	}
	if dropWanted {
		remaining--
	}
	if remaining < 1 {
		return fmt.Errorf("dropping the value under %s leaves the key with none — add the new value first", drop)
	}
	return nil
}

// hexPreview renders key bytes as a 0x literal, shortened in the middle —
// an encrypted value is a few hundred bytes and nothing about it is read by
// eye, but its length and ends identify it.
func hexPreview(b []byte) string {
	if len(b) == 0 {
		return "(none)"
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	if len(s) <= 48 {
		return "0x" + s
	}
	return fmt.Sprintf("0x%s…%s (%d bytes)", s[:24], s[len(s)-8:], len(b))
}

// parseHexBytes reads a pasted key blob — an enclave signature or an
// encrypted value — from the "0x..." form SSMS and the PowerShell cmdlets
// print. The prefix is optional and case doesn't matter; an odd number of
// digits is rejected rather than silently padded, since a blob that lost a
// character is not one the server can ever decrypt.
func parseHexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0X"), "0x")
	if s == "" {
		return nil, fmt.Errorf("no value")
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("%d hex digits — a byte takes two, so one is missing", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not hexadecimal: expected 0x followed by hex digits")
	}
	return b, nil
}

// showNewColumnMasterKeyDialog and showNewColumnEncryptionKeyDialog are the
// Object Explorer entry points for the two Always Encrypted create dialogs.
// Both take the folder node, not just the connection: each key belongs to one
// database, and that folder is also what gets refreshed afterwards.
func (a *App) showNewColumnMasterKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newCMKDialog.show(sc, node)
}

func (a *App) showNewColumnEncryptionKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newCEKDialog.show(sc, node)
}
