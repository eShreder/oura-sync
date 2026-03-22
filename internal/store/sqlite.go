package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/weather"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database for persisting Oura API data.
type Store struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at dbPath and runs schema migrations.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Pin to a single connection so that per-connection PRAGMAs
	// (foreign_keys, journal_mode) remain in effect for all queries.
	db.SetMaxOpenConns(1)

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}

	// Enable foreign key enforcement (off by default in SQLite).
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate creates all required tables based on the endpoint registry.
func (s *Store) migrate() error {
	// Create sync_state table.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sync_state (
			endpoint TEXT PRIMARY KEY,
			last_sync TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("creating sync_state table: %w", err)
	}

	// Create a table for each endpoint.
	for _, ep := range api.Endpoints {
		var ddl string
		switch {
		case ep.Name == "personal_info":
			ddl = `CREATE TABLE IF NOT EXISTS personal_info (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				data JSON NOT NULL,
				synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`
		case ep.Name == "heartrate":
			ddl = `CREATE TABLE IF NOT EXISTS heartrate (
				timestamp TEXT PRIMARY KEY,
				bpm INTEGER,
				source TEXT,
				data JSON NOT NULL,
				synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`
		default:
			ddl = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				day TEXT,
				data JSON NOT NULL,
				synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`, ep.Name)
		}

		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("creating table %s: %w", ep.Name, err)
		}
	}

	// Create location_period table for weather sync.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS location_period (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			city TEXT NOT NULL,
			latitude REAL NOT NULL,
			longitude REAL NOT NULL,
			timezone TEXT NOT NULL,
			start_date TEXT NOT NULL,
			end_date TEXT,
			synced_at TEXT DEFAULT CURRENT_TIMESTAMP
			/* UNIQUE enforced via CREATE UNIQUE INDEX below (avoids duplicate autoindex on fresh DBs) */
		)
	`); err != nil {
		return fmt.Errorf("creating location_period table: %w", err)
	}

	// Create daily_weather table.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS daily_weather (
			day TEXT NOT NULL,
			location_id INTEGER NOT NULL,
			temperature_max REAL,
			temperature_min REAL,
			temperature_mean REAL,
			apparent_temperature_max REAL,
			apparent_temperature_min REAL,
			humidity_mean REAL,
			dewpoint_mean REAL,
			precipitation_sum REAL,
			pressure_mean REAL,
			wind_speed_max REAL,
			cloud_cover_mean REAL,
			sunshine_duration REAL,
			uv_index_max REAL,
			weather_code INTEGER,
			data JSON NOT NULL,
			synced_at TEXT DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (day, location_id),
			FOREIGN KEY (location_id) REFERENCES location_period(id)
		)
	`); err != nil {
		return fmt.Errorf("creating daily_weather table: %w", err)
	}

	// For pre-existing databases created before UNIQUE(city, start_date) was
	// added to the CREATE TABLE DDL: deduplicate any rows first, keeping the
	// most recently synced entry per (city, start_date) pair, then add the
	// unique index so the ON CONFLICT upsert works correctly.
	// Reassign weather from duplicate location rows to the surviving row.
	// UPDATE OR IGNORE skips rows where the survivor already has that day,
	// preserving as much historical weather data as possible.
	// Survivor is chosen by most recent synced_at (not MAX(id)), because
	// the old select-then-update code could update any duplicate row,
	// making a lower-id row the freshest.
	const survivorSubquery = `
		SELECT id FROM (
			SELECT id, city, start_date,
				ROW_NUMBER() OVER (
					PARTITION BY city, start_date
					ORDER BY synced_at DESC, id DESC
				) AS rn
			FROM location_period
		) WHERE rn = 1
	`
	if _, err := s.db.Exec(`
		UPDATE OR IGNORE daily_weather
		SET location_id = (
			SELECT lp2.id
			FROM location_period lp2
			JOIN location_period dup ON dup.id = daily_weather.location_id
				AND lp2.city = dup.city AND lp2.start_date = dup.start_date
			WHERE lp2.id IN (` + survivorSubquery + `)
		)
		WHERE location_id NOT IN (` + survivorSubquery + `)
	`); err != nil {
		return fmt.Errorf("reassigning weather for duplicate locations: %w", err)
	}
	// Delete any remaining weather rows that couldn't be reassigned (day conflicts).
	if _, err := s.db.Exec(`
		DELETE FROM daily_weather
		WHERE location_id NOT IN (` + survivorSubquery + `)
	`); err != nil {
		return fmt.Errorf("cleaning weather for duplicate locations: %w", err)
	}
	if _, err := s.db.Exec(`
		DELETE FROM location_period
		WHERE id NOT IN (` + survivorSubquery + `)
	`); err != nil {
		return fmt.Errorf("deduplicating location_period rows: %w", err)
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_location_period_city_start ON location_period(city, start_date)`); err != nil {
		return fmt.Errorf("creating location_period unique index: %w", err)
	}

	return nil
}

