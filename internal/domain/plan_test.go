package domain

import (
	"testing"
	"time"
)

func TestBuildPlan_copyMissingSide(t *testing.T) {
	t100 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	blue := map[string]time.Time{"1": t100}
	green := map[string]time.Time{}

	ops := BuildPlan(blue, green, nil)
	if len(ops) != 1 || ops[0].Kind != CopyToGreen || ops[0].RecordID != "1" {
		t.Fatalf("got %+v", ops)
	}

	ops = BuildPlan(map[string]time.Time{}, map[string]time.Time{"1": t100}, nil)
	if len(ops) != 1 || ops[0].Kind != CopyToBlue {
		t.Fatalf("got %+v", ops)
	}
}

func TestBuildPlan_newerWins(t *testing.T) {
	t100 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t200 := t100.Add(time.Hour)
	ops := BuildPlan(
		map[string]time.Time{"1": t200},
		map[string]time.Time{"1": t100},
		nil,
	)
	if len(ops) != 1 || ops[0].Kind != CopyToGreen {
		t.Fatalf("blue newer -> CopyToGreen, got %+v", ops)
	}
	ops = BuildPlan(
		map[string]time.Time{"1": t100},
		map[string]time.Time{"1": t200},
		nil,
	)
	if len(ops) != 1 || ops[0].Kind != CopyToBlue {
		t.Fatalf("green newer -> CopyToBlue, got %+v", ops)
	}
}

func TestBuildPlan_deleteWins(t *testing.T) {
	t100 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t300 := t100.Add(3 * time.Hour)
	ops := BuildPlan(
		map[string]time.Time{"1": t100},
		map[string]time.Time{"1": t100.Add(time.Hour)},
		[]DeleteEntry{{RecordID: "1", DeletedAt: t300}},
	)
	if len(ops) != 2 {
		t.Fatalf("want 2 deletes, got %+v", ops)
	}
}

func TestBuildPlan_recordBeatsStaleDelete(t *testing.T) {
	t100 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t500 := t100.Add(5 * time.Hour)
	ops := BuildPlan(
		map[string]time.Time{"1": t500},
		map[string]time.Time{"1": t100},
		[]DeleteEntry{{RecordID: "1", DeletedAt: t100}},
	)
	if len(ops) != 1 || ops[0].Kind != CopyToGreen {
		t.Fatalf("record newer than delete journal -> push, got %+v", ops)
	}
}
