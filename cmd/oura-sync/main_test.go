package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/store"
	"github.com/user/oura-sync/internal/sync"
	"github.com/user/oura-sync/internal/weather"
)

// TestIntegration_FullSyncCycle runs a complete sync against a mock HTTP server
// and a temporary SQLite database, verifying end-to-end behavior.
func TestIntegration_FullSyncCycle(t *testing.T) {
	// Set up mock Oura API server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Verify auth header is present.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-integration-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
			return
		}

		switch path {
		case "/v2/usercollection/personal_info":
			w.Write([]byte(`{"age":32,"weight":78.5,"email":"user@example.com"}`))

		case "/v2/usercollection/heartrate":
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data: []json.RawMessage{
					json.RawMessage(`{"timestamp":"2024-03-01T08:00:00+00:00","bpm":65,"source":"awake"}`),
					json.RawMessage(`{"timestamp":"2024-03-01T08:05:00+00:00","bpm":68,"source":"awake"}`),
				},
				NextToken: nil,
			})

		default:
			// All other paginated endpoints return two records.
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data: []json.RawMessage{
					json.RawMessage(`{"id":"rec-001","day":"2024-03-01","score":85}`),
					json.RawMessage(`{"id":"rec-002","day":"2024-03-02","score":90}`),
				},
				NextToken: nil,
			})
		}
	}))
	defer srv.Close()

	// Create temp SQLite database.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-oura.db")

	// Initialize components.
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	client := api.NewClient("test-integration-token", srv.URL)
	syncer := sync.NewSyncer(client, st, nil)

	// Run full sync.
	results, err := syncer.SyncAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// Verify all 18 endpoints were synced.
	if len(results) != len(api.Endpoints) {
		t.Errorf("synced %d endpoints, want %d", len(results), len(api.Endpoints))
	}

	// Verify personal_info got 1 record.
	if results["personal_info"] != 1 {
		t.Errorf("personal_info: got %d, want 1", results["personal_info"])
	}

	// Verify heartrate got records (count depends on chunking).
	if results["heartrate"] < 2 {
		t.Errorf("heartrate: got %d, want >= 2", results["heartrate"])
	}

	// Verify standard endpoints got 2 records each.
	for _, ep := range api.Endpoints {
		if ep.IsSingleton || ep.Name == "heartrate" {
			continue
		}
		if results[ep.Name] != 2 {
			t.Errorf("%s: got %d, want 2", ep.Name, results[ep.Name])
		}
	}

	// Verify sync state was recorded for all endpoints.
	for _, ep := range api.Endpoints {
		lastSync, err := st.GetLastSync(ep.Name)
		if err != nil {
			t.Errorf("GetLastSync(%s): %v", ep.Name, err)
			continue
		}
		if lastSync.IsZero() {
			t.Errorf("sync state for %s is zero, expected non-zero", ep.Name)
		}
	}

	// Verify the database file was created on disk.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("database file not created at %s", dbPath)
	}

	// Run a second sync (incremental) - should succeed and use last sync dates.
	results2, err := syncer.SyncAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("SyncAll (incremental): %v", err)
	}

	if len(results2) != len(api.Endpoints) {
		t.Errorf("incremental sync: synced %d endpoints, want %d", len(results2), len(api.Endpoints))
	}
}

