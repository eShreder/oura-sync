package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
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

// GetLastSync returns the last sync time for the given endpoint.
// Uses SELECT ... FINAL to get the latest version from ReplacingMergeTree.
func (s *ClickHouseStore) GetLastSync(endpoint string) (time.Time, error) {
	ctx := context.Background()
	var lastSync string
	err := s.conn.QueryRow(ctx,
		"SELECT last_sync FROM sync_state FINAL WHERE endpoint = ?",
		endpoint,
	).Scan(&lastSync)

	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("querying sync state for %s: %w", endpoint, err)
	}

	t, err := time.Parse(time.RFC3339, lastSync)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing sync time for %s: %w", endpoint, err)
	}
	return t, nil
}

// SetLastSync updates the last sync time for the given endpoint.
// Uses plain INSERT; ReplacingMergeTree deduplicates by ORDER BY (endpoint).
func (s *ClickHouseStore) SetLastSync(endpoint string, t time.Time) error {
	ctx := context.Background()
	err := s.conn.Exec(ctx,
		"INSERT INTO sync_state (endpoint, last_sync) VALUES (?, ?)",
		endpoint, t.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("setting sync state for %s: %w", endpoint, err)
	}
	return nil
}

// UpsertRecords inserts or updates records for the given endpoint.
// Extraction logic matches SQLite: personal_info is singleton, heartrate uses timestamp,
// standard endpoints use id+day.
func (s *ClickHouseStore) UpsertRecords(endpointName string, records []json.RawMessage) error {
	if len(records) == 0 {
		return nil
	}

	if !validEndpointName.MatchString(endpointName) {
		return fmt.Errorf("invalid endpoint name: %q", endpointName)
	}

	ctx := context.Background()
	for _, raw := range records {
		if err := s.upsertOne(ctx, endpointName, raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClickHouseStore) upsertOne(ctx context.Context, endpointName string, raw json.RawMessage) error {
	switch endpointName {
	case "personal_info":
		err := s.conn.Exec(ctx,
			"INSERT INTO personal_info (id, data) VALUES (1, ?)",
			string(raw),
		)
		if err != nil {
			return fmt.Errorf("upserting personal_info: %w", err)
		}

	case "heartrate":
		var rec struct {
			Timestamp string `json:"timestamp"`
			BPM       *int   `json:"bpm"`
			Source    string `json:"source"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("parsing heartrate record: %w", err)
		}
		if rec.Timestamp == "" {
			return fmt.Errorf("heartrate record missing timestamp field")
		}
		var bpm *int64
		if rec.BPM != nil {
			v := int64(*rec.BPM)
			bpm = &v
		}
		err := s.conn.Exec(ctx,
			"INSERT INTO heartrate (timestamp, bpm, source, data) VALUES (?, ?, ?, ?)",
			rec.Timestamp, bpm, &rec.Source, string(raw),
		)
		if err != nil {
			return fmt.Errorf("upserting heartrate record: %w", err)
		}

	default:
		var rec struct {
			ID  string `json:"id"`
			Day string `json:"day"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("parsing %s record: %w", endpointName, err)
		}
		if rec.ID == "" {
			return fmt.Errorf("%s record missing id field", endpointName)
		}
		err := s.conn.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (id, day, data) VALUES (?, ?, ?)", endpointName),
			rec.ID, &rec.Day, string(raw),
		)
		if err != nil {
			return fmt.Errorf("upserting %s record: %w", endpointName, err)
		}
	}
	return nil
}

// locationPeriodID generates a deterministic positive Int64 ID from city and start_date.
// Uses FNV-64a hash, right-shifted by 1 to ensure positive value.
func locationPeriodID(city, startDate string) int64 {
	h := fnv.New64a()
	h.Write([]byte(city + "|" + startDate))
	return int64(h.Sum64() >> 1)
}

// UpsertLocationPeriods inserts or updates location periods and removes stale ones.
func (s *ClickHouseStore) UpsertLocationPeriods(periods []weather.LocationPeriod) error {
	ctx := context.Background()

	if len(periods) == 0 {
		// All locations removed: clean up everything.
		if err := s.conn.Exec(ctx, "DELETE FROM daily_weather WHERE 1=1"); err != nil {
			return fmt.Errorf("deleting all weather data: %w", err)
		}
		if err := s.conn.Exec(ctx, "DELETE FROM location_period WHERE 1=1"); err != nil {
			return fmt.Errorf("deleting all location periods: %w", err)
		}
		return nil
	}

	// Check existing periods for weather invalidation.
	for _, p := range periods {
		id := locationPeriodID(p.City, p.StartDate)

		var oldLat, oldLon float64
		var oldTz string
		var oldEndDate *string
		err := s.conn.QueryRow(ctx,
			"SELECT latitude, longitude, timezone, end_date FROM location_period FINAL WHERE id = ?",
			id,
		).Scan(&oldLat, &oldLon, &oldTz, &oldEndDate)

		existed := err == nil
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("querying location period %s: %w", p.City, err)
		}

		// Insert the period (ReplacingMergeTree deduplicates by ORDER BY key).
		endDate := nilIfEmpty(p.EndDate)
		if err := s.conn.Exec(ctx,
			"INSERT INTO location_period (id, city, latitude, longitude, timezone, start_date, end_date) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, p.City, p.Latitude, p.Longitude, p.Timezone, p.StartDate, endDate,
		); err != nil {
			return fmt.Errorf("upserting location period %s: %w", p.City, err)
		}

		// Invalidate cached weather if coordinates or timezone changed.
		if existed {
			if oldLat != p.Latitude || oldLon != p.Longitude || oldTz != p.Timezone {
				if err := s.conn.Exec(ctx, "DELETE FROM daily_weather WHERE location_id = ?", id); err != nil {
					return fmt.Errorf("invalidating weather for %s: %w", p.City, err)
				}
			} else {
				oldEnd := ""
				if oldEndDate != nil {
					oldEnd = *oldEndDate
				}
				if p.EndDate != "" && (oldEnd == "" || p.EndDate < oldEnd) {
					if err := s.conn.Exec(ctx, "DELETE FROM daily_weather WHERE location_id = ? AND day > ?", id, p.EndDate); err != nil {
						return fmt.Errorf("pruning weather for %s: %w", p.City, err)
					}
				}
			}
		}
	}

	// Remove location periods not in the current config set.
	// Build list of valid IDs.
	validIDs := make([]int64, len(periods))
	for i, p := range periods {
		validIDs[i] = locationPeriodID(p.City, p.StartDate)
	}

	idPlaceholders := strings.Repeat("?,", len(validIDs)-1) + "?"
	idArgs := make([]interface{}, len(validIDs))
	for i, id := range validIDs {
		idArgs[i] = id
	}

	// Delete weather data for removed locations first.
	if err := s.conn.Exec(ctx,
		fmt.Sprintf("DELETE FROM daily_weather WHERE location_id NOT IN (%s)", idPlaceholders),
		idArgs...,
	); err != nil {
		return fmt.Errorf("deleting weather for removed locations: %w", err)
	}

	// Delete removed location periods.
	if err := s.conn.Exec(ctx,
		fmt.Sprintf("DELETE FROM location_period WHERE id NOT IN (%s)", idPlaceholders),
		idArgs...,
	); err != nil {
		return fmt.Errorf("deleting removed location periods: %w", err)
	}

	return nil
}

