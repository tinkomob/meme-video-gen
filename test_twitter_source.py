import os
from dotenv import load_dotenv
from app.sources import fetch_one_from_twitter
from app.config import X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET, X_BEARER_TOKEN

load_dotenv()

bearer_token = X_BEARER_TOKEN
has_oauth = all([X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET])

if not bearer_token and not has_oauth:
    print("Twitter API credentials not configured in .env file")
    print("\nOption 1 (Recommended): Bearer Token")
    print("  X_BEARER_TOKEN=your_bearer_token")
    print("\nOption 2: OAuth 1.0a (all required)")
    print("  X_CONSUMER_KEY=your_key")
    print("  X_CONSUMER_SECRET=your_secret")
    print("  X_ACCESS_TOKEN=your_token")
    print("  X_ACCESS_TOKEN_SECRET=your_token_secret")
    exit(1)

twitter_accounts = [
    "https://x.com/imagesooc",
    "https://x.com/nocontextimg",
    "@EverythingOOC",
    "weirddalle"
]

print(f"Testing Twitter image fetching with {len(twitter_accounts)} accounts...")
if bearer_token:
    print(f"Using Bearer Token authentication: {bearer_token[:20]}...")
elif has_oauth:
    print(f"Using OAuth 1.0a authentication")
    print(f"  Consumer Key: {X_CONSUMER_KEY[:10] if X_CONSUMER_KEY else 'None'}...")
    print(f"  Access Token: {X_ACCESS_TOKEN[:10] if X_ACCESS_TOKEN else 'None'}...")
print()

result = fetch_one_from_twitter(twitter_accounts, output_dir='pins_test')

if result:
    print(f"\nSUCCESS! Downloaded image to: {result}")
    print(f"File size: {os.path.getsize(result)} bytes")
else:
    print("\nFAILED: Could not fetch image from Twitter")

print("\nCheck the console output above for details about the API calls.")
