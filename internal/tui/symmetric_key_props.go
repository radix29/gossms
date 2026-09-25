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

// symmetric_key_props.go is the Properties for a symmetric key. General's one
// write is the owner (key_actions.go); there is no rename, and the key's
// algorithm and material are fixed at CREATE. The key's other write — adding
// and dropping encryptions, which needs the key open in the same session — is
// the Encryption page's, below.

// findSymmetricKey resolves name in dbName. SymmetricKeyByName already turns
// an absent key into an ErrNotFound, unlike the certificate and asymmetric-key
// finders, so there is no (nil, nil) to translate.
func findSymmetricKey(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.SymmetricKey, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.SymmetricKeyByName(ctx, name)
}

func symmetricKeyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	general := propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			k, err := findSymmetricKey(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			// A symmetric key, unlike a certificate or asymmetric key, may be
			// owned by a role.
			owner, err := keyOwnerRow(ctx, sc, dbName, k.Owner, true)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Symmetric key"),
				propsheet.Static("Name", k.Name),
				owner,
				propsheet.Static("Algorithm", k.Algorithm),
				propsheet.Static("Key length", keyLengthText(k.KeyLength)),
				propsheet.Static("Key GUID", k.KeyGUID),
				propsheet.Static("Created", formatSQLDate(k.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(k.ModifyDate)),
				propsheet.Static("Provider", symmetricKeyProviderText(k)),
				propsheet.Note("The key material cannot be read back: a key with the same GUID "+
					"and value can be recreated only from the KEY_SOURCE and IDENTITY_VALUE it was created with."),
				keyOwnerNote("symmetric key"),
			)
			return f, keyOwnerApply(sc, dbName, owner, func(ctx context.Context, d *gosmo.Database, o string) error {
				return d.SymmetricKeyRef(name).SetOwner(ctx, o)
			}), nil
		},
	}
	return []propPage{
		withRequiresOn(general, dbName, "", name, gate.ControlOnSymmetricKey),
		withRequiresOn(symmetricKeyEncryptionPage(sc, dbName, name), dbName, "", name, symmetricKeyEncryptionRights...),
	}
}

// symmetricKeyEncryptionRights gate the Encryption page: the effective ALTER on
// the key, and nothing beside it — see gate.AlterOnSymmetricKey. Not the Delete
// set, whose per-securable half asks CONTROL: that under-offers to an
// ALTER-on-key grantee and over-offers to CONTROL with ALTER denied.
var symmetricKeyEncryptionRights = []gate.Right{gate.AlterOnSymmetricKey}

// The Encryption page edits the key's ENCRYPTION BY list. Every ADD or DROP
// ENCRYPTION needs the key open in the same session (Msg 15315), so the page
// also asks how to open it — the decryptor, one of the encryptions the key has
// now — and gosmo sends each change as one OPEN / ALTER / CLOSE batch.
//
// A symmetric key is opened here only through a chain nobody has to type a
// password into: a parent key that opens by a certificate or asymmetric key
// the master key protects, or by such a parent in turn. That covers the
// parent a new encryption by a symmetric key needs open (probed: ADD and DROP
// ENCRYPTION BY SYMMETRIC KEY p both need p open) without a prompt per link;
// a key reachable only by a password is not offered as either.
//
// Rights: the page is gated on the Delete set — ALTER ANY SYMMETRIC KEY, the
// database-wide pair, or CONTROL on the key (its owner). Step 0 found ALTER on
// the key alone permits ADD/DROP too, but the per-securable probe asks only
// CONTROL, so that grantee sees the page read-only. The second securable is
// not gated: using a certificate or asymmetric key as the decryptor, or
// removing an encryption by one, needs CONTROL on it, and the server's 15151
// is left to say so.

// symKeyEncColumns is the Encryption page's grid header.
var symKeyEncColumns = []string{"Encrypted by", "Name"}

// symKeyEncEdit is one row of the Encryption page: an encryption the key had
// when the page loaded, or one added since. Nothing reaches the server until
// the page's apply runs, so Cancel discards it.
type symKeyEncEdit struct {
	kind gosmo.SymmetricKeyEncryptionKind
	// name is the certificate or key; "" for a password, and for an
	// encryptor the caller cannot see.
	name string
	// desc is the server's crypt_type_desc, shown for a kind gosmo does not
	// recognise.
	desc string
	// password is a new password encryption's password, or the one an
	// existing password encryption is removed with — DROP ENCRYPTION BY
	// PASSWORD names the encryption by its password (Msg 15313 on a miss).
	password string
	// open says how to open name, for a symmetric-key encryption.
	open *gosmo.SymmetricKeyDecryptor

	isNew         bool
	pendingRemove bool
}