// TestIntegration_WithPagination verifies that pagination works end-to-end.
func TestIntegration_WithPagination(t *testing.T) {
	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/v2/usercollection/personal_info" {
			w.Write([]byte(`{"age":30}`))
			return
		}

		if path == "/v2/usercollection/heartrate" {
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"timestamp":"2024-01-01T00:00:00+00:00","bpm":60,"source":"rest"}`)},
				NextToken: nil,
			})
			return
		}

		if path == "/v2/usercollection/daily_activity" {
			n := requestCount.Add(1)
			if n == 1 {
				token := "page2"
				json.NewEncoder(w).Encode(api.PaginatedResponse{
					Data:      []json.RawMessage{json.RawMessage(`{"id":"p1","day":"2024-01-01","score":80}`)},
					NextToken: &token,
				})
			} else {
				json.NewEncoder(w).Encode(api.PaginatedResponse{
					Data:      []json.RawMessage{json.RawMessage(`{"id":"p2","day":"2024-01-02","score":85}`)},
					NextToken: nil,
				})
			}
			return
		}

		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data:      []json.RawMessage{json.RawMessage(`{"id":"x1","day":"2024-01-01","score":70}`)},
			NextToken: nil,
		})
	}))
	defer srv.Close()

	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	client := api.NewClient("token", srv.URL)
	syncer := sync.NewSyncer(client, st, nil)

	results, err := syncer.SyncAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// daily_activity should have 2 records (from 2 pages).
	if results["daily_activity"] != 2 {
		t.Errorf("daily_activity: got %d records, want 2", results["daily_activity"])
	}

	if requestCount.Load() != 2 {
		t.Errorf("daily_activity pagination: got %d requests, want 2", requestCount.Load())
	}
}

// TestPrintSummary verifies the summary output function doesn't panic.
func TestPrintSummary(t *testing.T) {
	// Should not panic with nil/empty.
	printSummary(nil)
	printSummary(map[string]int{})

	// With data.
	printSummary(map[string]int{
		"daily_activity": 5,
		"heartrate":      100,
		"personal_info":  1,
	})
}

// TestIntegration_ContextCancellation verifies graceful shutdown.
func TestIntegration_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"age":30}`))
	}))
	defer srv.Close()

	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	client := api.NewClient("token", srv.URL)
	syncer := sync.NewSyncer(client, st, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = syncer.SyncAll(ctx, 30)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestIntegration_WeatherSync verifies weather sync integration.
func TestIntegration_WeatherSync(t *testing.T) {
	weatherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Daily *struct {
				Time           []string   `json:"time"`
				TempMax        []*float64 `json:"temperature_2m_max"`
				TempMin        []*float64 `json:"temperature_2m_min"`
				TempMean       []*float64 `json:"temperature_2m_mean"`
				Humidity       []*float64 `json:"relative_humidity_2m_mean"`
				Pressure       []*float64 `json:"surface_pressure_mean"`
				Precip         []*float64 `json:"precipitation_sum"`
				WeatherCode    []*float64 `json:"weather_code"`
			} `json:"daily"`
		}{
			Daily: &struct {
				Time        []string   `json:"time"`
				TempMax     []*float64 `json:"temperature_2m_max"`
				TempMin     []*float64 `json:"temperature_2m_min"`
				TempMean    []*float64 `json:"temperature_2m_mean"`
				Humidity    []*float64 `json:"relative_humidity_2m_mean"`
				Pressure    []*float64 `json:"surface_pressure_mean"`
				Precip      []*float64 `json:"precipitation_sum"`
				WeatherCode []*float64 `json:"weather_code"`
			}{
				Time:        []string{"2025-11-01", "2025-11-02"},
				TempMax:     []*float64{ptrF(32.5), ptrF(31.0)},
				TempMin:     []*float64{ptrF(24.0), ptrF(23.0)},
				TempMean:    []*float64{ptrF(28.0), ptrF(27.0)},
				Humidity:    []*float64{ptrF(82.0), ptrF(78.0)},
				Pressure:    []*float64{ptrF(1008.0), ptrF(1009.0)},
				Precip:      []*float64{ptrF(0.0), ptrF(5.0)},
				WeatherCode: []*float64{ptrF(1), ptrF(61)},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer weatherSrv.Close()

	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	cfg := config.Config{
		Locations: []config.Location{
			{City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
		},
	}

	// Convert and upsert locations.
	periods := []weather.LocationPeriod{
		{City: cfg.Locations[0].City, Latitude: cfg.Locations[0].Latitude, Longitude: cfg.Locations[0].Longitude, Timezone: cfg.Locations[0].Timezone, StartDate: cfg.Locations[0].StartDate},
	}
	if err := st.UpsertLocationPeriods(periods); err != nil {
		t.Fatalf("UpsertLocationPeriods: %v", err)
	}

	client := weather.NewClientWithURLs(weatherSrv.URL, weatherSrv.URL)
	logger := slog.Default()
	syncer := weather.NewSyncer(client, st, logger)

	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("weather SyncAll: %v", err)
	}

	if count == 0 {
		t.Error("expected weather records to be synced")
	}

	// Verify data in DB.
	locs, _ := st.GetLocationPeriods()
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}

	lastDay, err := st.GetLastWeatherDay(locs[0].ID)
	if err != nil {
		t.Fatalf("GetLastWeatherDay: %v", err)
	}
	if lastDay == "" {
		t.Error("expected non-empty last weather day")
	}
}

func ptrF(f float64) *float64 { return &f }
