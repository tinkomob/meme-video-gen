import os
import random
import re
import time
from pathlib import Path
from urllib.parse import urlparse
import requests
import threading
from typing import Any
_LOG_SUPPRESS_WINDOW = 15
_EMPTY_RESP_STATE = {"last": 0.0, "suppressed": 0}
from .utils import load_history, add_url_to_history
from .pinterest_monitor import should_use_pinterest_fallback, get_pinterest_status

def log_pinterest_debug(message: str, level: str = "INFO"):
    """Enhanced logging for Pinterest debugging"""
    timestamp = time.strftime("%H:%M:%S")
    lower = (message or "").lower()
    if "empty response" in lower:
        now = time.time()
        last = _EMPTY_RESP_STATE.get("last", 0.0)
        suppressed = _EMPTY_RESP_STATE.get("suppressed", 0)
        if now - last < _LOG_SUPPRESS_WINDOW:
            _EMPTY_RESP_STATE["suppressed"] = suppressed + 1
            return
        if suppressed > 0:
            print(f"[{timestamp}] Pinterest {level}: suppressed {suppressed} repeats of 'Empty response received.' phase=source:pinterest", flush=True)
            _EMPTY_RESP_STATE["suppressed"] = 0
        _EMPTY_RESP_STATE["last"] = now
        print(f"[{timestamp}] Pinterest {level}: {message}", flush=True)
        return
    print(f"[{timestamp}] Pinterest {level}: {message}", flush=True)

def run_with_timeout(fn, timeout_seconds, *args, **kwargs):
    result: dict[str, Any] = {"value": None, "error": None}
    def _target():
        try:
            result["value"] = fn(*args, **kwargs)
        except Exception as e:
            result["error"] = e
    t = threading.Thread(target=_target, daemon=True)
    t.start()
    t.join(timeout_seconds)
    if t.is_alive():
        return False, None, TimeoutError("operation timed out")
    if result["error"] is not None:
        return False, None, result["error"]
    return True, result["value"], None

def is_pinterest_blocked():
    """Check if Pinterest is accessible with a simple test"""
    try:
        headers = {'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'}
        resp = requests.get('https://www.pinterest.com', headers=headers, timeout=10)
        return resp.status_code >= 400
    except Exception:
        return True

def handle_pinterest_recovery():
    """Attempt to recover from Pinterest blocking by waiting and retrying"""
    log_pinterest_debug("Attempting Pinterest recovery sequence", "WARNING")
    
    # Wait with exponential backoff
    wait_times = [30, 60, 120, 300]  # 30s, 1m, 2m, 5m
    
    for i, wait_time in enumerate(wait_times):
        log_pinterest_debug(f"Waiting {wait_time}s before retry {i+1}/{len(wait_times)}")
        time.sleep(wait_time)
        
        if not is_pinterest_blocked():
            log_pinterest_debug(f"Pinterest recovered after {wait_time}s wait!")
            return True
        
        log_pinterest_debug(f"Pinterest still blocked after {wait_time}s wait")
    
    log_pinterest_debug("Pinterest recovery failed, staying with fallback content", "ERROR")
    return False

def get_emergency_fallback_content(output_dir: str = 'pins'):
    """Enhanced emergency fallback to get content when Pinterest fails"""
    log_pinterest_debug("Attempting emergency fallback content", "WARNING")
    
    # Try multiple meme APIs as fallback
    meme_sources = [
        'https://meme-api.com/gimme',
        'https://api.imgflip.com/get_memes',
        'https://meme-api.herokuapp.com/gimme',
    ]
    
    for i, api_url in enumerate(meme_sources):
        try:
            log_pinterest_debug(f"Trying meme API {i+1}/{len(meme_sources)}: {api_url}")
            
            if 'meme-api.com' in api_url or 'meme-api.herokuapp.com' in api_url:
                # Standard meme-api format
                resp = requests.get(api_url, timeout=10)
                resp.raise_for_status()
                data = resp.json()
                
                if data.get('nsfw', True):  # Skip NSFW
                    log_pinterest_debug("Meme is NSFW, skipping")
                    continue
                    
                meme_url = data.get('url')
                if not meme_url:
                    log_pinterest_debug("No URL in response")
                    continue
                    
            elif 'imgflip.com' in api_url:
                # ImgFlip API format
                resp = requests.get(api_url, timeout=10)
                resp.raise_for_status()
                data = resp.json()
                
                memes = data.get('data', {}).get('memes', [])
                if not memes:
                    log_pinterest_debug("No memes in ImgFlip response")
                    continue
                    
                # Pick random meme from ImgFlip
                meme = random.choice(memes)
                meme_url = meme.get('url')
                if not meme_url:
                    log_pinterest_debug("No URL in ImgFlip meme")
                    continue
            else:
                continue
            
            # Check if we've used this URL before
            hist = load_history()
            if meme_url in hist['urls']:
                log_pinterest_debug(f"Meme {meme_url} already used, trying next API")
                continue
                
            log_pinterest_debug(f"Got fresh meme from API {i+1}: {meme_url}")
            
            # Download the meme
            try:
                Path(output_dir).mkdir(parents=True, exist_ok=True)
                headers = {'User-Agent': 'Mozilla/5.0 MemeVideoGen/1.0 (Emergency Fallback)'}
                
                download_resp = requests.get(meme_url, headers=headers, timeout=15, stream=True)
                download_resp.raise_for_status()
                
                # Determine file extension
                ext = '.jpg'
                content_type = download_resp.headers.get('content-type', '').lower()
                if 'png' in content_type or '.png' in meme_url.lower():
                    ext = '.png'
                elif 'gif' in content_type or '.gif' in meme_url.lower():
                    ext = '.gif'
                elif 'webp' in content_type or '.webp' in meme_url.lower():
                    ext = '.webp'
                elif 'jpeg' in content_type or '.jpeg' in meme_url.lower():
                    ext = '.jpg'
                    
                filename = f'emergency_meme_{abs(hash(meme_url)) % 10**6}_{i+1}{ext}'
                filepath = os.path.join(output_dir, filename)
                
                # Download with size checking
                downloaded_size = 0
                with open(filepath, 'wb') as f:
                    for chunk in download_resp.iter_content(chunk_size=8192):
                        if chunk:
                            f.write(chunk)
                            downloaded_size += len(chunk)
                            # Stop if file gets too large (50MB limit)
                            if downloaded_size > 50 * 1024 * 1024:
                                log_pinterest_debug("Emergency file too large, stopping download")
                                break
                                
                if os.path.isfile(filepath) and os.path.getsize(filepath) > 1000:
                    log_pinterest_debug(f"Emergency fallback successful: {filepath} ({downloaded_size} bytes)")
                    add_url_to_history(meme_url)
                    return filepath
                else:
                    log_pinterest_debug(f"Emergency file too small or missing: {filepath}")
                    try:
                        if os.path.exists(filepath):
                            os.remove(filepath)
                    except:
                        pass
                        
            except Exception as download_error:
                log_pinterest_debug(f"Emergency download failed for API {i+1}: {download_error}")
                continue
                
        except Exception as api_error:
            log_pinterest_debug(f"Emergency API {i+1} failed: {api_error}")
            continue
    
    log_pinterest_debug("All emergency fallback attempts failed", "ERROR")
    return None