func (e *symKeyEncEdit) cells() []string {
	kind := symmetricKeyEncryptionText(gosmo.SymmetricKeyEncryption{Kind: e.kind, Name: "x", CryptTypeDesc: e.desc})
	kind = strings.TrimSuffix(kind, " x")
	switch {
	case e.kind == gosmo.SymmetricKeyByPassword:
		return []string{kind, ""}
	case e.name == "" && e.kind != "" && e.kind != gosmo.SymmetricKeyByMasterKey:
		return []string{kind, "(not visible)"}
	}
	return []string{kind, e.name}
}

// encryptor is the edit as gosmo's ENCRYPTION BY item. A password is replaced
// by its placeholder under Script Changes.
func (e *symKeyEncEdit) encryptor(ctx context.Context) gosmo.SymmetricKeyEncryptor {
	enc := gosmo.SymmetricKeyEncryptor{Kind: e.kind, Name: e.name, Open: e.open}
	if e.kind == gosmo.SymmetricKeyByPassword {
		enc.Name = ""
		enc.Password = scriptSafePassword(ctx, e.password)
	}
	return enc
}

// symKeyCatalog is everything the page reads beside the key: the
// certificates, asymmetric keys and other symmetric keys the caller can see,
// in the server's order and by name.
type symKeyCatalog struct {
	certNames, asymNames, symNames []string

	certs map[string]*gosmo.Certificate
	asym  map[string]*gosmo.AsymmetricKey
	sym   map[string]*gosmo.SymmetricKey
}

func loadSymKeyCatalog(ctx context.Context, d *gosmo.Database) (*symKeyCatalog, error) {
	certs, err := d.Certificates(ctx)
	if err != nil {
		return nil, err
	}
	asym, err := d.AsymmetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	sym, err := d.SymmetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	c := &symKeyCatalog{
		certs: make(map[string]*gosmo.Certificate, len(certs)),
		asym:  make(map[string]*gosmo.AsymmetricKey, len(asym)),
		sym:   make(map[string]*gosmo.SymmetricKey, len(sym)),
	}
	for _, x := range certs {
		c.certNames = append(c.certNames, x.Name)
		c.certs[x.Name] = x
	}
	for _, x := range asym {
		c.asymNames = append(c.asymNames, x.Name)
		c.asym[x.Name] = x
	}
	for _, x := range sym {
		c.symNames = append(c.symNames, x.Name)
		c.sym[x.Name] = x
	}
	return c, nil
}

// privateKey reports how a certificate's or asymmetric key's private key is
// protected: usable is false when there is none (a certificate imported from
// its public half) or the object is not visible, and byPassword says a
// password must be typed to open with it (Msg 15334 without one).
func (c *symKeyCatalog) privateKey(kind gosmo.SymmetricKeyEncryptionKind, name string) (usable, byPassword bool) {
	var protection string
	switch kind {
	case gosmo.SymmetricKeyByCertificate:
		x := c.certs[name]
		if x == nil {
			return false, false
		}
		protection = x.PvtKeyEncryptionType
	case gosmo.SymmetricKeyByAsymmetricKey:
		x := c.asym[name]
		if x == nil {
			return false, false
		}
		protection = x.PvtKeyEncryptionType
	default:
		return false, false
	}
	switch protection {
	case "ENCRYPTED_BY_MASTER_KEY":
		return true, false
	case "ENCRYPTED_BY_PASSWORD":
		return true, true
	}
	return false, false
}

