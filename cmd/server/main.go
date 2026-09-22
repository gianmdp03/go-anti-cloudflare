package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gcast/go-anti-cloudflare/internal/config"
	"github.com/gcast/go-anti-cloudflare/internal/middleware"
	"github.com/gcast/go-anti-cloudflare/internal/proxy"
	"github.com/gcast/go-anti-cloudflare/internal/session"
)

const version = "1.0.0"

func main() {
	startTime := time.Now()

	// 1. Fail-Fast configuration loading
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: configuration validation failed: %v\n", err)
		os.Exit(1)
	}

	// 2. Configure human-readable operational logging for local runs and Docker logs.
	var programLevel slog.Level
	switch cfg.LogLevel() {
	case "debug":
		programLevel = slog.LevelDebug
	case "warn":
		programLevel = slog.LevelWarn
	case "error":
		programLevel = slog.LevelError
	default:
		programLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: programLevel,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts)).With(
		slog.String("service", "mgp-proxy-sidecar"),
		slog.String("version", version),
	)
	slog.SetDefault(logger)

	logger.Info("iniciando proxy MGP",
		slog.Int("port", cfg.Port()),
		slog.String("listen_addr", cfg.ListenAddr()),
		slog.String("upstream_url", cfg.UpstreamURLString()),
		slog.Duration("timeout", cfg.Timeout()),
		slog.String("allowed_origin", cfg.AllowedOrigin()),
		slog.String("tls_profile", cfg.TLSProfile()),
		slog.String("log_level", cfg.LogLevel()),
	)

	// 3. Initialize TLS spoofing engine
	engine, err := proxy.NewTLSEngine(cfg.TLSProfile(), cfg.Timeout(), cfg.ProxyURL())
	if err != nil {
		logger.Error("no se pudo iniciar el motor TLS", "error", err)
		os.Exit(1)
	}
	defer engine.CloseIdleConnections()

	// 4. Initialize session credential manager
	sessionMgr := session.NewManager(cfg.AuthServiceURL())

	// 5. Register HTTP routes
	mux := http.NewServeMux()
	mux.Handle("/healthz", proxy.HealthHandler(startTime, engine.ProfileName()))
	mux.Handle("/proxy", proxy.NewHandler(cfg, engine, sessionMgr, logger))

	// 5. Wrap router with production middleware chain
	// Chain order: Recovery (outermost) -> Security/CORS -> Logger -> Handler (innermost)
	var handler http.Handler = mux
	handler = middleware.Logger(logger)(handler)
	handler = middleware.Security(middleware.SecurityOptions{
		AllowedOrigin: cfg.AllowedOrigin(),
	})(handler)
	handler = middleware.Recovery(logger)(handler)

	// 6. Configure HTTP Server with strict timeouts against Slowloris
	server := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// 7. Launch server listener in background goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("proxy listo para recibir consultas", slog.String("addr", cfg.ListenAddr()))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// 8. Graceful Shutdown listener
	shutdownSig := make(chan os.Signal, 1)
	signal.Notify(shutdownSig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErrors:
		logger.Error("el servidor no pudo iniciarse", "error", err)
		os.Exit(1)
	case sig := <-shutdownSig:
		logger.Info("señal de apagado recibida", "signal", sig.String())

		// Drain active in-flight requests with 5-second context timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("falló el apagado ordenado; se fuerza el cierre", "error", err)
			_ = server.Close()
		}

		engine.CloseIdleConnections()
		logger.Info("proxy detenido; conexiones finalizadas")
	}
}
