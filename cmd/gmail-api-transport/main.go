package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"gmail-api-client/internal"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Config holds the application configuration
type Config struct {
	internal.Common
	// Never mark as spam (ignore Gmail spam classifier)
	NotSpam bool `json:"not_spam"`
	// API call timeout in seconds (default: 30)
	APITimeout int `json:"api_timeout"`
	// Overall operation timeout in seconds (default: 120)
	OperationTimeout int `json:"operation_timeout"`
	// Filter processing delay in seconds (default: 2)
	FilterDelay int `json:"filter_delay"`
}

type MessageLogInfo struct {
	From    string
	Subject string
}

const maxLogSubjectRunes = 120

var (
	verbose       bool
	neverMarkSpam bool
	testAPI       bool
	logger        *internal.Logger
)

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Args[0])
		os.Exit(1)
	}

	configFile := os.Args[1]

	if err := parseFlags(os.Args[0], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize logger
	logger = internal.NewLogger(verbose, "gmail-api-transport")
	if verbose {
		logger.SetOutput(os.Stderr)
	}

	logger.Debug("starting gmail-api-transport", "config_file", configFile)

	// Load configuration
	cfg, err := loadConfig(configFile)
	if err != nil {
		logger.Fatal("failed to load config", err)
	}

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		logger.Fatal("invalid configuration", err)
	}

	// Override verbose setting if command line flag is set
	if verbose {
		cfg.Verbose = true
	}

	// Override not-spam setting if command line flag is set
	if neverMarkSpam {
		cfg.NotSpam = true
	}

	logger.Debug("configuration loaded successfully",
		"user_id", cfg.UserID,
		"not_spam", cfg.NotSpam)

	// If test-api mode, just test the API connection and exit
	if testAPI {
		logger.Info("testing Gmail API connection")
		if err := testAPIConnection(ctx, cfg); err != nil {
			logger.Fatal("API test failed", err)
		}
		return
	}

	// Pre-validate and refresh token before reading message from stdin
	// This ensures we don't read and lose a message if auth fails
	logger.Debug("validating OAuth2 token before reading message")
	if err := validateAndRefreshToken(ctx, cfg); err != nil {
		logger.Fatal("token validation failed", err)
	}
	logger.Debug("token validated successfully")

	// Read email message from stdin
	logger.Debug("reading message from stdin")
	message, err := io.ReadAll(os.Stdin)
	if err != nil {
		logger.Fatal("failed to read from stdin", err)
	}

	if len(message) == 0 {
		logger.Fatal("no message received from stdin", nil)
	}

	logger.Debug("message received", "bytes", len(message))

	logInfo := extractMessageLogInfo(message)

	// Deliver message to Gmail
	if err := deliverMessage(ctx, cfg, message); err != nil {
		logger.Fatal("message delivery failed", err)
	}

	// Success message for Exim - first line of stdout
	logger.Success(formatSuccessLogLine(logInfo))
}

func parseFlags(program string, args []string) error {
	flags := flag.NewFlagSet(program, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { printUsage(program) }

	flags.BoolVar(&verbose, "v", false, "Enable verbose logging")
	flags.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flags.BoolVar(&neverMarkSpam, "not-spam", false, "Never mark this message as spam")
	flags.BoolVar(&testAPI, "test-api", false, "Test API connection")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	return nil
}

func printUsage(program string) {
	fmt.Fprintf(os.Stderr, "Usage: %s <config-file> [-v|--verbose] [--not-spam] [--test-api]\n", program)
	fmt.Fprintf(os.Stderr, "\nReads email message from stdin and imports it to Gmail using the API.\n")
	fmt.Fprintf(os.Stderr, "Options:\n")
	fmt.Fprintf(os.Stderr, "  -v, --verbose    Enable verbose logging\n")
	fmt.Fprintf(os.Stderr, "  --not-spam       Never mark this message as spam (only with import)\n")
	fmt.Fprintf(os.Stderr, "  --test-api       Test API connection (shows Gmail language settings)\n")
}

func extractMessageLogInfo(rawMessage []byte) MessageLogInfo {
	info := MessageLogInfo{
		From:    "<unknown>",
		Subject: "",
	}

	msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return info
	}

	if from := msg.Header.Get("From"); from != "" {
		info.From = decodeHeaderValue(from)
	}
	if subject := msg.Header.Get("Subject"); subject != "" {
		info.Subject = truncateRunes(decodeHeaderValue(subject), maxLogSubjectRunes)
	}

	return info
}

