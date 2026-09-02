package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Endpoint Properties, driven through fakedb_test.go.
//
// The endpoint under test is neither first nor last in sys.endpoints, and the
// by-name read is scoped with arg: and placed before the list read — see
// endpointByName in explorer_endpoints_test.go for why.

func endpointPropResponses(name string, row []driver.Value, extra ...fakeResponse) []fakeResponse {
	return append([]fakeResponse{endpointByName(name, row), endpointRows()}, extra...)
}

func mirroringDetailResponse() fakeResponse {
	return fakeResponse{
		match: "sys.database_mirroring_endpoints",
		cols:  8,
		rows: [][]driver.Value{
			{"AGEP", int64(5022), "STARTED", "ALL", true, "AES", "CERTIFICATE", "sa"},
		},
	}
}

// The page must show the endpoint it was opened on, not the first row in
// sys.endpoints.
func TestEndpointGeneralLoadsTheSelectedEndpoint(t *testing.T) {
	sc, inst := newFakeConn(t, endpointPropResponses("AGEP", agepRow)...)
	form, apply := loadPage(t, pageEndpointGeneral(sc, "AGEP"), inst)

	if got := staticValue(t, form, "Name"); got != "AGEP" {
		t.Errorf("Name is %q, want the selected endpoint's %q", got, "AGEP")
	}
	if got := staticValue(t, form, "Payload"); got != "DATABASE_MIRRORING" {
		t.Errorf("Payload is %q", got)
	}
	if got := staticValue(t, form, "Port"); got != "5022" {
		t.Errorf("Port is %q", got)
	}
	if got := staticValue(t, form, "State"); got != "Started" {
		t.Errorf("State is %q", got)
	}
	// Start/Stop/Disable and Drop are Object Explorer commands, not an Apply.
	if apply != nil {
		t.Error("the General page has an apply, but an endpoint's writes are immediate commands")
	}
}

// A built-in endpoint's page must say why nothing can be done to it — the
// Object Explorer's own refusal only appears once the user has already tried.
func TestEndpointGeneralExplainsABuiltInEndpoint(t *testing.T) {
	sc, inst := newFakeConn(t, endpointPropResponses("Dedicated Admin Connection", dacRow)...)
	form, _ := loadPage(t, pageEndpointGeneral(sc, "Dedicated Admin Connection"), inst)

	var notes []string
	for _, r := range form.Rows() {
		if n, ok := r.(interface{ Text() string }); ok {
			notes = append(notes, n.Text())
		}
	}
	if !strings.Contains(strings.Join(notes, "\n"), "built-in") {
		t.Errorf("the page does not say the endpoint is built in: %v", notes)
	}
	if got := staticValue(t, form, "Admin endpoint"); got != "Yes" {
		t.Errorf("the Dedicated Admin Connection is not marked as one: %q", got)
	}
	// The built-in TCP endpoints report port 0, which is not a port anything
	// connects to.
	if got := staticValue(t, form, "Port"); got != "" {
		t.Errorf("Port is %q, want it blank", got)
	}
}

// The payload page branches on the endpoint's type inside its own load — a
// page that read the wrong endpoint would show a plausible mirroring page for
// a Service Broker endpoint.
func TestEndpointPayloadPageShowsTheMirroringDetail(t *testing.T) {
	sc, inst := newFakeConn(t, endpointPropResponses("AGEP", agepRow, mirroringDetailResponse())...)
	form, apply := loadPage(t, pageEndpointPayload(sc, "AGEP"), inst)

	if got := staticValue(t, form, "Role"); got != "ALL" {
		t.Errorf("Role is %q", got)
	}
	if got := staticValue(t, form, "Connection auth"); got != "CERTIFICATE" {
		t.Errorf("Connection auth is %q — the page lost how the far end proves who it is", got)
	}
	if got := staticValue(t, form, "Algorithm"); got != "AES" {
		t.Errorf("Algorithm is %q", got)
	}
	if apply != nil {
		t.Error("the payload page has an apply")
	}
}

func TestEndpointPayloadPageShowsTheServiceBrokerDetail(t *testing.T) {
	brokerRow := []driver.Value{int64(65537), "BrokerEP", "sa", "TCP", "SERVICE_BROKER", "DISABLED", false, int64(4022)}
	sc, inst := newFakeConn(t, endpointPropResponses("BrokerEP", brokerRow, fakeResponse{
		match: "sys.service_broker_endpoints",
		cols:  5,
		rows:  [][]driver.Value{{true, int64(32), "NEGOTIATE", "AES", "broker_cert"}},
	})...)
	form, _ := loadPage(t, pageEndpointPayload(sc, "BrokerEP"), inst)

	if got := staticValue(t, form, "Certificate"); got != "broker_cert" {
		t.Errorf("Certificate is %q", got)
	}
	if got := staticValue(t, form, "Forwarding"); got != "Enabled" {
		t.Errorf("Forwarding is %q", got)
	}
	// The size is only shown when forwarding is on; with it off, "0 MB" reads
	// as a configured limit.
	if got := staticValue(t, form, "Forward size (MB)"); got != "32" {
		t.Errorf("Forward size is %q", got)
	}
}

// A TSQL endpoint has no type-specific catalog view at all, so the page must
// say so rather than issue a read that finds nothing and render blank.
func TestEndpointPayloadPageHandlesATSQLEndpoint(t *testing.T) {
	sc, inst := newFakeConn(t, endpointPropResponses("Dedicated Admin Connection", dacRow)...)
	form, _ := loadPage(t, pageEndpointPayload(sc, "Dedicated Admin Connection"), inst)

	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok {
			t.Errorf("a TSQL endpoint drew a payload row %q", sr.Label())
		}
	}
	if reads := inst.Reads("sys.database_mirroring_endpoints"); len(reads) != 0 {
		t.Errorf("a TSQL endpoint read the mirroring view: %v", reads)
	}
}

// Both pages are read-only by nature, not by omission, and
// prop_page_requires_test.go's pagesThatOnlyRead only permits that for a page
// with no apply at all.
func TestEndpointPagesDoNotWrite(t *testing.T) {
	sc, inst := newFakeConn(t, endpointPropResponses("AGEP", agepRow, mirroringDetailResponse())...)

	for _, page := range endpointPropPages(sc, "AGEP") {
		_, apply := loadPage(t, page, inst)
		if apply != nil {
			t.Errorf("page %q has an apply", page.title)
		}
	}
	for _, q := range inst.Statements() {
		if strings.Contains(q, "ALTER ENDPOINT") || strings.Contains(q, "DROP ENDPOINT") {
			t.Errorf("loading Endpoint Properties wrote: %q", q)
		}
	}
}
