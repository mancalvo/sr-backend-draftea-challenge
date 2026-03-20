package database

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestIsUniqueViolation(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if IsUniqueViolation(nil) {
			t.Fatal("expected false for nil error")
		}
	})

	t.Run("pq error", func(t *testing.T) {
		err := &pq.Error{Code: "23505"}
		if !IsUniqueViolation(err) {
			t.Fatal("expected true for pq unique violation")
		}
	})

	t.Run("wrapped pq error", func(t *testing.T) {
		err := errors.Join(errors.New("context"), &pq.Error{Code: "23505"})
		if !IsUniqueViolation(err) {
			t.Fatal("expected true for wrapped pq unique violation")
		}
	})

	t.Run("message fallback", func(t *testing.T) {
		err := errors.New(`pq: duplicate key value violates unique constraint "users_pkey"`)
		if !IsUniqueViolation(err) {
			t.Fatal("expected true for duplicate-key message")
		}
	})

	t.Run("other error", func(t *testing.T) {
		err := errors.New("connection reset by peer")
		if IsUniqueViolation(err) {
			t.Fatal("expected false for unrelated error")
		}
	})
}
