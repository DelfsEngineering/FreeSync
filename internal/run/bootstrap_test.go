package run

import (
	"testing"
	"time"
)

func TestFindDivergenceBoundaryBinary(t *testing.T) {
	end := time.Date(2026, 5, 4, 19, 0, 0, 0, time.UTC)
	divergence := end.Add(-6 * time.Hour)

	start, err := findDivergenceBoundaryBinary(end, 24*time.Hour, func(ts time.Time) (bool, error) {
		// Monotonic probe model:
		// window [ts..end] diverges iff ts <= divergence.
		return !ts.After(divergence), nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Result should be close to boundary and never after it (safe side).
	if start.After(divergence) {
		t.Fatalf("start %s should not be after divergence %s", start, divergence)
	}
	if divergence.Sub(start) > 20*time.Minute {
		t.Fatalf("boundary too coarse: got %s want near %s", start, divergence)
	}
}

func TestFindDivergenceBoundaryBinary_NoDivergence(t *testing.T) {
	end := time.Date(2026, 5, 4, 19, 0, 0, 0, time.UTC)
	maxLookback := 24 * time.Hour

	start, err := findDivergenceBoundaryBinary(end, maxLookback, func(ts time.Time) (bool, error) {
		return false, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := end.Add(-maxLookback)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}
