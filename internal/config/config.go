// Package config loads Free Sync JSON configuration (SPEC.md).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config matches the committed example shape; unknown JSON keys are ignored.
type Config struct {
	Files           []FileConfig `json:"files"`
	Defaults        Defaults    `json:"defaults"`
	OverlapMinutes  int         `json:"overlapMinutes"`
	InitialLookback string      `json:"initialLookback"`
	MaxLookback     string      `json:"maxLookback"`
	BootstrapMode   string      `json:"bootstrapMode"`
	SchemaMode      string      `json:"schemaMode"`
	BatchSize       int         `json:"batchSize"`
	MaxWorkers      int         `json:"maxWorkers"`
	VerifyMode      string      `json:"verifyMode"`
}

type FileConfig struct {
	ID       string      `json:"id"`
	Servers  []Server    `json:"servers"`
	Tables   []TableSpec `json:"tables"`
	Defaults Defaults    `json:"defaults"`
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
	IgnoreFields   []string `json:"ignoreFields"`   // optional: local/generated fields to exclude from writes and verification
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
	if len(c.Files) == 0 {
		return fmt.Errorf("need at least 1 file config")
	}
	seen := make(map[string]bool, len(c.Files))
	for i := range c.Files {
		f := &c.Files[i]
		if strings.TrimSpace(f.ID) == "" {
			return fmt.Errorf("files[%d]: id required", i)
		}
		if seen[f.ID] {
			return fmt.Errorf("files[%d]: duplicate id %q", i, f.ID)
		}
		seen[f.ID] = true
		if err := f.Validate(); err != nil {
			return fmt.Errorf("files[%d] (%s): %w", i, f.ID, err)
		}
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
func (d Defaults) Merge(override Defaults) Defaults {
	out := d
	if override.PrimaryKey != "" {
		out.PrimaryKey = override.PrimaryKey
	}
	if override.ModifiedField != "" {
		out.ModifiedField = override.ModifiedField
	}
	return out
}

// DefaultsForFile returns the effective defaults for a specific file group.
func (c *Config) DefaultsForFile(f FileConfig) Defaults {
	return c.Defaults.Merge(f.Defaults)
}

// PKMod returns primary key and modification field names for a table (defaults apply).
func (c *Config) PKMod(f FileConfig, t TableSpec) (pk, mod string) {
	def := c.DefaultsForFile(f)
	pk = t.PrimaryKey
	if pk == "" {
		pk = def.PrimaryKey
	}
	mod = t.ModifiedField
	if mod == "" {
		mod = def.ModifiedField
	}
	return pk, mod
}

// TotalTableCount reports the number of configured table entries across all files.
func (c *Config) TotalTableCount() int {
	total := 0
	for _, f := range c.Files {
		total += len(f.Tables)
	}
	return total
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

// BootstrapBinary reports whether binary bootstrap probing is enabled.
// Supported: "fixed" (default), "binary".
func (c *Config) BootstrapBinary() bool {
	switch c.BootstrapMode {
	case "", "fixed":
		return false
	case "binary":
		return true
	default:
		return false
	}
}
