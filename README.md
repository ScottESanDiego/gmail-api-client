# Gmail API Transport

Non-interactive Go program that reads email messages from stdin and delivers them to Gmail. Designed for integration with mail transfer agents like Exim.

## Program

This repository contains one transport program:

1. **gmail-api-transport** - Uses the Gmail API for delivery

## Features

- Reads RFC 822 email messages from stdin
- Uses Gmail API's `users.messages.import` to preserve original headers
- Non-interactive operation using pre-authorized OAuth2 tokens
- Configurable via JSON configuration file
- Uses Gmail's Import API for standard delivery scanning and classification
- Automatic retry with exponential backoff for transient failures
- Token validation before message read to prevent message loss
- Concurrent-safe token refresh with file locking

## Prerequisites

1. **Go 1.26 or newer**: Install a current Go toolchain.
2. **Google Cloud Project**: Create a project in [Google Cloud Console](https://console.cloud.google.com/)
3. **Enable Gmail API**: Enable the Gmail API for your project
4. **OAuth2 Credentials**: Create OAuth2 credentials (Desktop application type)
5. **Download credentials**: Save the credentials JSON file

## Setup

### 1. Install Dependencies

```bash
go mod download
```

### 2. Build the Program

```bash
# Build the Gmail API transport
go build -o gmail-api-transport cmd/gmail-api-transport/main.go

# Build the token helper (for initial setup only)
go build -o gmail-api-transport-get-token cmd/gmail-api-transport-get-token/main.go
```

### 3. Obtain OAuth2 Token (One-time Setup)

**Important**: Before running this step, you must configure the OAuth2 redirect URI in Google Cloud Console:

1. Go to [Google Cloud Console - Credentials](https://console.cloud.google.com/apis/credentials)
2. Click on your OAuth 2.0 Client ID
3. Under "Authorized redirect URIs", add: `http://localhost:8080/oauth2callback`
4. Click "Save"

Run the interactive helper to authorize and save your token:

```bash
./gmail-api-transport-get-token credentials.json token.json
```

This will:
1. Start a local web server on port 8080
2. Automatically open your browser to the Google authorization page
3. Wait for you to authorize the application
4. Automatically capture the authorization code when Google redirects back
5. Exchange the code for a token and save it to `token.json`

If the browser doesn't open automatically, copy the URL shown in the terminal and paste it into your browser.

**Important**: Keep `token.json` secure. It provides access to your Gmail account.

### 4. Create Configuration File

Copy the example configuration:

```bash
cp config.json.example config.json
```

Edit `config.json` to match your setup:

```json
{
  "credentials_file": "credentials.json",
  "token_file": "token.json",
  "user_id": "me",
  "verbose": false,
  "not_spam": false,
  "log_message_details": true,
  "api_timeout": 30,
  "operation_timeout": 120,
  "filter_delay": 2,
  "max_retries": 3,
  "retry_delay": 1
}
```

## Configuration Options

- `credentials_file`: Path to OAuth2 credentials from Google Cloud Console
- `token_file`: Path to the token file created by `gmail-api-transport-get-token`
- `verbose`: Enable verbose logging (can be overridden with `-v` flag)
- `max_retries`: Maximum number of retry attempts for transient failures (default: 3)
- `retry_delay`: Initial retry delay in seconds for exponential backoff (default: 1)
- `user_id`: Gmail user ID ("me" for authenticated user, or specific email address)
- `not_spam`: Never mark messages as spam - only applies to Import API (can be overridden with `--not-spam` flag)
- `log_message_details`: Include sanitized sender and subject details in the Exim-visible success log line (default: true when omitted)
- `api_timeout`: Timeout for individual Gmail API calls in seconds (default: 30)
- `operation_timeout`: Overall timeout for the entire operation in seconds (default: 120)
- `filter_delay`: Delay in seconds to wait for Gmail filters to process after message delivery (default: 2)

## Reliability Features

The transport program includes robust reliability features designed for production use with mail transfer agents:

### Automatic Retry with Exponential Backoff
- Automatically retries transient failures (network errors, timeouts, rate limits, server errors)
- Uses exponential backoff algorithm: 1s, 2s, 4s, 8s... (capped at 60 seconds)
- Configurable retry attempts via `max_retries` (default: 3)
- Configurable base delay via `retry_delay` (default: 1 second)
- Smart error classification distinguishes retryable from permanent failures
- All failures exit with code 1 for Exim compatibility

### Structured Logging
- Built-in structured logging with key-value pairs for better debugging
- Verbose mode (`-v` flag) provides detailed operation logs to stderr
- Non-verbose mode minimizes output for production use
- **First line of output is always useful for Exim logging** (success/failure state)
- Error messages to stderr are clear and actionable

When `log_message_details` is true, the success line written to stdout includes the message sender and the first portion of the subject:

```text
Gmail import succeeded from="sender@example.com" subject="Quarterly invoice"
```

When `log_message_details` is false, the success line omits message-specific header values:

```text
Gmail import succeeded
```

The detailed log values come from the message `From` and `Subject` headers. They are MIME-decoded, control characters are converted to spaces, whitespace is collapsed, and values are quoted before logging. The sender is truncated to 160 runes and the subject is truncated to 120 runes.

### Token Validation and Refresh
- OAuth2 token is validated and refreshed **before** reading message from stdin
- Prevents message loss due to expired tokens
- Token files maintain original file permissions when saved
- Automatic token refresh is transparent to the user

### Concurrent-Safe Operation
- File locking prevents token corruption from concurrent invocations
- Atomic write pattern (temp file + rename) prevents partial writes
- Safe for high-volume mail processing with multiple simultaneous deliveries
- Lock acquisition includes timeout to prevent indefinite blocking

### Modular Architecture
- Shared retry logic in `internal/retry.go` for consistency
- Structured logging in `internal/logging.go`
- Configuration validation helpers in `internal/config.go`
- OAuth token handling in `internal/oauth.go`
- Clean separation of concerns for maintainability
- All internal packages consolidated in single directory for simplicity

## Usage

### Basic Usage

```bash
cat message.eml | ./gmail-api-transport config.json
```

### Verbose Mode

Enable verbose logging to see detailed information about the delivery process:

```bash
cat message.eml | ./gmail-api-transport config.json -v
```

Or use the long form:

```bash
cat message.eml | ./gmail-api-transport config.json --verbose
```

Verbose output includes:
- Configuration loading details
- OAuth2 token information
- Message size and encoding details
- Gmail API call progress
- Message ID and thread ID upon successful import
- Retry attempts and backoff delays
- Structured key-value pairs for all operations

In non-verbose mode, only critical errors and the final success/failure message are shown. The **first line of output** is always a clear success or failure message, which is ideal for Exim's log format (Exim only logs the first line of stdout). Set `log_message_details` to `false` if Exim logs should not contain message sender or subject values.

### Bypass Spam Filter

To ensure messages are never marked as spam (ignoring Gmail's spam classifier):

```bash
cat message.eml | ./gmail-api-transport config.json --not-spam
```

This sets the `neverMarkSpam` parameter in the Gmail API, which tells Gmail to bypass the spam classifier for this message. This is useful for automated mail delivery systems where you trust the source.

You can combine flags:

```bash
cat message.eml | ./gmail-api-transport config.json --verbose --not-spam
```

### Test API Connection

To verify that your Gmail API credentials and OAuth token are working correctly without sending a message:

```bash
./gmail-api-transport config.json --test-api
```

This calls the Gmail API `users.settings.getLanguage` endpoint and displays the configured language for your Gmail account. It's useful for:
- Verifying OAuth credentials are valid
- Testing API connectivity
- Confirming token hasn't expired
- Troubleshooting authentication issues

Example output:
```
=== Gmail API Connection Test ===
Status: SUCCESS
User ID: me
Display Language: en
=================================
```

You can combine with verbose mode for more details.

### Integration with Exim

```bash
# In /etc/exim/exim.conf or similar

# Transport definition
gmail-api-transport:
  driver = pipe
  command = /path/to/gmail-api-transport /path/to/config.json
  user = mail
  return_fail_output = true
  temp_errors = *
```

Then configure a router to use this transport:

```
gmail-router:
  driver = accept
  domains = your-domain.com
  local_parts = specific-user
  transport = gmail-api-transport
```

### Testing

Test with a simple message:

```bash
cat << 'EOF' | ./gmail-api-transport config.json
From: sender@example.com
To: recipient@example.com
Subject: Test Message
Date: Thu, 19 Dec 2025 12:00:00 +0000

This is a test message.
EOF
```

## Configuration Options

### credentials_file
Path to the OAuth2 credentials JSON file downloaded from Google Cloud Console.

### token_file
Path to the OAuth2 token file. This file contains the refresh token and access token.

The token file will be automatically refreshed when needed, so ensure the program has write access to this file.

### user_id
- Use `"me"` for the authenticated user's mailbox
- Use a specific email address if your OAuth2 setup has domain-wide delegation

### log_message_details
Controls whether the Exim-visible success log line includes message-specific header values.

- `true`: log `from="..." subject="..."` after sanitizing and truncating those values
- `false`: log only `Gmail import succeeded`

This setting affects the success line written to stdout. Verbose/debug logging may still include operational details intended for troubleshooting.

## Security Considerations

1. **Protect Token File**: The `token.json` file grants access to your Gmail account. Set appropriate permissions:
   ```bash
   chmod 600 token.json
   ```

2. **Secure Credentials**: Similarly, protect your credentials file:
   ```bash
   chmod 600 credentials.json
   ```

3. **Run as Limited User**: When using with Exim, run as a dedicated mail user with minimal privileges.

4. **Log Privacy**: When `log_message_details` is true, Exim logs will include sanitized `From` and `Subject` header values. These values can contain personal data or sensitive business context. Set `log_message_details` to `false` when privacy matters more than per-message log detail.

5. **Token Refresh**: OAuth2 tokens are automatically refreshed. The token file will be updated, so ensure the process has write access.

6. **Permission Preservation**: Token files maintain their original permissions when saved after refresh, respecting system administrator security policies.

7. **Concurrent Access**: File locking ensures safe concurrent access to token files, preventing corruption when multiple processes run simultaneously.


## OAuth2 Scopes

**gmail-api-transport** requires:
- `https://www.googleapis.com/auth/gmail.modify` - Read, compose, and send emails

**Use gmail-api-transport when:**
- You want Gmail Import API delivery with standard scanning and classification
- You need to bypass spam filtering (`--not-spam` flag)
- You want to use Gmail's filters

## References

- [Gmail API Documentation](https://developers.google.com/gmail/api)
- [Gmail API - Import Messages](https://developers.google.com/gmail/api/reference/rest/v1/users.messages/import)
- [Exim Pipe Transport](https://www.exim.org/exim-html-current/doc/html/spec_html/ch-the_pipe_transport.html)