// validEndpointName matches only lowercase letters, digits, and underscores.
var validEndpointName = regexp.MustCompile(`^[a-z0-9_]+$`)

// UpsertRecords inserts or updates records for the given endpoint.
// It extracts the primary key from the JSON data:
//   - personal_info: singleton row with id=1
//   - heartrate: uses "timestamp" field as PK, also extracts bpm and source
//   - all others: uses "id" field as PK, also extracts "day" field
func (s *Store) UpsertRecords(endpointName string, records []json.RawMessage) error {
	if len(records) == 0 {
		return nil
	}

	if !validEndpointName.MatchString(endpointName) {
		return fmt.Errorf("invalid endpoint name: %q", endpointName)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	for _, raw := range records {
		if err := s.upsertOne(tx, endpointName, raw); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) upsertOne(tx *sql.Tx, endpointName string, raw json.RawMessage) error {
	switch endpointName {
	case "personal_info":
		_, err := tx.Exec(
			`INSERT INTO personal_info (id, data, synced_at) VALUES (1, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(id) DO UPDATE SET data=excluded.data, synced_at=excluded.synced_at`,
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
		_, err := tx.Exec(
			`INSERT INTO heartrate (timestamp, bpm, source, data, synced_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(timestamp) DO UPDATE SET bpm=excluded.bpm, source=excluded.source, data=excluded.data, synced_at=excluded.synced_at`,
			rec.Timestamp, rec.BPM, rec.Source, string(raw),
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

		_, err := tx.Exec(
			fmt.Sprintf(
				`INSERT INTO %s (id, day, data, synced_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
				 ON CONFLICT(id) DO UPDATE SET day=excluded.day, data=excluded.data, synced_at=excluded.synced_at`,
				endpointName,
			),
			rec.ID, rec.Day, string(raw),
		)
		if err != nil {
			return fmt.Errorf("upserting %s record: %w", endpointName, err)
		}
	}

	return nil
}

// GetLastSync returns the last sync time for the given endpoint.
// If the endpoint has never been synced, it returns the zero time and nil error.
func (s *Store) GetLastSync(endpoint string) (time.Time, error) {
	var lastSync string
	err := s.db.QueryRow(
		"SELECT last_sync FROM sync_state WHERE endpoint = ?",
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
func (s *Store) SetLastSync(endpoint string, t time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_state (endpoint, last_sync) VALUES (?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET last_sync=excluded.last_sync`,
		endpoint, t.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("setting sync state for %s: %w", endpoint, err)
	}
	return nil
}

// UpsertLocationPeriods syncs location periods from config into the DB.
// It inserts new periods and updates existing ones matched by city+start_date.
func (s *Store) UpsertLocationPeriods(periods []weather.LocationPeriod) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	for _, p := range periods {
		// Read old values for weather invalidation check (may not exist yet).
		var oldID int64
		var oldLat, oldLon float64
		var oldTz, oldEndDate string
		var existed bool
		err := tx.QueryRow(
			"SELECT id, latitude, longitude, timezone, COALESCE(end_date, '') FROM location_period WHERE city = ? AND start_date = ?",
			p.City, p.StartDate,
		).Scan(&oldID, &oldLat, &oldLon, &oldTz, &oldEndDate)
		if err == sql.ErrNoRows {
			existed = false
		} else if err != nil {
			return fmt.Errorf("querying location period %s: %w", p.City, err)
		} else {
			existed = true
		}

		// Atomic upsert — no race between concurrent writers.
		_, err = tx.Exec(
			`INSERT INTO location_period (city, latitude, longitude, timezone, start_date, end_date, synced_at)
			 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(city, start_date) DO UPDATE SET
				latitude=excluded.latitude, longitude=excluded.longitude,
				timezone=excluded.timezone, end_date=excluded.end_date,
				synced_at=excluded.synced_at`,
			p.City, p.Latitude, p.Longitude, p.Timezone, p.StartDate, nilIfEmpty(p.EndDate),
		)
		if err != nil {
			return fmt.Errorf("upserting location period %s: %w", p.City, err)
		}

		// Invalidate cached weather if coordinates or timezone changed.
		if existed {
			if oldLat != p.Latitude || oldLon != p.Longitude || oldTz != p.Timezone {
				if _, err := tx.Exec("DELETE FROM daily_weather WHERE location_id = ?", oldID); err != nil {
					return fmt.Errorf("invalidating weather for %s: %w", p.City, err)
				}
			} else if p.EndDate != "" && (oldEndDate == "" || p.EndDate < oldEndDate) {
				// End date was shortened — prune weather data beyond the new end date.
				if _, err := tx.Exec("DELETE FROM daily_weather WHERE location_id = ? AND day > ?", oldID, p.EndDate); err != nil {
					return fmt.Errorf("pruning weather for %s: %w", p.City, err)
				}
			}
		}
	}

	// Remove location periods not in the current config set.
	if len(periods) > 0 {
		// Delete weather data for removed locations first (FK constraint).
		_, err = tx.Exec(
			`DELETE FROM daily_weather WHERE location_id IN (
				SELECT id FROM location_period WHERE (city, start_date) NOT IN (`+
				placeholders(len(periods), 2)+`))`,
			cityStartDateArgs(periods)...,
		)
		if err != nil {
			return fmt.Errorf("deleting weather for removed locations: %w", err)
		}
		_, err = tx.Exec(
			`DELETE FROM location_period WHERE (city, start_date) NOT IN (`+
				placeholders(len(periods), 2)+`)`,
			cityStartDateArgs(periods)...,
		)
		if err != nil {
			return fmt.Errorf("deleting removed location periods: %w", err)
		}
	} else {
		// All locations removed from config: clean up everything.
		if _, err = tx.Exec(`DELETE FROM daily_weather`); err != nil {
			return fmt.Errorf("deleting all weather data: %w", err)
		}
		if _, err = tx.Exec(`DELETE FROM location_period`); err != nil {
			return fmt.Errorf("deleting all location periods: %w", err)
		}
	}

	return tx.Commit()
}

// placeholders generates n groups of m placeholders: (?,?),(?,?),...
func placeholders(n, m int) string {
	group := "(" + strings.Repeat("?,", m-1) + "?)"
	groups := make([]string, n)
	for i := range groups {
		groups[i] = group
	}
	return strings.Join(groups, ",")
}

// cityStartDateArgs returns a flat slice of (city, start_date) pairs for SQL IN.
func cityStartDateArgs(periods []weather.LocationPeriod) []interface{} {
	args := make([]interface{}, 0, len(periods)*2)
	for _, p := range periods {
		args = append(args, p.City, p.StartDate)
	}
	return args
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// GetLocationPeriods returns all location periods ordered by start_date.
func (s *Store) GetLocationPeriods() ([]weather.LocationPeriod, error) {
	rows, err := s.db.Query(
		"SELECT id, city, latitude, longitude, timezone, start_date, COALESCE(end_date, '') FROM location_period ORDER BY start_date",
	)
	if err != nil {
		return nil, fmt.Errorf("querying location periods: %w", err)
	}
	defer rows.Close()

	var periods []weather.LocationPeriod
	for rows.Next() {
		var p weather.LocationPeriod
		if err := rows.Scan(&p.ID, &p.City, &p.Latitude, &p.Longitude, &p.Timezone, &p.StartDate, &p.EndDate); err != nil {
			return nil, fmt.Errorf("scanning location period: %w", err)
		}
		periods = append(periods, p)
	}
	return periods, rows.Err()
}

// GetLocationForDay returns the location period that covers the given day.
func (s *Store) GetLocationForDay(day string) (*weather.LocationPeriod, error) {
	var p weather.LocationPeriod
	err := s.db.QueryRow(
		`SELECT id, city, latitude, longitude, timezone, start_date, COALESCE(end_date, '')
		 FROM location_period
		 WHERE start_date <= ? AND (end_date IS NULL OR end_date >= ?)
		 ORDER BY start_date DESC LIMIT 1`,
		day, day,
	).Scan(&p.ID, &p.City, &p.Latitude, &p.Longitude, &p.Timezone, &p.StartDate, &p.EndDate)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying location for day %s: %w", day, err)
	}
	return &p, nil
}

// UpsertWeatherRecords inserts or updates daily weather records for a location.
func (s *Store) UpsertWeatherRecords(locationID int64, records []weather.DayRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	for _, r := range records {
		_, err := tx.Exec(
			`INSERT INTO daily_weather (day, location_id, temperature_max, temperature_min, temperature_mean,
				apparent_temperature_max, apparent_temperature_min, humidity_mean, dewpoint_mean,
				precipitation_sum, pressure_mean, wind_speed_max, cloud_cover_mean, sunshine_duration,
				uv_index_max, weather_code, data, synced_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(day, location_id) DO UPDATE SET
				temperature_max=excluded.temperature_max, temperature_min=excluded.temperature_min,
				temperature_mean=excluded.temperature_mean, apparent_temperature_max=excluded.apparent_temperature_max,
				apparent_temperature_min=excluded.apparent_temperature_min, humidity_mean=excluded.humidity_mean,
				dewpoint_mean=excluded.dewpoint_mean, precipitation_sum=excluded.precipitation_sum,
				pressure_mean=excluded.pressure_mean, wind_speed_max=excluded.wind_speed_max,
				cloud_cover_mean=excluded.cloud_cover_mean, sunshine_duration=excluded.sunshine_duration,
				uv_index_max=excluded.uv_index_max, weather_code=excluded.weather_code,
				data=excluded.data, synced_at=excluded.synced_at`,
			r.Day, locationID, r.TemperatureMax, r.TemperatureMin, r.TemperatureMean,
			r.ApparentTemperatureMax, r.ApparentTemperatureMin, r.HumidityMean, r.DewpointMean,
			r.PrecipitationSum, r.PressureMean, r.WindSpeedMax, r.CloudCoverMean, r.SunshineDuration,
			r.UVIndexMax, r.WeatherCode, string(r.RawJSON),
		)
		if err != nil {
			return fmt.Errorf("upserting weather record for %s: %w", r.Day, err)
		}
	}

	return tx.Commit()
}

// GetLastWeatherDay returns the most recent day with weather data for a location.
// Returns empty string if no data exists.
func (s *Store) GetLastWeatherDay(locationID int64) (string, error) {
	var day string
	err := s.db.QueryRow(
		"SELECT day FROM daily_weather WHERE location_id = ? ORDER BY day DESC LIMIT 1",
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

