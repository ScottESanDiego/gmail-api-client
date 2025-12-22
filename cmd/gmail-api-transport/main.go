package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gmail-api-client/internal/oauth"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// Config holds the application configuration
type Config struct {
	// Path to OAuth2 credentials JSON file (from Google Cloud Console)
	CredentialsFile string `json:"credentials_file"`
	// Path to stored OAuth2 token file
	TokenFile string `json:"token_file"`
	// Gmail user ID (email address or "me" for authenticated user)
	UserID string `json:"user_id"`
	// Enable verbose logging
	Verbose bool `json:"verbose"`
	// Never mark as spam (ignore Gmail spam classifier)
	NotSpam bool `json:"not_spam"`
	// Use Insert instead of Import (bypasses scanning, similar to IMAP APPEND)
	UseInsert bool `json:"use_insert"`
	// API call timeout in seconds (default: 30)
	APITimeout int `json:"api_timeout"`
	// Overall operation timeout in seconds (default: 120)
	OperationTimeout int `json:"operation_timeout"`
	// Filter processing delay in seconds (default: 2)
	FilterDelay int `json:"filter_delay"`
	// Maximum retry attempts for transient failures (default: 3)
	MaxRetries int `json:"max_retries"`
	// Initial retry delay in seconds (default: 1)
	RetryDelay int `json:"retry_delay"`
}

var verbose bool
var neverMarkSpam bool
var useInsert bool
var testAPI bool

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <config-file> [-v|--verbose] [--not-spam] [--use-insert] [--test-api]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nReads email message from stdin and imports it to Gmail using the API.\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -v, --verbose    Enable verbose logging\n")
		fmt.Fprintf(os.Stderr, "  --not-spam       Never mark this message as spam (only with import)\n")
		fmt.Fprintf(os.Stderr, "  --use-insert     Use Insert API instead of Import (bypasses scanning)\n")
		fmt.Fprintf(os.Stderr, "  --test-api       Test API connection (shows Gmail language settings)\n")
		os.Exit(1)
	}

	configFile := os.Args[1]

	// Check for flags
	for _, arg := range os.Args[2:] {
		switch arg {
		case "-v", "--verbose":
			verbose = true
		case "--not-spam":
			neverMarkSpam = true
		case "--use-insert":
			useInsert = true
		case "--test-api":
			testAPI = true
		}
	}

	// Setup logging
	log.SetFlags(log.LstdFlags)
	if !verbose {
		log.SetOutput(io.Discard)
	}

	log.Printf("Starting gmail-api-transport")
	log.Printf("Config file: %s", configFile)

	// Load configuration
	config, err := loadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Validate configuration
	if err := validateConfig(config); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Override verbose setting if command line flag is set
	if verbose {
		config.Verbose = true
	}

	// Override not-spam setting if command line flag is set
	if neverMarkSpam {
		config.NotSpam = true
	}

	// Override use-insert setting if command line flag is set
	if useInsert {
		config.UseInsert = true
	}

	log.Printf("Configuration loaded successfully")
	log.Printf("  User ID: %s", config.UserID)
	log.Printf("  Credentials file: %s", config.CredentialsFile)
	log.Printf("  Token file: %s", config.TokenFile)
	log.Printf("  Never mark spam: %v", config.NotSpam)
	log.Printf("  Use Insert API: %v", config.UseInsert)

	// If test-api mode, just test the API connection and exit
	if testAPI {
		log.Printf("Testing Gmail API connection...")
		if err := testAPIConnection(config); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: API test failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Pre-validate and refresh token before reading message from stdin
	// This ensures we don't read and lose a message if auth fails
	log.Printf("Validating OAuth2 token before reading message...")
	if err := validateAndRefreshToken(config); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Token validation failed: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Token validated successfully")

	// Read email message from stdin
	log.Printf("Reading message from stdin...")
	message, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to read from stdin: %v\n", err)
		os.Exit(1)
	}

	if len(message) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: No message received from stdin\n")
		os.Exit(1)
	}

	log.Printf("Message received: %d bytes", len(message))

	// Deliver message to Gmail
	if err := deliverMessage(config, message); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to deliver message: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Message successfully delivered to Gmail")
	fmt.Println("Message successfully imported to Gmail")
}

