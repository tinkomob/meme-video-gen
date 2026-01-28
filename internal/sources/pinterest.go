package sources

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly"
	"github.com/gocolly/colly/extensions"

	"meme-video-gen/internal/model"
)

// ScrapePinterest extracts a single high-quality image from a Pinterest board URL.
func (sc *Scraper) ScrapePinterest(ctx context.Context, boardURL string) (*model.SourceAsset, error) {
	sc.logf("[SCRAPER] 📍 Attempting Chrome-based scraping (with JavaScript)...")

	// Try Chrome-based scraping first
	imgURL, err := sc.scrapePinterestChrome(boardURL)
	if err == nil && imgURL != "" {
		sc.logf("[SCRAPER] ✓ Chrome scraping successful")
		return &model.SourceAsset{
			ID:        fmt.Sprintf("pinterest-%d", time.Now().UnixNano()),
			Kind:      model.SourceKindPinterest,
			SourceURL: boardURL,
			MediaKey:  imgURL,
			MimeType:  "image/jpeg",
			AddedAt:   time.Now(),
		}, nil
	}

	if err != nil {
		sc.logf("[SCRAPER] ⚠️ Chrome scraping failed: %v", err)
	} else {
		sc.logf("[SCRAPER] ⚠️ Chrome returned no image")
	}

	sc.logf("[SCRAPER] Attempting fallback Colly scraping (HTTP-only)...")
	return sc.scrapePinterestColly(ctx, boardURL)
}

// scrapePinterestChrome uses headless Chrome to properly load JavaScript-rendered content
func (sc *Scraper) scrapePinterestChrome(boardURL string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"),
	)

	sc.logf("[CHROME] Starting Chrome instance...")
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	pinHref := ""

	sc.logf("[CHROME] Navigating to board: %s", boardURL)
	err := chromedp.Run(ctx,
		chromedp.Navigate(boardURL),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(`window.scrollTo(0, Math.random() * Math.max(document.body.scrollHeight, 2000))`, nil),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(`
			(function() {
				const links = Array.from(document.querySelectorAll('a[href*="/pin/"]'));
				console.log('[DEBUG] Total pin links found:', links.length);
				if (links.length > 0) {
					const randomPin = links[Math.floor(Math.random() * links.length)];
					const href = randomPin.href || randomPin.getAttribute('href');
					console.log('[DEBUG] Selected random pin:', href);
					return href;
				}
				return null;
			})()
		`, &pinHref),
	)

	if err != nil {
		sc.logf("[CHROME] ❌ Failed to navigate board: %v", err)
		return "", fmt.Errorf("chrome scraping failed: %w", err)
	}

	if pinHref != "" {
		sc.logf("[CHROME] ✓ Found pin link: %s", pinHref)
		imgURL, err := sc.scrapePinPage(ctx, pinHref)
		if err == nil && imgURL != "" {
			sc.logf("[CHROME] ✓ Successfully got image from pin page")
			return imgURL, nil
		}
		sc.logf("[CHROME] ⚠ Failed to scrape pin page: %v, falling back to board images", err)
	} else {
		sc.logf("[CHROME] ⚠ No pin links found on board")
	}

	sc.logf("[CHROME] Falling back to board image scraping...")
	return sc.scrapeBoardImages(ctx)
}

