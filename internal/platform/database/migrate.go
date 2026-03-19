// Package database provides shared database helpers including embedded migration support.
package database

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// MigrateUp applies all pending migrations from the given embedded filesystem.
// Each service provides its own embed.FS and a unique migrationsTable name so
// that multiple services sharing the same database track their schema versions
// independently.
func MigrateUp(dbURL string, migrations fs.FS, migrationsTable string, logger *slog.Logger) error {
	connURL, err := appendMigrateParams(dbURL, migrationsTable)
	if err != nil {
		return fmt.Errorf("migrate: build url: %w", err)
	}

	sourceDriver, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("migrate: create source driver: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", sourceDriver, connURL)
	if err != nil {
		return fmt.Errorf("migrate: create instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate: up: %w", err)
	}

	logger.Info("migrations completed", "table", migrationsTable)
	return nil
}

// appendMigrateParams adds x-migrations-table and x-multi-statement query
// parameters to a Postgres connection URL.
func appendMigrateParams(dbURL, migrationsTable string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", fmt.Errorf("parse database url: %w", err)
	}

	q := u.Query()
	q.Set("x-migrations-table", migrationsTable)
	q.Set("x-multi-statement", "true")

	// Ensure sslmode is present (required by lib/pq).
	if !q.Has("sslmode") && !strings.Contains(dbURL, "sslmode") {
		q.Set("sslmode", "disable")
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}
