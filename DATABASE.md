# Oura Ring — SQLite Database Schema

SQLite database synced from the [Oura Ring API v2](https://cloud.ouraring.com/v2/docs).
Contains personal health & wellness data: sleep, activity, heart rate, readiness, stress, etc.

## Conventions

- **JSON storage**: every data table has a `data` column (JSON) with the full API response for that record. Extracted columns (`id`, `day`, `bpm`, etc.) are denormalized for convenient querying.
- **Dates**: `day` columns use `YYYY-MM-DD` format (local calendar date). `timestamp` uses ISO 8601 with timezone.
- **Upsert**: records are inserted or replaced by primary key on each sync, so the database always holds the latest version.
- **`synced_at`**: timestamp of when the row was last written (UTC, `CURRENT_TIMESTAMP`).

## Tables

### `sync_state`

Tracks the last successful sync time per endpoint. Used to do incremental fetches.

| Column      | Type | Description                          |
|-------------|------|--------------------------------------|
| `endpoint`  | TEXT | Endpoint name (PK)                   |
| `last_sync` | TEXT | ISO 8601 / RFC 3339 timestamp        |

---

### `personal_info`

Singleton row (always `id = 1`). User profile: age, weight, height, email, biological sex.

| Column      | Type    | Description                |
|-------------|---------|----------------------------|
| `id`        | INTEGER | Always 1 (PK)             |
| `data`      | JSON    | Full API response          |
| `synced_at` | TEXT    | Last sync timestamp        |

`data` example fields: `age`, `weight`, `height`, `biological_sex`, `email`.

---

### `heartrate`

Heart rate samples, typically every 5 minutes. High volume (hundreds of rows per day).

| Column      | Type    | Description                              |
|-------------|---------|------------------------------------------|
| `timestamp` | TEXT    | ISO 8601 with timezone (PK)             |
| `bpm`       | INTEGER | Beats per minute                         |
| `source`    | TEXT    | Measurement source (`awake`, `rest`, `sleep`, etc.) |
| `data`      | JSON    | Full API record                          |
| `synced_at` | TEXT    | Last sync timestamp                      |

---

### Standard tables (16 endpoints)

All remaining endpoints share the same schema:

| Column      | Type | Description                              |
|-------------|------|------------------------------------------|
| `id`        | TEXT | Oura-assigned UUID (PK)                 |
| `day`       | TEXT | Calendar date `YYYY-MM-DD`              |
| `data`      | JSON | Full API record                          |
| `synced_at` | TEXT | Last sync timestamp                      |

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

---

### `daily_weather`

One row per day per location. Weather data from [Open-Meteo](https://open-meteo.com/).

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

## Querying

All interesting data lives in the `data` JSON column. Use SQLite JSON functions to extract fields.

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

-- Correlate sleep with weather (pick one location, or use a subquery)
SELECT ds.day,
       json_extract(ds.data, '$.score') AS sleep_score,
       dw.temperature_mean, dw.humidity_mean, dw.pressure_mean
FROM daily_sleep ds
JOIN daily_weather dw ON dw.day = ds.day
JOIN location_period lp ON lp.id = dw.location_id
  AND ds.day >= lp.start_date AND (lp.end_date IS NULL OR ds.day <= lp.end_date)
ORDER BY ds.day;
```

## Notes

- The `data` column is the source of truth — extracted columns are for convenience only.
- Some tables may be empty if the user's ring/subscription doesn't support that feature.
- `heartrate` is the highest-volume table; expect ~200–300 rows per day.
- Durations in `sleep` are in seconds. Timestamps in `sleep` records include timezone offsets.
- Weather data requires `locations` configured in YAML. Fetched from Open-Meteo (free, no API key).
