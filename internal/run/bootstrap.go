package run

import "time"

// findDivergenceBoundaryBinary finds the latest start timestamp that still
// indicates divergence in [start..end] using a monotonic probe function.
//
// Probe contract:
// - returns true if windows starting at ts are divergent
// - expected monotonic behavior: older starts are at least as divergent as newer
func findDivergenceBoundaryBinary(end time.Time, maxLookback time.Duration, probe func(time.Time) (bool, error), logf func(string, ...any)) (time.Time, error) {
	if maxLookback <= 0 {
		maxLookback = 90 * 24 * time.Hour
	}
	low := end.Add(-maxLookback)
	high := end

	lowDiv, err := probe(low)
	if err != nil {
		return time.Time{}, err
	}
	if !lowDiv {
		// No divergence in the max lookback window.
		if logf != nil {
			logf("binary bootstrap: no divergence in lookback; using %s", low.Format(time.RFC3339))
		}
		return low, nil
	}

	// Invariant for search:
	// - low is divergent
	// - high is potentially non-divergent (end tends toward empty window)
	// Keep the divergent side (safe) for final start.
	const maxIterations = 24
	for i := 0; i < maxIterations; i++ {
		span := high.Sub(low)
		if span <= time.Minute {
			break
		}
		mid := low.Add(span / 2)
		div, err := probe(mid)
		if err != nil {
			return time.Time{}, err
		}
		if logf != nil {
			logf("binary bootstrap: probe %d start=%s divergent=%v", i+1, mid.Format(time.RFC3339), div)
		}
		if div {
			low = mid
		} else {
			high = mid
		}
	}
	return low, nil
}
