"""
AI module for generating video ideas using Google Gemini API.
"""
import os
import logging
from typing import Optional
from google import genai
from google.genai import types

logger = logging.getLogger(__name__)


def _get_gemini_client() -> Optional[genai.Client]:
    """
    Create and return Gemini client instance.
    Returns None if API key is not configured.
    """
    api_key = os.getenv('GEMINI_API_KEY')
    if not api_key:
        logger.error("GEMINI_API_KEY not found in environment variables")
        return None
    
    try:
        client = genai.Client(api_key=api_key)
        return client
    except Exception as e:
        logger.error(f"Failed to create Gemini client: {e}")
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
