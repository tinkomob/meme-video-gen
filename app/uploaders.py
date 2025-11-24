from pathlib import Path
import os
import tempfile
import shutil
import inspect
import glob
from .config import CLIENT_SECRETS, TOKEN_PICKLE
import re

def test_instagram_login():
    """
    Проверяет возможность входа в Instagram без загрузки видео
    """
    try:
        import os
        from instagrapi import Client
        
        username = os.getenv('INSTAGRAM_USERNAME')
        password = os.getenv('INSTAGRAM_PASSWORD')
        totp_secret = os.getenv('INSTAGRAM_TOTP_SECRET')
        proxy_url = os.getenv('INSTAGRAM_PROXY')
        
        if not username or not password:
            return {'error': 'Missing credentials', 'details': 'INSTAGRAM_USERNAME and INSTAGRAM_PASSWORD required'}
        
        cl = Client()
        cl.delay_range = [1, 3]
        
        # Set consistent device settings
        cl.set_device({
            'app_version': '269.0.0.18.75',
            'android_version': 30,
            'android_release': '11',
            'dpi': '480dpi',
            'resolution': '1080x2400',
            'manufacturer': 'samsung',
            'device': 'SM-G991B',
            'model': 'Galaxy S21',
            'cpu': 'exynos2100',
            'version_code': '314665256'
        })
        
        if proxy_url:
            cl.set_proxy(proxy_url)
        
        session_file = 'instagram_session.json'
        login_required = True
        
        # Try existing session
        if Path(session_file).exists():
            try:
                cl.load_settings(session_file)
                user_info = cl.account_info()
                if user_info and user_info.pk:
                    return {'success': True, 'details': f'Existing session valid for @{user_info.username}', 'user': user_info.username}
            except Exception:
                pass
        
        # Fresh login
        try:
            if totp_secret:
                try:
                    import pyotp
                    totp = pyotp.TOTP(totp_secret)
                    code = totp.now()
                    cl.verification_code = code
                except ImportError:
                    pass
            
            login_result = cl.login(username, password)
            if not login_result:
                return {'error': 'Login failed', 'details': 'Invalid credentials'}
            
            cl.dump_settings(session_file)
            user_info = cl.account_info()
            return {'success': True, 'details': f'Fresh login successful for @{user_info.username}', 'user': user_info.username}
            
        except Exception as e:
            error_msg = str(e).lower()
            if 'challenge' in error_msg:
                return {'error': 'Challenge required', 'details': 'Instagram requires verification'}
            elif 'two_factor' in error_msg:
                return {'error': '2FA required', 'details': 'Set INSTAGRAM_TOTP_SECRET'}
            elif 'rate' in error_msg:
                return {'error': 'Rate limited', 'details': 'Too many attempts'}
            else:
                return {'error': 'Login error', 'details': str(e)}
                
    except ImportError:
        return {'error': 'Missing dependency', 'details': 'instagrapi not installed'}
    except Exception as e:
        return {'error': 'Unexpected error', 'details': str(e)}

def youtube_authenticate(credentials_path: str = CLIENT_SECRETS, token_path: str = TOKEN_PICKLE):
    try:
        from googleapiclient.discovery import build
        from google_auth_oauthlib.flow import InstalledAppFlow
        from google.auth.transport.requests import Request
        import pickle
        scopes = [
            'https://www.googleapis.com/auth/youtube.upload',
            'https://www.googleapis.com/auth/youtube'
        ]
        creds = None
        if Path(token_path).exists():
            with open(token_path, 'rb') as token:
                creds = pickle.load(token)
        if creds is None or not hasattr(creds, 'valid') or not creds.valid:
            if creds and hasattr(creds, 'expired') and creds.expired and hasattr(creds, 'refresh_token') and creds.refresh_token:
                creds.refresh(Request())
            else:
                flow = InstalledAppFlow.from_client_secrets_file(credentials_path, scopes)
                creds = flow.run_local_server(port=0)
            with open(token_path, 'wb') as token:
                pickle.dump(creds, token)
        return build('youtube', 'v3', credentials=creds)
    except Exception as e:
        print(f'YouTube auth failed: {e}')
        return None

