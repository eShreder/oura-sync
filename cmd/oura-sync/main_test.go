package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/store"
	"github.com/user/oura-sync/internal/sync"
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
	st, err := store.New(dbPath)
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

	// Verify heartrate got 2 records.
	if results["heartrate"] != 2 {
		t.Errorf("heartrate: got %d, want 2", results["heartrate"])
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

	st, err := store.New(":memory:")
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

	st, err := store.New(":memory:")
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
