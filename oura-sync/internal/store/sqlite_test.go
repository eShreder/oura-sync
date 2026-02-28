package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/user/oura-sync/internal/api"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew_CreatesAllTables(t *testing.T) {
	s := newTestStore(t)

	tables, err := s.TableNames()
	if err != nil {
		t.Fatalf("getting table names: %v", err)
	}

	// Should have one table per endpoint (18 total).
	if len(tables) != len(api.Endpoints) {
		t.Errorf("got %d tables, want %d", len(tables), len(api.Endpoints))
	}

	// Verify sync_state table exists separately.
	var count int
	err = s.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sync_state'",
	).Scan(&count)
	if err != nil {
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

	err := s.UpsertRecords("daily_activity", records)
	if err != nil {
		t.Fatalf("upserting records: %v", err)
	}

	// Verify records were inserted.
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&count)
	if count != 2 {
		t.Errorf("got %d records, want 2", count)
	}

	// Verify data integrity.
	var data string
	s.db.QueryRow("SELECT data FROM daily_activity WHERE id='abc123'").Scan(&data)
	var parsed map[string]interface{}
	json.Unmarshal([]byte(data), &parsed)
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
	if err := s.UpsertRecords("daily_activity", records); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	// Update with new data.
	updated := []json.RawMessage{
		json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":90}`),
	}
	if err := s.UpsertRecords("daily_activity", updated); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	// Should still be 1 record.
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&count)
	if count != 1 {
		t.Errorf("got %d records, want 1 after upsert", count)
	}

	// Score should be updated.
	var data string
	s.db.QueryRow("SELECT data FROM daily_activity WHERE id='abc123'").Scan(&data)
	var parsed map[string]interface{}
	json.Unmarshal([]byte(data), &parsed)
	if parsed["score"].(float64) != 90 {
		t.Errorf("got score %v, want 90 after update", parsed["score"])
	}
}

func TestUpsertRecords_PersonalInfo(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"age":35,"weight":75.5,"height":180,"biological_sex":"male","email":"test@example.com"}`),
	}

	err := s.UpsertRecords("personal_info", records)
	if err != nil {
		t.Fatalf("upserting personal_info: %v", err)
	}

	var data string
	s.db.QueryRow("SELECT data FROM personal_info WHERE id=1").Scan(&data)
	var parsed map[string]interface{}
	json.Unmarshal([]byte(data), &parsed)
	if parsed["age"].(float64) != 35 {
		t.Errorf("got age %v, want 35", parsed["age"])
	}

	// Update personal_info.
	updated := []json.RawMessage{
		json.RawMessage(`{"age":36,"weight":76.0,"height":180,"biological_sex":"male","email":"test@example.com"}`),
	}
	if err := s.UpsertRecords("personal_info", updated); err != nil {
		t.Fatalf("updating personal_info: %v", err)
	}

	// Should still be 1 record (singleton).
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM personal_info").Scan(&count)
	if count != 1 {
		t.Errorf("got %d personal_info records, want 1", count)
	}

	s.db.QueryRow("SELECT data FROM personal_info WHERE id=1").Scan(&data)
	json.Unmarshal([]byte(data), &parsed)
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

	err := s.UpsertRecords("heartrate", records)
	if err != nil {
		t.Fatalf("upserting heartrate: %v", err)
	}

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM heartrate").Scan(&count)
	if count != 2 {
		t.Errorf("got %d heartrate records, want 2", count)
	}

	// Verify bpm and source extraction.
	var bpm int
	var source string
	s.db.QueryRow("SELECT bpm, source FROM heartrate WHERE timestamp='2024-01-15T10:00:00+00:00'").Scan(&bpm, &source)
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
	if err := s.UpsertRecords("heartrate", records); err != nil {
		t.Fatalf("initial heartrate insert: %v", err)
	}

	updated := []json.RawMessage{
		json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":75,"source":"awake"}`),
	}
	if err := s.UpsertRecords("heartrate", updated); err != nil {
		t.Fatalf("heartrate upsert update: %v", err)
	}

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM heartrate").Scan(&count)
	if count != 1 {
		t.Errorf("got %d records, want 1 after upsert", count)
	}

	var bpm int
	s.db.QueryRow("SELECT bpm FROM heartrate WHERE timestamp='2024-01-15T10:00:00+00:00'").Scan(&bpm)
	if bpm != 75 {
		t.Errorf("got bpm %d, want 75 after update", bpm)
	}
}

func TestUpsertRecords_EmptySlice(t *testing.T) {
	s := newTestStore(t)

	// Should be a no-op, no error.
	err := s.UpsertRecords("daily_activity", nil)
	if err != nil {
		t.Fatalf("upserting nil records: %v", err)
	}

	err = s.UpsertRecords("daily_activity", []json.RawMessage{})
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
	err := s.UpsertRecords("daily_activity", records)
	if err == nil {
		t.Fatal("expected error for record missing id, got nil")
	}
}

func TestUpsertRecords_Heartrate_MissingTimestamp(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{"bpm":72,"source":"awake"}`),
	}
	err := s.UpsertRecords("heartrate", records)
	if err == nil {
		t.Fatal("expected error for heartrate record missing timestamp, got nil")
	}
}

func TestGetLastSync_NeverSynced(t *testing.T) {
	s := newTestStore(t)

	ts, err := s.GetLastSync("daily_activity")
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
	if err := s.SetLastSync("daily_activity", now); err != nil {
		t.Fatalf("setting last sync: %v", err)
	}

	got, err := s.GetLastSync("daily_activity")
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

	if err := s.SetLastSync("daily_activity", t1); err != nil {
		t.Fatalf("setting first sync time: %v", err)
	}
	if err := s.SetLastSync("daily_activity", t2); err != nil {
		t.Fatalf("setting second sync time: %v", err)
	}

	got, err := s.GetLastSync("daily_activity")
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

	s.SetLastSync("daily_activity", t1)
	s.SetLastSync("heartrate", t2)

	got1, _ := s.GetLastSync("daily_activity")
	got2, _ := s.GetLastSync("heartrate")

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
	err := s.UpsertRecords("daily_activity", records)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestUpsertRecords_Heartrate_InvalidJSON(t *testing.T) {
	s := newTestStore(t)

	records := []json.RawMessage{
		json.RawMessage(`{invalid`),
	}
	err := s.UpsertRecords("heartrate", records)
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

	if err := s.UpsertRecords("daily_activity", activityRecords); err != nil {
		t.Fatalf("upserting daily_activity: %v", err)
	}
	if err := s.UpsertRecords("daily_sleep", sleepRecords); err != nil {
		t.Fatalf("upserting daily_sleep: %v", err)
	}

	var activityCount, sleepCount int
	s.db.QueryRow("SELECT COUNT(*) FROM daily_activity").Scan(&activityCount)
	s.db.QueryRow("SELECT COUNT(*) FROM daily_sleep").Scan(&sleepCount)

	if activityCount != 1 {
		t.Errorf("daily_activity: got %d, want 1", activityCount)
	}
	if sleepCount != 1 {
		t.Errorf("daily_sleep: got %d, want 1", sleepCount)
	}
}
