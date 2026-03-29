package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/user/oura-sync/internal/weather"
)

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }

// runSharedStoreTests exercises the Store interface against any backend.
// The factory function should return a fresh, empty store for each subtest.
func runSharedStoreTests(t *testing.T, factory func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("GetLastSync_NeverSynced", func(t *testing.T) {
		s := factory(t)
		ts, err := s.GetLastSync(ctx, "daily_activity")
		if err != nil {
			t.Fatalf("GetLastSync: %v", err)
		}
		if !ts.IsZero() {
			t.Errorf("expected zero time for never-synced endpoint, got %v", ts)
		}
	})

	t.Run("SetLastSync_AndGet", func(t *testing.T) {
		s := factory(t)
		now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		if err := s.SetLastSync(ctx,"daily_activity", now); err != nil {
			t.Fatalf("SetLastSync: %v", err)
		}
		got, err := s.GetLastSync(ctx,"daily_activity")
		if err != nil {
			t.Fatalf("GetLastSync: %v", err)
		}
		if !got.Equal(now) {
			t.Errorf("got sync time %v, want %v", got, now)
		}
	})

	t.Run("SetLastSync_Update", func(t *testing.T) {
		s := factory(t)
		t1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)

		if err := s.SetLastSync(ctx,"daily_activity", t1); err != nil {
			t.Fatalf("SetLastSync t1: %v", err)
		}
		if err := s.SetLastSync(ctx,"daily_activity", t2); err != nil {
			t.Fatalf("SetLastSync t2: %v", err)
		}
		got, err := s.GetLastSync(ctx,"daily_activity")
		if err != nil {
			t.Fatalf("GetLastSync: %v", err)
		}
		if !got.Equal(t2) {
			t.Errorf("got sync time %v, want %v", got, t2)
		}
	})

	t.Run("SetLastSync_MultipleEndpoints", func(t *testing.T) {
		s := factory(t)
		t1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)

		if err := s.SetLastSync(ctx,"daily_activity", t1); err != nil {
			t.Fatalf("SetLastSync daily_activity: %v", err)
		}
		if err := s.SetLastSync(ctx,"heartrate", t2); err != nil {
			t.Fatalf("SetLastSync heartrate: %v", err)
		}

		got1, err := s.GetLastSync(ctx,"daily_activity")
		if err != nil {
			t.Fatalf("GetLastSync daily_activity: %v", err)
		}
		got2, err := s.GetLastSync(ctx,"heartrate")
		if err != nil {
			t.Fatalf("GetLastSync heartrate: %v", err)
		}

		if !got1.Equal(t1) {
			t.Errorf("daily_activity: got %v, want %v", got1, t1)
		}
		if !got2.Equal(t2) {
			t.Errorf("heartrate: got %v, want %v", got2, t2)
		}
	})

	t.Run("UpsertRecords_EmptySlice", func(t *testing.T) {
		s := factory(t)
		if err := s.UpsertRecords(ctx,"daily_activity", nil); err != nil {
			t.Fatalf("nil records: %v", err)
		}
		if err := s.UpsertRecords(ctx,"daily_activity", []json.RawMessage{}); err != nil {
			t.Fatalf("empty records: %v", err)
		}
	})

	t.Run("UpsertRecords_MissingID", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"day":"2024-01-15","score":85}`),
		}
		err := s.UpsertRecords(ctx,"daily_activity", records)
		if err == nil {
			t.Fatal("expected error for record missing id, got nil")
		}
	})

	t.Run("UpsertRecords_Heartrate_MissingTimestamp", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"bpm":72,"source":"awake"}`),
		}
		err := s.UpsertRecords(ctx,"heartrate", records)
		if err == nil {
			t.Fatal("expected error for heartrate record missing timestamp, got nil")
		}
	})

	t.Run("UpsertRecords_InvalidJSON", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`not valid json`),
		}
		err := s.UpsertRecords(ctx,"daily_activity", records)
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
	})

	t.Run("UpsertRecords_Heartrate_InvalidJSON", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{invalid`),
		}
		err := s.UpsertRecords(ctx,"heartrate", records)
		if err == nil {
			t.Fatal("expected error for invalid heartrate JSON, got nil")
		}
	})

	t.Run("UpsertRecords_InvalidEndpointName", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"id":"1","day":"2024-01-15"}`),
		}
		err := s.UpsertRecords(ctx,"foo; DROP TABLE sync_state--", records)
		if err == nil {
			t.Fatal("expected error for invalid endpoint name, got nil")
		}
	})

	t.Run("UpsertRecords_StandardEndpoint_NoError", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":85}`),
			json.RawMessage(`{"id":"def456","day":"2024-01-16","score":72}`),
		}
		if err := s.UpsertRecords(ctx,"daily_activity", records); err != nil {
			t.Fatalf("UpsertRecords: %v", err)
		}
	})

	t.Run("UpsertRecords_StandardEndpoint_Upsert", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":85}`),
		}
		if err := s.UpsertRecords(ctx,"daily_activity", records); err != nil {
			t.Fatalf("initial insert: %v", err)
		}
		updated := []json.RawMessage{
			json.RawMessage(`{"id":"abc123","day":"2024-01-15","score":90}`),
		}
		if err := s.UpsertRecords(ctx,"daily_activity", updated); err != nil {
			t.Fatalf("upsert update: %v", err)
		}
	})

	t.Run("UpsertRecords_PersonalInfo_NoError", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"age":35,"weight":75.5,"height":180}`),
		}
		if err := s.UpsertRecords(ctx,"personal_info", records); err != nil {
			t.Fatalf("UpsertRecords personal_info: %v", err)
		}
		// Update should also work.
		updated := []json.RawMessage{
			json.RawMessage(`{"age":36,"weight":76.0,"height":180}`),
		}
		if err := s.UpsertRecords(ctx,"personal_info", updated); err != nil {
			t.Fatalf("UpsertRecords personal_info update: %v", err)
		}
	})

	t.Run("UpsertRecords_Heartrate_NoError", func(t *testing.T) {
		s := factory(t)
		records := []json.RawMessage{
			json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
			json.RawMessage(`{"timestamp":"2024-01-15T10:05:00+00:00","bpm":68,"source":"rest"}`),
		}
		if err := s.UpsertRecords(ctx,"heartrate", records); err != nil {
			t.Fatalf("UpsertRecords heartrate: %v", err)
		}
	})

	t.Run("UpsertLocationPeriods_InsertAndGet", func(t *testing.T) {
		s := factory(t)
		periods := []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
			{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("UpsertLocationPeriods: %v", err)
		}

		got, err := s.GetLocationPeriods(ctx)
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
	})

	t.Run("UpsertLocationPeriods_Update", func(t *testing.T) {
		s := factory(t)
		periods := []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("initial insert: %v", err)
		}
		periods[0].EndDate = "2026-03-12"
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("update: %v", err)
		}
		got, err := s.GetLocationPeriods(ctx)
		if err != nil {
			t.Fatalf("GetLocationPeriods: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d periods, want 1", len(got))
		}
		if got[0].EndDate != "2026-03-12" {
			t.Errorf("EndDate = %q, want 2026-03-12", got[0].EndDate)
		}
	})

	t.Run("UpsertLocationPeriods_RemovesStale", func(t *testing.T) {
		s := factory(t)
		periods := []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
			{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("initial insert: %v", err)
		}
		got, err := s.GetLocationPeriods(ctx)
		if err != nil {
			t.Fatalf("GetLocationPeriods: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d periods, want 2", len(got))
		}

		// Add weather data for Tbilisi.
		if err := s.UpsertWeatherRecords(ctx,got[1].ID, []weather.DayRecord{
			{Day: "2026-03-14", TemperatureMax: ptrF(10.0), RawJSON: json.RawMessage(`{}`)},
		}); err != nil {
			t.Fatalf("inserting weather: %v", err)
		}

		// Upsert with only Da Nang — Tbilisi should be removed.
		periods = []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("upsert with removal: %v", err)
		}
		got, err = s.GetLocationPeriods(ctx)
		if err != nil {
			t.Fatalf("GetLocationPeriods after removal: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d periods after removal, want 1", len(got))
		}
		if got[0].City != "Da Nang" {
			t.Errorf("remaining city = %q, want Da Nang", got[0].City)
		}
	})

	t.Run("GetLocationForDay", func(t *testing.T) {
		s := factory(t)
		periods := []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2026-03-12"},
			{City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2026-03-13"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("UpsertLocationPeriods: %v", err)
		}

		// Day in Da Nang period.
		loc, err := s.GetLocationForDay(ctx,"2025-12-15")
		if err != nil {
			t.Fatalf("GetLocationForDay: %v", err)
		}
		if loc == nil || loc.City != "Da Nang" {
			t.Errorf("expected Da Nang for 2025-12-15, got %v", loc)
		}

		// Day in Tbilisi period.
		loc, err = s.GetLocationForDay(ctx,"2026-03-20")
		if err != nil {
			t.Fatalf("GetLocationForDay: %v", err)
		}
		if loc == nil || loc.City != "Tbilisi" {
			t.Errorf("expected Tbilisi for 2026-03-20, got %v", loc)
		}

		// Day before any period.
		loc, err = s.GetLocationForDay(ctx,"2025-10-01")
		if err != nil {
			t.Fatalf("GetLocationForDay: %v", err)
		}
		if loc != nil {
			t.Errorf("expected nil for 2025-10-01, got %v", loc)
		}
	})

	t.Run("GetLastWeatherDay", func(t *testing.T) {
		s := factory(t)
		periods := []weather.LocationPeriod{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
		}
		if err := s.UpsertLocationPeriods(ctx,periods); err != nil {
			t.Fatalf("UpsertLocationPeriods: %v", err)
		}
		locs, err := s.GetLocationPeriods(ctx)
		if err != nil {
			t.Fatalf("GetLocationPeriods: %v", err)
		}
		locID := locs[0].ID

		// No data yet.
		day, err := s.GetLastWeatherDay(ctx,locID)
		if err != nil {
			t.Fatalf("GetLastWeatherDay: %v", err)
		}
		if day != "" {
			t.Errorf("expected empty for no data, got %q", day)
		}

		// Insert weather data.
		records := []weather.DayRecord{
			{Day: "2025-11-01", TemperatureMax: ptrF(32.0), RawJSON: json.RawMessage(`{}`)},
			{Day: "2025-11-03", TemperatureMax: ptrF(30.0), RawJSON: json.RawMessage(`{}`)},
			{Day: "2025-11-02", TemperatureMax: ptrF(31.0), RawJSON: json.RawMessage(`{}`)},
		}
		if err := s.UpsertWeatherRecords(ctx,locID, records); err != nil {
			t.Fatalf("UpsertWeatherRecords: %v", err)
		}

		day, err = s.GetLastWeatherDay(ctx,locID)
		if err != nil {
			t.Fatalf("GetLastWeatherDay: %v", err)
		}
		if day != "2025-11-03" {
			t.Errorf("last weather day = %q, want 2025-11-03", day)
		}
	})

	t.Run("UpsertWeatherRecords_EmptySlice", func(t *testing.T) {
		s := factory(t)
		if err := s.UpsertWeatherRecords(ctx,1, nil); err != nil {
			t.Fatalf("nil records: %v", err)
		}
		if err := s.UpsertWeatherRecords(ctx,1, []weather.DayRecord{}); err != nil {
			t.Fatalf("empty records: %v", err)
		}
	})
}
