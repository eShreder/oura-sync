package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	chmodule "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/weather"
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

func TestClickHouseStore_GetLastSync_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	got, err := s.GetLastSync("daily_activity")
	if err != nil {
		t.Fatalf("GetLastSync: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("expected zero time for unseen endpoint, got %v", got)
	}
}

func TestClickHouseStore_SetAndGetLastSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	if err := s.SetLastSync("daily_activity", ts); err != nil {
		t.Fatalf("SetLastSync: %v", err)
	}

	got, err := s.GetLastSync("daily_activity")
	if err != nil {
		t.Fatalf("GetLastSync: %v", err)
	}
	if !got.Equal(ts) {
		t.Errorf("GetLastSync = %v, want %v", got, ts)
	}

	// Update with a newer time
	ts2 := time.Date(2025, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := s.SetLastSync("daily_activity", ts2); err != nil {
		t.Fatalf("SetLastSync (update): %v", err)
	}

	// Force merge to ensure ReplacingMergeTree dedup is applied
	ctx := context.Background()
	_ = s.conn.Exec(ctx, "OPTIMIZE TABLE sync_state FINAL")

	got2, err := s.GetLastSync("daily_activity")
	if err != nil {
		t.Fatalf("GetLastSync after update: %v", err)
	}
	if !got2.Equal(ts2) {
		t.Errorf("GetLastSync after update = %v, want %v", got2, ts2)
	}
}

func TestClickHouseStore_SetLastSync_MultipleEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	ts1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	if err := s.SetLastSync("daily_activity", ts1); err != nil {
		t.Fatalf("SetLastSync daily_activity: %v", err)
	}
	if err := s.SetLastSync("heartrate", ts2); err != nil {
		t.Fatalf("SetLastSync heartrate: %v", err)
	}

	got1, err := s.GetLastSync("daily_activity")
	if err != nil {
		t.Fatalf("GetLastSync daily_activity: %v", err)
	}
	if !got1.Equal(ts1) {
		t.Errorf("daily_activity = %v, want %v", got1, ts1)
	}

	got2, err := s.GetLastSync("heartrate")
	if err != nil {
		t.Fatalf("GetLastSync heartrate: %v", err)
	}
	if !got2.Equal(ts2) {
		t.Errorf("heartrate = %v, want %v", got2, ts2)
	}
}

func TestClickHouseStore_UpsertRecords_PersonalInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"email":"test@example.com","age":30}`),
	}
	if err := s.UpsertRecords("personal_info", records); err != nil {
		t.Fatalf("UpsertRecords personal_info: %v", err)
	}

	// Verify data was inserted
	ctx := context.Background()
	var data string
	err = s.conn.QueryRow(ctx, "SELECT data FROM personal_info FINAL WHERE id = 1").Scan(&data)
	if err != nil {
		t.Fatalf("querying personal_info: %v", err)
	}
	if data != `{"email":"test@example.com","age":30}` {
		t.Errorf("data = %s, want original JSON", data)
	}

	// Update personal_info
	records2 := []json.RawMessage{
		json.RawMessage(`{"email":"new@example.com","age":31}`),
	}
	if err := s.UpsertRecords("personal_info", records2); err != nil {
		t.Fatalf("UpsertRecords personal_info update: %v", err)
	}

	_ = s.conn.Exec(ctx, "OPTIMIZE TABLE personal_info FINAL")

	err = s.conn.QueryRow(ctx, "SELECT data FROM personal_info FINAL WHERE id = 1").Scan(&data)
	if err != nil {
		t.Fatalf("querying personal_info after update: %v", err)
	}
	if data != `{"email":"new@example.com","age":31}` {
		t.Errorf("updated data = %s, want new JSON", data)
	}
}

func TestClickHouseStore_UpsertRecords_Heartrate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2025-06-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
		json.RawMessage(`{"timestamp":"2025-06-15T10:01:00+00:00","bpm":75,"source":"awake"}`),
	}
	if err := s.UpsertRecords("heartrate", records); err != nil {
		t.Fatalf("UpsertRecords heartrate: %v", err)
	}

	ctx := context.Background()
	var count uint64
	err = s.conn.QueryRow(ctx, "SELECT count() FROM heartrate FINAL").Scan(&count)
	if err != nil {
		t.Fatalf("counting heartrate: %v", err)
	}
	if count != 2 {
		t.Errorf("heartrate count = %d, want 2", count)
	}

	// Verify extracted fields
	var bpm int64
	var source string
	err = s.conn.QueryRow(ctx,
		"SELECT bpm, source FROM heartrate FINAL WHERE timestamp = ?",
		"2025-06-15T10:00:00+00:00",
	).Scan(&bpm, &source)
	if err != nil {
		t.Fatalf("querying heartrate row: %v", err)
	}
	if bpm != 72 {
		t.Errorf("bpm = %d, want 72", bpm)
	}
	if source != "awake" {
		t.Errorf("source = %s, want awake", source)
	}

	// Update existing record
	records2 := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2025-06-15T10:00:00+00:00","bpm":80,"source":"rest"}`),
	}
	if err := s.UpsertRecords("heartrate", records2); err != nil {
		t.Fatalf("UpsertRecords heartrate update: %v", err)
	}

	_ = s.conn.Exec(ctx, "OPTIMIZE TABLE heartrate FINAL")

	err = s.conn.QueryRow(ctx,
		"SELECT bpm, source FROM heartrate FINAL WHERE timestamp = ?",
		"2025-06-15T10:00:00+00:00",
	).Scan(&bpm, &source)
	if err != nil {
		t.Fatalf("querying heartrate after update: %v", err)
	}
	if bpm != 80 {
		t.Errorf("updated bpm = %d, want 80", bpm)
	}
}

