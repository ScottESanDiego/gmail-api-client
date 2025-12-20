package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"

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

	// Set redirect URI to localhost
	config.RedirectURL = "http://localhost:8080/oauth2callback"

	// Get token from web using localhost server
	token := getTokenFromWeb(config)

	// Save token using shared oauth package
	if err := oauth.SaveToken(tokenFile, token); err != nil {
		log.Fatalf("Unable to save token: %v", err)
	}

	fmt.Printf("Token saved to: %s\n", tokenFile)
	fmt.Println("You can now use this token with the gmail-api-transport program.")
}

// getTokenFromWeb requests a token from the web using a localhost callback server
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	// Create a channel to receive the auth code
	codeChan := make(chan string)
	errChan := make(chan error)

	// Start local server to handle OAuth callback
	server := &http.Server{Addr: ":8080"}

	http.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errChan <- fmt.Errorf("no authorization code received")
			fmt.Fprintf(w, "Error: No authorization code received")
			return
		}

		// Send success page
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authorization Successful</title></head>
<body>
<h1>Authorization Successful!</h1>
<p>You can close this window and return to the terminal.</p>
</body>
</html>`)

		codeChan <- code
	})

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server error: %v", err)
		}
	}()

	// Generate auth URL
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	fmt.Println("Opening browser for authorization...")
	fmt.Printf("\nIf the browser doesn't open automatically, visit this URL:\n%s\n\n", authURL)

	// Try to open browser automatically
	openBrowser(authURL)

	// Wait for authorization code or error
	var authCode string
	select {
	case authCode = <-codeChan:
		fmt.Println("Authorization code received!")
	case err := <-errChan:
		log.Fatalf("Error during authorization: %v", err)
	}

	// Shutdown server
	server.Shutdown(context.Background())

	// Exchange authorization code for token
	token, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token: %v", err)
	}

	return token
}

// openBrowser tries to open the URL in the default browser
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		fmt.Printf("Unable to open browser automatically: %v\n", err)
	}
}
