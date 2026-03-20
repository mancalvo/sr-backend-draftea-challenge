package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
)

const maxDecodeBodyBytes = 1 << 20

// RequestError represents a client-facing request parsing error.
type RequestError struct {
	status  int
	message string
}

func (e *RequestError) Error() string {
	return e.message
}

// StatusCode returns the HTTP status associated with the request error.
func (e *RequestError) StatusCode() int {
	return e.status
}

// Decode reads the request body as JSON into dst. It enforces that the body
// contains exactly one JSON object and disallows unknown fields.
func Decode(r *http.Request, dst any) error {
	if r.Body == nil {
		return &RequestError{status: http.StatusBadRequest, message: "empty request body"}
	}
	if err := validateJSONContentType(r.Header.Get("Content-Type")); err != nil {
		return err
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDecodeBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) == 0 {
		return &RequestError{status: http.StatusBadRequest, message: "empty request body"}
	}
	if len(body) > maxDecodeBodyBytes {
		return &RequestError{status: http.StatusRequestEntityTooLarge, message: "request body too large"}
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return &RequestError{status: http.StatusBadRequest, message: "invalid JSON: request body must contain a single JSON value"}
	}
	return nil
}

func validateJSONContentType(contentType string) error {
	if contentType == "" {
		return nil
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return &RequestError{status: http.StatusBadRequest, message: "invalid Content-Type header"}
	}
	if mediaType != "application/json" {
		return &RequestError{status: http.StatusUnsupportedMediaType, message: "Content-Type must be application/json"}
	}
	return nil
}