func TestClickHouseStore_UpsertRecords_Heartrate_MissingTimestamp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"bpm":72}`),
	}
	err = s.UpsertRecords("heartrate", records)
	if err == nil {
		t.Fatal("expected error for missing timestamp, got nil")
	}
}

func TestClickHouseStore_UpsertRecords_StandardEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"id":"abc-123","day":"2025-06-15","score":85}`),
		json.RawMessage(`{"id":"def-456","day":"2025-06-16","score":90}`),
	}
	if err := s.UpsertRecords("daily_activity", records); err != nil {
		t.Fatalf("UpsertRecords daily_activity: %v", err)
	}

	ctx := context.Background()
	var count uint64
	err = s.conn.QueryRow(ctx, "SELECT count() FROM daily_activity FINAL").Scan(&count)
	if err != nil {
		t.Fatalf("counting daily_activity: %v", err)
	}
	if count != 2 {
		t.Errorf("daily_activity count = %d, want 2", count)
	}

	// Verify extracted fields
	var day string
	var data string
	err = s.conn.QueryRow(ctx,
		"SELECT day, data FROM daily_activity FINAL WHERE id = ?",
		"abc-123",
	).Scan(&day, &data)
	if err != nil {
		t.Fatalf("querying daily_activity row: %v", err)
	}
	if day != "2025-06-15" {
		t.Errorf("day = %s, want 2025-06-15", day)
	}

	// Update existing record
	records2 := []json.RawMessage{
		json.RawMessage(`{"id":"abc-123","day":"2025-06-15","score":99}`),
	}
	if err := s.UpsertRecords("daily_activity", records2); err != nil {
		t.Fatalf("UpsertRecords daily_activity update: %v", err)
	}

	_ = s.conn.Exec(ctx, "OPTIMIZE TABLE daily_activity FINAL")

	err = s.conn.QueryRow(ctx,
		"SELECT data FROM daily_activity FINAL WHERE id = ?",
		"abc-123",
	).Scan(&data)
	if err != nil {
		t.Fatalf("querying daily_activity after update: %v", err)
	}
	if data != `{"id":"abc-123","day":"2025-06-15","score":99}` {
		t.Errorf("updated data = %s, want new JSON", data)
	}
}

