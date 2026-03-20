package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"

	paymentsmigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/payments"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/lifecycle"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	paymentsapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/api"
	paymentsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/consumer"
	paymentsprovider "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/provider"
	paymentsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/repository"
	paymentsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/service"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

func main() {
	logger := logging.New("payments")
	port := config.GetEnvInt("PAYMENTS_PORT", 8082)

	// Database connection
	pgCfg := config.LoadPostgres()
	db, err := sql.Open("postgres", pgCfg.DSN())
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	logger.Info("database connected")

	// Run service-owned migrations
	if err := database.MigrateUp(pgCfg.DSN(), paymentsmigrations.FS, "payments_schema_migrations", logger); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Repository
	repo := paymentsrepository.NewPostgresRepository(db)

	// RabbitMQ connection (with retry for Docker Compose startup ordering)
	rmqCfg := config.LoadRabbitMQ()
	rmqConn, err := rabbitmq.DialWithRetry(rmqCfg.URL(), logger, 10, time.Second)
	if err != nil {
		logger.Error("failed to connect to RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer rmqConn.Close()

	rmqRuntime, err := rabbitmq.OpenRuntime(rmqConn, logger, rabbitmq.DefaultRetryConfig())
	if err != nil {
		logger.Error("failed to initialize RabbitMQ runtime", "error", err)
		os.Exit(1)
	}
	defer rmqRuntime.Close()

	// Provider (mock implementation for local development)
	providerTimeout := config.GetEnvDuration("PROVIDER_CHARGE_TIMEOUT", 30*time.Second)
	provider := paymentsprovider.NewMockProvider(providerTimeout)

	// Handlers
	httpHandler := paymentsapi.NewHandler(paymentsservice.New(repo), logger)
	amqpHandler := paymentsconsumer.NewHandler(processdeposit.New(provider, rmqRuntime.Publisher, logger), logger)

	// HTTP server with readiness checks
	router := paymentsapi.NewRouter(httpHandler, logger,
		&health.DBPinger{Pinger: db},
		&health.RabbitMQChecker{Conn: rmqConn},
	)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if err := lifecycle.Run(lifecycle.Config{
		ServiceName: "payments",
		Port:        port,
		Logger:      logger,
		Server:      srv,
	}, lifecycle.Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			return rmqRuntime.Consumer.Consume(ctx, messaging.QueuePaymentsCommands, amqpHandler.Handle)
		},
	}); err != nil {
		logger.Error("service lifecycle failed", "error", err)
		os.Exit(1)
	}
}
