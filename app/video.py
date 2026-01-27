import os
import random

# Monkey patch for PIL compatibility - must be before moviepy import
import PIL.Image
try:
    # For older Pillow versions
    if not hasattr(PIL.Image, 'ANTIALIAS'):
        PIL.Image.ANTIALIAS = PIL.Image.LANCZOS
except AttributeError:
    # For newer Pillow versions where LANCZOS might not be directly available
    try:
        PIL.Image.ANTIALIAS = PIL.Image.Resampling.LANCZOS
    except (AttributeError, ImportError):
        # Fallback - just skip the monkey patch
        pass

# Disable PIL deprecation warnings
os.environ['PILLOW_IGNORE_DEPRECATION'] = '1'

from moviepy.editor import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, AudioFileClip, concatenate_videoclips, concatenate_audioclips

def convert_to_tiktok_format(input_path, output_path, is_youtube=False, audio_path=None, song_title=None, seed=None, variant_group=None):
    print(f"convert_to_tiktok_format called with input_path: {input_path}, output_path: {output_path}", flush=True)
    if not os.path.exists(input_path):
        print(f"Input file does not exist: {input_path}", flush=True)
        return None
    
    # Handle PIL compatibility
    try:
        from PIL import Image
        # Test PIL compatibility
        if hasattr(Image, 'ANTIALIAS'):
            # Older Pillow
            pass
        elif hasattr(Image, 'Resampling'):
            # Newer Pillow
            pass
        else:
            print("Warning: PIL version may have compatibility issues", flush=True)
    except Exception as e:
        print(f"PIL compatibility check failed: {e}", flush=True)
    
    base_clip = None
    clip = None
    concat_clip = None
    background = None
    final_clip = None
    audio_clip = None
    clip_resized = None
    overlay_clips = None
    try:
        MIN_VIDEO_DURATION = 8  # Минимальная длина видео - 8 секунд
        random_duration = random.uniform(7, 12)
        ext = os.path.splitext(input_path)[1].lower()
        if ext in ['.png', '.jpg', '.jpeg']:
            # Static images - convert to video clip with duration
            clip = ImageClip(input_path, duration=random_duration)
            print(f"Processing static image: {input_path}", flush=True)
        elif ext == '.gif':
            # GIF files - process as video but ensure they loop properly
            base_clip = VideoFileClip(input_path)
            clip = base_clip
            if not is_youtube and clip.duration > random_duration:
                # For GIFs, we might want to loop them to fill duration
                if clip.duration < random_duration:
                    # Loop the GIF to reach desired duration
                    loops_needed = int(random_duration / clip.duration) + 1
                    from moviepy.editor import concatenate_videoclips
                    concat_clip = concatenate_videoclips([base_clip] * loops_needed)
                    clip = concat_clip.subclip(0, random_duration)
                else:
                    clip = clip.subclip(0, random_duration)
            elif clip.duration < MIN_VIDEO_DURATION:
                # Loop the GIF if it's shorter than minimum duration
                loops_needed = int(MIN_VIDEO_DURATION / clip.duration) + 1
                concat_clip = concatenate_videoclips([base_clip] * loops_needed)
                clip = concat_clip.subclip(0, MIN_VIDEO_DURATION)
            print(f"Processing GIF: {input_path} (original duration: {base_clip.duration}s)", flush=True)
        else:
            # Video files (mp4, webm, mov, etc.)
            base_clip = VideoFileClip(input_path)
            clip = base_clip
            if not is_youtube and clip.duration > random_duration:
                clip = clip.subclip(0, random_duration)
            elif clip.duration < MIN_VIDEO_DURATION:
                # Loop the video if it's shorter than minimum duration
                loops_needed = int(MIN_VIDEO_DURATION / clip.duration) + 1
                concat_clip = concatenate_videoclips([base_clip] * loops_needed)
                clip = concat_clip.subclip(0, MIN_VIDEO_DURATION)
            print(f"Processing video: {input_path} (duration: {base_clip.duration}s)", flush=True)
        
        # Гарантируем минимальную длину видео 8 секунд
        current_duration = getattr(clip, 'duration', 0) or 0
        if current_duration < MIN_VIDEO_DURATION:
            print(f"Video is too short ({current_duration:.1f}s), looping to reach {MIN_VIDEO_DURATION}s minimum", flush=True)
            loops_needed = int(MIN_VIDEO_DURATION / current_duration) + 1
            concat_clip = concatenate_videoclips([clip] * loops_needed)
            clip = concat_clip.subclip(0, MIN_VIDEO_DURATION)
            print(f"Video looped to {MIN_VIDEO_DURATION}s", flush=True)
        
        # Ограничиваем длительность, чтобы ролики подходили под Instagram Reels
        # и оставались короткими шортами.
        if clip.duration > 60:
            clip = clip.subclip(0, 60)

        # Универсальное вертикальное разрешение для TikTok/Instagram Reels
        tiktok_res = (1080, 1920)
        # universal resize and center (moviepy clips usually support .resize())
        try:
            resize_func = getattr(clip, 'resize', None)
            if callable(resize_func):
                # Calculate scale to fit within TikTok dimensions while maintaining aspect ratio
                original_size = getattr(clip, 'size', None)
                if original_size:
                    original_w, original_h = original_size
                    scale_w = tiktok_res[0] / original_w
                    scale_h = tiktok_res[1] / original_h
                    scale = min(scale_w, scale_h)  # Use smaller scale to fit within bounds
                    clip_resized = resize_func(scale)
                else:
                    clip_resized = resize_func(width=tiktok_res[0])
                
                # Center the clip in the TikTok frame
                set_position_func = getattr(clip_resized, 'set_position', None)
                if callable(set_position_func):
                    try:
                        clip_resized = set_position_func('center')
                    except Exception:
                        # Fallback: manually calculate center position
                        resized_size = getattr(clip_resized, 'size', original_size)
                        if resized_size:
                            pos_x = (tiktok_res[0] - resized_size[0]) // 2
                            pos_y = (tiktok_res[1] - resized_size[1]) // 2
                            clip_resized = set_position_func((pos_x, pos_y))
            else:
                clip_resized = clip
                set_position_func = getattr(clip_resized, 'set_position', None)
                if callable(set_position_func):
                    try:
                        clip_resized = set_position_func('center')
                    except Exception:
                        pass
        except Exception:
            clip_resized = clip.set_position('center')
        
        # Эффекты отключены - используем клип как есть
        # clip_resized = apply_random_effects(clip_resized, seed=seed, variant_group=variant_group)
        
        clip_duration = getattr(clip_resized, 'duration', getattr(clip, 'duration', 10)) or 10
        background = ColorClip(size=tiktok_res, color=(0, 0, 0), duration=clip_duration)
        final_clip = CompositeVideoClip([background, clip_resized])
        if audio_path and os.path.exists(audio_path):
            print(f"Adding audio from: {audio_path}", flush=True)
            try:
                audio_clip = AudioFileClip(audio_path)
                audio_duration_val = getattr(audio_clip, 'duration', 0)
                if audio_duration_val > 0:
                    max_d = getattr(clip_resized, 'duration', clip_duration)
                    
                    # Зацикливаем аудио, если оно короче видео
                    if audio_clip.duration < max_d:
                        print(f"Audio ({audio_duration_val:.1f}s) is shorter than video ({max_d:.1f}s), looping audio", flush=True)
                        loops_needed = int(max_d / audio_clip.duration) + 1
                        audio_looped = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_looped.subclip(0, max_d)
                        print(f"Audio looped to {max_d:.1f}s", flush=True)
                    elif audio_clip.duration > max_d:
                        audio_clip = audio_clip.subclip(0, max_d)
                    
                    try:
                        clip_with_audio = clip_resized.set_audio(audio_clip)  # type: ignore[attr-defined]
                        final_clip = CompositeVideoClip([background, clip_with_audio])
                        print(f"Audio added to video successfully (duration: {audio_duration_val:.1f}s)", flush=True)
                    except Exception as audio_err:
                        print(f"Failed to attach audio to video: {audio_err}", flush=True)
                        clip_with_audio = clip_resized
                        final_clip = CompositeVideoClip([background, clip_with_audio])
                        print("Video created WITHOUT audio due to attachment error", flush=True)
                else:
                    print(f"Audio file has zero duration, skipping audio", flush=True)
                    try:
                        audio_clip.close()
                    except Exception:
                        pass
            except Exception as audio_load_err:
                print(f"Failed to load audio file: {audio_load_err}", flush=True)
                print("Video will be created WITHOUT audio", flush=True)
        else:
            if audio_path:
                print(f"Audio file does not exist: {audio_path}", flush=True)
            else:
                print(f"No audio path provided", flush=True)
            print("Video will be created WITHOUT audio", flush=True)
        
        # Настройки кодека подобраны так, чтобы результат подходил
        # как для TikTok, так и для Instagram (H.264 + AAC в MP4).
        final_clip.write_videofile(
            output_path,
            codec='libx264',
            audio_codec='aac',
            fps=30,
            preset='medium',
            bitrate='8M',
            audio_bitrate='192k',
        )
        return output_path
    except Exception as e:
        print(f"Error during video conversion: {e}", flush=True)
        return None
    finally:
        # Clean up all video objects to free memory
        objs = [final_clip, audio_clip, background, clip_resized, clip, concat_clip, base_clip]
        if overlay_clips:
            objs.extend(overlay_clips if isinstance(overlay_clips, list) else [overlay_clips])
        for obj in objs:
            try:
                if obj:
                    obj.close()
            except Exception:
                pass

