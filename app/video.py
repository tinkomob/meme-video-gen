import os
import random

# Monkey patch for PIL compatibility - must be before moviepy import
import PIL.Image
if not hasattr(PIL.Image, 'ANTIALIAS'):
    PIL.Image.ANTIALIAS = PIL.Image.LANCZOS

# Disable PIL deprecation warnings
os.environ['PILLOW_IGNORE_DEPRECATION'] = '1'

from moviepy.editor import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, AudioFileClip

def apply_random_effects(clip, seed=None, variant_group=None):
    if seed is not None:
        random.seed(seed)
    def _fx(name):
        return getattr(vfx, name, None)
    effects_bank = [
        lambda c: c.fx(_fx('mirrorx')) if _fx('mirrorx') else c,
        lambda c: c.fx(_fx('mirrory')) if _fx('mirrory') else c,
        lambda c: c.fx(_fx('fadein'), random.uniform(0.2, 0.8)) if _fx('fadein') else c,
        lambda c: c.fx(_fx('fadeout'), random.uniform(0.2, 0.8)) if _fx('fadeout') else c,
        lambda c: c.fx(_fx('colorx'), random.uniform(0.7, 1.4)) if _fx('colorx') else c,
        lambda c: c.fx(_fx('speedx'), random.uniform(0.95, 1.05)) if _fx('speedx') else c,
        lambda c: c.fx(_fx('blackwhite')) if _fx('blackwhite') and random.random() < 0.35 else c,
        lambda c: c.fx(_fx('margin'), left=random.randint(0,30), right=random.randint(0,30), top=random.randint(0,60), bottom=random.randint(0,60), color=(0,0,0)) if _fx('margin') else c,
        lambda c: c.fx(_fx('crop'), x1=random.randint(0,15), y1=random.randint(0,30), x2=None, y2=None) if _fx('crop') else c,
        lambda c: c.set_opacity(random.uniform(0.88, 1.0)),
        lambda c: c.fx(_fx('time_symetrize')) if _fx('time_symetrize') and random.random() < 0.2 else c,
    ]
    variant_sets = [
        [0,2,4,7],
        [1,3,5,8],
        [0,5,9],
        [2,6,7,10],
        [1,4,8,9],
    ]
    if variant_group is not None and 0 <= int(variant_group) < len(variant_sets):
        pool_idx = variant_sets[int(variant_group)]
    else:
        pool_idx = list(range(len(effects_bank)))
    pool = [effects_bank[i] for i in pool_idx]
    k = random.randint(2, min(4, len(pool)))
    chosen = random.sample(pool, k)
    random.shuffle(chosen)
    for fx_func in chosen:
        try:
            clip = fx_func(clip)
        except Exception:
            continue
    return clip

def convert_to_tiktok_format(input_path, output_path, is_youtube=False, audio_path=None, seed=None, variant_group=None):
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
    try:
        random_duration = random.uniform(7, 12)
        ext = os.path.splitext(input_path)[1].lower()
        if ext in ['.png', '.jpg', '.jpeg']:
            clip = ImageClip(input_path, duration=random_duration)
        else:
            base_clip = VideoFileClip(input_path)
            clip = base_clip
            if not is_youtube and clip.duration > random_duration:
                clip = clip.subclip(0, random_duration)
        if clip.duration > 60:
            clip = clip.subclip(0, 60)
        tiktok_res = (1080, 1920)
        # universal resize (moviepy clips usually support .resize())
        try:
            resize_func = getattr(clip, 'resize', None)
            if callable(resize_func):
                clip_resized = resize_func(width=tiktok_res[0])
            else:
                clip_resized = clip
        except Exception:
            clip_resized = clip
        clip_resized = apply_random_effects(clip_resized, seed=seed, variant_group=variant_group)
        clip_duration = getattr(clip_resized, 'duration', getattr(clip, 'duration', 10)) or 10
        background = ColorClip(size=tiktok_res, color=(0, 0, 0), duration=clip_duration)
        final_clip = CompositeVideoClip([background, clip_resized])
        if audio_path and os.path.exists(audio_path):
            print(f"Adding audio from: {audio_path}", flush=True)
            audio_clip = AudioFileClip(audio_path)
            if getattr(audio_clip, 'duration', 0) > 0:
                max_d = getattr(clip_resized, 'duration', clip_duration)
                if audio_clip.duration > max_d:
                    audio_clip = audio_clip.subclip(0, max_d)
                else:
                    audio_clip = audio_clip.subclip(0, min(audio_clip.duration, max_d))
                # attempt to attach audio; if not supported, ignore
                try:
                    clip_with_audio = clip_resized.set_audio(audio_clip)  # type: ignore[attr-defined]
                except Exception:
                    clip_with_audio = clip_resized
                final_clip = CompositeVideoClip([background, clip_with_audio])
                print("Audio added to video successfully", flush=True)
            else:
                try:
                    audio_clip.close()
                except Exception:
                    pass
        else:
            print(f"No audio to add - audio_path: {audio_path}, exists: {audio_path and os.path.exists(audio_path)}", flush=True)
        final_clip.write_videofile(output_path, codec='libx264', audio_codec='aac', fps=24)
        return output_path
    except Exception as e:
        print(f"Error during video conversion: {e}", flush=True)
        return None
    finally:
        objs = [final_clip, audio_clip, background, clip_resized, clip, concat_clip, base_clip]
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