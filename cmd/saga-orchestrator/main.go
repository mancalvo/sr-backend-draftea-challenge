package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"

	sagamigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/saga"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/lifecycle"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	sagaapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/api"
	sagaclient "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	sagaconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/consumer"
	sagarepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	timeoutusecase "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/timeout"
)

func main() {
	logger := logging.New("saga-orchestrator")
	port := config.GetEnvInt("SAGA_ORCHESTRATOR_PORT", 8081)

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
	if err := database.MigrateUp(pgCfg.DSN(), sagamigrations.FS, "saga_orchestrator_schema_migrations", logger); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Repository
	repo := sagarepository.NewPostgresRepository(db)

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

	// Sync HTTP clients
	syncTimeout := config.GetEnvDuration("SYNC_HTTP_TIMEOUT", 2*time.Second)
	catalogBaseURL := config.GetEnv("CATALOG_ACCESS_URL", "http://localhost:8084")
	paymentsBaseURL := config.GetEnv("PAYMENTS_URL", "http://localhost:8082")

	catalogClient := sagaclient.NewHTTPCatalogClient(catalogBaseURL, syncTimeout)
	paymentsClient := sagaclient.NewHTTPPaymentsClient(paymentsBaseURL, syncTimeout)

	// Saga timeout configuration
	sagaTimeout := config.GetEnvDuration("SAGA_TIMEOUT", 30*time.Second)
	pollInterval := config.GetEnvDuration("TIMEOUT_POLL_INTERVAL", 5*time.Second)

	// Handlers
	httpHandler := sagaapi.NewHandler(repo, catalogClient, paymentsClient, rmqRuntime.Publisher, sagaTimeout, logger)
	amqpHandler := sagaconsumer.NewHandler(repo, paymentsClient, rmqRuntime.Publisher, logger)

	// Timeout poller
	timeoutPoller := timeoutusecase.NewTimeoutPoller(repo, paymentsClient, timeoutusecase.TimeoutConfig{
		PollInterval: pollInterval,
		SagaTimeout:  sagaTimeout,
	}, logger)

	// HTTP server with readiness checks
	router := sagaapi.NewRouter(httpHandler, logger,
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
		ServiceName: "saga-orchestrator",
		Port:        port,
		Logger:      logger,
		Server:      srv,
	}, lifecycle.Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			return rmqRuntime.Consumer.Consume(ctx, messaging.QueueSagaOutcomes, amqpHandler.Handle)
		},
	}, lifecycle.Task{
		Name: "timeout-poller",
		Run: func(ctx context.Context) error {
			timeoutPoller.Run(ctx)
			return nil
		},
	}); err != nil {
		logger.Error("service lifecycle failed", "error", err)
		os.Exit(1)
	}
}