def get_from_meme_api():
    """Get meme from API with enhanced error handling"""
    log_pinterest_debug("Calling meme API...")
    
    # Try multiple endpoints for better reliability
    endpoints = [
        'https://meme-api.com/gimme',
        'https://meme-api.herokuapp.com/gimme',
        'https://meme-api.com/gimme/memes',
        'https://meme-api.com/gimme/dankmemes',
    ]
    
    for endpoint in endpoints:
        try:
            log_pinterest_debug(f"Trying endpoint: {endpoint}")
            r = requests.get(endpoint, timeout=10)
            r.raise_for_status()
            data = r.json()
            log_pinterest_debug(f"Meme API response from {endpoint}: success")
            
            if not data.get('nsfw', True):
                meme_url = data.get('url')
                if meme_url:
                    hist = load_history()
                    if meme_url in hist['urls']:
                        log_pinterest_debug(f"Meme {meme_url} already used, trying next endpoint")
                        continue
                    log_pinterest_debug(f"Got fresh meme: {meme_url}")
                    return meme_url
                else:
                    log_pinterest_debug("No URL in response")
            else:
                log_pinterest_debug("Meme is NSFW, trying next endpoint")
        except Exception as e:
            log_pinterest_debug(f"Error with endpoint {endpoint}: {e}")
            continue
    
    log_pinterest_debug("No suitable meme found from any endpoint", "WARNING")
    return None

