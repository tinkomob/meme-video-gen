import os
import tempfile
from app.uploaders import tiktok_upload
from app.video import create_text_video

def test_real_tiktok_upload():
    print("=== Реальный тест загрузки TikTok с куками ===")
    
    try:
        # Создаем тестовое видео
        with tempfile.NamedTemporaryFile(suffix='.mp4', delete=False) as temp_file:
            temp_path = temp_file.name
        
        print(f"Создаем тестовое видео: {temp_path}")
        create_text_video("Тест TikTok с куками", temp_path, 1080, 1920)
        
        if not os.path.exists(temp_path):
            print("❌ Не удалось создать тестовое видео")
            return False
        
        file_size = os.path.getsize(temp_path)
        print(f"✅ Видео создано, размер: {file_size} байт")
        
        # Проверяем cookies файл
        cookies_path = "cookies.txt"
        if not os.path.exists(cookies_path):
            print(f"❌ Файл с куками не найден: {cookies_path}")
            return False
        
        print(f"✅ Файл с куками найден: {cookies_path}")
        
        # Тестируем загрузку с куками
        print("\n=== Загрузка в TikTok ===")
        result = tiktok_upload(
            video_path=temp_path,
            description="Тест загрузки с реальными куками #test #meme",
            cookies=cookies_path
        )
        
        print(f"Результат загрузки: {result}")
        
        # Анализируем результат
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
    
    finally:
        # Удаляем тестовое видео
        if 'temp_path' in locals() and os.path.exists(temp_path):
            os.unlink(temp_path)
            print("🗑️ Тестовое видео удалено")

if __name__ == "__main__":
    print("Начинаем тест с реальными куками TikTok...")
    success = test_real_tiktok_upload()
    print(f"\n{'='*60}")
    print(f"🎯 РЕЗУЛЬТАТ: {'УСПЕХ' if success else 'НЕУДАЧА'}")
    print(f"{'='*60}")