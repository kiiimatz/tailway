// Package config manages the tailway configuration file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds all saved settings.
type Config struct {
	ClientIP  string `json:"client_ip"`
	ClientKey string `json:"client_key"`
	ServerKey string `json:"server_key"`
}

// configPath returns the platform-appropriate path for config.json.
func configPath() (string, error) {
	var dir string
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			dir = filepath.Join(d, "tailway")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, "AppData", "Roaming", "tailway")
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			dir = filepath.Join(xdg, "tailway")
		} else {
			dir = filepath.Join(home, ".config", "tailway")
		}
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file. Returns an empty Config (not an error) if the
// file does not exist yet.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes the config to disk (0600 permissions).
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
