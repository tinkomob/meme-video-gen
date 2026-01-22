import os
import random
import re
import time
from pathlib import Path
from urllib.parse import urlparse
import requests
import threading
import asyncio
from typing import Any
import urllib3

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

_LOG_SUPPRESS_WINDOW = 15
_EMPTY_RESP_STATE = {"last": 0.0, "suppressed": 0}
from .utils import load_history, add_url_to_history
from .pinterest_monitor import should_use_pinterest_fallback, get_pinterest_status
from .config import DEFAULT_PINS_DIR

# Twitter caching and rate limiting (1 request per 15 minutes on Free plan)
_TWITTER_CACHE_FILE = 'twitter_cache.json'
_TWITTER_RATE_WINDOW_SECONDS = 15 * 60

def _now_ts() -> float:
    return time.time()

def _load_twitter_cache() -> dict:
    try:
        import json
        if os.path.isfile(_TWITTER_CACHE_FILE):
            with open(_TWITTER_CACHE_FILE, 'r', encoding='utf-8') as f:
                return json.load(f) or {}
    except Exception:
        pass
    return {"next_allowed_ts": 0.0, "users": {}, "media": []}

def _save_twitter_cache(data: dict):
    try:
        import json
        tmp = _TWITTER_CACHE_FILE + '.tmp'
        with open(tmp, 'w', encoding='utf-8') as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
        os.replace(tmp, _TWITTER_CACHE_FILE)
    except Exception:
        pass

def _twitter_cache_put_user(username: str, user_id: str):
    data = _load_twitter_cache()
    users = data.get('users') or {}
    users[username.lower()] = {"id": str(user_id), "ts": _now_ts()}
    data['users'] = users
    _save_twitter_cache(data)

def _twitter_cache_get_user(username: str) -> str | None:
    data = _load_twitter_cache()
    users = data.get('users') or {}
    info = users.get(username.lower())
    return (info or {}).get('id') if info else None

def _twitter_cache_add_media(username: str, urls: list[str]):
    if not urls:
        return
    data = _load_twitter_cache()
    media = data.get('media') or []
    ts = _now_ts()
    for u in urls:
        media.append({"username": username.lower(), "url": u, "ts": ts})
    # de-dup, keep latest
    seen = set()
    dedup = []
    for m in reversed(media):
        key = m.get('url')
        if key and key not in seen:
            seen.add(key)
            dedup.append(m)
    data['media'] = list(reversed(dedup))[-500:]  # cap cache size
    _save_twitter_cache(data)

def _twitter_cache_pop_candidate(exclude_urls: set[str]) -> tuple[str | None, str | None]:
    data = _load_twitter_cache()
    media = data.get('media') or []
    for i, m in enumerate(media):
        url = (m or {}).get('url')
        if not url or url in exclude_urls:
            continue
        # pop and save
        picked = media.pop(i)
        data['media'] = media
        _save_twitter_cache(data)
        return picked.get('username'), url
    return None, None

def _twitter_rate_next_allowed() -> float:
    data = _load_twitter_cache()
    return float(data.get('next_allowed_ts') or 0.0)

