package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// key_signatures_page.go is the Signatures page of Certificate and Asymmetric
// Key Properties — the modules the object signs, with Add and Remove — and
// the "Signed by" rows a module's Details pane shows.
//
// Rights, probed on 13 and 17 (2026-09-22), identical: ADD SIGNATURE needs
// CONTROL on the signer and ALTER on the module; DROP SIGNATURE needs only
// ALTER on the module. The page is gated on CONTROL on the signer or the
// database-wide rights that carry ALTER on every module — either lets part of
// it work — and the module half is left to the server (Msg 15151). Counter
// signatures are listed and can be removed, but not added here.

// keySignatureColumns is the Signatures grid's header.
var keySignatureColumns = []string{"Module", "Type", "Signature"}

// keySignatureRights gate the Signatures page — see the file comment.
func keySignatureRights(control gate.Right) []gate.Right {
	return []gate.Right{control, gate.AlterDatabase, gate.ControlDB, gate.AlterAnySchema}
}

// keySignatureEdit is one row of the page: a signature the signer had when the
// page loaded, or one added since.
type keySignatureEdit struct {
	schema, module, typ string
	counter             bool

	isNew         bool
	pendingRemove bool
}

func (e *keySignatureEdit) key() string { return e.schema + "." + e.module }

func (e *keySignatureEdit) cells() []string {
	kind := "Signature"
	if e.counter {
		kind = "Counter signature"
	}
	return []string{e.key(), moduleTypeText(e.typ), kind}
}

// moduleTypeText is a sys.objects type_desc as the Signatures grid shows it.
func moduleTypeText(typeDesc string) string {
	switch typeDesc {
	case "SQL_STORED_PROCEDURE":
		return "Stored procedure"
	case "SQL_SCALAR_FUNCTION":
		return "Scalar function"
	case "SQL_TABLE_VALUED_FUNCTION":
		return "Table-valued function"
	case "SQL_TRIGGER":
		return "Trigger"
	}
	return typeDesc
}

// keySigner is what the page needs to know about its own signer.
type keySigner struct {
	kind gosmo.SignerKind
	name string
	// protection is the private key's pvt_key_encryption_type_desc.
	protection string
	signed     func(ctx context.Context, d *gosmo.Database) ([]*gosmo.ModuleSignature, error)
}

func certificateSignaturesPage(sc *db.ServerConn, dbName, name string) propPage {
	return withRequiresOn(keySignaturesPage(sc, dbName, func(ctx context.Context, d *gosmo.Database) (*keySigner, error) {
		c, err := findCertificate(ctx, sc, dbName, name)
		if err != nil {
			return nil, err
		}
		return &keySigner{kind: gosmo.SignerCertificate, name: name, protection: c.PvtKeyEncryptionType,
			signed: func(ctx context.Context, d *gosmo.Database) ([]*gosmo.ModuleSignature, error) {
				return d.CertificateRef(name).SignedModules(ctx)
			}}, nil
	}), dbName, "", name, keySignatureRights(gate.ControlOnCertificate)...)
}

func asymmetricKeySignaturesPage(sc *db.ServerConn, dbName, name string) propPage {
	return withRequiresOn(keySignaturesPage(sc, dbName, func(ctx context.Context, d *gosmo.Database) (*keySigner, error) {
		k, err := findAsymmetricKey(ctx, sc, dbName, name)
		if err != nil {
			return nil, err
		}
		return &keySigner{kind: gosmo.SignerAsymmetricKey, name: name, protection: k.PvtKeyEncryptionType,
			signed: func(ctx context.Context, d *gosmo.Database) ([]*gosmo.ModuleSignature, error) {
				return d.AsymmetricKeyRef(name).SignedModules(ctx)
			}}, nil
	}), dbName, "", name, keySignatureRights(gate.ControlOnAsymmetricKey)...)
}

func keySignaturesPage(sc *db.ServerConn, dbName string, signer func(context.Context, *gosmo.Database) (*keySigner, error)) propPage {
	return propPage{
		title: "Signatures",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			s, err := signer(ctx, d)
			if err != nil {
				return nil, nil, err
			}
			signed, err := s.signed(ctx, d)
			if err != nil {
				return nil, nil, err
			}
			mods, err := d.SignableModules(ctx)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildKeySignaturesForm(s, signed, mods, sc.Server.DatabaseRef(dbName))
			return f, apply, nil
		},
	}
}

