package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_AuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-123" {
			t.Errorf("expected Bearer test-token-123, got %s", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("test-token-123", srv.URL)
	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/usercollection/daily_activity", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
}

func TestDo_QueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start_date") != "2025-01-01" {
			t.Errorf("expected start_date=2025-01-01, got %s", r.URL.Query().Get("start_date"))
		}
		if r.URL.Query().Get("end_date") != "2025-01-31" {
			t.Errorf("expected end_date=2025-01-31, got %s", r.URL.Query().Get("end_date"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	params := url.Values{}
	params.Set("start_date", "2025-01-01")
	params.Set("end_date", "2025-01-31")

	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/test", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
}

func TestDo_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	c := NewClient("bad-token", srv.URL)
	_, err := c.Do(context.Background(), http.MethodGet, "/v2/test", nil)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestDo_RetryOn429(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	c.maxRetries = 3

	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestDo_RetryOn5xx(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)

	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestDo_RetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	c.maxRetries = 1

	_, err := c.Do(context.Background(), http.MethodGet, "/v2/test", nil)
	if err == nil {
		t.Fatal("expected error when retries exhausted")
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := c.Do(ctx, http.MethodGet, "/v2/test", nil)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

func TestFetch_SinglePage(t *testing.T) {
	response := PaginatedResponse{
		Data: []json.RawMessage{
			json.RawMessage(`{"id":"1","value":"a"}`),
			json.RawMessage(`{"id":"2","value":"b"}`),
		},
		NextToken: nil,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	data, err := c.Fetch(context.Background(), "/v2/usercollection/daily_activity", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data) != 2 {
		t.Fatalf("expected 2 records, got %d", len(data))
	}
}

func TestFetch_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		nextToken := r.URL.Query().Get("next_token")

		switch {
		case n == 1 && nextToken == "":
			token := "page2"
			json.NewEncoder(w).Encode(PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"1"}`)},
				NextToken: &token,
			})
		case n == 2 && nextToken == "page2":
			token := "page3"
			json.NewEncoder(w).Encode(PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"2"}`)},
				NextToken: &token,
			})
		case n == 3 && nextToken == "page3":
			json.NewEncoder(w).Encode(PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"3"}`)},
				NextToken: nil,
			})
		default:
			t.Errorf("unexpected request: count=%d, next_token=%s", n, nextToken)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	data, err := c.Fetch(context.Background(), "/v2/usercollection/daily_activity", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data) != 3 {
		t.Fatalf("expected 3 records, got %d", len(data))
	}
	if requestCount.Load() != 3 {
		t.Errorf("expected 3 requests, got %d", requestCount.Load())
	}
}

func TestFetch_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PaginatedResponse{
			Data:      []json.RawMessage{},
			NextToken: nil,
		})
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	data, err := c.Fetch(context.Background(), "/v2/usercollection/daily_activity", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data) != 0 {
		t.Fatalf("expected 0 records, got %d", len(data))
	}
}

func TestFetch_RetryDuringPagination(t *testing.T) {
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)

		switch n {
		case 1:
			// First page succeeds
			token := "page2"
			json.NewEncoder(w).Encode(PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"1"}`)},
				NextToken: &token,
			})
		case 2:
			// Second request returns 429
			w.WriteHeader(http.StatusTooManyRequests)
		case 3:
			// Retry succeeds
			json.NewEncoder(w).Encode(PaginatedResponse{
				Data:      []json.RawMessage{json.RawMessage(`{"id":"2"}`)},
				NextToken: nil,
			})
		}
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	data, err := c.Fetch(context.Background(), "/v2/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data) != 2 {
		t.Fatalf("expected 2 records, got %d", len(data))
	}
}

func TestFetch_PreservesQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start_date") != "2025-01-01" {
			t.Errorf("start_date not preserved, got %s", r.URL.Query().Get("start_date"))
		}
		json.NewEncoder(w).Encode(PaginatedResponse{
			Data:      []json.RawMessage{json.RawMessage(`{"id":"1"}`)},
			NextToken: nil,
		})
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	params := url.Values{}
	params.Set("start_date", "2025-01-01")

	data, err := c.Fetch(context.Background(), "/v2/test", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("expected 1 record, got %d", len(data))
	}
}

func TestFetch_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not valid json at all`))
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL)
	_, err := c.Fetch(context.Background(), "/v2/test", nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}

func TestDo_EmptyTokenSendsEmptyBearer(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// An empty token still sends "Bearer " header. The CLI prevents this by
	// checking OURA_TOKEN before creating the client.
	c := NewClient("", srv.URL)
	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Go's HTTP library trims trailing whitespace, so "Bearer " becomes "Bearer".
	if receivedAuth != "Bearer" {
		t.Errorf("expected 'Bearer' header with empty token, got %q", receivedAuth)
	}
}
