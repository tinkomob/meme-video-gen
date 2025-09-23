import os
import random
import re
from pathlib import Path
from urllib.parse import urlparse
import requests
from .utils import load_history

def get_from_meme_api():
    print("Calling meme API...")
    url = 'https://meme-api.com/gimme'
    try:
        r = requests.get(url, timeout=10)
        r.raise_for_status()
        data = r.json()
        print(f"Meme API response: {data}", flush=True)
        if not data.get('nsfw', True):
            meme_url = data['url']
            hist = load_history()
            if meme_url in hist['urls']:
                print(f"Meme {meme_url} already used, skipping", flush=True)
                return None
            print(f"Got fresh meme: {meme_url}", flush=True)
            return meme_url
        else:
            print("Meme is NSFW, skipping", flush=True)
    except Exception as e:
        print(f"Error getting meme from API: {e}", flush=True)
    print("No suitable meme found", flush=True)
    return None

def scrape_pinterest_search(search_url: str, output_dir: str = 'pins', num: int = 30):
    try:
        try:
            from bs4 import BeautifulSoup
            from bs4.element import Tag
        except ImportError:
            print("Pinterest search: bs4 not available, skipping HTML parsing", flush=True)
            # Skip directly to browser fallback
            Path(output_dir).mkdir(parents=True, exist_ok=True)
            try:
                from pinterest_dl import PinterestDL
                target = random.randint(30, 120)
                print(f"Pinterest search: trying browser fallback with target {target}", flush=True)
                if 'pinterest.com' in search_url and 'www.pinterest.com' not in search_url:
                    search_url = search_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
                browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True)
                scraped_browser = browser_client.scrape(url=search_url, num=target)
                print(f"Browser fallback scraped {len(scraped_browser) if scraped_browser else 0} items", flush=True)
                if scraped_browser:
                    random.shuffle(scraped_browser)
                    chosen_meta = random.choice(scraped_browser)
                    print("Downloading one media from browser fallback", flush=True)
                    downloaded_items = PinterestDL.download_media(media=[chosen_meta], output_dir=output_dir, download_streams=True)
                    if downloaded_items:
                        item = downloaded_items[0]
                        file_path = None
                        if isinstance(item, str) and os.path.isfile(item):
                            file_path = item
                        elif isinstance(item, dict):
                            file_path = item.get('path') or item.get('filepath') or item.get('file')
                        
                        if file_path and os.path.isfile(file_path):
                            file_size = os.path.getsize(file_path)
                            if file_size < 1000:
                                print(f"Browser fallback: file too small ({file_size} bytes)", flush=True)
                            else:
                                try:
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
                                    print(f"Browser fallback: file validation failed: {e}", flush=True)
                    print("Browser fallback: no valid file returned", flush=True)
            except Exception as e:
                print(f"Browser fallback failed: {e}", flush=True)
            return None
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        ua = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
        try:
            from fake_useragent import UserAgent
            ua = UserAgent().random or ua
        except Exception:
            pass
        headers = {
            'User-Agent': ua,
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8',
            'Accept-Language': 'en-US,en;q=0.9',
            'Referer': 'https://www.pinterest.com/'
        }
        if 'pinterest.com' in search_url and 'www.pinterest.com' not in search_url:
            search_url = search_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
        print(f"Pinterest search: requesting {search_url}", flush=True)
        sess = requests.Session()
        sess.headers.update(headers)
        resp = sess.get(search_url, timeout=15)
        resp.raise_for_status()
        print(f"Pinterest search: got response {resp.status_code}, content length: {len(resp.text)}", flush=True)
        print(f"Pinterest search: first 200 chars of HTML: {resp.text[:200]}", flush=True)
        soup = BeautifulSoup(resp.text, 'html.parser')
        image_urls = []
        
        # Try to extract from img tags first
        for img in soup.find_all('img'):
            if not isinstance(img, Tag):
                continue
            attrs = getattr(img, 'attrs', {}) or {}
            candidates = []
            for key in ('src', 'data-src', 'data-lazy-src', 'data-pin-media', 'data-pin-href'):
                v = attrs.get(key)
                if v:
                    candidates.append(str(v))
            for key in ('srcset', 'data-srcset'):
                srcset = attrs.get(key)
                if srcset:
                    for part in str(srcset).split(','):
                        u = part.strip().split(' ')[0]
                        if u:
                            candidates.append(u)
            src = ''
            for cand in candidates:
                if 'pinimg.com' in cand or 'pinterest.com' in cand:
                    src = cand
                    break
            if not src:
                continue
            if src.startswith('//'):
                src = 'https:' + src
            elif src.startswith('/'):
                src = 'https://www.pinterest.com' + src
            src = src.replace('236x', '564x').replace('474x', '736x')
            image_urls.append(src)
        image_urls = list(dict.fromkeys(image_urls))
        print(f"Pinterest search: found {len(soup.find_all('img'))} img tags, extracted {len(image_urls)} image URLs", flush=True)
        
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
            from urllib.parse import urlparse
            random.shuffle(candidates)
            attempts = candidates[:max(1, min(len(candidates), max(6, min(12, num))))]
            print(f"Pinterest search: trying to download {len(attempts)} candidate URLs", flush=True)
            for i, u in enumerate(attempts):
                try:
                    print(f"Pinterest search: attempting download {i+1}/{len(attempts)}: {u[:80]}...", flush=True)
                    r = sess.get(u, timeout=15, stream=True)
                    r.raise_for_status()
                    ct = (r.headers.get('content-type') or '').lower()
                    content_length = r.headers.get('content-length')
                    if content_length:
                        try:
                            size_mb = int(content_length) / (1024 * 1024)
                            if size_mb > 50:  # Skip files larger than 50MB
                                print(f"Pinterest search: skipping large file ({size_mb:.1f}MB)", flush=True)
                                continue
                            if int(content_length) < 1000:  # Skip files smaller than 1KB (likely placeholders)
                                print(f"Pinterest search: skipping tiny file ({content_length} bytes)", flush=True)
                                continue
                        except ValueError:
                            pass
                    
                    if not any(s in ct for s in ('image/', 'video/')):
                        # try by URL extension
                        parsed = urlparse(u)
                        path = parsed.path.lower()
                        if not re.search(r'\.(jpg|jpeg|png|gif|webp|mp4|webm|mov)$', path):
                            print(f"Pinterest search: skipping non-media URL (content-type: {ct})", flush=True)
                            continue
                    ext = '.jpg'
                    if 'png' in ct or u.lower().endswith('.png'):
                        ext = '.png'
                    elif 'gif' in ct or u.lower().endswith('.gif'):
                        ext = '.gif'
                    elif 'webp' in ct or u.lower().endswith('.webp'):
                        ext = '.webp'
                    elif 'mp4' in ct or u.lower().endswith('.mp4'):
                        ext = '.mp4'
                    elif 'webm' in ct or u.lower().endswith('.webm'):
                        ext = '.webm'
                    elif 'mov' in ct or u.lower().endswith('.mov'):
                        ext = '.mov'
                    p = os.path.join(output_dir, f'pinterest_search_{abs(hash(u)) % 10**8}{ext}')
                    downloaded_size = 0
                    with open(p, 'wb') as f:
                        for chunk in r.iter_content(chunk_size=8192):
                            if chunk:
                                f.write(chunk)
                                downloaded_size += len(chunk)
                    
                    if os.path.isfile(p) and os.path.getsize(p) > 1000:  # At least 1KB
                        # Validate the file is actually readable
                        try:
                            if ext.lower() in ('.jpg', '.jpeg', '.png', '.webp'):
                                from PIL import Image
                                with Image.open(p) as img:
                                    img.verify()  # Will raise exception if corrupted
                                    print(f"Pinterest search: validated image {p} ({downloaded_size} bytes)", flush=True)
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
                            print(f"Pinterest search: downloaded {p} ({downloaded_size} bytes, no validation)", flush=True)
                            return p
                        except Exception as e:
                            print(f"Pinterest search: file validation failed for {p}: {e}", flush=True)
                            try:
                                os.remove(p)
                            except:
                                pass
                            continue
                    else:
                        print(f"Pinterest search: downloaded file is too small: {p} ({os.path.getsize(p) if os.path.isfile(p) else 0} bytes)", flush=True)
                        try:
                            if os.path.isfile(p):
                                os.remove(p)
                        except:
                            pass
                except Exception as e:
                    print(f"Pinterest search: download failed for {u[:80]}...: {e}", flush=True)
                    continue
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
            scraped_browser = browser_client.scrape(url=search_url, num=target)
            print(f"Browser fallback scraped {len(scraped_browser) if scraped_browser else 0} items", flush=True)
            if scraped_browser:
                random.shuffle(scraped_browser)
                chosen_meta = random.choice(scraped_browser)
                print("Downloading one media from browser fallback", flush=True)
                downloaded_items = PinterestDL.download_media(media=[chosen_meta], output_dir=output_dir, download_streams=True)
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
    except Exception as e:
        print(f"Pinterest search: fatal error: {e}", flush=True)
        return None

