package internal

import (
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
)

// IsRetryableError determines if an error is transient and should be retried
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Google API errors
	if apiErr, ok := err.(*googleapi.Error); ok {
		// Retry on rate limit, server errors, and service unavailable
		// 429 - Too Many Requests (rate limit)
		// 500 - Internal Server Error
		// 502 - Bad Gateway
		// 503 - Service Unavailable
		// 504 - Gateway Timeout
		return apiErr.Code == 429 || apiErr.Code >= 500
	}

	// Check for context deadline exceeded (timeout)
	errStr := err.Error()
	if strings.Contains(errStr, "context deadline exceeded") {
		return true
	}

	// Check for network errors
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "UNAVAILABLE") {
		return true
	}

	// OAuth token refresh errors are not retryable at this level
	// (they should be handled before message delivery)
	if strings.Contains(errStr, "oauth2") || strings.Contains(errStr, "token") {
		return false
	}

	// Authentication errors are generally not retryable
	if strings.Contains(errStr, "authentication failed") ||
		strings.Contains(errStr, "invalid credentials") {
		return false
	}

	return false
}

// CalculateBackoff calculates exponential backoff delay
func CalculateBackoff(attempt int, baseDelay int) time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	backoff := float64(baseDelay) * math.Pow(2, float64(attempt))
	// Cap at 60 seconds
	if backoff > 60 {
		backoff = 60
	}
	return time.Duration(backoff) * time.Second
}

// RetryConfig holds retry configuration
type RetryConfig struct {
	MaxRetries int
	RetryDelay int
}

// LoggerInterface interface for retry operations
type LoggerInterface interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// RetryOperation executes an operation with exponential backoff retry logic
func RetryOperation(cfg *RetryConfig, logger LoggerInterface, operation func() error, operationName string) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := CalculateBackoff(attempt-1, cfg.RetryDelay)
			logger.Info("retrying operation", "operation", operationName, "attempt", attempt, "max_attempts", cfg.MaxRetries, "backoff", backoff)
			time.Sleep(backoff)
		}

		err := operation()
		if err == nil {
			if attempt > 0 {
				logger.Info("operation succeeded after retries", "operation", operationName, "attempts", attempt)
			}
			return nil
		}

		lastErr = err

		if !IsRetryableError(err) {
			logger.Error("operation failed with non-retryable error", "operation", operationName, "error", err)
			return err
		}

		logger.Info("operation failed with retryable error", "operation", operationName, "attempt", attempt+1, "max_attempts", cfg.MaxRetries+1, "error", err)
	}

	logger.Error("operation failed after max retries", "operation", operationName, "attempts", cfg.MaxRetries+1)
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
