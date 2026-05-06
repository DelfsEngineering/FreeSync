package run

import (
	"testing"

	"github.com/DelfsEngineering/FreeSync/internal/domain"
	"github.com/DelfsEngineering/FreeSync/internal/odata"
)

func TestBuildSyncFieldAllowlist_nonComputedAndOverrides(t *testing.T) {
	blue := []odata.PropertySpec{
		{Name: "id"},
		{Name: "ModificationTimestamp"},
		{Name: "email"},
		{Name: "sumField", Computed: true},
	}
	green := []odata.PropertySpec{
		{Name: "id"},
		{Name: "ModificationTimestamp"},
		{Name: "email"},
		{Name: "sumField", Computed: true},
	}

	al := buildSyncFieldAllowlist(blue, green, nil, nil, "id", "ModificationTimestamp")
	if _, ok := al["email"]; !ok {
		t.Fatal("expected email")
	}
	if _, ok := al["sumField"]; ok {
		t.Fatal("computed field should be excluded without override")
	}

	al2 := buildSyncFieldAllowlist(blue, green, []string{"sumField"}, nil, "id", "ModificationTimestamp")
	if _, ok := al2["sumField"]; !ok {
		t.Fatal("override should include computed field")
	}
}

func TestBuildSyncFieldAllowlist_ignoreFields(t *testing.T) {
	blue := []odata.PropertySpec{
		{Name: "id"},
		{Name: "ModificationTimestamp"},
		{Name: "name"},
		{Name: "thumbURL"},
	}
	green := []odata.PropertySpec{
		{Name: "id"},
		{Name: "ModificationTimestamp"},
		{Name: "name"},
		{Name: "thumbURL"},
	}

	al := buildSyncFieldAllowlist(blue, green, nil, []string{"thumbURL"}, "id", "ModificationTimestamp")
	if _, ok := al["name"]; !ok {
		t.Fatal("expected normal field to remain syncable")
	}
	if _, ok := al["thumbURL"]; ok {
		t.Fatal("ignored local-generated field should be excluded")
	}
	if _, ok := al["id"]; !ok {
		t.Fatal("expected primary key to remain present")
	}
	if _, ok := al["ModificationTimestamp"]; !ok {
		t.Fatal("expected modification field to remain present")
	}
}

func TestFilterPlanOneWay(t *testing.T) {
	plan := []domain.Op{
		{RecordID: "1", Kind: domain.CopyToBlue},
		{RecordID: "2", Kind: domain.CopyToGreen},
		{RecordID: "3", Kind: domain.DeleteFromBlue},
		{RecordID: "4", Kind: domain.DeleteFromGreen},
	}

	toBlue := filterPlanOneWay(plan, "to-blue")
	if len(toBlue) != 2 || toBlue[0].Kind != domain.CopyToBlue || toBlue[1].Kind != domain.DeleteFromBlue {
		t.Fatalf("to-blue filter mismatch: %+v", toBlue)
	}

	toGreen := filterPlanOneWay(plan, "to-green")
	if len(toGreen) != 2 || toGreen[0].Kind != domain.CopyToGreen || toGreen[1].Kind != domain.DeleteFromGreen {
		t.Fatalf("to-green filter mismatch: %+v", toGreen)
	}

	both := filterPlanOneWay(plan, "")
	if len(both) != len(plan) {
		t.Fatalf("expected bidirectional mode to keep all ops, got %d", len(both))
	}
}

func TestNormalizeOneWayMode(t *testing.T) {
	got, err := normalizeOneWayMode(" To-Green ")
	if err != nil {
		t.Fatalf("normalize one-way mode returned error: %v", err)
	}
	if got != "to-green" {
		t.Fatalf("got %q want to-green", got)
	}
	if _, err := normalizeOneWayMode("backwards"); err == nil {
		t.Fatal("expected invalid mode error")
	}
}