def generate_thumbnail(video_path: str, output_path: str, time: float = 1.0):
    try:
        with VideoFileClip(video_path) as clip:
            frame = clip.get_frame(time)
            from PIL import Image
            img = Image.fromarray(frame)
            # Handle different Pillow versions
            try:
                # For Pillow >= 10.0.0
                resample_method = Image.Resampling.LANCZOS
            except AttributeError:
                resample_method = getattr(Image, 'LANCZOS', None) or getattr(Image, 'BICUBIC', None)
            # Resize if needed (thumbnail generation)
            if img.size[0] > 320 or img.size[1] > 320:
                if resample_method is None:
                    img.thumbnail((320, 320))
                else:
                    img.thumbnail((320, 320), resample_method)
            img.save(output_path)
            return output_path
    except Exception as e:
        print(f'Error generating thumbnail: {e}', flush=True)
        return None

def get_video_metadata(video_path: str):
    try:
        if not os.path.exists(video_path):
            return None
        
        file_size = os.path.getsize(video_path)
        file_size_mb = file_size / (1024 * 1024)
        
        with VideoFileClip(video_path) as clip:
            duration = getattr(clip, 'duration', 0) or 0
            fps = getattr(clip, 'fps', 0) or 0
            size = getattr(clip, 'size', None)
            width, height = (size[0], size[1]) if size else (0, 0)
            
            has_audio = hasattr(clip, 'audio') and clip.audio is not None
            
            return {
                'filename': os.path.basename(video_path),
                'size_bytes': file_size,
                'size_mb': round(file_size_mb, 1),
                'duration': round(duration, 1),
                'fps': round(fps, 1) if fps else 0,
                'width': width,
                'height': height,
                'resolution': f"{width}x{height}" if width and height else "unknown",
                'has_audio': has_audio
            }
    except Exception as e:
        print(f'Error getting video metadata: {e}', flush=True)
        return {
            'filename': os.path.basename(video_path),
            'size_bytes': os.path.getsize(video_path) if os.path.exists(video_path) else 0,
            'size_mb': round(os.path.getsize(video_path) / (1024 * 1024), 1) if os.path.exists(video_path) else 0,
            'duration': 0,
            'fps': 0,
            'width': 0,
            'height': 0,
            'resolution': "unknown",
            'has_audio': False
        }

def add_text_to_video(input_path, output_path, text, position=("center", "bottom")):
    try:
        with VideoFileClip(input_path) as video_clip:
            with TextClip(txt=text, fontsize=70, color='white', stroke_color='black', stroke_width=2) as txt_clip:
                txt = txt_clip.set_position(position).set_duration(video_clip.duration)
                final = CompositeVideoClip([video_clip, txt])
                final.write_videofile(output_path, codec='libx264', audio_codec='aac', fps=24)
        return output_path
    except Exception as e:
        print(f'Error adding text: {e}')
        return None