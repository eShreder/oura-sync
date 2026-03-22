package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/store"
)

// newTestDeps sets up a mock HTTP server, API client, and in-memory store for testing.
func newTestDeps(t *testing.T, handler http.Handler) (*api.Client, *store.Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := api.NewClient("test-token", srv.URL)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return client, st, srv
}

func TestSyncEndpoint_StandardEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify date params are sent.
		if r.URL.Query().Get("start_date") == "" {
			t.Error("expected start_date param")
		}
		if r.URL.Query().Get("end_date") == "" {
			t.Error("expected end_date param")
		}

		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data: []json.RawMessage{
				json.RawMessage(`{"id":"act1","day":"2024-01-15","score":85}`),
				json.RawMessage(`{"id":"act2","day":"2024-01-16","score":72}`),
			},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "daily_activity", Path: "/v2/usercollection/daily_activity"}
	count, err := s.SyncEndpoint(context.Background(), ep, "2024-01-15", "2024-01-16")
	if err != nil {
		t.Fatalf("SyncEndpoint: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d records, want 2", count)
	}
}

func TestSyncEndpoint_Heartrate_UsesDatetime(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify datetime params are sent (not date).
		if r.URL.Query().Get("start_datetime") == "" {
			t.Error("expected start_datetime param for heartrate")
		}
		if r.URL.Query().Get("end_datetime") == "" {
			t.Error("expected end_datetime param for heartrate")
		}
		if r.URL.Query().Get("start_date") != "" {
			t.Error("heartrate should not use start_date")
		}

		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data: []json.RawMessage{
				json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
			},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "heartrate", Path: "/v2/usercollection/heartrate", UseDatetime: true}
	count, err := s.SyncEndpoint(context.Background(), ep, "2024-01-15", "2024-01-15")
	if err != nil {
		t.Fatalf("SyncEndpoint heartrate: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d records, want 1", count)
	}
}

func TestSyncEndpoint_PersonalInfo_Singleton(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// personal_info should not have date params.
		if r.URL.Query().Get("start_date") != "" {
			t.Error("personal_info should not send start_date")
		}

		// Returns a single object, not a paginated response.
		w.Write([]byte(`{"age":35,"weight":75.5,"email":"test@example.com"}`))
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "personal_info", Path: "/v2/usercollection/personal_info", IsSingleton: true}
	count, err := s.SyncEndpoint(context.Background(), ep, "", "")
	if err != nil {
		t.Fatalf("SyncEndpoint personal_info: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d records, want 1", count)
	}
}

func TestSyncEndpoint_EmptyResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data:      []json.RawMessage{},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "daily_activity", Path: "/v2/usercollection/daily_activity"}
	count, err := s.SyncEndpoint(context.Background(), ep, "2024-01-15", "2024-01-16")
	if err != nil {
		t.Fatalf("SyncEndpoint empty: %v", err)
	}
	if count != 0 {
		t.Errorf("got %d records, want 0", count)
	}
}

func TestSyncEndpoint_WithPagination(t *testing.T) {
	var requestCount atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		if n == 1 {
			token := "page2"
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"1","day":"2024-01-15","score":80}`)},
				NextToken: &token,
			})
		} else {
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"2","day":"2024-01-16","score":90}`)},
				NextToken: nil,
			})
		}
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "daily_activity", Path: "/v2/usercollection/daily_activity"}
	count, err := s.SyncEndpoint(context.Background(), ep, "2024-01-15", "2024-01-16")
	if err != nil {
		t.Fatalf("SyncEndpoint paginated: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d records, want 2", count)
	}
	if requestCount.Load() != 2 {
		t.Errorf("expected 2 HTTP requests, got %d", requestCount.Load())
	}
}

func TestSyncEndpoint_APIError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ep := api.Endpoint{Name: "daily_activity", Path: "/v2/usercollection/daily_activity"}
	_, err := s.SyncEndpoint(context.Background(), ep, "2024-01-15", "2024-01-16")
	if err == nil {
		t.Fatal("expected error from API, got nil")
	}
}

