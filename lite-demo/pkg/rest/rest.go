// Package rest is the JSON boundary: response writing and request binding.
package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

const maxBody = 1 << 20

// JSON writes the response; an encode error goes to the log, not into silence.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

// Bind decodes exactly one JSON object: bounded body, no unknown fields, no
// trailing data.
func Bind(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	if dec.More() {
		return errors.New("trailing data after JSON object")
	}
	return nil
}
