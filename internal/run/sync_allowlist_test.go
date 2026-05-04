package run

import (
	"testing"

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

	al := buildSyncFieldAllowlist(blue, green, nil, "id", "ModificationTimestamp")
	if _, ok := al["email"]; !ok {
		t.Fatal("expected email")
	}
	if _, ok := al["sumField"]; ok {
		t.Fatal("computed field should be excluded without override")
	}

	al2 := buildSyncFieldAllowlist(blue, green, []string{"sumField"}, "id", "ModificationTimestamp")
	if _, ok := al2["sumField"]; !ok {
		t.Fatal("override should include computed field")
	}
}
