package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
)

func TestValidateNewEndpoint(t *testing.T) {
	two := []*newEndpointInstance{{name: "ubusql1", local: true}, {name: "ubusql2"}}

	if err := validateNewEndpoint("Hadr_endpoint", "inSecure123", two); err != nil {
		t.Fatalf("a complete configuration was rejected: %v", err)
	}

	tests := []struct {
		name      string
		endpoint  string
		password  string
		instances []*newEndpointInstance
		wantErr   string
	}{
		{"no endpoint name", "  ", "p", two, "endpoint name is required"},
		// Without a master key the certificate's private key has nothing to be
		// encrypted by, and the failure lands halfway through the pipeline on
		// whichever instance got there first.
		{"no master key password", "e", "", two, "master key password is required"},
		// One instance can be given an endpoint, but nothing can then
		// authenticate to it — the exchange needs a second party.
		{"only the local instance", "e", "p",
			[]*newEndpointInstance{{name: "ubusql1", local: true}}, "at least one other instance"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewEndpoint(tt.endpoint, tt.password, tt.instances)
			if err == nil {
				t.Fatalf("accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestEndpointPrincipalNamesMatchOnBothSides(t *testing.T) {
	// The whole exchange turns on both instances deriving the same names from
	// the same instance name: A creates B_Cert owned by B_user/B_login and
	// grants B_login CONNECT, while B presents the certificate it calls
	// B_Cert. A mismatch produces an endpoint that authenticates nothing, with
	// no error at configuration time.
	d := NewNewEndpointDialog(&App{})
	if got := d.certificateName("ubusql2"); got != "ubusql2_Cert" {
		t.Errorf("certificate name = %q, want ubusql2_Cert", got)
	}
	// The names the pipeline builds inline, pinned here so a change to either
	// side has to change this test too.
	if got := endpointPrincipalBase("ubusql2") + "_login"; got != "ubusql2_login" {
		t.Errorf("login name = %q", got)
	}
	if got := endpointPrincipalBase("ubusql2") + "_user"; got != "ubusql2_user" {
		t.Errorf("user name = %q", got)
	}
}

func TestEndpointPrincipalBaseSurvivesANamedInstance(t *testing.T) {
	// @@SERVERNAME on a named instance is HOST\INSTANCE, and the backslash is
	// what makes [HOST\INST_login] the spelling of a Windows principal. The
	// test cluster is all default instances, so this path has never run live —
	// this is the only thing pinning it.
	d := NewNewEndpointDialog(&App{})
	if got := d.certificateName(`WIN10CLI\SQL2019`); got != "WIN10CLI$SQL2019_Cert" {
		t.Errorf("certificate name = %q, want WIN10CLI$SQL2019_Cert", got)
	}
	if got := endpointPrincipalBase(`WIN10CLI\SQL2019`) + "_login"; got != "WIN10CLI$SQL2019_login" {
		t.Errorf("login name = %q, want WIN10CLI$SQL2019_login", got)
	}
	// Two named instances on one host must not collapse to the same principal
	// names — which is what truncating to the host, the way endpointURL does,
	// would do.
	if endpointPrincipalBase(`HOST\A`) == endpointPrincipalBase(`HOST\B`) {
		t.Error("two named instances on one host share a principal base")
	}
	// A default instance is unchanged, which is why every deployment so far
	// ran through this untouched.
	if got := endpointPrincipalBase("ubusql1"); got != "ubusql1" {
		t.Errorf("default instance = %q, want ubusql1", got)
	}
}

func TestRandomPasswordSatisfiesComplexity(t *testing.T) {
	// Nothing ever signs in as these logins, but CREATE LOGIN still enforces
	// the instance's password policy, and a rejected password fails the whole
	// exchange on a peer the user cannot see.
	seen := map[string]bool{}
	for range 50 {
		p, err := randomPassword()
		if err != nil {
			t.Fatalf("randomPassword() error = %v", err)
		}
		if len(p) < 12 {
			t.Errorf("password %q is only %d characters", p, len(p))
		}
		var upper, lower, digit, other bool
		for _, r := range p {
			switch {
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= 'a' && r <= 'z':
				lower = true
			case r >= '0' && r <= '9':
				digit = true
			default:
				other = true
			}
		}
		if !upper || !lower || !digit || !other {
			t.Errorf("password %q lacks a required character class (upper=%v lower=%v digit=%v other=%v)",
				p, upper, lower, digit, other)
		}
		if seen[p] {
			t.Fatalf("randomPassword() repeated %q", p)
		}
		seen[p] = true
	}
}

// The certificate name goes into the AUTHENTICATION clause, which gosmo passes
// through verbatim — so the quoting has to happen in the dialog, and a closing
// bracket in the name has to come out doubled.
func TestEndpointAuthenticationQuotesTheCertificateName(t *testing.T) {
	d, _, _ := newEndpointDialogForTest(t)
	d.certificateName = func(string) string { return "we]ird_Cert" }

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}
	var all []string
	for _, g := range d.scriptedGroups {
		all = append(all, g.stmts...)
	}
	joined := strings.Join(all, "\n")
	if want := "AUTHENTICATION = CERTIFICATE [we]]ird_Cert]"; !strings.Contains(joined, want) {
		t.Errorf("no statement contains %q:\n%s", want, joined)
	}
}

func TestAlwaysOnRootOffersTheDashboardAndTheEndpointFlow(t *testing.T) {
	labels := menuLabels(t, agNode(NodeAlwaysOn, "", ""))
	for _, want := range []string{"Show Dashboard", "New Database Mirroring Endpoint..."} {
		if !slicesContains(labels, want) {
			t.Errorf("Always On root menu = %v, want a %q item", labels, want)
		}
	}
}

// The pipeline treats "already exists" as success, and a German or French
// server words that differently — the messages below are the real ones, from
// SET LANGUAGE Deutsch on 17. The number is what identifies the error; a
// driver error's text does not contain it, so a text match skipped nothing on
// a non-English server and the pipeline failed on a login it had just been
// told was absent.
func TestIsAlreadyExistsMatchesTheNumberInAnyLanguage(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"login, German", mssql.Error{Number: 15025, Message: `Der Serverprinzipal "x_login" ist bereits vorhanden.`}, true},
		{"user, German", mssql.Error{Number: 15023, Message: `Der Benutzer, die Gruppe oder die Rolle "x_user" ist in der aktuellen Datenbank bereits vorhanden.`}, true},
		{"wrapped by gosmo", fmt.Errorf("gosmo: create login: %w", mssql.Error{Number: 15025, Message: "déjà"}), true},
		{"login, English", mssql.Error{Number: 15025, Message: `The server principal 'x_login' already exists.`}, true},
		// A different error that merely mentions the phrase is not the
		// collision: the number wins once there is one.
		{"other number", mssql.Error{Number: 15247, Message: "User does not have permission; the object already exists elsewhere"}, false},
		{"permission denied", mssql.Error{Number: 15247, Message: "User does not have permission to perform this action."}, false},
		{"no SQL error, English text", errors.New("login already exists"), true},
		{"nil", nil, false},
	} {
		if got := isAlreadyExists(c.err); got != c.want {
			t.Errorf("%s: isAlreadyExists = %v, want %v", c.name, got, c.want)
		}
	}
}
