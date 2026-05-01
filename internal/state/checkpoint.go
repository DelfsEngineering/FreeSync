// Package state persists sync checkpoints (SPEC: SQLite in production; JSON file for early builds).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type checkpointDisk struct {
	Tables map[string]string `json:"tables"` // RFC3339 nanoseconds
}

// LoadSafeThrough reads checkpoint file (if any) and returns safe-through time for table.
func LoadSafeThrough(path, table string) (time.Time, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	var d checkpointDisk
	if err := json.Unmarshal(b, &d); err != nil {
		return time.Time{}, false, fmt.Errorf("checkpoint json: %w", err)
	}
	if d.Tables == nil {
		return time.Time{}, false, nil
	}
	s, ok := d.Tables[table]
	if !ok || s == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t, true, nil
}

// SaveSafeThrough merges into checkpoint file and writes atomically (best-effortDir).
func SaveSafeThrough(path, table string, ts time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var d checkpointDisk
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &d)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if d.Tables == nil {
		d.Tables = make(map[string]string)
	}
	d.Tables[table] = ts.UTC().Format(time.RFC3339Nano)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
