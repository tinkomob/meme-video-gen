#!/usr/bin/env python3
"""
Script to generate YouTube authentication tokens for multiple accounts.
Usage: python get_youtube_token.py [token_name] [credentials_file]
Examples:
  python get_youtube_token.py token.pickle client_secrets.json
  python get_youtube_token.py token_account2.pickle client_secrets_account2.json
"""

import sys
import os
from pathlib import Path
from google_auth_oauthlib.flow import InstalledAppFlow
from google.auth.transport.requests import Request
import pickle

def get_youtube_token(token_path: str = 'token.pickle', credentials_path: str = 'client_secrets.json'):
    """
    Generate or refresh YouTube authentication token.
    
    Args:
        token_path: Path to save the token.pickle file
        credentials_path: Path to client_secrets.json file
    
    Returns:
        True if successful, False otherwise
    """
    
    # Check if credentials file exists
    if not os.path.exists(credentials_path):
        print(f"❌ Файл с учетными данными не найден: {credentials_path}")
        print(f"   Скачай client_secrets.json с https://console.cloud.google.com/")
        return False
    
    print(f"📝 Используется файл учетных данных: {credentials_path}")
    print(f"💾 Токен будет сохранен в: {token_path}")
    print()
    
    # OAuth scopes
    scopes = [
        'https://www.googleapis.com/auth/youtube.upload',
        'https://www.googleapis.com/auth/youtube'
    ]
    
    try:
        # Check if token already exists
        creds = None
        if os.path.exists(token_path):
            print(f"✓ Найден существующий токен: {token_path}")
            with open(token_path, 'rb') as token_file:
                creds = pickle.load(token_file)
            
            # Try to refresh if expired
            if creds and hasattr(creds, 'expired') and creds.expired and hasattr(creds, 'refresh_token') and creds.refresh_token:
                print("🔄 Токен истек, обновляю...")
                creds.refresh(Request())
                with open(token_path, 'wb') as token_file:
                    pickle.dump(creds, token_file)
                print(f"✅ Токен успешно обновлен: {token_path}")
                return True
            elif creds and hasattr(creds, 'valid') and creds.valid:
                print(f"✅ Токен валиден: {token_path}")
                return True
        
        # Generate new token
        print("🔐 Запускаю процесс аутентификации...")
        print("   Браузер откроется для входа в Google аккаунт")
        print()
        
        flow = InstalledAppFlow.from_client_secrets_file(credentials_path, scopes)
        creds = flow.run_local_server(port=0)
        
        # Save token
        with open(token_path, 'wb') as token_file:
            pickle.dump(creds, token_file)
        
        print()
        print(f"✅ Токен успешно создан: {token_path}")
        print()
        
        # Get account info
        from googleapiclient.discovery import build
        youtube = build('youtube', 'v3', credentials=creds)
        channels = youtube.channels().list(part='snippet', mine=True).execute()
        
        if channels.get('items'):
            channel_name = channels['items'][0]['snippet']['title']
            channel_id = channels['items'][0]['id']
            print(f"📺 Аккаунт: {channel_name}")
            print(f"   ID канала: {channel_id}")
        
        return True
        
    except Exception as e:
        print(f"❌ Ошибка при создании токена: {e}")
        import traceback
        traceback.print_exc()
        return False

def main():
    if len(sys.argv) > 3:
        print("Usage: python get_youtube_token.py [token_name] [credentials_file]")
        print("Examples:")
        print("  python get_youtube_token.py                              # Использует defaults")
        print("  python get_youtube_token.py token_account2.pickle client_secrets_account2.json")
        sys.exit(1)
    
    token_path = sys.argv[1] if len(sys.argv) > 1 else 'token.pickle'
    credentials_path = sys.argv[2] if len(sys.argv) > 2 else 'client_secrets.json'
    
    success = get_youtube_token(token_path, credentials_path)
    sys.exit(0 if success else 1)

if __name__ == '__main__':
    main()
