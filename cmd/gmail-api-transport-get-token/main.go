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
		fmt.Fprintf(os.Stderr, "This starts a local web server on port 8080 for the OAuth callback.\n")
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

	// Use localhost redirect URL for production OAuth
	config.RedirectURL = "http://localhost:8080/oauth2callback"

	// Get token using localhost web server callback
	token := getTokenFromWeb(config)

	// Save token using shared oauth package
	if err := oauth.SaveToken(tokenFile, token); err != nil {
		log.Fatalf("Unable to save token: %v", err)
	}

	fmt.Printf("\nToken saved to: %s\n", tokenFile)
	fmt.Println("You can now use this token with the gmail-api-transport program.")
}

// getTokenFromWeb requests a token from the web using a local callback server
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	// Generate auth URL with offline access and force approval prompt
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	// Channels to receive the authorization code or error
	codeChan := make(chan string)
	errChan := make(chan error)

	// Start local HTTP server to receive the callback
	server := &http.Server{Addr: ":8080"}

	http.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errChan <- fmt.Errorf("no authorization code received")
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			return
		}

		// Send success response to browser
		fmt.Fprintf(w, "<html><body><h1>Authorization Successful!</h1><p>You can close this window and return to the terminal.</p></body></html>")

		// Send code to main goroutine
		codeChan <- code
	})

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("failed to start server: %w", err)
		}
	}()

	fmt.Println("=================================================================")
	fmt.Println("Gmail OAuth2 Authorization")
	fmt.Println("=================================================================")
	fmt.Println("\nOpening your browser to authorize the application...")
	fmt.Println("\nIf the browser doesn't open automatically, visit this URL:")
	fmt.Println(authURL)
	fmt.Println()

	// Try to open the browser
	openBrowser(authURL)

	// Wait for authorization code or error
	var authCode string
	select {
	case authCode = <-codeChan:
		fmt.Println("\n✓ Authorization code received!")
	case err := <-errChan:
		log.Fatalf("Error during authorization: %v", err)
	}

	// Shutdown the server
	if err := server.Shutdown(context.Background()); err != nil {
		log.Printf("Warning: error shutting down server: %v", err)
	}

	fmt.Println("Exchanging authorization code for access token...")

	// Exchange authorization code for token
	token, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token: %v", err)
	}

	fmt.Println("✓ Token obtained successfully!")

	return token
}

// openBrowser attempts to open the default browser to the specified URL
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
		log.Printf("Unable to open browser automatically: %v", err)
		log.Println("Please open the URL manually in your browser.")
	}
}
