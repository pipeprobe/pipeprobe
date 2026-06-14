package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/pipeprobe/pipeprobe/internal/api"
	"github.com/pipeprobe/pipeprobe/internal/config"
	"github.com/pipeprobe/pipeprobe/internal/logging"
	"github.com/pipeprobe/pipeprobe/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/config.yaml", "path to the YAML config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	logger.Info("starting pipeprobe API service",
		"app", cfg.App.Name,
		"version", cfg.App.Version,
		"environment", cfg.App.Environment,
	)

	logger.Info("configuration loaded",
		"server.host", cfg.Server.Host,
		"server.port", cfg.Server.Port,
		"db", cfg.DB,
		"security", cfg.Security,
	)

	// 1. Open the pool and verify the database is reachable before anything
	//    else. sql.Open is lazy, so the Ping is what actually proves the DB is
	//    up. If it fails we return here and the HTTP server never starts.
	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := st.Ping(pingCtx); err != nil {
		return fmt.Errorf("database not available: %w", err)
	}
	logger.Info("database connection verified",
		"host", cfg.DB.Host, "port", cfg.DB.Port, "name", cfg.DB.Name,
	)

	// 2. Start the HTTP server.
	srv := &http.Server{
		Addr:         net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:      api.NewRouter(st),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// 3. Block until a shutdown signal or a server startup error, then drain
	//    in-flight requests within ShutdownTimeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown timed out, forcing", "err", err)
		_ = srv.Close()
		return err
	}

	logger.Info("server stopped cleanly")
	return nil
}
