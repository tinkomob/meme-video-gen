package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

// TokenData represents the OAuth2 token in JSON format
type TokenData struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	Expiry       string   `json:"expiry,omitempty"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
}

func main() {
	tokenPath := flag.String("token", "token.json", "Path to save token.json")
	credentialsPath := flag.String("credentials", "client_secrets.json", "Path to client_secrets.json")
	flag.Parse()

	fmt.Println("🔐 YouTube Token Generator")
	fmt.Println("========================================")
	fmt.Println()

	// Check if credentials file exists
	if _, err := os.Stat(*credentialsPath); os.IsNotExist(err) {
		fmt.Printf("❌ Credentials file not found: %s\n", *credentialsPath)
		fmt.Println("   Download from https://console.cloud.google.com/")
		fmt.Println("   1. Go to Google Cloud Console")
		fmt.Println("   2. Create OAuth 2.0 credentials (Desktop app)")
		fmt.Println("   3. Download JSON file and rename to client_secrets.json")
		os.Exit(1)
	}

	fmt.Printf("📝 Using credentials: %s\n", *credentialsPath)
	fmt.Printf("💾 Token will be saved to: %s\n", *tokenPath)
	fmt.Println()

	// YouTube scopes
	scopes := []string{
		youtube.YoutubeUploadScope,
		youtube.YoutubeScope,
	}

	ctx := context.Background()

	// Read client secrets
	b, err := os.ReadFile(*credentialsPath)
	if err != nil {
		fmt.Printf("❌ Failed to read credentials: %v\n", err)
		os.Exit(1)
	}

	// Create OAuth2 config
	config, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		fmt.Printf("❌ Failed to create config: %v\n", err)
		os.Exit(1)
	}

	// Generate auth URL
	authURL := config.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	fmt.Println("🔐 Starting authentication process...")
	fmt.Println()
	fmt.Println("📱 Open this URL in your browser:")
	fmt.Printf("   %s\n", authURL)
	fmt.Println()
	fmt.Println("After authorization, paste the authorization code:")
	fmt.Print("👉 Code: ")

	var authCode string
	_, err = fmt.Scanln(&authCode)
	if err != nil {
		fmt.Printf("❌ Failed to read auth code: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("⏳ Exchanging code for token...")

	// Exchange authorization code for token
	token, err := config.Exchange(ctx, authCode)
	if err != nil {
		fmt.Printf("❌ Failed to exchange token: %v\n", err)
		os.Exit(1)
	}

	// Save token as JSON
	tokenData := TokenData{
		Token:        token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry.String(),
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
	}

	tokenJSON, err := json.MarshalIndent(tokenData, "", "  ")
	if err != nil {
		fmt.Printf("❌ Failed to marshal token: %v\n", err)
		os.Exit(1)
	}

	// Create directory if needed
	dir := filepath.Dir(*tokenPath)
	if dir != "." && dir != "" {
		os.MkdirAll(dir, 0755)
	}

	err = os.WriteFile(*tokenPath, tokenJSON, 0600)
	if err != nil {
		fmt.Printf("❌ Failed to save token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Token successfully saved: %s\n", *tokenPath)
	fmt.Println()

	// Get account info
	fmt.Println("📺 Fetching channel information...")

	client := config.Client(ctx, token)
	youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Printf("⚠️  Could not verify channel (will still work): %v\n", err)
		os.Exit(0)
	}

	channelsCall := youtubeService.Channels.List([]string{"snippet"}).Mine(true)
	channels, err := channelsCall.Do()
	if err != nil {
		fmt.Printf("⚠️  Could not fetch channel info: %v\n", err)
		os.Exit(0)
	}

	if len(channels.Items) > 0 {
		channel := channels.Items[0]
		fmt.Printf("✅ Channel: %s\n", channel.Snippet.Title)
		fmt.Printf("   ID: %s\n", channel.Id)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("1. Upload %s to S3 in bot-uploads/ directory\n", *tokenPath)
	fmt.Println("2. Update code to use token.json instead of token.pickle")
	fmt.Println()
}
