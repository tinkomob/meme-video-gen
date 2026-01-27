"""
AI module for generating video ideas using Google Gemini API.
"""
import os
import logging
import sys
from typing import Optional
from google import genai
from google.genai import types

logger = logging.getLogger(__name__)

# Ensure logger output is captured
if not logger.handlers:
    handler = logging.StreamHandler(sys.stdout)
    handler.setLevel(logging.DEBUG)
    formatter = logging.Formatter('%(asctime)s - [%(name)s] - %(levelname)s - %(message)s')
    handler.setFormatter(formatter)
    logger.addHandler(handler)
    logger.setLevel(logging.DEBUG)


def _get_gemini_client() -> Optional[genai.Client]:
    """
    Create and return Gemini client instance.
    Returns None if API key is not configured.
    """
    api_key = os.getenv('GEMINI_API_KEY')
    if not api_key:
        print("[AI] GEMINI_API_KEY not found in environment variables", flush=True)
        logger.error("GEMINI_API_KEY not found in environment variables")
        sys.stdout.flush()
        return None
    
    try:
        print(f"[AI] Creating Gemini client with API key: {api_key[:10]}...", flush=True)
        sys.stdout.flush()
        client = genai.Client(api_key=api_key)
        print("[AI] Gemini client created successfully", flush=True)
        sys.stdout.flush()
        return client
    except Exception as e:
        print(f"[AI] Failed to create Gemini client: {e}", flush=True)
        logger.error(f"Failed to create Gemini client: {e}")
        sys.stdout.flush()
        return None


def generate_video_idea_from_track(
    track_title: str,
    track_artist: Optional[str] = None,
    track_duration: int = 8
) -> Optional[str]:
    """
    Generate a creative video idea based on a music track.
    
    Args:
        track_title: Title of the music track
        track_artist: Optional artist name
        track_duration: Duration of the video in seconds (default: 8)
    
    Returns:
        A single sentence describing a creative video idea, or None if generation fails
    """
    client = _get_gemini_client()
    if not client:
        return None
    
    try:
        # Build the track description
        track_info = track_title
        if track_artist:
            track_info = f"{track_artist} - {track_title}"
        
        # Create a creative prompt for the AI
        prompt = f"""Ты - креативный режиссёр коротких вирусных видео для TikTok и Instagram Reels.

Трек: {track_info}
Длительность видео: {track_duration} секунд

Придумай одно короткое КРЕАТИВНОЕ предложение (максимум 15-20 слов) с идеей для ВИЗУАЛЬНОГО ряда короткого мем-видео под этот трек.

ВАЖНЫЕ ПРАВИЛА:
- НЕ ИСПОЛЬЗУЙ ТЕКСТ НА ВИДЕО (только визуальные образы)
- Идея должна быть ЯРКОЙ, ЗАПОМИНАЮЩЕЙСЯ и СМЕШНОЙ
- Опиши конкретное визуальное действие или сцену
- Используй неожиданные, абсурдные или ироничные образы
- Одно предложение, без объяснений

Пример хороших идей:
- "Кот в костюме космонавта медленно летит среди парящих пицц"
- "Танцующий хомяк в солнечных очках на фоне взрывающихся арбузов"
- "Человек серьёзно гладит невидимую собаку, камера в замедленной съёмке"

Твоя идея:"""

        response = client.models.generate_content(
            model='gemini-3-flash-preview',
            contents=prompt,
            config=types.GenerateContentConfig(
                temperature=1.0,  # Higher creativity
                max_output_tokens=3500,  # Increased for full response
                top_p=0.95,
                top_k=40,
            ),
        )
        
        if response and response.text:
            idea = response.text.strip()
            # Remove quotes if present
            idea = idea.strip('"').strip("'")
            logger.info(f"Generated video idea: {idea}")
            return idea
        else:
            logger.error("Empty response from Gemini API")
            return None
            
    except Exception as e:
        logger.error(f"Error generating video idea: {e}")
        return None
    finally:
        try:
            client.close()
        except Exception:
            pass


