// Package logging builds the application's structured logger from configuration.
// It wraps the standart library's log/slog so the rest of the codebase depends
// only on *slog.Logger, not on a third-party logging framework.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/pipeprobe/pipeprobe/internal/config"
)

// New constructs an *slog.Logger from the log configuration. Level and format
// have already been validated by config.Validate, but New re-checks so it is
// safe to call independently (e.g. in tests).
//
// The caller decides whether to install it as the process-wide default via
// slog.SetDefault.
func New(cfg config.Log) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	w, err := openOutput(cfg.Output)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q", cfg.Format)
	}

	return slog.New(h), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q", s)
	}
}

// openOutput resolves the configured destination to a writer:
// "" or "stdout" -> os.Stdout, "stderr" -> os.Stderr, otherwise an append-mode
// file. For production file logging, wrap the returned file with a rotator
// (e.g. gopkg.in/natefinch/lumberjack.v2) at the call site.
func openOutput(out string) (io.Writer, error) {
	switch strings.ToLower(out) {
	case "", "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	default:
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open log file %q: %w", out, err)
		}
		return f, nil
	}
}