// GetLocationPeriods returns all location periods ordered by start_date.
func (s *ClickHouseStore) GetLocationPeriods() ([]weather.LocationPeriod, error) {
	ctx := context.Background()
	rows, err := s.conn.Query(ctx,
		"SELECT id, city, latitude, longitude, timezone, start_date, end_date FROM location_period FINAL ORDER BY start_date",
	)
	if err != nil {
		return nil, fmt.Errorf("querying location periods: %w", err)
	}
	defer rows.Close()

	var periods []weather.LocationPeriod
	for rows.Next() {
		var p weather.LocationPeriod
		var endDate *string
		if err := rows.Scan(&p.ID, &p.City, &p.Latitude, &p.Longitude, &p.Timezone, &p.StartDate, &endDate); err != nil {
			return nil, fmt.Errorf("scanning location period: %w", err)
		}
		if endDate != nil {
			p.EndDate = *endDate
		}
		periods = append(periods, p)
	}
	return periods, rows.Err()
}

// GetLocationForDay returns the location period that covers the given day.
func (s *ClickHouseStore) GetLocationForDay(day string) (*weather.LocationPeriod, error) {
	ctx := context.Background()
	var p weather.LocationPeriod
	var endDate *string
	err := s.conn.QueryRow(ctx,
		`SELECT id, city, latitude, longitude, timezone, start_date, end_date
		 FROM location_period FINAL
		 WHERE start_date <= ? AND (end_date IS NULL OR end_date >= ?)
		 ORDER BY start_date DESC LIMIT 1`,
		day, day,
	).Scan(&p.ID, &p.City, &p.Latitude, &p.Longitude, &p.Timezone, &p.StartDate, &endDate)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying location for day %s: %w", day, err)
	}
	if endDate != nil {
		p.EndDate = *endDate
	}
	return &p, nil
}

// UpsertWeatherRecords inserts daily weather records for a location.
// ReplacingMergeTree deduplicates by ORDER BY (day, location_id).
func (s *ClickHouseStore) UpsertWeatherRecords(locationID int64, records []weather.DayRecord) error {
	if len(records) == 0 {
		return nil
	}

	ctx := context.Background()
	for _, r := range records {
		var weatherCode *int64
		if r.WeatherCode != nil {
			v := int64(*r.WeatherCode)
			weatherCode = &v
		}
		err := s.conn.Exec(ctx,
			`INSERT INTO daily_weather (day, location_id, temperature_max, temperature_min, temperature_mean,
				apparent_temperature_max, apparent_temperature_min, humidity_mean, dewpoint_mean,
				precipitation_sum, pressure_mean, wind_speed_max, cloud_cover_mean, sunshine_duration,
				uv_index_max, weather_code, data)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Day, locationID, r.TemperatureMax, r.TemperatureMin, r.TemperatureMean,
			r.ApparentTemperatureMax, r.ApparentTemperatureMin, r.HumidityMean, r.DewpointMean,
			r.PrecipitationSum, r.PressureMean, r.WindSpeedMax, r.CloudCoverMean, r.SunshineDuration,
			r.UVIndexMax, weatherCode, string(r.RawJSON),
		)
		if err != nil {
			return fmt.Errorf("upserting weather record for %s: %w", r.Day, err)
		}
	}
	return nil
}

// GetLastWeatherDay returns the most recent day with weather data for a location.
// Returns empty string if no data exists.
func (s *ClickHouseStore) GetLastWeatherDay(locationID int64) (string, error) {
	ctx := context.Background()
	var day string
	err := s.conn.QueryRow(ctx,
		"SELECT day FROM daily_weather FINAL WHERE location_id = ? ORDER BY day DESC LIMIT 1",
		locationID,
	).Scan(&day)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying last weather day for location %d: %w", locationID, err)
	}
	return day, nil
}
