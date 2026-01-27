#!/usr/bin/env python
"""
Test script for Google Images scraping using SerpAPI.
Downloads one image to verify the integration works.
"""

import os
import sys
from pathlib import Path

# Add parent directory to path to import app modules
sys.path.insert(0, str(Path(__file__).parent))

from app.sources import scrape_one_from_google_images
from app.config import SERPAPI_KEY


def main():
    print("=" * 60)
    print("Google Images Scraper Test")
    print("=" * 60)
    print()
    
    # Check if API key is configured
    if not SERPAPI_KEY:
        print("❌ ERROR: SERPAPI_KEY not found in environment variables!")
        print()
        print("Please add SERPAPI_KEY to your .env file:")
        print("  SERPAPI_KEY=your_serpapi_key_here")
        print()
        print("Get your free API key at: https://serpapi.com/")
        print("Free plan includes 100 searches/month")
        return 1
    
    print(f"✓ API key configured: {SERPAPI_KEY[:10]}...")
    print()
    
    # Create test output directory
    test_dir = "test_google_images"
    Path(test_dir).mkdir(parents=True, exist_ok=True)
    print(f"✓ Test directory created: {test_dir}")
    print()
    
    print("Starting Google Images scrape...")
    print("-" * 60)
    
    # Attempt to scrape one image
    image_path, source_url = scrape_one_from_google_images(output_dir=test_dir)
    
    print("-" * 60)
    print()
    
    # Check results
    if image_path and os.path.isfile(image_path):
        file_size = os.path.getsize(image_path)
        print("✓ SUCCESS!")
        print(f"  Image saved to: {image_path}")
        print(f"  File size: {file_size:,} bytes")
        print(f"  Source URL: {source_url or 'N/A'}")
        print()
        print(f"You can view the image at: {os.path.abspath(image_path)}")
        return 0
    else:
        print("❌ FAILED to download image")
        print()
        print("Possible causes:")
        print("  - API key is invalid or expired")
        print("  - API rate limit exceeded (100 searches/month on free plan)")
        print("  - Network connectivity issues")
        print("  - All images from keywords already used (check download_history.json)")
        print()
        print("Check the logs above for detailed error messages.")
        return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\n\nTest interrupted by user")
        sys.exit(130)
    except Exception as e:
        print(f"\n❌ Unexpected error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
