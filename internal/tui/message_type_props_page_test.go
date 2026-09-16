package tui

import (
	"testing"
)

// A message type's validation is two catalog columns decoded into one clause:
// validation_desc says XML for both WELL_FORMED_XML and
// VALID_XML_WITH_SCHEMA_COLLECTION, and only the presence of a schema
// collection tells them apart. A page showing the wrong one describes a
// contract the broker does not enforce.

func TestMessageTypePageShowsTheValidationItsCollectionDecidesTo(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(), brokerRowByArg(messageTypeResp(), "//claims/Submit", 1))
	pages := messageTypePropPages(sc, brokerDB, "//claims/Submit")
	form, apply := loadPage(t, pages[0], inst)

	if apply != nil {
		t.Error("Message Type Properties is read-only; its page returned an apply")
	}
	if got := staticValue(t, form, "Validation"); got != "VALID_XML_WITH_SCHEMA_COLLECTION" {
		t.Errorf("Validation reads %q, want the schema-collection form", got)
	}
	if got := staticValue(t, form, "Schema collection"); got != "dbo.ClaimSchema" {
		t.Errorf("Schema collection reads %q, want the qualified collection name", got)
	}
}

// TestMessageTypePageMarksAShippedTypeSystem. The id range is the whole
// discriminator here — the thirteen schemas.microsoft.com types and DEFAULT
// sit below 65536 — and a page reading it the other way offers a user a
// Delete the server refuses.
func TestMessageTypePageMarksAShippedTypeSystem(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(), brokerRowByArg(messageTypeResp(), "DEFAULT", 0))
	pages := messageTypePropPages(sc, brokerDB, "DEFAULT")
	form, _ := loadPage(t, pages[0], inst)

	if got := staticValue(t, form, "System object"); got != boolStr(true) {
		t.Errorf("System object reads %q for DEFAULT, want %q", got, boolStr(true))
	}
}
