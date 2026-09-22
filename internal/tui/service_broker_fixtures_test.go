package tui

import (
	"database/sql/driver"
	"time"
)

// service_broker_fixtures_test.go scripts the catalog reads the seven Service
// Broker families make, so each family's page test and the Detail Browser's
// tests describe only what they are actually about.
//
// The match strings are taken from gosmo's SELECTs verbatim, aliases included:
// "sys.service_message_types" alone also matches the contract-message read
// that joins it, and "sys.service_queues" alone also matches the service read
// that joins it — either would answer the wrong query with the right number of
// columns, which is a page reading another family's rows and looking fine.

// brokerDB is the database every fixture here lives in.
const brokerDB = "appdb"

func brokerDBResp() fakeResponse { return dbByNameResp(brokerDB, 5) }

// messageTypeRows are two user types and one SQL Server ships: DEFAULT, whose
// id is inside the system range, and a validating type with a schema
// collection so the VALID_XML_WITH_SCHEMA_COLLECTION decoding is exercised.
func messageTypeResp() fakeResponse {
	return fakeResponse{match: "sys.service_message_types mt", cols: 6, rows: [][]driver.Value{
		{int64(14), "DEFAULT", "dbo", "NONE", "", ""},
		{int64(65536), "//claims/Submit", "dbo", "XML", "dbo", "ClaimSchema"},
		{int64(65537), "//claims/Ack", "dbo", "EMPTY", "", ""},
	}}
}

func contractResp() fakeResponse {
	return fakeResponse{match: "sys.service_contracts c", cols: 3, rows: [][]driver.Value{
		{int64(1), "DEFAULT", "dbo"},
		{int64(65538), "//claims/Contract", "dbo"},
	}}
}

// contractMessageResp gives the user contract all three senders, because both
// bits set is SENT BY ANY — the case a one-column reading gets wrong, and the
// one that produces a contract script that does not parse.
func contractMessageResp() fakeResponse {
	return fakeResponse{match: "sys.service_contract_message_usages", cols: 4, rows: [][]driver.Value{
		{int64(65538), "//claims/Ack", false, true},
		{int64(65538), "//claims/Both", true, true},
		{int64(65538), "//claims/Submit", true, false},
	}}
}

// queueResp is three queues in the order the catalog answers in — by schema,
// then name — because a by-name fixture is one of these rows by index.
//
// dbo.ClaimQueue is a user queue taken out of service with a full ACTIVATION
// block that is switched off; QueryNotificationErrorsQueue is one SQL Server
// ships, which is is_ms_shipped and not an id range — object_ids here are
// ordinary, nowhere near the 65536 the other families' systems sit below;
// sales.IdleQueue is a plain running queue with no activation at all.
func queueResp() fakeResponse {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return fakeResponse{match: "FROM   sys.service_queues q", cols: 18, rows: [][]driver.Value{
		{int64(200), "ClaimQueue", "dbo", "dbo",
			false, false, false, true, true, false,
			"[dbo].[ProcessClaims]", "dbo", "ProcessClaims", int64(4), "OWNER", "PRIMARY", created, created},
		{int64(100), "QueryNotificationErrorsQueue", "dbo", "dbo",
			true, true, true, false, true, false, "", "", "", int64(0), "", "PRIMARY", created, created},
		{int64(300), "IdleQueue", "sales", "dbo",
			false, true, true, false, false, false, "", "", "", int64(0), "", "PRIMARY", created, created},
	}}
}

// brokerRowByArg narrows a listing fixture to one row and restricts it to a
// by-name read. Both are needed: substring matching alone cannot tell a
// by-name finder's query from the listing it is a WHERE clause away from, so
// without the arg the finder is answered with the listing and every object on
// the page resolves to whichever row sorted first — which is what a page
// showing the wrong object's properties looks like.
func brokerRowByArg(r fakeResponse, arg string, row int) fakeResponse {
	r.arg = arg
	r.rows = [][]driver.Value{r.rows[row]}
	return r
}

func brokerServiceResp() fakeResponse {
	return fakeResponse{match: "FROM   sys.services s", cols: 5, rows: [][]driver.Value{
		{int64(3), "ServiceBroker", "dbo", "dbo", "ServiceBrokerQueue"},
		{int64(65539), "//claims/Service", "dbo", "dbo", "ClaimQueue"},
	}}
}

func serviceContractResp() fakeResponse {
	return fakeResponse{match: "sys.service_contract_usages", cols: 2, rows: [][]driver.Value{
		{int64(65539), "//claims/Contract"},
		{int64(65539), "DEFAULT"},
	}}
}

// routeResp is AutoCreatedLocal — which sits at route_id 65536, inside the
// user range, in every database — and a TCP route with a lifetime.
func routeResp() fakeResponse {
	return fakeResponse{match: "FROM   sys.routes r", cols: 8, rows: [][]driver.Value{
		{int64(65536), "AutoCreatedLocal", "dbo", "", "", "LOCAL", "", nil},
		{int64(65537), "ClaimRoute", "dbo", "//claims/Service", "",
			"TCP://remote:4022", "TCP://mirror:4022",
			time.Now().UTC().Add(2 * time.Hour)},
	}}
}

func remoteServiceBindingResp() fakeResponse {
	return fakeResponse{match: "sys.remote_service_bindings b", cols: 7, rows: [][]driver.Value{
		{int64(65540), "ClaimBinding", "dbo", "//claims/Remote", "claim_user", false, ""},
	}}
}

// brokerPriorityResp leaves the remote service empty, which is how
// CREATE BROKER PRIORITY spells a criterion it was not given: ANY.
func brokerPriorityResp() fakeResponse {
	return fakeResponse{match: "sys.conversation_priorities p", cols: 6, rows: [][]driver.Value{
		{int64(1), "ClaimPriority", "//claims/Contract", "//claims/Service", "", int64(8)},
	}}
}

// queueCountResp answers the whole-database message-count read. Its match is
// the GROUP BY rather than the DMV's name, which the per-queue count read
// beside it also carries — that one returns a single column, so answering it
// here would be a scan error inside a read whose failure is meant to be
// invisible.
func queueCountResp() fakeResponse {
	return fakeResponse{match: "GROUP  BY it.parent_object_id", cols: 2, rows: [][]driver.Value{
		{int64(200), int64(42)},
	}}
}

// oneQueueCountResp answers BrokerQueue.MessageCount, the per-queue
// read the leaf's Detail Browser view makes.
func oneQueueCountResp() fakeResponse {
	return fakeResponse{match: "WHERE  it.parent_object_id = @p1", cols: 1, rows: [][]driver.Value{
		{int64(42)},
	}}
}

func queueMonitorResp() fakeResponse {
	return fakeResponse{match: "sys.dm_broker_queue_monitors", cols: 5, rows: [][]driver.Value{
		{int64(200), "INACTIVE", int64(0), nil, nil},
	}}
}