// scrapePinPage navigates to individual pin page and extracts high-quality image
func (sc *Scraper) scrapePinPage(ctx context.Context, pinURL string) (string, error) {
	sc.logf("[PIN] Navigating to pin page...")

	var imgURL string
	var debugInfo map[string]interface{}

	err := chromedp.Run(ctx,
		chromedp.Navigate(pinURL),
		chromedp.Sleep(4*time.Second),
		chromedp.Evaluate(`
			(function() {
				console.log('[DEBUG] Starting image extraction from pin page');
				
				const closeupBody = document.querySelector('.closeup-body-style');
				if (closeupBody) {
					console.log('[DEBUG] Found .closeup-body-style container');
					const allImages = Array.from(closeupBody.querySelectorAll('img[src*="pinimg.com"]'));
					console.log('[DEBUG] Images in closeup-body-style:', allImages.length);
					
					const highRes = allImages.filter(img => {
						const src = img.src || '';
						return !src.match(/\/75x75/) && 
							   !src.match(/\/200x/) && 
							   !src.match(/\/236x/) &&
							   src.length > 80;
					});
					
					console.log('[DEBUG] High-res images in closeup:', highRes.length);
					
					if (highRes.length > 0) {
						highRes.sort((a, b) => {
							const aScore = (a.src.includes('736x') ? 3000 : 0) + 
										   (a.src.includes('originals') ? 2000 : 0) + 
										   (a.src.includes('600x') ? 1500 : 0) +
										   (a.src || '').length;
							const bScore = (b.src.includes('736x') ? 3000 : 0) + 
										   (b.src.includes('originals') ? 2000 : 0) + 
										   (b.src.includes('600x') ? 1500 : 0) +
										   (b.src || '').length;
							return bScore - aScore;
						});
						console.log('[DEBUG] Selected high-res from closeup:', highRes[0].src);
						return {
							url: highRes[0].src,
							source: 'closeup-high-res'
						};
					}
				}

				const highResSelectors = [
					'img[src*="736x"]',
					'img[src*="originals"]',
					'img[src*="600x"]'
				];

				for (const selector of highResSelectors) {
					console.log('[DEBUG] Trying high-res selector:', selector);
					const img = document.querySelector(selector);
					if (img && img.src && img.src.includes('pinimg.com')) {
						console.log('[DEBUG] Found image with high-res selector:', selector, img.src);
						return {
							url: img.src,
							source: selector
						};
					}
				}

				const allImages = Array.from(document.querySelectorAll('img[src*="pinimg.com"]'));
				console.log('[DEBUG] Total pinimg.com images found:', allImages.length);
				
				if (allImages.length > 0) {
					const filtered = allImages.filter(img => {
						const src = img.src || '';
						return !src.match(/\/75x75/) && 
							   !src.match(/\/200x/) && 
							   !src.match(/\/236x/) &&
							   src.length > 80;
					});
					
					console.log('[DEBUG] Filtered non-thumbnail images:', filtered.length);
					
					if (filtered.length > 0) {
						filtered.sort((a, b) => {
							const aScore = (a.src.includes('736x') ? 3000 : 0) + 
										   (a.src.includes('originals') ? 2000 : 0) + 
										   (a.src.includes('600x') ? 1500 : 0) +
										   (a.src || '').length;
							const bScore = (b.src.includes('736x') ? 3000 : 0) + 
										   (b.src.includes('originals') ? 2000 : 0) + 
										   (b.src.includes('600x') ? 1500 : 0) +
										   (b.src || '').length;
							return bScore - aScore;
						});
						console.log('[DEBUG] Selected best quality:', filtered[0].src);
						return {
							url: filtered[0].src,
							source: 'quality-filtered'
						};
					}
					
					console.log('[DEBUG] WARNING: Using thumbnail as last resort:', allImages[0].src);
					return {
						url: allImages[0].src,
						source: 'thumbnail-fallback'
					};
				}

				console.log('[DEBUG] No images found at all');
				return {
					url: null,
					source: 'none'
				};
			})()
		`, &debugInfo),
	)

	if err != nil {
		sc.logf("[PIN] ❌ Error navigating pin page: %v", err)
		return "", fmt.Errorf("failed to scrape pin page: %w", err)
	}

	if debugInfo != nil {
		if source, ok := debugInfo["source"].(string); ok {
			sc.logf("[PIN] 📍 Image source: %s", source)
		}
		if url, ok := debugInfo["url"].(string); ok && url != "" {
			imgURL = url
		}
	}

	if imgURL == "" {
		sc.logf("[PIN] ❌ No image URL extracted from pin page")
		return "", fmt.Errorf("no image found on pin page")
	}

	sc.logf("[PIN] ✓ Found image URL (length: %d)", len(imgURL))

	if !strings.Contains(imgURL, "?") {
		sc.logf("[PIN] Adding quality parameters: fit=1200x1200")
		imgURL += "?fit=1200x1200"
	}

	sc.logf("[PIN] ✓ Successfully extracted image from pin page")
	return imgURL, nil
}

