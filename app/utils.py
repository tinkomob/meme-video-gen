import json
import os
from pathlib import Path
from .config import HISTORY_FILE
from typing import Optional

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


def replace_file_from_bytes(target_path: str, content: bytes) -> bool:
    try:
        p = Path(target_path)
        p.parent.mkdir(parents=True, exist_ok=True)
        with open(p, 'wb') as f:
            f.write(content)
        return True
    except Exception:
        return False


def clear_video_history(path: str = "video_history.json") -> bool:
    try:
        with open(path, 'w', encoding='utf-8') as f:
            json.dump([], f, ensure_ascii=False, indent=2)
        return True
    except Exception:
        return False


def read_small_file(path: str, max_bytes: int = 1024 * 1024) -> Optional[bytes]:
    try:
        p = Path(path)
        if p.exists() and p.is_file() and p.stat().st_size <= max_bytes:
            with open(p, 'rb') as f:
                return f.read()
    except Exception:
        pass
    return None