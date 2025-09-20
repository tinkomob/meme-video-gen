import os
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

import argparse
from app.utils import load_urls_json
from app.service import generate_meme_video, deploy_to_socials

def main():
    parser = argparse.ArgumentParser(description='Generate meme video and optionally deploy to socials')
    parser.add_argument('--pinterest-json', default='pinterest_urls.json')
    parser.add_argument('--music-json', default='music_playlists.json')
    parser.add_argument('--pin-num', type=int, default=30)
    parser.add_argument('--audio-duration', type=int, default=10)
    parser.add_argument('--deploy', action='store_true')
    parser.add_argument('--privacy', default='public', choices=['public','unlisted','private'])
    args = parser.parse_args()
    pins = load_urls_json(args.pinterest_json)
    music = load_urls_json(args.music_json)
    result = generate_meme_video(pins, music, pin_num=args.pin_num, audio_duration=args.audio_duration)
    if args.deploy and result.video_path:
        links = deploy_to_socials(result.video_path, result.thumbnail_path, result.source_url, None, privacy=args.privacy)
        print(links)
    else:
        print({'video_path': result.video_path, 'thumbnail_path': result.thumbnail_path, 'source_url': result.source_url})

if __name__ == '__main__':
    main()