// loadConfig reads and parses the configuration file
func loadConfig(filename string) (*Config, error) {
	log.Printf("Loading configuration from: %s", filename)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Set defaults
	if config.UserID == "" {
		config.UserID = "me"
		log.Printf("Using default user ID: me")
	}

	// Expand relative paths
	if !filepath.IsAbs(config.CredentialsFile) {
		dir := filepath.Dir(filename)
		config.CredentialsFile = filepath.Join(dir, config.CredentialsFile)
		log.Printf("Expanded credentials file path: %s", config.CredentialsFile)
	}
	if !filepath.IsAbs(config.TokenFile) {
		dir := filepath.Dir(filename)
		config.TokenFile = filepath.Join(dir, config.TokenFile)
		log.Printf("Expanded token file path: %s", config.TokenFile)
	}

	return &config, nil
}

// validateAndRefreshToken validates the token and refreshes it if needed
// This is called before reading message from stdin to avoid losing messages
func validateAndRefreshToken(config *Config) error {
	log.Printf("Loading and validating OAuth2 token...")

	// Load original token to compare later
	originalToken, err := oauth.LoadToken(config.TokenFile)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	// Load OAuth config
	oauthConfig, err := oauth.LoadOAuthConfig(config.CredentialsFile)
	if err != nil {
		return fmt.Errorf("loading OAuth config: %w", err)
	}

	// Create token source
	tokenSource := oauth.CreateTokenSource(oauthConfig, originalToken)

	// Get fresh token (auto-refreshes if needed)
	freshToken, wasRefreshed, err := oauth.RefreshToken(tokenSource, originalToken)
	if err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}

	// Save if refreshed, preserving original permissions
	if wasRefreshed {
		log.Printf("Token was refreshed, saving to file...")
		if err := oauth.SaveTokenIfChanged(config.TokenFile, originalToken, freshToken); err != nil {
			return fmt.Errorf("saving refreshed token: %w", err)
		}
		log.Printf("Refreshed token saved successfully")
	}

	return nil
}

// validateConfig validates the configuration and sets defaults
func validateConfig(config *Config) error {
	log.Printf("Validating configuration...")

	// Validate required fields
	if config.CredentialsFile == "" {
		return fmt.Errorf("credentials_file is required")
	}
	if config.TokenFile == "" {
		return fmt.Errorf("token_file is required")
	}

	// Check if files exist
	if _, err := os.Stat(config.CredentialsFile); os.IsNotExist(err) {
		return fmt.Errorf("credentials file not found: %s", config.CredentialsFile)
	}
	if _, err := os.Stat(config.TokenFile); os.IsNotExist(err) {
		return fmt.Errorf("token file not found: %s", config.TokenFile)
	}

	// Set timeout defaults if not specified
	if config.APITimeout <= 0 {
		config.APITimeout = 30
		log.Printf("Using default API timeout: %d seconds", config.APITimeout)
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = 120
		log.Printf("Using default operation timeout: %d seconds", config.OperationTimeout)
	}
	if config.FilterDelay <= 0 {
		config.FilterDelay = 2
		log.Printf("Using default filter delay: %d seconds", config.FilterDelay)
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
		log.Printf("Using default max retries: %d", config.MaxRetries)
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = 1
		log.Printf("Using default retry delay: %d seconds", config.RetryDelay)
	}

	// Validate timeout values are reasonable
	if config.APITimeout > config.OperationTimeout {
		return fmt.Errorf("api_timeout (%d) cannot be greater than operation_timeout (%d)",
			config.APITimeout, config.OperationTimeout)
	}
	if config.FilterDelay > 30 {
		return fmt.Errorf("filter_delay (%d) is too large (max 30 seconds)", config.FilterDelay)
	}

	log.Printf("Configuration validated successfully")
	return nil
}

// getGmailService creates and returns a Gmail service client and token source
func getGmailService(config *Config) (*gmail.Service, oauth2.TokenSource, error) {
	log.Printf("Creating Gmail API service...")

	// Use shared oauth package to handle token refresh
	freshToken, tokenSource, err := oauth.RefreshAndSaveToken(config.CredentialsFile, config.TokenFile)
	if err != nil {
		return nil, nil, err
	}

	// Create OAuth2 client with background context
	// The token source handles refresh independently
	log.Printf("Creating OAuth2 HTTP client...")
	client := oauth2.NewClient(context.Background(), tokenSource)

	// Create Gmail service with timeout context for API operations
	// This timeout applies to API calls, not token refresh
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.APITimeout)*time.Second)
	defer cancel()

	log.Printf("Initializing Gmail API service...")
	service, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, nil, fmt.Errorf("creating Gmail service: %w", err)
	}
	log.Printf("Gmail API service created successfully")

	// Update token reference in case it was refreshed
	_ = freshToken

	return service, tokenSource, nil
}

