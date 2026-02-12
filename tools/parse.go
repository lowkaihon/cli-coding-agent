package tools

import (
	"encoding/json"
	"fmt"
)

// parseInput unmarshals JSON tool input into a typed struct.
func parseInput[T any](input json.RawMessage) (T, error) {
	var params T
	if err := json.Unmarshal(input, &params); err != nil {
		return params, fmt.Errorf("invalid input: %w", err)
	}
	return params, nil
}