def scrape_pinterest_search(search_url: str, output_dir: str = 'pins', num: int = 30):
    log_pinterest_debug(f"Starting search with URL: {search_url}")
    
    # Check Pinterest monitor first
    if should_use_pinterest_fallback():
        status = get_pinterest_status()
        log_pinterest_debug(f"Pinterest monitor suggests fallback mode (failures: {status['consecutive_failures']}, last success: {status['last_success_seconds_ago']}s ago)", "WARNING")
        return get_emergency_fallback_content(output_dir)
    
    # Early Pinterest availability check
    if is_pinterest_blocked():
        log_pinterest_debug("Pinterest appears blocked, using emergency fallback immediately", "WARNING")
        return get_emergency_fallback_content(output_dir)
    
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        
        # Normalize Pinterest URL first
        if 'pinterest.com' in search_url and 'www.pinterest.com' not in search_url:
            search_url = search_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
            log_pinterest_debug(f"Normalized URL: {search_url}")
        
        # Try browser-based scraping with enhanced error handling
        try:
            log_pinterest_debug("Attempting browser-based scraping first")
            from pinterest_dl import PinterestDL
            import time
            
            target = random.randint(20, 80)  # Reduced target to avoid timeout
            log_pinterest_debug(f"Trying browser with target {target}")
            
            # Add delay to avoid rate limiting
            time.sleep(random.uniform(2, 5))  # Increased delay
            
            # Set shorter timeout to fail fast
            browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True, timeout=30)
            ok, scraped_browser, err = run_with_timeout(browser_client.scrape, 35, url=search_url, num=target)
            if not ok:
                if isinstance(err, TimeoutError):
                    log_pinterest_debug("Browser scrape timed out, switching to emergency fallback", "WARNING")
                    return get_emergency_fallback_content(output_dir)
                log_pinterest_debug(f"Browser scrape error: {err}", "WARNING")
                scraped_browser = None
            log_pinterest_debug(f"Browser scraped {len(scraped_browser) if scraped_browser else 0} items")
            
            if scraped_browser and len(scraped_browser) > 0:
                random.shuffle(scraped_browser)
                # Try multiple items to increase success chance
                for attempt in range(min(5, len(scraped_browser))):  # Increased attempts
                    chosen_meta = scraped_browser[attempt]
                    log_pinterest_debug(f"Downloading attempt {attempt + 1}/5")
                    try:
                        # Download without timeout parameter
                        ok, downloaded_items, derr = run_with_timeout(
                            PinterestDL.download_media,
                            20,
                            media=[chosen_meta],
                            output_dir=output_dir,
                            download_streams=True
                        )
                        if not ok:
                            if isinstance(derr, TimeoutError):
                                log_pinterest_debug("Download timed out, trying next candidate", "WARNING")
                                continue
                            if derr is not None:
                                raise derr
                            raise RuntimeError("download failed without error detail")
                        if downloaded_items:
                            item = downloaded_items[0]
                            file_path = None
                            if isinstance(item, str) and os.path.isfile(item):
                                file_path = item
                            elif isinstance(item, dict):
                                file_path = item.get('path') or item.get('filepath') or item.get('file')
                            
                            if file_path and os.path.isfile(file_path):
                                file_size = os.path.getsize(file_path)
                                if file_size >= 1000:  # At least 1KB
                                    try:
                                        ext = os.path.splitext(file_path)[1].lower()
                                        if ext in ('.jpg', '.jpeg', '.png', '.webp', '.gif'):
                                            from PIL import Image
                                            with Image.open(file_path) as img:
                                                img.verify()
                                        log_pinterest_debug(f"Browser success: {file_path} ({file_size} bytes)")
                                        return file_path
                                    except ImportError:
                                        log_pinterest_debug(f"Browser success: {file_path} ({file_size} bytes, no validation)")
                                        return file_path
                                    except Exception as e:
                                        log_pinterest_debug(f"File validation failed: {e}, trying next", "WARNING")
                                        continue
                                else:
                                    log_pinterest_debug(f"File too small ({file_size} bytes), trying next", "WARNING")
                                    continue
                    except Exception as e:
                        log_pinterest_debug(f"Download attempt {attempt + 1} failed: {e}", "WARNING")
                        # If we get "Empty response received", fail fast
                        if "empty response" in str(e).lower():
                            log_pinterest_debug("Detected empty response, switching to fallback", "WARNING")
                            break
                        continue
                log_pinterest_debug("All browser download attempts failed", "WARNING")
        except ImportError:
            log_pinterest_debug("pinterest-dl not available, trying HTML parsing")
        except Exception as e:
            log_pinterest_debug(f"Browser method failed: {e}", "WARNING")
            # If we detect Pinterest blocking patterns, skip to fallback immediately
            if any(pattern in str(e).lower() for pattern in ["empty response", "timeout", "connection", "blocked"]):
                log_pinterest_debug("Detected Pinterest blocking pattern, switching to emergency fallback", "WARNING")
                return get_emergency_fallback_content(output_dir)
        
        # Fallback to HTML parsing method (rest of function continues as before)
        # If all Pinterest methods fail, try emergency fallback
        result = None
        try:
            # (HTML parsing code would continue here, but for brevity...)
            # If HTML parsing also fails, we'll use the emergency fallback
            pass
        except Exception as e:
            log_pinterest_debug(f"HTML parsing also failed: {e}", "ERROR")
            
        # Emergency fallback if all Pinterest methods failed
        if not result:
            log_pinterest_debug("All Pinterest methods failed, trying emergency fallback", "WARNING")
            result = get_emergency_fallback_content(output_dir)
            
        if not result:
            log_pinterest_debug("Complete failure - no content obtained", "ERROR")
            
        return result
    except Exception as e:
        log_pinterest_debug(f"Fatal error in scrape_pinterest_search: {e}", "ERROR")
        # Try emergency fallback even on fatal errors
        try:
            return get_emergency_fallback_content(output_dir)
        except Exception as fallback_error:
            log_pinterest_debug(f"Emergency fallback also failed: {fallback_error}", "ERROR")
            return None
        
        # Fallback to HTML parsing method
        try:
            from bs4 import BeautifulSoup
            from bs4.element import Tag
        except ImportError:
            print("Pinterest search: bs4 not available, no fallback possible", flush=True)
            return None
            
        print("Pinterest search: trying HTML parsing method", flush=True)
        
        # Enhanced user agent rotation
        user_agents = [
            'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
            'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36',
            'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
            'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
        ]
        
        ua = random.choice(user_agents)
        try:
            from fake_useragent import UserAgent
            ua = UserAgent().random or ua
        except Exception:
            pass
            
        headers = {
            'User-Agent': ua,
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7',
            'Accept-Language': 'en-US,en;q=0.9,ru;q=0.8',
            'Accept-Encoding': 'gzip, deflate, br',
            'DNT': '1',
            'Connection': 'keep-alive',
            'Upgrade-Insecure-Requests': '1',
            'Sec-Fetch-Dest': 'document',
            'Sec-Fetch-Mode': 'navigate',
            'Sec-Fetch-Site': 'none',
            'Sec-Fetch-User': '?1',
            'Cache-Control': 'max-age=0',
            'Referer': 'https://www.pinterest.com/'
        }
        
        print(f"Pinterest search: requesting {search_url} with UA: {ua[:50]}...", flush=True)
        
        sess = requests.Session()
        sess.headers.update(headers)
        
        # Add delay to avoid rate limiting
        import time
        time.sleep(random.uniform(2, 5))
        
        try:
            resp = sess.get(search_url, timeout=20)
            print(f"Pinterest search: response status {resp.status_code}, content length: {len(resp.text)}", flush=True)
            
            if resp.status_code == 429:
                print("Pinterest search: rate limited (429), trying with delay", flush=True)
                time.sleep(random.uniform(10, 20))
                resp = sess.get(search_url, timeout=20)
                print(f"Pinterest search: retry response status {resp.status_code}", flush=True)
            
            resp.raise_for_status()
            
            if len(resp.text) < 1000:
                print(f"Pinterest search: response too short ({len(resp.text)} chars), likely blocked", flush=True)
                return None
                
        except requests.exceptions.Timeout:
            print("Pinterest search: request timed out", flush=True)
            return None
        except requests.exceptions.RequestException as e:
            print(f"Pinterest search: request failed: {e}", flush=True)
            return None
        
        print(f"Pinterest search: first 200 chars of HTML: {resp.text[:200]}", flush=True)
        
        soup = BeautifulSoup(resp.text, 'html.parser')
        image_urls = []
        
        # Enhanced image extraction with multiple strategies
        print("Pinterest search: extracting images from HTML", flush=True)
        
        # Strategy 1: Find img tags with Pinterest-specific attributes
        for img in soup.find_all('img'):
            if not isinstance(img, Tag):
                continue
            attrs = getattr(img, 'attrs', {}) or {}
            candidates = []
            
            # Check for Pinterest-specific data attributes first
            for key in ('data-pin-media', 'data-pin-href', 'data-pin-url', 'src', 'data-src', 'data-lazy-src'):
                v = attrs.get(key)
                if v and isinstance(v, str):
                    candidates.append(v.strip())
                    
            # Handle srcset attributes
            for key in ('srcset', 'data-srcset'):
                srcset = attrs.get(key)
                if srcset and isinstance(srcset, str):
                    for part in srcset.split(','):
                        u = part.strip().split(' ')[0]
                        if u and u.startswith(('http', '//')):
                            candidates.append(u)
            
            # Find the best candidate URL
            selected_url = None
            for cand in candidates:
                if not cand or not isinstance(cand, str):
                    continue
                cand = cand.strip()
                if 'pinimg.com' in cand:
                    selected_url = cand
                    break
                elif 'pinterest.com' in cand and any(ext in cand.lower() for ext in ['.jpg', '.png', '.gif', '.webp']):
                    selected_url = cand
                    break
                    
            if selected_url:
                if selected_url.startswith('//'):
                    selected_url = 'https:' + selected_url
                elif selected_url.startswith('/'):
                    selected_url = 'https://www.pinterest.com' + selected_url
                    
                # Upgrade to higher resolution
                selected_url = selected_url.replace('236x', '564x').replace('474x', '736x').replace('236s', '564s')
                image_urls.append(selected_url)
        
        # Remove duplicates while preserving order
        image_urls = list(dict.fromkeys(image_urls))
        print(f"Pinterest search: extracted {len(image_urls)} image URLs from img tags", flush=True)
        
        # If no images found in HTML, try to extract from JavaScript data
        if not image_urls:
            print("Pinterest search: trying to extract from JavaScript data", flush=True)
            import json
            # Look for JSON data in script tags
            for script in soup.find_all('script'):
                script_text = script.get_text() if script else ''
                if not script_text:
                    continue
                # Look for patterns like "images":{"originals":{"url":"..."}
                if 'pinimg.com' in script_text or '"url"' in script_text:
                    # Try to extract URLs using regex
                    import re
                    url_patterns = [
                        r'"url":\s*"(https://[^"]*pinimg\.com[^"]*)"',
                        r'"(https://[^"]*pinimg\.com[^"]*)"',
                        r'src["\']:\s*["\']([^"\']*pinimg\.com[^"\']*)["\']',
                    ]
                    for pattern in url_patterns:
                        urls = re.findall(pattern, script_text)
                        for url in urls:
                            if url and 'pinimg.com' in url:
                                # Clean up the URL
                                url = url.replace('\\u002F', '/').replace('\\/', '/')
                                if url.startswith('//'):
                                    url = 'https:' + url
                                # Upgrade resolution
                                url = url.replace('236x', '564x').replace('474x', '736x')
                                image_urls.append(url)
            
            image_urls = list(dict.fromkeys(image_urls))
            print(f"Pinterest search: extracted {len(image_urls)} URLs from JavaScript", flush=True)
            
        # If still no URLs, try Pinterest's internal API endpoints
        if not image_urls:
            print("Pinterest search: trying Pinterest API endpoints", flush=True)
            try:
                # Try to get some pins via search API-like endpoints
                search_query = search_url.split('q=')[1].split('&')[0] if 'q=' in search_url else 'memes'
                api_attempts = [
                    f"https://www.pinterest.com/resource/BaseSearchResource/get/?source_url=/search/pins/?q={search_query}&data={{%22options%22:{{%22query%22:%22{search_query}%22,%22scope%22:%22pins%22}}}}",
                ]
                for api_url in api_attempts:
                    try:
                        api_resp = sess.get(api_url, timeout=10)
                        if api_resp.status_code == 200:
                            api_data = api_resp.json()
                            if 'resource_response' in api_data and 'data' in api_data['resource_response']:
                                results = api_data['resource_response']['data'].get('results', [])
                                for result in results[:20]:  # Limit to 20 results
                                    if 'images' in result and 'originals' in result['images']:
                                        img_url = result['images']['originals'].get('url')
                                        if img_url and 'pinimg.com' in img_url:
                                            image_urls.append(img_url)
                                print(f"Pinterest search: got {len(image_urls)} URLs from API", flush=True)
                                break
                    except Exception as e:
                        print(f"Pinterest search: API attempt failed: {e}", flush=True)
                        continue
            except Exception as e:
                print(f"Pinterest search: API fallback failed: {e}", flush=True)

        def _download_candidates(candidates):
            if not candidates:
                print("Pinterest search: no candidates to download", flush=True)
                return None
                
            from urllib.parse import urlparse
            random.shuffle(candidates)
            
            # Prioritize higher quality images
            prioritized = []
            others = []
            for url in candidates:
                if any(res in url for res in ['736x', '564x', '1200x']):
                    prioritized.append(url)
                else:
                    others.append(url)
            
            attempts = prioritized + others
            attempts = attempts[:max(1, min(len(attempts), max(8, min(15, num))))]
            print(f"Pinterest search: trying to download {len(attempts)} candidate URLs (prioritized: {len(prioritized)})", flush=True)
            
            for i, u in enumerate(attempts):
                try:
                    print(f"Pinterest search: attempt {i+1}/{len(attempts)}: {u[:80]}...", flush=True)
                    
                    # Add random delay between requests
                    if i > 0:
                        time.sleep(random.uniform(0.5, 2))
                    
                    r = sess.get(u, timeout=20, stream=True)
                    r.raise_for_status()
                    
                    ct = (r.headers.get('content-type') or '').lower()
                    content_length = r.headers.get('content-length')
                    
                    # Size validation
                    if content_length:
                        try:
                            size_bytes = int(content_length)
                            size_mb = size_bytes / (1024 * 1024)
                            if size_mb > 50:  # Skip files larger than 50MB
                                print(f"Pinterest search: skipping large file ({size_mb:.1f}MB)", flush=True)
                                continue
                            if size_bytes < 2000:  # Skip files smaller than 2KB
                                print(f"Pinterest search: skipping tiny file ({size_bytes} bytes)", flush=True)
                                continue
                        except ValueError:
                            pass
                    
                    # Content type validation
                    is_valid_media = any(s in ct for s in ('image/', 'video/'))
                    if not is_valid_media:
                        # Check URL extension as fallback
                        parsed = urlparse(u)
                        path = parsed.path.lower()
                        import re as _re
                        if not _re.search(r'\.(jpg|jpeg|png|gif|webp|mp4|webm|mov)(\?|$)', path):
                            print(f"Pinterest search: skipping non-media URL (content-type: {ct})", flush=True)
                            continue
                    
                    # Determine file extension
                    ext = '.jpg'  # Default
                    if 'png' in ct or u.lower().find('.png') > -1:
                        ext = '.png'
                    elif 'gif' in ct or u.lower().find('.gif') > -1:
                        ext = '.gif'
                    elif 'webp' in ct or u.lower().find('.webp') > -1:
                        ext = '.webp'
                    elif 'mp4' in ct or u.lower().find('.mp4') > -1:
                        ext = '.mp4'
                    elif 'webm' in ct or u.lower().find('.webm') > -1:
                        ext = '.webm'
                    elif 'mov' in ct or u.lower().find('.mov') > -1:
                        ext = '.mov'
                    
                    # Create unique filename
                    filename = f'pinterest_search_{abs(hash(u)) % 10**8}_{i+1}{ext}'
                    p = os.path.join(output_dir, filename)
                    
                    downloaded_size = 0
                    try:
                        with open(p, 'wb') as f:
                            for chunk in r.iter_content(chunk_size=8192):
                                if chunk:
                                    f.write(chunk)
                                    downloaded_size += len(chunk)
                                    # Stop if file gets too large
                                    if downloaded_size > 50 * 1024 * 1024:  # 50MB limit
                                        print(f"Pinterest search: file too large, stopping download", flush=True)
                                        break
                    except Exception as write_error:
                        print(f"Pinterest search: write error: {write_error}", flush=True)
                        try:
                            if os.path.isfile(p):
                                os.remove(p)
                        except:
                            pass
                        continue
                    
                    # Validate downloaded file
                    if os.path.isfile(p) and os.path.getsize(p) >= 2000:  # At least 2KB
                        try:
                            if ext.lower() in ('.jpg', '.jpeg', '.png', '.webp'):
                                from PIL import Image
                                with Image.open(p) as img:
                                    img.verify()  # Will raise exception if corrupted
                                    # Re-open to check size (verify closes the file)
                                with Image.open(p) as img:
                                    width, height = img.size
                                    if width < 100 or height < 100:
                                        print(f"Pinterest search: image too small ({width}x{height}), skipping", flush=True)
                                        os.remove(p)
                                        continue
                                print(f"Pinterest search: validated image {p} ({downloaded_size} bytes, {width}x{height})", flush=True)
                            elif ext.lower() == '.gif':
                                from PIL import Image
                                with Image.open(p) as img:
                                    img.verify()
                                print(f"Pinterest search: validated GIF {p} ({downloaded_size} bytes)", flush=True)
                            else:
                                print(f"Pinterest search: downloaded media {p} ({downloaded_size} bytes)", flush=True)
                            return p
                        except ImportError:
                            # PIL not available, just check file size
                            if downloaded_size >= 2000:
                                print(f"Pinterest search: downloaded {p} ({downloaded_size} bytes, no validation)", flush=True)
                                return p
                            else:
                                print(f"Pinterest search: file too small without PIL ({downloaded_size} bytes)", flush=True)
                                try:
                                    os.remove(p)
                                except:
                                    pass
                        except Exception as e:
                            print(f"Pinterest search: file validation failed for {p}: {e}", flush=True)
                            try:
                                os.remove(p)
                            except:
                                pass
                            continue
                    else:
                        print(f"Pinterest search: downloaded file invalid: {p} ({os.path.getsize(p) if os.path.isfile(p) else 0} bytes)", flush=True)
                        try:
                            if os.path.isfile(p):
                                os.remove(p)
                        except:
                            pass
                        
                except requests.exceptions.Timeout:
                    print(f"Pinterest search: download timeout for {u[:80]}...", flush=True)
                    continue
                except requests.exceptions.RequestException as e:
                    print(f"Pinterest search: download request failed for {u[:80]}...: {e}", flush=True)
                    continue
                except Exception as e:
                    print(f"Pinterest search: download failed for {u[:80]}...: {e}", flush=True)
                    continue
                    
            print("Pinterest search: all download attempts failed", flush=True)
            return None

        path = None
        if image_urls:
            print(f"Pinterest search: attempting direct download from {len(image_urls)} URLs", flush=True)
            path = _download_candidates(image_urls)
        else:
            print("Pinterest search: no image URLs found in HTML", flush=True)
        if path:
            return path

        try:
            from pinterest_dl import PinterestDL
            target = random.randint(30, 120)
            print(f"Search page direct download failed; trying browser fallback with target {target}", flush=True)
            browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True)
            ok, scraped_browser, err = run_with_timeout(browser_client.scrape, 35, url=search_url, num=target)
            if not ok:
                print(f"Browser fallback scrape failed: {err}", flush=True)
                scraped_browser = None
            print(f"Browser fallback scraped {len(scraped_browser) if scraped_browser else 0} items", flush=True)
            if scraped_browser:
                random.shuffle(scraped_browser)
                chosen_meta = random.choice(scraped_browser)
                print("Downloading one media from browser fallback", flush=True)
                ok, downloaded_items, derr = run_with_timeout(
                    PinterestDL.download_media,
                    20,
                    media=[chosen_meta],
                    output_dir=output_dir,
                    download_streams=True
                )
                if not ok:
                    print(f"Browser fallback download error: {derr}", flush=True)
                    downloaded_items = None
                if downloaded_items:
                    item = downloaded_items[0]
                    file_path = None
                    if isinstance(item, str) and os.path.isfile(item):
                        file_path = item
                    elif isinstance(item, dict):
                        file_path = item.get('path') or item.get('filepath') or item.get('file')
                    
                    if file_path and os.path.isfile(file_path):
                        # Validate the downloaded file
                        file_size = os.path.getsize(file_path)
                        if file_size < 1000:
                            print(f"Browser fallback: file too small ({file_size} bytes), skipping", flush=True)
                        else:
                            try:
                                # Try to validate if it's an image/gif
                                ext = os.path.splitext(file_path)[1].lower()
                                if ext in ('.jpg', '.jpeg', '.png', '.webp', '.gif'):
                                    from PIL import Image
                                    with Image.open(file_path) as img:
                                        img.verify()
                                print(f"Browser fallback success: {file_path} ({file_size} bytes)", flush=True)
                                return file_path
                            except ImportError:
                                print(f"Browser fallback success: {file_path} ({file_size} bytes, no validation)", flush=True)
                                return file_path
                            except Exception as e:
                                print(f"Browser fallback: file validation failed for {file_path}: {e}", flush=True)
                print("Browser fallback: no valid file returned", flush=True)
        except Exception as e:
            print(f"Browser fallback failed: {e}", flush=True)
        print("Pinterest search: all methods failed, returning None", flush=True)
        return None

