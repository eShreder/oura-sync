# Oura Ring — Database Schema

Database synced from the [Oura Ring API v2](https://cloud.ouraring.com/v2/docs).
Contains personal health & wellness data: sleep, activity, heart rate, readiness, stress, etc.

Two storage backends are supported: **SQLite** (default) and **ClickHouse**.

## Conventions

- **JSON storage**: every data table has a `data` column (JSON) with the full API response for that record. Extracted columns (`id`, `day`, `bpm`, etc.) are denormalized for convenient querying.
- **Dates**: `day` columns use `YYYY-MM-DD` format (local calendar date). `timestamp` uses ISO 8601 with timezone.
- **Upsert**: records are inserted or replaced by primary key on each sync, so the database always holds the latest version.
- **`synced_at`**: timestamp of when the row was last written (UTC, `CURRENT_TIMESTAMP` in SQLite, `now64(3)` in ClickHouse).

## Backend Differences

| Aspect | SQLite | ClickHouse |
|--------|--------|------------|
| Engine | Standard B-tree tables | ReplacingMergeTree |
| Upsert | `INSERT OR REPLACE` (ON CONFLICT) | Plain INSERT; dedup at merge time |
| Reads | Standard SELECT | `SELECT ... FINAL` for deduplicated results |
| JSON functions | `json_extract(data, '$.field')` | `JSONExtractInt(data, 'field')`, etc. |
| Location IDs | AUTOINCREMENT | Deterministic FNV-64a hash: `fnv64a(city + "\|" + start_date) >> 1` |
| Delete | Standard DELETE | Lightweight DELETE (ClickHouse 23.3+) |
| Column types | TEXT, INTEGER, REAL | String, Int64, Float64, Nullable(...) |

## Tables

### `sync_state`

Tracks the last successful sync time per endpoint. Used to do incremental fetches.

**SQLite:**

| Column      | Type | Description                          |
|-------------|------|--------------------------------------|
| `endpoint`  | TEXT | Endpoint name (PK)                   |
| `last_sync` | TEXT | ISO 8601 / RFC 3339 timestamp        |

**ClickHouse:** `ENGINE = ReplacingMergeTree(updated_at) ORDER BY (endpoint)`

| Column       | Type     | Description                          |
|-------------|----------|--------------------------------------|
| `endpoint`  | String   | Endpoint name (ORDER BY key)         |
| `last_sync` | String   | ISO 8601 / RFC 3339 timestamp        |
| `updated_at`| DateTime64(3) | Version column for dedup (`DEFAULT now64(3)`) |

---

### `personal_info`

Singleton row (always `id = 1`). User profile: age, weight, height, email, biological sex.

**SQLite:**

| Column      | Type    | Description                |
|-------------|---------|----------------------------|
| `id`        | INTEGER | Always 1 (PK)             |
| `data`      | JSON    | Full API response          |
| `synced_at` | TEXT    | Last sync timestamp        |

**ClickHouse:** `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (id)`

| Column      | Type     | Description                |
|-------------|----------|----------------------------|
| `id`        | Int64    | Always 1 (ORDER BY key)   |
| `data`      | String   | Full API response (JSON)   |
| `synced_at` | DateTime64(3) | Version column (`DEFAULT now64(3)`) |

`data` example fields: `age`, `weight`, `height`, `biological_sex`, `email`.

---

### `heartrate`

Heart rate samples, typically every 5 minutes. High volume (hundreds of rows per day).

**SQLite:**

| Column      | Type    | Description                              |
|-------------|---------|------------------------------------------|
| `timestamp` | TEXT    | ISO 8601 with timezone (PK)             |
| `bpm`       | INTEGER | Beats per minute                         |
| `source`    | TEXT    | Measurement source (`awake`, `rest`, `sleep`, etc.) |
| `data`      | JSON    | Full API record                          |
| `synced_at` | TEXT    | Last sync timestamp                      |

**ClickHouse:** `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (timestamp)`

| Column      | Type             | Description                              |
|-------------|------------------|------------------------------------------|
| `timestamp` | String           | ISO 8601 with timezone (ORDER BY key)   |
| `bpm`       | Nullable(Int64)  | Beats per minute                         |
| `source`    | Nullable(String) | Measurement source                       |
| `data`      | String           | Full API record (JSON)                   |
| `synced_at` | DateTime64(3)    | Version column (`DEFAULT now64(3)`)      |

---

### Standard tables (16 endpoints)

All remaining endpoints share the same schema.

**SQLite:**

| Column      | Type | Description                              |
|-------------|------|------------------------------------------|
| `id`        | TEXT | Oura-assigned UUID (PK)                 |
| `day`       | TEXT | Calendar date `YYYY-MM-DD`              |
| `data`      | JSON | Full API record                          |
| `synced_at` | TEXT | Last sync timestamp                      |

**ClickHouse:** `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (id)`

| Column      | Type             | Description                              |
|-------------|------------------|------------------------------------------|
| `id`        | String           | Oura-assigned UUID (ORDER BY key)       |
| `day`       | Nullable(String) | Calendar date `YYYY-MM-DD`              |
| `data`      | String           | Full API record (JSON)                   |
| `synced_at` | DateTime64(3)    | Version column (`DEFAULT now64(3)`)      |

These tables are:

| Table                        | Granularity  | What's inside `data`                                              |
|------------------------------|-------------|-------------------------------------------------------------------|
| `daily_activity`             | 1 row/day   | Steps, calories, distance, MET levels, activity score             |
| `daily_readiness`            | 1 row/day   | Readiness score and contributing factors (HRV, temperature, etc.) |
| `daily_sleep`                | 1 row/day   | Sleep score and contributing factors                              |
| `daily_spo2`                 | 1 row/day   | Blood oxygen (SpO2) average                                       |
| `daily_stress`               | 1 row/day   | Daytime stress level and recovery ratio                           |
| `daily_cardiovascular_age`   | 1 row/day   | Estimated cardiovascular age                                       |
| `daily_resilience`           | 1 row/day   | Resilience (stress recovery capacity) metrics                     |
| `sleep`                      | 1 row/sleep period | Detailed sleep session: stages, HR, HRV, movement, timing  |
| `sleep_time`                 | 1 row/day   | Recommended bedtime window                                         |
| `rest_mode_period`           | 1 row/period | Rest mode start/end and settings                                 |
| `ring_configuration`         | 1 row/ring  | Ring hardware info: color, design, size, firmware                  |
| `tag`                        | 1 row/tag   | User-created tags (e.g. "alcohol", "caffeine")                    |
| `enhanced_tag`               | 1 row/tag   | Extended tags with custom text and comments                        |
| `workout`                    | 1 row/workout | Workout type, duration, calories, HR, intensity                 |
| `session`                    | 1 row/session | Guided session (meditation, breathing, etc.)                    |
| `vo2_max`                    | 1 row/measurement | VO2 max estimate                                           |

---

### `location_period`

Tracks where the user was for weather data correlation. Populated from config file.

**SQLite:**

| Column       | Type    | Description                              |
|-------------|---------|------------------------------------------|
| `id`        | INTEGER | Auto-increment PK                        |
| `city`      | TEXT    | City name                                |
| `latitude`  | REAL    | Latitude                                 |
| `longitude` | REAL    | Longitude                                |
| `timezone`  | TEXT    | IANA timezone (e.g. `Asia/Ho_Chi_Minh`)  |
| `start_date`| TEXT    | Start date `YYYY-MM-DD`                  |
| `end_date`  | TEXT    | End date `YYYY-MM-DD` (NULL = ongoing)   |
| `synced_at` | TEXT    | Last sync timestamp                      |

**ClickHouse:** `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (city, start_date)`

| Column       | Type             | Description                                          |
|-------------|------------------|------------------------------------------------------|
| `id`        | Int64            | Deterministic hash: `fnv64a(city+"\|"+start_date) >> 1` |
| `city`      | String           | City name (ORDER BY key part 1)                      |
| `latitude`  | Float64          | Latitude                                             |
| `longitude` | Float64          | Longitude                                            |
| `timezone`  | String           | IANA timezone                                        |
| `start_date`| String           | Start date `YYYY-MM-DD` (ORDER BY key part 2)       |
| `end_date`  | Nullable(String) | End date `YYYY-MM-DD` (NULL = ongoing)               |
| `synced_at` | DateTime64(3)    | Version column (`DEFAULT now64(3)`)                  |

---

### `daily_weather`

One row per day per location. Weather data from [Open-Meteo](https://open-meteo.com/).

**SQLite:**

| Column                     | Type    | Description                              |
|---------------------------|---------|------------------------------------------|
| `day`                     | TEXT    | Calendar date `YYYY-MM-DD` (PK part 1)  |
| `location_id`             | INTEGER | FK to `location_period.id` (PK part 2)  |
| `temperature_max`         | REAL    | Max temperature °C                       |
| `temperature_min`         | REAL    | Min temperature °C                       |
| `temperature_mean`        | REAL    | Mean temperature °C                      |
| `apparent_temperature_max`| REAL    | Max feels-like °C                        |
| `apparent_temperature_min`| REAL    | Min feels-like °C                        |
| `humidity_mean`           | REAL    | Mean relative humidity %                 |
| `dewpoint_mean`           | REAL    | Mean dewpoint °C                         |
| `precipitation_sum`       | REAL    | Total precipitation mm                   |
| `pressure_mean`           | REAL    | Mean sea-level pressure hPa              |
| `wind_speed_max`          | REAL    | Max wind speed km/h                      |
| `cloud_cover_mean`        | REAL    | Mean cloud cover %                       |
| `sunshine_duration`       | REAL    | Sunshine duration seconds                |
| `uv_index_max`            | REAL    | Max UV index                             |
| `weather_code`            | INTEGER | WMO weather code                         |
| `data`                    | JSON    | Full API response for the day            |
| `synced_at`               | TEXT    | Last sync timestamp                      |

**ClickHouse:** `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (day, location_id)`

| Column                     | Type              | Description                              |
|---------------------------|-------------------|------------------------------------------|
| `day`                     | String            | Calendar date `YYYY-MM-DD` (ORDER BY key part 1) |
| `location_id`             | Int64             | Reference to `location_period.id` (ORDER BY key part 2) |
| `temperature_max`         | Nullable(Float64) | Max temperature °C                       |
| `temperature_min`         | Nullable(Float64) | Min temperature °C                       |
| `temperature_mean`        | Nullable(Float64) | Mean temperature °C                      |
| `apparent_temperature_max`| Nullable(Float64) | Max feels-like °C                        |
| `apparent_temperature_min`| Nullable(Float64) | Min feels-like °C                        |
| `humidity_mean`           | Nullable(Float64) | Mean relative humidity %                 |
| `dewpoint_mean`           | Nullable(Float64) | Mean dewpoint °C                         |
| `precipitation_sum`       | Nullable(Float64) | Total precipitation mm                   |
| `pressure_mean`           | Nullable(Float64) | Mean sea-level pressure hPa              |
| `wind_speed_max`          | Nullable(Float64) | Max wind speed km/h                      |
| `cloud_cover_mean`        | Nullable(Float64) | Mean cloud cover %                       |
| `sunshine_duration`       | Nullable(Float64) | Sunshine duration seconds                |
| `uv_index_max`            | Nullable(Float64) | Max UV index                             |
| `weather_code`            | Nullable(Int64)   | WMO weather code                         |
| `data`                    | String            | Full API response (JSON)                 |
| `synced_at`               | DateTime64(3)     | Version column (`DEFAULT now64(3)`)      |

## Querying

All interesting data lives in the `data` JSON column.

### SQLite

Use SQLite JSON functions to extract fields.

```sql
-- Last 7 days of sleep scores
SELECT day, json_extract(data, '$.score') AS score
FROM daily_sleep
WHERE day >= date('now', '-7 days')
ORDER BY day;

-- Average resting heart rate by day
SELECT date(timestamp) AS day, round(avg(bpm)) AS avg_bpm
FROM heartrate
WHERE source = 'rest'
GROUP BY day
ORDER BY day DESC
LIMIT 14;

-- Daily steps
SELECT day, json_extract(data, '$.steps') AS steps
FROM daily_activity
ORDER BY day DESC
LIMIT 30;

-- Sleep stages breakdown for last night
SELECT day,
       json_extract(data, '$.deep_sleep_duration') / 60 AS deep_min,
       json_extract(data, '$.light_sleep_duration') / 60 AS light_min,
       json_extract(data, '$.rem_sleep_duration') / 60 AS rem_min,
       json_extract(data, '$.total_sleep_duration') / 60 AS total_min
FROM sleep
ORDER BY day DESC
LIMIT 1;

-- Readiness score trend
SELECT day, json_extract(data, '$.score') AS readiness
FROM daily_readiness
WHERE day >= date('now', '-30 days')
ORDER BY day;

-- Correlate sleep with weather
SELECT ds.day,
       json_extract(ds.data, '$.score') AS sleep_score,
       dw.temperature_mean, dw.humidity_mean, dw.pressure_mean
FROM daily_sleep ds
JOIN daily_weather dw ON dw.day = ds.day
JOIN location_period lp ON lp.id = dw.location_id
  AND ds.day >= lp.start_date AND (lp.end_date IS NULL OR ds.day <= lp.end_date)
ORDER BY ds.day;
```

### ClickHouse

Use `FINAL` after table names and ClickHouse JSON functions.

```sql
-- Last 7 days of sleep scores
SELECT day, JSONExtractInt(data, 'score') AS score
FROM daily_sleep FINAL
WHERE day >= toString(today() - 7)
ORDER BY day;

-- Average resting heart rate by day
SELECT toDate(timestamp) AS day, round(avg(bpm)) AS avg_bpm
FROM heartrate FINAL
WHERE source = 'rest'
GROUP BY day
ORDER BY day DESC
LIMIT 14;

-- Daily steps
SELECT day, JSONExtractInt(data, 'steps') AS steps
FROM daily_activity FINAL
ORDER BY day DESC
LIMIT 30;

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

## External Tables (ClickHouse only)

These tables are not managed by oura-sync but live in the same ClickHouse database and can be joined with health data for richer analysis.

### `scd41_readings`

Indoor air quality readings from SCD41 CO₂/temperature/humidity sensors. Multiple sensors report to the same table, distinguished by `place`. Readings arrive every ~5 minutes per sensor.

| Column        | Type     | Description                                       |
|---------------|----------|---------------------------------------------------|
| `timestamp`   | DateTime | Measurement time (ORDER BY key)                   |
| `place`       | String   | Sensor location (`bedroom-room`, `work-room`)     |
| `co2`         | UInt16   | CO₂ concentration in ppm                          |
| `temperature` | Float32  | Temperature °C                                    |
| `humidity`    | Float32  | Relative humidity %                               |

**Example queries:**

```sql
-- Average CO₂ by room for the last 24 hours
SELECT place, round(avg(co2)) AS avg_co2
FROM scd41_readings
WHERE timestamp >= now() - INTERVAL 24 HOUR
GROUP BY place;

-- Correlate bedroom CO₂ with sleep score
SELECT ds.day,
       JSONExtractInt(ds.data, 'score') AS sleep_score,
       round(avg(sr.co2)) AS avg_bedroom_co2,
       round(avg(sr.temperature), 1) AS avg_bedroom_temp
FROM daily_sleep ds FINAL
JOIN scd41_readings sr
  ON toDate(sr.timestamp) = toDate(ds.day)
  AND sr.place = 'bedroom-room'
  AND toHour(sr.timestamp) BETWEEN 22 AND 23  -- evening before sleep
GROUP BY ds.day, sleep_score
ORDER BY ds.day DESC
LIMIT 30;
```

---

## Notes

- The `data` column is the source of truth — extracted columns are for convenience only.
- Some tables may be empty if the user's ring/subscription doesn't support that feature.
- `heartrate` is the highest-volume table; expect ~200–300 rows per day.
- Durations in `sleep` are in seconds. Timestamps in `sleep` records include timezone offsets.
- Weather data requires `locations` configured in YAML. Fetched from Open-Meteo (free, no API key).
