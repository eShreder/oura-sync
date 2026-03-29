package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/weather"
)

// ClickHouseStore implements the Store interface using ClickHouse as the backend.
type ClickHouseStore struct {
	cfg *config.ClickHouse
}

var _ Store = (*ClickHouseStore)(nil)

var errNotImplemented = errors.New("clickhouse store not yet implemented")

// NewClickHouseStore creates a new ClickHouseStore connected to the given ClickHouse instance.
func NewClickHouseStore(cfg *config.ClickHouse) (*ClickHouseStore, error) {
	return nil, errNotImplemented
}

func (s *ClickHouseStore) Close() error {
	return errNotImplemented
}

func (s *ClickHouseStore) UpsertRecords(endpointName string, records []json.RawMessage) error {
	return errNotImplemented
}

func (s *ClickHouseStore) GetLastSync(endpoint string) (time.Time, error) {
	return time.Time{}, errNotImplemented
}

func (s *ClickHouseStore) SetLastSync(endpoint string, t time.Time) error {
	return errNotImplemented
}

func (s *ClickHouseStore) UpsertLocationPeriods(periods []weather.LocationPeriod) error {
	return errNotImplemented
}

func (s *ClickHouseStore) GetLocationPeriods() ([]weather.LocationPeriod, error) {
	return nil, errNotImplemented
}

func (s *ClickHouseStore) GetLocationForDay(day string) (*weather.LocationPeriod, error) {
	return nil, errNotImplemented
}

func (s *ClickHouseStore) UpsertWeatherRecords(locationID int64, records []weather.DayRecord) error {
	return errNotImplemented
}

func (s *ClickHouseStore) GetLastWeatherDay(locationID int64) (string, error) {
	return "", errNotImplemented
}