// opener is how to open symmetric key name with no password typed, or nil
// when there is no such way: an encryption by a master-key-protected
// certificate or asymmetric key, else by a symmetric key that itself has an
// opener. seen stops a cycle.
func (c *symKeyCatalog) opener(name string, seen map[string]bool) *gosmo.SymmetricKeyDecryptor {
	k := c.sym[name]
	if k == nil || seen[name] {
		return nil
	}
	for _, e := range k.Encryptions {
		if usable, byPassword := c.privateKey(e.Kind, e.Name); usable && !byPassword {
			return &gosmo.SymmetricKeyDecryptor{Kind: e.Kind, Name: e.Name}
		}
	}
	seen[name] = true
	for _, e := range k.Encryptions {
		if e.Kind != gosmo.SymmetricKeyBySymmetricKey {
			continue
		}
		if p := c.opener(e.Name, seen); p != nil {
			return &gosmo.SymmetricKeyDecryptor{Kind: e.Kind, Name: e.Name, Open: p}
		}
	}
	return nil
}

// symKeyDecOption is one way the page can open the key: one of its current
// encryptions, with whether a password must be typed for it.
type symKeyDecOption struct {
	label         string
	dec           gosmo.SymmetricKeyDecryptor
	needsPassword bool
}

// symKeyDecryptorOptions lists the key's encryptions the page can open it
// with. Left out: an encryptor the caller cannot see, a certificate with no
// private key, a symmetric key with no opener, and every password encryption
// after the first — any one of them opens the key with its own password, and
// the option says only "Password".
func symKeyDecryptorOptions(k *gosmo.SymmetricKey, c *symKeyCatalog) []symKeyDecOption {
	var opts []symKeyDecOption
	password := false
	for _, e := range k.Encryptions {
		switch e.Kind {
		case gosmo.SymmetricKeyByPassword:
			if !password {
				password = true
				opts = append(opts, symKeyDecOption{label: "Password", dec: gosmo.SymmetricKeyDecryptor{Kind: e.Kind}, needsPassword: true})
			}
		case gosmo.SymmetricKeyByCertificate, gosmo.SymmetricKeyByAsymmetricKey:
			if usable, byPassword := c.privateKey(e.Kind, e.Name); usable {
				opts = append(opts, symKeyDecOption{label: symmetricKeyEncryptionText(e),
					dec: gosmo.SymmetricKeyDecryptor{Kind: e.Kind, Name: e.Name}, needsPassword: byPassword})
			}
		case gosmo.SymmetricKeyBySymmetricKey:
			if p := c.opener(e.Name, map[string]bool{k.Name: true}); p != nil {
				opts = append(opts, symKeyDecOption{label: symmetricKeyEncryptionText(e),
					dec: gosmo.SymmetricKeyDecryptor{Kind: e.Kind, Name: e.Name, Open: p}})
			}
		}
	}
	return opts
}

// symKeyAddOption is one item of the page's "Add encryption by" picker.
type symKeyAddOption struct {
	label string
	kind  gosmo.SymmetricKeyEncryptionKind
	name  string
	open  *gosmo.SymmetricKeyDecryptor
}

// symKeyAddOptions is a password, then every certificate and asymmetric key
// the caller can see — encrypting needs only the public key, so one with no
// private key is still offered — then every other symmetric key the page can
// open without a password.
func symKeyAddOptions(k *gosmo.SymmetricKey, c *symKeyCatalog) []symKeyAddOption {
	opts := []symKeyAddOption{{label: "Password", kind: gosmo.SymmetricKeyByPassword}}
	for _, n := range c.certNames {
		opts = append(opts, symKeyAddOption{label: "Certificate " + n, kind: gosmo.SymmetricKeyByCertificate, name: n})
	}
	for _, n := range c.asymNames {
		opts = append(opts, symKeyAddOption{label: "Asymmetric key " + n, kind: gosmo.SymmetricKeyByAsymmetricKey, name: n})
	}
	for _, n := range c.symNames {
		if n == k.Name {
			continue
		}
		if p := c.opener(n, map[string]bool{k.Name: true}); p != nil {
			opts = append(opts, symKeyAddOption{label: "Symmetric key " + n, kind: gosmo.SymmetricKeyBySymmetricKey, name: n, open: p})
		}
	}
	return opts
}

func symmetricKeyEncryptionPage(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "Encryption",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			k, err := d.SymmetricKeyByName(ctx, name)
			if err != nil {
				return nil, nil, err
			}
			cat, err := loadSymKeyCatalog(ctx, d)
			if err != nil {
				return nil, nil, err
			}
			// SymmetricKeyRef for the writes: the ALTER addresses the key by
			// name, and a by-name read does not run under Script Changes.
			ref := sc.Server.DatabaseRef(dbName).SymmetricKeyRef(name)
			f, apply := buildSymmetricKeyEncryptionForm(k, cat, ref)
			return f, apply, nil
		},
	}
}

