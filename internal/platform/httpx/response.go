// Package httpx provides shared HTTP helpers: JSON response writers,
// error envelope formatting, request decoding, and common middleware.
package httpx

import (
	"encoding/json"
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
	resp := Response{
		Success: status >= 200 && status < 300,
		Data:    data,
	}
	writeJSON(w, status, resp)
}

// Error writes a JSON error response with the given status code.
func Error(w http.ResponseWriter, status int, message string) {
	resp := Response{
		Success: false,
		Error: &ErrorBody{
			Message: message,
		},
	}
	writeJSON(w, status, resp)
}

// ErrorWithCode writes a JSON error response with an explicit error code.
func ErrorWithCode(w http.ResponseWriter, status int, message, code string) {
	resp := Response{
		Success: false,
		Error: &ErrorBody{
			Message: message,
			Code:    code,
		},
	}
	writeJSON(w, status, resp)
}

// ValidationError writes a 422 Unprocessable Entity response with field-level
// validation details. The fields slice must contain at least one entry.
func ValidationError(w http.ResponseWriter, fields []FieldError) {
	resp := Response{
		Success: false,
		Error: &ErrorBody{
			Message: "Request validation failed",
			Code:    "VALIDATION_ERROR",
			Fields:  fields,
		},
	}
	writeJSON(w, http.StatusUnprocessableEntity, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
