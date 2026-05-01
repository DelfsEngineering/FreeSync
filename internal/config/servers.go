package config

import "fmt"

// BlueGreen returns servers by id "blue" and "green" (SPEC layout).
func (c *Config) BlueGreen() (blue Server, green Server, err error) {
	for i := range c.Servers {
		switch c.Servers[i].ID {
		case "blue":
			blue = c.Servers[i]
		case "green":
			green = c.Servers[i]
		}
	}
	if blue.ID == "" || green.ID == "" {
		return Server{}, Server{}, fmt.Errorf("config must include servers with id \"blue\" and \"green\"")
	}
	return blue, green, nil
}
