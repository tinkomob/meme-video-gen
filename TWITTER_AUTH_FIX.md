# Twitter Authentication Fix - 401 Unauthorized

## Problem
Getting `401 Unauthorized` error when trying to fetch images from Twitter API.

## Root Cause
Twitter API v2 requires proper authentication. There are two methods:
1. **Bearer Token** (App-only authentication) - **RECOMMENDED** for read-only operations
2. **OAuth 1.0a** (User context) - More complex, requires elevated access

The error typically occurs when:
- Using OAuth 1.0a credentials without API v2 elevated access
- Invalid or expired credentials
- App doesn't have proper permissions

## Solution: Use Bearer Token

### Step 1: Get Your Bearer Token

1. Go to [Twitter Developer Portal](https://developer.twitter.com/en/portal/dashboard)

2. Select your app (or create one if needed)

3. Navigate to **"Keys and tokens"** tab

4. Under **"Bearer Token"** section, click **"Generate"** or **"Regenerate"**

5. **IMPORTANT**: Copy the Bearer Token immediately - you won't be able to see it again!
   - It looks like: `AAAAAAAAAAAAAAAAAAAAABcdefg...` (very long string)

### Step 2: Add Bearer Token to .env

Add this line to your `.env` file:

```env
X_BEARER_TOKEN=AAAAAAAAAAAAAAAAAAAAABcdefg1234567890...your_actual_bearer_token
```

**OR** use the alternative name:

```env
TWITTER_BEARER_TOKEN=AAAAAAAAAAAAAAAAAAAAABcdefg1234567890...your_actual_bearer_token
```

### Step 3: Test the Connection

```bash
python test_twitter_source.py
```

You should see:
```
Using Bearer Token authentication: AAAAAAAAAAAAAAAAAAAA...
Twitter: Using Bearer Token authentication
Twitter: Fetching user info for @username
Twitter: User ID: 1234567890
...
SUCCESS! Downloaded image to: pins_test/twitter_...
```

## Alternative: Fix OAuth 1.0a (Advanced)

If you prefer to use OAuth 1.0a instead of Bearer Token:

### Requirements
- Twitter Developer Account with **Elevated** access (not Basic)
- App must have "Read and Write" permissions
- Valid OAuth 1.0a credentials

### Steps

1. **Apply for Elevated Access**:
   - Go to [Twitter Developer Portal](https://developer.twitter.com/en/portal/products/elevated)
   - Apply for Elevated access (free, but requires review)
   - Wait for approval (usually 24-48 hours)

2. **Verify App Permissions**:
   - Go to your app settings
   - Check "User authentication settings"
   - Ensure "Read" permission is enabled
   - Save changes

3. **Regenerate Tokens** (if needed):
   - Go to "Keys and tokens"
   - Regenerate Access Token & Secret
   - Update your `.env` file

4. **Verify Credentials**:
   ```env
   X_CONSUMER_KEY=your_consumer_key
   X_CONSUMER_SECRET=your_consumer_secret
   X_ACCESS_TOKEN=your_access_token
   X_ACCESS_TOKEN_SECRET=your_access_token_secret
   ```

## Recommended Configuration

**Best practice**: Use Bearer Token for simplicity and reliability.

Your `.env` should have:

```env
# Twitter/X API - Use Bearer Token (RECOMMENDED)
X_BEARER_TOKEN=AAAAAAAAAAAAAAAAAAAAABcdefg1234567890...

# Optional: OAuth 1.0a (only if you have elevated access)
# X_CONSUMER_KEY=your_key
# X_CONSUMER_SECRET=your_secret
# X_ACCESS_TOKEN=your_token
# X_ACCESS_TOKEN_SECRET=your_token_secret
```

The code will automatically prefer Bearer Token if available, falling back to OAuth 1.0a.

## Testing Different Auth Methods

### Test Bearer Token Only
```bash
# In .env, set only:
X_BEARER_TOKEN=your_token

# Remove or comment out OAuth credentials
# X_CONSUMER_KEY=...
# X_CONSUMER_SECRET=...
# X_ACCESS_TOKEN=...
# X_ACCESS_TOKEN_SECRET=...

python test_twitter_source.py
```

### Test OAuth 1.0a Only
```bash
# In .env, remove Bearer Token
# X_BEARER_TOKEN=...

# Set OAuth credentials
X_CONSUMER_KEY=your_key
X_CONSUMER_SECRET=your_secret
X_ACCESS_TOKEN=your_token
X_ACCESS_TOKEN_SECRET=your_token_secret

python test_twitter_source.py
```

## Common Issues

### Issue 1: "403 Forbidden"
**Cause**: App doesn't have permission to access the endpoint
**Solution**: 
- Use Bearer Token instead
- Or apply for Elevated access for OAuth 1.0a

### Issue 2: "429 Too Many Requests"
**Cause**: Rate limit exceeded
**Solution**: 
- Wait 15 minutes
- Reduce number of requests
- Check rate limit status in Developer Portal

### Issue 3: "Invalid or expired token"
**Cause**: Token was regenerated or revoked
**Solution**: 
- Generate new Bearer Token
- Or regenerate OAuth credentials
- Update `.env` file

### Issue 4: Still getting 401 with Bearer Token
**Cause**: Invalid Bearer Token format or copied incorrectly
**Solution**:
- Ensure no extra spaces in `.env`
- Copy the complete token (usually starts with "AAAA")
- Regenerate if needed
- Check for line breaks in the token value

## Verification Checklist

- [ ] Bearer Token obtained from Developer Portal
- [ ] Token added to `.env` file (no extra spaces)
- [ ] `.env` file in same directory as bot.py
- [ ] Restarted application after changing `.env`
- [ ] Test script runs successfully: `python test_twitter_source.py`
- [ ] See "Using Bearer Token authentication" in output
- [ ] No 401 or 403 errors in console
- [ ] Image successfully downloaded to pins_test/

## API Access Levels

### Free Tier
- ✅ Bearer Token available
- ✅ Read tweets (10,000/month limit)
- ✅ Good for this bot's usage
- ❌ No OAuth 1.0a v2 API access

### Basic Tier ($100/month)
- ✅ Bearer Token available
- ✅ Read tweets (100,000/month limit)
- ✅ OAuth 1.0a v2 API access
- ✅ Sufficient for bot with multiple users

### Elevated (Legacy Free)
- ✅ Bearer Token available
- ✅ OAuth 1.0a v2 API access
- ⚠️ No longer accepting new applications
- ⚠️ Existing users grandfathered in

## Support

If issues persist:

1. Check Twitter API status: https://api.twitterstat.us/
2. Verify app status in Developer Portal
3. Review rate limits in Developer Portal
4. Check application logs for detailed error messages
5. Ensure Twitter account is in good standing

## Quick Reference

**Get Bearer Token**: Developer Portal → Your App → Keys and tokens → Bearer Token → Generate

**Add to .env**: `X_BEARER_TOKEN=your_very_long_token_here`

**Test**: `python test_twitter_source.py`

**Expected output**: "Using Bearer Token authentication" → "SUCCESS! Downloaded image..."

That's it! Bearer Token is the simplest and most reliable method for read-only Twitter API access.