def _remove_shorts_hashtag(s: str) -> str:
    try:
        if not isinstance(s, str):
            return s
        cleaned = re.sub(r'(?i)(?:^|\s)#shorts\b', lambda m: ' ' if m.group(0).startswith(' ') else '', s)
        cleaned = re.sub(r'\s{2,}', ' ', cleaned).strip()
        return cleaned
    except Exception:
        return s

def youtube_upload_short(youtube, file_path: str, title: str, description: str = '', tags=None, categoryId='24', privacyStatus='public'):
    try:
        from googleapiclient.http import MediaFileUpload
        if tags is None:
            tags = ['shorts', 'meme', 'funny']
        media = MediaFileUpload(file_path, chunksize=-1, resumable=True, mimetype='video/mp4')
        request = youtube.videos().insert(
            part='snippet,status',
            body={
                'snippet': {
                    'title': title,
                    'description': description,
                    'tags': tags,
                    'categoryId': categoryId
                },
                'status': {
                    'privacyStatus': privacyStatus,
                    'selfDeclaredMadeForKids': False
                }
            },
            media_body=media
        )
        response = None
        while response is None:
            status, response = request.next_chunk()
        return response
    except Exception as e:
        print(f'YouTube upload failed: {e}')
        return None

def instagram_upload(video_path: str, caption: str, thumbnail: str | None = None):
    try:
        import os
        import uuid
        from instagrapi import Client
        
        username = os.getenv('INSTAGRAM_USERNAME')
        password = os.getenv('INSTAGRAM_PASSWORD')
        totp_secret = os.getenv('INSTAGRAM_TOTP_SECRET')
        proxy_url = os.getenv('INSTAGRAM_PROXY')
        
        if not username or not password:
            print('Instagram credentials not found in environment variables')
            return {'error': 'Missing credentials', 'details': 'INSTAGRAM_USERNAME and INSTAGRAM_PASSWORD required'}
        
        cl = Client()
        cl.delay_range = [1, 3]
        
        # Set consistent device settings to avoid detection
        cl.set_device({
            'app_version': '269.0.0.18.75',
            'android_version': 30,
            'android_release': '11',
            'dpi': '480dpi',
            'resolution': '1080x2400',
            'manufacturer': 'samsung',
            'device': 'SM-G991B',
            'model': 'Galaxy S21',
            'cpu': 'exynos2100',
            'version_code': '314665256'
        })
        
        # Set proxy if provided
        if proxy_url:
            cl.set_proxy(proxy_url)
            print(f'Using proxy: {proxy_url}')
        
        session_file = 'instagram_session.json'
        login_required = True
        
        # Try to load existing session
        if Path(session_file).exists():
            try:
                cl.load_settings(session_file)
                # Test if session is still valid
                user_info = cl.account_info()
                if user_info and user_info.pk:
                    print(f'Existing session valid for user: {user_info.username}')
                    login_required = False
                else:
                    print('Existing session invalid, need to re-login')
            except Exception as e:
                print(f'Failed to load session: {e}')
        
        # Login if needed
        if login_required:
            try:
                print('Logging into Instagram...')
                
                # Setup 2FA handler if TOTP secret is provided
                if totp_secret:
                    try:
                        import pyotp
                        totp = pyotp.TOTP(totp_secret)
                        code = totp.now()
                        print(f'Generated TOTP code: {code}')
                        cl.verification_code = code
                    except ImportError:
                        print('pyotp not installed, TOTP codes won\'t work. Install with: pip install pyotp')
                
                # Attempt login
                login_result = cl.login(username, password)
                
                if not login_result:
                    return {'error': 'Login failed', 'details': 'Invalid credentials or account locked'}
                
                # Save session for future use
                cl.dump_settings(session_file)
                print('Instagram login successful, session saved')
                
            except Exception as login_error:
                error_msg = str(login_error).lower()
                
                if 'challenge' in error_msg:
                    return {'error': 'Challenge required', 'details': 'Instagram requires additional verification. Check your email/SMS or try logging in through browser first.'}
                elif 'two_factor' in error_msg or '2fa' in error_msg:
                    return {'error': '2FA required', 'details': 'Two-factor authentication required. Set INSTAGRAM_TOTP_SECRET environment variable.'}
                elif 'rate' in error_msg or 'too many' in error_msg:
                    return {'error': 'Rate limited', 'details': 'Too many login attempts. Wait before trying again.'}
                elif 'user not found' in error_msg or 'incorrect' in error_msg:
                    return {'error': 'Invalid credentials', 'details': 'Username or password is incorrect'}
                else:
                    return {'error': 'Login error', 'details': f'Login failed: {login_error}'}
        
        # Verify we're logged in
        try:
            user_info = cl.account_info()
            if not user_info or not user_info.pk:
                return {'error': 'Authentication failed', 'details': 'Could not verify login status'}
            print(f'Authenticated as: {user_info.username} (ID: {user_info.pk})')
        except Exception as e:
            return {'error': 'Account verification failed', 'details': f'Could not get account info: {e}'}
        
        # Upload video
        print(f'Uploading video: {video_path}')
        print(f'Caption: {caption[:100]}...' if len(caption) > 100 else f'Caption: {caption}')
        
        try:
            if thumbnail:
                media = cl.clip_upload(
                    path=Path(video_path), 
                    caption=caption, 
                    thumbnail=Path(thumbnail)
                )
            else:
                media = cl.clip_upload(
                    path=Path(video_path), 
                    caption=caption
                )
            
            if media and hasattr(media, 'pk'):
                return {
                    'success': True,
                    'media_id': media.pk,
                    'url': f'https://www.instagram.com/p/{media.code}/',
                    'details': 'Video uploaded successfully'
                }
            else:
                return {'error': 'Upload failed', 'details': 'Upload completed but no media returned'}
                
        except Exception as upload_error:
            error_msg = str(upload_error).lower()
            
            if 'video too long' in error_msg or 'duration' in error_msg:
                return {'error': 'Video too long', 'details': 'Instagram Reels must be under 90 seconds'}
            elif 'file size' in error_msg or 'too large' in error_msg:
                return {'error': 'File too large', 'details': 'Video file size exceeds Instagram limits'}
            elif 'format' in error_msg or 'codec' in error_msg:
                return {'error': 'Invalid format', 'details': 'Video format not supported by Instagram'}
            elif 'spam' in error_msg or 'blocked' in error_msg:
                return {'error': 'Content blocked', 'details': 'Content may violate Instagram policies or be flagged as spam'}
            else:
                return {'error': 'Upload error', 'details': f'Upload failed: {upload_error}'}
    
    except ImportError as e:
        return {'error': 'Missing dependency', 'details': f'Required library not installed: {e}'}
    except Exception as e:
        return {'error': 'Unexpected error', 'details': f'Instagram upload failed: {e}'}

