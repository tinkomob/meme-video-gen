import os
from dotenv import load_dotenv

load_dotenv()

YOUTUBE_API_KEY = os.getenv('YOUTUBE_API_KEY')
INSTAGRAM_USERNAME = os.getenv('INSTAGRAM_USERNAME')
INSTAGRAM_PASSWORD = os.getenv('INSTAGRAM_PASSWORD')
API_NINJAS_KEY = os.getenv('API_NINJAS_KEY')
TELEGRAM_BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')

X_CONSUMER_KEY = os.getenv('X_CONSUMER_KEY')
X_CONSUMER_SECRET = os.getenv('X_CONSUMER_SECRET')
X_ACCESS_TOKEN = os.getenv('X_ACCESS_TOKEN')
X_ACCESS_TOKEN_SECRET = os.getenv('X_ACCESS_TOKEN_SECRET')

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
YT_COOKIES_FILE = os.getenv('YT_COOKIES_FILE', 'youtube_cookies.txt')
DAILY_GENERATIONS = int(os.getenv('DAILY_GENERATIONS', '3') or 3)