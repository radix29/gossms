package tui

import "github.com/radix29/gossms/internal/tui/gate"

// withRequires attaches the rights a page's writes need. Declared where the
// page set is assembled rather than inside each page, so one list shows what
// every page of a dialog needs and a new page cannot quietly arrive ungated.
func withRequires(p propPage, in string, rights ...gate.Right) propPage {
	p.requires = rights
	p.requiresIn = in
	return p
}

// withRequiresOn is withRequires for a page whose object- or schema-scoped
// rights need to know which securable to ask about — every page set built on
// gate.ObjectWriteRights(). object is the *table*: SQL Server checks ALTER on the
// table for a change to its index, its statistics or one of its keys, and the
// probe records the table, not the index.
func withRequiresOn(p propPage, in, schema, object string, rights ...gate.Right) propPage {
	p = withRequires(p, in, rights...)
	p.requiresSchema = schema
	p.requiresObject = object
	return p
}
