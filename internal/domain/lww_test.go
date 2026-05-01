package domain

import (
	"testing"
	"time"
)

func TestCompareModification(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	if CompareModification(t2, t1) != -1 {
		t.Fatal("newer first arg should return -1")
	}
	if CompareModification(t1, t2) != 1 {
		t.Fatal("older first arg should return 1")
	}
	if CompareModification(t1, t1) != 0 {
		t.Fatal("equal should return 0")
	}
}
