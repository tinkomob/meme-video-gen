#!/usr/bin/env python3

"""
Простой скрипт для тестирования входа в Instagram
"""

import os
from dotenv import load_dotenv
from app.uploaders import test_instagram_login

def main():
    load_dotenv()
    
    print("Testing Instagram login...")
    print("=" * 50)
    
    # Check environment variables
    username = os.getenv('INSTAGRAM_USERNAME')
    password = os.getenv('INSTAGRAM_PASSWORD')
    totp_secret = os.getenv('INSTAGRAM_TOTP_SECRET')
    proxy_url = os.getenv('INSTAGRAM_PROXY')
    
    print(f"Username: {'✓' if username else '✗'}")
    print(f"Password: {'✓' if password else '✗'}")
    print(f"TOTP Secret: {'✓' if totp_secret else '✗'}")
    print(f"Proxy: {'✓' if proxy_url else '✗'}")
    print("-" * 50)
    
    if not username or not password:
        print("❌ Missing required credentials!")
        print("Please set INSTAGRAM_USERNAME and INSTAGRAM_PASSWORD in .env file")
        return
    
    # Test login
    result = test_instagram_login()
    
    if result.get('success'):
        print(f"✅ Login successful!")
        print(f"User: {result.get('user', 'Unknown')}")
        print(f"Details: {result.get('details', 'No details')}")
    else:
        print(f"❌ Login failed!")
        print(f"Error: {result.get('error', 'Unknown error')}")
        print(f"Details: {result.get('details', 'No details')}")
        
        # Provide suggestions based on error type
        error_type = result.get('error', '').lower()
        if 'challenge' in error_type:
            print("\n💡 Suggestions:")
            print("- Try logging in through Instagram app/website first")
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