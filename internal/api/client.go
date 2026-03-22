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

// NotFoundError is returned when the API responds with HTTP 404.
// This typically means the endpoint is not available for the user's
// account, subscription, or ring model.
type NotFoundError struct {
	Path string
	Body string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("API error: HTTP 404: %s (path: %s)", e.Body, e.Path)
}

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

// SetMaxRetries sets the maximum number of retries for transient errors.
func (c *Client) SetMaxRetries(n int) {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, &NotFoundError{Path: path, Body: string(body)}
		}
		return nil, fmt.Errorf("API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// maxPages is the safety limit for pagination to prevent infinite loops.
const maxPages = 1000

// Fetch retrieves all records from an endpoint, handling pagination automatically.
// It returns a slice of raw JSON messages representing individual records.
func (c *Client) Fetch(ctx context.Context, path string, params url.Values) ([]json.RawMessage, error) {
	var allData []json.RawMessage

	// Clone params to avoid mutating the caller's map.
	p := url.Values{}
	if params != nil {
		for k, v := range params {
			p[k] = v
		}
	}

	for page := 0; page < maxPages; page++ {
		resp, err := c.Do(ctx, http.MethodGet, path, p)
		if err != nil {
			return nil, err
		}

		var pr PaginatedResponse
		err = json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&pr) // 10 MB per page
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		allData = append(allData, pr.Data...)

		if pr.NextToken == nil || *pr.NextToken == "" {
			return allData, nil
		}
		p.Set("next_token", *pr.NextToken)
	}

	return nil, fmt.Errorf("pagination limit (%d pages) exceeded for %s", maxPages, path)
}
