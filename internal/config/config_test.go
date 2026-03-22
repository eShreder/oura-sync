package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.DB != "oura.db" {
		t.Errorf("DB = %q, want %q", d.DB, "oura.db")
	}
	if d.Days != 90 {
		t.Errorf("Days = %d, want 90", d.Days)
	}
	if d.Timeout != 10*time.Minute {
		t.Errorf("Timeout = %v, want 10m", d.Timeout)
	}
	if d.Token != "" {
		t.Errorf("Token = %q, want empty", d.Token)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `token: "abc123"
db: "my.db"
days: 30
timeout: 5m
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Token != "abc123" {
		t.Errorf("Token = %q, want %q", cfg.Token, "abc123")
	}
	if cfg.DB != "my.db" {
		t.Errorf("DB = %q, want %q", cfg.DB, "my.db")
	}
	if cfg.Days != 30 {
		t.Errorf("Days = %d, want 30", cfg.Days)
	}
	if cfg.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", cfg.Timeout)
	}
}

func TestLoad_PartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `db: "custom.db"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DB != "custom.db" {
		t.Errorf("DB = %q, want %q", cfg.DB, "custom.db")
	}
	// Unset fields should retain defaults.
	if cfg.Days != 90 {
		t.Errorf("Days = %d, want 90", cfg.Days)
	}
	if cfg.Timeout != 10*time.Minute {
		t.Errorf("Timeout = %v, want 10m", cfg.Timeout)
	}
}

func TestLoad_MissingFile_NotRequired(t *testing.T) {
	cfg, err := Load("/nonexistent/config.yaml", false)
	if err != nil {
		t.Fatalf("expected no error for missing optional file, got: %v", err)
	}
	// Should return defaults.
	if cfg.DB != "oura.db" {
		t.Errorf("DB = %q, want %q", cfg.DB, "oura.db")
	}
}

func TestLoad_MissingFile_Required(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml", true)
	if err == nil {
		t.Fatal("expected error for missing required file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":::not valid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestMerge_Priority(t *testing.T) {
	// Config file values.
	cfg := Config{
		Token:   "file-token",
		DB:      "file.db",
		Days:    30,
		Timeout: 5 * time.Minute,
	}

	env := EnvVars{Token: "env-token"}
	flags := FlagVals{DB: "flag.db", Days: 7, Timeout: 1 * time.Minute}

	// Only --db was explicitly set on CLI.
	flagSet := map[string]bool{"db": true}

	result := Merge(cfg, env, flags, flagSet)

	// Token: env overrides config file.
	if result.Token != "env-token" {
		t.Errorf("Token = %q, want %q", result.Token, "env-token")
	}
	// DB: flag overrides config file.
	if result.DB != "flag.db" {
		t.Errorf("DB = %q, want %q", result.DB, "flag.db")
	}
	// Days: no flag set, so config file value stays.
	if result.Days != 30 {
		t.Errorf("Days = %d, want 30", result.Days)
	}
	// Timeout: no flag set, so config file value stays.
	if result.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", result.Timeout)
	}
}

func TestMerge_EnvOverridesFile(t *testing.T) {
	cfg := Config{Token: "file-token"}
	env := EnvVars{Token: "env-token"}
	flags := FlagVals{}
	flagSet := map[string]bool{}

	result := Merge(cfg, env, flags, flagSet)
	if result.Token != "env-token" {
		t.Errorf("Token = %q, want %q", result.Token, "env-token")
	}
}

func TestMerge_NoEnv_UsesFile(t *testing.T) {
	cfg := Config{Token: "file-token"}
	env := EnvVars{}
	flags := FlagVals{}
	flagSet := map[string]bool{}

	result := Merge(cfg, env, flags, flagSet)
	if result.Token != "file-token" {
		t.Errorf("Token = %q, want %q", result.Token, "file-token")
	}
}

func TestMerge_AllFlagsSet(t *testing.T) {
	cfg := Config{DB: "file.db", Days: 30, Timeout: 5 * time.Minute}
	env := EnvVars{}
	flags := FlagVals{DB: "flag.db", Days: 7, Timeout: 2 * time.Minute}
	flagSet := map[string]bool{"db": true, "days": true, "timeout": true}

	result := Merge(cfg, env, flags, flagSet)
	if result.DB != "flag.db" {
		t.Errorf("DB = %q, want %q", result.DB, "flag.db")
	}
	if result.Days != 7 {
		t.Errorf("Days = %d, want 7", result.Days)
	}
	if result.Timeout != 2*time.Minute {
		t.Errorf("Timeout = %v, want 2m", result.Timeout)
	}
}

func TestLoad_WithLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `token: "abc"
locations:
  - city: "Da Nang"
    latitude: 16.0544
    longitude: 108.2022
    timezone: "Asia/Ho_Chi_Minh"
    start_date: "2025-11-01"
  - city: "Tbilisi"
    latitude: 41.6938
    longitude: 44.8015
    timezone: "Asia/Tbilisi"
    start_date: "2026-03-13"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.Locations) != 2 {
		t.Fatalf("Locations count = %d, want 2", len(cfg.Locations))
	}
	if cfg.Locations[0].City != "Da Nang" {
		t.Errorf("Locations[0].City = %q, want Da Nang", cfg.Locations[0].City)
	}
	if cfg.Locations[0].Latitude != 16.0544 {
		t.Errorf("Locations[0].Latitude = %v, want 16.0544", cfg.Locations[0].Latitude)
	}
	if cfg.Locations[0].Timezone != "Asia/Ho_Chi_Minh" {
		t.Errorf("Locations[0].Timezone = %q, want Asia/Ho_Chi_Minh", cfg.Locations[0].Timezone)
	}
	if cfg.Locations[1].City != "Tbilisi" {
		t.Errorf("Locations[1].City = %q, want Tbilisi", cfg.Locations[1].City)
	}
	if cfg.Locations[1].StartDate != "2026-03-13" {
		t.Errorf("Locations[1].StartDate = %q, want 2026-03-13", cfg.Locations[1].StartDate)
	}
}

func TestLoad_NoLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `token: "abc"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Locations != nil {
		t.Errorf("Locations = %v, want nil", cfg.Locations)
	}
}
