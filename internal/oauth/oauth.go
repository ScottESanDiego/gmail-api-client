package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

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

// SaveToken writes an OAuth2 token to a file with 0600 permissions
func SaveToken(filename string, token *oauth2.Token) error {
	log.Printf("Saving token to: %s", filename)
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}

	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}

	log.Printf("Token saved successfully, expiry: %s", token.Expiry)
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

// RefreshAndSaveToken is a convenience function that refreshes a token and saves it if changed
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

	// Create token source
	tokenSource := CreateTokenSource(oauthConfig, token)

	// Get fresh token (auto-refreshes if needed)
	freshToken, wasRefreshed, err := RefreshToken(tokenSource, token)
	if err != nil {
		return nil, nil, err
	}

	// Save if refreshed
	if wasRefreshed {
		log.Printf("Saving refreshed token to file...")
		if err := SaveToken(tokenFile, freshToken); err != nil {
			log.Printf("WARNING: Failed to save refreshed token: %v", err)
		} else {
			log.Printf("Refreshed token saved successfully")
		}
	}

	return freshToken, tokenSource, nil
}
