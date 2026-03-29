# ClickHouse Dual Backend Support

## Overview
- Add ClickHouse as an alternative storage backend alongside existing SQLite
- Backend selection determined by presence of `clickhouse` section in YAML config
- SQLite remains the default; no breaking changes to existing users
- Enables analytics-friendly columnar storage for users with ClickHouse infrastructure

## Context (from discovery)
- Files/components involved:
  - `internal/store/sqlite.go` (545 lines) — current monolithic store, needs renaming
  - `internal/store/sqlite_test.go` — comprehensive tests (60 test functions)
  - `internal/sync/syncer.go` — uses concrete `*store.Store` type
  - `internal/weather/syncer.go` — defines `Store` interface (subset of store methods)
  - `cmd/oura-sync/main.go` — orchestration, opens store, passes to consumers
  - `internal/config/config.go` — YAML config loading
- Related patterns found:
  - Store uses `database/sql` with `modernc.org/sqlite` driver
  - All upserts use `ON CONFLICT` for idempotency
  - Full JSON stored in `data` column + extracted columns for querying
  - Weather package already defines a `Store` interface (good precedent)
- Dependencies: single dependency (`modernc.org/sqlite`), pure Go, no CGO

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- **Shared test helpers**: common test functions in `internal/store/store_test.go` exercising the `Store` interface for both backends
- **SQLite tests**: in-memory (`:memory:`) — fast, no external deps
- **ClickHouse tests**: `testcontainers-go` with ClickHouse module — real CH in Docker
- **Build tag or skip**: ClickHouse tests skip when Docker unavailable

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase
- **Post-Completion** (no checkboxes): items requiring external action
- **Checkbox placement**: Checkboxes belong only in Task sections

## Implementation Steps

### Task 1: Extract Store interface and rename SQLite implementation
- [x] create `internal/store/store.go` with `Store` interface covering all public methods: `Close`, `UpsertRecords`, `GetLastSync`, `SetLastSync`, `UpsertLocationPeriods`, `GetLocationPeriods`, `GetLocationForDay`, `UpsertWeatherRecords`, `GetLastWeatherDay`
- [x] rename `Store` → `SQLiteStore` and `New` → `NewSQLiteStore` in `internal/store/sqlite.go`
- [x] add compile-time check `var _ Store = (*SQLiteStore)(nil)` in `sqlite.go`
- [x] update `internal/store/sqlite_test.go`: replace `Store` → `SQLiteStore`, `New` → `NewSQLiteStore`
- [x] run tests — `go test ./internal/store/...` must pass

