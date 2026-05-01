package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpoint_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-state.json")
	tab := "Contacts"
	ts := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)

	if err := SaveSafeThrough(path, tab, ts); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadSafeThrough(path, tab)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !got.Equal(ts) {
		t.Fatalf("time mismatch: %v vs %v", got, ts)
	}
	_, okOther, _ := LoadSafeThrough(path, "Other")
	if okOther {
		t.Fatal("expected missing table")
	}
}
