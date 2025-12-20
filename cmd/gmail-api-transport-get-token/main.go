package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"gmail-api-client/internal/oauth"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// This is a helper tool to obtain OAuth2 tokens for the gmail-api-transport.
// Run this interactively to authorize the application and save the token.
//
// Usage: go run get_token.go <credentials.json> <token.json>

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <credentials.json> <token.json>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nInteractive OAuth2 flow to obtain and save a token.\n")
		fmt.Fprintf(os.Stderr, "This uses the headless/manual authorization flow.\n")
		os.Exit(1)
	}

	credentialsFile := os.Args[1]
	tokenFile := os.Args[2]

	// Read credentials
	credentials, err := os.ReadFile(credentialsFile)
	if err != nil {
		log.Fatalf("Unable to read credentials file: %v", err)
	}

	// Parse OAuth2 config with required scopes
	// gmail.modify includes both insert and settings.basic permissions
	config, err := google.ConfigFromJSON(credentials, gmail.GmailModifyScope)
	if err != nil {
		log.Fatalf("Unable to parse credentials: %v", err)
	}

	// Use out-of-band (OOB) redirect for headless authorization
	// This causes Google to display the code on a page for manual entry
	config.RedirectURL = "urn:ietf:wg:oauth:2.0:oob"

	// Get token using manual authorization code entry
	token := getTokenFromWeb(config)

	// Save token using shared oauth package
	if err := oauth.SaveToken(tokenFile, token); err != nil {
		log.Fatalf("Unable to save token: %v", err)
	}

	fmt.Printf("\nToken saved to: %s\n", tokenFile)
	fmt.Println("You can now use this token with the gmail-api-transport program.")
}

// getTokenFromWeb requests a token from the web using manual authorization code entry
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	// Generate auth URL with offline access and force approval prompt
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	fmt.Println("=================================================================")
	fmt.Println("Gmail OAuth2 Authorization")
	fmt.Println("=================================================================")
	fmt.Println("\nPlease visit the following URL in your web browser:")
	fmt.Println("(You can do this on any device - phone, tablet, another computer)")
	fmt.Println()
	fmt.Println(authURL)
	fmt.Println()
	fmt.Println("After authorization, Google will display an authorization code.")
	fmt.Print("\nEnter the authorization code here: ")

	// Read authorization code from user
	reader := bufio.NewReader(os.Stdin)
	authCode, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}
	authCode = strings.TrimSpace(authCode)

	if authCode == "" {
		log.Fatal("No authorization code provided")
	}

	fmt.Println("\nExchanging authorization code for access token...")

	// Exchange authorization code for token
	token, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token: %v", err)
	}

	fmt.Println("✓ Token obtained successfully!")

	return token
}
