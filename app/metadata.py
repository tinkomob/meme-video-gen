import os
import random
import requests
from .audio import get_song_title

def get_random_fact():
    try:
        api_key = os.getenv('API_NINJAS_KEY')
        if api_key:
            url = 'https://api.api-ninjas.com/v1/facts'
            headers = {'X-Api-Key': api_key}
            r = requests.get(url, headers=headers, timeout=10)
            r.raise_for_status()
            data = r.json()
            if isinstance(data, list) and data:
                fact = data[0].get('fact')
                if fact:
                    return fact
    except Exception:
        pass
    facts = [
        'The shortest war in history lasted only 38-45 minutes.',
        "A group of flamingos is called a 'flamboyance'.",
        'Octopuses have three hearts and blue blood.',
        'A day on Venus is longer than its year.',
        "Bananas are berries, but strawberries aren't.",
        'The human brain uses about 20% of the body\'s total energy.',
        'There are more possible games of chess than atoms in the observable universe.',
        "A shrimp's heart is in its head.",
    ]
    return random.choice(facts)

def generate_metadata_from_source(source_url: str, download_meta: dict | None, audio_path: str | None = None):
    title = None
    tags = ['meme', 'funny', 'shorts']
    if audio_path:
        title = get_song_title(audio_path)
    elif download_meta:
        title = download_meta.get('title')
        if download_meta.get('uploader'):
            tags.insert(0, download_meta.get('uploader'))
    else:
        title = 'Funny Meme #Shorts' if 'youtube' not in source_url else 'Funny YouTube Short #Shorts'
    fact = get_random_fact()
    description = f"Did you know? {fact}\n\n#Shorts #Meme #Funny"
    if title and '#Shorts' not in title:
        title += ' #Shorts'
    if title and len(title) > 100:
        title = title[:97] + '...'
    return {'title': title, 'description': description, 'tags': tags}