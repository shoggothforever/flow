// Package logx wraps log/slog with a simple verbosity switch.
package logx

import (
	"log/slog"
	"os"
	"sync"
)

var (
	initOnce sync.Once
	logger   *slog.Logger
)

// Init sets up the package-level logger. Calling more than once is a no-op.
// When verbose is true, log level is DEBUG; otherwise WARN (so successful runs are quiet).
func Init(verbose bool) {
	initOnce.Do(func() {
		level := slog.LevelWarn
		if verbose {
			level = slog.LevelDebug
		}
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
		logger = slog.New(h)
	})
}

// L returns the package logger, initializing a default one if Init was not called.
func L() *slog.Logger {
	if logger == nil {
		Init(false)
	}
	return logger
}

// Debug, Info, Warn, Error are convenience helpers.
func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }
