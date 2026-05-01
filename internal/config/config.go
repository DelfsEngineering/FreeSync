// Package config loads Free Sync JSON configuration (SPEC.md).
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config matches the committed example shape; unknown JSON keys are ignored.
type Config struct {
	Servers        []Server      `json:"servers"`
	Tables         []TableSpec   `json:"tables"`
	Defaults       Defaults      `json:"defaults"`
	OverlapMinutes int           `json:"overlapMinutes"`
	InitialLookback string       `json:"initialLookback"`
	MaxLookback    string        `json:"maxLookback"`
	SchemaMode     string        `json:"schemaMode"`
}

type Server struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type TableSpec struct {
	Name          string `json:"name"`
	PrimaryKey    string `json:"primaryKey"`
	ModifiedField string `json:"modifiedField"`
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
	return nil
}

// Overlap returns overlap as duration (defaults if unset).
func (c *Config) Overlap() string {
	if c.OverlapMinutes <= 0 {
		return "10m"
	}
	return fmt.Sprintf("%dm", c.OverlapMinutes)
}