### Task 2: Update consumers to use Store interface
- [x] update `internal/sync/syncer.go`: change `store *store.Store` field and `NewSyncer` param to `store store.Store`
- [x] update `cmd/oura-sync/main.go`: change `runWeatherSync` param from `*store.Store` to `store.Store`
- [x] remove `_ "modernc.org/sqlite"` import from `main.go` (already imported in `sqlite.go`)
- [x] verify `weather.Store` interface is still satisfied (it's a subset of `store.Store`)
- [x] run tests — `go test ./...` must all pass
- [x] run `go build ./...` to verify compilation

### Task 3: Add ClickHouse config
- [x] add `ClickHouse` struct to `internal/config/config.go` with fields: `Host`, `Port`, `Database`, `User`, `Password`
- [x] add `ClickHouse *ClickHouse` field to `Config` struct (nil means SQLite)
- [x] write tests for config loading with `clickhouse` section present and absent
- [x] run tests — `go test ./internal/config/...` must pass

### Task 4: Add backend selection in main.go
- [x] add logic in `cmd/oura-sync/main.go` to choose backend based on `cfg.ClickHouse != nil`
- [x] SQLite path: call `store.NewSQLiteStore(cfg.DB)` as before
- [x] ClickHouse path: call `store.NewClickHouseStore(cfg.ClickHouse)` (stub for now — will be implemented in Task 6)
- [x] run `go build ./...` to verify compilation (ClickHouse store will exist as stub)

### Task 5: Add ClickHouse dependency
- [ ] run `go get github.com/ClickHouse/clickhouse-go/v2`
- [ ] run `go get github.com/testcontainers/testcontainers-go`
- [ ] run `go get github.com/testcontainers/testcontainers-go/modules/clickhouse`
- [ ] run `go mod tidy`

### Task 6: Implement ClickHouseStore — connection and migrations
- [ ] create `internal/store/clickhouse.go` with `ClickHouseStore` struct
- [ ] implement `NewClickHouseStore(cfg)` — connect via `clickhouse-go/v2`, run migrations
- [ ] implement `Close()` method
- [ ] implement table creation with `ReplacingMergeTree` engines:
  - `sync_state`: `ENGINE = ReplacingMergeTree(updated_at) ORDER BY (endpoint)`
  - `personal_info`: `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (id)`
  - `heartrate`: `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (timestamp)`
  - standard endpoints: `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (id)`
  - `location_period`: `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (city, start_date)`
  - `daily_weather`: `ENGINE = ReplacingMergeTree(synced_at) ORDER BY (day, location_id)`
- [ ] add compile-time check `var _ Store = (*ClickHouseStore)(nil)`
- [ ] write testcontainers-based test for connection and table creation
- [ ] run tests — must pass

### Task 7: Implement ClickHouseStore — sync state and record upserts
- [ ] implement `GetLastSync(endpoint)` using `SELECT ... FINAL`
- [ ] implement `SetLastSync(endpoint, time)` using INSERT (ReplacingMergeTree handles dedup)
- [ ] implement `UpsertRecords(endpointName, records)` with same extraction logic as SQLite:
  - `personal_info`: singleton with id=1
  - `heartrate`: extract timestamp, bpm, source
  - standard endpoints: extract id, day
- [ ] write tests for sync state get/set
- [ ] write tests for UpsertRecords — personal_info, heartrate, standard endpoints (insert + update)
- [ ] run tests — must pass

### Task 8: Implement ClickHouseStore — location and weather methods
- [ ] implement `UpsertLocationPeriods(periods)` with deterministic ID: `fnv64a(city + "|" + start_date) >> 1`
- [ ] implement cleanup of stale location periods using lightweight DELETE
- [ ] implement `GetLocationPeriods()` using `SELECT ... FINAL ORDER BY start_date`
- [ ] implement `GetLocationForDay(day)` using `SELECT ... FINAL` with date range filter
- [ ] implement `UpsertWeatherRecords(locationID, records)` with extracted columns
- [ ] implement `GetLastWeatherDay(locationID)` using `SELECT ... FINAL`
- [ ] write tests for all location period methods (insert, update, cleanup)
- [ ] write tests for weather record methods (insert, retrieval, last day)
- [ ] run tests — must pass

### Task 9: Shared test helpers for both backends
- [ ] create `internal/store/store_test.go` with shared test runner accepting `Store` interface
- [ ] extract common test scenarios from `sqlite_test.go` into shared helpers
- [ ] run shared tests against both SQLiteStore (in-memory) and ClickHouseStore (testcontainers)
- [ ] keep SQLite-specific tests (e.g., sqlite_master queries) in `sqlite_test.go`
- [ ] run full test suite — `go test ./...` must pass

### Task 10: Verify acceptance criteria
- [ ] verify all Store interface methods implemented for both backends
- [ ] verify SQLite behavior unchanged (backward compatibility)
- [ ] verify ClickHouse backend works end-to-end through Store interface
- [ ] verify config selection logic (nil clickhouse → SQLite, present → ClickHouse)
- [ ] run full test suite — `go test ./...`
- [ ] run linter — `go vet ./...` — all issues must be fixed
- [ ] verify edge cases: empty records, invalid JSON, 404 handling still works

### Task 11: [Final] Update documentation
- [ ] update README.md with ClickHouse setup instructions and config example
- [ ] update DATABASE.md with ClickHouse schema details
- [ ] update `oura-sync.example.yaml` with commented-out `clickhouse` section

## Technical Details

### ClickHouse Table Engine Strategy
- **ReplacingMergeTree(synced_at)**: Deduplication by ORDER BY key, latest `synced_at` wins
- **Upsert**: Plain INSERT; dedup happens at merge time, `SELECT ... FINAL` for reads
- **Deletes**: Lightweight `DELETE FROM ... WHERE` (ClickHouse 23.3+) for location period cleanup

### Location Period ID Generation (no AUTOINCREMENT in ClickHouse)
- Deterministic: `fnv64a(city + "|" + start_date) >> 1` (right-shift for positive Int64)
- Stable across re-syncs — same input always produces same ID

### Key Differences from SQLite
- No `ON CONFLICT` — use ReplacingMergeTree for dedup
- No foreign keys — referential integrity managed in application
- No `AUTOINCREMENT` — use deterministic hashing
- `SELECT ... FINAL` required for deduplicated reads
- Batch INSERTs preferred for performance

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**
- Run full sync against real Oura API with ClickHouse backend
- Verify data in ClickHouse matches SQLite output for same sync period
- Test switching between backends (SQLite → ClickHouse config change)

**Infrastructure:**
- ClickHouse server/cluster setup for production use
- Backup strategy for ClickHouse data
- Monitoring/alerting for ClickHouse connectivity
