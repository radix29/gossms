package tui

import (
	"context"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageServerSecurity(sc *db.ServerConn) propPage {
	return propPage{
		title: "Security",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sec, err := sc.Server.SecurityInfoContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var boolRows []configBoolRow
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			f := propsheet.NewForm(
				propsheet.Section("Server authentication"),
				propsheet.Static("Authentication mode", sec.AuthenticationMode),
				propsheet.Section("Options"),
				cfgBool("contained database authentication", "Allow contained database authentication"),
				cfgBool("cross db ownership chaining", "Cross database ownership chaining"),
				cfgBool("c2 audit mode", "Enable C2 audit tracing"),
				propsheet.Note("Login auditing and the server proxy account are read from the registry, which this build does not access — see SQL Server's own security policy tooling for those."),
			)
			return f, configApply(sc, nil, boolRows), nil
		},
	}
}
