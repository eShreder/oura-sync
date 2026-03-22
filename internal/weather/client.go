package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dailyParams is the set of Open-Meteo daily weather variables to request.
const dailyParams = "temperature_2m_max,temperature_2m_min,temperature_2m_mean,apparent_temperature_max,apparent_temperature_min,relative_humidity_2m_mean,dewpoint_2m_mean,precipitation_sum,surface_pressure_mean,wind_speed_10m_max,cloud_cover_mean,sunshine_duration,uv_index_max,weather_code"

// DayRecord holds parsed daily weather data for one day.
type DayRecord struct {
	Day                    string  `json:"day"`
	TemperatureMax         *float64 `json:"temperature_max"`
	TemperatureMin         *float64 `json:"temperature_min"`
	TemperatureMean        *float64 `json:"temperature_mean"`
	ApparentTemperatureMax *float64 `json:"apparent_temperature_max"`
	ApparentTemperatureMin *float64 `json:"apparent_temperature_min"`
	HumidityMean           *float64 `json:"humidity_mean"`
	DewpointMean           *float64 `json:"dewpoint_mean"`
	PrecipitationSum       *float64 `json:"precipitation_sum"`
	PressureMean           *float64 `json:"pressure_mean"`
	WindSpeedMax           *float64 `json:"wind_speed_max"`
	CloudCoverMean         *float64 `json:"cloud_cover_mean"`
	SunshineDuration       *float64 `json:"sunshine_duration"`
	UVIndexMax             *float64 `json:"uv_index_max"`
	WeatherCode            *int     `json:"weather_code"`
	RawJSON                json.RawMessage `json:"raw_json"`
}

// apiResponse represents the Open-Meteo API response structure.
type apiResponse struct {
	Daily *dailyData `json:"daily"`
	Error *bool      `json:"error"`
	Reason string    `json:"reason"`
}

type dailyData struct {
	Time                   []string   `json:"time"`
	TemperatureMax         []*float64 `json:"temperature_2m_max"`
	TemperatureMin         []*float64 `json:"temperature_2m_min"`
	TemperatureMean        []*float64 `json:"temperature_2m_mean"`
	ApparentTemperatureMax []*float64 `json:"apparent_temperature_max"`
	ApparentTemperatureMin []*float64 `json:"apparent_temperature_min"`
	HumidityMean           []*float64 `json:"relative_humidity_2m_mean"`
	DewpointMean           []*float64 `json:"dewpoint_2m_mean"`
	PrecipitationSum       []*float64 `json:"precipitation_sum"`
	PressureMean           []*float64 `json:"surface_pressure_mean"`
	WindSpeedMax           []*float64 `json:"wind_speed_10m_max"`
	CloudCoverMean         []*float64 `json:"cloud_cover_mean"`
	SunshineDuration       []*float64 `json:"sunshine_duration"`
	UVIndexMax             []*float64 `json:"uv_index_max"`
	WeatherCode            []*float64 `json:"weather_code"`
}

// Client is an HTTP client for the Open-Meteo weather API.
type Client struct {
	httpClient *http.Client
	archiveURL string
	forecastURL string
	maxRetries int
}

// NewClient creates a new weather API client.
func NewClient() *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		archiveURL:  "https://archive-api.open-meteo.com",
		forecastURL: "https://api.open-meteo.com",
		maxRetries:  3,
	}
}

// NewClientWithURLs creates a client with custom base URLs (for testing).
func NewClientWithURLs(archiveURL, forecastURL string) *Client {
	c := NewClient()
	c.archiveURL = archiveURL
	c.forecastURL = forecastURL
	return c
}

// SetMaxRetries sets the maximum number of retries for transient errors.
func (c *Client) SetMaxRetries(n int) {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
}

// FetchDaily fetches daily weather data from the archive API for the given date range.
func (c *Client) FetchDaily(ctx context.Context, lat, lon float64, tz, startDate, endDate string) ([]DayRecord, error) {
	params := url.Values{}
	params.Set("latitude", fmt.Sprintf("%.4f", lat))
	params.Set("longitude", fmt.Sprintf("%.4f", lon))
	params.Set("timezone", tz)
	params.Set("start_date", startDate)
	params.Set("end_date", endDate)
	params.Set("daily", dailyParams)

	return c.fetch(ctx, c.archiveURL+"/v1/archive", params)
}