// testAPIConnection tests the Gmail API connection by calling getLanguage
func testAPIConnection(config *Config) error {
	log.Printf("Creating Gmail API service for testing...")

	// Load original token to compare later
	originalToken, err := oauth.LoadToken(config.TokenFile)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	service, tokenSource, err := getGmailService(config)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

	// Defer saving the token only if it changed
	defer func() {
		if token, err := tokenSource.Token(); err == nil {
			if err := oauth.SaveTokenIfChanged(config.TokenFile, originalToken, token); err != nil {
				log.Printf("WARNING: Failed to save token: %v", err)
			}
		}
	}()

	log.Printf("Calling Gmail API users.settings.getLanguage for user: %s", config.UserID)
	langSettings, err := service.Users.Settings.GetLanguage(config.UserID).Do()
	if err != nil {
		return fmt.Errorf("calling getLanguage: %w", err)
	}

	log.Printf("API test successful!")
	fmt.Println("\n=== Gmail API Connection Test ===")
	fmt.Println("Status: SUCCESS")
	fmt.Printf("User ID: %s\n", config.UserID)
	fmt.Printf("Display Language: %s\n", langSettings.DisplayLanguage)
	fmt.Println("=================================")

	return nil
}

// isRetryableError determines if an error is transient and should be retried
func isRetryableError(err error) bool {
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
		strings.Contains(errStr, "i/o timeout") {
		return true
	}

	// OAuth token refresh errors are not retryable at this level
	// (they should be handled before message delivery)
	if strings.Contains(errStr, "oauth2") || strings.Contains(errStr, "token") {
		return false
	}

	return false
}

// calculateBackoff calculates exponential backoff delay
func calculateBackoff(attempt int, baseDelay int) time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	// With jitter to avoid thundering herd
	backoff := float64(baseDelay) * math.Pow(2, float64(attempt))
	// Cap at 60 seconds
	if backoff > 60 {
		backoff = 60
	}
	return time.Duration(backoff) * time.Second
}

// retryOperation executes an operation with exponential backoff retry logic
func retryOperation(config *Config, operation func() error, operationName string) error {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(attempt-1, config.RetryDelay)
			log.Printf("Retry attempt %d/%d for %s after %v", attempt, config.MaxRetries, operationName, backoff)
			time.Sleep(backoff)
		}

		err := operation()
		if err == nil {
			if attempt > 0 {
				log.Printf("%s succeeded after %d retries", operationName, attempt)
			}
			return nil
		}

		lastErr = err

		if !isRetryableError(err) {
			log.Printf("%s failed with non-retryable error: %v", operationName, err)
			return err
		}

		log.Printf("%s failed with retryable error (attempt %d/%d): %v",
			operationName, attempt+1, config.MaxRetries+1, err)
	}

	log.Printf("%s failed after %d attempts", operationName, config.MaxRetries+1)
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// deliverMessage delivers an email message to Gmail using either Import or Insert API
func deliverMessage(config *Config, rawMessage []byte) error {
	log.Printf("Preparing to deliver message...")

	// Load original token to compare later
	originalToken, err := oauth.LoadToken(config.TokenFile)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	service, tokenSource, err := getGmailService(config)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

	// Defer saving the token only if it changed
	defer func() {
		if token, err := tokenSource.Token(); err == nil {
			if err := oauth.SaveTokenIfChanged(config.TokenFile, originalToken, token); err != nil {
				log.Printf("WARNING: Failed to save token: %v", err)
			}
		}
	}()

	// Encode message in base64url format (required by Gmail API)
	log.Printf("Encoding message (%d bytes) to base64url...", len(rawMessage))
	encodedMessage := base64.URLEncoding.EncodeToString(rawMessage)
	log.Printf("Encoded message size: %d bytes", len(encodedMessage))

	// Create the message object without labels - let Gmail apply filters first
	message := &gmail.Message{
		Raw: encodedMessage,
	}

	var result *gmail.Message

	// Wrap the API call in retry logic
	err = retryOperation(config, func() error {
		var apiErr error

		if config.UseInsert {
			// Use Insert API - bypasses most scanning and classification (like IMAP APPEND)
			log.Printf("Calling Gmail API users.messages.insert for user: %s", config.UserID)
			log.Printf("Insert bypasses most scanning and classification")

			call := service.Users.Messages.Insert(config.UserID, message).
				InternalDateSource("dateHeader")

			result, apiErr = call.Do()
		} else {
			// Use Import API - performs standard email delivery scanning and classification
			log.Printf("Calling Gmail API users.messages.import for user: %s", config.UserID)
			if config.NotSpam {
				log.Printf("Setting neverMarkSpam=true to bypass Gmail spam classifier")
			}

			call := service.Users.Messages.Import(config.UserID, message).
				InternalDateSource("dateHeader")

			if config.NotSpam {
				call = call.NeverMarkSpam(true)
			}

			result, apiErr = call.Do()
		}

		return apiErr
	}, "message delivery")

	if err != nil {
		return fmt.Errorf("delivering message: %w", err)
	}

	log.Printf("Message delivered successfully")
	log.Printf("  Message ID: %s", result.Id)
	log.Printf("  Thread ID: %s", result.ThreadId)
	if len(result.LabelIds) > 0 {
		log.Printf("  Labels: %v", result.LabelIds)
	}

	// Wait for Gmail filters to apply (labels may be applied asynchronously)
	filterDelay := time.Duration(config.FilterDelay) * time.Second
	log.Printf("Waiting %v for Gmail filters to process...", filterDelay)
	time.Sleep(filterDelay)

	// Re-fetch the message to get updated labels after filters have run
	// Wrap in retry logic
	err = retryOperation(config, func() error {
		var fetchErr error
		result, fetchErr = service.Users.Messages.Get(config.UserID, result.Id).Format("metadata").Do()
		return fetchErr
	}, "message re-fetch")

	if err != nil {
		// Non-fatal: continue even if re-fetch fails
		log.Printf("WARNING: Failed to re-fetch message: %v", err)
		log.Printf("WARNING: Continuing with original labels")
	} else {
		log.Printf("Labels after filter processing: %v", result.LabelIds)
	}

	// Attempt to apply labels - failures are non-fatal
	if err := applyLabels(service, config, result); err != nil {
		// Log warning but don't fail the delivery
		log.Printf("WARNING: Label modification had issues: %v", err)
		fmt.Fprintf(os.Stderr, "WARNING: Message delivered but label modification failed: %v\n", err)
	}

	return nil
}

