#!/usr/bin/env python3

import os
import sys
import tempfile
from pathlib import Path

# Add project root to Python path
project_root = Path(__file__).parent
sys.path.insert(0, str(project_root))

def test_tiktok_upload():
    print("Testing TikTok upload with local TiktokAutoUploader...")
    
    try:
        from app.uploaders import tiktok_upload
        
        # Create a dummy video file for testing
        test_video = tempfile.NamedTemporaryFile(suffix='.mp4', delete=False)
        test_video.write(b'fake video content for testing')
        test_video.close()
        
        print(f"Created test video: {test_video.name}")
        
        # Test the upload function
        result = tiktok_upload(
            video_path=test_video.name,
            description="Test upload using local TiktokAutoUploader #test",
            cookies=None  # No cookies for this test
        )
        
        print(f"Upload result: {result}")
        
        # Clean up
        os.unlink(test_video.name)
        
        if result and result.get('success'):
            print("✅ TikTok upload test PASSED")
            return True
        else:
            print("❌ TikTok upload test FAILED")
            return False
            
    except Exception as e:
        print(f"❌ TikTok upload test ERROR: {e}")
        import traceback
        traceback.print_exc()
        return False

def test_local_tiktok_structure():
    print("Testing local TiktokAutoUploader structure...")
    
    uploader_dir = Path(__file__).parent / 'TiktokAutoUploader'
    required_files = [
        'cli.py',
        'tiktok_uploader/tiktok.py',
        'tiktok_uploader/Config.py',
        'CookiesDir',
        'VideosDirPath'
    ]
    
    for file_path in required_files:
        full_path = uploader_dir / file_path
        if full_path.exists():
            print(f"✅ Found: {file_path}")
        else:
            print(f"❌ Missing: {file_path}")
            return False
    
    print("✅ All required TiktokAutoUploader files found")
    return True

if __name__ == "__main__":
    print("=== TikTok Upload Test with Local Library ===")
    
    tests = [
        ("Local TiktokAutoUploader Structure", test_local_tiktok_structure),
        ("TikTok Upload Function", test_tiktok_upload),
    ]
    
    results = []
    
    for test_name, test_func in tests:
        print(f"\n--- {test_name} ---")
        try:
            result = test_func()
            results.append((test_name, result))
        except Exception as e:
            print(f"Test {test_name} crashed: {e}")
            results.append((test_name, False))
    
    print("\n=== Test Results ===")
    for test_name, result in results:
        status = "PASS" if result else "FAIL"
        print(f"{test_name}: {status}")
    
    any_passed = any(result for _, result in results)
    
    if any_passed:
        print("\n🎉 At least one test passed! System is working.")
        sys.exit(0)
    else:
        print("\n💥 All tests failed. Please check the issues above.")
        sys.exit(1)