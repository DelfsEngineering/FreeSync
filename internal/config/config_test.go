package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFile_multiFileExample(t *testing.T) {
	path := writeConfigFile(t, `{
  "defaults": { "primaryKey": "id", "modifiedField": "ModificationTimestamp" },
  "files": [
    {
      "id": "betterforms_prod",
      "servers": [
        { "id": "blue", "url": "https://blue.example/db", "username": "u", "password": "p" },
        { "id": "green", "url": "https://green.example/db", "username": "u", "password": "p" }
      ],
      "tables": [
        { "name": "People", "ignoreFields": ["thumbURL"] }
      ]
    }
  ]
}`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 1 {
		t.Fatalf("files: got %d want 1", len(c.Files))
	}
	if c.Files[0].ID != "betterforms_prod" {
		t.Fatalf("file id: got %q", c.Files[0].ID)
	}
	if len(c.Files[0].Servers) != 2 || c.Files[0].Servers[0].ID != "blue" || c.Files[0].Servers[1].ID != "green" {
		t.Fatal("server ids")
	}
	if c.Defaults.PrimaryKey != "id" {
		t.Fatal("defaults")
	}
	if len(c.Files[0].Tables) == 0 || len(c.Files[0].Tables[0].IgnoreFields) != 1 || c.Files[0].Tables[0].IgnoreFields[0] != "thumbURL" {
		t.Fatalf("ignoreFields not loaded from example: %+v", c.Files[0].Tables)
	}
}

func TestValidate_duplicateFileIDs(t *testing.T) {
	path := writeConfigFile(t, `{
  "files": [
    {
      "id": "same",
      "servers": [
        { "id": "blue", "url": "https://blue.example/db1", "username": "u", "password": "p" },
        { "id": "green", "url": "https://green.example/db1", "username": "u", "password": "p" }
      ],
      "tables": [{ "name": "People" }]
    },
    {
      "id": "same",
      "servers": [
        { "id": "blue", "url": "https://blue.example/db2", "username": "u", "password": "p" },
        { "id": "green", "url": "https://green.example/db2", "username": "u", "password": "p" }
      ],
      "tables": [{ "name": "Forms" }]
    }
  ]
}`)
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestLoadFile_AllowsOmittedTablesForAutoDiscovery(t *testing.T) {
	path := writeConfigFile(t, `{
  "files": [
    {
      "id": "auto",
      "servers": [
        { "id": "blue", "url": "https://blue.example/db", "username": "u", "password": "p" },
        { "id": "green", "url": "https://green.example/db", "username": "u", "password": "p" }
      ]
    }
  ]
}`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 1 || len(c.Files[0].Tables) != 0 {
		t.Fatalf("expected auto-discovery config with no explicit tables, got %+v", c.Files)
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

func TestBootstrapMode_defaultAndBinary(t *testing.T) {
	c := &Config{}
	if c.BootstrapBinary() {
		t.Fatal("default bootstrap mode should be fixed")
	}
	c.BootstrapMode = "binary"
	if !c.BootstrapBinary() {
		t.Fatal("bootstrapMode=binary should enable binary bootstrap")
	}
	c.BootstrapMode = "bogus"
	if c.BootstrapBinary() {
		t.Fatal("unknown bootstrap mode should fall back to fixed")
	}
}

func TestPKMod_UsesFileDefaultsOverride(t *testing.T) {
	c := &Config{
		Defaults: Defaults{PrimaryKey: "id", ModifiedField: "ModificationTimestamp"},
	}
	f := FileConfig{
		Defaults: Defaults{PrimaryKey: "uuid", ModifiedField: "UpdatedAt"},
	}
	pk, mod := c.PKMod(f, TableSpec{Name: "People"})
	if pk != "uuid" || mod != "UpdatedAt" {
		t.Fatalf("got pk=%q mod=%q", pk, mod)
	}
}
