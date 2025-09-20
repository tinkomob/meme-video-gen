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
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        headers = {'User-Agent': 'Mozilla/5.0'}
        resp = requests.get(search_url, headers=headers, timeout=10)
        resp.raise_for_status()
        soup = BeautifulSoup(resp.text, 'html.parser')
        image_urls = []
        for img in soup.find_all('img'):
            src = img.get('src')
            if not src:
                continue
            if 'pinimg.com' in src or 'pinterest.com' in src:
                if src.startswith('//'):
                    src = 'https:' + src
                elif src.startswith('/'):
                    src = 'https://www.pinterest.com' + src
                src = src.replace('236x', '564x').replace('474x', '736x')
                image_urls.append(src)
        if not image_urls:
            return None
        image_urls = image_urls[:num]
        downloaded = []
        for i, u in enumerate(image_urls):
            try:
                r = requests.get(u, headers=headers, timeout=10)
                r.raise_for_status()
                ct = r.headers.get('content-type', '')
                ext = '.jpg'
                if 'png' in ct:
                    ext = '.png'
                elif 'gif' in ct:
                    ext = '.gif'
                p = os.path.join(output_dir, f'pinterest_search_{i}{ext}')
                with open(p, 'wb') as f:
                    f.write(r.content)
                downloaded.append(p)
            except Exception:
                continue
        if not downloaded:
            return None
        return random.choice(downloaded)
    except Exception:
        return None

def scrape_one_from_pinterest(board_url: str, output_dir: str = 'pins', num: int = 100):
    print(f"scrape_one_from_pinterest called with URL: {board_url}", flush=True)
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        num = random.randint(50, 200)  # Make num more random
        if 'search/pins' in board_url:
            print("Detected search URL, using search scraper", flush=True)
            return scrape_pinterest_search(board_url, output_dir, num)
        print("Using PinterestDL for board scraping", flush=True)
        from pinterest_dl import PinterestDL
        client = PinterestDL.with_api(timeout=3, verbose=False)
        scraped = client.scrape(url=board_url, num=num)
        print(f"PinterestDL scraped {len(scraped) if scraped else 0} items", flush=True)
        if scraped:
            random.shuffle(scraped)
        if not scraped:
            print("No items scraped from Pinterest", flush=True)
            return None
        downloaded_items = PinterestDL.download_media(media=scraped, output_dir=output_dir, download_streams=True)
        print(f"PinterestDL downloaded {len(downloaded_items) if downloaded_items else 0} items", flush=True)
        candidates = []
        for item in downloaded_items or []:
            if isinstance(item, str) and os.path.isfile(item):
                candidates.append(item)
            elif isinstance(item, dict):
                p = item.get('path') or item.get('filepath') or item.get('file')
                if p and os.path.isfile(p):
                    candidates.append(p)
        if not candidates:
            print("No valid downloaded files found, checking output_dir", flush=True)
            for root, _, files in os.walk(output_dir):
                for f in files:
                    if f.lower().endswith(('.jpg', '.jpeg', '.png', '.gif', '.mp4', '.webm', '.mov')):
                        candidates.append(os.path.join(root, f))
        print(f"Found {len(candidates)} candidate files", flush=True)
        if not candidates:
            return None
        # Select from a random subset for more randomness
        selected = random.sample(candidates, k=min(10, len(candidates)))
        chosen = random.choice(selected)
        print(f"Selected file: {chosen}", flush=True)
        return chosen
    except Exception as e:
        print(f"Error in scrape_one_from_pinterest: {e}", flush=True)
        return None