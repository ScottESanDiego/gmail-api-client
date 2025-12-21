# Gmail API/IMAP Transport

Non-interactive Go programs that read email messages from stdin and deliver them to Gmail. Designed for integration with mail transfer agents like Exim.

## Programs

This repository contains two transport programs:

1. **gmail-api-transport** - Uses the Gmail API for delivery
2. **gmail-imap-transport** - Uses IMAP APPEND for delivery

## Features

### gmail-api-transport
- Reads RFC 822 email messages from stdin
- Uses Gmail API's `users.messages.import` to preserve original headers
- Non-interactive operation using pre-authorized OAuth2 tokens
- Configurable via JSON configuration file
- Supports Insert API (bypass scanning) or Import API (standard delivery)

### gmail-imap-transport
- Reads RFC 822 email messages from stdin
- Uses IMAP APPEND command to deliver messages
- OAuth2 authentication via XOAUTH2 SASL mechanism
- Non-interactive operation using pre-authorized OAuth2 tokens
- Configurable via JSON configuration file
- Gmail automatically applies filters and labels

## Prerequisites

1. **Google Cloud Project**: Create a project in [Google Cloud Console](https://console.cloud.google.com/)
2. **Enable Gmail API**: Enable the Gmail API for your project
3. **OAuth2 Credentials**: Create OAuth2 credentials (Desktop application type)
4. **Download credentials**: Save the credentials JSON file

## Setup

### 1. Install Dependencies

```bash
go mod download
```

### 2. Build the Programs

```bash
# Build the Gmail API transport
go build -o gmail-api-transport cmd/gmail-api-transport/main.go

# Build the Gmail IMAP transport
go build -o gmail-imap-transport cmd/gmail-imap-transport/main.go

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

**For gmail-api-transport:**

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
  "verbose": false
}
```

**For gmail-imap-transport:**

Copy the IMAP example configuration:

```bash
cp imap-config.json.example imap-config.json
```

Edit `imap-config.json` to match your setup (note: user_id must be your full email address):

```json
{
  "credentials_file": "credentials.json",
  "token_file": "token.json",
  "user_id": "your-email@gmail.com",
  "verbose": false,
  "imap_server": "imap.gmail.com:993",
  "connection_timeout": 30
}
```

## Configuration Options

**Common Configuration Options:**
- `credentials_file`: Path to OAuth2 credentials from Google Cloud Console
- `token_file`: Path to the token file created by `gmail-api-transport-get-token`
- `verbose`: Enable verbose logging (can be overridden with `-v` flag)

**gmail-api-transport Specific:**
- `user_id`: Gmail user ID ("me" for authenticated user, or specific email address)
- `not_spam`: Never mark messages as spam - only applies to Import API (can be overridden with `--not-spam` flag)
- `use_insert`: Use Insert API instead of Import API to bypass scanning (can be overridden with `--use-insert` flag)

**gmail-imap-transport Specific:**
- `user_id`: Gmail email address (must be full email, not "me")
- `imap_server`: IMAP server address (default: "imap.gmail.com:993")
- `connection_timeout`: Connection timeout in seconds (default: 30
- `credentials_file`: Path to OAuth2 credentials from Google Cloud Console
- `token_file`: Path to the token file created by `gmail-api-transport-get-token`
- `user_id`: Gmail user ID ("me" for authenticated user, or specific email address)

## Usage

### Basic Usage

**Using Gmail API transport:**

```bash
cat message.eml | ./gmail-api-transport config.json
```

**Using IMAP transport:**

```bash
cat message.eml | ./gmail-imap-transport config.json
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

### Bypass Spam Filter

To ensure messages are never marked as spam (ignoring Gmail's spam classifier):

```bash
cat message.eml | ./gmail-api-transport config.json --not-spam
```

This sets the `neverMarkSpam` parameter in the Gmail API, which tells Gmail to bypass the spam classifier for this message. This is useful for automated mail delivery systems where you trust the source.

### Use Insert API Instead of Import

By default, the program uses the Gmail `import` API which performs standard email delivery scanning and classification similar to SMTP. To bypass most scanning and classification (similar to IMAP APPEND), use the `insert` API:

```bash
cat message.eml | ./gmail-api-transport config.json --use-insert
```

**Key differences:**
- **Import API** (default): Performs standard email delivery scanning and classification, similar to receiving via SMTP
- **Insert API**: Directly inserts messages bypassing most scanning and classification, similar to IMAP APPEND

Note: The `--not-spam` flag only works with the Import API (default). When using `--use-insert`, the Insert API already bypasses spam filtering.

You can combine flags:

```bash
cat message.eml | ./gmail-api-transport config.json --verbose --not-spam
cat message.eml | ./gmail-api-transport config.json --verbose --use-insert
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

You can combine with verbose mode for more details:
```bash:

**Option 1: Using Gmail API transport**

```
# In /etc/exim4/exim4.conf.localmacros or similar

# Transport definition
gmail-api-transport:
  driver = pipe
  command = /path/to/gmail-api-transport /path/to/config.json
  user = mail
  return_fail_output = true
  temp_errors = *
```

**Option 2: Using IMAP transport**

```
# In /etc/exim4/exim4.conf.localmacros or similar

# Transport definition
gmail-imap-transport:
  driver = pipe
  command = /path/to/gmail-imap-transport /path/to/config.json
  user = mail
  return_fail_output = true
  temp_errors = *
```

Then configure a router to use one of these transports:

```
gmail-router:
  driver = accept
  domains = your-domain.com
  local_parts = specific-user
  transport = gmail-api-transport  # or gmail-imap
```
gmail-api-router:
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

4. **Token Refresh**: OAuth2 tokens are automatically refreshed. The token file will be updated, so ensure the process has write access.

## Troubleshooting

### "Failed to load config"
- Verify the config file path is correct
- Check JSON syntax in config file
- Ensure referenced credential and token files exist

### gmail-api-transport: "Failed to import message"
- Verify OAuth2 token is valid and not expired
- Check that Gmail API is enabled in Google Cloud Console
- Ensure the OAuth2 scope includes `https://www.googleapis.com/auth/gmail.modify`
- Check Gmail API quota limits

### gmail-imap-transport: "IMAP authentication failed"
- Verify OAuth2 token is valid and not expired
- Ensure user_id is set to your full email address (not "me")
- Check that IMAP access is enabled in your Gmail settings
- Verify the OAuth2 scope includes `https://mail.google.com/`
- Check your Gmail account allows "less secure apps" or use OAuth2

### gmail-imap-transport: "user_id must be a valid email address"
- IMAP requires the full email address for authentication
- Change `"user_id": "me"` to `"user_id": "your-email@gmail.com"` in your config

### "No message received from stdin"
- Verify data is being piped correctly
- Check Exim logs for pipe transport errors

## OAuth2 Scopes

**gmail-api-transport** requires:
- `https://www.googleapis.com/auth/gmail.modify` - Read, compose, and send emails (includes insert and settings.basic)

**gmail-imap-transport** requires:
- `https://mail.google.com/` - Full mail access via IMAP/SMTP/POP

Note: Both programs use the same OAuth2 token generated by `gmail-api-transport-get-token`, which uses the `gmail.modify` scope.
Choosing Between API and IMAP Transport

**Use gmail-api-transport when:**
- You want more control over delivery (Import vs Insert API)
- You need to bypass spam filtering (`--not-spam` flag)
- You want to skip Gmail's scanning/classification (`--use-insert` flag)
- You prefer REST API over IMAP protocol

**Use gmail-imap-transport when:**
- You prefer standard IMAP protocol
- You want simpler implementation (less complex than API)
- Your OAuth2 scope is `https://mail.google.com/` 
- You want Gmail to automatically apply all filters and labels

**Key Differences:**
- **API Transport**: Uses Gmail REST API, more options for delivery control
- **IMAP Transport**: Uses standard IMAP APPEND, simpler but less control
- Both support OAuth2 authentication
- Both preserve message headers and content
- Both allow Gmail filters to run automatically (unless using Insert API with API transport)

## References

- [Gmail API Documentation](https://developers.google.com/gmail/api)
- [Gmail API - Import Messages](https://developers.google.com/gmail/api/reference/rest/v1/users.messages/import)
- [Gmail IMAP Extensions](https://developers.google.com/workspace/gmail/imap/imap-extensions)
- [OAuth2 for IMAP (XOAUTH2)](https://developers.google.com/workspace/gmail/imap/xoauth2-protocol

## References

- [Gmail API Documentation](https://developers.google.com/gmail/api)
- [Gmail API - Import Messages](https://developers.google.com/gmail/api/reference/rest/v1/users.messages/import)
- [Exim Pipe Transport](https://www.exim.org/exim-html-current/doc/html/spec_html/ch-the_pipe_transport.html)
