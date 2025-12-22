package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Common holds configuration options common to both transports
type Common struct {
	CredentialsFile string `json:"credentials_file"`
	TokenFile       string `json:"token_file"`
	UserID          string `json:"user_id"`
	Verbose         bool   `json:"verbose"`
	MaxRetries      int    `json:"max_retries"`
	RetryDelay      int    `json:"retry_delay"`
}

// Validator interface for configuration validation
type Validator interface {
	Validate() error
}

// LoadJSON reads and parses a JSON configuration file
func LoadJSON(filename string, config interface{}) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	return nil
}

// ExpandPath expands a relative path to absolute based on config file directory
func ExpandPath(configFile, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	dir := filepath.Dir(configFile)
	return filepath.Join(dir, path)
}

// ValidateCommon validates common configuration fields and sets defaults
func ValidateCommon(common *Common) error {
	// Validate required fields
	if common.CredentialsFile == "" {
		return fmt.Errorf("credentials_file is required")
	}
	if common.TokenFile == "" {
		return fmt.Errorf("token_file is required")
	}

	// Check if files exist
	if _, err := os.Stat(common.CredentialsFile); os.IsNotExist(err) {
		return fmt.Errorf("credentials file not found: %s", common.CredentialsFile)
	}
	if _, err := os.Stat(common.TokenFile); os.IsNotExist(err) {
		return fmt.Errorf("token file not found: %s", common.TokenFile)
	}

	// Set defaults for retry configuration
	if common.MaxRetries <= 0 {
		common.MaxRetries = 3
	}
	if common.RetryDelay <= 0 {
		common.RetryDelay = 1
	}

	// Set default user ID
	if common.UserID == "" {
		common.UserID = "me"
	}

	return nil
}

// ValidateTimeout validates timeout values are reasonable
func ValidateTimeout(apiTimeout, operationTimeout int) error {
	if apiTimeout <= 0 {
		return fmt.Errorf("api_timeout must be positive")
	}
	if operationTimeout <= 0 {
		return fmt.Errorf("operation_timeout must be positive")
	}
	if apiTimeout > operationTimeout {
		return fmt.Errorf("api_timeout (%d) cannot be greater than operation_timeout (%d)",
			apiTimeout, operationTimeout)
	}
	return nil
}

// ValidateDelay validates delay values are reasonable
func ValidateDelay(delay int, maxDelay int, name string) error {
	if delay < 0 {
		return fmt.Errorf("%s cannot be negative", name)
	}
	if delay > maxDelay {
		return fmt.Errorf("%s (%d) is too large (max %d seconds)", name, delay, maxDelay)
	}
	return nil
}

// SetDefaults sets default values for integer fields if they are zero
func SetDefaults(field *int, defaultValue int) {
	if *field <= 0 {
		*field = defaultValue
	}
}
