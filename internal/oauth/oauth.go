package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// LoadToken reads an OAuth2 token from a file
func LoadToken(filename string) (*oauth2.Token, error) {
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

// GetFilePermissions returns the file permissions (mode) of a file
func GetFilePermissions(filename string) (os.FileMode, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return 0, fmt.Errorf("getting file permissions: %w", err)
	}
	return info.Mode().Perm(), nil
}

// acquireFileLock acquires an exclusive lock on a file descriptor
// Returns an error if the lock cannot be acquired within a reasonable time
func acquireFileLock(file *os.File) error {
	// Try to acquire lock with timeout
	maxAttempts := 50
	for i := 0; i < maxAttempts; i++ {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("acquiring file lock: %w", err)
		}
		// Lock is held by another process, wait and retry
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for file lock after %d attempts", maxAttempts)
}

// releaseFileLock releases the lock on a file descriptor
func releaseFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// SaveToken writes an OAuth2 token to a file with specified permissions
// Uses atomic write (write to temp file, then rename) to prevent corruption
// Uses file locking to prevent concurrent write conflicts
func SaveToken(filename string, token *oauth2.Token, perm os.FileMode) error {
	log.Printf("Saving token to: %s", filename)
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}

	// Create temp file in same directory for atomic rename
	dir := filepath.Dir(filename)
	tempFile, err := os.CreateTemp(dir, ".token.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tempName := tempFile.Name()
	
	// Ensure temp file is cleaned up on error
	defer func() {
		if tempFile != nil {
			tempFile.Close()
			os.Remove(tempName)
		}
	}()

	// Acquire exclusive lock on temp file
	if err := acquireFileLock(tempFile); err != nil {
		return fmt.Errorf("locking temp file: %w", err)
	}

	// Write data to temp file
	if _, err := tempFile.Write(data); err != nil {
		releaseFileLock(tempFile)
		return fmt.Errorf("writing temp file: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := tempFile.Sync(); err != nil {
		releaseFileLock(tempFile)
		return fmt.Errorf("syncing temp file: %w", err)
	}

	// Set permissions on temp file
	if err := tempFile.Chmod(perm); err != nil {
		releaseFileLock(tempFile)
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Release lock and close file before rename
	releaseFileLock(tempFile)
	tempFile.Close()
	tempFile = nil // Prevent defer from closing again

	// Atomically rename temp file to target file
	if err := os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	log.Printf("Token saved successfully with permissions %v, expiry: %s", perm, token.Expiry)
	return nil
}

// LoadOAuthConfig reads credentials file and creates an OAuth2 config
func LoadOAuthConfig(credentialsFile string) (*oauth2.Config, error) {
	log.Printf("Reading credentials from: %s", credentialsFile)
	credentials, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}
	log.Printf("Credentials loaded: %d bytes", len(credentials))

	log.Printf("Parsing OAuth2 configuration...")
	// Use gmail.modify scope which includes insert and settings.basic permissions
	oauthConfig, err := google.ConfigFromJSON(credentials, gmail.GmailModifyScope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	log.Printf("OAuth2 config parsed successfully")

	return oauthConfig, nil
}

// CreateTokenSource creates a token source that automatically refreshes tokens
// Uses context.Background() to avoid timeout interference with token refresh
func CreateTokenSource(oauthConfig *oauth2.Config, token *oauth2.Token) oauth2.TokenSource {
	return oauthConfig.TokenSource(context.Background(), token)
}

// RefreshToken gets a fresh token from the token source, refreshing if needed
// Returns the fresh token and whether it was refreshed
func RefreshToken(tokenSource oauth2.TokenSource, originalToken *oauth2.Token) (*oauth2.Token, bool, error) {
	log.Printf("Obtaining fresh token (will refresh if expired)...")
	freshToken, err := tokenSource.Token()
	if err != nil {
		return nil, false, fmt.Errorf("getting fresh token: %w", err)
	}

	wasRefreshed := freshToken.AccessToken != originalToken.AccessToken
	if wasRefreshed {
		log.Printf("Token was refreshed")
	} else {
		log.Printf("Token is still valid")
	}

	return freshToken, wasRefreshed, nil
}

// TokenChanged checks if two tokens are different (different access token or expiry)
func TokenChanged(t1, t2 *oauth2.Token) bool {
	if t1 == nil || t2 == nil {
		return t1 != t2
	}
	return t1.AccessToken != t2.AccessToken || !t1.Expiry.Equal(t2.Expiry)
}

// SaveTokenIfChanged saves a token only if it differs from the original
// Preserves the original file permissions
func SaveTokenIfChanged(filename string, originalToken, currentToken *oauth2.Token) error {
	if !TokenChanged(originalToken, currentToken) {
		log.Printf("Token unchanged, skipping save")
		return nil
	}
	log.Printf("Token changed, saving to file...")
	
	// Get original file permissions
	perm, err := GetFilePermissions(filename)
	if err != nil {
		log.Printf("WARNING: Could not get original permissions, using 0600: %v", err)
		perm = 0600
	}
	
	return SaveToken(filename, currentToken, perm)
}

// RefreshAndSaveToken is a convenience function that refreshes a token and saves it if changed
// Preserves original token file permissions when saving
func RefreshAndSaveToken(credentialsFile, tokenFile string) (*oauth2.Token, oauth2.TokenSource, error) {
	// Load OAuth config
	oauthConfig, err := LoadOAuthConfig(credentialsFile)
	if err != nil {
		return nil, nil, err
	}

	// Load token from file
	log.Printf("Loading OAuth2 token from: %s", tokenFile)
	token, err := LoadToken(tokenFile)
	if err != nil {
		return nil, nil, fmt.Errorf("loading token: %w", err)
	}
	log.Printf("Token loaded, expiry: %s", token.Expiry)

	// Get original file permissions before any modifications
	perm, err := GetFilePermissions(tokenFile)
	if err != nil {
		log.Printf("WARNING: Could not get original permissions, will use 0600: %v", err)
		perm = 0600
	}

	// Create token source
	tokenSource := CreateTokenSource(oauthConfig, token)

	// Get fresh token (auto-refreshes if needed)
	freshToken, wasRefreshed, err := RefreshToken(tokenSource, token)
	if err != nil {
		return nil, nil, err
	}

	// Save if refreshed, using original permissions
	if wasRefreshed {
		log.Printf("Saving refreshed token to file...")
		if err := SaveToken(tokenFile, freshToken, perm); err != nil {
			log.Printf("WARNING: Failed to save refreshed token: %v", err)
		} else {
			log.Printf("Refreshed token saved successfully")
		}
	}

	return freshToken, tokenSource, nil
}
