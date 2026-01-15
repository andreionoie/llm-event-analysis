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
	"google.golang.org/genai"
)

type Config struct {
	Port         string
	DatabaseURL  string
	GeminiAPIKey string
	GeminiModel  string
	MaxEvents    int
}

func loadConfig() Config {
	return Config{
		Port:         common.GetenvOrDefault("PORT", "8080"),
		DatabaseURL:  common.RequireEnv("DATABASE_URL"),
		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
		GeminiModel:  common.GetenvOrDefault("GEMINI_MODEL", "gemini-3-flash-preview"),
		MaxEvents:    common.GetenvOrDefaultInt("ANALYZER_MAX_EVENTS", "100"),
	}
}

// Server state
type Server struct {
	cfg   Config
	ready atomic.Bool
	db    *pgxpool.Pool
	genai *genai.Client
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
	s.ready.Store(true)

	if s.cfg.GeminiAPIKey != "" {
		config := genai.ClientConfig{APIKey: s.cfg.GeminiAPIKey}
		client, err := genai.NewClient(context.Background(), &config)
		if err != nil {
			slog.Error("failed to create gemini client", "error", err)
			os.Exit(1)
		}
		s.genai = client
		slog.Info("gemini client initialized", "model", s.cfg.GeminiModel)
	} else {
		slog.Warn("GEMINI_API_KEY not set, analysis will return a mock response")
	}

	e := echo.New()
	common.SetupEchoDefaults(e, "analyzer-svc", s.handleHealth, s.handleReady)
	e.POST("/analyze", s.handleAnalyze)
	e.GET("/events", s.handleEvents)
	e.GET("/summaries", s.handleSummaries)

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
