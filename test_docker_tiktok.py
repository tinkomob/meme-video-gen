import os
import tempfile
from app.uploaders import tiktok_upload
from app.video import create_text_video

def test_tiktok_in_docker():
    print("=== Тестирование TikTok загрузки в Docker ===")
    
    try:
        with tempfile.NamedTemporaryFile(suffix='.mp4', delete=False) as temp_file:
            temp_path = temp_file.name
        
        print(f"Создаем тестовое видео: {temp_path}")
        create_text_video("Тест Docker TikTok", temp_path, 1920, 1080)
        
        if not os.path.exists(temp_path):
            print("❌ Не удалось создать тестовое видео")
            return False
        
        file_size = os.path.getsize(temp_path)
        print(f"✅ Видео создано, размер: {file_size} байт")
        
        print("\n=== Проверка Node.js в контейнере ===")
        import subprocess
        try:
            result = subprocess.run(['node', '--version'], capture_output=True, text=True, timeout=10)
            print(f"Node.js версия: {result.stdout.strip()}")
        except Exception as e:
            print(f"❌ Node.js недоступен: {e}")
        
        try:
            result = subprocess.run(['npm', '--version'], capture_output=True, text=True, timeout=10)
            print(f"npm версия: {result.stdout.strip()}")
        except Exception as e:
            print(f"❌ npm недоступен: {e}")
        
        print("\n=== Проверка TiktokAutoUploader ===")
        tiktok_dir = "/app/TiktokAutoUploader"
        if os.path.exists(tiktok_dir):
            print(f"✅ TiktokAutoUploader найден: {tiktok_dir}")
            
            signature_dir = os.path.join(tiktok_dir, "tiktok_uploader", "tiktok-signature")
            if os.path.exists(signature_dir):
                print(f"✅ tiktok-signature директория найдена: {signature_dir}")
                
                os.chdir(signature_dir)
                print("Установка Node.js зависимостей...")
                result = subprocess.run(['npm', 'install'], capture_output=True, text=True, timeout=30)
                
                if result.returncode == 0:
                    print("✅ npm install успешно")
                else:
                    print(f"❌ npm install ошибка: {result.stderr}")
            else:
                print(f"❌ tiktok-signature не найден в {signature_dir}")
        else:
            print(f"❌ TiktokAutoUploader не найден в {tiktok_dir}")
        
        print("\n=== Тестирование загрузки ===")
        result = tiktok_upload(
            video_path=temp_path,
            description="Тест загрузки из Docker",
            username="testuser"
        )
        
        print(f"Результат загрузки: {result}")
        
        os.unlink(temp_path)
        print("✅ Тестовое видео удалено")
        
        return result.get('success', False) if isinstance(result, dict) else False
        
    except Exception as e:
        print(f"❌ Ошибка во время тестирования: {e}")
        if 'temp_path' in locals() and os.path.exists(temp_path):
            os.unlink(temp_path)
        return False

if __name__ == "__main__":
    success = test_tiktok_in_docker()
    print(f"\n{'='*50}")
    print(f"Тест {'ПРОШЕЛ' if success else 'ПРОВАЛИЛСЯ'}")
    print(f"{'='*50}")