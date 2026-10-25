use crate::{
    model::{Song, SongsIndex, clean_author, eligible, valid_video_id},
    storage::Storage,
};
use anyhow::{Context, Result, bail, ensure};
use rand::Rng;
use std::{
    path::{Path, PathBuf},
    process::Stdio,
};
use tokio::process::Command;
pub async fn command(program: &str, args: &[&str]) -> Result<Vec<u8>> {
    let out = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .kill_on_drop(true)
        .output()
        .await
        .with_context(|| format!("run {program}"))?;
    ensure!(
        out.status.success(),
        "{program}: {}",
        String::from_utf8_lossy(&out.stderr)
    );
    Ok(out.stdout)
}
pub async fn songs(store: &Storage) -> Result<Vec<Song>> {
    let idx = store
        .read::<SongsIndex>("songs.json")
        .await?
        .unwrap_or_default();
    ensure!(!idx.items.is_empty(), "no songs found");
    Ok(idx.items)
}
pub fn choose_song(songs: Vec<Song>, id: &str) -> Result<Song> {
    if !id.is_empty() {
        return songs
            .into_iter()
            .find(|s| s.id == id)
            .context("song not found");
    }
    let pool: Vec<_> = songs.into_iter().filter(eligible).collect();
    ensure!(
        !pool.is_empty(),
        "no songs long enough for a 20-second snippet"
    );
    Ok(pool[rand::rng().random_range(0..pool.len())].clone())
}
pub async fn ensure_songs(store: &Storage) -> Result<()> {
    let mut index = store
        .read::<SongsIndex>("songs.json")
        .await?
        .unwrap_or_default();
    let data = match store.get("payload/music_playlists.json").await {
        Ok(Some(data)) => Some(data),
        other => {
            if let Err(e) = other {
                tracing::warn!("playlist S3 read: {e}");
            }
            tokio::fs::read("music_playlists.json").await.ok()
        }
    };
    let Some(data) = data else {
        tracing::info!("no playlists configured");
        return Ok(());
    };
    let playlists: Vec<String> = serde_json::from_slice(&data)?;
    let dir = tempfile::tempdir()?;
    let cookie = match store.get("payload/youtube_cookies.txt").await {
        Ok(Some(data)) => {
            let p = dir.path().join("cookies.txt");
            tokio::fs::write(&p, data).await?;
            Some(p)
        }
        Ok(None) => None,
        Err(e) => {
            tracing::warn!("YouTube cookies: {e}");
            None
        }
    };
    for url in playlists.into_iter().filter(|v| !v.trim().is_empty()) {
        let output = match command(
            "yt-dlp",
            &["--flat-playlist", "--dump-single-json", "--", &url],
        )
        .await
        {
            Ok(v) => v,
            Err(e) => {
                tracing::error!("playlist: {e}");
                continue;
            }
        };
        let playlist: serde_json::Value = serde_json::from_slice(&output)?;
        for entry in playlist["entries"]
            .as_array()
            .context("playlist has no entries")?
        {
            let id = entry["id"].as_str().unwrap_or_default();
            if !valid_video_id(id) || index.items.iter().any(|s| s.id == id) {
                continue;
            }
            let path = dir.path().join(format!("{id}.m4a"));
            let path_text = path.to_string_lossy();
            let cookie_text = cookie.as_ref().map(|p| p.to_string_lossy());
            let mut args = vec![
                "-x",
                "--audio-format",
                "m4a",
                "--no-playlist",
                "--quiet",
                "-o",
                &path_text,
            ];
            if let Some(ref c) = cookie_text {
                args.extend(["--cookies", c.as_ref()]);
            }
            args.extend(["--", id]);
            let mut downloaded = false;
            for attempt in 0..3 {
                match command("yt-dlp", &args).await {
                    Ok(_) => {
                        downloaded = true;
                        break;
                    }
                    Err(e) => {
                        let msg = e.to_string();
                        tracing::error!("download {id}: {msg}");
                        if ![
                            "403",
                            "429",
                            "timeout",
                            "TLS handshake",
                            "connection reset",
                            "connection refused",
                            "no such host",
                            "EOF",
                        ]
                        .iter()
                        .any(|s| msg.contains(s))
                        {
                            break;
                        }
                        tokio::time::sleep(std::time::Duration::from_secs(1 << attempt)).await;
                    }
                }
            }
            if !downloaded {
                continue;
            }
            let duration = probe(&path).await?;
            let data = tokio::fs::read(&path).await?;
            use sha2::Digest;
            let sha256 = hex::encode(sha2::Sha256::digest(&data));
            let key = format!("songs/{id}.m4a");
            store.put(&key, data, "audio/mp4").await?;
            let now = chrono::Utc::now().to_rfc3339();
            index.items.push(Song {
                id: id.into(),
                title: entry["title"].as_str().unwrap_or_default().into(),
                author: clean_author(
                    entry["channel"]
                        .as_str()
                        .or(entry["uploader"].as_str())
                        .unwrap_or_default(),
                ),
                source_url: format!("https://www.youtube.com/watch?v={id}"),
                audio_key: key,
                duration_s: duration,
                added_at: now.clone(),
                last_seen_at: now.clone(),
                sha256,
            });
            index.updated_at = now;
            store.write("songs.json", &index).await?;
            tokio::fs::remove_file(&path).await?;
        }
    }
    Ok(())
}
pub async fn probe(path: &Path) -> Result<f64> {
    let output = command(
        "ffprobe",
        &[
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=nw=1:nk=1",
            &path.to_string_lossy(),
        ],
    )
    .await?;
    let n: f64 = std::str::from_utf8(&output)?.trim().parse()?;
    ensure!(n.is_finite() && n > 0.0, "invalid audio duration");
    Ok(n)
}
pub async fn download(store: &Storage, song: &Song, dir: &Path) -> Result<PathBuf> {
    if song.audio_key.is_empty() {
        bail!("song audio key is empty")
    }
    let p = dir.join("song.m4a");
    store.download(&song.audio_key, &p).await?;
    Ok(p)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn selection_filters_short_and_missing_audio() {
        let good = Song {
            id: "good".into(),
            audio_key: "a".into(),
            duration_s: 20.0,
            ..Song::default()
        };
        for _ in 0..20 {
            assert_eq!(
                choose_song(
                    vec![
                        Song::default(),
                        Song {
                            duration_s: 19.0,
                            audio_key: "x".into(),
                            ..Song::default()
                        },
                        good.clone()
                    ],
                    ""
                )
                .unwrap()
                .id,
                "good"
            );
        }
        assert!(choose_song(vec![], "").is_err());
    }
}
