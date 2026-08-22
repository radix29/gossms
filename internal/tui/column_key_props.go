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

// column_key_props.go is the read-only Properties for the two Always
// Encrypted keys. Read-only throughout: SQL Server has no ALTER for either —
// a master key is rotated by creating the new one and re-encrypting the
// column encryption keys under it.

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

func columnEncryptionKeyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			k, err := findColumnEncryptionKey(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(k.Values))
			for i, v := range k.Values {
				rows[i] = []string{v.MasterKeyName, v.EncryptionAlgorithm, hexPreview(v.EncryptedValue)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Column master key", "Algorithm", "Encrypted value"}, rows)
			grid.SetCellCursor(true)

			formRows := []propsheet.Row{
				propsheet.Section("Column encryption key"),
				propsheet.Static("Name", k.Name),
				propsheet.Static("Encrypted values", fmt.Sprintf("%d", len(k.Values))),
				propsheet.Section("Values"),
				propsheet.NewGridRow(grid, 8),
			}
			if len(k.Values) > 1 {
				formRows = append(formRows,
					propsheet.Section("Note"),
					propsheet.Note("Two values mean a master-key rotation is in progress. Dropping either one makes the data encrypted under it unreadable."),
				)
			}
			return propsheet.NewForm(formRows...), nil, nil
		},
	}}
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
