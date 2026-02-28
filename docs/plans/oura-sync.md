# oura-sync: Go CLI для сбора данных Oura Ring API v2 в SQLite

## Overview
- CLI утилита на Go для инкрементальной синхронизации всех данных из Oura Ring API v2 в локальную SQLite базу
- Запускается через cron/systemd timer; при каждом запуске подтягивает данные с момента последней синхронизации
- Первый запуск — полная загрузка за указанный период (по умолчанию 90 дней)
- Минимум зависимостей: stdlib + modernc.org/sqlite (pure Go, без CGO)

## Context
- **Oura API v2 base URL**: `https://api.ouraring.com`
- **Auth**: Bearer token через env var `OURA_TOKEN`
- **18 эндпоинтов** (все под `/v2/usercollection/`):
  - `personal_info` — единственный без пагинации, возвращает один объект
  - Date-based (параметры `start_date`, `end_date`): `daily_activity`, `daily_readiness`, `daily_sleep`, `daily_spo2`, `daily_stress`, `daily_cardiovascular_age`, `daily_resilience`, `sleep`, `sleep_time`, `rest_mode_period`, `ring_configuration`, `tag`, `enhanced_tag`, `workout`, `session`, `vo2_max`
  - Datetime-based (параметры `start_datetime`, `end_datetime`): `heartrate`
- **Пагинация**: `next_token` в ответе — передаётся как query param для следующей страницы
- **Rate limits**: Oura API имеет rate limiting, нужен retry с backoff

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change

## Project Structure
```
oura-sync/
├── cmd/
│   └── oura-sync/
│       └── main.go          # CLI entry point, flags, env vars
├── internal/
│   ├── api/
│   │   ├── client.go        # HTTP client, auth, rate limiting, pagination
│   │   ├── client_test.go
│   │   ├── endpoints.go     # Endpoint definitions and registry
│   │   └── endpoints_test.go
│   ├── model/
│   │   └── types.go         # JSON response structs for all endpoints
│   ├── store/
│   │   ├── sqlite.go        # SQLite storage: schema, upsert, sync state
│   │   └── sqlite_test.go
│   └── sync/
│       ├── syncer.go        # Orchestration: incremental sync logic
│       └── syncer_test.go
├── go.mod
├── go.sum
└── README.md
```

## Testing Strategy
- **Unit tests**: mock HTTP responses для API client, in-memory SQLite для store
- Каждый таск завершается написанием тестов и прогоном `go test ./...`

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Инициализация Go модуля и зависимости
- [x] создать директорию `oura-sync/` и выполнить `go mod init github.com/user/oura-sync`
- [x] добавить зависимость `modernc.org/sqlite` через `go get`
- [x] создать `cmd/oura-sync/main.go` с минимальным `func main()` (парсинг флагов: `--db`, `--days`, env `OURA_TOKEN`)
- [x] убедиться что `go build ./...` проходит без ошибок

### Task 2: HTTP клиент для Oura API
- [x] создать `internal/api/client.go`: struct `Client` с полями `httpClient`, `token`, `baseURL`
- [x] реализовать метод `Do(ctx, method, path, params) (*http.Response, error)` с Bearer auth и query params
- [x] добавить retry с exponential backoff при 429/5xx (максимум 3 попытки)
- [x] реализовать generic метод `Fetch(ctx, endpoint, params) ([]json.RawMessage, error)` с автоматической пагинацией через `next_token`
- [x] написать тесты для `Client.Do` (auth header, query params, error handling) с httptest.Server
- [x] написать тесты для `Fetch` (пагинация, retry при 429)
- [x] запустить `go test ./...` — должны проходить

### Task 3: Определение эндпоинтов и моделей данных
- [x] создать `internal/api/endpoints.go`: struct `Endpoint` с полями `Name`, `Path`, `UseDatetime` (bool для heartrate)
- [x] определить реестр всех 18 эндпоинтов как `var Endpoints = []Endpoint{...}`
- [x] создать `internal/model/types.go`: базовый struct для paginated response (`Data []json.RawMessage`, `NextToken *string`)
- [x] написать тесты для endpoint registry (все 18 эндпоинтов определены, пути корректные)
- [x] запустить `go test ./...` — должны проходить

