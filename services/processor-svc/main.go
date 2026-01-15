package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Config struct {
	Port               string
	KafkaBrokers       []string
	KafkaTopic         string
	KafkaConsumerGroup string
	DatabaseURL        string
	SummaryBucket      time.Duration
	BatchSize          int
	FlushInterval      time.Duration
}

func loadConfig() Config {
	return Config{
		Port:               common.GetenvOrDefault("PORT", "8080"),
		KafkaBrokers:       common.SplitCommaSeparated(common.RequireEnv("KAFKA_BROKERS")),
		KafkaTopic:         common.RequireEnv("KAFKA_TOPIC"),
		KafkaConsumerGroup: common.GetenvOrDefault("KAFKA_CONSUMER_GROUP", "processor-svc"),
		DatabaseURL:        common.RequireEnv("DATABASE_URL"),
		SummaryBucket:      time.Second * time.Duration(common.GetenvOrDefaultInt("SUMMARY_BUCKET_SECONDS", "300")),
		BatchSize:          common.GetenvOrDefaultInt("PROCESSOR_BATCH_SIZE", "100"),
		FlushInterval:      time.Millisecond * time.Duration(common.GetenvOrDefaultInt("PROCESSOR_FLUSH_INTERVAL_MS", "500")),
	}
}

// Server state
type Server struct {
	cfg      Config
	ready    atomic.Bool
	consumer *kgo.Client
	db       *pgxpool.Pool
}

func main() {
	logLevel := common.InitSlog()

	s := &Server{
		cfg: loadConfig(),
	}
	db, err := common.ConnectPGXPoolWithRetry(context.Background(), s.cfg.DatabaseURL, logLevel, 10, 3*time.Second)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := runMigrations(db); err != nil {
		slog.Error("failed to run database migrations", "error", err)
		os.Exit(1)
	}
	s.db = db
	sqlDB, err := registerDBMetrics(db)
	if err != nil {
		slog.Error("failed to register database metrics", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("failed to close sql db", "error", err)
		}
	}()

	kafkaLogLevel := common.KgoLogLevelFromString(logLevel)
	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(s.cfg.KafkaBrokers...),
		kgo.WithLogger(common.NewKgoSlogLogger(slog.Default().With("component", "kafka"), kafkaLogLevel)),
		kgo.ConsumerGroup(s.cfg.KafkaConsumerGroup),
		kgo.ConsumeTopics(s.cfg.KafkaTopic),
		kgo.DisableAutoCommit(),
		// smarter readiness checks than a basic brokers ping
		kgo.OnPartitionsAssigned(func(ctx context.Context, cl *kgo.Client, assigned map[string][]int32) {
			if s.ready.CompareAndSwap(false, true) {
				slog.Info("consumer partitions assigned", "assignments", assigned)
			}
		}),
		kgo.OnPartitionsRevoked(func(ctx context.Context, cl *kgo.Client, revoked map[string][]int32) {
			if s.ready.CompareAndSwap(true, false) {
				slog.Info("consumer partitions revoked", "assignments", revoked)
			}
		}),
		kgo.OnPartitionsLost(func(ctx context.Context, cl *kgo.Client, lost map[string][]int32) {
			if s.ready.CompareAndSwap(true, false) {
				slog.Warn("consumer partitions lost", "assignments", lost)
			}
		}),
	)
	if err != nil {
		slog.Error("failed to create kafka client", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	s.consumer = consumer
	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	batchCh := make(chan batchItem, s.cfg.BatchSize*2)
	go s.processBatches(kafkaCtx, batchCh)
	go s.consume(kafkaCtx, batchCh)

	e := echo.New()
	common.SetupEchoDefaults(e, "processor-svc", s.handleHealth, s.handleReady)

	echoErrChan := make(chan error, 1)
	go func() {
		slog.Info("starting processor service", "port", s.cfg.Port)
		if err := e.Start(":" + s.cfg.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			echoErrChan <- err
		}
	}()

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
		slog.Info("shutting down")
	case err := <-echoErrChan:
		slog.Error("echo failed to start", "error", err)
		os.Exit(1)
	}

	s.ready.Store(false)
	kafkaCancel()
	time.Sleep(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		slog.Error("echo shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}

func (s *Server) handleHealth(c echo.Context) error {
	return c.NoContent(http.StatusOK)
}

func (s *Server) handleReady(c echo.Context) error {
	if !s.ready.Load() {
		return c.String(http.StatusServiceUnavailable, "not ready")
	}

	return c.NoContent(http.StatusOK)
}
