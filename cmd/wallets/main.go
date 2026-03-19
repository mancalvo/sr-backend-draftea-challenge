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

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/config"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets"
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

	// Repository
	repo := wallets.NewPostgresRepository(db)

	// RabbitMQ connection
	rmqCfg := config.LoadRabbitMQ()
	rmqConn, err := rabbitmq.Dial(rmqCfg.URL(), logger)
	if err != nil {
		logger.Error("failed to connect to RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer rmqConn.Close()
	logger.Info("RabbitMQ connected")

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
	httpHandler := wallets.NewHandler(repo, logger)
	amqpHandler := wallets.NewConsumerHandler(repo, publisher, logger)

	// HTTP server
	router := wallets.NewRouter(httpHandler, logger)
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
		if err := consumer.Consume(ctx, messaging.QueueWalletsCommands, func(msgCtx context.Context, env messaging.Envelope) error {
			switch env.Type {
			case messaging.RoutingKeyWalletDebitRequested:
				return amqpHandler.HandleWalletDebitRequested(msgCtx, env)
			case messaging.RoutingKeyWalletCreditRequested:
				return amqpHandler.HandleWalletCreditRequested(msgCtx, env)
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
		logger.Info("wallets HTTP server starting", "port", port)
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

	logger.Info("wallets stopped")
}
