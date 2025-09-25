#!/usr/bin/env python3
"""
Test script for improved Pinterest sources functionality
"""
import os
import sys
import tempfile
import shutil

# Add the app directory to the path
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'app'))

from app.sources import scrape_pinterest_search, scrape_one_from_pinterest, get_emergency_fallback_content, log_pinterest_debug

def test_pinterest_functions():
    """Test the Pinterest functions with improved error handling"""
    
    # Create a temporary directory for testing
    temp_dir = tempfile.mkdtemp(prefix="pinterest_test_")
    print(f"Using temporary directory: {temp_dir}")
    
    try:
        log_pinterest_debug("Starting Pinterest functionality tests", "TEST")
        
        # Test 1: Pinterest search
        print("\n=== Test 1: Pinterest Search ===")
        search_url = "https://www.pinterest.com/search/pins/?q=memes"
        result1 = scrape_pinterest_search(search_url, temp_dir, num=5)
        
        if result1:
            print(f"✅ Pinterest search successful: {result1}")
            print(f"   File size: {os.path.getsize(result1) if os.path.isfile(result1) else 'N/A'} bytes")
        else:
            print("❌ Pinterest search failed (empty response)")
        
        # Test 2: Pinterest board scraping
        print("\n=== Test 2: Pinterest Board Scraping ===")
        board_url = "https://www.pinterest.com/search/pins/?q=funny+memes"
        result2 = scrape_one_from_pinterest(board_url, temp_dir, num=10)
        
        if result2:
            print(f"✅ Pinterest board scraping successful: {result2}")
            print(f"   File size: {os.path.getsize(result2) if os.path.isfile(result2) else 'N/A'} bytes")
        else:
            print("❌ Pinterest board scraping failed (empty response)")
            
        # Test 3: Emergency fallback
        print("\n=== Test 3: Emergency Fallback ===")
        result3 = get_emergency_fallback_content(temp_dir)
        
        if result3:
            print(f"✅ Emergency fallback successful: {result3}")
            print(f"   File size: {os.path.getsize(result3) if os.path.isfile(result3) else 'N/A'} bytes")
        else:
            print("❌ Emergency fallback failed")
            
        # Summary
        print("\n=== Summary ===")
        successful_tests = sum([bool(result1), bool(result2), bool(result3)])
        print(f"Successful tests: {successful_tests}/3")
        
        if successful_tests == 0:
            print("⚠️  All Pinterest methods failed - this indicates a network or dependency issue")
        elif successful_tests < 3:
            print("⚠️  Some Pinterest methods failed - fallback mechanisms are working")
        else:
            print("✅ All Pinterest methods working correctly")
            
        # List downloaded files
        print(f"\nFiles in test directory ({temp_dir}):")
        for file in os.listdir(temp_dir):
            filepath = os.path.join(temp_dir, file)
            size = os.path.getsize(filepath) if os.path.isfile(filepath) else 0
            print(f"  - {file} ({size} bytes)")
            
    except Exception as e:
        log_pinterest_debug(f"Test failed with error: {e}", "ERROR")
        print(f"❌ Test execution failed: {e}")
        
    finally:
        # Clean up temporary directory
        try:
            shutil.rmtree(temp_dir)
            print(f"\nCleaned up temporary directory: {temp_dir}")
        except Exception as e:
            print(f"Warning: Could not clean up temp directory: {e}")

if __name__ == "__main__":
    test_pinterest_functions()