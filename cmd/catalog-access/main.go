package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	catalogaccessmigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/catalogaccess"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	catalogapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/api"
	catalogconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/consumer"
	catalogrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	catalogservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
)

func main() {
	logger := logging.New("catalog-access")
	port := config.GetEnvInt("CATALOG_ACCESS_PORT", 8084)

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
	if err := database.MigrateUp(pgCfg.DSN(), catalogaccessmigrations.FS, "catalog_access_schema_migrations", logger); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Repository
	repo := catalogrepository.NewPostgresRepository(db)

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

	// Handlers
	catalogService := catalogservice.New(repo)
	httpHandler := catalogapi.NewHandler(catalogService, logger)
	amqpHandler := catalogconsumer.NewHandler(catalogService, publisher, logger)

	// HTTP server with readiness checks
	router := catalogapi.NewRouter(httpHandler, logger,
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
		if err := consumer.Consume(ctx, messaging.QueueCatalogAccessCommands, amqpHandler.Handle); err != nil && ctx.Err() == nil {
			logger.Error("consumer stopped unexpectedly", "error", err)
			cancel()
		}
	}()

	// Start HTTP server in background
	go func() {
		logger.Info("catalog-access HTTP server starting", "port", port)
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

	logger.Info("catalog-access stopped")
}