func TestSyncAll_FirstRun(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/v2/usercollection/personal_info" {
			w.Write([]byte(`{"age":30,"email":"test@example.com"}`))
			return
		}

		// All other endpoints return one record.
		if path == "/v2/usercollection/heartrate" {
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data: []json.RawMessage{
					json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`),
				},
				NextToken: nil,
			})
			return
		}

		// Standard endpoints.
		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data: []json.RawMessage{
				json.RawMessage(`{"id":"rec1","day":"2024-01-15","score":80}`),
			},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	results, err := s.SyncAll(context.Background(), 90)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// Should have results for all 18 endpoints.
	if len(results) != len(api.Endpoints) {
		t.Errorf("got results for %d endpoints, want %d", len(results), len(api.Endpoints))
	}

	// personal_info should have 1 record.
	if results["personal_info"] != 1 {
		t.Errorf("personal_info: got %d records, want 1", results["personal_info"])
	}

	// heartrate should have 1 record.
	if results["heartrate"] != 1 {
		t.Errorf("heartrate: got %d records, want 1", results["heartrate"])
	}

	// All endpoints should have their sync state set.
	for _, ep := range api.Endpoints {
		lastSync, err := st.GetLastSync(ep.Name)
		if err != nil {
			t.Errorf("getting sync state for %s: %v", ep.Name, err)
		}
		if lastSync.IsZero() {
			t.Errorf("sync state for %s should not be zero after SyncAll", ep.Name)
		}
	}
}

func TestSyncAll_IncrementalSync(t *testing.T) {
	var capturedStartDate string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/v2/usercollection/personal_info" {
			w.Write([]byte(`{"age":30,"email":"test@example.com"}`))
			return
		}

		// Capture start_date from daily_activity to verify incremental behavior.
		if path == "/v2/usercollection/daily_activity" {
			capturedStartDate = r.URL.Query().Get("start_date")
		}

		if path == "/v2/usercollection/heartrate" {
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"timestamp":"2024-06-15T10:00:00+00:00","bpm":70,"source":"awake"}`)},
				NextToken: nil,
			})
			return
		}

		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data:      []json.RawMessage{json.RawMessage(`{"id":"rec1","day":"2024-06-15","score":80}`)},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	// Simulate a previous sync by setting last_sync for daily_activity.
	prevSync := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	if err := st.SetLastSync("daily_activity", prevSync); err != nil {
		t.Fatalf("setting prev sync: %v", err)
	}

	_, err := s.SyncAll(context.Background(), 90)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// daily_activity should have used the last sync date, not defaultDays.
	if capturedStartDate != "2024-06-10" {
		t.Errorf("expected start_date=2024-06-10 for incremental sync, got %q", capturedStartDate)
	}
}

func TestSyncAll_ContextCancellation(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay to give context time to cancel (not actually needed since we cancel before).
		w.Write([]byte(`{"age":30}`))
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := s.SyncAll(ctx, 90)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestSyncAll_NotFoundSkipsEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/v2/usercollection/personal_info" {
			w.Write([]byte(`{"age":30,"email":"test@example.com"}`))
			return
		}

		// vo2_max returns 404.
		if path == "/v2/usercollection/vo2_max" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"detail":"Not Found"}`))
			return
		}

		if path == "/v2/usercollection/heartrate" {
			json.NewEncoder(w).Encode(api.PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"timestamp":"2024-01-15T10:00:00+00:00","bpm":72,"source":"awake"}`)},
				NextToken: nil,
			})
			return
		}

		json.NewEncoder(w).Encode(api.PaginatedResponse{
			Data:      []json.RawMessage{json.RawMessage(`{"id":"rec1","day":"2024-01-15","score":80}`)},
			NextToken: nil,
		})
	})

	client, st, _ := newTestDeps(t, handler)
	s := NewSyncer(client, st, nil)

	results, err := s.SyncAll(context.Background(), 90)
	if err != nil {
		t.Fatalf("SyncAll should succeed despite 404: %v", err)
	}

	// vo2_max should not be in results (skipped).
	if _, ok := results["vo2_max"]; ok {
		t.Error("vo2_max should not be in results when 404")
	}

	// heartrate (after vo2_max) should still be synced.
	if results["heartrate"] != 1 {
		t.Errorf("heartrate: got %d records, want 1 (should sync despite earlier 404)", results["heartrate"])
	}

	// vo2_max sync state should NOT be updated.
	lastSync, err := st.GetLastSync("vo2_max")
	if err != nil {
		t.Fatalf("getting vo2_max sync state: %v", err)
	}
	if !lastSync.IsZero() {
		t.Error("vo2_max sync state should not be set when endpoint returned 404")
	}
}

func TestSyncAll_APIErrorStopsSync(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// personal_info is first -- fail it.
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	})

	client, st, _ := newTestDeps(t, handler)
	// Set maxRetries to 0 to fail fast.
	client.SetMaxRetries(0)
	s := NewSyncer(client, st, nil)

	_, err := s.SyncAll(context.Background(), 90)
	if err == nil {
		t.Fatal("expected error from API failure, got nil")
	}
}
