package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	paymentsmigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/payments"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments"
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
	repo := payments.NewPostgresRepository(db)

	// RabbitMQ connection (with retry for Docker Compose startup ordering)
	rmqCfg := config.LoadRabbitMQ()
	rmqConn, err := rabbitmq.DialWithRetry(rmqCfg.URL(), logger, 10, time.Second)
	if err != nil {
		logger.Error("failed to connect to RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer rmqConn.Close()

	// Declare RabbitMQ topology (idempotent)
	topoCh, err := rmqConn.Channel()
	if err != nil {
		logger.Error("failed to open topology channel", "error", err)
		os.Exit(1)
	}
	if err := rabbitmq.DeclareTopology(topoCh, rabbitmq.DefaultTopology(), logger); err != nil {
		logger.Error("failed to declare topology", "error", err)
		os.Exit(1)
	}
	topoCh.Close()

	// Publisher channel
	pubCh, err := rmqConn.Channel()
	if err != nil {
		logger.Error("failed to open publisher channel", "error", err)
		os.Exit(1)
	}
	publisher := rabbitmq.NewPublisher(pubCh, logger)

	// Consumer channel
	consCh, err := rmqConn.Channel()
	if err != nil {
		logger.Error("failed to open consumer channel", "error", err)
		os.Exit(1)
	}
	consumer := rabbitmq.NewConsumer(consCh, logger, rabbitmq.DefaultRetryConfig())

	// Provider (mock implementation for local development)
	providerTimeout := config.GetEnvDuration("PROVIDER_CHARGE_TIMEOUT", 30*time.Second)
	provider := payments.NewMockProvider(100 * time.Millisecond)
	_ = providerTimeout // used by orchestrator for saga timeout, not directly by provider

	// Handlers
	httpHandler := payments.NewHandler(repo, logger)
	amqpHandler := payments.NewConsumerHandler(provider, publisher, logger)

	// HTTP server with readiness checks
	router := payments.NewRouter(httpHandler, logger,
		&health.DBPinger{Pinger: db},
		&health.RabbitMQChecker{Conn: rmqConn},
	)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start AMQP consumer in background
	go func() {
		if err := consumer.Consume(ctx, messaging.QueuePaymentsCommands, func(msgCtx context.Context, env messaging.Envelope) error {
			switch env.Type {
			case messaging.RoutingKeyDepositRequested:
				return amqpHandler.HandleDepositRequested(msgCtx, env)
			default:
				logger.Warn("unknown message type", slog.String("type", env.Type))
				return nil
			}
		}); err != nil && ctx.Err() == nil {
			logger.Error("consumer stopped unexpectedly", "error", err)
			cancel()
		}
	}()

	// Start HTTP server in background
	go func() {
		logger.Info("payments HTTP server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	logger.Info("payments stopped")
}
