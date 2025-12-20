package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
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

// getGmailService creates and returns a Gmail service client
func getGmailService(config *Config) (*gmail.Service, error) {
	log.Printf("Creating Gmail API service...")

	// Create context with timeout for API operations
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.APITimeout)*time.Second)
	defer cancel()

	// Read credentials file
	log.Printf("Reading credentials from: %s", config.CredentialsFile)
	credentials, err := os.ReadFile(config.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}
	log.Printf("Credentials loaded: %d bytes", len(credentials))

	// Parse OAuth2 config
	log.Printf("Parsing OAuth2 configuration...")
	// Use gmail.modify scope which includes insert and settings.basic permissions
	oauthConfig, err := google.ConfigFromJSON(credentials, gmail.GmailModifyScope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	log.Printf("OAuth2 config parsed successfully")

	// Load token from file
	log.Printf("Loading OAuth2 token from: %s", config.TokenFile)
	token, err := loadToken(config.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("loading token: %w", err)
	}
	log.Printf("Token loaded, expiry: %s", token.Expiry)

	// Create OAuth2 client
	log.Printf("Creating OAuth2 HTTP client...")
	client := oauthConfig.Client(ctx, token)

	// Create Gmail service
	log.Printf("Initializing Gmail API service...")
	service, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("creating Gmail service: %w", err)
	}
	log.Printf("Gmail API service created successfully")

	return service, nil
}

// loadToken reads an OAuth2 token from a file
func loadToken(filename string) (*oauth2.Token, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading token file: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	return &token, nil
}

// testAPIConnection tests the Gmail API connection by calling getLanguage
func testAPIConnection(config *Config) error {
	log.Printf("Creating Gmail API service for testing...")
	service, err := getGmailService(config)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

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

// deliverMessage delivers an email message to Gmail using either Import or Insert API
func deliverMessage(config *Config, rawMessage []byte) error {
	log.Printf("Preparing to deliver message...")
	service, err := getGmailService(config)
	if err != nil {
		return fmt.Errorf("creating Gmail service: %w", err)
	}

	// Encode message in base64url format (required by Gmail API)
	log.Printf("Encoding message (%d bytes) to base64url...", len(rawMessage))
	encodedMessage := base64.URLEncoding.EncodeToString(rawMessage)
	log.Printf("Encoded message size: %d bytes", len(encodedMessage))

	// Create the message object without labels - let Gmail apply filters first
	message := &gmail.Message{
		Raw: encodedMessage,
	}

	var result *gmail.Message

	if config.UseInsert {
		// Use Insert API - bypasses most scanning and classification (like IMAP APPEND)
		log.Printf("Calling Gmail API users.messages.insert for user: %s", config.UserID)
		log.Printf("Insert bypasses most scanning and classification")

		call := service.Users.Messages.Insert(config.UserID, message).
			InternalDateSource("dateHeader")

		result, err = call.Do()
		if err != nil {
			return fmt.Errorf("inserting message: %w", err)
		}
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

		result, err = call.Do()
		if err != nil {
			return fmt.Errorf("importing message: %w", err)
		}
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
	result, err = service.Users.Messages.Get(config.UserID, result.Id).Format("metadata").Do()
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
		fmt.Fprintf(os.Stderr, "WARNING: Message delivered but label modification failed: %v\\n", err)
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
		// Add INBOX and UNREAD labels to the message
		modifyReq := &gmail.ModifyMessageRequest{
			AddLabelIds: []string{"INBOX", "UNREAD"},
		}
		_, err := service.Users.Messages.Modify(config.UserID, result.Id, modifyReq).Do()
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
			modifyReq := &gmail.ModifyMessageRequest{
				AddLabelIds: []string{"UNREAD"},
			}
			_, err := service.Users.Messages.Modify(config.UserID, result.Id, modifyReq).Do()
			if err != nil {
				return fmt.Errorf("failed to add UNREAD label: %w", err)
			}
			log.Printf("UNREAD label added successfully")
		}
	}

	return nil
}
