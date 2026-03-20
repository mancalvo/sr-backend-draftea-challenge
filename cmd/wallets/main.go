package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"

	walletsmigrations "github.com/draftea/sr-backend-draftea-challenge/db/migrations/wallets"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/lifecycle"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	walletsapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/api"
	walletsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/consumer"
	walletsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
	walletsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/service"
)

func main() {
	logger := logging.New("wallets")
	port := config.GetEnvInt("WALLETS_PORT", 8083)

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
	if err := database.MigrateUp(pgCfg.DSN(), walletsmigrations.FS, "wallets_schema_migrations", logger); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Repository
	repo := walletsrepository.NewPostgresRepository(db)

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
	walletService := walletsservice.New(repo)
	httpHandler := walletsapi.NewHandler(walletService, logger)
	amqpHandler := walletsconsumer.NewHandler(walletService, rmqRuntime.Publisher, logger)

	// HTTP server with readiness checks
	router := walletsapi.NewRouter(httpHandler, logger,
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
		ServiceName: "wallets",
		Port:        port,
		Logger:      logger,
		Server:      srv,
	}, lifecycle.Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			return rmqRuntime.Consumer.Consume(ctx, messaging.QueueWalletsCommands, amqpHandler.Handle)
		},
	}); err != nil {
		logger.Error("service lifecycle failed", "error", err)
		os.Exit(1)
	}
}
