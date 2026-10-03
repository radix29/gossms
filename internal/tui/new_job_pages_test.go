package tui

import "testing"

// TestNewJobOwnerDefaultsToTheConnectedLogin pins B14: the Owner row starts on
// the login the connection authenticated as, not on whatever sorts first —
// ##MS_AgentSigningCertificate## on a 2017 instance, which an untouched OK
// made the new job's owner.
func TestNewJobOwnerDefaultsToTheConnectedLogin(t *testing.T) {
	names := []string{"##MS_AgentSigningCertificate##", "sa", `win10cli\radu`}
	for _, tc := range []struct {
		name, collation, login string
		want                   int
	}{
		// SUSER_NAME() can spell a Windows login in another case than
		// sys.server_principals holds it; a case-insensitive server calls
		// them one login.
		{"case-insensitive, other case", "SQL_Latin1_General_CP1_CI_AS", `WIN10CLI\Radu`, 2},
		{"exact", "SQL_Latin1_General_CP1_CI_AS", "sa", 1},
		// On a case-sensitive server they are two logins.
		{"case-sensitive, other case", "Latin1_General_CS_AS", `WIN10CLI\Radu`, 0},
		{"not listed", "SQL_Latin1_General_CP1_CI_AS", "nobody", 0},
	} {
		info := serverInfoResponse()
		info.rows[0][4] = tc.collation
		info.rows[0][13] = tc.login
		sc, _ := newFakeConnFrom(t, []fakeResponse{info, sysInfoResponse()})

		f, _, _, _ := buildNewJobGeneralPage(sc, &njobPrefetch{loginNames: names})
		if got := selectRow(t, f, "Owner").Selected(); got != tc.want {
			t.Errorf("%s: Owner defaults to %q, want %q", tc.name, names[got], names[tc.want])
		}
	}
}
