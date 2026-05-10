package internal

import (
	"context"
	"encoding/json"
	"fmt"
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

func tokenLockFile(filename string) string {
	return filename + ".lock"
}

func withTokenFileLock(filename string, fn func() error) error {
	lockFile, err := os.OpenFile(tokenLockFile(filename), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening token lock file: %w", err)
	}
	defer lockFile.Close()

	if err := acquireFileLock(lockFile); err != nil {
		return err
	}
	defer releaseFileLock(lockFile)

	return fn()
}

// SaveToken writes an OAuth2 token to a file with specified permissions.
// Uses atomic write (write to temp file, then rename) and a stable token lock.
func SaveToken(filename string, token *oauth2.Token, perm os.FileMode) error {
	return withTokenFileLock(filename, func() error {
		return saveTokenUnlocked(filename, token, perm)
	})
}

func saveTokenUnlocked(filename string, token *oauth2.Token, perm os.FileMode) error {
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

	// Write data to temp file
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}

	// Set permissions on temp file
	if err := tempFile.Chmod(perm); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Close file before rename
	tempFile.Close()
	tempFile = nil // Prevent defer from closing again

	// Atomically rename temp file to target file
	if err := os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// LoadOAuthConfig reads credentials file and creates an OAuth2 config
func LoadOAuthConfig(credentialsFile string) (*oauth2.Config, error) {
	credentials, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	// Use gmail.modify scope, which supports message import and label modification.
	oauthConfig, err := google.ConfigFromJSON(credentials, gmail.GmailModifyScope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	return oauthConfig, nil
}

// CreateTokenSource creates a token source that automatically refreshes tokens.
func CreateTokenSource(ctx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) oauth2.TokenSource {
	return oauthConfig.TokenSource(ctx, token)
}

// RefreshToken gets a fresh token from the token source, refreshing if needed
// Returns the fresh token and whether it was refreshed
func RefreshToken(tokenSource oauth2.TokenSource, originalToken *oauth2.Token) (*oauth2.Token, bool, error) {
	freshToken, err := tokenSource.Token()
	if err != nil {
		return nil, false, fmt.Errorf("getting fresh token: %w", err)
	}

	wasRefreshed := freshToken.AccessToken != originalToken.AccessToken
	return freshToken, wasRefreshed, nil
}

// TokenChanged checks if two tokens are different (different access token or expiry)
func TokenChanged(t1, t2 *oauth2.Token) bool {
	if t1 == nil || t2 == nil {
		return t1 != t2
	}
	return t1.AccessToken != t2.AccessToken || !t1.Expiry.Equal(t2.Expiry)
}

// SaveTokenIfChanged saves a token only if it differs from the current token file.
func SaveTokenIfChanged(filename string, currentToken *oauth2.Token) error {
	if currentToken == nil {
		return nil
	}

	return withTokenFileLock(filename, func() error {
		fileToken, err := LoadToken(filename)
		if err != nil {
			return fmt.Errorf("loading token: %w", err)
		}
		if !TokenChanged(fileToken, currentToken) {
			return nil
		}

		tokenToSave := mergeRefreshToken(fileToken, currentToken)
		if fileToken.Expiry.After(tokenToSave.Expiry) {
			return nil
		}

		perm, err := GetFilePermissions(filename)
		if err != nil {
			perm = 0600
		}

		return saveTokenUnlocked(filename, tokenToSave, perm)
	})
}

// RefreshAndSaveToken is a convenience function that refreshes a token and saves it if changed
// Preserves original token file permissions when saving
func RefreshAndSaveToken(ctx context.Context, credentialsFile, tokenFile string) (*oauth2.Token, oauth2.TokenSource, error) {
	// Load OAuth config
	oauthConfig, err := LoadOAuthConfig(credentialsFile)
	if err != nil {
		return nil, nil, err
	}

	var freshToken *oauth2.Token
	var tokenSource oauth2.TokenSource

	err = withTokenFileLock(tokenFile, func() error {
		// Load token from file
		token, err := LoadToken(tokenFile)
		if err != nil {
			return fmt.Errorf("loading token: %w", err)
		}

		// Get original file permissions before any modifications
		perm, err := GetFilePermissions(tokenFile)
		if err != nil {
			perm = 0600
		}

		// Create token source
		tokenSource = CreateTokenSource(ctx, oauthConfig, token)

		// Get fresh token (auto-refreshes if needed)
		refreshedToken, wasRefreshed, err := RefreshToken(tokenSource, token)
		if err != nil {
			return err
		}

		freshToken = mergeRefreshToken(token, refreshedToken)

		// Save if refreshed, using original permissions
		if wasRefreshed {
			if err := saveTokenUnlocked(tokenFile, freshToken, perm); err != nil {
				return fmt.Errorf("saving refreshed token: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return freshToken, tokenSource, nil
}

func mergeRefreshToken(fileToken, currentToken *oauth2.Token) *oauth2.Token {
	if currentToken == nil {
		return nil
	}
	merged := *currentToken
	if merged.RefreshToken == "" && fileToken != nil {
		merged.RefreshToken = fileToken.RefreshToken
	}
	return &merged
}
