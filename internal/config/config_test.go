package config

import (
	"path/filepath"
	"testing"
)

func TestLoadFile_example(t *testing.T) {
	path := filepath.Join("..", "..", "config", "dev.example.json")
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 2 {
		t.Fatal("servers")
	}
	if c.Servers[0].ID != "blue" || c.Servers[1].ID != "green" {
		t.Fatal("server ids")
	}
	if c.Defaults.PrimaryKey != "id" {
		t.Fatal("defaults")
	}
}

func TestValidate_twoServers(t *testing.T) {
	_, err := LoadFile(filepath.Join("..", "..", "testdata", "config_invalid_one_server.json"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaults_batchWorkers(t *testing.T) {
	c := &Config{}
	if got := c.ApplyBatchSize(); got != 50 {
		t.Fatalf("batch size default: got %d want 50", got)
	}
	if got := c.ApplyWorkers(); got != 8 {
		t.Fatalf("workers default: got %d want 8", got)
	}
	c.BatchSize = 25
	c.MaxWorkers = 8
	if got := c.ApplyBatchSize(); got != 25 {
		t.Fatalf("batch size configured: got %d want 25", got)
	}
	if got := c.ApplyWorkers(); got != 8 {
		t.Fatalf("workers configured: got %d want 8", got)
	}
}

func TestVerifyMode_defaultAndOff(t *testing.T) {
	c := &Config{}
	if c.VerifyStrict() {
		t.Fatal("default verify should be off")
	}
	c.VerifyMode = "off"
	if c.VerifyStrict() {
		t.Fatal("verifyMode=off should disable strict verify")
	}
	c.VerifyMode = "strict"
	if !c.VerifyStrict() {
		t.Fatal("verifyMode=strict should enable strict verify")
	}
	c.VerifyMode = "bogus"
	if c.VerifyStrict() {
		t.Fatal("unknown verify mode should fall back to off")
	}
}
