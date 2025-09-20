import os
import random

# Monkey patch for PIL compatibility - must be before moviepy import
import PIL.Image
if not hasattr(PIL.Image, 'ANTIALIAS'):
    PIL.Image.ANTIALIAS = PIL.Image.LANCZOS

# Disable PIL deprecation warnings
os.environ['PILLOW_IGNORE_DEPRECATION'] = '1'

from moviepy.editor import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, concatenate_videoclips, AudioFileClip
from moviepy.audio.AudioClip import concatenate_audioclips

def apply_random_effects(clip):
    effects = [
        lambda c: c.fx(vfx.blackwhite),
        lambda c: c.fx(vfx.mirrorx),
        lambda c: c.fx(vfx.mirrory),
        lambda c: c.fx(vfx.invert_colors),
        lambda c: c.fx(vfx.fadein, 2),
        lambda c: c.fx(vfx.fadeout, 2),
        lambda c: c.fx(vfx.crop, x1=50, x2=300),
        lambda c: c.fx(vfx.colorx, 1.5),
        lambda c: c.fx(vfx.colorx, 0.5),
        lambda c: c.fx(vfx.resize, lambda t: 1 + 0.3 * (t / c.duration)),
        lambda c: c.fx(vfx.rotate, lambda t: 10 * (t / c.duration)),
        lambda c: c.fx(vfx.colorx, lambda t: 1 + 0.5 * (t / c.duration)),
    ]
    num = random.randint(1, 4)
    for effect in random.sample(effects, num):
        try:
            clip = effect(clip)
        except Exception:
            continue
    return clip

def convert_to_tiktok_format(input_path, output_path, is_youtube=False, audio_path=None):
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
    try:
        ext = os.path.splitext(input_path)[1].lower()
        if ext in ['.png', '.jpg', '.jpeg']:
            clip = ImageClip(input_path, duration=10)
        else:
            base_clip = VideoFileClip(input_path)
            if is_youtube:
                clip = base_clip
            else:
                if base_clip.duration < 10:
                    n = int(10 / base_clip.duration) + 1
                    clips = [base_clip] * n
                    concat_clip = concatenate_videoclips(clips)
                    clip = concat_clip.set_duration(10)
                else:
                    clip = base_clip
        if clip.duration > 60:
            clip = clip.set_duration(60)
        tiktok_res = (1080, 1920)
        clip_resized = clip.resize(width=tiktok_res[0])
        clip_resized = apply_random_effects(clip_resized)
        background = ColorClip(size=tiktok_res, color=(0, 0, 0), duration=clip_resized.duration)
        final_clip = CompositeVideoClip([background, clip_resized.set_position('center')])
        
        # Add audio to the final composite clip
        if audio_path and os.path.exists(audio_path):
            print(f"Adding audio from: {audio_path}", flush=True)
            print(f"Audio file size: {os.path.getsize(audio_path)} bytes", flush=True)
            audio_clip = AudioFileClip(audio_path)
            print(f"Audio clip duration: {audio_clip.duration}", flush=True)
            if audio_clip.duration <= 0:
                print("Audio clip has zero or negative duration, skipping", flush=True)
                audio_clip.close()
            else:
                if audio_clip.duration > clip_resized.duration:
                    audio_clip = audio_clip.subclip(0, clip_resized.duration)
                elif audio_clip.duration < clip_resized.duration:
                    n_loops = int(clip_resized.duration / audio_clip.duration) + 1
                    audio_clip = concatenate_audioclips([audio_clip] * n_loops).subclip(0, clip_resized.duration)
                # Set audio on the clip before creating composite
                clip_with_audio = clip_resized.set_audio(audio_clip)
                final_clip = CompositeVideoClip([background, clip_with_audio.set_position('center')])
                print("Audio added to video successfully", flush=True)
        else:
            print(f"No audio to add - audio_path: {audio_path}, exists: {audio_path and os.path.exists(audio_path)}", flush=True)
        final_clip.write_videofile(output_path, codec='libx264', audio_codec='aac', fps=24)
        return output_path
    except Exception as e:
        print(f'Error during video conversion: {e}', flush=True)
        return None
    finally:
        try:
            if final_clip:
                final_clip.close()
        except Exception:
            pass
        try:
            if audio_clip:
                audio_clip.close()
        except Exception:
            pass
        try:
            if background:
                background.close()
        except Exception:
            pass
        try:
            if clip_resized:
                clip_resized.close()
        except Exception:
            pass
        try:
            if clip:
                clip.close()
        except Exception:
            pass
        try:
            if concat_clip:
                concat_clip.close()
        except Exception:
            pass
        try:
            if base_clip:
                base_clip.close()
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
                # For older Pillow versions
                resample_method = Image.ANTIALIAS
            # Resize if needed (thumbnail generation)
            if img.size[0] > 320 or img.size[1] > 320:
                img.thumbnail((320, 320), resample_method)
            img.save(output_path)
            return output_path
    except Exception as e:
        print(f'Error generating thumbnail: {e}', flush=True)
        return None

def add_text_to_video(input_path, output_path, text, position=("center", "bottom")):
    try:
        with VideoFileClip(input_path) as video_clip:
            with TextClip(text=text, font_size=70, color='white', stroke_color='black', stroke_width=2) as txt_clip:
                txt = txt_clip.set_position(position).set_duration(video_clip.duration)
                final = CompositeVideoClip([video_clip, txt])
                final.write_videofile(output_path, codec='libx264', audio_codec='aac', fps=24)
        return output_path
    except Exception as e:
        print(f'Error adding text: {e}')
        return None