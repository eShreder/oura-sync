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

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/config"
	"github.com/user/oura-sync/internal/store"
	"github.com/user/oura-sync/internal/sync"

	_ "modernc.org/sqlite"
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

	// Open store.
	st, err := store.New(cfg.DB)
	if err != nil {
		logger.Error("failed to open database", "path", cfg.DB, "error", err)
		return 1
	}
	defer st.Close()

	// Create API client.
	client := api.NewClient(cfg.Token, "https://api.ouraring.com")

	// Create syncer.
	syncer := sync.NewSyncer(client, st, logger)

	// Set up context with timeout and signal handling.
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

	// Run sync.
	logger.Info("starting sync", "db", cfg.DB, "days", cfg.Days)
	results, err := syncer.SyncAll(ctx, cfg.Days)
	if err != nil {
		logger.Error("sync failed", "error", err)
		printSummary(results)
		return 1
	}

	printSummary(results)
	logger.Info("sync completed successfully")
	return 0
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
