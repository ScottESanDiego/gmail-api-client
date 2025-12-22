package internal

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// Level represents a log level
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger provides structured logging with support for verbose mode
type Logger struct {
	verbose   bool
	minLevel  Level
	logger    *log.Logger
	component string
	firstLine bool // Track if we've written the first line to stdout
}

// NewLogger creates a new logger
func NewLogger(verbose bool, component string) *Logger {
	minLevel := LevelInfo
	if verbose {
		minLevel = LevelDebug
	}

	return &Logger{
		verbose:   verbose,
		minLevel:  minLevel,
		logger:    log.New(io.Discard, "", log.LstdFlags),
		component: component,
		firstLine: true,
	}
}

// SetOutput sets the log output writer (for verbose mode)
func (l *Logger) SetOutput(w io.Writer) {
	l.logger.SetOutput(w)
}

// formatMessage formats a structured log message
func (l *Logger) formatMessage(level Level, msg string, args ...interface{}) string {
	var sb strings.Builder

	// Component prefix
	if l.component != "" {
		sb.WriteString(fmt.Sprintf("[%s] ", l.component))
	}

	// Level prefix for verbose mode
	if l.verbose {
		sb.WriteString(fmt.Sprintf("[%s] ", level.String()))
	}

	// Message
	sb.WriteString(msg)

	// Key-value pairs
	if len(args) > 0 {
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				key := args[i]
				value := args[i+1]

				// Format duration values nicely
				if dur, ok := value.(time.Duration); ok {
					sb.WriteString(fmt.Sprintf(" %v=%v", key, dur))
				} else {
					sb.WriteString(fmt.Sprintf(" %v=%v", key, value))
				}
			}
		}
	}

	return sb.String()
}

// log is the internal logging function
func (l *Logger) log(level Level, msg string, args ...interface{}) {
	if level < l.minLevel {
		return
	}

	formatted := l.formatMessage(level, msg, args...)

	// In verbose mode, write to the logger (stderr by default)
	if l.verbose {
		l.logger.Output(2, formatted)
	}
}

// Debug logs a debug message (only in verbose mode)
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.log(LevelDebug, msg, args...)
}

// Info logs an info message
func (l *Logger) Info(msg string, args ...interface{}) {
	l.log(LevelInfo, msg, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.log(LevelWarn, msg, args...)
}

// Error logs an error message
func (l *Logger) Error(msg string, args ...interface{}) {
	l.log(LevelError, msg, args...)
}

// Success writes a success message to stdout (for Exim logging)
// This should be called once at the end of successful operations
func (l *Logger) Success(msg string) {
	fmt.Println(msg)
}

// Fatal writes an error message to stderr and exits with code 1
// This writes to stderr for Exim error capture and ensures first line is useful
func (l *Logger) Fatal(msg string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s: %v\n", msg, err)
	} else {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", msg)
	}
	os.Exit(1)
}

// Progress logs a progress message that's always shown (for critical operations)
// In non-verbose mode, these help track what's happening without flooding logs
func (l *Logger) Progress(msg string) {
	if l.verbose {
		l.logger.Output(2, fmt.Sprintf("[%s] [INFO] %s", l.component, msg))
	}
}
