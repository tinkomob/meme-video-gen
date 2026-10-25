use anyhow::{Result, ensure};
use axum::{body::Body, http::Request};
use meme_video_gen::{
    audio,
    config::{self, Config},
    friends::{self, Friends},
    storage::Storage,
};
use tower::ServiceExt;
#[tokio::test]
#[ignore = "requires real .env/S3; reads only and never publishes"]
async fn live_s3_reads_existing_library_and_friends() -> Result<()> {
    config::load_env();
    let store = Storage::new(Config::load()?)?;
    let songs = audio::songs(&store).await?;
    let song = songs
        .iter()
        .find(|s| meme_video_gen::model::eligible(s))
        .ok_or_else(|| anyhow::anyhow!("no eligible song"))?;
    let dir = tempfile::tempdir()?;
    let path = audio::download(&store, song, dir.path()).await?;
    ensure!(audio::probe(&path).await? >= 20.0, "song is too short");
    let f = Friends::new(store);
    let episodes = f.episodes().await?;
    let e = &episodes[0];
    let response = friends::router(f)
        .oneshot(
            Request::builder()
                .uri(format!("/api/friends/video/{}", e.id()))
                .header("Range", "bytes=0-1023")
                .body(Body::empty())?,
        )
        .await?;
    ensure!(response.status() == 206, "live Friends range failed");
    let bytes = axum::body::to_bytes(response.into_body(), 2048).await?;
    ensure!(bytes.len() == 1024, "truncated live range");
    println!(
        "Read {} songs, {} Friends episodes; audio and 1 KiB video range verified",
        songs.len(),
        episodes.len()
    );
    Ok(())
}
