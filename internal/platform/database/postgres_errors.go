package database

import (
	"errors"
	"strings"

	"github.com/lib/pq"
)

const uniqueViolationCode = "23505"

// IsUniqueViolation reports whether err represents a PostgreSQL unique
// constraint violation.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == uniqueViolationCode
	}

	message := err.Error()
	return strings.Contains(message, uniqueViolationCode) ||
		strings.Contains(message, "unique_violation") ||
		strings.Contains(message, "duplicate key")
}
