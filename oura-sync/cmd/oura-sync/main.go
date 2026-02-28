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
	"github.com/user/oura-sync/internal/store"
	"github.com/user/oura-sync/internal/sync"

	_ "modernc.org/sqlite"
)

func main() {
	os.Exit(run())
}

func run() int {
	dbPath := flag.String("db", "oura.db", "path to SQLite database file")
	days := flag.Int("days", 90, "number of days to sync on first run")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall sync timeout")
	flag.Parse()

	token := os.Getenv("OURA_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: OURA_TOKEN environment variable is required")
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Open store.
	st, err := store.New(*dbPath)
	if err != nil {
		logger.Error("failed to open database", "path", *dbPath, "error", err)
		return 1
	}
	defer st.Close()

	// Create API client.
	client := api.NewClient(token, "https://api.ouraring.com")

	// Create syncer.
	syncer := sync.NewSyncer(client, st, logger)

	// Set up context with timeout and signal handling.
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	// Run sync.
	logger.Info("starting sync", "db", *dbPath, "days", *days)
	results, err := syncer.SyncAll(ctx, *days)
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
