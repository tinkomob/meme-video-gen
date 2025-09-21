import json
import os
from datetime import datetime
from typing import Any, Dict, List, Optional

HISTORY_FILE = "video_history.json"


def load_video_history(path: str = HISTORY_FILE) -> List[Dict[str, Any]]:
    try:
        if os.path.exists(path):
            with open(path, "r", encoding="utf-8") as f:
                data = json.load(f)
                if isinstance(data, list):
                    return data
    except Exception:
        pass
    return []


def save_video_history(items: List[Dict[str, Any]], path: str = HISTORY_FILE) -> None:
    try:
        with open(path, "w", encoding="utf-8") as f:
            json.dump(items, f, ensure_ascii=False, indent=2)
    except Exception:
        pass


essential_fields = [
    "id",
    "title",
    "video_path",
    "thumbnail_path",
    "source_url",
    "audio_path",
    "deployment_links",
    "created_at",
]


def add_video_history_item(
    video_path: Optional[str],
    thumbnail_path: Optional[str],
    source_url: Optional[str],
    audio_path: Optional[str] = None,
    deployment_links: Optional[dict] = None,
    path: str = HISTORY_FILE,
) -> Dict[str, Any]:
    items = load_video_history(path)
    new_item = {
        "id": str(len(items) + 1),
        "title": f"Мем-видео {datetime.now().strftime('%d.%m.%Y %H:%M')}",
        "video_path": video_path,
        "thumbnail_path": thumbnail_path,
        "source_url": source_url,
        "audio_path": audio_path,
        "deployment_links": deployment_links or {},
        "created_at": datetime.now().isoformat(),
    }
    items.append(new_item)
    save_video_history(items, path)
    return new_item
