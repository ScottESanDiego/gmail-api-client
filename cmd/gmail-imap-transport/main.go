package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"gmail-api-client/internal/oauth"

	"github.com/emersion/go-imap/client"
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
	// IMAP server address (default: imap.gmail.com:993)
	IMAPServer string `json:"imap_server"`
	// Connection timeout in seconds (default: 30)
	ConnectionTimeout int `json:"connection_timeout"`
}

var verbose bool

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <config-file> [-v|--verbose]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nReads email message from stdin and delivers it to Gmail using IMAP APPEND.\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -v, --verbose    Enable verbose logging\n")
		os.Exit(1)
	}

	configFile := os.Args[1]

	// Check for verbose flag
	for _, arg := range os.Args[2:] {
		if arg == "-v" || arg == "--verbose" {
			verbose = true
		}
	}

	// Setup logging
	log.SetFlags(log.LstdFlags)
	if !verbose {
		log.SetOutput(io.Discard)
	}

	log.Printf("Starting gmail-imap-transport")
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

	log.Printf("Configuration loaded successfully")
	log.Printf("  User ID: %s", config.UserID)
	log.Printf("  IMAP Server: %s", config.IMAPServer)
	log.Printf("  Token file: %s", config.TokenFile)

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

	// Deliver message to Gmail via IMAP
	if err := deliverMessage(config, message); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to deliver message: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Message successfully delivered to Gmail")
	fmt.Println("Message successfully delivered to Gmail via IMAP")
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

	if config.IMAPServer == "" {
		config.IMAPServer = "imap.gmail.com:993"
		log.Printf("Using default IMAP server: %s", config.IMAPServer)
	}

	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 30
		log.Printf("Using default connection timeout: %d seconds", config.ConnectionTimeout)
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

	log.Printf("Configuration validated successfully")
	return nil
}

// connectIMAP creates and authenticates an IMAP connection to Gmail
func connectIMAP(config *Config) (*client.Client, error) {
	log.Printf("Connecting to IMAP server: %s", config.IMAPServer)

	// Use shared oauth package to handle token refresh
	freshToken, _, err := oauth.RefreshAndSaveToken(config.CredentialsFile, config.TokenFile)
	if err != nil {
		return nil, err
	}

	// Connect to Gmail IMAP server with TLS
	c, err := client.DialTLS(config.IMAPServer, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to IMAP server: %w", err)
	}

	log.Printf("Connected to IMAP server")

	// Determine the username (email address)
	username := config.UserID
	if username == "me" {
		// We need the actual email address for XOAUTH2
		// Try to extract from credentials or token file
		// For now, we'll require the user to specify it
		c.Logout()
		return nil, fmt.Errorf("user_id must be a valid email address (not 'me') for IMAP authentication")
	}

	// Authenticate using XOAUTH2 with the fresh token
	log.Printf("Authenticating as: %s", username)
	auth := &XOAuth2{
		Username: username,
		Token:    freshToken.AccessToken,
	}

	if err := c.Authenticate(auth); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP authentication failed: %w", err)
	}

	log.Printf("Successfully authenticated to IMAP server")
	return c, nil
}

// XOAuth2 implements the SASL XOAUTH2 authentication mechanism
type XOAuth2 struct {
	Username string
	Token    string
}

// Start implements sasl.Client interface
func (a *XOAuth2) Start() (mech string, ir []byte, err error) {
	mech = "XOAUTH2"
	authString := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.Username, a.Token)
	ir = []byte(base64.StdEncoding.EncodeToString([]byte(authString)))
	return
}

// Next implements sasl.Client interface
func (a *XOAuth2) Next(challenge []byte) (response []byte, err error) {
	// If we receive a challenge (error response), send empty response
	if len(challenge) > 0 {
		log.Printf("Received authentication challenge (likely error), sending empty response")
		return []byte{}, nil
	}
	return nil, fmt.Errorf("unexpected server challenge")
}

// deliverMessage delivers an email message to Gmail using IMAP APPEND
func deliverMessage(config *Config, rawMessage []byte) error {
	log.Printf("Preparing to deliver message via IMAP...")

	// Connect and authenticate to IMAP
	c, err := connectIMAP(config)
	if err != nil {
		return err
	}
	defer c.Logout()

	// Parse the message to extract the date (optional, for INTERNALDATE)
	// For simplicity, we'll use the current time
	internalDate := time.Now()
	log.Printf("Using internal date: %s", internalDate.Format(time.RFC3339))

	// APPEND the message to INBOX with \Seen flag unset (mark as unread)
	// Gmail will apply filters and labels automatically
	flags := []string{} // No flags = unread
	mailbox := "INBOX"

	log.Printf("Appending message to mailbox: %s", mailbox)
	log.Printf("Message size: %d bytes", len(rawMessage))
	log.Printf("Flags: %v (empty = unread)", flags)

	// Create a literal from the raw message
	literal := &imapLiteral{data: rawMessage}

	if err := c.Append(mailbox, flags, internalDate, literal); err != nil {
		return fmt.Errorf("IMAP APPEND failed: %w", err)
	}

	log.Printf("Message successfully appended to %s", mailbox)
	log.Printf("Gmail will apply filters and labels automatically")

	return nil
}

// imapLiteral implements the imap.Literal interface
type imapLiteral struct {
	data []byte
	pos  int
}

func (l *imapLiteral) Len() int {
	return len(l.data)
}

func (l *imapLiteral) Read(p []byte) (n int, err error) {
	if l.pos >= len(l.data) {
		return 0, io.EOF
	}
	n = copy(p, l.data[l.pos:])
	l.pos += n
	return n, nil
}