### Task 4: SQLite хранилище — схема и базовые операции
- [x] создать `internal/store/sqlite.go`: struct `Store` с `*sql.DB`
- [x] реализовать `New(dbPath) (*Store, error)` — открытие БД и миграция схемы
- [x] схема: по одной таблице на эндпоинт (столбцы: `id TEXT PRIMARY KEY`, `day TEXT`, `data JSON`, `synced_at TIMESTAMP`), плюс таблица `sync_state` (`endpoint TEXT PRIMARY KEY`, `last_sync TEXT`)
- [x] для `personal_info` — таблица с одной строкой (`singleton`)
- [x] для `heartrate` — таблица с `timestamp TEXT PRIMARY KEY` вместо `id`
- [x] реализовать `UpsertRecords(endpoint, records []json.RawMessage) error` — вставка/обновление записей (извлекать `id` из JSON)
- [x] реализовать `GetLastSync(endpoint) (time.Time, error)` и `SetLastSync(endpoint, time.Time) error`
- [x] написать тесты: создание схемы, upsert (insert + update), sync state get/set
- [x] запустить `go test ./...` — должны проходить

### Task 5: Логика синхронизации
- [ ] создать `internal/sync/syncer.go`: struct `Syncer` с `api.Client` и `store.Store`
- [ ] реализовать `SyncEndpoint(ctx, endpoint, startDate, endDate) (int, error)` — загрузка данных с пагинацией и сохранение в SQLite
- [ ] реализовать `SyncAll(ctx, defaultDays int) error` — итерация по всем эндпоинтам, для каждого: определить start_date из sync_state (или now - defaultDays), загрузить, сохранить, обновить sync_state
- [ ] для `personal_info` — отдельная логика: GET без дат, upsert единственной записи
- [ ] для `heartrate` — использовать `start_datetime`/`end_datetime` вместо `start_date`/`end_date`
- [ ] добавить логирование через `log/slog`: начало/конец синхронизации эндпоинта, кол-во записей, ошибки
- [ ] написать тесты с mock API client и in-memory SQLite
- [ ] запустить `go test ./...` — должны проходить

### Task 6: Интеграция в main и CLI
- [ ] обновить `cmd/oura-sync/main.go`: парсинг флагов (`--db=oura.db`, `--days=90`), чтение `OURA_TOKEN`
- [ ] инициализация Store, Client, Syncer; вызов `SyncAll`
- [ ] вывод summary: сколько записей загружено по каждому эндпоинту
- [ ] graceful handling: context с timeout, корректное закрытие БД
- [ ] собрать `go build ./cmd/oura-sync/` — должно компилироваться
- [ ] написать integration test: httptest сервер + temp SQLite, прогнать полный цикл синхронизации
- [ ] запустить `go test ./...` — должны проходить

### Task 7: Верификация и финализация
- [ ] проверить все требования из Overview реализованы
- [ ] проверить обработку edge cases: пустые ответы API, невалидный JSON, отсутствие токена
- [ ] запустить полный `go test ./...`
- [ ] запустить `go vet ./...` — все замечания исправить
- [ ] проверить что `go build` создаёт рабочий бинарник

### Task 8: Документация
- [ ] написать README.md: описание, установка, использование, пример crontab
- [ ] добавить пример `.env` файла

## Technical Details

### Формат таблиц в SQLite
Каждый эндпоинт получает свою таблицу. Данные хранятся как JSON blob — это позволяет не определять все поля в схеме и быть устойчивым к изменениям API.

```sql
CREATE TABLE IF NOT EXISTS daily_activity (
    id TEXT PRIMARY KEY,
    day TEXT NOT NULL,
    data JSON NOT NULL,
    synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS heartrate (
    timestamp TEXT PRIMARY KEY,
    bpm INTEGER,
    source TEXT,
    data JSON NOT NULL,
    synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS personal_info (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    data JSON NOT NULL,
    synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sync_state (
    endpoint TEXT PRIMARY KEY,
    last_sync TEXT NOT NULL
);
```

### Извлечение ID из JSON
Большинство эндпоинтов возвращают объект с полем `id`. Для heartrate — используется `timestamp`. Для personal_info — singleton row. Для парсинга достаточно `json.Unmarshal` в `map[string]interface{}` для извлечения ключевого поля.

### Rate Limiting
Exponential backoff: 1s → 2s → 4s при 429 Too Many Requests. Максимум 3 retry.

## Post-Completion

**Ручная проверка:**
- Получить Personal Access Token на https://cloud.ouraring.com/personal-access-tokens
- Запустить `OURA_TOKEN=xxx ./oura-sync --db=oura.db` и проверить что данные загружаются
- Проверить содержимое SQLite через `sqlite3 oura.db ".tables"` и выборочные SELECT
- Настроить crontab: `0 */4 * * * OURA_TOKEN=xxx /path/to/oura-sync --db=/path/to/oura.db`
