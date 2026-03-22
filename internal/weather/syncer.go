package weather

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Store defines the store methods needed by the weather syncer.
type Store interface {
	UpsertLocationPeriods(periods []LocationPeriod) error
	GetLocationPeriods() ([]LocationPeriod, error)
	UpsertWeatherRecords(locationID int64, records []DayRecord) error
	GetLastWeatherDay(locationID int64) (string, error)
}

// LocationPeriod represents a period the user was at a specific location.
type LocationPeriod struct {
	ID        int64
	City      string
	Latitude  float64
	Longitude float64
	Timezone  string
	StartDate string
	EndDate   string // empty = ongoing
}

// Syncer orchestrates weather data sync for all location periods.
type Syncer struct {
	client *Client
	store  Store
	logger *slog.Logger
}

// NewSyncer creates a new weather Syncer.
func NewSyncer(client *Client, store Store, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		client: client,
		store:  store,
		logger: logger,
	}
}

// archiveLagDays is the number of days the archive API lags behind.
// Recent days are fetched from the forecast API instead.
const archiveLagDays = 5

// SyncAll syncs weather data for all location periods.
// Returns the total number of records upserted.
func (s *Syncer) SyncAll(ctx context.Context) (int, error) {
	periods, err := s.store.GetLocationPeriods()
	if err != nil {
		return 0, fmt.Errorf("getting location periods: %w", err)
	}

	total := 0

	for _, loc := range periods {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}

		// Compute today and archive cutoff in the location's timezone so day
		// boundaries are correct for locations outside the host timezone.
		today, archiveCutoff := todayAndCutoff(loc.Timezone)

		count, err := s.syncLocation(ctx, loc, today, archiveCutoff)
		if err != nil {
			s.logger.Warn("weather sync failed for location", "city", loc.City, "error", err)
			continue
		}
		total += count
		s.logger.Info("weather synced", "city", loc.City, "records", count)
	}

	// Check for context cancellation that may have occurred during the last location.
	if ctx.Err() != nil {
		return total, ctx.Err()
	}

	return total, nil
}

// todayAndCutoff returns today's date and the archive cutoff date in the given timezone.
// Falls back to UTC if the timezone cannot be loaded.
func todayAndCutoff(tz string) (today, archiveCutoff string) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today = now.Format("2006-01-02")
	archiveCutoff = now.AddDate(0, 0, -archiveLagDays).Format("2006-01-02")
	return
}

func (s *Syncer) syncLocation(ctx context.Context, loc LocationPeriod, today, archiveCutoff string) (int, error) {
	lastDay, err := s.store.GetLastWeatherDay(loc.ID)
	if err != nil {
		return 0, fmt.Errorf("getting last weather day: %w", err)
	}

	startDate := loc.StartDate
	if lastDay != "" && lastDay > startDate {
		// Start from the day after the last synced day, but never skip past
		// the archive cutoff. Days in the forecast window need to be
		// re-fetched once archive data becomes available.
		t, err := time.Parse("2006-01-02", lastDay)
		if err != nil {
			return 0, fmt.Errorf("parsing last weather day: %w", err)
		}
		nextDay := t.AddDate(0, 0, 1).Format("2006-01-02")
		if nextDay > archiveCutoff {
			startDate = archiveCutoff
		} else {
			startDate = nextDay
		}
		// Never fetch days before the location period actually started.
		if startDate < loc.StartDate {
			startDate = loc.StartDate
		}
	}

	endDate := today
	if loc.EndDate != "" && loc.EndDate < endDate {
		endDate = loc.EndDate
	}

	if startDate > endDate {
		return 0, nil
	}

	total := 0

	// Fetch from archive API (up to archiveCutoff).
	if startDate <= archiveCutoff {
		archiveEnd := archiveCutoff
		if archiveEnd > endDate {
			archiveEnd = endDate
		}

		records, err := s.client.FetchDaily(ctx, loc.Latitude, loc.Longitude, loc.Timezone, startDate, archiveEnd)
		if err != nil {
			return total, fmt.Errorf("fetching archive weather: %w", err)
		}

		if len(records) > 0 {
			if err := s.store.UpsertWeatherRecords(loc.ID, records); err != nil {
				return total, fmt.Errorf("storing archive weather: %w", err)
			}
			total += len(records)
		}
	}

	// Fetch recent days from forecast API (the archive lag gap).
	recentStart := archiveCutoff
	if recentStart < startDate {
		recentStart = startDate
	}
	// Move one day forward if we already covered archiveCutoff in the archive fetch.
	if total > 0 && recentStart == archiveCutoff {
		t, _ := time.Parse("2006-01-02", recentStart)
		recentStart = t.AddDate(0, 0, 1).Format("2006-01-02")
	}

	if recentStart <= endDate {
		pastDays := archiveLagDays + 1
		records, err := s.client.FetchRecent(ctx, loc.Latitude, loc.Longitude, loc.Timezone, pastDays)
		if err != nil {
			return total, fmt.Errorf("fetching recent weather: %w", err)
		}

		// Filter to only days in our range.
		var filtered []DayRecord
		for _, rec := range records {
			if rec.Day >= recentStart && rec.Day <= endDate {
				filtered = append(filtered, rec)
			}
		}

		if len(filtered) > 0 {
			if err := s.store.UpsertWeatherRecords(loc.ID, filtered); err != nil {
				return total, fmt.Errorf("storing recent weather: %w", err)
			}
			total += len(filtered)
		}
	}

	return total, nil
}
