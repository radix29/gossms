// Package cmd holds the command line's command handlers, one file per command
// group. A handler turns config, gosmo or query results into an
// output.Result; it formats nothing and chooses no exit code beyond the
// *output.Error it returns.
package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// Deps is what the handlers reach the outside world through, so tests run
// them without a config directory or a server.
type Deps struct {
	// LoadConfig reads the saved connections (config.LoadReadOnly).
	LoadConfig func() (*config.Config, error)
	// Connect dials (db.Connect).
	Connect connect.ConnectFunc
}

// ProfileCommands is the profile group: its node and its commands.
func ProfileCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"profile"},
			Summary: "List, show and test saved connection profiles",
			Description: "A profile is a saved connection named in the TUI: select it under Recent Connections in the Connect dialog " +
				"and choose Profile.... The command line only reads profiles; it never writes the configuration.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"profile", "list"},
			Summary: "List the saved profiles: server, database, authentication, ceiling, saved password",
			Description: "Lists the named profiles, sorted by name. --all adds the unnamed recent connections, most recent first, " +
				"named by their generated name, which --profile also accepts. Passwords are never shown, only whether one is saved.",
			Options: []spec.Option{
				{Name: "all", Type: spec.TypeBool, Help: "Also list unnamed recent connections"},
			},
			Class: spec.ClassRead,
			Run:   d.profileList,
			Examples: []string{
				"gossms profile list",
				"gossms profile list --all -o json",
			},
		},
		{
			Path:    []string{"profile", "show"},
			Summary: "Show one profile's settings, its operation ceiling and whether its saved password opens",
			Args:    []spec.Arg{{Name: "name", Help: "Profile name, or a recent connection's generated name", Required: true}},
			Class:   spec.ClassRead,
			Run:     d.profileShow,
			Examples: []string{
				"gossms profile show dev",
				"gossms profile show dev -o json",
			},
		},
		{
			Path:    []string{"profile", "test"},
			Summary: "Connect through a profile and report the server version, edition, login and latency",
			Description: "Connects as every command will: the profile, with any connection option given here laid over it. " +
				"An option that changes the server, port, login, authentication, Entra tenant or client, or weakens TLS " +
				"does not send the profile's saved password; pass --password-env or --password-stdin then.",
			Args:    []spec.Arg{{Name: "name", Help: "Profile name, or a recent connection's generated name", Required: true}},
			Options: connect.Options(),
			Class:   spec.ClassRead,
			Run:     d.profileTest,
			Examples: []string{
				"gossms profile test dev",
				"gossms profile test dev --database master -o json",
			},
		},
	}
}

func (d Deps) load() (*config.Config, error) {
	cfg, err := d.LoadConfig()
	if err != nil {
		return nil, output.NewError(output.CodeGeneralError, err.Error(),
			"The TUI repairs an unparseable configuration on its next start (it keeps a copy aside).")
	}
	return cfg, nil
}

// passwordState is a saved connection's password as profile list/show report
// it: never the password, only whether there is one the command line can use.
func passwordState(c config.Connection) string {
	switch {
	case c.PasswordUnreadable():
		return "unreadable"
	case c.Password != "":
		return "saved"
	default:
		return "none"
	}
}

func ptr(s string) *string { return &s }

func portCell(p int) *string {
	if p == 0 {
		return nil
	}
	return ptr(strconv.Itoa(p))
}

func (d Deps) profileList(_ context.Context, inv *spec.Invocation) (*output.Result, error) {
	cfg, err := d.load()
	if err != nil {
		return nil, err
	}
	set := output.Set{Columns: []output.Column{
		{Name: "name"}, {Name: "kind"}, {Name: "server"}, {Name: "port", Nullable: true}, {Name: "database"},
		{Name: "auth"}, {Name: "user"}, {Name: "max_allow", Nullable: true}, {Name: "password"},
	}}
	row := func(name, kind string, c config.Connection, maxAllow *string) []*string {
		return []*string{ptr(name), ptr(kind), ptr(c.Server), portCell(c.Port), ptr(c.Database),
			ptr(connect.AuthName(c.AuthMethod)), ptr(c.User), maxAllow, ptr(passwordState(c))}
	}
	for _, c := range cfg.Profiles() {
		set.Rows = append(set.Rows, row(c.Profile, "profile", c, ptr(string(c.Ceiling()))))
	}
	if inv.Values["all"].(bool) {
		for _, c := range cfg.MatchByServer("") {
			if c.Profile == "" {
				set.Rows = append(set.Rows, row(c.GeneratedName(), "recent", c, nil))
			}
		}
	}
	set.TotalRowCount = len(set.Rows)
	res := &output.Result{Sets: []output.Set{set}}
	if len(set.Rows) == 0 {
		res.Messages = append(res.Messages, output.Message{Type: "info", Text: noProfiles(cfg)})
	}
	return res, nil
}

