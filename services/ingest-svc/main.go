package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"golang.org/x/time/rate"
)

type Config struct {
	Port         string
	KafkaBrokers []string
	KafkaTopic   string
}

func loadConfig() Config {
	return Config{
		Port:         common.GetenvOrDefault("PORT", "8080"),
		KafkaBrokers: common.SplitCommaSeparated(common.RequireEnv("KAFKA_BROKERS")),
		KafkaTopic:   common.RequireEnv("KAFKA_TOPIC"),
	}
}

// Server state
type Server struct {
	cfg          Config
	ready        atomic.Bool
	shuttingDown atomic.Bool
	producer     *kgo.Client
}

func main() {
	logLevel := common.InitSlog()

	s := &Server{
		cfg: loadConfig(),
	}
	kafkaLogLevel := common.KgoLogLevelFromString(logLevel)
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(s.cfg.KafkaBrokers...),
		kgo.WithLogger(common.NewKgoSlogLogger(slog.Default().With("component", "kafka"), kafkaLogLevel)),
		kgo.ProducerBatchMaxBytes(1000*1000),     // 1000KB batch max; TODO: configurable
		kgo.ProducerLinger(100*time.Millisecond), // time interval for accumulating batches; TODO: configurable
	)
	if err != nil {
		slog.Error("failed to create kafka client", "error", err)
		os.Exit(1)
	}
	defer producer.Close()
	s.producer = producer
	// periodic kafka liveness check
	go startKafkaBrokersHealthCheck(context.Background(), producer, &s.ready)

	e := echo.New()
	common.SetupEchoDefaults(e, "ingest-svc", s.handleHealth, s.handleReady)
	// in-memory IP addr-based rate limiter: https://echo.labstack.com/docs/middleware/rate-limiter
	e.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{Rate: rate.Limit(50), Burst: 100, ExpiresIn: 3 * time.Minute},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusForbidden, nil)
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, nil)
		},
	}))
	// endpoints
	e.POST("/events", s.handleIngest)
	e.POST("/events/batch", s.handleIngestBatch)

	echoErrChan := make(chan error, 1)
	go func() {
		slog.Info("starting ingest service", "port", s.cfg.Port)
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

	s.shuttingDown.Store(true)
	s.ready.Store(false)
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

func startKafkaBrokersHealthCheck(ctx context.Context, kafkaClient *kgo.Client, ready *atomic.Bool) {
	check := func() {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		err := kafkaClient.Ping(pingCtx)
		if err != nil {
			if ready.CompareAndSwap(true, false) {
				slog.Warn("kafka not reachable", "error", err, "brokers", getBrokers(pingCtx, kafkaClient))
			}
		} else {
			if ready.CompareAndSwap(false, true) {

				slog.Info("kafka connection established", "brokers", getBrokers(pingCtx, kafkaClient))
			}
		}
	}

	check()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func getBrokers(ctx context.Context, kafkaClient *kgo.Client) []string {
	req := kmsg.NewMetadataRequest()
	md, mdErr := kafkaClient.RequestCachedMetadata(ctx, &req, 0)

	var brokerList []string
	if mdErr == nil {
		for _, b := range md.Brokers {
			addr := net.JoinHostPort(b.Host, strconv.Itoa(int(b.Port)))
			brokerList = append(brokerList, addr)
		}
	}

	return brokerList
}
