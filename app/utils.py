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


def clear_sources(sources_json_path: str = "sources.json", sources_dir: str = "sources") -> dict:
    """
    Clear sources.json file and sources directory.
    Returns dict with deletion stats: {
        'json_cleared': bool,
        'dir_removed': bool,
        'files_deleted': int,
        'errors': list[str]
    }
    """
    errors = []
    files_deleted = 0
    
    # Clear sources.json
    json_cleared = False
    try:
        if os.path.exists(sources_json_path):
            with open(sources_json_path, 'w', encoding='utf-8') as f:
                json.dump({"sources": []}, f, ensure_ascii=False, indent=2)
            json_cleared = True
    except Exception as e:
        errors.append(f"Error clearing {sources_json_path}: {e}")
    
    # Remove sources directory
    dir_removed = False
    try:
        if os.path.exists(sources_dir):
            import shutil
            shutil.rmtree(sources_dir)
            dir_removed = True
    except Exception as e:
        # If we can't remove the dir, try to delete files inside
        try:
            if os.path.isdir(sources_dir):
                for filename in os.listdir(sources_dir):
                    file_path = os.path.join(sources_dir, filename)
                    try:
                        if os.path.isfile(file_path):
                            os.remove(file_path)
                            files_deleted += 1
                        elif os.path.isdir(file_path):
                            import shutil
                            shutil.rmtree(file_path)
                    except Exception as fe:
                        errors.append(f"Error deleting {file_path}: {fe}")
        except Exception as de:
            errors.append(f"Error accessing {sources_dir}: {de}")
    
    return {
        'json_cleared': json_cleared,
        'dir_removed': dir_removed,
        'files_deleted': files_deleted,
        'errors': errors
    }


def read_small_file(path: str, max_bytes: int = 1024 * 1024) -> Optional[bytes]:
    try:
        p = Path(path)
        if p.exists() and p.is_file() and p.stat().st_size <= max_bytes:
            with open(p, 'rb') as f:
                return f.read()
    except Exception:
        pass
    return None