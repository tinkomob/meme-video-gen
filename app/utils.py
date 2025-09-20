import json
import os
from pathlib import Path
from .config import HISTORY_FILE

def load_history(path: str = HISTORY_FILE):
    try:
        if os.path.exists(path):
            with open(path, 'r', encoding='utf-8') as f:
                data = json.load(f)
                if isinstance(data, dict):
                    data.setdefault('urls', [])
                    return data
    except Exception:
        pass
    return {'urls': []}

def save_history(history: dict, path: str = HISTORY_FILE):
    try:
        with open(path, 'w', encoding='utf-8') as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
    except Exception:
        pass

def add_url_to_history(url: str, path: str = HISTORY_FILE):
    try:
        hist = load_history(path)
        if url and url not in hist['urls']:
            hist['urls'].append(url)
            save_history(hist, path)
    except Exception:
        pass

def ensure_gitignore_entries(entries: list[str], gitignore_path: str = '.gitignore'):
    try:
        existing = set()
        p = Path(gitignore_path)
        if p.exists():
            with open(p, 'r', encoding='utf-8') as f:
                for line in f:
                    existing.add(line.strip())
        with open(p, 'a', encoding='utf-8') as f:
            for e in entries:
                if e not in existing:
                    f.write(e + '\n')
    except Exception:
        pass

def load_urls_json(file_path: str, default_urls: list[str] | None = None):
    try:
        p = Path(file_path)
        if not p.exists():
            if default_urls is None:
                default_urls = []
            with open(p, 'w', encoding='utf-8') as f:
                json.dump(default_urls, f, ensure_ascii=False, indent=2)
            return list(default_urls)
        with open(p, 'r', encoding='utf-8') as f:
            data = json.load(f)
            if isinstance(data, list):
                return [x for x in data if isinstance(x, str) and x.strip()]
    except Exception:
        pass
    return []