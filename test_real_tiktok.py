import os
from pathlib import Path
from app.uploaders import tiktok_upload

def test_real_tiktok_upload():
    print("=== Реальный тест загрузки TikTok с куками ===")

    try:
        root = Path.cwd()
        video_path = root / "tiktok_video.mp4"
        print(f"Ищем видео в корне: {video_path}")

        if not video_path.exists():
            print("❌ Файл tiktok_video.mp4 не найден в корне проекта. Сначала сгенерируйте видео (например, командой /generate в боте).")
            return False

        file_size = video_path.stat().st_size
        print(f"✅ Видео найдено, размер: {file_size} байт")

        cookies_path = root / "cookies.txt"
        if not cookies_path.exists():
            print(f"❌ Файл с куками не найден: {cookies_path}")
            return False

        print(f"✅ Файл с куками найден: {cookies_path}")

        print("\n=== Загрузка в TikTok ===")
        result = tiktok_upload(
            video_path=str(video_path),
            description="Abuga #meme",
            cookies=str(cookies_path)
        )

        print(f"Результат загрузки: {result}")

        if isinstance(result, dict):
            if result.get('success'):
                print("🎉 УСПЕШНАЯ ЗАГРУЗКА!")
                if 'video_url' in result:
                    print(f"🔗 URL видео: {result['video_url']}")
                return True
            else:
                print(f"❌ Загрузка не удалась: {result.get('error', 'Неизвестная ошибка')}")
                return False
        else:
            print(f"❌ Неожиданный результат: {result}")
            return False

    except Exception as e:
        print(f"❌ Ошибка во время тестирования: {e}")
        return False

if __name__ == "__main__":
    print("Начинаем тест с реальными куками TikTok...")
    success = test_real_tiktok_upload()
    print(f"\n{'='*60}")
    print(f"🎯 РЕЗУЛЬТАТ: {'УСПЕХ' if success else 'НЕУДАЧА'}")
    print(f"{'='*60}")