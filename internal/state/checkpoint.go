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
	Files map[string]checkpointFile `json:"files"`
}

type checkpointFile struct {
	Tables map[string]string `json:"tables"` // RFC3339 nanoseconds
}

// LoadSafeThrough reads checkpoint file (if any) and returns safe-through time for file/table.
func LoadSafeThrough(path, fileID, table string) (time.Time, bool, error) {
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
	if d.Files == nil {
		return time.Time{}, false, nil
	}
	fileEntry, ok := d.Files[fileID]
	if !ok || fileEntry.Tables == nil {
		return time.Time{}, false, nil
	}
	s, ok := fileEntry.Tables[table]
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
func SaveSafeThrough(path, fileID, table string, ts time.Time) error {
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
	if d.Files == nil {
		d.Files = make(map[string]checkpointFile)
	}
	fileEntry := d.Files[fileID]
	if fileEntry.Tables == nil {
		fileEntry.Tables = make(map[string]string)
	}
	fileEntry.Tables[table] = ts.UTC().Format(time.RFC3339Nano)
	d.Files[fileID] = fileEntry
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
