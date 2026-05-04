// Package config loads Free Sync JSON configuration (SPEC.md).
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config matches the committed example shape; unknown JSON keys are ignored.
type Config struct {
	Servers         []Server    `json:"servers"`
	Tables          []TableSpec `json:"tables"`
	Defaults        Defaults    `json:"defaults"`
	OverlapMinutes  int         `json:"overlapMinutes"`
	InitialLookback string      `json:"initialLookback"`
	MaxLookback     string      `json:"maxLookback"`
	SchemaMode      string      `json:"schemaMode"`
	BatchSize       int         `json:"batchSize"`
	MaxWorkers      int         `json:"maxWorkers"`
	VerifyMode      string      `json:"verifyMode"`
}

type Server struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type TableSpec struct {
	Name           string   `json:"name"`
	PrimaryKey     string   `json:"primaryKey"`
	ModifiedField  string   `json:"modifiedField"`
	FieldOverrides []string `json:"fieldOverrides"` // optional: extra fields to sync (e.g. calculated) in addition to non-calculated intersection fields
}

type Defaults struct {
	PrimaryKey    string `json:"primaryKey"`
	ModifiedField string `json:"modifiedField"`
}

// LoadFile reads and parses a JSON config file.
func LoadFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks minimal requirements for a runnable sync.
func (c *Config) Validate() error {
	if len(c.Servers) != 2 {
		return fmt.Errorf("need exactly 2 servers, got %d", len(c.Servers))
	}
	for i, s := range c.Servers {
		if s.ID == "" || s.URL == "" || s.Username == "" {
			return fmt.Errorf("servers[%d]: id, url, username required", i)
		}
	}
	if c.Servers[0].ID == c.Servers[1].ID {
		return fmt.Errorf("servers must have distinct ids")
	}
	seen := make(map[string]bool)
	for _, s := range c.Servers {
		seen[s.ID] = true
	}
	if !seen["blue"] || !seen["green"] {
		return fmt.Errorf("servers must include id \"blue\" and \"green\"")
	}
	return nil
}

// Overlap returns overlap as duration (defaults if unset).
func (c *Config) Overlap() string {
	if c.OverlapMinutes <= 0 {
		return "10m"
	}
	return fmt.Sprintf("%dm", c.OverlapMinutes)
}

// PKMod returns primary key and modification field names for a table (defaults apply).
func (c *Config) PKMod(t TableSpec) (pk, mod string) {
	pk = t.PrimaryKey
	if pk == "" {
		pk = c.Defaults.PrimaryKey
	}
	mod = t.ModifiedField
	if mod == "" {
		mod = c.Defaults.ModifiedField
	}
	return pk, mod
}

// ApplyBatchSize returns apply batch size (defaults to 50).
func (c *Config) ApplyBatchSize() int {
	if c.BatchSize <= 0 {
		return 50
	}
	return c.BatchSize
}

// ApplyWorkers returns number of concurrent apply workers (defaults to 8).
func (c *Config) ApplyWorkers() int {
	if c.MaxWorkers <= 0 {
		return 8
	}
	return c.MaxWorkers
}

// VerifyStrict reports whether strict post-apply verification is enabled.
// Supported: "off" (default), "strict".
func (c *Config) VerifyStrict() bool {
	switch c.VerifyMode {
	case "", "off":
		return false
	case "strict":
		return true
	default:
		// Unknown values fall back to default behavior.
		return false
	}
}