def scrape_one_from_pinterest(board_url: str, output_dir: str = 'pins', num: int = 10000):
    print(f"scrape_one_from_pinterest called with URL: {board_url}", flush=True)
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        if 'pinterest.com' in board_url and 'www.pinterest.com' not in board_url:
            board_url = board_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
            print(f"Normalized Pinterest URL: {board_url}", flush=True)
        if 'search/pins' in board_url:
            print("Detected search URL, using search scraper", flush=True)
            return scrape_pinterest_search(board_url, output_dir, num)

        from pinterest_dl import PinterestDL
        max_scan_cap = 800
        max_scan = max(20, min(max_scan_cap, num if isinstance(num, int) and num > 0 else max_scan_cap))
        min_scan = 40 if max_scan >= 80 else 10
        target_api = random.randint(min_scan, max(min_scan + 5, max_scan // 2 if max_scan >= 100 else max_scan))
        print(f"Using PinterestDL (sample mode). Target API sample size: {target_api} (cap {max_scan})", flush=True)

        scraped = None
        try:
            client = PinterestDL.with_api(timeout=15, verbose=False)
            scraped = client.scrape(url=board_url, num=target_api)
            print(f"API mode scraped {len(scraped) if scraped else 0} items (target: {target_api})", flush=True)
        except Exception as ae:
            print(f"API mode scrape failed: {ae}", flush=True)

        if not scraped or len(scraped) < max(8, target_api // 4):
            try:
                remaining_cap = max_scan - (len(scraped) if scraped else 0)
                target_browser = random.randint(max(min_scan, 60), max(min_scan + 20, min(max_scan, remaining_cap if remaining_cap > 0 else max_scan)))
                print(f"Few items from API mode; trying browser mode with target {target_browser}…", flush=True)
                browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True)
                scraped_browser = browser_client.scrape(url=board_url, num=target_browser)
                print(f"Browser mode scraped {len(scraped_browser) if scraped_browser else 0} items", flush=True)
                if scraped_browser:
                    scraped = scraped_browser
            except Exception as be:
                print(f"Browser mode scrape failed: {be}", flush=True)

        if scraped:
            random.shuffle(scraped)
        if not scraped:
            print("No items scraped from Pinterest", flush=True)
            return None

        chosen_meta = random.choice(scraped)
        print("Downloading one randomly chosen media item", flush=True)
        downloaded_items = PinterestDL.download_media(media=[chosen_meta], output_dir=output_dir, download_streams=True)
        print(f"PinterestDL downloaded {len(downloaded_items) if downloaded_items else 0} item(s)", flush=True)
        if downloaded_items:
            item = downloaded_items[0]
            if isinstance(item, str) and os.path.isfile(item):
                print(f"Selected file: {item}", flush=True)
                return item
            if isinstance(item, dict):
                p = item.get('path') or item.get('filepath') or item.get('file')
                if p and os.path.isfile(p):
                    print(f"Selected file: {p}", flush=True)
                    return p

        print("No valid downloaded file returned, scanning output_dir as fallback", flush=True)
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.lower().endswith(('.jpg', '.jpeg', '.png', '.gif', '.mp4', '.webm', '.mov')):
                    chosen = os.path.join(root, f)
                    print(f"Selected file: {chosen}", flush=True)
                    return chosen
        return None
    except Exception as e:
        print(f"Error in scrape_one_from_pinterest: {e}", flush=True)
        return None