def generate_video_idea_from_audio_file(
    audio_path: str,
    track_duration: int = 8
) -> Optional[str]:
    """
    Generate a creative video idea by analyzing an audio file with AI.
    
    Args:
        audio_path: Path to the audio file (MP3/WAV)
        track_duration: Duration of the video in seconds (default: 8)
    
    Returns:
        A single sentence describing a creative video idea, or None if generation fails
    """
    client = _get_gemini_client()
    if not client:
        return None
    
    try:
        # Check if file exists
        if not os.path.isfile(audio_path):
            logger.error(f"Audio file not found: {audio_path}")
            return None
        
        # Create prompt for AI with audio analysis
        prompt = f"""Ты - креативный режиссёр коротких вирусных видео для TikTok и Instagram Reels.

Послушай этот аудиотрек и придумай для него КРЕАТИВНУЮ идею короткого мем-видео.

Длительность видео: {track_duration} секунд

Придумай одно короткое КРЕАТИВНОЕ предложение (максимум 15-20 слов) с идеей для ВИЗУАЛЬНОГО ряда под этот трек.

ВАЖНЫЕ ПРАВИЛА:
- НЕ ИСПОЛЬЗУЙ ТЕКСТ НА ВИДЕО (только визуальные образы)
- Идея должна быть ЯРКОЙ, ЗАПОМИНАЮЩЕЙСЯ и СМЕШНОЙ
- Учитывай темп, настроение и ритм музыки
- Опиши конкретное визуальное действие или сцену
- Используй неожиданные, абсурдные или ироничные образы
- Одно предложение, без объяснений

Пример хороших идей:
- "Кот в костюме космонавта медленно летит среди парящих пицц"
- "Танцующий хомяк в солнечных очках на фоне взрывающихся арбузов"
- "Человек серьёзно гладит невидимую собаку, камера в замедленной съёмке"

Твоя идея:"""

        # Read audio file as bytes
        with open(audio_path, 'rb') as f:
            audio_bytes = f.read()
        
        # Determine mime type
        mime_type = 'audio/mpeg' if audio_path.lower().endswith('.mp3') else 'audio/wav'
        
        # Create audio part
        audio_part = types.Part.from_bytes(
            data=audio_bytes,
            mime_type=mime_type
        )
        
        response = client.models.generate_content(
            model='gemini-3-flash-preview',
            contents=[prompt, audio_part],
            config=types.GenerateContentConfig(
                temperature=1.0,
                max_output_tokens=3500,  # Increased for full response
                top_p=0.95,
                top_k=40,
            ),
        )
        
        if response and response.text:
            idea = response.text.strip()
            # Remove quotes if present
            idea = idea.strip('"').strip("'")
            logger.info(f"Generated video idea from audio: {idea}")
            return idea
        else:
            logger.error("Empty response from Gemini API")
            return None
            
    except Exception as e:
        logger.error(f"Error generating video idea from audio: {e}")
        return None
    finally:
        try:
            client.close()
        except Exception:
            pass


