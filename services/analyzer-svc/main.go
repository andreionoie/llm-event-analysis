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
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
	"google.golang.org/genai"
)

type Config struct {
	Port         string
	DatabaseURL  string
	RedisAddr    string
	GeminiAPIKey string
	MaxEvents    int
}

func loadConfig() Config {
	return Config{
		Port:         common.GetenvOrDefault("PORT", "8080"),
		DatabaseURL:  common.RequireEnv("DATABASE_URL"),
		RedisAddr:    common.RequireEnv("REDIS_ADDR"),
		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
		MaxEvents:    common.GetenvOrDefaultInt("ANALYZER_MAX_EVENTS", "100"),
	}
}

// Server state
type Server struct {
	cfg                 Config
	ready               atomic.Bool
	db                  *pgxpool.Pool
	cache               *redis.Client
	genai               *genai.Client
	genaiCircuitBreaker *gobreaker.CircuitBreaker[*genai.GenerateContentResponse]
	prompts             *PromptLibrary
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
	s.db = db

	rdb := redis.NewClient(&redis.Options{Addr: s.cfg.RedisAddr})
	defer func(rdb *redis.Client) {
		err := rdb.Close()
		if err != nil {
			slog.Error("failed to close redis client", "error", err)
		}
	}(rdb)
	// Since cache is just an optimization, we do not verify connection on startup for simplicity
	s.cache = rdb

	s.ready.Store(true)

	prompts, err := NewPromptLibrary(promptsFS)
	if err != nil {
		slog.Error("failed to load prompts", "error", err)
		os.Exit(1)
	}
	s.prompts = prompts

	if s.cfg.GeminiAPIKey != "" {
		httpLogger := slog.Default().With("component", "genai_http")
		config := genai.ClientConfig{
			APIKey:     s.cfg.GeminiAPIKey,
			HTTPClient: newLoggingHTTPClient(httpLogger),
		}
		client, err := genai.NewClient(context.Background(), &config)
		if err != nil {
			slog.Error("failed to create gemini client", "error", err)
			os.Exit(1)
		}
		s.genai = client
		slog.Info("google genai client initialized")
		s.genaiCircuitBreaker = gobreaker.NewCircuitBreaker[*genai.GenerateContentResponse](gobreaker.Settings{
			Name:    "genai-client",
			Timeout: 60,
			OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
				slog.Debug("circuit breaker state change", "name", name, "from", from, "to", to)
			},
			IsSuccessful: nil,
			IsExcluded:   nil,
		})
	} else {
		slog.Warn("GEMINI_API_KEY not set, analysis will return a mock response")
	}

	e := echo.New()
	common.SetupEchoDefaults(e, "analyzer-svc", s.handleHealth, s.handleReady)
	e.POST("/analyze", s.handleAnalyze)
	e.GET("/events", s.handleEvents)
	e.GET("/summaries", s.handleSummaries)
	e.POST("/triage/jobs", s.handleCreateTriageJob)
	e.GET("/triage/jobs/:id", s.handleGetTriageJob)

	echoErrChan := make(chan error, 1)
	go func() {
		slog.Info("starting analyzer service", "port", s.cfg.Port)
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

func newLoggingHTTPClient(logger *slog.Logger) *http.Client {
	// TODO: instrument the genai client with prometheus metrics as well
	return &http.Client{
		Transport: &loggingRoundTripper{
			base:   http.DefaultTransport,
			logger: logger,
		},
	}
}

type loggingRoundTripper struct {
	base   http.RoundTripper
	logger *slog.Logger
}

func (l *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := l.base
	if base == nil {
		base = http.DefaultTransport
	}

	if l.logger == nil {
		return base.RoundTrip(req)
	}

	start := time.Now()
	resp, err := base.RoundTrip(req)
	latency := time.Since(start)
	if err != nil {
		l.logger.Warn("genai http request failed",
			"method", req.Method,
			"host", req.URL.Host,
			"path", req.URL.Path,
			"latency", latency,
			"error", err,
		)
		return resp, err
	}

	l.logger.Debug("genai http request",
		"method", req.Method,
		"host", req.URL.Host,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"latency", latency,
	)
	return resp, err
}
