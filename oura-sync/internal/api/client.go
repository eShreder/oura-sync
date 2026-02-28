package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

// PaginatedResponse represents the standard Oura API paginated response.
type PaginatedResponse struct {
	Data      []json.RawMessage `json:"data"`
	NextToken *string           `json:"next_token"`
}

// Client is an HTTP client for the Oura Ring API v2.
type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
	maxRetries int
}

// NewClient creates a new Oura API client.
func NewClient(token, baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
		baseURL:    baseURL,
		maxRetries: 3,
	}
}

// Do executes an HTTP request with Bearer auth and query parameters.
// It returns the raw http.Response. The caller is responsible for closing the body.
func (c *Client) Do(ctx context.Context, method, path string, params url.Values) (*http.Response, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp *http.Response
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
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
			// Re-create the request because the body may have been consumed
			req, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("creating request for retry: %w", err)
			}
			req.Header.Set("Authorization", "Bearer "+c.token)
			continue
		}

		break
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// Fetch retrieves all records from an endpoint, handling pagination automatically.
// It returns a slice of raw JSON messages representing individual records.
func (c *Client) Fetch(ctx context.Context, path string, params url.Values) ([]json.RawMessage, error) {
	var allData []json.RawMessage

	if params == nil {
		params = url.Values{}
	}

	for {
		resp, err := c.Do(ctx, http.MethodGet, path, params)
		if err != nil {
			return allData, err
		}

		var page PaginatedResponse
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return allData, fmt.Errorf("decoding response: %w", err)
		}

		allData = append(allData, page.Data...)

		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		params.Set("next_token", *page.NextToken)
	}

	return allData, nil
}
