import json
import os
import shutil
from datetime import datetime
from typing import Any, Dict, List, Optional

HISTORY_FILE = "video_history.json"
AUDIO_ARCHIVE_DIR = "archived_audio"  # Папка для сохранения аудиофайлов


def _ensure_audio_archive_dir():
    """Создаёт директорию для архивирования аудиофайлов"""
    if not os.path.exists(AUDIO_ARCHIVE_DIR):
        try:
            os.makedirs(AUDIO_ARCHIVE_DIR, exist_ok=True)
        except Exception:
            pass


def _archive_audio_file(audio_path: Optional[str]) -> Optional[str]:
    """
    Копирует аудиофайл из временной директории в архив.
    Возвращает путь к архивированному файлу или None.
    """
    import logging
    import sys
    
    if not audio_path:
        print(f"[archive] audio_path is None", flush=True)
        sys.stdout.flush()
        logging.warning(f"[archive] audio_path is None")
        return None
    
    if not os.path.exists(audio_path):
        print(f"[archive] Audio file does not exist: {audio_path}", flush=True)
        sys.stdout.flush()
        logging.warning(f"[archive] Audio file does not exist: {audio_path}")
        return None
    
    try:
        print(f"[archive] Starting archive of: {audio_path}", flush=True)
        sys.stdout.flush()
        logging.info(f"[archive] Starting archive of: {audio_path}")
        
        _ensure_audio_archive_dir()
        
        # Генерируем уникальное имя файла в архиве
        filename = os.path.basename(audio_path)
        archive_path = os.path.join(AUDIO_ARCHIVE_DIR, filename)
        
        # Если файл уже существует, добавляем индекс
        if os.path.exists(archive_path):
            base, ext = os.path.splitext(filename)
            counter = 1
            while os.path.exists(os.path.join(AUDIO_ARCHIVE_DIR, f"{base}_{counter}{ext}")):
                counter += 1
            archive_path = os.path.join(AUDIO_ARCHIVE_DIR, f"{base}_{counter}{ext}")
            print(f"[archive] File exists, using: {archive_path}", flush=True)
            sys.stdout.flush()
        
        # Копируем файл
        shutil.copy2(audio_path, archive_path)
        print(f"[archive] Successfully archived to: {archive_path}", flush=True)
        sys.stdout.flush()
        logging.info(f"[archive] Successfully archived to: {archive_path}")
        return archive_path
    except Exception as e:
        print(f"[archive] ERROR: Failed to archive audio file: {e}", flush=True)
        sys.stdout.flush()
        logging.error(f"[archive] Failed to archive audio file: {e}", exc_info=True)
        return None


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
    audio_title: Optional[str] = None,
    deployment_links: Optional[dict] = None,
    path: str = HISTORY_FILE,
) -> Dict[str, Any]:
    import logging
    import sys
    
    items = load_video_history(path)
    
    print(f"[add_history] Received audio_path: {audio_path}", flush=True)
    sys.stdout.flush()
    logging.info(f"[add_history] Received audio_path: {audio_path}")
    
    # Архивируем аудиофайл, если он существует
    archived_audio_path = _archive_audio_file(audio_path)
    
    print(f"[add_history] Archived audio_path: {archived_audio_path}", flush=True)
    sys.stdout.flush()
    logging.info(f"[add_history] Archived audio_path: {archived_audio_path}")
    
    new_item = {
        "id": str(len(items) + 1),
        "title": f"Мем-видео {datetime.now().strftime('%d.%m.%Y %H:%M')}",
        "video_path": video_path,
        "thumbnail_path": thumbnail_path,
        "source_url": source_url,
        "audio_path": archived_audio_path,  # Используем архивированный путь
        "audio_title": audio_title,
        "deployment_links": deployment_links or {},
        "created_at": datetime.now().isoformat(),
    }
    items.append(new_item)
    save_video_history(items, path)
    print(f"[add_history] Saved to history: {path}", flush=True)
    sys.stdout.flush()
    logging.info(f"[add_history] Saved to history: {path}")
    return new_item
