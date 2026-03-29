package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/weather"
)

// ClickHouseStore implements the Store interface using ClickHouse as the backend.
type ClickHouseStore struct {
	conn driver.Conn
}

var _ Store = (*ClickHouseStore)(nil)

var errNotImplemented = errors.New("clickhouse store not yet implemented")

// NewClickHouseStore creates a new ClickHouseStore connected to the given ClickHouse instance.
func NewClickHouseStore(cfg *config.ClickHouse) (*ClickHouseStore, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("opening clickhouse connection: %w", err)
	}

	ctx := context.Background()
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging clickhouse: %w", err)
	}

	s := &ClickHouseStore{conn: conn}
	if err := s.migrate(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

// Close closes the underlying ClickHouse connection.
func (s *ClickHouseStore) Close() error {
	return s.conn.Close()
}

// migrate creates all required tables using ReplacingMergeTree engines.
func (s *ClickHouseStore) migrate(ctx context.Context) error {
	// Create sync_state table.
	if err := s.conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sync_state (
			endpoint String,
			last_sync String,
			updated_at DateTime DEFAULT now()
		) ENGINE = ReplacingMergeTree(updated_at)
		ORDER BY (endpoint)
	`); err != nil {
		return fmt.Errorf("creating sync_state table: %w", err)
	}

	// Create a table for each endpoint.
	for _, ep := range api.Endpoints {
		var ddl string
		switch {
		case ep.Name == "personal_info":
			ddl = `CREATE TABLE IF NOT EXISTS personal_info (
				id Int64,
				data String,
				synced_at DateTime DEFAULT now()
			) ENGINE = ReplacingMergeTree(synced_at)
			ORDER BY (id)`
		case ep.Name == "heartrate":
			ddl = `CREATE TABLE IF NOT EXISTS heartrate (
				timestamp String,
				bpm Nullable(Int64),
				source Nullable(String),
				data String,
				synced_at DateTime DEFAULT now()
			) ENGINE = ReplacingMergeTree(synced_at)
			ORDER BY (timestamp)`
		default:
			ddl = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id String,
				day Nullable(String),
				data String,
				synced_at DateTime DEFAULT now()
			) ENGINE = ReplacingMergeTree(synced_at)
			ORDER BY (id)`, ep.Name)
		}

		if err := s.conn.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("creating table %s: %w", ep.Name, err)
		}
	}

	// Create location_period table.
	if err := s.conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS location_period (
			id Int64,
			city String,
			latitude Float64,
			longitude Float64,
			timezone String,
			start_date String,
			end_date Nullable(String),
			synced_at DateTime DEFAULT now()
		) ENGINE = ReplacingMergeTree(synced_at)
		ORDER BY (city, start_date)
	`); err != nil {
		return fmt.Errorf("creating location_period table: %w", err)
	}

	// Create daily_weather table.
	if err := s.conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS daily_weather (
			day String,
			location_id Int64,
			temperature_max Nullable(Float64),
			temperature_min Nullable(Float64),
			temperature_mean Nullable(Float64),
			apparent_temperature_max Nullable(Float64),
			apparent_temperature_min Nullable(Float64),
			humidity_mean Nullable(Float64),
			dewpoint_mean Nullable(Float64),
			precipitation_sum Nullable(Float64),
			pressure_mean Nullable(Float64),
			wind_speed_max Nullable(Float64),
			cloud_cover_mean Nullable(Float64),
			sunshine_duration Nullable(Float64),
			uv_index_max Nullable(Float64),
			weather_code Nullable(Int64),
			data String,
			synced_at DateTime DEFAULT now()
		) ENGINE = ReplacingMergeTree(synced_at)
		ORDER BY (day, location_id)
	`); err != nil {
		return fmt.Errorf("creating daily_weather table: %w", err)
	}

	return nil
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