func decodeHeaderValue(value string) string {
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func formatSuccessLogLine(info MessageLogInfo) string {
	return fmt.Sprintf("Gmail import succeeded from=%q subject=%q",
		sanitizeLogValue(info.From),
		sanitizeLogValue(info.Subject))
}

func sanitizeLogValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

// loadConfig reads and parses the configuration file
func loadConfig(filename string) (*Config, error) {
	logger.Debug("loading configuration", "file", filename)

	var cfg Config
	if err := internal.LoadJSON(filename, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.UserID == "" {
		cfg.UserID = "me"
		logger.Debug("using default user ID", "user_id", "me")
	}

	// Expand relative paths
	cfg.CredentialsFile = internal.ExpandPath(filename, cfg.CredentialsFile)
	cfg.TokenFile = internal.ExpandPath(filename, cfg.TokenFile)

	logger.Debug("paths expanded",
		"credentials_file", cfg.CredentialsFile,
		"token_file", cfg.TokenFile)

	return &cfg, nil
}

// validateAndRefreshToken validates the token and refreshes it if needed
// This is called before reading message from stdin to avoid losing messages
func validateAndRefreshToken(ctx context.Context, cfg *Config) error {
	logger.Debug("loading and validating OAuth2 token")
	_, _, err := internal.RefreshAndSaveToken(ctx, cfg.CredentialsFile, cfg.TokenFile)
	return err
}

// validateConfig validates the configuration and sets defaults
func validateConfig(cfg *Config) error {
	logger.Debug("validating configuration")

	// Validate common fields
	if err := internal.ValidateCommon(&cfg.Common); err != nil {
		return err
	}

	// Set timeout defaults if not specified
	internal.SetDefaults(&cfg.APITimeout, 30)
	internal.SetDefaults(&cfg.OperationTimeout, 120)
	internal.SetDefaults(&cfg.FilterDelay, 2)

	logger.Debug("defaults applied",
		"api_timeout", cfg.APITimeout,
		"operation_timeout", cfg.OperationTimeout,
		"filter_delay", cfg.FilterDelay,
		"max_retries", cfg.MaxRetries,
		"retry_delay", cfg.RetryDelay)

	// Validate timeout values are reasonable
	if err := internal.ValidateTimeout(cfg.APITimeout, cfg.OperationTimeout); err != nil {
		return err
	}

	// Validate delay
	if err := internal.ValidateDelay(cfg.FilterDelay, 30, "filter_delay"); err != nil {
		return err
	}

	logger.Debug("configuration validated successfully")
	return nil
}

// getGmailService creates and returns a Gmail service client and token source
func getGmailService(parentCtx context.Context, cfg *Config) (*gmail.Service, oauth2.TokenSource, error) {
	logger.Debug("creating Gmail API service")

	// Use shared oauth package to handle token refresh
	_, tokenSource, err := internal.RefreshAndSaveToken(parentCtx, cfg.CredentialsFile, cfg.TokenFile)
	if err != nil {
		return nil, nil, err
	}

	// Create OAuth2 client with background context
	// The token source handles refresh independently
	logger.Debug("creating OAuth2 HTTP client")
	client := oauth2.NewClient(parentCtx, tokenSource)

	// Create Gmail service with timeout context for API operations
	// This timeout applies to API calls, not token refresh
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(cfg.APITimeout)*time.Second)
	defer cancel()

	logger.Debug("initializing Gmail API service")
	service, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, nil, fmt.Errorf("creating Gmail service: %w", err)
	}
	logger.Debug("Gmail API service created successfully")

	return service, tokenSource, nil
}

// testAPIConnection tests the Gmail API connection by calling getLanguage
func testAPIConnection(parentCtx context.Context, cfg *Config) error {
	logger.Debug("creating Gmail API service for testing")

	service, tokenSource, err := getGmailService(parentCtx, cfg)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

	// Defer saving the token only if it changed
	defer func() {
		if token, err := tokenSource.Token(); err == nil {
			if err := internal.SaveTokenIfChanged(cfg.TokenFile, token); err != nil {
				logger.Warn("failed to save token", "error", err)
			}
		}
	}()

	logger.Debug("calling Gmail API users.settings.getLanguage", "user_id", cfg.UserID)
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(cfg.APITimeout)*time.Second)
	defer cancel()
	langSettings, err := service.Users.Settings.GetLanguage(cfg.UserID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("calling getLanguage: %w", err)
	}

	logger.Info("API test successful")
	fmt.Println("\n=== Gmail API Connection Test ===")
	fmt.Println("Status: SUCCESS")
	fmt.Printf("User ID: %s\n", cfg.UserID)
	fmt.Printf("Display Language: %s\n", langSettings.DisplayLanguage)
	fmt.Println("=================================")

	return nil
}

