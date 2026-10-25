use axum::{
    Router,
    body::Body,
    extract::State,
    http::{Request, StatusCode},
    response::{IntoResponse, Response},
    routing::any,
};
use http_body_util::BodyExt;
use meme_video_gen::{
    config::Config,
    friends::{self, Friends},
    model::{Draft, Song, SongsIndex},
    service::Service,
    upload::Telegram,
};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
};
use tower::ServiceExt;
#[derive(Default)]
struct Data {
    objects: HashMap<String, Vec<u8>>,
    uploads: usize,
    writes: usize,
    fail_write: Option<usize>,
    ranges: Vec<String>,
}
#[derive(Clone)]
struct Mock {
    data: Arc<Mutex<Data>>,
    base: String,
}
impl Mock {
    async fn start() -> (Self, tokio::task::JoinHandle<()>) {
        let data = Arc::new(Mutex::new(Data::default()));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let router = Router::new()
            .fallback(any(handler))
            .with_state(data.clone());
        let task = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        (Self { data, base }, task)
    }
    fn svc(&self) -> Service {
        let cfg = Config {
            telegram_token: "test-token".into(),
            endpoint: self.base.clone(),
            region: "test-region".into(),
            bucket: "bucket".into(),
            access_key: "test-access".into(),
            secret_key: "test-secret".into(),
            posts_chat_id: -100,
            owner_chat_id: 0,
            silent: true,
        };
        let mut svc = Service::new(cfg).unwrap();
        svc.telegram = Telegram::with_base("test-token".into(), &self.base).unwrap();
        svc
    }
    fn put(&self, key: &str, value: impl AsRef<[u8]>) {
        self.data
            .lock()
            .unwrap()
            .objects
            .insert(format!("/bucket/{key}"), value.as_ref().to_vec());
    }
}
async fn handler(State(data): State<Arc<Mutex<Data>>>, request: Request<Body>) -> Response {
    let path = request.uri().path().to_owned();
    let method = request.method().clone();
    let query = request.uri().query().unwrap_or_default().to_owned();
    let range = request
        .headers()
        .get("range")
        .and_then(|s| s.to_str().ok())
        .map(str::to_owned);
    if path.starts_with("/bottest-token/") {
        let bytes = request.into_body().collect().await.unwrap().to_bytes();
        assert!(String::from_utf8_lossy(&bytes).contains("true"));
        data.lock().unwrap().uploads += 1;
        return axum::Json(json!({"ok":true,"result":{"message_id":42}})).into_response();
    }
    assert!(
        request
            .headers()
            .get("authorization")
            .unwrap()
            .to_str()
            .unwrap()
            .starts_with("AWS4-HMAC-SHA256 Credential=test-access/")
    );
    assert!(request.headers().contains_key("x-amz-date"));
    assert!(request.headers().contains_key("x-amz-content-sha256"));
    let bytes = request.into_body().collect().await.unwrap().to_bytes();
    let mut data = data.lock().unwrap();
    match method.as_str() {
        "PUT" => {
            data.writes += 1;
            if data.fail_write == Some(data.writes) {
                return StatusCode::SERVICE_UNAVAILABLE.into_response();
            }
            data.objects.insert(path, bytes.to_vec());
            StatusCode::OK.into_response()
        }
        "DELETE" => {
            data.objects.remove(&path);
            StatusCode::NO_CONTENT.into_response()
        }
        "GET" if query.contains("list-type=2") => {
            let list = data
                .objects
                .iter()
                .filter(|(k, _)| k.ends_with(".mp4"))
                .map(|(k, v)| {
                    format!(
                        "<Contents><Key>{}</Key><Size>{}</Size></Contents>",
                        k.trim_start_matches("/bucket/"),
                        v.len()
                    )
                })
                .collect::<String>();
            (
                [(axum::http::header::CONTENT_TYPE, "application/xml")],
                format!("<ListBucketResult>{list}</ListBucketResult>"),
            )
                .into_response()
        }
        "GET" => {
            let Some(value) = data.objects.get(&path).cloned() else {
                return StatusCode::NOT_FOUND.into_response();
            };
            if let Some(range) = range {
                data.ranges.push(range.clone());
                let (a, b) = meme_video_gen::friends::parse_range(&range, value.len() as u64)
                    .unwrap()
                    .unwrap();
                (
                    StatusCode::PARTIAL_CONTENT,
                    value[a as usize..=b as usize].to_vec(),
                )
                    .into_response()
            } else {
                value.into_response()
            }
        }
        _ => StatusCode::METHOD_NOT_ALLOWED.into_response(),
    }
}
fn draft() -> Draft {
    Draft {
        id: "0123456789abcdef01234567".into(),
        chat_id: 1,
        user_id: 2,
        song: Song {
            title: "Song <&>".into(),
            author: "Artist".into(),
            ..Default::default()
        },
        style: "silver".into(),
        variant: 0,
        selected: vec!["telegram".into()],
        targets: Default::default(),
        created_at: "2026-10-25T00:00:00Z".into(),
    }
}
#[tokio::test]
async fn persisted_publication_survives_restart_and_does_not_duplicate() {
    let (mock, task) = Mock::start().await;
    let svc = mock.svc();
    let mut d = draft();
    mock.put(&format!("snippet-drafts/{}.mp4", d.id), b"saved video");
    svc.save_draft(&d).await.unwrap();
    let result = svc
        .publish_draft(&mut d, &["telegram".into()])
        .await
        .unwrap();
    assert!(result[0].1.is_ok());
    assert_eq!(d.targets["telegram"], "sent");
    let restarted = mock.svc();
    let mut loaded = restarted.load_draft(&d.id).await.unwrap();
    assert!(
        restarted
            .publish_draft(&mut loaded, &["telegram".into()])
            .await
            .unwrap()
            .is_empty()
    );
    assert_eq!(mock.data.lock().unwrap().uploads, 1);
    loaded.targets.insert("telegram".into(), "sending".into());
    svc.save_draft(&loaded).await.unwrap();
    assert!(
        svc.publish_draft(&mut loaded, &["telegram".into()])
            .await
            .unwrap()
            .is_empty()
    );
    assert_eq!(mock.data.lock().unwrap().uploads, 1);
    task.abort();
}
#[tokio::test]
async fn claim_write_failure_prevents_upload_and_result_failure_keeps_sending() {
    let (mock, task) = Mock::start().await;
    let svc = mock.svc();
    let mut d = draft();
    mock.put(&format!("snippet-drafts/{}.mp4", d.id), b"video");
    svc.save_draft(&d).await.unwrap();
    mock.data.lock().unwrap().fail_write = Some(2);
    assert!(
        svc.publish_draft(&mut d, &["telegram".into()])
            .await
            .is_err()
    );
    assert_eq!(mock.data.lock().unwrap().uploads, 0);
    assert!(!d.targets.contains_key("telegram"));
    mock.data.lock().unwrap().fail_write = Some(4);
    assert!(
        svc.publish_draft(&mut d, &["telegram".into()])
            .await
            .is_err()
    );
    assert_eq!(mock.data.lock().unwrap().uploads, 1);
    assert_eq!(d.targets["telegram"], "sending");
    assert_eq!(
        svc.load_draft(&d.id).await.unwrap().targets["telegram"],
        "sending"
    );
    assert!(
        svc.publish_draft(&mut d, &["telegram".into()])
            .await
            .unwrap()
            .is_empty()
    );
    task.abort();
}
#[tokio::test]
async fn go_null_fields_load_and_deleted_drafts_disappear() {
    let (mock, task) = Mock::start().await;
    let svc = mock.svc();
    let mut json = serde_json::to_value(draft()).unwrap();
    json["selected"] = Value::Null;
    json["targets"] = Value::Null;
    mock.put(
        "snippet-drafts/0123456789abcdef01234567.json",
        serde_json::to_vec(&json).unwrap(),
    );
    let d = svc.load_draft("0123456789abcdef01234567").await.unwrap();
    assert!(d.selected.is_empty() && d.targets.is_empty());
    svc.delete_draft(&d.id).await.unwrap();
    assert!(svc.load_draft(&d.id).await.is_err());
    task.abort();
}
#[tokio::test]
async fn friends_routes_stream_ranges_and_preserve_player() {
    let (mock, task) = Mock::start().await;
    mock.put("friends/friends_series_list.json",serde_json::to_vec(&json!([{"season_number":2,"episode_number_in_season":1,"title_ru":"История. Часть 1"},{"season_number":2,"episode_number_in_season":2,"title_ru":"История. Часть 2"}])).unwrap());
    let bytes: Vec<_> = (0..100).collect();
    mock.put("friends/Season2_FriendsS02E01-02.mp4", &bytes);
    let app = friends::router(Friends::new(mock.svc().store));
    let request = |url: &str| Request::builder().uri(url).body(Body::empty()).unwrap();
    let page = app.clone().oneshot(request("/friends")).await.unwrap();
    assert_eq!(page.status(), 200);
    let html = String::from_utf8(
        page.into_body()
            .collect()
            .await
            .unwrap()
            .to_bytes()
            .to_vec(),
    )
    .unwrap();
    assert!(
        html.contains("friends-last-watch")
            && html.contains("continueAfterFirstPart")
            && html.contains("/friends/logo.png")
    );
    let random = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/friends/random")
                .header("Content-Type", "application/json")
                .body(Body::from(r#"{"excluded":["s02e01"]}"#))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(random.status(), 200);
    let v: Value =
        serde_json::from_slice(&random.into_body().collect().await.unwrap().to_bytes()).unwrap();
    assert_eq!(v["id"], "s02e01");
    assert_eq!(v["is_first_part"], true);
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/friends/video/s02e02")
                .header("Range", "bytes=10-19")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), 206);
    assert_eq!(response.headers()["content-range"], "bytes 10-19/100");
    assert_eq!(response.headers()["content-type"], "video/mp4");
    assert_eq!(
        response
            .into_body()
            .collect()
            .await
            .unwrap()
            .to_bytes()
            .as_ref(),
        &bytes[10..20]
    );
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/friends/video/s02e01")
                .header("Range", "bytes=100-")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), 416);
    assert_eq!(response.headers()["content-range"], "bytes */100");
    let response = app
        .oneshot(request("/api/friends/episode/s99e99"))
        .await
        .unwrap();
    assert_eq!(response.status(), 404);
    assert_eq!(mock.data.lock().unwrap().ranges, vec!["bytes=10-19"]);
    task.abort();
}
#[tokio::test]
async fn actual_ffmpeg_renders_twenty_second_video_with_audio() {
    use meme_video_gen::{audio, snippet};
    let (mock, task) = Mock::start().await;
    let dir = tempfile::tempdir().unwrap();
    let audio_path = dir.path().join("input.m4a");
    audio::command(
        "ffmpeg",
        &[
            "-hide_banner",
            "-loglevel",
            "error",
            "-y",
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:duration=21",
            "-c:a",
            "aac",
            &audio_path.to_string_lossy(),
        ],
    )
    .await
    .unwrap();
    let song = Song {
        id: "abcdefghijk".into(),
        title: "Тестовый трек <&>".into(),
        author: "Артист - Topic".into(),
        audio_key: "songs/test.m4a".into(),
        duration_s: 21.0,
        ..Default::default()
    };
    let index = SongsIndex {
        items: vec![song],
        ..Default::default()
    };
    mock.put("songs.json", serde_json::to_vec(&index).unwrap());
    mock.put("songs/test.m4a", std::fs::read(audio_path).unwrap());
    let image = image::RgbImage::from_pixel(600, 600, image::Rgb([85, 100, 170]));
    let mut cover = std::io::Cursor::new(vec![]);
    image
        .write_to(&mut cover, image::ImageFormat::Jpeg)
        .unwrap();
    mock.put("covers/abcdefghijk.jpg", cover.into_inner());
    let generated = snippet::generate(&mock.svc().store, "rain", 2, "")
        .await
        .unwrap();
    assert_eq!(generated.song.author, "Артист");
    let probe = audio::command(
        "ffprobe",
        &[
            "-v",
            "error",
            "-show_streams",
            "-show_format",
            "-of",
            "json",
            &generated.path().to_string_lossy(),
        ],
    )
    .await
    .unwrap();
    let probe: Value = serde_json::from_slice(&probe).unwrap();
    let streams = probe["streams"].as_array().unwrap();
    let video = streams.iter().find(|s| s["codec_type"] == "video").unwrap();
    assert_eq!(video["codec_name"], "h264");
    assert_eq!(video["width"], 1080);
    assert_eq!(video["height"], 1920);
    assert_eq!(video["r_frame_rate"], "30/1");
    assert!(streams.iter().any(|s| s["codec_name"] == "aac"));
    let duration: f64 = probe["format"]["duration"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    assert!((duration - 20.0).abs() < 0.1);
    let output = dir.path().join("result.mp4");
    snippet::save_new(&generated.path(), &output).await.unwrap();
    assert!(snippet::save_new(&generated.path(), &output).await.is_err());
    task.abort();
}
#[tokio::test]
async fn oauth_exchange_accepts_standard_response_and_adds_expiry() {
    use meme_video_gen::upload::{OAuthClient, exchange};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let endpoint = format!("http://{}", listener.local_addr().unwrap());
    let app = Router::new().route(
        "/token",
        axum::routing::post(|body: String| async move {
            assert!(body.contains("grant_type=refresh_token"));
            assert!(body.contains("client_id=client"));
            axum::Json(json!({"access_token":"new-access","token_type":"Bearer","expires_in":3600}))
        }),
    );
    let task = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let cfg=OAuthClient::parse(json!({"installed":{"client_id":"client","client_secret":"secret","token_uri":format!("{endpoint}/token")}})).unwrap();
    let token = exchange(
        &reqwest::Client::new(),
        &cfg,
        &[
            ("grant_type", "refresh_token"),
            ("refresh_token", "refresh"),
        ],
    )
    .await
    .unwrap();
    assert_eq!(token.access_token, "new-access");
    assert!(!token.expiry.is_empty());
    assert!(!token.needs_refresh());
    task.abort();
}
