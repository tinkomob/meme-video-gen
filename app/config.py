import os
from dotenv import load_dotenv

load_dotenv()

YOUTUBE_API_KEY = os.getenv('YOUTUBE_API_KEY')
INSTAGRAM_USERNAME = os.getenv('INSTAGRAM_USERNAME')
INSTAGRAM_PASSWORD = os.getenv('INSTAGRAM_PASSWORD')
UPLOAD_POST_API_KEY = os.getenv('UPLOAD_POST_API_KEY')
API_NINJAS_KEY = os.getenv('API_NINJAS_KEY')
TELEGRAM_BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')
POSTS_CHATID = os.getenv('POSTS_CHATID')

X_CONSUMER_KEY = os.getenv('X_CONSUMER_KEY')
X_CONSUMER_SECRET = os.getenv('X_CONSUMER_SECRET')
X_ACCESS_TOKEN = os.getenv('X_ACCESS_TOKEN')
X_ACCESS_TOKEN_SECRET = os.getenv('X_ACCESS_TOKEN_SECRET')
X_BEARER_TOKEN = os.getenv('X_BEARER_TOKEN') or os.getenv('TWITTER_BEARER_TOKEN')

# Twikit fallback (scraper) credentials
TWIKIT_USERNAME = os.getenv('TWIKIT_USERNAME') or os.getenv('X_USERNAME')
TWIKIT_EMAIL = os.getenv('TWIKIT_EMAIL') or os.getenv('X_EMAIL')
TWIKIT_PASSWORD = os.getenv('TWIKIT_PASSWORD') or os.getenv('X_PASSWORD')
TWIKIT_COOKIES_FILE = os.getenv('TWIKIT_COOKIES_FILE', 'twitter_cookies.json')

HISTORY_FILE = 'download_history.json'
DEFAULT_PINS_DIR = 'pins'
DEFAULT_AUDIO_DIR = 'audio'
DEFAULT_OUTPUT_VIDEO = 'tiktok_video.mp4'
DEFAULT_THUMBNAIL = 'thumbnail.jpg'
TOKEN_PICKLE = 'token.pickle'
CLIENT_SECRETS = 'client_secrets.json'
YT_COOKIES_FILE = os.getenv('YT_COOKIES_FILE', 'youtube_cookies.txt')
DAILY_GENERATIONS = int(os.getenv('DAILY_GENERATIONS', '3') or 3)
MAX_PARALLEL_GENERATIONS = int(os.getenv('MAX_PARALLEL_GENERATIONS', '2') or 2)
DUP_REGEN_RETRIES = int(os.getenv('DUP_REGEN_RETRIES', '2') or 2)
TEMP_DIR_MAX_AGE_MINUTES = int(os.getenv('TEMP_DIR_MAX_AGE_MINUTES', '180') or 180)