# oura-sync

Go CLI tool for incrementally syncing all data from the Oura Ring API v2 into a local SQLite or ClickHouse database.

Designed to run via cron or systemd timer. Each run fetches data since the last sync; the first run performs a full load for a configurable period (default 90 days).

## Features

- All 18 Oura API v2 endpoints supported
- Incremental sync with persistent state tracking
- Automatic pagination via `next_token`
- Retry with exponential backoff on 429/5xx errors
- Pure Go, no CGO — single static binary
- Dual storage backends: SQLite (default) or ClickHouse
- Data stored as JSON blobs for forward compatibility with API changes
- Weather data sync via Open-Meteo API (free, no API key) for biometric–weather correlation

## Supported Endpoints

`personal_info`, `daily_activity`, `daily_readiness`, `daily_sleep`, `daily_spo2`, `daily_stress`, `daily_cardiovascular_age`, `daily_resilience`, `sleep`, `sleep_time`, `rest_mode_period`, `ring_configuration`, `tag`, `enhanced_tag`, `workout`, `session`, `vo2_max`, `heartrate`

## Installation

Requires Go 1.24+.

```sh
go build -o oura-sync ./cmd/oura-sync/
```

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `oura-sync.yaml` | Path to YAML config file |
| `--db` | `oura.db` | Path to SQLite database file |
| `--days` | `90` | Number of days to sync on first run |
| `--timeout` | `10m` | Overall sync timeout |
| `--skip-weather` | `false` | Skip weather data sync |

The Oura API token must be set via the `OURA_TOKEN` environment variable. Get a Personal Access Token at https://cloud.ouraring.com/personal-access-tokens.

### Storage Backend

By default, oura-sync stores data in a local SQLite database. To use ClickHouse instead, add a `clickhouse` section to your YAML config:

```yaml
clickhouse:
  host: "localhost"
  port: 9000
  database: "oura"
  user: "default"
  password: ""
  secure: false    # set to true for TLS (recommended for remote/cloud instances)
```

When the `clickhouse` section is present, SQLite is not used. The `--db` flag is ignored.

The ClickHouse database must exist before the first run (e.g., `CREATE DATABASE oura`). Tables are created automatically. Requires ClickHouse 23.3+ (for lightweight DELETE support).

ClickHouse uses ReplacingMergeTree engines for all tables, providing deduplication on merge. Reads use `SELECT ... FINAL` to get the latest version of each row. See [DATABASE.md](DATABASE.md) for schema details.

### Weather / Location Config

To enable weather sync, add `locations` to your YAML config. Each entry defines where you were and when. The `end_date` for each location is auto-derived from the next entry's `start_date`.

```yaml
locations:
  - city: "Da Nang"
    latitude: 16.0544
    longitude: 108.2022
    timezone: "Asia/Ho_Chi_Minh"
    start_date: "2025-11-01"
  - city: "Tbilisi"
    latitude: 41.6938
    longitude: 44.8015
    timezone: "Asia/Tbilisi"
    start_date: "2026-03-13"
```

Weather data is fetched from [Open-Meteo](https://open-meteo.com/) (free, no API key). Historical data comes from the archive API; the most recent ~5 days use the forecast API's `past_days` parameter. Weather sync errors never block the Oura sync.

## Usage

```sh
# Basic usage
OURA_TOKEN=your_token ./oura-sync

# Custom database path and initial sync period
OURA_TOKEN=your_token ./oura-sync --db=/path/to/oura.db --days=365

# With a longer timeout for first sync
OURA_TOKEN=your_token ./oura-sync --timeout=30m
```

## Cron Setup

Sync every 4 hours:

```crontab
0 */4 * * * OURA_TOKEN=your_token /path/to/oura-sync --db=/path/to/oura.db
```

Or use a `.env` file (see `.env.example`):

```crontab
0 */4 * * * . /path/to/.env && /path/to/oura-sync --db=/path/to/oura.db
```

## Querying Data

Data is stored with one table per endpoint. Each record is stored as a JSON blob in the `data` column.

### SQLite

```sh
# List tables
sqlite3 oura.db ".tables"

# Recent daily activity scores
sqlite3 oura.db "SELECT day, json_extract(data, '$.score') FROM daily_activity ORDER BY day DESC LIMIT 7"

# Heart rate data
sqlite3 oura.db "SELECT timestamp, bpm, source FROM heartrate ORDER BY timestamp DESC LIMIT 10"

# Last sync times
sqlite3 oura.db "SELECT * FROM sync_state"

# Correlate sleep score with weather
sqlite3 oura.db "
SELECT ds.day,
       json_extract(ds.data, '$.score') AS sleep_score,
       dw.temperature_mean, dw.humidity_mean, dw.pressure_mean
FROM daily_sleep ds
JOIN daily_weather dw ON dw.day = ds.day
JOIN location_period lp ON lp.id = dw.location_id
  AND ds.day >= lp.start_date AND (lp.end_date IS NULL OR ds.day <= lp.end_date)
ORDER BY ds.day;
"
```

### ClickHouse

```sql
-- Recent daily activity scores
SELECT day, JSONExtractInt(data, 'score') FROM daily_activity FINAL ORDER BY day DESC LIMIT 7;

-- Heart rate data
SELECT timestamp, bpm, source FROM heartrate FINAL ORDER BY timestamp DESC LIMIT 10;

-- Correlate sleep with weather
SELECT ds.day,
       JSONExtractInt(ds.data, 'score') AS sleep_score,
       dw.temperature_mean, dw.humidity_mean, dw.pressure_mean
FROM daily_sleep ds FINAL
JOIN daily_weather dw FINAL ON dw.day = ds.day
JOIN location_period lp FINAL ON lp.id = dw.location_id
  AND ds.day >= lp.start_date AND (lp.end_date IS NULL OR ds.day <= lp.end_date)
ORDER BY ds.day;
```

Note: ClickHouse queries require `FINAL` after table names to get deduplicated results. Use `JSONExtract*` functions instead of SQLite's `json_extract`.

## Running Tests

```sh
cd oura-sync
go test ./...
```
