package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpoint_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-state.json")
	fileID := "betterforms_prod"
	tab := "Contacts"
	ts := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)

	if err := SaveSafeThrough(path, fileID, tab, ts); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadSafeThrough(path, fileID, tab)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !got.Equal(ts) {
		t.Fatalf("time mismatch: %v vs %v", got, ts)
	}
	_, okOther, _ := LoadSafeThrough(path, fileID, "Other")
	if okOther {
		t.Fatal("expected missing table")
	}
}

func TestCheckpoint_IsolatesSameTableAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-state.json")
	ts1 := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)
	ts2 := time.Date(2026, 5, 3, 16, 45, 0, 0, time.UTC)

	if err := SaveSafeThrough(path, "betterforms_prod", "Forms", ts1); err != nil {
		t.Fatal(err)
	}
	if err := SaveSafeThrough(path, "other_prod", "Forms", ts2); err != nil {
		t.Fatal(err)
	}

	got1, ok1, err := LoadSafeThrough(path, "betterforms_prod", "Forms")
	if err != nil || !ok1 {
		t.Fatalf("load first: ok=%v err=%v", ok1, err)
	}
	got2, ok2, err := LoadSafeThrough(path, "other_prod", "Forms")
	if err != nil || !ok2 {
		t.Fatalf("load second: ok=%v err=%v", ok2, err)
	}
	if !got1.Equal(ts1) || !got2.Equal(ts2) {
		t.Fatalf("got1=%v got2=%v", got1, got2)
	}
}