// buildSymmetricKeyEncryptionForm is the Encryption page: the key's
// encryptions in a grid with Add / Remove, collected and applied on OK or
// Apply as membership_page.go's are, and the decryptor they are applied with.
func buildSymmetricKeyEncryptionForm(k *gosmo.SymmetricKey, cat *symKeyCatalog, ref *gosmo.SymmetricKey) (*propsheet.Form, propApply) {
	edits := make([]*symKeyEncEdit, len(k.Encryptions))
	for i, e := range k.Encryptions {
		edits[i] = &symKeyEncEdit{kind: e.Kind, name: e.Name, desc: e.CryptTypeDesc}
		if e.Kind == gosmo.SymmetricKeyBySymmetricKey && e.Name != "" {
			edits[i].open = cat.opener(e.Name, map[string]bool{k.Name: true})
		}
	}
	visible := func() []*symKeyEncEdit {
		out := make([]*symKeyEncEdit, 0, len(edits))
		for _, e := range edits {
			if !e.pendingRemove {
				out = append(out, e)
			}
		}
		return out
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
	grid.SetData(symKeyEncColumns, rowsFor())
	grid.SetCellCursor(true)

	addOpts := symKeyAddOptions(k, cat)
	addLabels := make([]string, len(addOpts))
	for i, o := range addOpts {
		addLabels[i] = o.label
	}
	addSelect := propsheet.Select("Add encryption by", addLabels, 0)
	addSelect.SetDirtyTracked(false)
	passField := propsheet.Password("Password", 20)
	passField.SetDirtyTracked(false)
	passConfirm := propsheet.Password("Confirm password", 20)
	passConfirm.SetDirtyTracked(false)
	hint := propsheet.Hint()

	decOpts := symKeyDecryptorOptions(k, cat)
	decLabels := []string{noneItem}
	decDefault := 0
	if len(decOpts) > 0 {
		decLabels = make([]string, len(decOpts))
		decDefault = -1
		for i, o := range decOpts {
			decLabels[i] = o.label
			if decDefault < 0 && !o.needsPassword {
				decDefault = i
			}
		}
		decDefault = max(decDefault, 0)
	}
	decSelect := propsheet.Select("Decrypt by", decLabels, decDefault)
	decSelect.SetDirtyTracked(false)
	decPass := propsheet.Password("Decryption password", 20)
	decPass.SetDirtyTracked(false)

	var removeBtn *widgets.Button
	// The server refuses to remove the last encryption (Msg 15558); counting
	// a pending Add is right, since apply adds before it removes.
	syncRemove := func() { removeBtn.SetEnabled(len(visible()) > 1) }
	clearPasswords := func() {
		passField.SetValue("")
		passConfirm.SetValue("")
	}

	addBtn := widgets.NewButton("Add", func() {
		o := addOpts[addSelect.Selected()]
		e := &symKeyEncEdit{kind: o.kind, name: o.name, open: o.open, isNew: true}
		if o.kind == gosmo.SymmetricKeyByPassword {
			switch {
			case passField.Value() == "":
				hint.SetError("Type the new password in Password and Confirm password, then Add.")
				return
			case passField.Value() != passConfirm.Value():
				hint.SetError("Passwords do not match.")
				return
			}
			e.password = passField.Value()
			clearPasswords()
		} else {
			for _, x := range edits {
				if x.kind != o.kind || x.name != o.name {
					continue
				}
				if x.pendingRemove {
					// Undo the Remove rather than dropping and re-adding.
					x.pendingRemove = false
					hint.Clear()
					resetGrid(grid, symKeyEncColumns, rowsFor(), len(visible())-1)
					syncRemove()
					return
				}
				hint.Set("The key is already encrypted by " + strings.ToLower(o.label[:1]) + o.label[1:] + ".")
				return
			}
		}
		hint.Clear()
		edits = append(edits, e)
		resetGrid(grid, symKeyEncColumns, rowsFor(), len(visible())-1)
		syncRemove()
	})

	removeBtn = widgets.NewButton("Remove", func() {
		vis := visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			hint.Set("Select an encryption in the grid above to remove it.")
			return
		}
		if len(vis) <= 1 {
			hint.Set("A symmetric key must keep at least one encryption.")
			return
		}
		e := vis[i]
		switch {
		case e.isNew:
			edits = slices.DeleteFunc(edits, func(x *symKeyEncEdit) bool { return x == e })
		case e.kind == gosmo.SymmetricKeyByPassword:
			if passField.Value() == "" {
				hint.SetError("Type the password to remove in Password, then Remove — the server finds the encryption by its password.")
				return
			}
			e.password = passField.Value()
			e.pendingRemove = true
			clearPasswords()
		case e.name == "" || e.kind == "" || e.kind == gosmo.SymmetricKeyByMasterKey:
			hint.SetError("This encryption cannot be removed from here: its encryptor is not one you can see.")
			return
		case e.kind == gosmo.SymmetricKeyBySymmetricKey && e.open == nil:
			hint.SetError("Symmetric key " + e.name + " cannot be opened without a password, and removing it needs it open — use a query window.")
			return
		default:
			e.pendingRemove = true
		}
		hint.Clear()
		resetGrid(grid, symKeyEncColumns, rowsFor(), 0)
		syncRemove()
	})
	syncRemove()

	gridRow := propsheet.NewGridRow(grid, 8)
	gridRow.DirtyFn = func() bool {
		return slices.ContainsFunc(edits, func(e *symKeyEncEdit) bool { return e.isNew || e.pendingRemove })
	}
	gridRow.RevertFn = func() {
		edits = slices.DeleteFunc(edits, func(e *symKeyEncEdit) bool { return e.isNew })
		for _, e := range edits {
			e.pendingRemove, e.password = false, ""
		}
		resetGrid(grid, symKeyEncColumns, rowsFor(), 0)
		hint.Clear()
		syncRemove()
	}

	f := propsheet.NewForm(
		propsheet.Section("Encrypted by"),
		gridRow,
		addSelect,
		passField, passConfirm,
		propsheet.Buttons(addBtn, removeBtn),
		hint,
		propsheet.Note("A password is added from Password and Confirm password, and removed by typing it in Password first. "+
			"A symmetric key is offered only when it opens with no password typed. The last encryption cannot be removed."),
		propsheet.Section("Open the key with"),
		decSelect,
		decPass,
		propsheet.Note("Each change opens the key first, in the same batch. A certificate or asymmetric key whose private key "+
			"the master key protects needs no password; a password, or a private key protected by one, is typed here. "+
			"Opening the key with a certificate or asymmetric key, and removing an encryption by one, needs CONTROL on it."),
	)

	apply := func(ctx context.Context) error {
		var adds, removes []*symKeyEncEdit
		for _, e := range edits {
			switch {
			case e.isNew && !e.pendingRemove:
				adds = append(adds, e)
			case e.pendingRemove && !e.isNew:
				removes = append(removes, e)
			}
		}
		if len(adds)+len(removes) == 0 {
			return nil
		}
		if len(decOpts) == 0 {
			return fmt.Errorf("none of this key's encryptions can open it here — it needs a visible certificate or asymmetric key with a private key, a password, or a symmetric key that opens without one")
		}
		o := decOpts[decSelect.Selected()]
		dec, decPassword := o.dec, ""
		if o.needsPassword {
			if decPassword = decPass.Value(); decPassword == "" {
				return fmt.Errorf("type the password for %s in Decryption password", strings.ToLower(o.label[:1])+o.label[1:])
			}
			dec.Password = scriptSafePassword(ctx, decPassword)
		}
		// Removing the decryptor's own encryption goes last: each change
		// opens the key afresh, and once it is gone nothing opens it this way.
		opensWith := func(e *symKeyEncEdit) bool {
			if e.kind != dec.Kind {
				return false
			}
			if e.kind == gosmo.SymmetricKeyByPassword {
				return e.password == decPassword
			}
			return e.name == dec.Name
		}
		if i := slices.IndexFunc(removes, opensWith); i >= 0 {
			last := removes[i]
			removes = append(slices.Delete(removes, i, i+1), last)
		}
		for _, e := range adds {
			if err := ref.AddEncryption(ctx, e.encryptor(ctx), dec); err != nil {
				return err
			}
		}
		for _, e := range removes {
			if err := ref.DropEncryption(ctx, e.encryptor(ctx), dec); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}
