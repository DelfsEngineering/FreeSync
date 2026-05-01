package domain

import (
	"sort"
	"time"
)

// OpKind is a concrete replication step for one record in one table (manifest phase).
type OpKind int

const (
	CopyToGreen OpKind = iota // upsert green from blue’s payload (hydration later)
	CopyToBlue
	DeleteFromBlue
	DeleteFromGreen
)

func (k OpKind) String() string {
	switch k {
	case CopyToGreen:
		return "CopyToGreen"
	case CopyToBlue:
		return "CopyToBlue"
	case DeleteFromBlue:
		return "DeleteFromBlue"
	case DeleteFromGreen:
		return "DeleteFromGreen"
	default:
		return "OpKind?"
	}
}

// Op is one row-level action after comparing manifests and delete journal (SPEC sync model).
type Op struct {
	RecordID string
	Kind     OpKind
}

// DeleteEntry is a row from SyncDeletes for one table (SPEC).
type DeleteEntry struct {
	RecordID  string
	DeletedAt time.Time
}

// BuildPlan compares blue vs green modification times and optional delete journal (last-write-wins).
// Maps are record id -> ModificationTimestamp from manifests only (same table).
func BuildPlan(blue, green map[string]time.Time, deletes []DeleteEntry) []Op {
	delMax := make(map[string]time.Time)
	for _, d := range deletes {
		if t, ok := delMax[d.RecordID]; !ok || d.DeletedAt.After(t) {
			delMax[d.RecordID] = d.DeletedAt
		}
	}

	ids := unionKeys(blue, green, delMax)
	sort.Strings(ids)

	var out []Op
	for _, id := range ids {
		tb, hb := blue[id]
		tg, hg := green[id]
		td, hd := delMax[id]

		recordMax := maxTime(tb, tg, hb, hg)
		if hd && (!recordMax.IsZero()) && td.After(recordMax) {
			// Delete wins over both record versions.
			if hb {
				out = append(out, Op{RecordID: id, Kind: DeleteFromBlue})
			}
			if hg {
				out = append(out, Op{RecordID: id, Kind: DeleteFromGreen})
			}
			continue
		}
		if hd && recordMax.IsZero() {
			// Tombstone with no live rows (already deleted both sides) — nothing.
			continue
		}
		if !hb && !hg {
			continue
		}
		if hb && !hg {
			out = append(out, Op{RecordID: id, Kind: CopyToGreen})
			continue
		}
		if !hb && hg {
			out = append(out, Op{RecordID: id, Kind: CopyToBlue})
			continue
		}
		// both have record — compare mod times; tie: no-op (deterministic)
		switch CompareModification(tb, tg) {
		case -1:
			out = append(out, Op{RecordID: id, Kind: CopyToGreen})
		case 1:
			out = append(out, Op{RecordID: id, Kind: CopyToBlue})
		case 0:
			// identical timestamps — no push
		}
	}
	return out
}

func maxTime(tb, tg time.Time, hb, hg bool) time.Time {
	var m time.Time
	var set bool
	if hb {
		m, set = tb, true
	}
	if hg {
		if !set || tg.After(m) {
			m = tg
			set = true
		}
	}
	if !set {
		return time.Time{}
	}
	return m
}

func unionKeys(a, b map[string]time.Time, c map[string]time.Time) []string {
	seen := make(map[string]struct{})
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	for k := range c {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
