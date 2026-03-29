package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/weather"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew_CreatesAllTables(t *testing.T) {
	s := newTestStore(t)

	// Count endpoint tables (excluding sync_state, location_period, daily_weather).
	var tableCount int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT IN ('sync_state','location_period','daily_weather') AND name NOT LIKE 'sqlite_%'",
	).Scan(&tableCount); err != nil {
		t.Fatalf("counting tables: %v", err)
	}

	if tableCount != len(api.Endpoints) {
		t.Errorf("got %d tables, want %d", tableCount, len(api.Endpoints))
	}

	// Verify sync_state table exists separately.
	var count int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sync_state'",
	).Scan(&count); err != nil {
		t.Fatalf("checking sync_state table: %v", err)
	}
	if count != 1 {
		t.Error("sync_state table not found")
	}
}

func TestNew_AllEndpointTablesExist(t *testing.T) {
	s := newTestStore(t)

	for _, ep := range api.Endpoints {
		var count int
		err := s.db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			ep.Name,
		).Scan(&count)
		if err != nil {
			t.Fatalf("checking table %s: %v", ep.Name, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", ep.Name)
		}
	}
}

func TestNew_IdempotentMigration(t *testing.T) {
	// Opening the same DB twice should not fail (IF NOT EXISTS).
	s := newTestStore(t)

	// Run migrate again explicitly.
	err := s.migrate()
	if err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
}

func TestUpsertRecords_StandardEndpoint_Insert(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":85,"contributors":{"steps":90}}`),
		json.RawMessage(`{"id":"def456","day":"2024-01-16","score":72,"contributors":{"steps":65}}`),
	}

	err := s.UpsertRecords(context.Background(),"daily_activity", records)
	if err != nil {
		t.Fatalf("upserting records: %v", err)
	}

	// Verify records were inserted.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&count); err != nil {
		t.Fatalf("counting records: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d records, want 2", count)
	}

	// Verify data integrity.
	var data string
	if err := s.db.QueryRow("SELECT data FROM daily_activity WHERE id='abc123'").Scan(&data); err != nil {
		t.Fatalf("selecting data: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshaling data: %v", err)
	}
	if parsed["score"].(float64) != 85 {
		t.Errorf("got score %v, want 85", parsed["score"])
	}
}

func TestUpsertRecords_StandardEndpoint_Update(t *testing.T) {
	s := newTestStore(t)

	// Insert initial record.
	records := []json.RawMessage{
		json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":85}`),
	}
	if err := s.UpsertRecords(context.Background(),"daily_activity", records); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	// Update with new data.
	updated := []json.RawMessage{
		json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":90}`),
	}
	if err := s.UpsertRecords(context.Background(),"daily_activity", updated); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	// Should still be 1 record.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&count); err != nil {
		t.Fatalf("counting records: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d records, want 1 after upsert", count)
	}

	// Score should be updated.
	var data string
	if err := s.db.QueryRow("SELECT data FROM daily_activity WHERE id='abc123'").Scan(&data); err != nil {
		t.Fatalf("selecting data: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshaling data: %v", err)
	}
	if parsed["score"].(float64) != 90 {
		t.Errorf("got score %v, want 90 after update", parsed["score"])
	}
}