def _twitter_rate_mark_window(reset_epoch: float | None = None):
    data = _load_twitter_cache()
    now = _now_ts()
    if reset_epoch and reset_epoch > now:
        data['next_allowed_ts'] = float(reset_epoch)
    else:
        data['next_allowed_ts'] = now + _TWITTER_RATE_WINDOW_SECONDS
    _save_twitter_cache(data)

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
                # Check for driver issues
                if err and "unable to obtain driver" in str(err).lower():
                    log_pinterest_debug("Chrome driver not available, skipping browser mode", "WARNING")
                    scraped_browser = None
                else:
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

        
        
        try:
            log_pinterest_debug("Trying Selenium scroll first", "INFO")
            from selenium import webdriver
            from selenium.webdriver.common.by import By
            from selenium.webdriver.chrome.service import Service as ChromeService
            from selenium.webdriver.support.ui import WebDriverWait
            from selenium.webdriver.support import expected_conditions as EC

            user_agents = [
                'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
                'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36',
                'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
                'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
            ]
            ua_s = random.choice(user_agents)
            try:
                from fake_useragent import UserAgent
                ua_s = UserAgent().random or ua_s
            except Exception:
                pass

            chrome_options = webdriver.ChromeOptions()
            chrome_options.add_argument("--headless=new")
            chrome_options.add_argument("--no-sandbox")
            chrome_options.add_argument("--disable-dev-shm-usage")
            chrome_options.add_argument("--disable-gpu")
            chrome_options.add_argument("--window-size=1920,1080")
            chrome_options.add_argument("--disable-blink-features=AutomationControlled")
            chrome_options.add_argument(f"--user-agent={ua_s}")
            try:
                chrome_options.binary_location = "/usr/bin/google-chrome-stable"
            except Exception:
                pass

            service = ChromeService(executable_path="/usr/local/bin/chromedriver")
            driver = None
            try:
                driver = webdriver.Chrome(service=service, options=chrome_options)
                driver.set_page_load_timeout(25)
                driver.get(search_url)
                WebDriverWait(driver, 15).until(lambda d: d.execute_script("return document.readyState") == "complete")
                try:
                    WebDriverWait(driver, 5).until(EC.presence_of_element_located((By.TAG_NAME, "body")))
                except Exception:
                    pass
                import time as _t
                
                # Initial aggressive scroll to trigger lazy loading
                try:
                    for _ in range(5):
                        driver.execute_script("window.scrollBy(0, window.innerHeight);")
                        _t.sleep(0.3)
                except Exception:
                    pass
                _t.sleep(2.0)
                
                image_urls_scroll = []
                seen = set()
                # Now do random deep scrolls
                num_scrolls = random.randint(3, 6)
                for scroll_idx in range(num_scrolls):
                    try:
                        total_h = driver.execute_script("return Math.max(document.body.scrollHeight, document.documentElement.scrollHeight)") or 0
                    except Exception:
                        total_h = 0
                    target_y = 0
                    if total_h and isinstance(total_h, (int, float)):
                        # Progressively scroll deeper with randomness
                        min_pos = 0.3 + (scroll_idx * 0.1)
                        max_pos = min(0.95, 0.5 + (scroll_idx * 0.15))
                        target_y = int(random.uniform(min_pos, max_pos) * float(total_h))
                    try:
                        driver.execute_script("window.scrollTo(0, arguments[0]);", target_y)
                    except Exception:
                        pass
                    _t.sleep(random.uniform(2.0, 3.5))
                    try:
                        imgs = driver.find_elements(By.CSS_SELECTOR, 'img[src*="pinimg.com"], img[data-pin-media], img[srcset]')
                        for el in imgs:
                            src = el.get_attribute("src") or ""
                            srcset = el.get_attribute("srcset") or ""
                            cand = None
                            if src and "pinimg.com" in src:
                                cand = src
                            elif srcset:
                                parts = [p.strip().split(" ")[0] for p in srcset.split(",") if p.strip()]
                                for p in parts:
                                    if "pinimg.com" in p:
                                        cand = p
                                        break
                            if cand and cand not in seen:
                                seen.add(cand)
                                cand = cand.replace('236x', '564x').replace('474x', '736x').replace('236s', '564s')
                                image_urls_scroll.append(cand)
                    except Exception:
                        continue
                if image_urls_scroll:
                    image_urls_scroll = list(dict.fromkeys(image_urls_scroll))
                    log_pinterest_debug(f"Selenium scroll found {len(image_urls_scroll)} image URLs", "INFO")
                    def _dl_one(url: str):
                        try:
                            headers = {
                                'User-Agent': ua_s,
                                'Accept': '*/*',
                                'Accept-Language': 'en-US,en;q=0.9,ru;q=0.8'
                            }
                            rr = requests.get(url, headers=headers, timeout=20, stream=True)
                            rr.raise_for_status()
                            ct = (rr.headers.get('content-type') or '').lower()
                            from urllib.parse import urlparse as _uparse
                            path = _uparse(url).path.lower()
                            ok_ct = ('image/' in ct) or ('video/' in ct)
                            import re as _re
                            if not ok_ct and not _re.search(r'\.(jpg|jpeg|png|gif|webp|mp4|webm|mov)(\?|$)', path):
                                return None
                            ext = '.jpg'
                            if 'png' in ct or '.png' in path:
                                ext = '.png'
                            elif 'gif' in ct or '.gif' in path:
                                ext = '.gif'
                            elif 'webp' in ct or '.webp' in path:
                                ext = '.webp'
                            elif 'mp4' in ct or '.mp4' in path:
                                ext = '.mp4'
                            elif 'webm' in ct or '.webm' in path:
                                ext = '.webm'
                            elif 'mov' in ct or '.mov' in path:
                                ext = '.mov'
                            fn = f'pinterest_search_selenium_{abs(hash(url)) % 10**8}{ext}'
                            pth = os.path.join(output_dir, fn)
                            size = 0
                            with open(pth, 'wb') as f:
                                for chunk in rr.iter_content(chunk_size=8192):
                                    if chunk:
                                        f.write(chunk)
                                        size += len(chunk)
                                        if size > 50 * 1024 * 1024:
                                            break
                            if not os.path.isfile(pth) or os.path.getsize(pth) < 2000:
                                try:
                                    if os.path.exists(pth):
                                        os.remove(pth)
                                except Exception:
                                    pass
                                return None
                            try:
                                if ext in ('.jpg', '.jpeg', '.png', '.webp'):
                                    from PIL import Image
                                    with Image.open(pth) as img:
                                        img.verify()
                                        w, h = Image.open(pth).size
                                        if w < 100 or h < 100:
                                            os.remove(pth)
                                            return None
                                return pth
                            except ImportError:
                                return pth
                            except Exception:
                                try:
                                    os.remove(pth)
                                except Exception:
                                    pass
                                return None
                        except Exception:
                            return None
                    try:
                        chosen = random.choice(image_urls_scroll)
                        path = _dl_one(chosen)
                        if not path and len(image_urls_scroll) > 1:
                            backup = random.choice([u for u in image_urls_scroll if u != chosen])
                            path = _dl_one(backup)
                        if path:
                            log_pinterest_debug(f"Selenium scroll download success: {path}", "INFO")
                            return path
                    except Exception:
                        pass
                else:
                    log_pinterest_debug("Selenium scroll collected 0 URLs", "WARNING")
            except Exception as se_err:
                log_pinterest_debug(f"Selenium scroll first attempt failed: {se_err}", "WARNING")
            finally:
                if driver:
                    try:
                        driver.quit()
                    except Exception:
                        pass
        except Exception:
            pass

        # Fallback to HTML parsing method
        log_pinterest_debug("Starting HTML parsing fallback", "INFO")
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
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
            'Accept-Language': 'en-US,en;q=0.9,ru;q=0.8',
            'Accept-Encoding': 'gzip, deflate',
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
            
            # Ensure proper decoding
            if resp.apparent_encoding and resp.apparent_encoding != resp.encoding:
                resp.encoding = resp.apparent_encoding
            
            print(f"Pinterest search: response status {resp.status_code}, content length: {len(resp.content)}, encoding: {resp.encoding}", flush=True)
            
            if resp.status_code == 429:
                print("Pinterest search: rate limited (429), trying with delay", flush=True)
                time.sleep(random.uniform(10, 20))
                resp = sess.get(search_url, timeout=20)
                print(f"Pinterest search: retry response status {resp.status_code}", flush=True)
            
            resp.raise_for_status()
            
            # Check if response is valid HTML/text
            content_type = resp.headers.get('content-type', '').lower()
            if 'text/html' not in content_type and 'application/json' not in content_type:
                print(f"Pinterest search: unexpected content-type: {content_type}", flush=True)
            
            html_content = resp.text
            if len(html_content) < 1000:
                print(f"Pinterest search: response too short ({len(html_content)} chars), likely blocked", flush=True)
                return None
            
            # Check if content looks like valid HTML
            if not any(tag in html_content[:1000].lower() for tag in ['<!doctype', '<html', '<head', '<body', '<script']):
                print(f"Pinterest search: response doesn't look like HTML (first 200 chars): {html_content[:200]}", flush=True)
                # Try to decode as bytes if it appears to be compressed
                if html_content and (html_content.startswith('\x1f\x8b') or html_content.startswith('n`') or ord(html_content[0]) < 32):
                    print("Pinterest search: response appears compressed/binary, attempting decode", flush=True)
                    try:
                        import gzip
                        import brotli
                        raw = resp.content
                        if raw[:2] == b'\x1f\x8b':
                            html_content = gzip.decompress(raw).decode('utf-8', errors='replace')
                        else:
                            try:
                                html_content = brotli.decompress(raw).decode('utf-8', errors='replace')
                            except Exception:
                                html_content = raw.decode('utf-8', errors='replace')
                        print(f"Pinterest search: successfully decoded response, new length: {len(html_content)}", flush=True)
                    except Exception as decompress_error:
                        print(f"Pinterest search: failed to decompress: {decompress_error}", flush=True)
                        # Try curl_cffi as a last resort
                        try:
                            from curl_cffi import requests as curl_requests
                            ch = curl_requests.get(search_url, headers=headers, impersonate="chrome", timeout=20)
                            ch.raise_for_status()
                            html_content = ch.text
                            print(f"Pinterest search: curl_cffi fetched HTML of length {len(html_content)}", flush=True)
                        except Exception as curl_err:
                            print(f"Pinterest search: curl_cffi fallback failed: {curl_err}", flush=True)
                            return None
                else:
                    # Try curl_cffi even if it's not clearly compressed, content may be obfuscated
                    try:
                        from curl_cffi import requests as curl_requests
                        ch = curl_requests.get(search_url, headers=headers, impersonate="chrome", timeout=20)
                        ch.raise_for_status()
                        html_content = ch.text
                        print(f"Pinterest search: curl_cffi fetched HTML of length {len(html_content)}", flush=True)
                    except Exception as curl_err:
                        print(f"Pinterest search: curl_cffi fallback failed: {curl_err}", flush=True)
                        return None
                
        except requests.exceptions.Timeout:
            print("Pinterest search: request timed out", flush=True)
            return None
        except requests.exceptions.RequestException as e:
            print(f"Pinterest search: request failed: {e}", flush=True)
            return None
        
        print(f"Pinterest search: first 200 chars of HTML: {html_content[:200]}", flush=True)
        
        soup = BeautifulSoup(html_content, 'html.parser')
        image_urls = []

        def _filter_media_urls(urls: list[str]) -> list[str]:
            kept = []
            seen = set()
            host_re = re.compile(r'^https?://(?:(?:i\d?|i|v)\.)?pinimg\.com/', re.IGNORECASE)
            ext_re = re.compile(r'\.(jpg|jpeg|png|gif|webp|mp4|webm|mov)(\?|$)', re.IGNORECASE)
            for u in urls or []:
                if not u or not isinstance(u, str):
                    continue
                u = u.strip()
                if not host_re.match(u):
                    continue
                u = u.replace('236x', '564x').replace('474x', '736x').replace('236s', '564s')
                if ext_re.search(u) or any(seg in u for seg in ['/736x/', '/564x/', '/originals/', '/videos/']):
                    if u not in seen:
                        seen.add(u)
                        kept.append(u)
            return kept
        
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
        if image_urls:
            before = len(image_urls)
            image_urls = _filter_media_urls(image_urls)
            print(f"Pinterest search: filtered img-tag URLs to {len(image_urls)} from {before}", flush=True)
        
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
                    import re as _re
                    url_patterns = [
                        r'"url":\s*"(https://[^"]*pinimg\.com[^"]*)"',
                        r'"(https://[^"]*pinimg\.com[^"]*)"',
                        r'src["\']:\s*["\']([^"\']*pinimg\.com[^"\']*)["\']',
                    ]
                    for pattern in url_patterns:
                        urls = _re.findall(pattern, script_text)
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
            if image_urls:
                before_js = len(image_urls)
                image_urls = _filter_media_urls(image_urls)
                print(f"Pinterest search: filtered JS URLs to {len(image_urls)} from {before_js}", flush=True)
            
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

        def _download_candidates(candidates, max_attempts: int | None = None):
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
            if max_attempts is not None:
                random.shuffle(attempts)
                attempts = attempts[:max(1, max_attempts)]
            else:
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
            log_pinterest_debug(f"HTML parsing found {len(image_urls)} image URLs, attempting download", "INFO")
            try:
                chosen = random.choice(image_urls)
                log_pinterest_debug("Selecting one random image from HTML results", "INFO")
                path = _download_candidates([chosen], max_attempts=1)
                if not path and len(image_urls) > 1:
                    backup = random.choice([u for u in image_urls if u != chosen])
                    log_pinterest_debug("First random image failed, trying one more", "WARNING")
                    path = _download_candidates([backup], max_attempts=1)
            except Exception as _e:
                path = _download_candidates(image_urls)
        else:
            log_pinterest_debug("HTML parsing: no image URLs found in page", "WARNING")
        if path:
            log_pinterest_debug(f"HTML parsing successful: {path}", "INFO")
            return path

        

        try:
            from pinterest_dl import PinterestDL
            target = random.randint(30, 120)
            print(f"Search page direct download failed; trying browser fallback with target {target}", flush=True)
            browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True)
            ok, scraped_browser, err = run_with_timeout(browser_client.scrape, 35, url=search_url, num=target)
            if not ok:
                # Check for driver issues
                if err and "unable to obtain driver" in str(err).lower():
                    print(f"Browser fallback: Chrome driver not available, skipping", flush=True)
                    scraped_browser = None
                else:
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
        log_pinterest_debug("HTML parsing and browser fallback all failed", "WARNING")
        return None
    except Exception as e:
        log_pinterest_debug(f"Fatal error in scrape_pinterest_search: {e}", "ERROR")
        try:
            return get_emergency_fallback_content(output_dir)
        except Exception as fallback_error:
            log_pinterest_debug(f"Emergency fallback also failed: {fallback_error}", "ERROR")
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

        # Route ideas URLs to search scraper by extracting topic
        if '/ideas/' in board_url:
            log_pinterest_debug("Detected ideas URL, converting to search")
            parts = board_url.split('/')
            if len(parts) >= 5 and parts[3] == 'ideas':
                topic = parts[4].replace('-', ' ')
                import urllib.parse
                encoded_topic = urllib.parse.quote(topic)
                search_url = f"https://www.pinterest.com/search/pins/?q={encoded_topic}"
                log_pinterest_debug(f"Converted ideas URL to search: {search_url}")
                return scrape_pinterest_search(search_url, output_dir, num)

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
                    # Check for driver issues
                    if err and "unable to obtain driver" in str(err).lower():
                        log_pinterest_debug("Chrome driver not available, skipping browser mode", "WARNING")
                        scraped_browser = None
                    else:
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

