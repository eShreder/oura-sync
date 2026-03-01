package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/user/oura-sync/internal/api"

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

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
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