func TestUpsertRecords_PersonalInfo(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"age":35,"weight":75.5,"height":180,"biological_sex":"male","email":"test@example.com"}`),
	}

	err := s.UpsertRecords(context.Background(),"personal_info", records)
	if err != nil {
		t.Fatalf("upserting personal_info: %v", err)
	}

	var data string
	if err := s.db.QueryRow("SELECT data FROM personal_info WHERE id=1").Scan(&data); err != nil {
		t.Fatalf("selecting personal_info data: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshaling personal_info data: %v", err)
	}
	if parsed["age"].(float64) != 35 {
		t.Errorf("got age %v, want 35", parsed["age"])
	}

	// Update personal_info.
	updated := []json.RawMessage{
		json.RawMessage(`{"age":36,"weight":76.0,"height":180,"biological_sex":"male","email":"test@example.com"}`),
	}
	if err := s.UpsertRecords(context.Background(),"personal_info", updated); err != nil {
		t.Fatalf("updating personal_info: %v", err)
	}

	// Should still be 1 record (singleton).
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM personal_info").Scan(&count); err != nil {
		t.Fatalf("counting personal_info records: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d personal_info records, want 1", count)
	}

	if err := s.db.QueryRow("SELECT data FROM personal_info WHERE id=1").Scan(&data); err != nil {
		t.Fatalf("selecting updated personal_info data: %v", err)
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshaling updated personal_info data: %v", err)
	}
	if parsed["age"].(float64) != 36 {
		t.Errorf("got age %v, want 36 after update", parsed["age"])
	}
}

func TestUpsertRecords_Heartrate(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
		json.RawMessage(`{"timestamp":"2024-01-15T10:05:00+00:00","bpm":68,"source":"rest"}`),
	}

	err := s.UpsertRecords(context.Background(),"heartrate", records)
	if err != nil {
		t.Fatalf("upserting heartrate: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM heartrate").Scan(&count); err != nil {
		t.Fatalf("counting heartrate records: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d heartrate records, want 2", count)
	}

	// Verify bpm and source extraction.
	var bpm int
	var source string
	if err := s.db.QueryRow("SELECT bpm, source FROM heartrate WHERE timestamp='2024-01-15T10:00:00+00:00'").Scan(&bpm, &source); err != nil {
		t.Fatalf("selecting heartrate data: %v", err)
	}
	if bpm != 72 {
		t.Errorf("got bpm %d, want 72", bpm)
	}
	if source != "awake" {
		t.Errorf("got source %q, want %q", source, "awake")
	}
}

func TestUpsertRecords_Heartrate_Update(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
	}
	if err := s.UpsertRecords(context.Background(),"heartrate", records); err != nil {
		t.Fatalf("initial heartrate insert: %v", err)
	}

	updated := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":75,"source":"awake"}`),
	}
	if err := s.UpsertRecords(context.Background(),"heartrate", updated); err != nil {
		t.Fatalf("heartrate upsert update: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM heartrate").Scan(&count); err != nil {
		t.Fatalf("counting heartrate records: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d records, want 1 after upsert", count)
	}

	var bpm int
	if err := s.db.QueryRow("SELECT bpm FROM heartrate WHERE timestamp='2024-01-15T10:00:00+00:00'").Scan(&bpm); err != nil {
		t.Fatalf("selecting heartrate bpm: %v", err)
	}
	if bpm != 75 {
		t.Errorf("got bpm %d, want 75 after update", bpm)
	}
}

func TestUpsertRecords_EmptySlice(t *testing.T) {
	s := newTestStore(t)

	// Should be a no-op, no error.
	err := s.UpsertRecords(context.Background(),"daily_activity", nil)
	if err != nil {
		t.Fatalf("upserting nil records: %v", err)
	}

	err = s.UpsertRecords(context.Background(),"daily_activity", []json.RawMessage{})
	if err != nil {
		t.Fatalf("upserting empty records: %v", err)
	}
}

func TestUpsertRecords_MissingID(t *testing.T) {
	s := newTestStore(t)

	// Record without "id" field should error.
	records := []json.RawMessage{
		json.RawMessage(`{"day":"2024-01-15","score":85}`),
	}
	err := s.UpsertRecords(context.Background(),"daily_activity", records)
	if err == nil {
		t.Fatal("expected error for record missing id, got nil")
	}
}