def fetch_one_from_twitter(sources: list[str], output_dir: str = 'pins'):
    try:
        if not sources:
            print("Twitter: No sources provided", flush=True)
            return None
        Path(output_dir).mkdir(parents=True, exist_ok=True)

        # Optional API client
        try:
            import tweepy
        except ImportError:
            tweepy = None

        from .config import X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET, X_BEARER_TOKEN
        bearer_token = X_BEARER_TOKEN
        has_oauth = all([X_CONSUMER_KEY, X_CONSUMER_SECRET, X_ACCESS_TOKEN, X_ACCESS_TOKEN_SECRET])

        def _parse_user(raw: str) -> tuple[str | None, str | None]:
            uname = None
            uid = None
            if raw.startswith('http://') or raw.startswith('https://'):
                parsed = urlparse(raw)
                parts = [p for p in parsed.path.split('/') if p]
                if parts:
                    if len(parts) >= 3 and parts[0] == 'i' and parts[1] == 'user' and parts[2].isdigit():
                        uid = parts[2]
                    else:
                        uname = parts[0].lstrip('@')
            else:
                m = re.match(r'^id:(\d+)$', raw)
                if m:
                    uid = m.group(1)
                elif raw.isdigit():
                    uid = raw
                else:
                    uname = raw.lstrip('@').strip()
            return uname, uid

        source_list = [s.strip() for s in sources if isinstance(s, str) and s.strip()]
        random.shuffle(source_list)

        client = None
        api_used = False
        hist = load_history()
        exclude = set((hist or {}).get('urls') or [])

        # Iterate through sources sequentially, skip on error
        for raw in source_list:
            raw = raw.strip()
            print(f"Twitter: Selected source: '{raw}'", flush=True)
            username, user_id_override = _parse_user(raw)
            if not username and not user_id_override:
                print(f"Twitter: Could not extract username from: {raw}", flush=True)
                continue
            if user_id_override:
                print(f"Twitter: Using user id: {user_id_override}", flush=True)
            else:
                print(f"Twitter: Using username: @{username}", flush=True)

            # Try cached media first
            cu, cached_url = _twitter_cache_pop_candidate(exclude_urls=exclude)
            if cached_url:
                print(f"Twitter: Using cached media from @{cu}: {cached_url[:80]}...", flush=True)
                candidates = [{"url": cached_url, "tweet_id": "cached"}]
            else:
                candidates = []

            # Try API if no cached media and credentials available
            if not candidates and tweepy and (bearer_token or has_oauth) and not api_used:
                try:
                    if bearer_token:
                        print("Twitter: Using Bearer Token authentication", flush=True)
                        client = tweepy.Client(bearer_token=bearer_token)
                    else:
                        print("Twitter: Using OAuth 1.0a authentication", flush=True)
                        client = tweepy.Client(
                            consumer_key=X_CONSUMER_KEY,
                            consumer_secret=X_CONSUMER_SECRET,
                            access_token=X_ACCESS_TOKEN,
                            access_token_secret=X_ACCESS_TOKEN_SECRET,
                        )
                    api_used = True

                    user_id = user_id_override
                    if not user_id and username:
                        user_id = _twitter_cache_get_user(username)

                    fresh_urls: list[str] = []
                    
                    if not user_id and username:
                        print(f"Twitter: Fetching user info for @{username}", flush=True)
                        user_response = client.get_user(username=username)
                        udata = getattr(user_response, 'data', None) if user_response else None
                        if udata:
                            user_id = str(getattr(udata, 'id', ''))
                            _twitter_cache_put_user(username, user_id)
                            print(f"Twitter: User ID: {user_id}", flush=True)

                    if user_id:
                        print(f"Twitter: Fetching tweets with media from user_id={user_id}", flush=True)
                        tweets_response = client.get_users_tweets(
                            id=user_id,
                            max_results=100,
                            exclude=['retweets', 'replies'],
                            tweet_fields=['attachments', 'possibly_sensitive'],
                            media_fields=['url', 'type'],
                            expansions=['attachments.media_keys']
                        )
                        if tweets_response and getattr(tweets_response, 'data', None):
                            tweets = getattr(tweets_response, 'data', [])
                            media_dict = {}
                            inc = getattr(tweets_response, 'includes', None)
                            if inc and isinstance(inc, dict) and 'media' in inc:
                                for media in inc['media']:
                                    media_dict[getattr(media, 'media_key', None)] = media
                            for tweet in tweets:
                                if getattr(tweet, 'possibly_sensitive', False):
                                    continue
                                atts = getattr(tweet, 'attachments', None) or {}
                                media_keys = atts.get('media_keys', [])
                                for mk in media_keys:
                                    m = media_dict.get(mk)
                                    if m and getattr(m, 'type', None) == 'photo':
                                        mu = getattr(m, 'url', None)
                                        if mu and mu not in exclude:
                                            fresh_urls.append(mu)

                    if fresh_urls:
                        print(f"Twitter: Collected {len(fresh_urls)} fresh URLs", flush=True)
                        _twitter_cache_add_media(username or 'twitter', fresh_urls)
                        _, cached_url = _twitter_cache_pop_candidate(exclude_urls=exclude)
                        if cached_url:
                            candidates = [{"url": cached_url, "tweet_id": "cached"}]

                except Exception as e:
                    print(f"Twitter: API error for @{username}: {e}", flush=True)
                    _twitter_rate_mark_window()
                    continue

            # Try to download candidates
            if candidates:
                print(f"Twitter: Found {len(candidates)} candidate images", flush=True)
                random.shuffle(candidates)
                headers = {'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'}
                
                for candidate in candidates[:5]:
                    media_url = candidate['url']
                    print(f"Twitter: Downloading: {media_url[:50]}...", flush=True)
                    try:
                        r = requests.get(media_url, headers=headers, timeout=15, stream=True)
                        r.raise_for_status()
                        ct = (r.headers.get('content-type') or '').lower()
                        
                        if 'image/' not in ct:
                            print(f"Twitter: Invalid content type: {ct}", flush=True)
                            continue
                        
                        ext = '.jpg'
                        if 'png' in ct or media_url.lower().endswith('.png'):
                            ext = '.png'
                        elif 'gif' in ct or media_url.lower().endswith('.gif'):
                            ext = '.gif'
                        elif 'webp' in ct or media_url.lower().endswith('.webp'):
                            ext = '.webp'
                        
                        filename = f'twitter_{username or "media"}_{abs(hash(media_url)) % 10**6}{ext}'
                        p = os.path.join(output_dir, filename)
                        
                        size = 0
                        with open(p, 'wb') as f:
                            for chunk in r.iter_content(chunk_size=8192):
                                if chunk:
                                    f.write(chunk)
                                    size += len(chunk)
                                    if size > 50 * 1024 * 1024:
                                        raise Exception("File too large")
                        
                        if size < 1000:
                            print(f"Twitter: File too small ({size} bytes), removing", flush=True)
                            os.remove(p)
                            continue
                        
                        # Basic image validation
                        try:
                            from PIL import Image
                            with Image.open(p) as img:
                                w, h = img.size
                                if w < 100 or h < 100:
                                    print(f"Twitter: Image too small ({w}x{h}), removing", flush=True)
                                    os.remove(p)
                                    continue
                        except ImportError:
                            pass
                        except Exception as e:
                            print(f"Twitter: Image validation failed: {e}, removing", flush=True)
                            try:
                                os.remove(p)
                            except Exception:
                                pass
                            continue
                        
                        add_url_to_history(media_url)
                        print(f"Twitter: Successfully downloaded: {p}", flush=True)
                        return p
                        
                    except Exception as e:
                        print(f"Twitter: Download failed: {e}", flush=True)
                        continue
                
                print(f"Twitter: All download attempts failed for @{username}", flush=True)
            
            # Move to next source on any error
            print(f"Twitter: Moving to next source", flush=True)

        print("Twitter: No images fetched from any sources", flush=True)
        return None
    except Exception as e:
        print(f"Twitter: Fatal error: {e}", flush=True)
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