def generate_catchy_title_from_audio(
    audio_path: str,
    track_title: str,
    thumbnail_path: Optional[str] = None
) -> Optional[str]:
    """
    Generate a catchy YouTube Shorts title by analyzing audio file and thumbnail.
    
    Args:
        audio_path: Path to the audio file (MP3/WAV)
        track_title: Original track title
        thumbnail_path: Optional path to thumbnail image for visual context
    
    Returns:
        A catchy title for YouTube Shorts, or original title if generation fails
    """
    # Force logging to stdout immediately
    print(f"[AI Title] Starting generation for: {track_title}", flush=True)
    print(f"[AI Title] Audio path: {audio_path}", flush=True)
    if thumbnail_path:
        print(f"[AI Title] Thumbnail path: {thumbnail_path}", flush=True)
    logger.info(f"[AI Title] Starting generation for: {track_title}")
    logger.info(f"[AI Title] Audio path: {audio_path}")
    if thumbnail_path:
        logger.info(f"[AI Title] Thumbnail path: {thumbnail_path}")
    sys.stdout.flush()
    
    client = _get_gemini_client()
    if not client:
        print("[AI Title] Failed to create Gemini client", flush=True)
        logger.error("[AI Title] Failed to create Gemini client")
        sys.stdout.flush()
        return track_title
    
    try:
        # Check if file exists
        if not os.path.isfile(audio_path):
            print(f"[AI Title] Audio file not found: {audio_path}", flush=True)
            logger.error(f"[AI Title] Audio file not found: {audio_path}")
            sys.stdout.flush()
            return track_title
        
        # Get file size
        file_size = os.path.getsize(audio_path)
        print(f"[AI Title] Audio file size: {file_size} bytes", flush=True)
        logger.info(f"[AI Title] Audio file size: {file_size} bytes")
        sys.stdout.flush()
        
        # Upload audio file to Gemini
        print("[AI Title] Uploading audio to Gemini...", flush=True)
        logger.info("[AI Title] Uploading audio to Gemini...")
        sys.stdout.flush()
        audio_file = client.files.upload(file=audio_path)
        print(f"[AI Title] Audio file uploaded: {audio_file.uri}", flush=True)
        logger.info(f"[AI Title] Audio file uploaded: {audio_file.uri}")
        sys.stdout.flush()
        
        # Upload thumbnail if available
        thumbnail_file = None
        if thumbnail_path and os.path.isfile(thumbnail_path):
            try:
                print(f"[AI Title] Uploading thumbnail to Gemini...", flush=True)
                logger.info(f"[AI Title] Uploading thumbnail to Gemini...")
                sys.stdout.flush()
                thumbnail_file = client.files.upload(file=thumbnail_path)
                print(f"[AI Title] Thumbnail uploaded: {thumbnail_file.uri}", flush=True)
                logger.info(f"[AI Title] Thumbnail uploaded: {thumbnail_file.uri}")
                sys.stdout.flush()
            except Exception as e:
                print(f"[AI Title] Warning: Failed to upload thumbnail: {e}", flush=True)
                logger.warning(f"[AI Title] Failed to upload thumbnail: {e}")
                sys.stdout.flush()
        
        # Create prompt
        prompt = f"""Ты - эксперт по созданию вирусных названий для YouTube Shorts.

Оригинальное название трека: {track_title}

Прослушай аудиотрек и посмотри на миниатюру видео. Создай ОДНО привлекательное название для YouTube Shorts (максимум 70-80 символов).

ВАЖНЫЕ ПРАВИЛА:
- Название должно быть КРАТКИМ и ЦЕПЛЯЮЩИМ
- Учитывай стиль, настроение и энергию музыки
- Рассмотри визуальный контент на миниатюре для большего контекста
- Используй эмодзи (1-2 максимум) для привлечения внимания
- НЕ добавляй хештеги или лишние слова
- Если трек известный - можешь немного модифицировать название для привлекательности
- Если это инструментал или неизвестный трек - создай интригующее название

ПРИМЕРЫ ХОРОШИХ НАЗВАНИЙ:
- "🔥 Cyberpunk Vibes"
- "Late Night Drive 🌙"
- "Pure Energy ⚡"
- "Chill Beats to Relax"
- "That Song You Needed 🎵"

Твоё название (ТОЛЬКО название, без кавычек и объяснений):"""
        
        print("[AI Title] Sending to Gemini API...", flush=True)
        logger.info("[AI Title] Sending to Gemini API...")
        sys.stdout.flush()
        
        # Prepare content list with audio and optional thumbnail
        content_parts = [prompt, audio_file]
        if thumbnail_file:
            content_parts.append(thumbnail_file)
        
        response = client.models.generate_content(
            model='gemini-3-flash-preview',
            contents=content_parts,
            config=types.GenerateContentConfig(
                temperature=0.9,
                max_output_tokens=100,
                top_p=0.9,
                top_k=40,
            )
        )
        
        print(f"[AI Title] API Response received", flush=True)
        logger.info(f"[AI Title] API Response: {response}")
        sys.stdout.flush()
        
        if response and response.text:
            title = response.text.strip()
            # Remove quotes if present
            title = title.strip('"').strip("'").strip()
            
            # Limit length
            if len(title) > 100:
                title = title[:97] + '...'
            
            print(f"[AI Title] Generated title: '{title}'", flush=True)
            logger.info(f"[AI Title] Generated title: '{title}'")
            sys.stdout.flush()
            return title
        else:
            print("[AI Title] Empty response from Gemini API", flush=True)
            logger.error("[AI Title] Empty response from Gemini API")
            logger.info(f"[AI Title] Falling back to original title: {track_title}")
            sys.stdout.flush()
            return track_title
            
    except Exception as e:
        print(f"[AI Title] ERROR: {str(e)}", flush=True)
        logger.error(f"[AI Title] Error generating title: {str(e)}", exc_info=True)
        logger.info(f"[AI Title] Falling back to original title: {track_title}")
        sys.stdout.flush()
        return track_title