// noProfiles says how to make one, since an empty list is the first thing a
// new user meets.
func noProfiles(cfg *config.Config) string {
	if len(cfg.Connections) > 0 {
		return fmt.Sprintf("No profiles yet; %d recent connection(s) can be named in the TUI (Connect dialog, Profile...), "+
			"or used by their generated name: `gossms profile list --all`.", len(cfg.Connections))
	}
	return "No saved connections. Connect once in the TUI, then name the connection there (Connect dialog, Profile...)."
}

func (d Deps) profileShow(_ context.Context, inv *spec.Invocation) (*output.Result, error) {
	cfg, err := d.load()
	if err != nil {
		return nil, err
	}
	c, lerr := cfg.ProfileByName(inv.Args[0])
	if lerr != nil {
		return nil, connect.ProfileNotFound(cfg, inv.Args[0])
	}
	kind, name, maxAllow := "recent", c.GeneratedName(), ""
	if c.Profile != "" {
		kind, name, maxAllow = "profile", c.Profile, string(c.Ceiling())
	}
	port := ""
	if c.Port != 0 {
		port = strconv.Itoa(c.Port)
	}
	res := &output.Result{Fields: []output.Field{
		{Key: "name", Value: name},
		{Key: "kind", Value: kind},
		{Key: "generatedName", Value: c.GeneratedName()},
		{Key: "server", Value: c.Server},
		{Key: "port", Value: port},
		{Key: "database", Value: c.Database},
		{Key: "auth", Value: connect.AuthName(c.AuthMethod)},
		{Key: "user", Value: c.User},
		{Key: "tenantId", Value: c.TenantID},
		{Key: "clientId", Value: c.ClientID},
		{Key: "encrypt", Value: string(cmpOr(c.Encrypt, config.EncryptOptional))},
		{Key: "trustServerCertificate", Value: strconv.FormatBool(c.TrustServerCertificate)},
		{Key: "hostNameInCertificate", Value: c.HostNameInCertificate},
		{Key: "extraProperties", Value: extraKeys(c.ExtraProperties)},
		{Key: "maxAllow", Value: maxAllow},
		{Key: "password", Value: passwordState(c)},
	}}
	if c.PasswordUnreadable() {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"The saved password cannot be opened (the key file in %s is missing or replaced, or the entry was edited by hand); "+
				"reconnect in the TUI with Remember Password ticked to save it again.", config.ConfigDir()))
	}
	return res, nil
}

func cmpOr[T comparable](a, b T) T {
	var zero T
	if a != zero {
		return a
	}
	return b
}

// extraKeys is the Extra Properties' keys only: a value may be anything the
// user put there, and profile show never prints what could be a secret.
func extraKeys(extra string) string {
	props, err := db.ParseExtraProperties(extra)
	if err != nil {
		return "(unparseable)"
	}
	var keys []string
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

func (d Deps) profileTest(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	cfg, err := d.load()
	if err != nil {
		return nil, err
	}
	t, err := connect.Resolve(inv, cfg, inv.Args[0])
	if err != nil {
		return nil, err
	}
	res := &output.Result{Server: connect.Address(t.Conn), Database: t.Conn.Database}
	start := time.Now()
	sc, err := connect.Open(ctx, t, d.Connect)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	latency := time.Since(start)
	info := sc.Server.Info()
	if info == nil {
		info = new(gosmo.ServerInfo)
	}
	res.Fields = []output.Field{
		{Key: "profile", Value: t.Profile},
		{Key: "serverName", Value: info.Name},
		{Key: "productVersion", Value: info.ProductVersion},
		{Key: "productLevel", Value: info.ProductLevel},
		{Key: "edition", Value: info.Edition},
		{Key: "platform", Value: info.Platform},
		{Key: "login", Value: info.Login},
		{Key: "connectedDatabase", Value: cmpOr(sc.Server.InitialDatabase(), t.Conn.Database)},
		{Key: "auth", Value: connect.AuthName(t.Conn.AuthMethod)},
		{Key: "passwordSource", Value: t.Password},
		{Key: "maxAllow", Value: string(t.Ceiling)},
		{Key: "latencyMs", Value: strconv.FormatInt(latency.Milliseconds(), 10)},
	}
	if t.SavedPasswordDropped {
		res.Warnings = append(res.Warnings,
			"The profile's saved password was not sent: an override changed the server, login, authentication or TLS.")
	}
	return res, nil
}