def x_upload(video_path: str, text: str = ''):
    try:
        text = _remove_shorts_hashtag(text)
        import tweepy
        from .config import X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET
        
        if not all([X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET]):
            print('X credentials not found')
            return None
        
        print(f"X credentials loaded - Consumer Key: {X_CONSUMER_KEY[:10]}..., Access Token: {X_ACCESS_TOKEN[:10]}...")
        
        # Create API v1.1 for media upload
        auth = tweepy.OAuth1UserHandler(
            X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET
        )
        api_v1 = tweepy.API(auth)
        
        # Test authentication
        try:
            user = api_v1.verify_credentials()
            print(f"X authentication successful for user: @{user.screen_name}")
        except Exception as auth_error:
            print(f"X authentication failed: {auth_error}")
            return None
        
        # Upload media using v1.1 API
        print(f"Uploading media: {video_path}")
        media = api_v1.media_upload(video_path, media_category='tweet_video')
        print(f"Media uploaded successfully, media_id: {media.media_id}")
        
        # Create Client for v2 API tweet creation
        client = tweepy.Client(
            consumer_key=X_CONSUMER_KEY,
            consumer_secret=X_CONSUMER_SECRET,
            access_token=X_ACCESS_TOKEN,
            access_token_secret=X_ACCESS_TOKEN_SECRET
        )
        
        # Post tweet with media using v2 API
        print(f"Posting tweet with text: {text[:50]}...")
        response = client.create_tweet(text=text, media_ids=[media.media_id])
        print(f"Tweet posted successfully, tweet_id: {response.data['id']}")
        
        return response
    except Exception as e:
        print(f'X upload failed: {e}')
        return None
    finally:
        pass