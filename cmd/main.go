package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/niksmi-lab/booking-inventory-service/internal/config"
	"github.com/niksmi-lab/booking-inventory-service/internal/handlers"
	"github.com/niksmi-lab/booking-inventory-service/internal/service"
	"github.com/niksmi-lab/booking-inventory-service/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "booking_http_requests_total",
			Help: "Total number of handled HTTP requests.",
		},
		[]string{"path", "method", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "booking_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("service stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("failed to load .env file", slog.Any("error", err))
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("DATABASE_URL is invalid")
	}
	poolConfig.MaxConns = cfg.DBMaxConnections
	poolConfig.MinConns = cfg.DBMinConnections
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute

	startupCtx, startupCancel := context.WithTimeout(ctx, 15*time.Second)
	defer startupCancel()
	pool, err := pgxpool.NewWithConfig(startupCtx, poolConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if cfg.AutoMigrate {
		migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
		err = storage.ApplyMigrations(migrationCtx, pool)
		migrationCancel()
		if err != nil {
			return fmt.Errorf("apply database migrations: %w", err)
		}
	}

	repository := storage.NewPostgresRepo(pool, cfg.ReservationTTL, cfg.DBOperationTimeout)
	stockService := service.NewService(repository, logger)
	stockHandler := handlers.NewStockHandler(stockService, logger)

	router, err := newRouter(pool, stockHandler, cfg, logger)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	var workerWG sync.WaitGroup
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		stockService.RunCleanupWorker(workerCtx, cfg.CleanupInterval)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server started", slog.String("address", cfg.Address))
		serverErrors <- server.ListenAndServe()
	}()

	var serveErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case serveErr = <-serverErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		} else {
			serveErr = fmt.Errorf("serve HTTP: %w", serveErr)
		}
	}

	stopWorker()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	workerWG.Wait()

	if shutdownErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", shutdownErr)
	}
	return serveErr
}

func newRouter(pool *pgxpool.Pool, stockHandler *handlers.StockHandler, cfg config.Config, logger *slog.Logger) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	router.Use(
		handlers.RequestIDMiddleware(),
		handlers.SecurityHeaders(),
		observabilityMiddleware(logger),
		gin.Recovery(),
	)

	router.GET("/metrics", handlers.RequireBearerToken(cfg.AdminAPIKey), gin.WrapH(promhttp.Handler()))
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		readyCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		var schemaReady bool
		err := pool.QueryRow(readyCtx, "SELECT to_regclass('inventory') IS NOT NULL AND to_regclass('reservations') IS NOT NULL").Scan(&schemaReady)
		if err != nil || !schemaReady {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	api := router.Group("/api/v1/stock")
	admin := api.Group("", handlers.RequireBearerToken(cfg.AdminAPIKey))
	admin.POST("/restock", stockHandler.HandleRestock)

	secured := api.Group("", handlers.RequireBearerToken(cfg.APIKey))
	secured.POST("/reserve", stockHandler.HandleReserve)
	secured.POST("/cancel", stockHandler.HandleCancel)
	secured.POST("/clear", stockHandler.HandleCancel) // Backward-compatible alias.
	secured.POST("/confirm", stockHandler.HandleConfirm)

	return router, nil
}

func observabilityMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		method := c.Request.Method
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		status := c.Writer.Status()
		duration := time.Since(startedAt)

		httpRequestsTotal.WithLabelValues(path, method, strconv.Itoa(status)).Inc()
		httpRequestDuration.WithLabelValues(path, method).Observe(duration.Seconds())

		logger.InfoContext(c.Request.Context(), "HTTP request processed",
			slog.String("request_id", handlers.RequestID(c)),
			slog.String("method", method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Int("response_bytes", c.Writer.Size()),
			slog.Duration("duration", duration),
			slog.String("client_ip", c.ClientIP()),
		)
	}
}
