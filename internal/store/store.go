package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/user/oura-sync/internal/weather"
)

// Store defines the interface for persisting Oura API data.
type Store interface {
	Close() error
	UpsertRecords(ctx context.Context, endpointName string, records []json.RawMessage) error
	GetLastSync(ctx context.Context, endpoint string) (time.Time, error)
	SetLastSync(ctx context.Context, endpoint string, t time.Time) error
	UpsertLocationPeriods(ctx context.Context, periods []weather.LocationPeriod) error
	GetLocationPeriods(ctx context.Context) ([]weather.LocationPeriod, error)
	GetLocationForDay(ctx context.Context, day string) (*weather.LocationPeriod, error)
	UpsertWeatherRecords(ctx context.Context, locationID int64, records []weather.DayRecord) error
	GetLastWeatherDay(ctx context.Context, locationID int64) (string, error)
}
