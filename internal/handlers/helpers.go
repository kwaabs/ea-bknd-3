package handlers

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response. Used by handlers still in this package
// (meter_metrics.go) — domains that have their own package use httpx.JSON.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if data == nil {
		return
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(data)
}
