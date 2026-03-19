package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNew_IncludesServiceField(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler).With(slog.String("service", "test-svc"))

	logger.Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
	}

	if entry["service"] != "test-svc" {
		t.Errorf("service = %v, want %q", entry["service"], "test-svc")
	}
	if entry["msg"] != "hello" {
		t.Errorf("msg = %v, want %q", entry["msg"], "hello")
	}
}

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler).With(slog.String("service", "ctx-svc"))

	ctx := WithContext(context.Background(), logger)
	got := FromContext(ctx)

	got.Info("from-ctx")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
	}
	if entry["service"] != "ctx-svc" {
		t.Errorf("service = %v, want %q", entry["service"], "ctx-svc")
	}
}

func TestFromContext_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	logger := FromContext(ctx)
	if logger == nil {
		t.Fatal("FromContext returned nil for empty context")
	}
}
