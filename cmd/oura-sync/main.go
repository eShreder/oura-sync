package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/store"
	"github.com/user/oura-sync/internal/sync"
	"github.com/user/oura-sync/internal/weather"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := config.Defaults()

	configPath := flag.String("config", "oura-sync.yaml", "path to YAML config file")
	dbPath := flag.String("db", defaults.DB, "path to SQLite database file")
	days := flag.Int("days", defaults.Days, "number of days to sync on first run")
	timeout := flag.Duration("timeout", defaults.Timeout, "overall sync timeout")
	skipWeather := flag.Bool("skip-weather", false, "skip weather data sync")
	flag.Parse()

	// Track which flags were explicitly set on the command line.
	flagSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })

	// Load config file. If --config was explicitly set and file is missing, error out.
	// If using the default path, silently skip when the file doesn't exist.
	cfg, err := config.Load(*configPath, flagSet["config"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Merge: CLI flags > env vars > config file > defaults.
	cfg = config.Merge(cfg, config.EnvVars{
		Token: os.Getenv("OURA_TOKEN"),
	}, config.FlagVals{
		DB:      *dbPath,
		Days:    *days,
		Timeout: *timeout,
	}, flagSet)

	if cfg.Token == "" {
		fmt.Fprintln(os.Stderr, "error: token is required (set OURA_TOKEN env var or token in config file)")
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Set up context with timeout and signal handling before opening the store,
	// so that startup (connection, migration) is also bounded by --timeout.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	// Open store — ClickHouse if configured, otherwise SQLite (default).
	var st store.Store
	if cfg.ClickHouse != nil {
		st, err = store.NewClickHouseStore(ctx, cfg.ClickHouse)
		if err != nil {
			logger.Error("failed to open clickhouse", "host", cfg.ClickHouse.Host, "error", err)
			return 1
		}
		logger.Info("using ClickHouse backend", "host", cfg.ClickHouse.Host, "port", cfg.ClickHouse.Port)
	} else {
		st, err = store.NewSQLiteStore(cfg.DB)
		if err != nil {
			logger.Error("failed to open database", "path", cfg.DB, "error", err)
			return 1
		}
		logger.Info("using SQLite backend", "path", cfg.DB)
	}
	defer st.Close()

	// Create API client.
	client := api.NewClient(cfg.Token, "https://api.ouraring.com")

	// Create syncer.
	syncer := sync.NewSyncer(client, st, logger)

	// Run sync.
	logger.Info("starting sync", "days", cfg.Days)
	results, err := syncer.SyncAll(ctx, cfg.Days)
	if err != nil {
		logger.Error("sync failed", "error", err)
		printSummary(results)
		return 1
	}

	printSummary(results)

	// Weather sync (non-blocking: errors are logged as warnings).
	if !*skipWeather {
		weatherCount := runWeatherSync(ctx, cfg, st, logger)
		if weatherCount > 0 {
			logger.Info("weather sync completed", "records", weatherCount)
		}
	}

	logger.Info("sync completed successfully")
	return 0
}

func runWeatherSync(ctx context.Context, cfg config.Config, st store.Store, logger *slog.Logger) int {
	if len(cfg.Locations) == 0 {
		// Clean up any previously-synced location data.
		if err := st.UpsertLocationPeriods(ctx, nil); err != nil {
			logger.Warn("failed to clean location periods", "error", err)
		}
		return 0
	}

	if err := config.ValidateLocations(cfg.Locations); err != nil {
		logger.Warn("invalid location config, skipping weather sync", "error", err)
		return 0
	}

	// Sort locations by start_date for correct end_date derivation.
	locs := make([]config.Location, len(cfg.Locations))
	copy(locs, cfg.Locations)
	sort.Slice(locs, func(i, j int) bool { return locs[i].StartDate < locs[j].StartDate })

	// Convert config locations to weather.LocationPeriod and derive end_dates.
	periods := make([]weather.LocationPeriod, len(locs))
	for i, loc := range locs {
		periods[i] = weather.LocationPeriod{
			City:      loc.City,
			Latitude:  loc.Latitude,
			Longitude: loc.Longitude,
			Timezone:  loc.Timezone,
			StartDate: loc.StartDate,
		}
		// Derive end_date from next location's start_date (minus 1 day).
		if i+1 < len(locs) {
			next := locs[i+1]
			t, err := time.Parse("2006-01-02", next.StartDate)
			if err == nil {
				periods[i].EndDate = t.AddDate(0, 0, -1).Format("2006-01-02")
			}
		}
	}

	if err := st.UpsertLocationPeriods(ctx, periods); err != nil {
		logger.Warn("failed to sync location periods", "error", err)
		return 0
	}

	if len(periods) == 0 {
		return 0
	}

	client := weather.NewClient()
	syncer := weather.NewSyncer(client, st, logger)

	count, err := syncer.SyncAll(ctx)
	if err != nil {
		logger.Warn("weather sync failed", "error", err)
	}
	return count
}

func printSummary(results map[string]int) {
	if len(results) == 0 {
		return
	}

	// Sort endpoint names for consistent output.
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)

	total := 0
	fmt.Println("\n--- Sync Summary ---")
	for _, name := range names {
		count := results[name]
		total += count
		fmt.Printf("  %-30s %d records\n", name, count)
	}
	fmt.Printf("  %-30s %d records\n", "TOTAL", total)
}
