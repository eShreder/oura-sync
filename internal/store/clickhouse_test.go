package store

import (
	"context"
	"fmt"
	"testing"

	chmodule "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
)

// startClickHouseContainer starts a ClickHouse testcontainer and returns a config
// pointing to it. The container is terminated when the test finishes.
// Skips the test if Docker is not available.
func startClickHouseContainer(t *testing.T) *config.ClickHouse {
	t.Helper()
	ctx := context.Background()

	var container *chmodule.ClickHouseContainer
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("docker not available: %v", r)
			}
		}()
		container, err = chmodule.Run(ctx, "clickhouse/clickhouse-server:24.6")
	}()
	if err != nil {
		t.Skipf("skipping: could not start ClickHouse container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("getting container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("getting container port: %v", err)
	}

	return &config.ClickHouse{
		Host:     host,
		Port:     port.Int(),
		Database: "default",
		User:     "default",
		Password: "",
	}
}

func TestClickHouseStore_ConnectAndMigrate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)

	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	// Verify all expected tables were created.
	ctx := context.Background()

	expectedTables := map[string]bool{
		"sync_state":      false,
		"location_period":  false,
		"daily_weather":    false,
	}
	for _, ep := range api.Endpoints {
		expectedTables[ep.Name] = false
	}

	rows, err := s.conn.Query(ctx, "SHOW TABLES")
	if err != nil {
		t.Fatalf("SHOW TABLES: %v", err)
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		found[name] = true
	}

	for table := range expectedTables {
		if !found[table] {
			t.Errorf("expected table %q was not created", table)
		}
	}
}

func TestClickHouseStore_TableEngines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)

	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Verify all tables use ReplacingMergeTree.
	tables := []string{"sync_state", "personal_info", "heartrate", "location_period", "daily_weather", "daily_activity"}
	for _, table := range tables {
		var engine string
		err := s.conn.QueryRow(ctx,
			fmt.Sprintf("SELECT engine FROM system.tables WHERE database = 'default' AND name = '%s'", table),
		).Scan(&engine)
		if err != nil {
			t.Errorf("querying engine for %s: %v", table, err)
			continue
		}
		if engine != "ReplacingMergeTree" {
			t.Errorf("table %s: expected engine ReplacingMergeTree, got %s", table, engine)
		}
	}
}

func TestClickHouseStore_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)

	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestClickHouseStore_MigrateIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)

	// Create store twice — migrations should be idempotent (CREATE TABLE IF NOT EXISTS).
	s1, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("first NewClickHouseStore: %v", err)
	}
	s1.Close()

	s2, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("second NewClickHouseStore: %v", err)
	}
	s2.Close()
}
