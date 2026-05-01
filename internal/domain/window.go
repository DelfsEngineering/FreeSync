package domain

import "time"

// SyncWindow is the inclusive OData filter window [Start, End] for one sync pass (SPEC).
type SyncWindow struct {
	Start time.Time
	End   time.Time
}

// ComputeSyncWindow derives the processing window from the last verified checkpoint.
// windowStart = safeThrough - overlap; windowEnd = now.
func ComputeSyncWindow(safeThrough time.Time, overlap time.Duration, now time.Time) SyncWindow {
	return SyncWindow{
		Start: safeThrough.Add(-overlap),
		End:   now,
	}
}
