package odata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DelfsEngineering/FreeSync/internal/config"
)

// Live test against deng3 (reads ../../config/dev.local.json — gitignored).
// Run: FREESYNC_LIVE=1 go test ./internal/odata -run Live -count=1 -v
func TestLive_fetchPeopleManifest(t *testing.T) {
	if os.Getenv("FREESYNC_LIVE") != "1" {
		t.Skip("set FREESYNC_LIVE=1 and ensure config/dev.local.json exists")
	}
	root := findRoot(t)
	cfgPath := filepath.Join(root, "config", "dev.local.json")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Files) == 0 {
		t.Fatal("config has no files")
	}
	blue, _, err := cfg.Files[0].BlueGreen()
	if err != nil {
		t.Fatal(err)
	}
	cli := &Client{BaseURL: TrimBase(blue.URL), Username: blue.Username, Password: blue.Password}

	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	m, err := FetchManifest(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live manifest rows (24h window): %d", len(m))
	if len(m) < 1 {
		t.Log("warning: no People rows in last 24h — widen clock or seed data")
	}
}

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}
