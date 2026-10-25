use crate::storage::Storage;
use anyhow::{Context, Result, ensure};
use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::{DefaultBodyLimit, Path, State},
    http::{HeaderMap, StatusCode, header},
    response::{Html, IntoResponse, Response},
    routing::{get, post},
};
use rand::Rng;
use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::sync::{Arc, LazyLock};
use tokio::sync::Mutex;
static FILENAME: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?i)friendss(\d{2})e(\d{2})(?:-(\d{2}))?(?:\.|\[|$)").unwrap());
static PART: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?i)(?:часть|part)\s*(?:№\s*)?([\p{L}\p{N}]+)").unwrap());
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Episode {
    pub season_number: u32,
    pub episode_number_in_season: u32,
    pub title_eng: String,
    pub title_ru: String,
    #[serde(skip)]
    pub video_key: String,
    #[serde(skip)]
    pub video_size: u64,
}
impl Episode {
    pub fn id(&self) -> String {
        format!(
            "s{:02}e{:02}",
            self.season_number, self.episode_number_in_season
        )
    }
    pub fn part(&self) -> u32 {
        for title in [&self.title_ru, &self.title_eng] {
            if let Some(m) = PART.captures(title) {
                let v = m[1].to_lowercase();
                let n = v.parse().unwrap_or(match v.as_str() {
                    "первый" | "первая" | "первое" | "первые" | "first" | "one" | "i" => {
                        1
                    }
                    "второй" | "вторая" | "второе" | "вторые" | "second" | "two" | "ii" => {
                        2
                    }
                    "третий" | "третья" | "третье" | "третьи" | "third" | "three" | "iii" => {
                        3
                    }
                    "четвертый" | "четвёртый" | "четвертая" | "четвёртая" | "fourth" | "four"
                    | "iv" => 4,
                    _ => 0,
                });
                if n > 0 {
                    return n;
                }
            }
        }
        0
    }
}
pub fn matches_filename(name: &str, season: u32, episode: u32) -> bool {
    let Some(m) = FILENAME.captures(name) else {
        return false;
    };
    let s: u32 = m[1].parse().unwrap();
    let first: u32 = m[2].parse().unwrap();
    let last = m
        .get(3)
        .map(|m| m.as_str().parse().unwrap())
        .unwrap_or(first);
    s == season && (first..=last).contains(&episode)
}
#[derive(Clone)]
pub struct Friends {
    store: Storage,
    episodes: Arc<Mutex<Option<Vec<Episode>>>>,
}
impl Friends {
    pub fn new(store: Storage) -> Self {
        Self {
            store,
            episodes: Arc::new(Mutex::new(None)),
        }
    }
    pub async fn episodes(&self) -> Result<Vec<Episode>> {
        let mut cache = self.episodes.lock().await;
        if let Some(e) = cache.as_ref() {
            return Ok(e.clone());
        }
        let metadata: Vec<Episode> = self
            .store
            .read("friends/friends_series_list.json")
            .await?
            .context("Friends episode list not found")?;
        let objects = self.store.list("friends/").await?;
        let mut episodes = vec![];
        for mut episode in metadata {
            if let Some(object) = objects.iter().find(|o| {
                o.key.to_lowercase().ends_with(".mp4")
                    && matches_filename(
                        o.key.rsplit('/').next().unwrap_or_default(),
                        episode.season_number,
                        episode.episode_number_in_season,
                    )
            }) {
                episode.video_key = object.key.clone();
                episode.video_size = object.size;
                episodes.push(episode);
            }
        }
        ensure!(!episodes.is_empty(), "no Friends videos matched");
        episodes.sort_by_key(Episode::id);
        *cache = Some(episodes.clone());
        Ok(episodes)
    }
    async fn find(&self, id: &str) -> Result<Option<Episode>> {
        Ok(self.episodes().await?.into_iter().find(|e| e.id() == id))
    }
}
fn response(e: &Episode) -> serde_json::Value {
    json!({"id":e.id(),"season":e.season_number,"episode":e.episode_number_in_season,"title_ru":e.title_ru,"video_url":format!("/api/friends/video/{}",e.id()),"is_first_part":e.part()==1})
}
fn error(status: StatusCode, msg: &str) -> Response {
    (status, Json(json!({"error":msg}))).into_response()
}
pub fn router(friends: Friends) -> Router {
    Router::new()
        .route(
            "/friends",
            get(|| async { Html(include_str!("../assets/friends.html")) }),
        )
        .route(
            "/friends/logo.png",
            get(|| async {
                (
                    [
                        (header::CONTENT_TYPE, "image/png"),
                        (header::CACHE_CONTROL, "public, max-age=31536000, immutable"),
                    ],
                    include_bytes!("../assets/friends_logo.png").as_slice(),
                )
            }),
        )
        .route("/api/friends/random", post(random))
        .route("/api/friends/episode/{id}", get(episode))
        .route("/api/friends/video/{id}", get(video))
        .layer(DefaultBodyLimit::max(8192))
        .with_state(friends)
}
#[derive(Default, Deserialize)]
struct RandomRequest {
    #[serde(default, deserialize_with = "crate::model::nullable_vec")]
    excluded: Vec<String>,
}
async fn random(State(f): State<Friends>, body: Bytes) -> Response {
    let request: RandomRequest = if body.is_empty() {
        RandomRequest::default()
    } else {
        match serde_json::from_slice(&body) {
            Ok(v) => v,
            Err(_) => return error(StatusCode::BAD_REQUEST, "Некорректная история просмотра"),
        }
    };
    match f.episodes().await {
        Ok(episodes) => {
            let eligible: Vec<_> = episodes.iter().filter(|e| e.part() <= 1).collect();
            if eligible.is_empty() {
                return error(StatusCode::SERVICE_UNAVAILABLE, "Серия пока недоступна");
            }
            let available: Vec<_> = eligible
                .iter()
                .copied()
                .filter(|e| !request.excluded.contains(&e.id()))
                .collect();
            let pool = if available.is_empty() {
                eligible
            } else {
                available
            };
            Json(response(pool[rand::rng().random_range(0..pool.len())])).into_response()
        }
        Err(e) => {
            tracing::error!("friends random: {e}");
            error(StatusCode::SERVICE_UNAVAILABLE, "Серия пока недоступна")
        }
    }
}
async fn episode(State(f): State<Friends>, Path(id): Path<String>) -> Response {
    match f.find(&id).await {
        Ok(Some(e)) => Json(response(&e)).into_response(),
        Ok(None) => error(StatusCode::NOT_FOUND, "Серия не найдена"),
        Err(e) => {
            tracing::error!("friends episode: {e}");
            error(StatusCode::SERVICE_UNAVAILABLE, "Серия временно недоступна")
        }
    }
}
pub fn parse_range(header: &str, size: u64) -> Result<Option<(u64, u64)>> {
    if header.is_empty() {
        return Ok(None);
    }
    ensure!(size > 0 && !header.contains(','), "invalid range");
    let value = header
        .strip_prefix("bytes=")
        .context("invalid range unit")?;
    let (a, b) = value.split_once('-').context("invalid range")?;
    if a.is_empty() {
        let n: u64 = b.parse()?;
        ensure!(n > 0, "invalid suffix");
        return Ok(Some((size - n.min(size), size - 1)));
    }
    let start: u64 = a.parse()?;
    ensure!(start < size, "invalid start");
    let end = if b.is_empty() {
        size - 1
    } else {
        b.parse::<u64>()?.min(size - 1)
    };
    ensure!(end >= start, "invalid end");
    Ok(Some((start, end)))
}
async fn video(State(f): State<Friends>, Path(id): Path<String>, headers: HeaderMap) -> Response {
    let e = match f.find(&id).await {
        Ok(Some(e)) => e,
        Ok(None) => return error(StatusCode::NOT_FOUND, "Серия не найдена"),
        Err(e) => {
            tracing::error!("friends lookup: {e}");
            return error(StatusCode::SERVICE_UNAVAILABLE, "Видео временно недоступно");
        }
    };
    let range = match parse_range(
        headers
            .get(header::RANGE)
            .and_then(|s| s.to_str().ok())
            .unwrap_or_default(),
        e.video_size,
    ) {
        Ok(r) => r,
        Err(_) => {
            let mut r = error(
                StatusCode::RANGE_NOT_SATISFIABLE,
                "Недопустимый диапазон видео",
            );
            r.headers_mut().insert(
                header::CONTENT_RANGE,
                format!("bytes */{}", e.video_size).parse().unwrap(),
            );
            return r;
        }
    };
    match f.store.open(&e.video_key, range).await {
        Ok(upstream) => {
            let mut builder = Response::builder()
                .status(if range.is_some() { 206 } else { 200 })
                .header(header::CONTENT_TYPE, "video/mp4")
                .header(header::ACCEPT_RANGES, "bytes");
            if let Some((a, b)) = range {
                builder = builder
                    .header(
                        header::CONTENT_RANGE,
                        format!("bytes {a}-{b}/{}", e.video_size),
                    )
                    .header(header::CONTENT_LENGTH, b - a + 1);
            } else {
                builder = builder.header(header::CONTENT_LENGTH, e.video_size);
            }
            builder
                .body(Body::from_stream(upstream.bytes_stream()))
                .unwrap()
        }
        Err(e) => {
            tracing::error!("friends stream: {e}");
            error(StatusCode::BAD_GATEWAY, "Не удалось открыть видео")
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn ranges() {
        for (h, expected) in [
            ("bytes=0-9", (0, 9)),
            ("bytes=90-", (90, 99)),
            ("bytes=-10", (90, 99)),
            ("bytes=90-1000", (90, 99)),
            ("bytes=-200", (0, 99)),
        ] {
            assert_eq!(parse_range(h, 100).unwrap(), Some(expected));
        }
        for h in [
            "bytes=100-",
            "bytes=10-5",
            "bytes=-0",
            "bytes=0-1,3-4",
            "nonsense",
            "bytes=--1",
        ] {
            assert!(parse_range(h, 100).is_err(), "{h}");
        }
    }
    #[test]
    fn combined_and_season_prefix() {
        assert!(matches_filename("Season 2_FriendsS09E23-24.mp4", 9, 24));
        assert!(!matches_filename("FriendsS09E23.mp4", 9, 24));
    }
    #[test]
    fn russian_and_english_parts() {
        for title in ["The One Part II", "Часть вторая"] {
            assert_eq!(
                Episode {
                    title_ru: title.into(),
                    ..Episode::default()
                }
                .part(),
                2
            );
        }
    }
}
