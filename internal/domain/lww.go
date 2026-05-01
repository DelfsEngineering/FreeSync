package domain

import "time"

// CompareModification returns -1 if a is newer, 1 if b is newer, 0 if equal (last-write-wins tie-break).
func CompareModification(a, b time.Time) int {
	switch {
	case a.After(b):
		return -1
	case b.After(a):
		return 1
	default:
		return 0
	}
}