// FetchRecent fetches recent daily weather data from the forecast API using past_days.
func (c *Client) FetchRecent(ctx context.Context, lat, lon float64, tz string, pastDays int) ([]DayRecord, error) {
	params := url.Values{}
	params.Set("latitude", fmt.Sprintf("%.4f", lat))
	params.Set("longitude", fmt.Sprintf("%.4f", lon))
	params.Set("timezone", tz)
	params.Set("past_days", fmt.Sprintf("%d", pastDays))
	params.Set("forecast_days", "0")
	params.Set("daily", dailyParams)

	return c.fetch(ctx, c.forecastURL+"/v1/forecast", params)
}

func (c *Client) fetch(ctx context.Context, baseURL string, params url.Values) ([]DayRecord, error) {
	u := baseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	var resp *http.Response
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			if attempt == c.maxRetries {
				return nil, fmt.Errorf("request failed after %d retries: HTTP %d", c.maxRetries, resp.StatusCode)
			}
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return nil, fmt.Errorf("creating request for retry: %w", err)
			}
			continue
		}

		break
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if apiResp.Error != nil && *apiResp.Error {
		return nil, fmt.Errorf("weather API error: %s", apiResp.Reason)
	}

	if apiResp.Daily == nil {
		return nil, nil
	}

	return parseRecords(apiResp.Daily)
}

func parseRecords(d *dailyData) ([]DayRecord, error) {
	n := len(d.Time)
	if n == 0 {
		return nil, nil
	}

	var records []DayRecord
	for i := 0; i < n; i++ {
		if strings.TrimSpace(d.Time[i]) == "" {
			continue
		}

		rec := DayRecord{Day: d.Time[i]}

		if i < len(d.TemperatureMax) {
			rec.TemperatureMax = d.TemperatureMax[i]
		}
		if i < len(d.TemperatureMin) {
			rec.TemperatureMin = d.TemperatureMin[i]
		}
		if i < len(d.TemperatureMean) {
			rec.TemperatureMean = d.TemperatureMean[i]
		}
		if i < len(d.ApparentTemperatureMax) {
			rec.ApparentTemperatureMax = d.ApparentTemperatureMax[i]
		}
		if i < len(d.ApparentTemperatureMin) {
			rec.ApparentTemperatureMin = d.ApparentTemperatureMin[i]
		}
		if i < len(d.HumidityMean) {
			rec.HumidityMean = d.HumidityMean[i]
		}
		if i < len(d.DewpointMean) {
			rec.DewpointMean = d.DewpointMean[i]
		}
		if i < len(d.PrecipitationSum) {
			rec.PrecipitationSum = d.PrecipitationSum[i]
		}
		if i < len(d.PressureMean) {
			rec.PressureMean = d.PressureMean[i]
		}
		if i < len(d.WindSpeedMax) {
			rec.WindSpeedMax = d.WindSpeedMax[i]
		}
		if i < len(d.CloudCoverMean) {
			rec.CloudCoverMean = d.CloudCoverMean[i]
		}
		if i < len(d.SunshineDuration) {
			rec.SunshineDuration = d.SunshineDuration[i]
		}
		if i < len(d.UVIndexMax) {
			rec.UVIndexMax = d.UVIndexMax[i]
		}
		if i < len(d.WeatherCode) && d.WeatherCode[i] != nil {
			code := int(*d.WeatherCode[i])
			rec.WeatherCode = &code
		}

		// Build raw JSON from the individual day's values.
		rawMap := map[string]interface{}{
			"time": d.Time[i],
		}
		addIfPresent := func(key string, vals []*float64) {
			if i < len(vals) && vals[i] != nil {
				rawMap[key] = *vals[i]
			}
		}
		addIfPresent("temperature_2m_max", d.TemperatureMax)
		addIfPresent("temperature_2m_min", d.TemperatureMin)
		addIfPresent("temperature_2m_mean", d.TemperatureMean)
		addIfPresent("apparent_temperature_max", d.ApparentTemperatureMax)
		addIfPresent("apparent_temperature_min", d.ApparentTemperatureMin)
		addIfPresent("relative_humidity_2m_mean", d.HumidityMean)
		addIfPresent("dewpoint_2m_mean", d.DewpointMean)
		addIfPresent("precipitation_sum", d.PrecipitationSum)
		addIfPresent("surface_pressure_mean", d.PressureMean)
		addIfPresent("wind_speed_10m_max", d.WindSpeedMax)
		addIfPresent("cloud_cover_mean", d.CloudCoverMean)
		addIfPresent("sunshine_duration", d.SunshineDuration)
		addIfPresent("uv_index_max", d.UVIndexMax)
		addIfPresent("weather_code", d.WeatherCode)

		rawJSON, err := json.Marshal(rawMap)
		if err != nil {
			return nil, fmt.Errorf("marshaling raw JSON for day %s: %w", d.Time[i], err)
		}
		rec.RawJSON = rawJSON

		records = append(records, rec)
	}

	return records, nil
}
