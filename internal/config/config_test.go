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
