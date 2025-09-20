import os
from dotenv import load_dotenv

load_dotenv()

YOUTUBE_API_KEY = os.getenv('YOUTUBE_API_KEY')
INSTAGRAM_USERNAME = os.getenv('INSTAGRAM_USERNAME')
INSTAGRAM_PASSWORD = os.getenv('INSTAGRAM_PASSWORD')
API_NINJAS_KEY = os.getenv('API_NINJAS_KEY')

HISTORY_FILE = 'download_history.json'
DEFAULT_PINS_DIR = 'pins'
DEFAULT_AUDIO_DIR = 'audio'
DEFAULT_OUTPUT_VIDEO = 'tiktok_video.mp4'
DEFAULT_THUMBNAIL = 'thumbnail.jpg'
TOKEN_PICKLE = 'token.pickle'
CLIENT_SECRETS = 'client_secrets.json'
TIKTOK_COOKIES_FILE = os.getenv('TIKTOK_COOKIES_FILE', 'cookies.txt')
TIKTOK_HEADLESS = os.getenv('TIKTOK_HEADLESS', 'false').lower() == 'true'
TIKTOK_BROWSER = os.getenv('TIKTOK_BROWSER', 'chrome')