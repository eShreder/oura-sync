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
```

## Notes

- The `data` column is the source of truth — extracted columns are for convenience only.
- Some tables may be empty if the user's ring/subscription doesn't support that feature.
- `heartrate` is the highest-volume table; expect ~200–300 rows per day.
- Durations in `sleep` are in seconds. Timestamps in `sleep` records include timezone offsets.
