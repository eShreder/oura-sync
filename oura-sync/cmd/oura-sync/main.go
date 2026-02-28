package main

import (
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "oura.db", "path to SQLite database file")
	days := flag.Int("days", 90, "number of days to sync on first run")
	flag.Parse()

	token := os.Getenv("OURA_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: OURA_TOKEN environment variable is required")
		os.Exit(1)
	}

	fmt.Printf("db=%s days=%d token=%s...\n", *dbPath, *days, token[:min(4, len(token))])
}
