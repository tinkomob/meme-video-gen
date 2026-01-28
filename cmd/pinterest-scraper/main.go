package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meme-video-gen/internal/sources"
)

func main() {
	outputDir := flag.String("output", ".", "Output directory for downloaded image")
	configURL := flag.String("config-url", "https://s3.twcstorage.ru/e6aaf273-7eb8d838-9903-4cf7-8d19-1ad785babf91/payload/pinterest_urls.json", "URL to pinterest_urls.json config file")
	flag.Parse()

	// Ensure output directory exists
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Printf("[ERROR] Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[MAIN] 📍 Pinterest Image Scraper")
	fmt.Println("[MAIN] Loading Pinterest URLs from API...")
	boardURL, err := loadRandomURLFromAPI(*configURL)
	if err != nil {
		fmt.Printf("[ERROR] ❌ Failed to load config from API: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[MAIN] ✓ Selected random URL: %s\n", boardURL)
	fmt.Println("[MAIN] Starting Pinterest scrape...")

	// Create a minimal scraper instance for CLI usage
	scraper := &sources.Scraper{}
	asset, err := scraper.ScrapePinterest(context.Background(), boardURL)
	if err != nil {
		fmt.Printf("[ERROR] ❌ Error scraping Pinterest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[MAIN] ✓ Found image URL\n")

	if asset == nil || asset.MediaKey == "" {
		fmt.Printf("[ERROR] ❌ No image URL obtained\n")
		os.Exit(1)
	}

	filename, err := downloadImage(asset.MediaKey, *outputDir)
	if err != nil {
		fmt.Printf("[ERROR] ❌ Error downloading image: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[MAIN] ✓ Image saved to: %s\n", filename)
}

// downloadImage downloads an image from URL and saves it to the specified directory.
func downloadImage(imgURL, outputDir string) (string, error) {
	fmt.Printf("[DOWNLOAD] 📍 Downloading image...\n")
	fmt.Printf("[DOWNLOAD] URL: %s\n", imgURL)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(imgURL)
	if err != nil {
		fmt.Printf("[DOWNLOAD] ❌ Failed to download: %v\n", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[DOWNLOAD] ❌ Bad status code: %d\n", resp.StatusCode)
		return "", fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	// Determine file extension
	ext := ".jpg"
	if strings.Contains(resp.Header.Get("Content-Type"), "png") {
		ext = ".png"
	} else if strings.HasSuffix(imgURL, ".png") {
		ext = ".png"
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(outputDir, fmt.Sprintf("pinterest_%s%s", timestamp, ext))

	// Create file
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("[DOWNLOAD] ❌ Failed to create file: %v\n", err)
		return "", err
	}
	defer file.Close()

	// Copy image data to file
	if _, err := io.Copy(file, resp.Body); err != nil {
		os.Remove(filename)
		fmt.Printf("[DOWNLOAD] ❌ Failed to write image: %v\n", err)
		return "", err
	}

	fmt.Printf("[DOWNLOAD] ✓ Saved as: %s\n", filename)
	return filename, nil
}

// loadURLsFromAPI loads Pinterest URLs from a remote JSON API endpoint.
func loadURLsFromAPI(apiURL string) ([]string, error) {
	fmt.Printf("[API] 📍 Fetching Pinterest URLs from API...\n")
	fmt.Printf("[API] URL: %s\n", apiURL)

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Get(apiURL)
	if err != nil {
		fmt.Printf("[API] ❌ Failed to fetch config: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[API] ❌ API returned status code: %d\n", resp.StatusCode)
		return nil, fmt.Errorf("API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("[API] ❌ Failed to read response: %v\n", err)
		return nil, err
	}

	var urls []string
	if err := json.Unmarshal(body, &urls); err != nil {
		fmt.Printf("[API] ❌ Failed to parse JSON: %v\n", err)
		return nil, err
	}

	if len(urls) == 0 {
		fmt.Println("[API] ❌ No URLs found in API response")
		return nil, fmt.Errorf("no URLs found in API response")
	}

	fmt.Printf("[API] ✓ Loaded %d Pinterest URLs from API\n", len(urls))
	return urls, nil
}

// loadRandomURLFromAPI loads a random Pinterest URL from a remote JSON API endpoint.
func loadRandomURLFromAPI(apiURL string) (string, error) {
	urls, err := loadURLsFromAPI(apiURL)
	if err != nil {
		return "", err
	}

	rand.Seed(time.Now().UnixNano())
	selectedURL := urls[rand.Intn(len(urls))]
	fmt.Printf("[API] ✓ Selected random URL: %s\n", selectedURL)
	return selectedURL, nil
}
