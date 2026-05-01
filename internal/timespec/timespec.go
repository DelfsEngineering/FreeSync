// Package timespec parses config duration strings like "1d", "90d", "10m".
package timespec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse parses "10m", "1h", "1d", "7d", "90d" etc.
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
