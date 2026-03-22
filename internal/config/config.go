package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Location represents a period the user spent at a specific location.
type Location struct {
	City      string  `yaml:"city"`
	Latitude  float64 `yaml:"latitude"`
	Longitude float64 `yaml:"longitude"`
	Timezone  string  `yaml:"timezone"`
	StartDate string  `yaml:"start_date"`
}

// Config holds all application settings.
type Config struct {
	Token     string        `yaml:"token"`
	DB        string        `yaml:"db"`
	Days      int           `yaml:"days"`
	Timeout   time.Duration `yaml:"timeout"`
	Locations []Location    `yaml:"locations"`
}

// Defaults returns a Config populated with default values.
func Defaults() Config {
	return Config{
		DB:      "oura.db",
		Days:    90,
		Timeout: 10 * time.Minute,
	}
}

// Load reads and parses a YAML config file. If the file does not exist and
// required is false, it returns Defaults() with no error. If required is true
// and the file is missing, it returns an error.
func Load(path string, required bool) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}

// Merge applies overrides on top of cfg using the priority:
// CLI flags > env vars > config file values (already in cfg) > defaults.
//
// flagSet tracks which flags were explicitly provided on the command line.
func Merge(cfg Config, env EnvVars, flags FlagVals, flagSet map[string]bool) Config {
	// Env vars override config file.
	if env.Token != "" {
		cfg.Token = env.Token
	}

	// CLI flags override everything.
	if flagSet["db"] {
		cfg.DB = flags.DB
	}
	if flagSet["days"] {
		cfg.Days = flags.Days
	}
	if flagSet["timeout"] {
		cfg.Timeout = flags.Timeout
	}

	return cfg
}

// EnvVars holds values read from environment variables.
type EnvVars struct {
	Token string
}

// FlagVals holds values read from CLI flags.
type FlagVals struct {
	DB      string
	Days    int
	Timeout time.Duration
}
