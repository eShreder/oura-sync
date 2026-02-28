package model

import "encoding/json"

// PaginatedResponse is the standard envelope for paginated Oura API v2 responses.
// Most collection endpoints return data in this format.
type PaginatedResponse struct {
	Data      []json.RawMessage `json:"data"`
	NextToken *string           `json:"next_token"`
}
