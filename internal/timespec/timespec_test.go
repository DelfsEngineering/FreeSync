package timespec

import (
	"testing"
	"time"
)

func TestParse_days(t *testing.T) {
	d, err := Parse("1d")
	if err != nil || d != 24*time.Hour {
		t.Fatalf("1d: %v %v", d, err)
	}
	d, err = Parse("90d")
	if err != nil || d != 90*24*time.Hour {
		t.Fatalf("90d")
	}
}

func TestParse_duration(t *testing.T) {
	d, err := Parse("10m")
	if err != nil || d != 10*time.Minute {
		t.Fatalf("10m %v", err)
	}
}
