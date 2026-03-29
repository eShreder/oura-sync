package store

import (
	"encoding/json"
	"time"

	"github.com/user/oura-sync/internal/weather"
)

// Store defines the interface for persisting Oura API data.
type Store interface {
	Close() error
	UpsertRecords(endpointName string, records []json.RawMessage) error
	GetLastSync(endpoint string) (time.Time, error)
	SetLastSync(endpoint string, t time.Time) error
	UpsertLocationPeriods(periods []weather.LocationPeriod) error
	GetLocationPeriods() ([]weather.LocationPeriod, error)
	GetLocationForDay(day string) (*weather.LocationPeriod, error)
	UpsertWeatherRecords(locationID int64, records []weather.DayRecord) error
	GetLastWeatherDay(locationID int64) (string, error)
}
