#!/usr/bin/env python3

"""
Тестирование конфигурации Instagram API (upload-post.com)
"""

import os
from dotenv import load_dotenv

def main():
    load_dotenv()
    
    print("Testing Instagram API configuration...")
    print("=" * 50)
    
    # Check environment variables
    username = os.getenv('INSTAGRAM_USERNAME')
    api_key = os.getenv('UPLOAD_POST_API_KEY')
    
    print(f"INSTAGRAM_USERNAME: {'✓ ' + username if username else '✗ NOT SET'}")
    print(f"UPLOAD_POST_API_KEY: {'✓ ' + api_key[:20] + '...' if api_key and len(api_key) > 20 else '✗ NOT SET' if not api_key else '✓ ' + api_key}")
    print("-" * 50)
    
    if not username or not api_key:
        print("❌ Missing required credentials!")
        print("Please set the following in .env file:")
        if not username:
            print("  - INSTAGRAM_USERNAME=your_instagram_username")
        if not api_key:
            print("  - UPLOAD_POST_API_KEY=your_api_key_from_upload_post_com")
        return
    
    print("✅ Configuration looks good!")
    print("\nTo test actual upload, use /deploy command in the bot with 'instagram' in socials parameter.")
    print("Example: /deploy socials=instagram privacy=public")
            print("- Complete any verification steps")
            print("- Wait some time before trying again")
        elif '2fa' in error_type or 'two_factor' in error_type:
            print("\n💡 Suggestions:")
            print("- Set INSTAGRAM_TOTP_SECRET in .env file")
            print("- Install Google Authenticator or similar app")
            print("- Get TOTP secret from Instagram security settings")
        elif 'rate' in error_type:
            print("\n💡 Suggestions:")
            print("- Wait 15-30 minutes before trying again")
            print("- Use proxy with INSTAGRAM_PROXY variable")
            print("- Try from different IP address")

if __name__ == "__main__":
    main()