// scrapeBoardImages scrapes images directly from the board
func (sc *Scraper) scrapeBoardImages(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var imageURLs []interface{}
	sc.logf("[BOARD] Attempting to scrape images from board...")
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				const images = Array.from(document.querySelectorAll('img[src*="pinimg.com"]'));
				console.log('[DEBUG] Found', images.length, 'pinimg images on board');
				return images.map(img => ({
					src: img.src || img.getAttribute('src'),
					alt: img.alt || img.getAttribute('alt')
				})).filter(img => img.src && img.src.length > 50);
			})()
		`, &imageURLs),
	)

	if err != nil {
		sc.logf("[BOARD] ❌ Failed to evaluate images: %v", err)
		return "", fmt.Errorf("failed to get board images: %w", err)
	}

	sc.logf("[BOARD] Found %d pinimg images", len(imageURLs))
	if len(imageURLs) == 0 {
		sc.logf("[BOARD] ❌ No images found on board")
		return "", fmt.Errorf("no images found on board")
	}

	randomIdx := rand.Intn(len(imageURLs))
	selectedImg := imageURLs[randomIdx].(map[string]interface{})
	imgURL := selectedImg["src"].(string)

	if !strings.Contains(imgURL, "fit=") {
		sc.logf("[BOARD] Adding quality parameters: fit=1200x1200")
		imgURL += "?fit=1200x1200"
	}

	sc.logf("[BOARD] ✓ Selected image %d/%d", randomIdx+1, len(imageURLs))
	return imgURL, nil
}

// scrapePinterestColly uses Colly as fallback method
func (sc *Scraper) scrapePinterestColly(ctx context.Context, boardURL string) (*model.SourceAsset, error) {
	for attempt := 1; attempt <= 2; attempt++ {
		sc.logf("[COLLY] Attempt %d/2: Fetching %s", attempt, boardURL)

		c := colly.NewCollector(
			colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
		)
		extensions.RandomUserAgent(c)
		c.SetRequestTimeout(30 * time.Second)

		c.OnRequest(func(r *colly.Request) {
			r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
			r.Headers.Set("DNT", "1")
			r.Headers.Set("Connection", "keep-alive")
			r.Headers.Set("Upgrade-Insecure-Requests", "1")
			r.Headers.Set("Referer", "https://www.pinterest.com/")
		})

		var bestImgURL string
		var maxDimensions int

		imgSelectors := []string{
			"img[src*='pinimg.com']",
			"img[data-src*='pinimg.com']",
			"img[alt]",
			"div[role='img'] img",
		}

		for _, selector := range imgSelectors {
			c.OnHTML(selector, func(e *colly.HTMLElement) {
				src := e.Attr("src")
				if src == "" {
					src = e.Attr("data-src")
				}
				if src == "" {
					src = e.Attr("data-lazy-src")
				}

				if src == "" || !strings.Contains(src, "pinimg.com") {
					return
				}

				width, height := extractDimensions(src)
				if width == 0 && height == 0 {
					width, height = 1200, 1200
				}

				currentDimensions := width * height
				if currentDimensions > maxDimensions {
					maxDimensions = currentDimensions
					bestImgURL = src
				} else if bestImgURL == "" {
					bestImgURL = src
				}
			})
		}

		c.OnError(func(_ *colly.Response, err error) {
			_ = err
		})

		if err := c.Visit(boardURL); err != nil {
			sc.logf("[COLLY] ❌ Attempt %d failed: %v", attempt, err)
			if attempt < 2 {
				sc.logf("[COLLY] Waiting 2s before retry...")
				time.Sleep(2 * time.Second)
			}
			continue
		}

		if bestImgURL != "" {
			sc.logf("[COLLY] ✓ Found image from Colly")
			finalURL := bestImgURL
			if !strings.Contains(finalURL, "fit=") && !strings.Contains(finalURL, "?") {
				sc.logf("[COLLY] Adding quality parameters: fit=1200x1200")
				finalURL += "?fit=1200x1200"
			}
			return &model.SourceAsset{
				ID:        fmt.Sprintf("pinterest-%d", time.Now().UnixNano()),
				Kind:      model.SourceKindPinterest,
				SourceURL: boardURL,
				MediaKey:  finalURL,
				MimeType:  "image/jpeg",
				AddedAt:   time.Now(),
			}, nil
		}
	}

	sc.logf("[COLLY] ❌ Colly failed on both attempts")
	return nil, errors.New("no pinterest image found using colly fallback")
}

// extractDimensions parses width and height from image URL
func extractDimensions(src string) (int, int) {
	var width, height int

	if strings.Contains(src, "fit=") {
		parts := strings.Split(src, "fit=")
		if len(parts) > 1 {
			dimStr := strings.Split(parts[1], "&")[0]
			dimensions := strings.Split(dimStr, "x")
			if len(dimensions) == 2 {
				_, _ = fmt.Sscanf(dimensions[0], "%d", &width)
				_, _ = fmt.Sscanf(dimensions[1], "%d", &height)
				if width > 0 && height > 0 {
					return width, height
				}
			}
		}
	}

	urlParts := strings.Split(src, "/")
	for _, part := range urlParts {
		if strings.Contains(part, "x") && !strings.Contains(part, ".") {
			dimensions := strings.Split(part, "x")
			if len(dimensions) == 2 {
				_, _ = fmt.Sscanf(dimensions[0], "%d", &width)
				_, _ = fmt.Sscanf(dimensions[1], "%d", &height)
				if width > 0 && height > 0 {
					return width, height
				}
			}
		}
	}

	return 0, 0
}
