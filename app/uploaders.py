from pathlib import Path
import os
import tempfile
import shutil
import inspect
import glob
import requests
from .config import CLIENT_SECRETS, TOKEN_PICKLE, UPLOAD_POST_API_KEY, INSTAGRAM_USERNAME
import re

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
    """
    Upload video to Instagram using upload-post.com API
    """
    try:
        if not UPLOAD_POST_API_KEY:
            print('UPLOAD_POST_API_KEY not found in environment variables')
            return {'error': 'Missing API key', 'details': 'UPLOAD_POST_API_KEY required'}
        
        if not INSTAGRAM_USERNAME:
            print('INSTAGRAM_USERNAME not found in environment variables')
            return {'error': 'Missing username', 'details': 'INSTAGRAM_USERNAME required'}
        
        # Extract song name from caption if present
        song_name = 'Unknown'
        if '♪' in caption:
            # Extract text between ♪ symbols
            parts = caption.split('♪')
            if len(parts) >= 2:
                song_name = parts[1].strip()
        
        print(f'Uploading video to Instagram: {video_path}')
        print(f'Username: {INSTAGRAM_USERNAME}')
        print(f'Song: {song_name}')
        print(f'Caption: {caption[:100]}...' if len(caption) > 100 else f'Caption: {caption}')
        
        # Prepare multipart form data
        files = {
            'video': ('video.mp4', open(video_path, 'rb'), 'video/mp4')
        }
        
        data = {
            'user': INSTAGRAM_USERNAME,
            'title': song_name,
            'platform[]': 'instagram',
            'async_upload': 'true',
            'media_type': 'REELS',
            'share_to_feed': 'true',
            'first_comment': f'song is {song_name}'
        }
        
        headers = {
            'Authorization': f'Apikey {UPLOAD_POST_API_KEY}'
        }
        
        # Make API request
        response = requests.post(
            'https://api.upload-post.com/api/upload',
            headers=headers,
            data=data,
            files=files,
            timeout=300  # 5 minute timeout for video upload
        )
        
        # Close file handle
        files['video'][1].close()
        
        # Check response
        if response.status_code == 200:
            response_data = response.json()
            print(f'Instagram upload response: {response_data}')
            
            return {
                'success': True,
                'details': 'Video uploaded successfully (async)',
                'response': response_data
            }
        else:
            error_msg = f'API returned status {response.status_code}'
            try:
                error_data = response.json()
                error_msg = error_data.get('message', error_msg)
            except:
                error_msg = response.text[:200] if response.text else error_msg
            
            print(f'Instagram upload failed: {error_msg}')
            return {
                'error': 'Upload failed',
                'details': error_msg
            }
    
    except requests.exceptions.Timeout:
        return {'error': 'Upload timeout', 'details': 'Request timed out after 5 minutes'}
    except requests.exceptions.RequestException as e:
        return {'error': 'Network error', 'details': f'Request failed: {e}'}
    except FileNotFoundError:
        return {'error': 'File not found', 'details': f'Video file does not exist: {video_path}'}
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