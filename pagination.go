package dyngeo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// paginationState is the internal representation serialized into the opaque NextToken.
type paginationState struct {
	Offset int `json:"o"`
}

func encodePaginationState(state paginationState) (string, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("failed to encode pagination state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func decodePaginationState(token string) (paginationState, error) {
	b, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return paginationState{}, fmt.Errorf("invalid pagination token: %w", err)
	}
	var state paginationState
	if err := json.Unmarshal(b, &state); err != nil {
		return paginationState{}, fmt.Errorf("invalid pagination token: %w", err)
	}
	return state, nil
}

// paginateResults applies offset-based pagination to a slice of items.
// If limit is 0, all items are returned with no NextToken.
func paginateResults(items []map[string]types.AttributeValue, limit int, nextToken string) ([]map[string]types.AttributeValue, string, error) {
	offset := 0
	if nextToken != "" {
		state, err := decodePaginationState(nextToken)
		if err != nil {
			return nil, "", err
		}
		offset = state.Offset
	}

	// Apply offset
	if offset >= len(items) {
		return nil, "", nil
	}
	items = items[offset:]

	// Apply limit
	var token string
	if limit > 0 && len(items) > limit {
		items = items[:limit]
		newState := paginationState{Offset: offset + limit}
		var err error
		token, err = encodePaginationState(newState)
		if err != nil {
			return nil, "", err
		}
	}

	return items, token, nil
}