func TestClickHouseStore_UpsertRecords_StandardEndpoint_MissingID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"day":"2025-06-15"}`),
	}
	err = s.UpsertRecords("daily_activity", records)
	if err == nil {
		t.Fatal("expected error for missing id, got nil")
	}
}

func TestClickHouseStore_UpsertRecords_EmptyRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	err = s.UpsertRecords("daily_activity", nil)
	if err != nil {
		t.Fatalf("UpsertRecords with nil records: %v", err)
	}

	err = s.UpsertRecords("daily_activity", []json.RawMessage{})
	if err != nil {
		t.Fatalf("UpsertRecords with empty records: %v", err)
	}
}

func TestClickHouseStore_UpsertRecords_InvalidEndpointName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	records := []json.RawMessage{json.RawMessage(`{"id":"1"}`)}
	err = s.UpsertRecords("bad-name!", records)
	if err == nil {
		t.Fatal("expected error for invalid endpoint name, got nil")
	}
}

func TestClickHouseStore_UpsertLocationPeriods_InsertAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01", EndDate: "2025-06-01"},
		{City: "Tokyo", Latitude: 35.6762, Longitude: 139.6503, Timezone: "Asia/Tokyo", StartDate: "2025-06-02"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	got, err := s.GetLocationPeriods()
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d periods, want 2", len(got))
	}

	// Periods should be ordered by start_date.
	if got[0].City != "Berlin" {
		t.Errorf("first period city = %s, want Berlin", got[0].City)
	}
	if got[1].City != "Tokyo" {
		t.Errorf("second period city = %s, want Tokyo", got[1].City)
	}
	if got[0].EndDate != "2025-06-01" {
		t.Errorf("Berlin end_date = %s, want 2025-06-01", got[0].EndDate)
	}
	if got[1].EndDate != "" {
		t.Errorf("Tokyo end_date = %q, want empty", got[1].EndDate)
	}

	// IDs should be deterministic.
	expectedID := locationPeriodID("Berlin", "2025-01-01")
	if got[0].ID != expectedID {
		t.Errorf("Berlin ID = %d, want %d", got[0].ID, expectedID)
	}
}

func TestClickHouseStore_UpsertLocationPeriods_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	// Update with new end date.
	periods[0].EndDate = "2025-06-01"
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods update: %v", err)
	}

	ctx := context.Background()
	_ = s.conn.Exec(ctx, "OPTIMIZE TABLE location_period FINAL")

	got, err := s.GetLocationPeriods()
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d periods, want 1", len(got))
	}
	if got[0].EndDate != "2025-06-01" {
		t.Errorf("updated end_date = %s, want 2025-06-01", got[0].EndDate)
	}
}

func TestClickHouseStore_UpsertLocationPeriods_Cleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	// Insert two periods.
	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01"},
		{City: "Tokyo", Latitude: 35.6762, Longitude: 139.6503, Timezone: "Asia/Tokyo", StartDate: "2025-06-01"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	// Upsert with only Berlin — Tokyo should be removed.
	periods = periods[:1]
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods cleanup: %v", err)
	}

	got, err := s.GetLocationPeriods()
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d periods after cleanup, want 1", len(got))
	}
	if got[0].City != "Berlin" {
		t.Errorf("remaining period = %s, want Berlin", got[0].City)
	}
}

func TestClickHouseStore_UpsertLocationPeriods_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	// Insert a period first.
	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	// Upsert empty — should clear all.
	if err := s.UpsertLocationPeriods(nil); err != nil {
		t.Fatalf("UpsertLocationPeriods empty: %v", err)
	}

	got, err := s.GetLocationPeriods()
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d periods after empty upsert, want 0", len(got))
	}
}

func TestClickHouseStore_GetLocationForDay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01", EndDate: "2025-06-01"},
		{City: "Tokyo", Latitude: 35.6762, Longitude: 139.6503, Timezone: "Asia/Tokyo", StartDate: "2025-06-02"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	// Day within Berlin period.
	got, err := s.GetLocationForDay("2025-03-15")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if got == nil {
		t.Fatal("expected Berlin, got nil")
	}
	if got.City != "Berlin" {
		t.Errorf("city = %s, want Berlin", got.City)
	}

	// Day within Tokyo period (open-ended).
	got, err = s.GetLocationForDay("2025-07-01")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if got == nil {
		t.Fatal("expected Tokyo, got nil")
	}
	if got.City != "Tokyo" {
		t.Errorf("city = %s, want Tokyo", got.City)
	}

	// Day before any period.
	got, err = s.GetLocationForDay("2024-01-01")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for day before all periods, got %v", got)
	}
}

func TestClickHouseStore_UpsertWeatherRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	// Create a location period first.
	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}
	locationID := locationPeriodID("Berlin", "2025-01-01")

	tempMax := 25.5
	tempMin := 15.0
	code := 1
	records := []weather.DayRecord{
		{Day: "2025-06-15", TemperatureMax: &tempMax, TemperatureMin: &tempMin, WeatherCode: &code, RawJSON: json.RawMessage(`{"day":"2025-06-15"}`)},
		{Day: "2025-06-16", TemperatureMax: &tempMax, RawJSON: json.RawMessage(`{"day":"2025-06-16"}`)},
	}
	if err := s.UpsertWeatherRecords(locationID, records); err != nil {
		t.Fatalf("UpsertWeatherRecords: %v", err)
	}

	// Verify count.
	ctx := context.Background()
	var count uint64
	err = s.conn.QueryRow(ctx, "SELECT count() FROM daily_weather FINAL WHERE location_id = ?", locationID).Scan(&count)
	if err != nil {
		t.Fatalf("counting daily_weather: %v", err)
	}
	if count != 2 {
		t.Errorf("daily_weather count = %d, want 2", count)
	}

	// Verify extracted fields.
	var gotMax float64
	var gotMin *float64
	err = s.conn.QueryRow(ctx,
		"SELECT temperature_max, temperature_min FROM daily_weather FINAL WHERE day = ? AND location_id = ?",
		"2025-06-15", locationID,
	).Scan(&gotMax, &gotMin)
	if err != nil {
		t.Fatalf("querying weather row: %v", err)
	}
	if gotMax != 25.5 {
		t.Errorf("temperature_max = %f, want 25.5", gotMax)
	}
	if gotMin == nil || *gotMin != 15.0 {
		t.Errorf("temperature_min = %v, want 15.0", gotMin)
	}
}

func TestClickHouseStore_UpsertWeatherRecords_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	err = s.UpsertWeatherRecords(1, nil)
	if err != nil {
		t.Fatalf("UpsertWeatherRecords empty: %v", err)
	}
}

func TestClickHouseStore_GetLastWeatherDay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	locationID := int64(42)

	// No data — should return empty.
	day, err := s.GetLastWeatherDay(locationID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay empty: %v", err)
	}
	if day != "" {
		t.Errorf("expected empty, got %s", day)
	}

	// Insert some weather data.
	tempMax := 20.0
	records := []weather.DayRecord{
		{Day: "2025-06-10", TemperatureMax: &tempMax, RawJSON: json.RawMessage(`{}`)},
		{Day: "2025-06-15", TemperatureMax: &tempMax, RawJSON: json.RawMessage(`{}`)},
		{Day: "2025-06-12", TemperatureMax: &tempMax, RawJSON: json.RawMessage(`{}`)},
	}
	if err := s.UpsertWeatherRecords(locationID, records); err != nil {
		t.Fatalf("UpsertWeatherRecords: %v", err)
	}

	day, err = s.GetLastWeatherDay(locationID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay: %v", err)
	}
	if day != "2025-06-15" {
		t.Errorf("last weather day = %s, want 2025-06-15", day)
	}
}

func TestClickHouseStore_WeatherInvalidation_CoordinateChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := startClickHouseContainer(t)
	s, err := NewClickHouseStore(cfg)
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer s.Close()

	periods := []weather.LocationPeriod{
		{City: "Berlin", Latitude: 52.52, Longitude: 13.405, Timezone: "Europe/Berlin", StartDate: "2025-01-01"},
	}
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}
	locationID := locationPeriodID("Berlin", "2025-01-01")

	// Insert weather data.
	tempMax := 20.0
	records := []weather.DayRecord{
		{Day: "2025-06-15", TemperatureMax: &tempMax, RawJSON: json.RawMessage(`{}`)},
	}
	if err := s.UpsertWeatherRecords(locationID, records); err != nil {
		t.Fatalf("UpsertWeatherRecords: %v", err)
	}

	// Change coordinates — weather should be invalidated.
	periods[0].Latitude = 53.0
	if err := s.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods with new coords: %v", err)
	}

	day, err := s.GetLastWeatherDay(locationID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay: %v", err)
	}
	if day != "" {
		t.Errorf("expected weather invalidated (empty), got %s", day)
	}
}
