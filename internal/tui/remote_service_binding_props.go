package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// remote_service_binding_props.go is the read-only Properties for a remote
// service binding.
//
// ALTER REMOTE SERVICE BINDING exists and changes the user whose certificate
// authenticates the remote service, or makes the binding anonymous. It is not
// offered here: the user it names has to already own the remote service's
// public certificate, which is a key-management step this build has no page
// for, and a binding pointed at a user with no certificate authenticates
// nothing while looking correct. The page is named in
// prop_page_requires_test.go's pagesThatOnlyRead.
//
// The family exists on Azure SQL Managed Instance and this page works there:
// only CREATE is refused (Msg 41906, at compile time), which is the edition
// gate's business — see edition_gate.go.

func findRemoteServiceBinding(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.RemoteServiceBinding, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.RemoteServiceBindingByName(ctx, name)
}

func remoteServiceBindingPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			b, err := findRemoteServiceBinding(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Remote service binding"),
				propsheet.Static("Name", b.Name),
				propsheet.Static("Owner", b.Owner),
				propsheet.Section("Remote service"),
				propsheet.Static("Remote service", boundOrNone(b.RemoteService)),
				propsheet.Static("User", boundOrNone(b.User)),
				propsheet.Static("Anonymous", boolStr(b.IsAnonymous)),
				propsheet.Note("The user named here authenticates the remote service through the certificate it owns. Changing it is a key-management step rather than a setting, so it is made by script."),
			)
			return f, nil, nil
		},
	}}
}
