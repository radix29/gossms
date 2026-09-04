package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// endpointPropPages builds the page set for Endpoint Properties: General and
// Type Properties.
//
// Every page is read-only, and that is the family's shape rather than an
// omission. An endpoint's writes are ALTER ENDPOINT ... STATE and DROP
// ENDPOINT, both immediate Object Explorer commands rather than an Apply, and
// changing a payload's authentication or encryption means rewriting the
// endpoint every replica connects through — which goSSMS does not offer from a
// form. Both pages are named in prop_page_requires_test.go's
// pagesThatOnlyRead.
//
// The payload-specific half is one page that branches inside its own load
// rather than a page chosen per type when the dialog opens: the page list is
// built on the UI goroutine, and reading the endpoint there to decide would
// block the whole application on a round trip.
func endpointPropPages(sc *db.ServerConn, epName string) []propPage {
	return []propPage{
		pageEndpointGeneral(sc, epName),
		pageEndpointPayload(sc, epName),
	}
}

// pageEndpointGeneral is Endpoint Properties > General.
func pageEndpointGeneral(sc *db.ServerConn, epName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			e, err := sc.Server.EndpointByNameContext(ctx, epName)
			if err != nil {
				return nil, nil, err
			}
			rows := []propsheet.Row{
				propsheet.Section("Endpoint"),
				propsheet.Static("Name", e.Name),
				propsheet.Static("Owner", e.Owner),
				propsheet.Static("Protocol", e.Protocol),
				propsheet.Static("Payload", e.Type),
				propsheet.Static("State", endpointStateLabel(e.State)),
				propsheet.Static("Port", endpointPortText(e)),
			}
			if e.IsAdmin {
				rows = append(rows, propsheet.Static("Admin endpoint", yesNo(true)))
			}
			if e.IsSystem {
				rows = append(rows, propsheet.Note("This is one of SQL Server's built-in endpoints. It cannot be started, stopped, disabled or dropped, and it cannot be scripted — neither half of such a script would run."))
			} else {
				rows = append(rows, propsheet.Note("Start, Stop and Disable are on the endpoint's Object Explorer menu — they take effect the moment they run. To change its authentication or encryption, script the endpoint and recreate it."))
			}
			return propsheet.NewForm(rows...), nil, nil
		},
	}
}

// pageEndpointPayload is Endpoint Properties > Type Properties: whatever the
// endpoint's payload adds to the catalog row on the General page.
//
// The mirroring detail is read through gosmo's DatabaseMirroringEndpoint,
// which reads the instance's one mirroring endpoint rather than this one by
// name — an instance can have at most one, so they are the same endpoint.
func pageEndpointPayload(sc *db.ServerConn, epName string) propPage {
	return propPage{
		title: "Type Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			e, err := sc.Server.EndpointByNameContext(ctx, epName)
			if err != nil {
				return nil, nil, err
			}
			switch e.Type {
			case "DATABASE_MIRRORING":
				return endpointMirroringForm(ctx, e)
			case "SERVICE_BROKER":
				return endpointServiceBrokerForm(ctx, e)
			default:
				return propsheet.NewForm(
					propsheet.Section(endpointPayloadTitle(e.Type)),
					propsheet.Note("A "+endpointPayloadTitle(e.Type)+" endpoint carries no settings beyond the ones on the General page."),
				), nil, nil
			}
		},
	}
}

// endpointPayloadTitle names a payload the way SSMS does.
func endpointPayloadTitle(payload string) string {
	switch payload {
	case "TSQL":
		return "TSQL"
	case "DATABASE_MIRRORING":
		return "Database Mirroring"
	case "SERVICE_BROKER":
		return "Service Broker"
	case "":
		return "Endpoint"
	default:
		return payload
	}
}

func endpointMirroringForm(ctx context.Context, e *gosmo.Endpoint) (*propsheet.Form, propApply, error) {
	d, err := e.MirroringDetailContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if d == nil {
		return propsheet.NewForm(
			propsheet.Section("Database mirroring"),
			propsheet.Note("The mirroring detail for this endpoint could not be read."),
		), nil, nil
	}
	return propsheet.NewForm(
		propsheet.Section("Database mirroring"),
		propsheet.Static("Role", d.Role),
		propsheet.Static("Connection auth", d.ConnectionAuth),
		propsheet.Static("Encryption", yesNo(d.IsEncryptionEnabled)),
		propsheet.Static("Algorithm", d.EncryptionAlgorithm),
		propsheet.Section("Address"),
		propsheet.Static("Endpoint URL", d.URL()),
	), nil, nil
}

func endpointServiceBrokerForm(ctx context.Context, e *gosmo.Endpoint) (*propsheet.Form, propApply, error) {
	d, err := e.ServiceBrokerDetailContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if d == nil {
		return propsheet.NewForm(
			propsheet.Section("Service Broker"),
			propsheet.Note("The Service Broker detail for this endpoint could not be read."),
		), nil, nil
	}
	rows := []propsheet.Row{
		propsheet.Section("Service Broker"),
		propsheet.Static("Connection auth", d.ConnectionAuth),
		propsheet.Static("Certificate", d.CertificateName),
		// "Algorithm", not "Encryption", so the label means the same thing it
		// does on the mirroring page above, where "Encryption" carries the
		// yes/no. ServiceBrokerDetail has no enabled flag of its own — the
		// scripter derives one from this very field — and deriving it here too
		// would put that rule in two places.
		propsheet.Static("Algorithm", d.EncryptionAlgorithm),
		propsheet.Section("Message forwarding"),
		propsheet.Static("Forwarding", enabledText(d.IsMessageForwardingEnabled)),
	}
	// The size only means anything when forwarding is on, and "0 MB" beside a
	// disabled forwarder reads as a configured limit.
	if d.IsMessageForwardingEnabled {
		rows = append(rows, propsheet.Static("Forward size (MB)", strconv.Itoa(d.MessageForwardingSize)))
	}
	return propsheet.NewForm(rows...), nil, nil
}
