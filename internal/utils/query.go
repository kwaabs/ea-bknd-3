package utils

import "strings"

// ParseQueryList handles both repeated and comma-separated query params.
// Example:
//
//	?meterNumber=123,456   → ["123","456"]
//	?meterNumber=123&meterNumber=456  → ["123","456"]
func ParseQueryList(q map[string][]string, key string) []string {
	values := q[key]

	if len(values) == 0 {
		return nil
	}

	// If single value contains commas, split it
	if len(values) == 1 && strings.Contains(values[0], ",") {
		parts := strings.Split(values[0], ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}

	// Otherwise return the values as-is
	cleaned := make([]string, len(values))
	for i, v := range values {
		cleaned[i] = strings.TrimSpace(v)
	}
	return cleaned
}
