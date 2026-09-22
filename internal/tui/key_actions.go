package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// key_actions.go is what the three key families share beyond their own
// Properties: the Owner row their General pages edit, and Remove Private Key
// for the two with a private key.
//
// Probed on 13 and 17 (2026-09-22), identical — docs/decisions.md § Keys and
// certificates has the table:
//
//   - ALTER AUTHORIZATION drops every explicit permission on the object. Taking
//     ownership yourself needs CONTROL or TAKE OWNERSHIP on it; giving it to
//     another principal also needs IMPERSONATE on them, which only CONTROL on
//     the database carries implicitly. The page is gated on CONTROL on the
//     object — the effective answer folds in CONTROL on the database — and the
//     IMPERSONATE half is left to the server (Msg 15151 naming the principal).
//   - A certificate or asymmetric key cannot be owned by a role (Msg 15345); a
//     symmetric key can.
//   - REMOVE PRIVATE KEY needs the effective ALTER on the object.

// keyOwnerNote is the Note under a key family's Owner row.
func keyOwnerNote(noun string) propsheet.Row {
	return propsheet.Note("Changing the owner drops every explicit permission on the " + noun +
		". Giving it to another principal needs IMPERSONATE on them, or CONTROL on the database.")
}

// keyOwnerRow is the Owner select for a key family's General page: the
// database's users, and its roles as well when withRoles — only a symmetric
// key may be owned by one. The current owner is kept even when it is not in
// the list (a certificate-mapped user, an application role), so an untouched
// row stays clean.
func keyOwnerRow(ctx context.Context, sc *db.ServerConn, dbName, current string, withRoles bool) (*propsheet.SelectRow, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	users, err := d.Users(ctx)
	if err != nil {
		return nil, err
	}
	var roles []*gosmo.DatabaseRole
	if withRoles {
		if roles, err = d.DatabaseRoles(ctx); err != nil {
			return nil, err
		}
	}
	return selectPreserving("Owner", principalNames(users, roles), current, unknownOwnerItem), nil
}

// keyOwnerApply writes an edited Owner row through change, and does nothing
// for an untouched one.
func keyOwnerApply(sc *db.ServerConn, dbName string, row *propsheet.SelectRow,
	change func(ctx context.Context, d *gosmo.Database, owner string) error) propApply {
	return func(ctx context.Context) error {
		owner, ok := changedTo(row, unknownOwnerItem)
		if !ok {
			return nil
		}
		// DatabaseRef: the write addresses the object by name, and a by-name
		// read of the database is nothing this needs.
		return change(ctx, sc.Server.DatabaseRef(dbName), owner)
	}
}

// removePrivateKeyItem is the Remove Private Key menu item: disabled with its
// own note on a node with no private key, which the server would refuse, and
// gated on the effective ALTER on the object otherwise.
func removePrivateKeyItem(sc *db.ServerConn, node *explorerNode, alter gate.Right, action func()) controls.MenuItem {
	item := controls.MenuItem{Label: "Remove Private Key...", Action: action}
	if !node.data.HasPrivateKey {
		item.Enabled = func() bool { return false }
		item.Note = "no private key"
		return item
	}
	return gate.ItemOn(item, sc, node.data.DBName, "", node.data.Name, alter)
}

// removePrivateKey confirms, then deletes the private key of the certificate
// or asymmetric key node names, and refetches its Details. what is
// "certificate" or "asymmetric key": only a certificate's private key can have
// been backed up first, which changes what the warning can promise.
func (a *App) removePrivateKey(sc *db.ServerConn, node *explorerNode, what string, run func(ctx context.Context, d *gosmo.Database) error) {
	if !a.requireConn(sc) {
		return
	}
	name, dbName := node.data.Name, node.data.DBName
	msg := fmt.Sprintf("Remove the private key of %s %q? This cannot be undone. Nothing it signed can be re-signed, "+
		"and nothing encrypted by it can be decrypted, unless the private key is restored from a backup.", what, name)
	if what == "asymmetric key" {
		msg = fmt.Sprintf("Remove the private key of asymmetric key %q? This cannot be undone, and an asymmetric key's "+
			"private key cannot be backed up first: nothing encrypted by it can be decrypted again.", name)
	}
	title := "Remove Private Key"
	a.confirmDialog.ShowConfirm(title, msg, func(confirmed bool) {
		if !confirmed {
			return
		}
		a.runWithProgress(progressJob{
			title:           title,
			message:         fmt.Sprintf("Removing the private key of %q...", name),
			what:            "removing a private key",
			sc:              sc,
			uninterruptible: "Removing a private key is a single statement that cannot be stopped halfway.",
		}, func(ctx context.Context, _ progressReport) error {
			return run(ctx, sc.Server.DatabaseRef(dbName))
		}, func(err error, _ bool) {
			if err != nil {
				a.setStatus(fmt.Sprintf("Remove private key failed: %v", withPermissionAdvice(err)))
				return
			}
			a.setStatus(fmt.Sprintf("Private key of %s %q removed", what, name))
			node.data.HasPrivateKey = false
			a.detailBrowser.Invalidate(a, node)
		})
	})
}

