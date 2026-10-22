package observability

import (
	"log"
	"os"
)

// Logger is a minimal structured logger for services.
type Logger struct {
	std *log.Logger
}

// NewLogger returns a stderr logger with a service prefix.
func NewLogger(service string) *Logger {
	prefix := service
	if prefix != "" {
		prefix = service + ": "
	}
	return &Logger{std: log.New(os.Stderr, prefix, log.LstdFlags)}
}

// Infof logs an informational message.
func (l *Logger) Infof(format string, args ...any) {
	if l == nil || l.std == nil {
		return
	}
	l.std.Printf(format, args...)
}

// Warnf logs a warning message.
func (l *Logger) Warnf(format string, args ...any) {
	if l == nil || l.std == nil {
		return
	}
	l.std.Printf("warning: "+format, args...)
}
