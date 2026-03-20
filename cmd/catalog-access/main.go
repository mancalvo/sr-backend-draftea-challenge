package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"

	catalogaccessmigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/catalogaccess"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/lifecycle"
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

	rmqRuntime, err := rabbitmq.OpenRuntime(rmqConn, logger, rabbitmq.DefaultRetryConfig())
	if err != nil {
		logger.Error("failed to initialize RabbitMQ runtime", "error", err)
		os.Exit(1)
	}
	defer rmqRuntime.Close()

	// Handlers
	catalogService := catalogservice.New(repo)
	httpHandler := catalogapi.NewHandler(catalogService, logger)
	amqpHandler := catalogconsumer.NewHandler(catalogService, rmqRuntime.Publisher, logger)

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

	if err := lifecycle.Run(lifecycle.Config{
		ServiceName: "catalog-access",
		Port:        port,
		Logger:      logger,
		Server:      srv,
	}, lifecycle.Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			return rmqRuntime.Consumer.Consume(ctx, messaging.QueueCatalogAccessCommands, amqpHandler.Handle)
		},
	}); err != nil {
		logger.Error("service lifecycle failed", "error", err)
		os.Exit(1)
	}
}
