package tui

import (
	"context"
	"testing"

	"github.com/radix29/gosmo"
)

func TestNextPermStateCycles(t *testing.T) {
	// Four states, and the cycle must close — a cycle that doesn't return to
	// its start leaves a cell the user cannot put back.
	got := permStateNone
	want := []string{permStateGrant, permStateGrantWith, permStateDeny, permStateNone}
	for i, w := range want {
		got = nextPermState(got)
		if got != w {
			t.Fatalf("step %d: state = %q, want %q", i+1, got, w)
		}
	}
}

func TestDisplayPermState(t *testing.T) {
	cases := map[string]string{
		permStateNone:      "(none)",
		permStateGrant:     "Grant",
		permStateGrantWith: "Grant With Grant",
		permStateDeny:      "Deny",
	}
	for state, want := range cases {
		if got := displayPermState(state); got != want {
			t.Errorf("displayPermState(%q) = %q, want %q", state, got, want)
		}
	}
}

// The modifiers are the whole point of permTransition: a transition *out of*
// Grant With Grant must carry CASCADE, or SQL Server refuses it outright.
func TestPermTransitionModifiers(t *testing.T) {
	cases := []struct {
		name       string
		orig, want string
		verb       string
		opts       gosmo.PermissionOptions
	}{
		{"none to grant", permStateNone, permStateGrant, "GRANT", gosmo.PermissionOptions{}},
		{"grant to with-grant", permStateGrant, permStateGrantWith, "GRANT",
			gosmo.PermissionOptions{WithGrantOption: true}},
		{"with-grant down to grant", permStateGrantWith, permStateGrant, "REVOKE",
			gosmo.PermissionOptions{GrantOptionOnly: true}},
		{"with-grant to deny", permStateGrantWith, permStateDeny, "DENY",
			gosmo.PermissionOptions{Cascade: true}},
		{"with-grant to none", permStateGrantWith, permStateNone, "REVOKE",
			gosmo.PermissionOptions{Cascade: true}},
		{"grant to deny", permStateGrant, permStateDeny, "DENY", gosmo.PermissionOptions{}},
		{"grant to none", permStateGrant, permStateNone, "REVOKE", gosmo.PermissionOptions{}},
		{"deny to none", permStateDeny, permStateNone, "REVOKE", gosmo.PermissionOptions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb, opts := permTransition(tc.orig, tc.want)
			if verb != tc.verb || opts != tc.opts {
				t.Errorf("permTransition(%q, %q) = %q %+v, want %q %+v",
					tc.orig, tc.want, verb, opts, tc.verb, tc.opts)
			}
		})
	}
}

// The Grant With Grant -> Grant step must not be a plain re-GRANT: that
// leaves the grant option in place and changes nothing, so the user's edit
// would silently do nothing at all.
func TestGrantWithGrantDowngradeIsNotAReGrant(t *testing.T) {
	verb, opts := permTransition(permStateGrantWith, permStateGrant)
	if verb == "GRANT" {
		t.Fatal("downgrade issued a GRANT, which leaves WITH GRANT OPTION standing")
	}
	if !opts.GrantOptionOnly {
		t.Errorf("opts = %+v, want GrantOptionOnly (REVOKE GRANT OPTION FOR)", opts)
	}
}

func TestApplyPermChangeSkipsNoOps(t *testing.T) {
	calls := 0
	apply := func(context.Context, string, gosmo.PermissionOptions, string, string) error {
		calls++
		return nil
	}
	for _, s := range []string{permStateNone, permStateGrant, permStateGrantWith, permStateDeny} {
		if err := applyPermChange(context.Background(), apply, s, s, "SELECT", "app"); err != nil {
			t.Fatalf("applyPermChange: %v", err)
		}
	}
	if calls != 0 {
		t.Errorf("an unchanged state issued %d statement(s), want 0", calls)
	}

	if err := applyPermChange(context.Background(), apply, permStateNone, permStateGrant, "SELECT", "app"); err != nil {
		t.Fatalf("applyPermChange: %v", err)
	}
	if calls != 1 {
		t.Errorf("a real change issued %d statement(s), want 1", calls)
	}
}

func TestMatchesFilter(t *testing.T) {
	if !matchesFilter("", "anything") {
		t.Error("an empty filter must match everything")
	}
	if !matchesFilter("  ", "anything") {
		t.Error("a whitespace-only filter must match everything")
	}
	if !matchesFilter("sel", "SELECT") {
		t.Error("filtering must be case-insensitive")
	}
	if !matchesFilter("role", "app_reader", "DATABASE_ROLE") {
		t.Error("a match in any field must count")
	}
	if matchesFilter("zzz", "app_reader", "DATABASE_ROLE") {
		t.Error("a term matching no field must not match")
	}
}
