package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchDaily_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query params.
		q := r.URL.Query()
		if q.Get("latitude") == "" {
			t.Error("expected latitude param")
		}
		if q.Get("longitude") == "" {
			t.Error("expected longitude param")
		}
		if q.Get("timezone") != "Asia/Ho_Chi_Minh" {
			t.Errorf("expected timezone=Asia/Ho_Chi_Minh, got %s", q.Get("timezone"))
		}
		if q.Get("start_date") != "2025-11-01" {
			t.Errorf("expected start_date=2025-11-01, got %s", q.Get("start_date"))
		}
		if q.Get("end_date") != "2025-11-03" {
			t.Errorf("expected end_date=2025-11-03, got %s", q.Get("end_date"))
		}
		if q.Get("daily") == "" {
			t.Error("expected daily param")
		}

		json.NewEncoder(w).Encode(apiResponse{
			Daily: &dailyData{
				Time:           []string{"2025-11-01", "2025-11-02", "2025-11-03"},
				TemperatureMax: []*float64{ptr(32.5), ptr(31.8), ptr(30.2)},
				TemperatureMin: []*float64{ptr(24.1), ptr(23.5), ptr(22.8)},
				TemperatureMean: []*float64{ptr(28.3), ptr(27.6), ptr(26.5)},
				HumidityMean:   []*float64{ptr(82.0), ptr(78.5), ptr(85.0)},
				PressureMean:   []*float64{ptr(1008.5), ptr(1009.2), ptr(1007.8)},
				PrecipitationSum: []*float64{ptr(0.0), ptr(5.2), ptr(12.3)},
				WeatherCode:    []*float64{ptr(1), ptr(61), ptr(63)},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	records, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-03")
	if err != nil {
		t.Fatalf("FetchDaily: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}

	if records[0].Day != "2025-11-01" {
		t.Errorf("records[0].Day = %q, want 2025-11-01", records[0].Day)
	}
	if records[0].TemperatureMax == nil || *records[0].TemperatureMax != 32.5 {
		t.Errorf("records[0].TemperatureMax = %v, want 32.5", records[0].TemperatureMax)
	}
	if records[0].HumidityMean == nil || *records[0].HumidityMean != 82.0 {
		t.Errorf("records[0].HumidityMean = %v, want 82.0", records[0].HumidityMean)
	}
	if records[0].WeatherCode == nil || *records[0].WeatherCode != 1 {
		t.Errorf("records[0].WeatherCode = %v, want 1", records[0].WeatherCode)
	}
	if records[0].RawJSON == nil {
		t.Error("records[0].RawJSON should not be nil")
	}
}

func TestFetchRecent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("past_days") != "5" {
			t.Errorf("expected past_days=5, got %s", q.Get("past_days"))
		}
		if q.Get("forecast_days") != "0" {
			t.Errorf("expected forecast_days=0, got %s", q.Get("forecast_days"))
		}

		json.NewEncoder(w).Encode(apiResponse{
			Daily: &dailyData{
				Time:           []string{"2026-03-17", "2026-03-18"},
				TemperatureMax: []*float64{ptr(12.0), ptr(14.0)},
				TemperatureMin: []*float64{ptr(4.0), ptr(5.0)},
				TemperatureMean: []*float64{ptr(8.0), ptr(9.5)},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	records, err := c.FetchRecent(context.Background(), 41.6938, 44.8015, "Asia/Tbilisi", 5)
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[1].Day != "2026-03-18" {
		t.Errorf("records[1].Day = %q, want 2026-03-18", records[1].Day)
	}
}

func TestFetch_RetryOn5xx(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(apiResponse{
			Daily: &dailyData{
				Time:           []string{"2025-11-01"},
				TemperatureMax: []*float64{ptr(30.0)},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	records, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err != nil {
		t.Fatalf("FetchDaily with retries: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestFetch_RetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	c.SetMaxRetries(1)
	_, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err == nil {
		t.Fatal("expected error when retries exhausted")
	}
}

func TestFetch_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse{Daily: nil})
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	records, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err != nil {
		t.Fatalf("FetchDaily empty: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}

func TestFetch_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	_, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetch_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":true,"reason":"Invalid date range"}`))
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	_, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestFetch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.FetchDaily(ctx, 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestFetch_NullValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse{
			Daily: &dailyData{
				Time:           []string{"2025-11-01"},
				TemperatureMax: []*float64{ptr(30.0)},
				TemperatureMin: []*float64{nil},
				HumidityMean:   []*float64{},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURLs(srv.URL, srv.URL)
	records, err := c.FetchDaily(context.Background(), 16.0544, 108.2022, "Asia/Ho_Chi_Minh", "2025-11-01", "2025-11-01")
	if err != nil {
		t.Fatalf("FetchDaily with nulls: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].TemperatureMax == nil || *records[0].TemperatureMax != 30.0 {
		t.Errorf("TemperatureMax should be 30.0, got %v", records[0].TemperatureMax)
	}
	if records[0].TemperatureMin != nil {
		t.Errorf("TemperatureMin should be nil, got %v", records[0].TemperatureMin)
	}
	if records[0].HumidityMean != nil {
		t.Errorf("HumidityMean should be nil (short slice), got %v", records[0].HumidityMean)
	}
}

func ptr(f float64) *float64 {
	return &f
}
