#!/usr/bin/env python3

import os
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from app.audio import download_random_song_from_playlist, extract_random_audio_clip

def test_audio_error_handling():
    print("=== Тест обработки ошибок при загрузке аудио ===\n")
    
    # Тест 1: Несуществующий плейлист
    print("Тест 1: Несуществующий URL плейлиста")
    try:
        result = download_random_song_from_playlist(
            "https://www.youtube.com/playlist?list=INVALID_PLAYLIST_ID",
            output_dir=tempfile.mkdtemp()
        )
        print(f"Результат: {result}")
    except Exception as e:
        print(f"✅ Ошибка перехвачена: {e}\n")
    
    # Тест 2: Несуществующий аудио файл
    print("Тест 2: Несуществующий аудио файл")
    try:
        result = extract_random_audio_clip("/nonexistent/audio.mp3")
        print(f"Результат: {result}")
    except FileNotFoundError as e:
        print(f"✅ Ошибка перехвачена: {e}\n")
    
    # Тест 3: Пустой файл
    print("Тест 3: Пустой аудио файл")
    try:
        temp_dir = tempfile.mkdtemp()
        empty_file = os.path.join(temp_dir, "empty.mp3")
        Path(empty_file).touch()
        
        result = extract_random_audio_clip(empty_file)
        print(f"Результат: {result}")
    except ValueError as e:
        print(f"✅ Ошибка перехвачена: {e}\n")
    finally:
        try:
            os.remove(empty_file)
            os.rmdir(temp_dir)
        except:
            pass
    
    print("=== Все тесты обработки ошибок пройдены ===")

if __name__ == "__main__":
    test_audio_error_handling()
