package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"gmail-api-client/internal"

	"github.com/emersion/go-imap/client"
)

// Config holds the application configuration
type Config struct {
	internal.Common
	// IMAP server address (default: imap.gmail.com:993)
	IMAPServer string `json:"imap_server"`
	// Connection timeout in seconds (default: 30)
	ConnectionTimeout int `json:"connection_timeout"`
}

var (
	verbose bool
	logger  *internal.Logger
)

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

	// Initialize logger
	logger = internal.NewLogger(verbose, "gmail-imap-transport")
	if verbose {
		logger.SetOutput(os.Stderr)
	}

	logger.Debug("starting gmail-imap-transport", "config_file", configFile)

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

	logger.Debug("configuration loaded successfully",
		"user_id", cfg.UserID,
		"imap_server", cfg.IMAPServer)

	// Pre-validate and refresh token before reading message from stdin
	// This ensures we don't read and lose a message if auth fails
	logger.Debug("validating OAuth2 token before reading message")
	if err := validateAndRefreshToken(cfg); err != nil {
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

	// Deliver message to Gmail via IMAP
	if err := deliverMessage(cfg, message); err != nil {
		logger.Fatal("message delivery failed", err)
	}

	// Success message for Exim - first line of stdout
	logger.Success("Message delivered successfully to Gmail via IMAP")
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

	if cfg.IMAPServer == "" {
		cfg.IMAPServer = "imap.gmail.com:993"
		logger.Debug("using default IMAP server", "server", cfg.IMAPServer)
	}

	internal.SetDefaults(&cfg.ConnectionTimeout, 30)

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
func validateAndRefreshToken(cfg *Config) error {
	logger.Debug("loading and validating OAuth2 token")

	// Load original token to compare later
	originalToken, err := internal.LoadToken(cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	// Load OAuth config
	oauthConfig, err := internal.LoadOAuthConfig(cfg.CredentialsFile)
	if err != nil {
		return fmt.Errorf("loading OAuth config: %w", err)
	}

	// Create token source
	tokenSource := internal.CreateTokenSource(oauthConfig, originalToken)

	// Get fresh token (auto-refreshes if needed)
	freshToken, wasRefreshed, err := internal.RefreshToken(tokenSource, originalToken)
	if err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}

	// Save if refreshed, preserving original permissions
	if wasRefreshed {
		logger.Debug("token was refreshed, saving to file")
		if err := internal.SaveTokenIfChanged(cfg.TokenFile, originalToken, freshToken); err != nil {
			return fmt.Errorf("saving refreshed token: %w", err)
		}
		logger.Debug("refreshed token saved successfully")
	}

	return nil
}

// validateConfig validates the configuration and sets defaults
func validateConfig(cfg *Config) error {
	logger.Debug("validating configuration")

	// Validate common fields
	if err := internal.ValidateCommon(&cfg.Common); err != nil {
		return err
	}

	logger.Debug("defaults applied",
		"connection_timeout", cfg.ConnectionTimeout,
		"max_retries", cfg.MaxRetries,
		"retry_delay", cfg.RetryDelay)

	logger.Debug("configuration validated successfully")
	return nil
}

// connectIMAP creates and authenticates an IMAP connection to Gmail
func connectIMAP(cfg *Config) (*client.Client, error) {
	logger.Debug("connecting to IMAP server", "server", cfg.IMAPServer)

	// Use shared oauth package to handle token refresh
	freshToken, _, err := internal.RefreshAndSaveToken(cfg.CredentialsFile, cfg.TokenFile)
	if err != nil {
		return nil, err
	}

	// Connect to Gmail IMAP server with TLS and timeout
	timeout := time.Duration(cfg.ConnectionTimeout) * time.Second

	// Create a dialer with timeout
	dialer := &net.Dialer{
		Timeout: timeout,
	}

	// Dial with timeout
	conn, err := dialer.Dial("tcp", cfg.IMAPServer)
	if err != nil {
		return nil, fmt.Errorf("connecting to IMAP server: %w", err)
	}

	// Upgrade to TLS connection
	c, err := client.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("creating IMAP client: %w", err)
	}

	// Start TLS
	if err := c.StartTLS(nil); err != nil {
		c.Logout()
		return nil, fmt.Errorf("starting TLS: %w", err)
	}

	logger.Debug("connected to IMAP server")

	// Determine the username (email address)
	username := cfg.UserID
	if username == "me" {
		// We need the actual email address for XOAUTH2
		// Try to extract from credentials or token file
		// For now, we'll require the user to specify it
		c.Logout()
		return nil, fmt.Errorf("user_id must be a valid email address (not 'me') for IMAP authentication")
	}

	// Authenticate using XOAUTH2 with the fresh token
	logger.Debug("authenticating as", "username", username)
	auth := &XOAuth2{
		Username: username,
		Token:    freshToken.AccessToken,
	}

	if err := c.Authenticate(auth); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP authentication failed: %w", err)
	}

	logger.Debug("successfully authenticated to IMAP server")
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
		logger.Debug("received authentication challenge, sending empty response")
		return []byte{}, nil
	}
	return nil, fmt.Errorf("unexpected server challenge")
}

// deliverMessage delivers an email message to Gmail using IMAP APPEND
func deliverMessage(cfg *Config, rawMessage []byte) error {
	logger.Debug("preparing to deliver message via IMAP")

	var c *client.Client
	var err error

	// Wrap the entire delivery operation in retry logic
	retryCfg := &internal.RetryConfig{
		MaxRetries: cfg.MaxRetries,
		RetryDelay: cfg.RetryDelay,
	}

	err = internal.RetryOperation(retryCfg, logger, func() error {
		// Connect and authenticate to IMAP
		c, err = connectIMAP(cfg)
		if err != nil {
			return err
		}

		// Parse the message to extract the date (optional, for INTERNALDATE)
		// For simplicity, we'll use the current time
		internalDate := time.Now()
		logger.Debug("using internal date", "date", internalDate.Format(time.RFC3339))

		// APPEND the message to INBOX with \Seen flag unset (mark as unread)
		// Gmail will apply filters and labels automatically
		flags := []string{} // No flags = unread
		mailbox := "INBOX"

		logger.Debug("appending message to mailbox",
			"mailbox", mailbox,
			"bytes", len(rawMessage),
			"flags", flags)

		// Create a literal from the raw message
		literal := &imapLiteral{data: rawMessage}

		appendErr := c.Append(mailbox, flags, internalDate, literal)
		if appendErr != nil {
			// Close connection on error before potential retry
			c.Logout()
			return fmt.Errorf("IMAP APPEND failed: %w", appendErr)
		}

		logger.Info("message successfully appended", "mailbox", mailbox)
		logger.Debug("Gmail will apply filters and labels automatically")

		// Logout cleanly after successful delivery
		c.Logout()
		return nil
	}, "IMAP message delivery")

	return err
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