func TestUpsertRecords_Heartrate_MissingTimestamp(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"bpm":72,"source":"awake"}`),
	}
	err := s.UpsertRecords(context.Background(),"heartrate", records)
	if err == nil {
		t.Fatal("expected error for heartrate record missing timestamp, got nil")
	}
}

func TestGetLastSync_NeverSynced(t *testing.T) {
	s := newTestStore(t)

	ts, err := s.GetLastSync(context.Background(),"daily_activity")
	if err != nil {
		t.Fatalf("getting last sync: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time for never-synced endpoint, got %v", ts)
	}
}

func TestSetLastSync_AndGet(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetLastSync(context.Background(),"daily_activity", now); err != nil {
		t.Fatalf("setting last sync: %v", err)
	}

	got, err := s.GetLastSync(context.Background(),"daily_activity")
	if err != nil {
		t.Fatalf("getting last sync: %v", err)
	}
	if !got.Equal(now) {
		t.Errorf("got sync time %v, want %v", got, now)
	}
}

func TestSetLastSync_Update(t *testing.T) {
	s := newTestStore(t)

	t1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)

	if err := s.SetLastSync(context.Background(),"daily_activity", t1); err != nil {
		t.Fatalf("setting first sync time: %v", err)
	}
	if err := s.SetLastSync(context.Background(),"daily_activity", t2); err != nil {
		t.Fatalf("setting second sync time: %v", err)
	}

	got, err := s.GetLastSync(context.Background(),"daily_activity")
	if err != nil {
		t.Fatalf("getting last sync: %v", err)
	}
	if !got.Equal(t2) {
		t.Errorf("got sync time %v, want %v", got, t2)
	}
}

func TestSetLastSync_MultipleEndpoints(t *testing.T) {
	s := newTestStore(t)

	t1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)

	if err := s.SetLastSync(context.Background(),"daily_activity", t1); err != nil {
		t.Fatalf("setting daily_activity sync: %v", err)
	}
	if err := s.SetLastSync(context.Background(),"heartrate", t2); err != nil {
		t.Fatalf("setting heartrate sync: %v", err)
	}

	got1, err := s.GetLastSync(context.Background(),"daily_activity")
	if err != nil {
		t.Fatalf("getting daily_activity sync: %v", err)
	}
	got2, err := s.GetLastSync(context.Background(),"heartrate")
	if err != nil {
		t.Fatalf("getting heartrate sync: %v", err)
	}

	if !got1.Equal(t1) {
		t.Errorf("daily_activity sync time: got %v, want %v", got1, t1)
	}
	if !got2.Equal(t2) {
		t.Errorf("heartrate sync time: got %v, want %v", got2, t2)
	}
}

func TestUpsertRecords_InvalidJSON(t *testing.T) {
	s := newTestStore(t)

	// Completely invalid JSON should error.
	records := []json.RawMessage{
		json.RawMessage(`not valid json`),
	}
	err := s.UpsertRecords(context.Background(),"daily_activity", records)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestUpsertRecords_Heartrate_InvalidJSON(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{invalid`),
	}
	err := s.UpsertRecords(context.Background(),"heartrate", records)
	if err == nil {
		t.Fatal("expected error for invalid heartrate JSON, got nil")
	}
}

func TestUpsertRecords_MultipleEndpoints(t *testing.T) {
	s := newTestStore(t)

	// Insert into different tables.
	activityRecords := []json.RawMessage{
		json.RawMessage(`{"id":"a1","day":"2024-01-15","score":80}`),
	}
	sleepRecords := []json.RawMessage{
		json.RawMessage(`{"id":"s1","day":"2024-01-15","score":90}`),
	}

	if err := s.UpsertRecords(context.Background(),"daily_activity", activityRecords); err != nil {
		t.Fatalf("upserting daily_activity: %v", err)
	}
	if err := s.UpsertRecords(context.Background(),"daily_sleep", sleepRecords); err != nil {
		t.Fatalf("upserting daily_sleep: %v", err)
	}

	var activityCount, sleepCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&activityCount); err != nil {
		t.Fatalf("counting daily_activity: %v", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_sleep").Scan(&sleepCount); err != nil {
		t.Fatalf("counting daily_sleep: %v", err)
	}

	if activityCount != 1 {
		t.Errorf("daily_activity: got %d, want 1", activityCount)
	}
	if sleepCount != 1 {
		t.Errorf("daily_sleep: got %d, want 1", sleepCount)
	}
}

