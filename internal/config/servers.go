package config

import "fmt"

// Validate checks minimal requirements for one file group.
func (f *FileConfig) Validate() error {
	if len(f.Servers) != 2 {
		return fmt.Errorf("need exactly 2 servers, got %d", len(f.Servers))
	}
	for i, s := range f.Servers {
		if s.ID == "" || s.URL == "" || s.Username == "" {
			return fmt.Errorf("servers[%d]: id, url, username required", i)
		}
	}
	if len(f.Tables) > 0 {
		hasNamedTable := false
		for _, t := range f.Tables {
			if t.Name != "" {
				hasNamedTable = true
				break
			}
		}
		if !hasNamedTable {
			return fmt.Errorf("need at least 1 table with a non-empty name")
		}
	}
	seen := make(map[string]bool, len(f.Servers))
	for _, s := range f.Servers {
		if seen[s.ID] {
			return fmt.Errorf("servers must have distinct ids")
		}
		seen[s.ID] = true
	}
	if !seen["blue"] || !seen["green"] {
		return fmt.Errorf("servers must include id \"blue\" and \"green\"")
	}
	return nil
}

// BlueGreen returns servers by id "blue" and "green" within one file group.
func (f *FileConfig) BlueGreen() (blue Server, green Server, err error) {
	for i := range f.Servers {
		switch f.Servers[i].ID {
		case "blue":
			blue = f.Servers[i]
		case "green":
			green = f.Servers[i]
		}
	}
	if blue.ID == "" || green.ID == "" {
		return Server{}, Server{}, fmt.Errorf("config must include servers with id \"blue\" and \"green\"")
	}
	return blue, green, nil
}