// deliverMessage delivers an email message to Gmail using the Import API
func deliverMessage(parentCtx context.Context, cfg *Config, rawMessage []byte) error {
	logger.Debug("preparing to deliver message")

	service, tokenSource, err := getGmailService(parentCtx, cfg)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

	// Defer saving the token only if it changed
	defer func() {
		if token, err := tokenSource.Token(); err == nil {
			if err := internal.SaveTokenIfChanged(cfg.TokenFile, token); err != nil {
				logger.Warn("failed to save token", "error", err)
			}
		}
	}()

	// Encode message in base64url format (required by Gmail API)
	logger.Debug("encoding message to base64url", "bytes", len(rawMessage))
	encodedMessage := base64.RawURLEncoding.EncodeToString(rawMessage)
	logger.Debug("message encoded", "encoded_bytes", len(encodedMessage))

	// Create the message object without labels - let Gmail apply filters first
	message := &gmail.Message{
		Raw: encodedMessage,
	}

	var result *gmail.Message

	// Wrap the API call in retry logic
	retryCfg := &internal.RetryConfig{
		MaxRetries: cfg.MaxRetries,
		RetryDelay: cfg.RetryDelay,
	}

	operationCtx, cancelOperation := context.WithTimeout(parentCtx, time.Duration(cfg.OperationTimeout)*time.Second)
	defer cancelOperation()

	err = internal.RetryOperationWithContext(operationCtx, retryCfg, logger, func() error {
		logger.Debug("calling Gmail API users.messages.import", "user_id", cfg.UserID)
		if cfg.NotSpam {
			logger.Info("using Import API with neverMarkSpam=true")
		} else {
			logger.Info("using Import API (standard delivery)")
		}

		call := service.Users.Messages.Import(cfg.UserID, message)

		if cfg.NotSpam {
			call = call.NeverMarkSpam(true)
		}

		ctx, cancel := context.WithTimeout(operationCtx, time.Duration(cfg.APITimeout)*time.Second)
		defer cancel()

		var apiErr error
		result, apiErr = call.Context(ctx).Do()
		return apiErr
	}, "message delivery")

	if err != nil {
		return fmt.Errorf("delivering message: %w", err)
	}

	logger.Info("message delivered successfully",
		"message_id", result.Id,
		"thread_id", result.ThreadId)
	if len(result.LabelIds) > 0 {
		logger.Debug("initial labels", "labels", result.LabelIds)
	}

	// Wait for Gmail filters to apply (labels may be applied asynchronously)
	filterDelay := time.Duration(cfg.FilterDelay) * time.Second
	logger.Debug("waiting for Gmail filters to process", "delay", filterDelay)
	select {
	case <-time.After(filterDelay):
	case <-operationCtx.Done():
		return fmt.Errorf("operation timed out while waiting for Gmail filters: %w", operationCtx.Err())
	}

	// Re-fetch the message to get updated labels after filters have run
	// Wrap in retry logic
	err = internal.RetryOperationWithContext(operationCtx, retryCfg, logger, func() error {
		ctx, cancel := context.WithTimeout(operationCtx, time.Duration(cfg.APITimeout)*time.Second)
		defer cancel()

		var fetchErr error
		result, fetchErr = service.Users.Messages.Get(cfg.UserID, result.Id).Format("metadata").Context(ctx).Do()
		return fetchErr
	}, "message re-fetch")

	if err != nil {
		// Non-fatal: continue even if re-fetch fails
		logger.Warn("failed to re-fetch message, continuing with original labels", "error", err)
	} else {
		logger.Debug("labels after filter processing", "labels", result.LabelIds)
	}

	// Attempt to apply labels - failures are non-fatal
	if err := applyLabels(operationCtx, service, cfg, result); err != nil {
		// Log warning but don't fail the delivery
		logger.Warn("label modification had issues", "error", err)
		fmt.Fprintf(os.Stderr, "WARNING: Message delivered but label modification failed: %v\n", err)
	}

	return nil
}

// applyLabels applies INBOX and UNREAD labels as needed
func applyLabels(ctx context.Context, service *gmail.Service, cfg *Config, result *gmail.Message) error {
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
	hasInbox := slices.Contains(result.LabelIds, "INBOX")

	retryCfg := &internal.RetryConfig{
		MaxRetries: cfg.MaxRetries,
		RetryDelay: cfg.RetryDelay,
	}

	if !hasUserLabel && !hasInbox {
		logger.Debug("no user labels applied, adding INBOX label")
		// Add INBOX and UNREAD labels to the message with retry logic
		err := internal.RetryOperationWithContext(ctx, retryCfg, logger, func() error {
			apiCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.APITimeout)*time.Second)
			defer cancel()

			modifyReq := &gmail.ModifyMessageRequest{
				AddLabelIds: []string{"INBOX", "UNREAD"},
			}
			_, modifyErr := service.Users.Messages.Modify(cfg.UserID, result.Id, modifyReq).Context(apiCtx).Do()
			return modifyErr
		}, "add INBOX and UNREAD labels")

		if err != nil {
			return fmt.Errorf("failed to add INBOX and UNREAD labels: %w", err)
		}
		logger.Debug("INBOX and UNREAD labels added successfully")
	} else {
		// Even if message has labels or is in INBOX, ensure it's marked UNREAD
		hasUnread := slices.Contains(result.LabelIds, "UNREAD")

		if !hasUnread {
			logger.Debug("adding UNREAD label")
			err := internal.RetryOperationWithContext(ctx, retryCfg, logger, func() error {
				apiCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.APITimeout)*time.Second)
				defer cancel()

				modifyReq := &gmail.ModifyMessageRequest{
					AddLabelIds: []string{"UNREAD"},
				}
				_, modifyErr := service.Users.Messages.Modify(cfg.UserID, result.Id, modifyReq).Context(apiCtx).Do()
				return modifyErr
			}, "add UNREAD label")

			if err != nil {
				return fmt.Errorf("failed to add UNREAD label: %w", err)
			}
			logger.Debug("UNREAD label added successfully")
		}
	}

	return nil
}
