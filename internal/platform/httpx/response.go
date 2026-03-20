// Package httpx provides shared HTTP helpers: JSON response writers,
// error envelope formatting, request decoding, and common middleware.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Response is the standard JSON envelope for all API responses.
type Response struct {
	Success bool       `json:"success"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`
}

// ErrorBody is the error detail inside the standard JSON envelope.
type ErrorBody struct {
	Message string       `json:"message"`
	Code    string       `json:"code,omitempty"`
	Fields  []FieldError `json:"fields,omitempty"`
}

// FieldError describes a single field-level validation failure.
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, data any) {
	body, err := MarshalResponse(status, data)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	WriteJSONBytes(w, status, body)
}

// Error writes a JSON error response with the given status code.
func Error(w http.ResponseWriter, status int, message string) {
	body, err := MarshalErrorResponse(message, "", nil)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	WriteJSONBytes(w, status, body)
}

// ErrorWithCode writes a JSON error response with an explicit error code.
func ErrorWithCode(w http.ResponseWriter, status int, message, code string) {
	body, err := MarshalErrorResponse(message, code, nil)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	WriteJSONBytes(w, status, body)
}

// ValidationError writes a 422 Unprocessable Entity response with field-level
// validation details. The fields slice must contain at least one entry.
func ValidationError(w http.ResponseWriter, fields []FieldError) {
	body, err := MarshalErrorResponse("Request validation failed", "VALIDATION_ERROR", fields)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	WriteJSONBytes(w, http.StatusUnprocessableEntity, body)
}

// WriteDecodeError writes a request-decode failure with its associated status.
func WriteDecodeError(w http.ResponseWriter, err error) {
	var requestErr *RequestError
	if err == nil {
		return
	}
	if !AsRequestError(err, &requestErr) {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	Error(w, requestErr.StatusCode(), requestErr.Error())
}

// MarshalResponse marshals the standard success envelope for the given status.
func MarshalResponse(status int, data any) ([]byte, error) {
	return marshalEnvelope(Response{
		Success: status >= 200 && status < 300,
		Data:    data,
	})
}

// MarshalErrorResponse marshals the standard error envelope for the given values.
func MarshalErrorResponse(message, code string, fields []FieldError) ([]byte, error) {
	return marshalEnvelope(Response{
		Success: false,
		Error: &ErrorBody{
			Message: message,
			Code:    code,
			Fields:  fields,
		},
	})
}

// WriteJSONBytes writes a pre-marshaled JSON response body using the standard content type.
func WriteJSONBytes(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if len(body) == 0 {
		return
	}
	_, _ = w.Write(body)
}

// AsRequestError unwraps err into a RequestError when present.
func AsRequestError(err error, target **RequestError) bool {
	return errors.As(err, target)
}

func marshalEnvelope(resp Response) ([]byte, error) {
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return body, nil
}
