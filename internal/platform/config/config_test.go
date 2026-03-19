package config

import (
	"os"
	"testing"
	"time"
)

func TestGetEnv_ReturnsValue(t *testing.T) {
	os.Setenv("TEST_CONFIG_KEY", "hello")
	defer os.Unsetenv("TEST_CONFIG_KEY")

	got := GetEnv("TEST_CONFIG_KEY", "default")
	if got != "hello" {
		t.Errorf("GetEnv = %q, want %q", got, "hello")
	}
}

func TestGetEnv_ReturnsFallback(t *testing.T) {
	os.Unsetenv("TEST_CONFIG_MISSING")

	got := GetEnv("TEST_CONFIG_MISSING", "fallback")
	if got != "fallback" {
		t.Errorf("GetEnv = %q, want %q", got, "fallback")
	}
}

func TestMustGetEnv_Panics(t *testing.T) {
	os.Unsetenv("TEST_CONFIG_MUST_PANIC")

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGetEnv did not panic for missing key")
		}
	}()
	MustGetEnv("TEST_CONFIG_MUST_PANIC")
}

func TestMustGetEnv_ReturnsValue(t *testing.T) {
	os.Setenv("TEST_CONFIG_MUST_OK", "value")
	defer os.Unsetenv("TEST_CONFIG_MUST_OK")

	got := MustGetEnv("TEST_CONFIG_MUST_OK")
	if got != "value" {
		t.Errorf("MustGetEnv = %q, want %q", got, "value")
	}
}

func TestGetEnvInt_ReturnsValue(t *testing.T) {
	os.Setenv("TEST_INT_KEY", "42")
	defer os.Unsetenv("TEST_INT_KEY")

	got := GetEnvInt("TEST_INT_KEY", 0)
	if got != 42 {
		t.Errorf("GetEnvInt = %d, want %d", got, 42)
	}
}

func TestGetEnvInt_ReturnsFallbackOnInvalid(t *testing.T) {
	os.Setenv("TEST_INT_INVALID", "not-a-number")
	defer os.Unsetenv("TEST_INT_INVALID")

	got := GetEnvInt("TEST_INT_INVALID", 99)
	if got != 99 {
		t.Errorf("GetEnvInt = %d, want %d", got, 99)
	}
}

func TestGetEnvInt_ReturnsFallbackOnMissing(t *testing.T) {
	os.Unsetenv("TEST_INT_MISSING")

	got := GetEnvInt("TEST_INT_MISSING", 10)
	if got != 10 {
		t.Errorf("GetEnvInt = %d, want %d", got, 10)
	}
}

func TestGetEnvDuration_ReturnsValue(t *testing.T) {
	os.Setenv("TEST_DUR_KEY", "5s")
	defer os.Unsetenv("TEST_DUR_KEY")

	got := GetEnvDuration("TEST_DUR_KEY", time.Second)
	if got != 5*time.Second {
		t.Errorf("GetEnvDuration = %v, want %v", got, 5*time.Second)
	}
}

func TestGetEnvDuration_ReturnsFallbackOnInvalid(t *testing.T) {
	os.Setenv("TEST_DUR_INVALID", "bad")
	defer os.Unsetenv("TEST_DUR_INVALID")

	got := GetEnvDuration("TEST_DUR_INVALID", 2*time.Second)
	if got != 2*time.Second {
		t.Errorf("GetEnvDuration = %v, want %v", got, 2*time.Second)
	}
}

func TestPostgres_DSN(t *testing.T) {
	p := Postgres{
		Host:     "localhost",
		Port:     5432,
		User:     "user",
		Password: "pass",
		DB:       "mydb",
	}
	want := "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
	got := p.DSN()
	if got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
}

func TestRabbitMQ_URL(t *testing.T) {
	r := RabbitMQ{
		Host:     "localhost",
		Port:     5672,
		User:     "guest",
		Password: "guest",
	}
	want := "amqp://guest:guest@localhost:5672/"
	got := r.URL()
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}
