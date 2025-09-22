import os
import random
from pathlib import Path
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
        from bs4 import BeautifulSoup
        from bs4.element import Tag
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        headers = {'User-Agent': 'Mozilla/5.0'}
        if 'pinterest.com' in search_url and 'www.pinterest.com' not in search_url:
            search_url = search_url.replace('://ru.pinterest.com', '://www.pinterest.com').replace('://uk.pinterest.com', '://www.pinterest.com').replace('://br.pinterest.com', '://www.pinterest.com')
        resp = requests.get(search_url, headers=headers, timeout=10)
        resp.raise_for_status()
        soup = BeautifulSoup(resp.text, 'html.parser')
        image_urls = []
        for img in soup.find_all('img'):
            if not isinstance(img, Tag):
                continue
            attrs = getattr(img, 'attrs', {}) or {}
            candidates = []
            for key in ('src', 'data-src', 'data-lazy-src', 'data-pin-media', 'data-pin-href'):
                v = attrs.get(key)
                if v:
                    candidates.append(str(v))
            srcset = attrs.get('srcset')
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
        if not image_urls:
            try:
                from pinterest_dl import PinterestDL
                target = random.randint(30, 120)
                print(f"Search page yielded no <img>; trying browser fallback with target {target}", flush=True)
                browser_client = PinterestDL.with_browser(browser_type="chrome", headless=True)
                scraped_browser = browser_client.scrape(url=search_url, num=target)
                if scraped_browser:
                    random.shuffle(scraped_browser)
                    chosen_meta = random.choice(scraped_browser)
                    print("Downloading one media from browser fallback", flush=True)
                    downloaded_items = PinterestDL.download_media(media=[chosen_meta], output_dir=output_dir, download_streams=True)
                    if downloaded_items:
                        item = downloaded_items[0]
                        if isinstance(item, str) and os.path.isfile(item):
                            return item
                        if isinstance(item, dict):
                            p = item.get('path') or item.get('filepath') or item.get('file')
                            if p and os.path.isfile(p):
                                return p
            except Exception:
                pass
            return None
        random.shuffle(image_urls)
        image_urls = image_urls[:max(1, min(num, 50))]
        u = random.choice(image_urls)
        try:
            r = requests.get(u, headers=headers, timeout=10)
            r.raise_for_status()
            ct = r.headers.get('content-type', '')
            ext = '.jpg'
            if 'png' in ct:
                ext = '.png'
            elif 'gif' in ct:
                ext = '.gif'
            p = os.path.join(output_dir, f'pinterest_search_random{ext}')
            with open(p, 'wb') as f:
                f.write(r.content)
            return p
        except Exception:
            return None
    except Exception:
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