// providerKeyFields are the Extensible Key Management rows of New Asymmetric
// Key and New Symmetric Key: whether SQL Server or an EKM provider holds the
// new key, and which provider key. Shown only on an instance with a provider
// registered — almost none has one.
//
// Not run against a real provider: no test instance has one (gosmo's
// OPEN-THREADS.md, "Keys FROM PROVIDER have never executed"). The statement
// follows the documented grammar, and the server has the last word.
type providerKeyFields struct {
	heldBy   *propsheet.RadioRow
	provider *propsheet.SelectRow
	keyName  *propsheet.TextRow
	existing *propsheet.RadioRow
}

// Indexes into providerKeyFields.heldBy and .existing.
const (
	keyHeldBySQLServer = 0
	keyProviderNew     = 0
)

// newProviderKeyFields returns nil when there is no provider to offer.
func newProviderKeyFields(providers []*gosmo.CryptographicProvider) *providerKeyFields {
	var names []string
	for _, p := range providers {
		if p.IsEnabled {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return &providerKeyFields{
		heldBy:   propsheet.Radio("Key held by", []string{"SQL Server", "EKM provider"}, keyHeldBySQLServer),
		provider: propsheet.Select("Provider", names, 0),
		keyName:  propsheet.Text("Provider key name", "", 30),
		existing: propsheet.Radio("Provider key", []string{"Create new", "Open existing"}, keyProviderNew),
	}
}

// rows are the fields as one form section; nil-safe.
func (p *providerKeyFields) rows(ignored string) []propsheet.Row {
	if p == nil {
		return nil
	}
	return []propsheet.Row{
		propsheet.Section("Extensible Key Management"),
		p.heldBy, p.provider, p.keyName, p.existing,
		propsheet.Note("A key an EKM provider holds is protected by the provider, so " + ignored + " is not used. " +
			"Open existing maps a key the provider already has, and takes its algorithm from the provider."),
	}
}

// spec is the FROM PROVIDER half of the new key, or nil when SQL Server holds
// it; nil-safe.
func (p *providerKeyFields) spec() *gosmo.ProviderKey {
	if p == nil || p.heldBy.Selected() == keyHeldBySQLServer {
		return nil
	}
	disposition := gosmo.ProviderCreateNew
	if p.existing.Selected() != keyProviderNew {
		disposition = gosmo.ProviderOpenExisting
	}
	return &gosmo.ProviderKey{Provider: p.provider.Value(), KeyName: strings.TrimSpace(p.keyName.Value()), Disposition: disposition}
}

// validate refuses a provider key with no name; nil-safe.
func (p *providerKeyFields) validate() error {
	if s := p.spec(); s != nil && s.KeyName == "" {
		return fmt.Errorf("type the key's name in the provider in Provider key name")
	}
	return nil
}

// readProviders lists the instance's EKM providers for a New dialog's
// prefetch. A failure reads as none: a login that cannot see the providers
// cannot create a key in one either, and the dialog's own purpose — a key SQL
// Server holds — must not fail with it.
func readProviders(ctx context.Context, sc *db.ServerConn) []*gosmo.CryptographicProvider {
	ps, err := sc.Server.CryptographicProviders(ctx)
	if err != nil {
		return nil
	}
	return ps
}