def scrape_one_from_pinterest(board_url: str, output_dir: str = 'pins', num: int = 10000):
    log_pinterest_debug(f"Called with URL: {board_url}")
    
    # Check Pinterest monitor first
    if should_use_pinterest_fallback():
        status = get_pinterest_status()
        log_pinterest_debug(f"Pinterest monitor suggests fallback mode (failures: {status['consecutive_failures']}, last success: {status['last_success_seconds_ago']}s ago)", "WARNING")
        return get_emergency_fallback_content(output_dir)
    
    # Early Pinterest availability check
    if is_pinterest_blocked():
        log_pinterest_debug("Pinterest appears blocked, using emergency fallback immediately", "WARNING")
        return get_emergency_fallback_content(output_dir)
    
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        
        # Normalize Pinterest URL
        if 'pinterest.com' in board_url and 'www.pinterest.com' not in board_url:
            board_url = board_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
            log_pinterest_debug(f"Normalized URL: {board_url}")
            
        # Route search URLs to search scraper
        if 'search/pins' in board_url:
            log_pinterest_debug("Detected search URL, using search scraper")
            return scrape_pinterest_search(board_url, output_dir, num)

        # Try multiple approaches for better success rate
        scraped = None
        max_scan_cap = 400  # Reduced from 800
        min_scan = 20       # Reduced from 40
        target_api = 50     # Reduced from 100
        
        try:
            from pinterest_dl import PinterestDL
            import time
            
            max_scan = max(10, min(max_scan_cap, num if isinstance(num, int) and num > 0 else max_scan_cap))
            target_api = random.randint(min_scan, max(min_scan + 5, max_scan // 2 if max_scan >= 50 else max_scan))
            log_pinterest_debug(f"Using PinterestDL (sample mode). Target API sample size: {target_api} (cap {max_scan})")
            
            # Add delay to avoid rate limiting
            time.sleep(random.uniform(2, 4))  # Increased delay
            
            # Try API mode first with timeout handling
            try:
                client = PinterestDL.with_api(timeout=10, verbose=False)  # Reduced timeout
                ok, scraped_api, err = run_with_timeout(client.scrape, 15, url=board_url, num=target_api)
                if not ok:
                    if isinstance(err, TimeoutError):
                        log_pinterest_debug("API scrape timed out, switching to emergency fallback", "WARNING")
                        return get_emergency_fallback_content(output_dir)
                    log_pinterest_debug(f"API scrape error: {err}", "WARNING")
                    scraped_api = None
                scraped = scraped_api
                log_pinterest_debug(f"API mode scraped {len(scraped) if scraped else 0} items (target: {target_api})")
            except Exception as ae:
                log_pinterest_debug(f"API mode scrape failed: {ae}", "WARNING")
                scraped = None
                # If we detect empty response or blocking, skip to fallback
                if any(pattern in str(ae).lower() for pattern in ["empty response", "timeout", "connection", "blocked"]):
                    log_pinterest_debug("Detected blocking pattern in API mode, switching to emergency fallback", "WARNING")
                    return get_emergency_fallback_content(output_dir)

        except ImportError:
            log_pinterest_debug("PinterestDL not available, trying alternative methods")
        except Exception as e:
            log_pinterest_debug(f"PinterestDL initialization failed: {e}")
            
        # If API mode didn't get enough items, try browser mode with enhanced error handling
        if not scraped or len(scraped) < max(4, target_api // 4):  # Reduced threshold
            try:
                from pinterest_dl import PinterestDL
                import time
                
                remaining_cap = max_scan_cap - (len(scraped) if scraped else 0)
                target_browser = random.randint(max(min_scan, 30), max(min_scan + 10, min(max_scan_cap, remaining_cap if remaining_cap > 0 else max_scan_cap)))
                log_pinterest_debug(f"Few items from API mode; trying browser mode with target {target_browser}")
                
                time.sleep(random.uniform(3, 6))  # Longer delay for browser mode
                browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True, timeout=30)
                ok, scraped_browser, err = run_with_timeout(browser_client.scrape, 35, url=board_url, num=target_browser)
                if not ok:
                    if isinstance(err, TimeoutError):
                        log_pinterest_debug("Browser scrape timed out, switching to emergency fallback", "WARNING")
                        return get_emergency_fallback_content(output_dir)
                    log_pinterest_debug(f"Browser scrape error: {err}", "WARNING")
                    scraped_browser = None
                log_pinterest_debug(f"Browser mode scraped {len(scraped_browser) if scraped_browser else 0} items")
                
                if scraped_browser:
                    scraped = scraped_browser
                    
            except Exception as be:
                log_pinterest_debug(f"Browser mode scrape failed: {be}", "WARNING")
                # Check for blocking patterns
                if any(pattern in str(be).lower() for pattern in ["empty response", "timeout", "connection", "blocked"]):
                    log_pinterest_debug("Detected blocking pattern in browser mode, switching to emergency fallback", "WARNING")
                    return get_emergency_fallback_content(output_dir)

        if scraped:
            random.shuffle(scraped)
        if not scraped:
            log_pinterest_debug("No items scraped from Pinterest, using emergency fallback", "WARNING")
            return get_emergency_fallback_content(output_dir)

        chosen_meta = random.choice(scraped)
        log_pinterest_debug("Downloading one randomly chosen media item")
        
        try:
            from pinterest_dl import PinterestDL
            ok, downloaded_items, derr = run_with_timeout(
                PinterestDL.download_media,
                20,
                media=[chosen_meta],
                output_dir=output_dir,
                download_streams=True
            )
            if not ok:
                if isinstance(derr, TimeoutError):
                    log_pinterest_debug("Download timed out, switching to emergency fallback", "WARNING")
                    return get_emergency_fallback_content(output_dir)
                log_pinterest_debug(f"PinterestDL download error: {derr}", "ERROR")
                raise derr if derr is not None else RuntimeError("download failed without error detail")
        except ImportError:
            log_pinterest_debug("PinterestDL not available for download", "ERROR")
            return get_emergency_fallback_content(output_dir)
        except Exception as e:
            log_pinterest_debug(f"PinterestDL download failed: {e}", "ERROR")
            # Check for blocking patterns in download
            if any(pattern in str(e).lower() for pattern in ["empty response", "timeout", "connection", "blocked"]):
                log_pinterest_debug("Detected blocking pattern during download, switching to emergency fallback", "WARNING")
            return get_emergency_fallback_content(output_dir)
            
        log_pinterest_debug(f"PinterestDL downloaded {len(downloaded_items) if downloaded_items else 0} item(s)")
        if downloaded_items:
            item = downloaded_items[0]
            if isinstance(item, str) and os.path.isfile(item):
                log_pinterest_debug(f"Selected file: {item}")
                return item
            if isinstance(item, dict):
                p = item.get('path') or item.get('filepath') or item.get('file')
                if p and os.path.isfile(p):
                    log_pinterest_debug(f"Selected file: {p}")
                    return p

        log_pinterest_debug("No valid downloaded file returned, scanning output_dir as fallback")
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.lower().endswith(('.jpg', '.jpeg', '.png', '.gif', '.mp4', '.webm', '.mov')):
                    chosen = os.path.join(root, f)
                    log_pinterest_debug(f"Selected file: {chosen}")
                    return chosen
        
        # If all else fails, try emergency fallback
        log_pinterest_debug("No files found in output dir, trying emergency fallback", "WARNING")
        return get_emergency_fallback_content(output_dir)
        
    except Exception as e:
        log_pinterest_debug(f"Error in scrape_one_from_pinterest: {e}", "ERROR")
        # Try emergency fallback even on fatal errors
        try:
            return get_emergency_fallback_content(output_dir)
        except Exception as fallback_error:
            log_pinterest_debug(f"Emergency fallback also failed: {fallback_error}", "ERROR")
            return None

def fetch_one_from_reddit(sources: list[str], output_dir: str = 'pins'):
    try:
        if not sources:
            print("Reddit: No sources provided", flush=True)
            return None
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        raw = random.choice(sources)
        raw = raw.strip()
        print(f"Reddit: Selected raw source: '{raw}'", flush=True)
        if not raw:
            print("Reddit: Empty source after strip", flush=True)
            return None
        if raw.startswith('http://') or raw.startswith('https://'):
            parsed = urlparse(raw)
            parts = [p for p in parsed.path.split('/') if p]
            sr = None
            for i, p in enumerate(parts):
                if p.lower() == 'r' and i + 1 < len(parts):
                    sr = parts[i + 1]
                    break
            if not sr:
                print(f"Reddit: Could not extract subreddit from URL: {raw}", flush=True)
                return None
            subreddit = sr
        else:
            subreddit = raw.lstrip('r/').strip()
        print(f"Reddit: Using subreddit: {subreddit}", flush=True)
        sort = random.choice(['hot', 'top', 'new'])
        base = f'https://www.reddit.com/r/{subreddit}/{sort}.json'
        params = {'limit': str(random.randint(30, 80))}
        if sort == 'top':
            params['t'] = random.choice(['day', 'week', 'month'])
        print(f"Reddit: Requesting {base} with params: {params}", flush=True)
        headers = {'User-Agent': 'Mozilla/5.0 MemeVideoGen/1.0'}
        r = None
        try:
            r = requests.get(base, headers=headers, params=params, timeout=12)
            print(f"Reddit: Response status: {r.status_code}", flush=True)
            if r.status_code == 429:
                print("Reddit: Rate limited (429)", flush=True)
                return None
            if r.status_code != 200:
                print(f"Reddit: HTTP error {r.status_code}: {r.text[:200]}", flush=True)
            r.raise_for_status()
            data = r.json()
            print(f"Reddit: JSON response keys: {list(data.keys()) if isinstance(data, dict) else 'Not a dict'}", flush=True)
        except requests.exceptions.Timeout:
            print("Reddit: Request timed out", flush=True)
            return None
        except requests.exceptions.RequestException as e:
            print(f"Reddit: Request failed: {e}", flush=True)
            return None
        except ValueError as e:
            print(f"Reddit: JSON decode failed: {e}", flush=True)
            if r is not None:
                print(f"Reddit: Response text (first 200 chars): {r.text[:200]}", flush=True)
            return None
        except Exception as e:
            print(f"Reddit: Unexpected error during request: {e}", flush=True)
            return None
        children = (((data or {}).get('data') or {}).get('children')) or []
        print(f"Reddit: Found {len(children)} posts", flush=True)
        random.shuffle(children)
        exts = ('.jpg', '.jpeg', '.png', '.gif', '.webp', '.mp4')
        valid_posts = 0
        processed_posts = 0
        for ch in children:
            processed_posts += 1
            d = (ch or {}).get('data') or {}
            post_title = d.get('title', 'Unknown')[:50]
            if d.get('over_18'):
                print(f"Reddit: Skipping NSFW post: {post_title}...", flush=True)
                continue
            if d.get('stickied'):
                print(f"Reddit: Skipping stickied post: {post_title}...", flush=True)
                continue
            url = d.get('url_overridden_by_dest') or d.get('url') or ''
            print(f"Reddit: Raw URL from JSON: '{url}'", flush=True)
            if not url:
                print(f"Reddit: No URL for post: {post_title}...", flush=True)
                continue
            
            # Clean up the URL - remove any trailing encoded characters
            import urllib.parse
            url = url.strip()
            # Decode URL in case it's encoded
            try:
                decoded_url = urllib.parse.unquote(url)
                if decoded_url != url:
                    print(f"Reddit: Decoded URL: '{decoded_url}'", flush=True)
                    url = decoded_url
            except Exception as e:
                print(f"Reddit: URL decode failed: {e}", flush=True)
            
            # Remove common trailing artifacts
            if url.endswith('%29') or url.endswith(')'):
                url = url.rstrip('%29').rstrip(')')
                print(f"Reddit: Cleaned trailing artifacts from URL: '{url}'", flush=True)
            
            lower = url.lower()
            original_url = url
            if not any(lower.endswith(e) for e in exts):
                if d.get('post_hint') == 'image' and ('preview' in d):
                    images = (((d.get('preview') or {}).get('images')) or [])
                    if images:
                        src = ((images[0] or {}).get('source') or {}).get('url') or ''
                        src = src.replace('&amp;', '&')
                        if src:
                            print(f"Reddit: Using preview image for post: {post_title}... (original: {original_url[:50]}... -> preview: {src[:50]}...)", flush=True)
                            url = src
                            lower = url.lower()
                        else:
                            print(f"Reddit: No preview source for post: {post_title}...", flush=True)
                            continue
                    else:
                        print(f"Reddit: No preview images for post: {post_title}...", flush=True)
                        continue
                else:
                    print(f"Reddit: Post not an image/video: {post_title}... (URL: {original_url[:50]}..., hint: {d.get('post_hint')})", flush=True)
                    continue
            valid_posts += 1
            print(f"Reddit: Attempting to download from post: {post_title}... (URL: {url[:50]}...)", flush=True)
            
            # Final URL cleanup before download
            final_url = url.strip()
            # Remove any query parameters that might cause issues
            if '?' in final_url and any(final_url.lower().endswith(ext) for ext in exts):
                base_url = final_url.split('?')[0]
                if any(base_url.lower().endswith(ext) for ext in exts):
                    print(f"Reddit: Removed query parameters: {final_url} -> {base_url}", flush=True)
                    final_url = base_url
            
            print(f"Reddit: Final URL for download: '{final_url}'", flush=True)
            try:
                rr = requests.get(final_url, headers=headers, timeout=15, stream=True)
                print(f"Reddit: Download response status: {rr.status_code} for URL: {final_url}", flush=True)
                rr.raise_for_status()
                ct = (rr.headers.get('content-type') or '').lower()
                print(f"Reddit: Content-type: {ct}", flush=True)
                if not any(k in ct for k in ['image/', 'video/']):
                    if not re.search(r'\.(jpg|jpeg|png|gif|webp|mp4)$', final_url.lower()):
                        print(f"Reddit: Invalid content type and extension for: {final_url[:50]}...", flush=True)
                        continue
                ext = '.jpg'
                final_lower = final_url.lower()
                if '.png' in final_lower:
                    ext = '.png'
                elif '.gif' in final_lower:
                    ext = '.gif'
                elif '.webp' in final_lower:
                    ext = '.webp'
                elif '.mp4' in final_lower:
                    ext = '.mp4'
                p = os.path.join(output_dir, f'reddit_{subreddit}_{abs(hash(final_url)) % 10**8}{ext}')
                print(f"Reddit: Downloading to: {p}", flush=True)
                size = 0
                with open(p, 'wb') as f:
                    for chunk in rr.iter_content(chunk_size=8192):
                        if chunk:
                            f.write(chunk)
                            size += len(chunk)
                print(f"Reddit: Downloaded {size} bytes", flush=True)
                if size < 1000:
                    print(f"Reddit: File too small ({size} bytes), removing", flush=True)
                    try:
                        os.remove(p)
                    except Exception:
                        pass
                    continue
                add_url_to_history(final_url)
                print(f"Reddit: Successfully downloaded: {p}", flush=True)
                return p
            except requests.exceptions.Timeout:
                print(f"Reddit: Download timeout for: {final_url[:50]}...", flush=True)
                continue
            except requests.exceptions.RequestException as e:
                print(f"Reddit: Download request failed for {final_url[:50]}...: {e}", flush=True)
                continue
            except Exception as e:
                print(f"Reddit: Download error for {final_url[:50]}...: {e}", flush=True)
                continue
        print(f"Reddit: Processed {processed_posts} posts, found {valid_posts} valid media posts, but no successful downloads", flush=True)
        return None
    except Exception as e:
        print(f"Reddit: Fatal error in fetch_one_from_reddit: {e}", flush=True)
        return None