// applyLabels applies INBOX and UNREAD labels as needed
func applyLabels(service *gmail.Service, config *Config, result *gmail.Message) error {
	// Check if Gmail applied any user labels (from filters)
	// If not, add INBOX label so message appears in inbox
	hasUserLabel := false
	for _, label := range result.LabelIds {
		// System labels start with uppercase, user labels are IDs
		// Check for common system labels
		if label != "UNREAD" && label != "IMPORTANT" && label != "CATEGORY_PERSONAL" &&
			label != "CATEGORY_SOCIAL" && label != "CATEGORY_PROMOTIONS" &&
			label != "CATEGORY_UPDATES" && label != "CATEGORY_FORUMS" {
			hasUserLabel = true
			break
		}
	}

	// If no user labels and not already in INBOX, add INBOX label
	hasInbox := false
	for _, label := range result.LabelIds {
		if label == "INBOX" {
			hasInbox = true
			break
		}
	}

	if !hasUserLabel && !hasInbox {
		log.Printf("No user labels applied, adding INBOX label")
		// Add INBOX and UNREAD labels to the message with retry logic
		err := retryOperation(config, func() error {
			modifyReq := &gmail.ModifyMessageRequest{
				AddLabelIds: []string{"INBOX", "UNREAD"},
			}
			_, modifyErr := service.Users.Messages.Modify(config.UserID, result.Id, modifyReq).Do()
			return modifyErr
		}, "add INBOX and UNREAD labels")

		if err != nil {
			return fmt.Errorf("failed to add INBOX and UNREAD labels: %w", err)
		}
		log.Printf("INBOX and UNREAD labels added successfully")
	} else {
		// Even if message has labels or is in INBOX, ensure it's marked UNREAD
		hasUnread := false
		for _, label := range result.LabelIds {
			if label == "UNREAD" {
				hasUnread = true
				break
			}
		}

		if !hasUnread {
			log.Printf("Adding UNREAD label")
			err := retryOperation(config, func() error {
				modifyReq := &gmail.ModifyMessageRequest{
					AddLabelIds: []string{"UNREAD"},
				}
				_, modifyErr := service.Users.Messages.Modify(config.UserID, result.Id, modifyReq).Do()
				return modifyErr
			}, "add UNREAD label")

			if err != nil {
				return fmt.Errorf("failed to add UNREAD label: %w", err)
			}
			log.Printf("UNREAD label added successfully")
		}
	}

	return nil
}
