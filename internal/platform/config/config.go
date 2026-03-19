// Package config provides environment-based configuration loading for all services.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// GetEnv returns the value of an environment variable or a default value.
func GetEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// MustGetEnv returns the value of an environment variable or panics.
func MustGetEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return v
}

// GetEnvInt returns the integer value of an environment variable or a default.
func GetEnvInt(key string, fallback int) int {
	s := GetEnv(key, "")
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// GetEnvDuration returns the duration value of an environment variable or a default.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	s := GetEnv(key, "")
	if s == "" {
		return fallback
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return v
}

// Postgres holds PostgreSQL connection configuration.
type Postgres struct {
	Host     string
	Port     int
	User     string
	Password string
	DB       string
}

// DSN returns a PostgreSQL connection string.
func (p Postgres) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		p.User, p.Password, p.Host, p.Port, p.DB,
	)
}

// LoadPostgres loads PostgreSQL configuration from environment variables.
func LoadPostgres() Postgres {
	return Postgres{
		Host:     GetEnv("POSTGRES_HOST", "localhost"),
		Port:     GetEnvInt("POSTGRES_PORT", 5432),
		User:     GetEnv("POSTGRES_USER", "draftea"),
		Password: GetEnv("POSTGRES_PASSWORD", "draftea"),
		DB:       GetEnv("POSTGRES_DB", "draftea"),
	}
}

// RabbitMQ holds RabbitMQ connection configuration.
type RabbitMQ struct {
	Host     string
	Port     int
	User     string
	Password string
}

// URL returns an AMQP connection string.
func (r RabbitMQ) URL() string {
	return fmt.Sprintf(
		"amqp://%s:%s@%s:%d/",
		r.User, r.Password, r.Host, r.Port,
	)
}

// LoadRabbitMQ loads RabbitMQ configuration from environment variables.
func LoadRabbitMQ() RabbitMQ {
	return RabbitMQ{
		Host:     GetEnv("RABBITMQ_HOST", "localhost"),
		Port:     GetEnvInt("RABBITMQ_PORT", 5672),
		User:     GetEnv("RABBITMQ_USER", "guest"),
		Password: GetEnv("RABBITMQ_PASSWORD", "guest"),
	}
}
