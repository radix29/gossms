package scripting

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// Every family from Database to DatabaseMail has a row: a constant without one
// would be a family the command line names and then cannot script.
func TestEveryFamilyHasVerbs(t *testing.T) {
	for f := Database; f <= DatabaseMail; f++ {
		vs := Verbs(f)
		if len(vs) == 0 {
			t.Errorf("family %d has no verbs", f)
		}
		for _, v := range vs {
			if For(f, v) == nil {
				t.Errorf("family %d lists verb %d without a generator", f, v)
			}
		}
	}
	if Verbs(0) != nil || Verbs(DatabaseMail+1) != nil {
		t.Error("a value outside the families has verbs")
	}
}

// SSMS's order: CREATE, ALTER, DROP, DROP And CREATE, then the templates.
func TestVerbOrder(t *testing.T) {
	for _, c := range []struct {
		f    Family
		want []Verb
	}{
		{Database, []Verb{Create, Drop, DropAndCreate}},
		{Table, []Verb{Create, Drop, DropAndCreate, Select, Insert, Update, Delete}},
		{View, []Verb{Create, Alter, Drop, DropAndCreate, Select, Insert, Update, Delete}},
		{StoredProcedure, []Verb{Create, Alter, Drop, DropAndCreate, Execute}},
		{Function, []Verb{Create, Alter, Drop, DropAndCreate, Select}},
		{Trigger, []Verb{Create, Alter, Drop, DropAndCreate}},
		{Index, []Verb{Create, Drop, DropAndCreate, Rebuild, Reorganize, UpdateStatistics}},
		{Statistic, []Verb{Create, Drop, DropAndCreate, UpdateStatistics}},
		{Login, []Verb{Create, Drop, DropAndCreate}},
		{ResourceGovernor, []Verb{Alter}},
		{DatabaseMail, []Verb{Create}},
	} {
		if got := Verbs(c.f); !slices.Equal(got, c.want) {
			t.Errorf("Verbs(%d) = %v, want %v", c.f, got, c.want)
		}
	}
}

// A verb the family lacks is refused before anything is read: srv is nil, so
// reaching a generator would panic.
func TestScriptRefusesAVerbTheFamilyLacks(t *testing.T) {
	_, err := Script(context.Background(), nil, Table, Alter, Target{Schema: "dbo", Name: "t"})
	if !errors.Is(err, ErrNotOffered) {
		t.Errorf("Script(Table, Alter) = %v, want ErrNotOffered", err)
	}
}

func TestOffered(t *testing.T) {
	for _, c := range []struct {
		f        Family
		isSystem bool
		want     bool
	}{
		{Table, false, true},
		{View, true, false},
		{ResourcePool, true, false},
		{Database, true, true},
		{0, false, false},
	} {
		if got := Offered(c.f, c.isSystem); got != c.want {
			t.Errorf("Offered(%d, %v) = %v, want %v", c.f, c.isSystem, got, c.want)
		}
	}
}

func TestAzureRefusesOnlyTheBindingsCreate(t *testing.T) {
	for f := Database; f <= DatabaseMail; f++ {
		for _, v := range Verbs(f) {
			want := f == RemoteServiceBinding && (v == Create || v == DropAndCreate)
			if got := AzureRefuses(f, v); got != want {
				t.Errorf("AzureRefuses(%d, %d) = %v, want %v", f, v, got, want)
			}
		}
	}
}