func TestUpsertRecords_InvalidEndpointName(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"id":"1","day":"2024-01-15"}`),
	}
	err := s.UpsertRecords(context.Background(),"foo; DROP TABLE sync_state--", records)
	if err == nil {
		t.Fatal("expected error for invalid endpoint name, got nil")
	}
}

// --- Weather / Location tests ---

func TestNew_CreatesWeatherTables(t *testing.T) {
	s := newTestStore(t)

	for _, table := range []string{"location_period", "daily_weather"} {
		var count int
		err := s.db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", table)
		}
	}
}

func TestUpsertLocationPeriods_Insert(t *testing.T) {
	s := newTestStore(t)

	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
		{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
	}

	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	got, err := s.GetLocationPeriods(context.Background())
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d periods, want 2", len(got))
	}
	if got[0].City != "Da Nang" {
		t.Errorf("got[0].City = %q, want Da Nang", got[0].City)
	}
	if got[0].EndDate != "2026-03-12" {
		t.Errorf("got[0].EndDate = %q, want 2026-03-12", got[0].EndDate)
	}
	if got[1].City != "Tbilisi" {
		t.Errorf("got[1].City = %q, want Tbilisi", got[1].City)
	}
	if got[1].EndDate != "" {
		t.Errorf("got[1].EndDate = %q, want empty (ongoing)", got[1].EndDate)
	}
}

func TestUpsertLocationPeriods_Update(t *testing.T) {
	s := newTestStore(t)

	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	// Update with end_date.
	periods[0].EndDate = "2026-03-12"
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetLocationPeriods(context.Background())
	if err != nil {
		t.Fatalf("GetLocationPeriods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d periods, want 1", len(got))
	}
	if got[0].EndDate != "2026-03-12" {
		t.Errorf("EndDate = %q, want 2026-03-12", got[0].EndDate)
	}
}

func TestUpsertLocationPeriods_RemovesStale(t *testing.T) {
	s := newTestStore(t)

	// Insert two locations.
	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
		{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	got, _ := s.GetLocationPeriods(context.Background())
	if len(got) != 2 {
		t.Fatalf("got %d periods, want 2", len(got))
	}

	// Add weather data for Tbilisi (the one we'll remove).
	if err := s.UpsertWeatherRecords(context.Background(),got[1].ID, []weather.DayRecord{
		{Day: "2026-03-14", TemperatureMax: ptrF(10.0), RawJSON: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatalf("inserting weather: %v", err)
	}

	// Now upsert with only Da Nang — Tbilisi should be removed.
	periods = []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("upsert with removal: %v", err)
	}

	got, _ = s.GetLocationPeriods(context.Background())
	if len(got) != 1 {
		t.Fatalf("got %d periods after removal, want 1", len(got))
	}
	if got[0].City != "Da Nang" {
		t.Errorf("remaining city = %q, want Da Nang", got[0].City)
	}

	// Verify weather data for removed location was also deleted.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_weather").Scan(&count); err != nil {
		t.Fatalf("querying weather count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 weather records after location removal, got %d", count)
	}
}

func TestGetLocationForDay(t *testing.T) {
	s := newTestStore(t)

	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
		{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	// Day in Da Nang period.
	loc, err := s.GetLocationForDay(context.Background(),"2025-12-15")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if loc == nil || loc.City != "Da Nang" {
		t.Errorf("expected Da Nang for 2025-12-15, got %v", loc)
	}

	// Day in Tbilisi period.
	loc, err = s.GetLocationForDay(context.Background(),"2026-03-20")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if loc == nil || loc.City != "Tbilisi" {
		t.Errorf("expected Tbilisi for 2026-03-20, got %v", loc)
	}

	// Day before any period.
	loc, err = s.GetLocationForDay(context.Background(),"2025-10-01")
	if err != nil {
		t.Fatalf("GetLocationForDay: %v", err)
	}
	if loc != nil {
		t.Errorf("expected nil for 2025-10-01, got %v", loc)
	}
}

// TestShared_SQLite runs the shared Store interface tests against SQLiteStore.
func TestShared_SQLite(t *testing.T) {
	runSharedStoreTests(t, func(t *testing.T) Store {
		t.Helper()
		return newTestStore(t)
	})
}

func TestUpsertWeatherRecords_InsertAndUpdate(t *testing.T) {
	s := newTestStore(t)

	// Insert a location first.
	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}
	locs, _ := s.GetLocationPeriods(context.Background())
	locID := locs[0].ID

	records := []weather.DayRecord{
		{Day: "2025-11-01", TemperatureMax: ptrF(32.5), TemperatureMin: ptrF(24.1), TemperatureMean: ptrF(28.3), HumidityMean: ptrF(82.0), WeatherCode: ptrI(1), RawJSON: json.RawMessage(`{"temperature_2m_max":32.5}`)},
		{Day: "2025-11-02", TemperatureMax: ptrF(31.0), TemperatureMean: ptrF(27.0), RawJSON: json.RawMessage(`{"temperature_2m_max":31.0}`)},
	}

	if err := s.UpsertWeatherRecords(context.Background(),locID, records); err != nil {
		t.Fatalf("UpsertWeatherRecords: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_weather WHERE location_id = ?", locID).Scan(&count); err != nil {
		t.Fatalf("counting weather records: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d weather records, want 2", count)
	}

	// Verify extracted columns.
	var tempMax, humidity float64
	if err := s.db.QueryRow("SELECT temperature_max, humidity_mean FROM daily_weather WHERE day='2025-11-01' AND location_id=?", locID).Scan(&tempMax, &humidity); err != nil {
		t.Fatalf("selecting weather data: %v", err)
	}
	if tempMax != 32.5 {
		t.Errorf("temperature_max = %v, want 32.5", tempMax)
	}
	if humidity != 82.0 {
		t.Errorf("humidity_mean = %v, want 82.0", humidity)
	}

	// Update record.
	updated := []weather.DayRecord{
		{Day: "2025-11-01", TemperatureMax: ptrF(33.0), HumidityMean: ptrF(85.0), RawJSON: json.RawMessage(`{"temperature_2m_max":33.0}`)},
	}
	if err := s.UpsertWeatherRecords(context.Background(),locID, updated); err != nil {
		t.Fatalf("UpsertWeatherRecords update: %v", err)
	}

	// Still 2 records.
	if err := s.db.QueryRow("SELECT COUNT(*) FROM daily_weather WHERE location_id = ?", locID).Scan(&count); err != nil {
		t.Fatalf("counting after update: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d records after update, want 2", count)
	}

	// Values should be updated.
	if err := s.db.QueryRow("SELECT temperature_max, humidity_mean FROM daily_weather WHERE day='2025-11-01' AND location_id=?", locID).Scan(&tempMax, &humidity); err != nil {
		t.Fatalf("selecting updated data: %v", err)
	}
	if tempMax != 33.0 {
		t.Errorf("updated temperature_max = %v, want 33.0", tempMax)
	}
}

func TestGetLastWeatherDay(t *testing.T) {
	s := newTestStore(t)

	periods := []weather.LocationPeriod{
		{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
	}
	if err := s.UpsertLocationPeriods(context.Background(),periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}
	locs, _ := s.GetLocationPeriods(context.Background())
	locID := locs[0].ID

	// No data yet.
	day, err := s.GetLastWeatherDay(context.Background(),locID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay: %v", err)
	}
	if day != "" {
		t.Errorf("expected empty string for no data, got %q", day)
	}

	// Insert some data.
	records := []weather.DayRecord{
		{Day: "2025-11-01", TemperatureMax: ptrF(32.0), RawJSON: json.RawMessage(`{}`)},
		{Day: "2025-11-03", TemperatureMax: ptrF(30.0), RawJSON: json.RawMessage(`{}`)},
		{Day: "2025-11-02", TemperatureMax: ptrF(31.0), RawJSON: json.RawMessage(`{}`)},
	}
	if err := s.UpsertWeatherRecords(context.Background(),locID, records); err != nil {
		t.Fatalf("UpsertWeatherRecords: %v", err)
	}

	day, err = s.GetLastWeatherDay(context.Background(),locID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay: %v", err)
	}
	if day != "2025-11-03" {
		t.Errorf("last weather day = %q, want 2025-11-03", day)
	}
}

func TestUpsertWeatherRecords_EmptySlice(t *testing.T) {
	s := newTestStore(t)

	err := s.UpsertWeatherRecords(context.Background(),1, nil)
	if err != nil {
		t.Fatalf("upserting nil records: %v", err)
	}

	err = s.UpsertWeatherRecords(context.Background(),1, []weather.DayRecord{})
	if err != nil {
		t.Fatalf("upserting empty records: %v", err)
	}
}
