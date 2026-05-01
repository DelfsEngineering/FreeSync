package domain

import (
	"testing"
	"time"
)

func TestComputeSyncWindow(t *testing.T) {
	overlap := 10 * time.Minute
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	safeThrough := now.Add(-1 * time.Hour)

	w := ComputeSyncWindow(safeThrough, overlap, now)

	wantStart := safeThrough.Add(-overlap)
	if !w.Start.Equal(wantStart) {
		t.Fatalf("Start = %v, want %v", w.Start, wantStart)
	}
	if !w.End.Equal(now) {
		t.Fatalf("End = %v, want %v", w.End, now)
	}
}

func TestComputeSyncWindow_overlapZero(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	safe := now.Add(-30 * time.Minute)
	w := ComputeSyncWindow(safe, 0, now)
	if !w.Start.Equal(safe) {
		t.Fatalf("with zero overlap Start should equal safeThrough: got %v want %v", w.Start, safe)
	}
}