// buildKeySignaturesForm is the Signatures page: the signer's signatures in a
// grid with Add / Remove, collected and applied on OK or Apply.
func buildKeySignaturesForm(s *keySigner, signed []*gosmo.ModuleSignature, mods []*gosmo.SignableModule, ref *gosmo.Database) (*propsheet.Form, propApply) {
	edits := make([]*keySignatureEdit, len(signed))
	for i, x := range signed {
		edits[i] = &keySignatureEdit{schema: x.Schema, module: x.Module, typ: x.ModuleType, counter: x.Counter}
	}
	visible := func() []*keySignatureEdit {
		return slices.DeleteFunc(slices.Clone(edits), func(e *keySignatureEdit) bool { return e.pendingRemove })
	}
	rowsFor := func() [][]string {
		vis := visible()
		rows := make([][]string, len(vis))
		for i, e := range vis {
			rows[i] = e.cells()
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData(keySignatureColumns, rowsFor())
	grid.SetCellCursor(true)
	hint := propsheet.Hint()

	canSign := s.protection != "" && s.protection != "NO_PRIVATE_KEY"
	byPassword := s.protection == "ENCRYPTED_BY_PASSWORD"

	modLabels := make([]string, len(mods))
	for i, m := range mods {
		modLabels[i] = m.Schema + "." + m.Name
	}
	if len(modLabels) == 0 {
		modLabels = []string{noneItem}
	}
	modSelect := propsheet.Select("Sign module", modLabels, 0)
	modSelect.SetDirtyTracked(false)
	passField := propsheet.Password("Private key password", 20)
	passField.SetDirtyTracked(false)

	addBtn := widgets.NewButton("Add", func() {
		if len(mods) == 0 {
			hint.Set("The database has no module that can be signed.")
			return
		}
		m := mods[modSelect.Selected()]
		for _, x := range edits {
			if x.schema != m.Schema || x.module != m.Name || x.counter {
				continue
			}
			if x.pendingRemove {
				// Undo the Remove rather than dropping and re-signing.
				x.pendingRemove = false
				hint.Clear()
				resetGrid(grid, keySignatureColumns, rowsFor(), len(visible())-1)
				return
			}
			hint.Set(x.key() + " is already signed by this " + strings.ToLower(string(s.kind)) + ".")
			return
		}
		hint.Clear()
		edits = append(edits, &keySignatureEdit{schema: m.Schema, module: m.Name, typ: m.Type, isNew: true})
		resetGrid(grid, keySignatureColumns, rowsFor(), len(visible())-1)
	})
	addBtn.SetEnabled(canSign)

	removeBtn := widgets.NewButton("Remove", func() {
		vis := visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			hint.Set("Select a signature in the grid above to remove it.")
			return
		}
		e := vis[i]
		if e.isNew {
			edits = slices.DeleteFunc(edits, func(x *keySignatureEdit) bool { return x == e })
		} else {
			e.pendingRemove = true
		}
		hint.Clear()
		resetGrid(grid, keySignatureColumns, rowsFor(), 0)
	})

	gridRow := propsheet.NewGridRow(grid, 8)
	gridRow.DirtyFn = func() bool {
		return slices.ContainsFunc(edits, func(e *keySignatureEdit) bool { return e.isNew || e.pendingRemove })
	}
	gridRow.RevertFn = func() {
		edits = slices.DeleteFunc(edits, func(e *keySignatureEdit) bool { return e.isNew })
		for _, e := range edits {
			e.pendingRemove = false
		}
		resetGrid(grid, keySignatureColumns, rowsFor(), 0)
		hint.Clear()
	}

	rows := []propsheet.Row{
		propsheet.Section("Signed modules"),
		gridRow,
		modSelect,
	}
	if byPassword {
		rows = append(rows, passField)
	}
	rows = append(rows, propsheet.Buttons(addBtn, removeBtn), hint)
	switch {
	case !canSign:
		rows = append(rows, propsheet.Note("This "+strings.ToLower(string(s.kind))+" has no private key, so it cannot sign; "+
			"its signatures can still be removed."))
	case byPassword:
		rows = append(rows, propsheet.Note("The private key is protected by a password, which signing needs: type it in "+
			"Private key password. Removing a signature needs none."))
	}
	rows = append(rows, propsheet.Note("Signing needs CONTROL on the "+strings.ToLower(string(s.kind))+" and ALTER on the module; "+
		"removing a signature needs ALTER on the module. Altering a module drops its signatures. Counter signatures are "+
		"listed and can be removed, but are added from a query window."))
	f := propsheet.NewForm(rows...)

	apply := func(ctx context.Context) error {
		signer := gosmo.Signer{Kind: s.kind, Name: s.name}
		for _, e := range edits {
			if e.pendingRemove && !e.isNew {
				if err := ref.DropSignature(ctx, e.schema, e.module, signer, e.counter); err != nil {
					return err
				}
			}
		}
		for _, e := range edits {
			if !e.isNew || e.pendingRemove {
				continue
			}
			if byPassword && passField.Value() == "" {
				return fmt.Errorf("type the private key's password in Private key password to sign %s", e.key())
			}
			if byPassword {
				signer.Password = scriptSafePassword(ctx, passField.Value())
			}
			if err := ref.AddSignature(ctx, e.schema, e.module, signer, false); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// moduleDetail is the Details pane's view of a stored procedure, function or
// DML trigger: the default Property/Value rows, plus one "Signed by" row per
// signature on it. These families have no Properties dialog, so this is where
// a module's signers are shown.
func moduleDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	d, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	sigs, err := d.SignaturesOn(ctx, node.data.Schema, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	rows := [][]string{
		{"Name", node.label},
		{"Type", nodeTypeName(node.data.Type)},
		{"Database", node.data.DBName},
		{"Schema", node.data.Schema},
	}
	if len(sigs) == 0 {
		rows = append(rows, []string{"Signed by", "(not signed)"})
	}
	for _, x := range sigs {
		rows = append(rows, []string{"Signed by", moduleSignatureText(x)})
	}
	return []string{"Property", "Value"}, rows, nil
}

// moduleSignatureText names one signature's signer: "Certificate c1",
// "Asymmetric key a1 (counter signature)", or the server's description for a
// signer the caller cannot see.
func moduleSignatureText(x *gosmo.ModuleSignature) string {
	var s string
	switch {
	case x.Signer == "":
		s = strings.ToLower(x.CryptTypeDesc) + " (not visible)"
		s = strings.ToUpper(s[:1]) + s[1:]
	case x.Kind == gosmo.SignerCertificate:
		s = "Certificate " + x.Signer
	case x.Kind == gosmo.SignerAsymmetricKey:
		s = "Asymmetric key " + x.Signer
	default:
		s = x.CryptTypeDesc + " " + x.Signer
	}
	if x.Counter && x.Signer != "" {
		s += " (counter signature)"
	}
	return s
}
