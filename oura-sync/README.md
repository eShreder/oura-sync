# oura-sync

Go CLI tool for incrementally syncing all data from the Oura Ring API v2 into a local SQLite database.

Designed to run via cron or systemd timer. Each run fetches data since the last sync; the first run performs a full load for a configurable period (default 90 days).

## Features

- All 18 Oura API v2 endpoints supported
- Incremental sync with persistent state tracking
- Automatic pagination via `next_token`
- Retry with exponential backoff on 429/5xx errors
- Pure Go, no CGO — single static binary
- Data stored as JSON blobs for forward compatibility with API changes

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
| `--db` | `oura.db` | Path to SQLite database file |
| `--days` | `90` | Number of days to sync on first run |
| `--timeout` | `10m` | Overall sync timeout |

The Oura API token must be set via the `OURA_TOKEN` environment variable. Get a Personal Access Token at https://cloud.ouraring.com/personal-access-tokens.

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

Data is stored in SQLite with one table per endpoint. Each record is stored as a JSON blob in the `data` column.

```sh
# List tables
sqlite3 oura.db ".tables"

# Recent daily activity scores
sqlite3 oura.db "SELECT day, json_extract(data, '$.score') FROM daily_activity ORDER BY day DESC LIMIT 7"

# Heart rate data
sqlite3 oura.db "SELECT timestamp, bpm, source FROM heartrate ORDER BY timestamp DESC LIMIT 10"

# Last sync times
sqlite3 oura.db "SELECT * FROM sync_state"
```

## Running Tests

```sh
cd oura-sync
go test ./...
```
