package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// The EKM section appears only when an enabled provider exists, and its spec
// is nil while SQL Server holds the key.
func TestProviderKeyFields(t *testing.T) {
	if f := newProviderKeyFields(nil); f != nil || f.rows("x") != nil || f.spec() != nil || f.validate() != nil {
		t.Error("with no provider the section must be absent and nil-safe")
	}
	if newProviderKeyFields([]*gosmo.CryptographicProvider{{Name: "off", IsEnabled: false}}) != nil {
		t.Error("a disabled provider was offered")
	}
	f := newProviderKeyFields([]*gosmo.CryptographicProvider{{Name: "off"}, {Name: "ekm", IsEnabled: true}})
	if f.spec() != nil {
		t.Error("SQL Server is the default holder; spec must be nil")
	}
	f.heldBy.Edit(1)
	if f.validate() == nil {
		t.Error("a provider key with no name was accepted")
	}
	f.keyName.Edit(" k1 ")
	f.existing.Edit(1)
	got := f.spec()
	if got == nil || *got != (gosmo.ProviderKey{Provider: "ekm", KeyName: "k1", Disposition: gosmo.ProviderOpenExisting}) {
		t.Errorf("spec = %+v", got)
	}
}

// A provider symmetric key takes no encryption and no key material, and needs
// none of the encryptions an ordinary key must have.
func TestValidateNewSymmetricKeyWithProvider(t *testing.T) {
	base := newSymmetricKeyInput{name: "k", provider: true, existingNames: newNameSet("")}
	if err := validateNewSymmetricKey(base); err != nil {
		t.Errorf("a provider key with no encryption was refused: %v", err)
	}
	withPass := base
	withPass.password, withPass.passwordConfirm = "p", "p"
	if validateNewSymmetricKey(withPass) == nil {
		t.Error("a provider key with a password was accepted")
	}
	withSource := base
	withSource.keySource, withSource.sourceConfirm = "s", "s"
	if validateNewSymmetricKey(withSource) == nil {
		t.Error("a provider key with a key source was accepted")
	}
}
