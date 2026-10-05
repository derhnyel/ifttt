package log

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	mu      sync.RWMutex
	logger  *slog.Logger
	handler slog.Handler
)

func init() {
	setHandler(slog.LevelInfo)
}

// SetLevel updates the logger handler with the provided slog level.
func SetLevel(level slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	setHandler(level)
}

func setHandler(level slog.Level) {
	handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	logger = slog.New(handler)
}

// Logger returns the current slog logger.
func Logger() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return logger
}

// ParseLevel converts a string into a slog.Level.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug", "trace":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", raw)
	}
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	Logger().Debug(msg, args...)
}

// Info logs an informational message.
func Info(msg string, args ...any) {
	Logger().Info(msg, args...)
}

// Warn logs a warning.
func Warn(msg string, args ...any) {
	Logger().Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	Logger().Error(msg, args...)
}

// Enabled reports whether logs at the provided level would be emitted.
func Enabled(level slog.Level) bool {
	return Logger().Enabled(nil, level)
}
