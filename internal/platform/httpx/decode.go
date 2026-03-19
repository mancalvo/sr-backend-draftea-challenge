package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Decode reads the request body as JSON into dst. It enforces that the body
// contains exactly one JSON object and disallows unknown fields.
func Decode(r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
