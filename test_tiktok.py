#!/usr/bin/env python3
"""
Test script for TikTok upload functionality
"""
import os
import sys
sys.path.append(os.path.dirname(__file__))

from app.uploaders import tiktok_upload
from app.service import deploy_to_socials

def test_tiktok_upload():
    """Test TikTok upload with dry run"""
    print("Testing TikTok upload functionality...")

    # Test dry run deployment
    result = deploy_to_socials(
        video_path='tiktok_video.mp4',
        thumbnail_path='thumbnail.jpg',
        source_url='https://example.com/test',
        audio_path=None,
        socials=['tiktok'],
        dry_run=True
    )

    print(f"Dry run result: {result}")

    # Test direct upload function (would require actual files and cookies)
    print("\nTo test actual upload, run:")
    print("from app.uploaders import tiktok_upload")
    print("result = tiktok_upload('tiktok_video.mp4', description='Test upload')")
    print("print(result)")

if __name__ == "__main__":
    test_tiktok_upload()