package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockStore implements Store for testing.
type mockStore struct {
	periods  []LocationPeriod
	records  map[int64][]DayRecord
	lastDays map[int64]string
}

func newMockStore() *mockStore {
	return &mockStore{
		records:  make(map[int64][]DayRecord),
		lastDays: make(map[int64]string),
	}
}

func (m *mockStore) UpsertLocationPeriods(periods []LocationPeriod) error {
	m.periods = periods
	return nil
}

func (m *mockStore) GetLocationPeriods() ([]LocationPeriod, error) {
	return m.periods, nil
}

func (m *mockStore) UpsertWeatherRecords(locationID int64, records []DayRecord) error {
	m.records[locationID] = append(m.records[locationID], records...)
	// Update last day.
	for _, r := range records {
		if r.Day > m.lastDays[locationID] {
			m.lastDays[locationID] = r.Day
		}
	}
	return nil
}

func (m *mockStore) GetLastWeatherDay(locationID int64) (string, error) {
	return m.lastDays[locationID], nil
}

func weatherServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startDate := r.URL.Query().Get("start_date")
		pastDays := r.URL.Query().Get("past_days")

		if pastDays != "" {
			// Forecast/recent API.
			json.NewEncoder(w).Encode(apiResponse{
				Daily: &dailyData{
					Time:           []string{"2026-03-18", "2026-03-19", "2026-03-20", "2026-03-21", "2026-03-22"},
					TemperatureMax: []*float64{ptr(14.0), ptr(15.0), ptr(13.0), ptr(16.0), ptr(12.0)},
					TemperatureMean: []*float64{ptr(9.0), ptr(10.0), ptr(8.0), ptr(11.0), ptr(7.0)},
					HumidityMean:   []*float64{ptr(65.0), ptr(70.0), ptr(75.0), ptr(60.0), ptr(80.0)},
				},
			})
			return
		}

		if startDate != "" {
			// Archive API.
			json.NewEncoder(w).Encode(apiResponse{
				Daily: &dailyData{
					Time:           []string{"2025-11-01", "2025-11-02", "2025-11-03"},
					TemperatureMax: []*float64{ptr(32.0), ptr(31.0), ptr(30.0)},
					TemperatureMean: []*float64{ptr(28.0), ptr(27.0), ptr(26.0)},
					HumidityMean:   []*float64{ptr(82.0), ptr(80.0), ptr(85.0)},
				},
			})
			return
		}
	}))
}

func TestSyncer_BackfillFromScratch(t *testing.T) {
	srv := weatherServer()
	defer srv.Close()

	store := newMockStore()
	store.periods = []LocationPeriod{
		{ID: 1, City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2025-11-03"},
	}

	client := NewClientWithURLs(srv.URL, srv.URL)
	syncer := NewSyncer(client, store, nil)

	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	if count == 0 {
		t.Error("expected records to be synced")
	}
	if len(store.records[1]) == 0 {
		t.Error("expected records stored for location 1")
	}
}

func TestSyncer_IncrementalSync(t *testing.T) {
	srv := weatherServer()
	defer srv.Close()

	store := newMockStore()
	store.periods = []LocationPeriod{
		{ID: 1, City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2025-11-03"},
	}
	// Simulate already synced up to 2025-11-02.
	store.lastDays[1] = "2025-11-03"

	client := NewClientWithURLs(srv.URL, srv.URL)
	syncer := NewSyncer(client, store, nil)

	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// Already fully synced, should be 0 new records.
	if count != 0 {
		t.Errorf("expected 0 new records, got %d", count)
	}
}

func TestSyncer_MultipleLocations(t *testing.T) {
	srv := weatherServer()
	defer srv.Close()

	store := newMockStore()
	store.periods = []LocationPeriod{
		{ID: 1, City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2025-11-03"},
		{ID: 2, City: "Tbilisi", Latitude: 41.6938, Longitude: 44.8015, Timezone: "Asia/Tbilisi", StartDate: "2025-11-01", EndDate: "2025-11-03"},
	}

	client := NewClientWithURLs(srv.URL, srv.URL)
	syncer := NewSyncer(client, store, nil)

	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	if count == 0 {
		t.Error("expected records from both locations")
	}
	if len(store.records[1]) == 0 {
		t.Error("expected records for location 1")
	}
	if len(store.records[2]) == 0 {
		t.Error("expected records for location 2")
	}
}

func TestSyncer_APIErrorNonBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	store := newMockStore()
	store.periods = []LocationPeriod{
		{ID: 1, City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01", EndDate: "2025-11-03"},
	}

	client := NewClientWithURLs(srv.URL, srv.URL)
	client.SetMaxRetries(0)
	syncer := NewSyncer(client, store, nil)

	// Should not return error — weather errors are logged as warnings.
	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll should not fail on weather API error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 records on error, got %d", count)
	}
}

func TestSyncer_ContextCancellation(t *testing.T) {
	srv := weatherServer()
	defer srv.Close()

	store := newMockStore()
	store.periods = []LocationPeriod{
		{ID: 1, City: "Da Nang", Latitude: 16.0544, Longitude: 108.2022, Timezone: "Asia/Ho_Chi_Minh", StartDate: "2025-11-01"},
	}

	client := NewClientWithURLs(srv.URL, srv.URL)
	syncer := NewSyncer(client, store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := syncer.SyncAll(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestSyncer_NoLocations(t *testing.T) {
	store := newMockStore()
	client := NewClient()
	syncer := NewSyncer(client, store, nil)

	count, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll with no locations: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 records with no locations, got %d", count)
	}
}
