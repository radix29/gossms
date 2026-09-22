package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// route_props.go is Route Properties, the second of the two writable pages in
// the Service Broker tree.
//
// A route is here for the same reason a queue is: where it sends changes in
// operation — a remote instance moves, a mirror takes over, a route is
// repointed at a new address — and every one of those is an ALTER ROUTE
// clause.
//
// The one thing the page cannot do is *clear* a setting. ALTER ROUTE has no
// way to: an empty value is refused by the server and NULL does not parse, so
// a route that must lose its broker instance, its mirror address or its
// lifetime is dropped and created again. Emptying a row here is therefore an
// error naming that, never a silent no-op.
//
// The right is ALTER ANY ROUTE, which permits this page's ALTER and the
// route's Delete alike — unlike a queue, whose two verbs differ.

func findRoute(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.Route, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.RouteByName(ctx, name)
}

func routePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{
		withRequires(pageRouteGeneral(sc, dbName, name), dbName, gate.RouteWriteRights()...),
	}
}

func pageRouteGeneral(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			r, err := findRoute(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}

			remoteService := propsheet.Text("Remote service", r.RemoteService, 40)
			brokerInstance := propsheet.Text("Broker instance", r.BrokerInstance, 40)
			address := propsheet.Text("Address", r.Address, 40)
			mirrorAddress := propsheet.Text("Mirror address", r.MirrorAddress, 40)
			// The remaining lifetime, not the one CREATE ROUTE was given:
			// sys.routes keeps only the expiry instant, so the original number
			// is not recoverable once any time has passed.
			lifetime := propsheet.Int("Lifetime (seconds)", int64(r.LifetimeSeconds()), 0, 2147483647, "sec")

			f := propsheet.NewForm(
				propsheet.Section("Route"),
				propsheet.Static("Name", r.Name),
				propsheet.Static("Owner", r.Owner),
				propsheet.Section("Destination"),
				remoteService, brokerInstance, address, mirrorAddress,
				propsheet.Note("The remote service name is matched byte for byte by the broker, even on a case-insensitive collation. ADDRESS takes a TCP address, LOCAL, or TRANSPORT."),
				propsheet.Section("Lifetime"),
				lifetime,
				propsheet.Static("Expires", routeExpiryText(r)),
				propsheet.Note("ALTER ROUTE can change a setting but never clear one — the server refuses an empty value and NULL does not parse. A route that must lose its broker instance, mirror address or lifetime is dropped and created again."),
			)

			apply := func(ctx context.Context) error {
				s := gosmo.RouteSettings{}
				dirty := false
				set := func(row *propsheet.TextRow, label string, dst **string) error {
					if !row.Dirty() {
						return nil
					}
					v := strings.TrimSpace(row.Value())
					if v == "" {
						return fmt.Errorf("%s cannot be cleared: ALTER ROUTE has no way to remove a setting, "+
							"so a route that must lose it is dropped and created again", label)
					}
					*dst, dirty = &v, true
					return nil
				}
				if err := set(remoteService, "Remote service", &s.RemoteService); err != nil {
					return err
				}
				if err := set(brokerInstance, "Broker instance", &s.BrokerInstance); err != nil {
					return err
				}
				if err := set(address, "Address", &s.Address); err != nil {
					return err
				}
				if err := set(mirrorAddress, "Mirror address", &s.MirrorAddress); err != nil {
					return err
				}
				if lifetime.Dirty() {
					n, err := lifetime.IntValue()
					if err != nil {
						return err
					}
					if n < 1 {
						return fmt.Errorf("a lifetime of %d cannot be sent: LIFETIME must be 1 or more, "+
							"and a route's lifetime cannot be cleared by an ALTER", n)
					}
					secs := int(n)
					s.LifetimeSeconds, dirty = &secs, true
				}
				if !dirty {
					return nil
				}
				// Re-read rather than reusing r, for the reason Queue
				// Properties re-reads: a route dropped between the load and
				// the apply must fail here rather than have an ALTER sent.
				route, err := findRoute(ctx, sc, dbName, name)
				if err != nil {
					return err
				}
				return route.Alter(ctx, s)
			}
			return f, apply, nil
		},
	}
}
