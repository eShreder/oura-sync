package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/user/oura-sync/internal/api"
	"github.com/user/oura-sync/internal/store"
)

// Syncer orchestrates incremental sync of all Oura API endpoints.
type Syncer struct {
	client *api.Client
	store  store.Store
	logger *slog.Logger
}

// NewSyncer creates a new Syncer.
func NewSyncer(client *api.Client, store store.Store, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		client: client,
		store:  store,
		logger: logger,
	}
}

// SyncEndpoint fetches data from one endpoint and saves it to the store.
// It returns the number of records upserted.
func (s *Syncer) SyncEndpoint(ctx context.Context, ep api.Endpoint, startDate, endDate string) (int, error) {
	s.logger.Info("syncing endpoint", "endpoint", ep.Name)

	if ep.IsSingleton {
		return s.syncSingleton(ctx, ep)
	}

	// Split into chunks if the endpoint has a max range limit.
	chunks := dateChunks(startDate, endDate, ep.MaxRangeDays)

	totalCount := 0
	for _, chunk := range chunks {
		if ctx.Err() != nil {
			return totalCount, ctx.Err()
		}

		params := url.Values{}
		if ep.UseDatetime {
			params.Set("start_datetime", chunk[0]+"T00:00:00+00:00")
			params.Set("end_datetime", chunk[1]+"T23:59:59+00:00")
		} else {
			params.Set("start_date", chunk[0])
			params.Set("end_date", chunk[1])
		}

		records, err := s.client.Fetch(ctx, ep.Path, params)
		if err != nil {
			return totalCount, fmt.Errorf("fetching %s: %w", ep.Name, err)
		}

		if len(records) > 0 {
			if err := s.store.UpsertRecords(ctx, ep.Name, records); err != nil {
				return totalCount, fmt.Errorf("storing %s: %w", ep.Name, err)
			}
			totalCount += len(records)
		}
	}

	if totalCount == 0 {
		s.logger.Info("no records found", "endpoint", ep.Name)
	} else {
		s.logger.Info("synced endpoint", "endpoint", ep.Name, "records", totalCount)
	}
	return totalCount, nil
}

// dateChunks splits a date range into chunks of at most maxDays.
// If maxDays is 0, returns the full range as a single chunk.
// Each chunk is a [2]string{start, end} in YYYY-MM-DD format.
func dateChunks(startDate, endDate string, maxDays int) [][2]string {
	if maxDays <= 0 {
		return [][2]string{{startDate, endDate}}
	}

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return [][2]string{{startDate, endDate}}
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return [][2]string{{startDate, endDate}}
	}

	var chunks [][2]string
	for start.Before(end) || start.Equal(end) {
		chunkEnd := start.AddDate(0, 0, maxDays-1)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		chunks = append(chunks, [2]string{
			start.Format("2006-01-02"),
			chunkEnd.Format("2006-01-02"),
		})
		start = chunkEnd.AddDate(0, 0, 1)
	}
	return chunks
}

// syncSingleton handles the personal_info endpoint which returns a single object
// without pagination.
func (s *Syncer) syncSingleton(ctx context.Context, ep api.Endpoint) (int, error) {
	resp, err := s.client.Do(ctx, http.MethodGet, ep.Path, nil)
	if err != nil {
		return 0, fmt.Errorf("fetching %s: %w", ep.Name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return 0, fmt.Errorf("reading %s response: %w", ep.Name, err)
	}

	if !json.Valid(body) {
		return 0, fmt.Errorf("%s response is not valid JSON", ep.Name)
	}

	records := []json.RawMessage{json.RawMessage(body)}
	if err := s.store.UpsertRecords(ctx, ep.Name, records); err != nil {
		return 0, fmt.Errorf("storing %s: %w", ep.Name, err)
	}

	s.logger.Info("synced singleton endpoint", "endpoint", ep.Name)
	return 1, nil
}

// SyncAll syncs all endpoints. For each endpoint it determines the start date
// from the last sync state (or now minus defaultDays if never synced).
// It returns a map of endpoint name to number of records synced.
func (s *Syncer) SyncAll(ctx context.Context, defaultDays int) (map[string]int, error) {
	now := time.Now().UTC()
	endDate := now.Format("2006-01-02")
	results := make(map[string]int)

	for _, ep := range api.Endpoints {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		var startDate string

		if !ep.IsSingleton {
			lastSync, err := s.store.GetLastSync(ctx, ep.Name)
			if err != nil {
				return results, fmt.Errorf("getting last sync for %s: %w", ep.Name, err)
			}

			if lastSync.IsZero() {
				startDate = now.AddDate(0, 0, -defaultDays).Format("2006-01-02")
			} else {
				startDate = lastSync.Format("2006-01-02")
			}
		}

		count, err := s.SyncEndpoint(ctx, ep, startDate, endDate)
		if err != nil {
			var nfe *api.NotFoundError
			if errors.As(err, &nfe) {
				s.logger.Warn("endpoint not available, skipping", "endpoint", ep.Name)
				continue
			}
			return results, fmt.Errorf("syncing %s: %w", ep.Name, err)
		}

		results[ep.Name] = count

		if err := s.store.SetLastSync(ctx, ep.Name, now); err != nil {
			return results, fmt.Errorf("updating sync state for %s: %w", ep.Name, err)
		}
	}

	return results, nil
}
