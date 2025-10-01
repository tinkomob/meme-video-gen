# Twitter/X Image Source Integration

## Overview
The meme video generator now supports fetching images directly from Twitter/X accounts using the official Twitter API v2. This provides an additional content source alongside Pinterest, Reddit, and meme APIs.

## Features
- Fetches recent tweets with photo attachments from specified Twitter accounts
- Filters out sensitive/NSFW content automatically
- Respects history to avoid reusing images
- Handles API rate limits gracefully
- Supports both full URLs and username formats

## Setup

### 1. Twitter API Credentials
You need a Twitter Developer account with API access. Apply at: https://developer.twitter.com/

**RECOMMENDED: Use Bearer Token** (simplest for read-only access)

1. Go to [Twitter Developer Portal](https://developer.twitter.com/en/portal/dashboard)
2. Select your app (or create one)
3. Navigate to **"Keys and tokens"** tab
4. Under **"Bearer Token"**, click **"Generate"**
5. Copy the token (starts with "AAAA...")

**Alternative: OAuth 1.0a** (requires elevated access, more complex)

Once approved, create an app and obtain:
- **Consumer Key** (API Key)
- **Consumer Secret** (API Secret)  
- **Access Token**
- **Access Token Secret**

### 2. Environment Configuration

**Option 1: Bearer Token (Recommended)**

Add to your `.env` file:

```env
X_BEARER_TOKEN=AAAAAAAAAAAAAAAAAAAAABcdefg1234567890...your_bearer_token
```

**Option 2: OAuth 1.0a** (if you have elevated access)

```env
X_CONSUMER_KEY=your_consumer_key_here
X_CONSUMER_SECRET=your_consumer_secret_here
X_ACCESS_TOKEN=your_access_token_here
X_ACCESS_TOKEN_SECRET=your_access_token_secret_here
```

The code automatically uses Bearer Token if available, falling back to OAuth 1.0a.

### 3. Configure Twitter Accounts
Edit `twitter_urls.json` to specify which Twitter accounts to scrape images from:

```json
[
  "https://x.com/imagesooc",
  "https://x.com/nocontextimg",
  "@EverythingOOC",
  "weirddalle"
]
```

Supported formats:
- Full URL: `https://x.com/username` or `https://twitter.com/username`
- X.com URL: `https://x.com/username`
- With @: `@username`
- Plain username: `username`

## How It Works

### Source Selection Process
When generating a video, the system:
1. Randomly selects between available sources (Pinterest, Reddit, Twitter, meme APIs)
2. If Twitter is selected, picks a random account from `twitter_urls.json`
3. Fetches up to 100 recent tweets (excludes retweets and replies)
4. Filters for tweets with photo attachments
5. Skips sensitive/NSFW content
6. Checks against history to avoid duplicates
7. Downloads the first unused image found

### API Usage
The integration uses Twitter API v2 with the following endpoints:
- `GET /2/users/by/username/:username` - Get user ID from username
- `GET /2/users/:id/tweets` - Get user's recent tweets with media

Parameters used:
- `max_results=100` - Fetch up to 100 tweets
- `exclude=['retweets', 'replies']` - Original tweets only
- `expansions=['attachments.media_keys']` - Include media data
- `media_fields=['url', 'type', 'variants']` - Get image URLs

### Rate Limits
Twitter API v2 rate limits (per 15-minute window):
- User lookup: 900 requests
- User tweets: 900 requests

The bot makes 2 API calls per Twitter source attempt:
1. User lookup
2. Tweet fetching

This allows ~450 Twitter source attempts per 15 minutes, which is more than sufficient for typical usage.

## Testing

### Test Twitter Integration
Run the test script to verify your setup:

```bash
python test_twitter_source.py
```

This will:
- Check if API credentials are configured
- Attempt to fetch an image from configured accounts
- Display detailed API call logs
- Save test image to `pins_test/` directory

### Test via Bot Command
Use the `/generate` command in Telegram:

```
/generate
```

If Twitter URLs are configured, the bot will randomly select between all available sources (Pinterest, Reddit, Twitter, meme APIs).

## Usage in Bot

### Automatic Source Selection
Twitter is automatically included in the source rotation. The bot will:
1. Load all configured sources (Pinterest, Reddit, Twitter)
2. Randomly shuffle the order
3. Try each source until one succeeds
4. Fall back to meme API if all fail

### Manual Generation
Use the generate command as usual:

```
/generate          # Generate 1 video with default settings
/generate 5        # Generate 5 videos
/generate 100 15   # Use pin_num=100, audio_duration=15s
```

### Source Notification
When Twitter is used as the source, you'll see:
```
🐦 Пробую Twitter/X…
🖼️ Получено изображение из Twitter
```

## Troubleshooting

### No Images Found
**Symptom**: "Twitter: No unused images found for @username"

**Causes**:
1. Account has no recent tweets with images
2. All images already used (check `download_history.json`)
3. Account tweets are all marked as sensitive
4. Account has retweets/replies only

**Solutions**:
- Add more Twitter accounts to `twitter_urls.json`
- Clear history: `/clearhistory` in bot
- Choose accounts that post original image content

### Authentication Failed
**Symptom**: "Twitter: API error: 401 Unauthorized"

**Causes**:
1. Invalid or missing API credentials
2. Using OAuth 1.0a without elevated access
3. Token expired or regenerated

**Solutions**:
- **Use Bearer Token** (recommended) - see [TWITTER_AUTH_FIX.md](TWITTER_AUTH_FIX.md)
- Get Bearer Token from Developer Portal → Keys and tokens
- Add to `.env`: `X_BEARER_TOKEN=your_token`
- For OAuth 1.0a: Apply for Elevated access
- Verify credentials in `.env` file
- Regenerate tokens if needed

### Rate Limited
**Symptom**: "Twitter: API error: 429 Too Many Requests"

**Cause**: Exceeded Twitter API rate limits

**Solution**: Wait 15 minutes for rate limit reset. The bot will automatically fall back to other sources.

### Content Type Errors
**Symptom**: "Twitter: Invalid content type"

**Cause**: Tweet contains video instead of image

**Solution**: This is expected - the bot automatically skips videos and finds image tweets.

## Integration Points

### Code Structure
- **`app/sources.py`**: Contains `fetch_one_from_twitter()` function
- **`app/service.py`**: Includes `_twitter_provider()` in source candidates
- **`app/config.py`**: Defines Twitter API credential variables
- **`bot.py`**: Loads `twitter_urls.json` and passes to generation

### Function Signature
```python
def fetch_one_from_twitter(
    sources: list[str],
    output_dir: str = 'pins'
) -> str | None
```

**Parameters**:
- `sources`: List of Twitter usernames/URLs
- `output_dir`: Directory to save downloaded images

**Returns**:
- Path to downloaded image on success
- `None` on failure

### Error Handling
The function gracefully handles:
- Missing API credentials (returns None)
- Invalid usernames (tries next account)
- Network errors (tries next account)
- Rate limiting (tries next account)
- Content validation errors (tries next image)

### History Tracking
Downloaded image URLs are automatically added to `download_history.json` via `add_url_to_history()` to prevent reuse.

## Advanced Configuration

### Filtering Sensitive Content
The bot automatically filters sensitive content via:
```python
if hasattr(tweet, 'possibly_sensitive') and tweet.possibly_sensitive:
    continue
```

To include sensitive content (not recommended), comment out this check in `app/sources.py`.

### Adjusting Tweet Count
To fetch more or fewer tweets, modify the `max_results` parameter:
```python
tweets_response = client.get_users_tweets(
    id=user_id,
    max_results=100,  # Change this (10-100)
    ...
)
```

### Priority Accounts
To prioritize certain accounts, list them first in `twitter_urls.json`:
```json
[
  "priority_account_1",
  "priority_account_2",
  "other_account_1",
  "other_account_2"
]
```

The system randomly selects, but you can modify the selection logic in `fetch_one_from_twitter()`.

## Best Practices

### Account Selection
- Choose accounts that post original memes/images
- Avoid accounts that mostly retweet
- Mix different types of content for variety
- Check accounts post regularly (active in last 30 days)

### API Usage
- Don't add too many accounts (10-20 is plenty)
- The bot tries one random account per generation
- Rate limits are per app, not per account
- Monitor API usage in Twitter Developer Portal

### Content Quality
- Prefer accounts with high-quality images (>500x500px)
- Avoid accounts with heavy text overlays
- Choose visually interesting content
- Consider niche meme accounts for unique content

## Future Enhancements

Potential improvements for future versions:
1. Support for Twitter lists (fetch from curated lists)
2. Keyword/hashtag search instead of specific accounts
3. Trending topics integration
4. Image quality filtering (skip low-res images)
5. Smart account rotation based on success rate
6. Caching user IDs to reduce API calls
7. Support for Twitter threads (multiple images per tweet)

## API Costs

Twitter API v2 tiers:
- **Free Tier**: 1,500 tweets/month (very limited)
- **Basic**: $100/month - 10,000 tweets/month
- **Pro**: $5,000/month - 1,000,000 tweets/month

For this bot's usage:
- Typical usage: 10-50 API calls/day
- Monthly estimate: 300-1,500 calls/month
- Recommended: **Basic tier** ($100/month)

Note: If using only for personal bot with limited generations, Free tier might suffice.

## Support

If you encounter issues:
1. Check logs in console output for detailed error messages
2. Verify API credentials are correct
3. Test with `test_twitter_source.py`
4. Check Twitter Developer Portal for API status
5. Review rate limit status in Developer Portal

## License

This integration uses the official Twitter API v2 and Tweepy library, both subject to their respective licenses and Twitter's Developer Terms of Service.
