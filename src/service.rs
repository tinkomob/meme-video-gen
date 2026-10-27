use crate::{
    audio,
    config::Config,
    model::{Draft, Song, draft_key},
    schedule::{Plan, tomsk},
    snippet::{self, Generated},
    storage::Storage,
    upload::{self, Telegram},
};
use anyhow::{Context, Result, ensure};
use chrono::{Timelike, Utc};
use std::{collections::BTreeMap, sync::Arc, time::Duration};
use tokio::sync::Mutex;
use tokio_util::sync::CancellationToken;
#[derive(Clone)]
pub struct Service {
    pub store: Storage,
    pub telegram: Telegram,
    pub cfg: Config,
    pub posts: Arc<Mutex<i64>>,
    pub generation: Arc<tokio::sync::Semaphore>,
    schedule: Arc<Mutex<Option<Plan>>>,
}
impl Service {
    pub fn new(cfg: Config) -> Result<Self> {
        Ok(Self {
            store: Storage::new(cfg.clone())?,
            telegram: Telegram::new(cfg.telegram_token.clone())?,
            posts: Arc::new(Mutex::new(cfg.posts_chat_id)),
            cfg,
            generation: Arc::new(tokio::sync::Semaphore::new(1)),
            schedule: Default::default(),
        })
    }
    pub async fn posts_chat(&self) -> Result<i64> {
        let mut posts = self.posts.lock().await;
        if *posts == 0
            && let Some(v) = self.store.read::<serde_json::Value>("config.json").await?
        {
            *posts = v["posts_chat_id"].as_i64().unwrap_or(0);
        }
        Ok(*posts)
    }
    pub async fn generate(&self, style: &str, variant: i32, id: &str) -> Result<Generated> {
        tokio::time::timeout(
            Duration::from_secs(600),
            snippet::generate(&self.store, style, variant, id),
        )
        .await?
    }
    pub async fn save_draft(&self, draft: &Draft) -> Result<()> {
        self.store
            .write(&draft_key(&draft.id, ".json")?, draft)
            .await
    }
    pub async fn load_draft(&self, id: &str) -> Result<Draft> {
        let d: Draft = self
            .store
            .read(&draft_key(id, ".json")?)
            .await?
            .context("snippet draft not found")?;
        ensure!(d.id == id, "snippet draft ID mismatch");
        Ok(d)
    }
    pub async fn store_draft(
        &self,
        generated: &Generated,
        chat_id: i64,
        user_id: i64,
    ) -> Result<Draft> {
        let id = hex::encode(rand::random::<[u8; 12]>());
        let d = Draft {
            id,
            chat_id,
            user_id,
            song: generated.song.clone(),
            style: generated.style.clone(),
            variant: generated.variant,
            selected: vec![],
            targets: BTreeMap::new(),
            created_at: Utc::now().to_rfc3339(),
        };
        let key = draft_key(&d.id, ".mp4")?;
        self.store
            .put(&key, tokio::fs::read(generated.path()).await?, "video/mp4")
            .await?;
        if let Err(e) = self.save_draft(&d).await {
            if let Err(cleanup) = self.store.delete(&key).await {
                tracing::error!("draft cleanup: {cleanup}");
            }
            return Err(e);
        }
        Ok(d)
    }
    pub async fn delete_draft(&self, id: &str) -> Result<()> {
        self.store.delete(&draft_key(id, ".mp4")?).await?;
        self.store.delete(&draft_key(id, ".json")?).await
    }
    async fn upload(&self, platform: &str, path: &std::path::Path, song: &Song) -> Result<()> {
        match platform {
            "telegram" => {
                let chat = self.posts_chat().await?;
                ensure!(chat != 0, "POSTS_CHAT_ID is not configured");
                self.telegram
                    .video(
                        chat,
                        path,
                        &format!("{} — {}", song.author, song.title),
                        None,
                    )
                    .await?;
                Ok(())
            }
            "youtube" => upload::youtube(&self.store, path, song).await,
            _ => anyhow::bail!("unsupported snippet platform"),
        }
    }
    pub async fn publish_draft(
        &self,
        d: &mut Draft,
        platforms: &[String],
    ) -> Result<Vec<(String, Result<()>)>> {
        ensure!(!platforms.is_empty(), "select at least one platform");
        ensure!(
            platforms.iter().all(|p| p == "telegram" || p == "youtube"),
            "unsupported snippet platform"
        );
        let dir = tempfile::tempdir()?;
        let path = dir.path().join("publish.mp4");
        self.store
            .download(&draft_key(&d.id, ".mp4")?, &path)
            .await?;
        let mut results = vec![];
        for p in platforms {
            if d.targets
                .get(p)
                .is_some_and(|v| v == "sent" || v == "sending")
            {
                continue;
            }
            let previous = d.targets.insert(p.clone(), "sending".into());
            if let Err(e) = self.save_draft(d).await {
                match previous {
                    Some(v) => {
                        d.targets.insert(p.clone(), v);
                    }
                    None => {
                        d.targets.remove(p);
                    }
                }
                return Err(e);
            }
            let result = self.upload(p, &path, &d.song).await;
            d.targets.insert(
                p.clone(),
                if result.is_ok() { "sent" } else { "failed" }.into(),
            );
            results.push((p.clone(), result));
            if let Err(e) = self.save_draft(d).await {
                d.targets.insert(p.clone(), "sending".into());
                return Err(e.context(format!(
                    "could not save publication result; check {p} before retrying"
                )));
            }
        }
        Ok(results)
    }
    pub async fn scheduled_publish(&self) -> Result<()> {
        let mut platforms = vec![];
        if self.posts_chat().await? != 0 {
            platforms.push("telegram");
        }
        match upload::youtube_available(&self.store).await {
            Ok(()) => platforms.push("youtube"),
            Err(e) => tracing::warn!("YouTube uploader unavailable: {e}"),
        };
        ensure!(!platforms.is_empty(), "no snippet uploaders configured");
        let generated = self.generate("random", -1, "").await?;
        let mut published = vec![];
        for p in platforms {
            match self.upload(p, &generated.path(), &generated.song).await {
                Ok(()) => {
                    published.push(p);
                    tracing::info!("snippet published to {p}");
                }
                Err(e) => tracing::error!(
                    "snippet upload to {p}: {}",
                    self.telegram.redact(&e.to_string())
                ),
            }
        }
        ensure!(!published.is_empty(), "snippet not published");
        self.notify_scheduled_publication(&generated.song, &published)
            .await;
        Ok(())
    }
    async fn notify_scheduled_publication(&self, song: &Song, platforms: &[&str]) {
        if self.cfg.owner_chat_id == 0 {
            return;
        }
        let text = format!(
            "Сниппет опубликован по расписанию: {} — {}\nПлощадки: {}",
            song.author,
            song.title,
            platforms.join(", ")
        );
        if let Err(e) = self
            .telegram
            .text(self.cfg.owner_chat_id, &text, None)
            .await
        {
            tracing::error!(
                "scheduled snippet notification: {}",
                self.telegram.redact(&e.to_string())
            );
        }
    }
    async fn load_plan(
        &self,
        plan: &mut Option<Plan>,
        now: chrono::DateTime<chrono::FixedOffset>,
    ) -> Result<()> {
        let today = now.format("%Y-%m-%d").to_string();
        if plan
            .as_ref()
            .is_none_or(|p| p.date != today || p.slots.len() != 2)
        {
            let saved = self.store.read::<Plan>("snippet_schedule.json").await?;
            if let Some(ref p) = saved {
                p.validate()?;
                ensure!(p.date <= today, "snippet schedule dated in the future");
            }
            let p = match saved {
                Some(p) if p.date == today && p.slots.len() == 2 => p,
                Some(p) if p.date == today => {
                    let next = Plan::migrate(now, &p, &mut rand::rng())?;
                    self.store.write("snippet_schedule.json", &next).await?;
                    next
                }
                previous => {
                    let next = Plan::new(now, previous.as_ref(), &mut rand::rng())?;
                    self.store.write("snippet_schedule.json", &next).await?;
                    next
                }
            };
            *plan = Some(p);
        }
        Ok(())
    }
    async fn claim_slot(
        &self,
        plan: &mut Option<Plan>,
        now: chrono::DateTime<chrono::FixedOffset>,
    ) -> Result<bool> {
        self.load_plan(plan, now).await?;
        let mut next = plan.as_ref().unwrap().clone();
        let (changed, due) = next.claim(now);
        if changed {
            self.store.write("snippet_schedule.json", &next).await?;
            *plan = Some(next);
        }
        Ok(due)
    }
    pub async fn snippet_schedule(&self, args: &str) -> Result<String> {
        let now = Utc::now().with_timezone(&tomsk());
        let mut plan = self.schedule.lock().await;
        self.load_plan(&mut plan, now).await?;
        let mut next = plan.as_ref().unwrap().clone();
        if !args.trim().is_empty() {
            next.reschedule(args, now)?;
            self.store.write("snippet_schedule.json", &next).await?;
            *plan = Some(next.clone());
        }
        let mut lines = vec![format!("Расписание сниппетов на {} (Томск):", next.date)];
        for (i, slot) in next.slots.iter().enumerate() {
            lines.push(format!(
                "#{} — {} {}",
                i + 1,
                slot.format("%H:%M"),
                if next.handled[i] {
                    "обработан"
                } else if *slot < now {
                    "пропущен"
                } else {
                    "ожидает"
                }
            ));
        }
        lines.push("/setsnippet <номер> <HH:MM | +30m | -1h | YYYY-MM-DD HH:MM>\nДва слота в день, интервал 10–14 часов. Изменения действуют на сегодня.".into());
        Ok(lines.join("\n"))
    }
    async fn schedule_tick(&self) -> Result<()> {
        let due = {
            let mut plan = self.schedule.lock().await;
            self.claim_slot(&mut plan, Utc::now().with_timezone(&tomsk()))
                .await?
        };
        if due {
            tokio::time::timeout(Duration::from_secs(900), self.scheduled_publish()).await??;
        }
        Ok(())
    }
    pub async fn run_schedule(&self, cancel: CancellationToken) {
        loop {
            let delay = 60 - Utc::now().second() as u64;
            tokio::select! {
                _ = cancel.cancelled() => break,
                _ = tokio::time::sleep(Duration::from_secs(delay)) => {}
            }
            let result = tokio::select! {
                _ = cancel.cancelled() => break,
                result = self.schedule_tick() => result
            };
            if let Err(e) = result {
                tracing::error!("schedule: {}", self.telegram.redact(&e.to_string()));
            }
        }
    }
    pub async fn run_indexer(&self, cancel: CancellationToken) {
        loop {
            let now = Utc::now();
            let delay = 3600 - (now.minute() * 60 + now.second()) as u64;
            tokio::select! {
                _ = cancel.cancelled() => break,
                _ = tokio::time::sleep(Duration::from_secs(delay)) => {}
            }
            tokio::select! {
                _ = cancel.cancelled() => break,
                result = audio::ensure_songs(&self.store) => {
                    if let Err(e) = result {
                        tracing::error!("song indexing: {e}");
                    }
                }
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Router,
        body::Bytes,
        extract::State,
        http::{Method, StatusCode},
        response::IntoResponse,
        routing::any,
    };
    use chrono::{DateTime, Duration as ChronoDuration};
    use std::sync::Mutex as StdMutex;
    #[derive(Default)]
    struct Memory {
        saved: Option<Vec<u8>>,
        messages: Vec<serde_json::Value>,
        fail_reads: bool,
        fail_writes: bool,
    }
    async fn handler(
        State(state): State<Arc<StdMutex<Memory>>>,
        method: Method,
        body: Bytes,
    ) -> axum::response::Response {
        let mut memory = state.lock().unwrap();
        if method == Method::POST {
            memory.messages.push(serde_json::from_slice(&body).unwrap());
            return axum::Json(serde_json::json!({
                "ok": true, "result": {"message_id": 1}
            }))
            .into_response();
        }
        if method == Method::GET {
            if memory.fail_reads {
                return StatusCode::SERVICE_UNAVAILABLE.into_response();
            }
            return memory
                .saved
                .clone()
                .map(IntoResponse::into_response)
                .unwrap_or_else(|| StatusCode::NOT_FOUND.into_response());
        }
        if memory.fail_writes {
            return StatusCode::SERVICE_UNAVAILABLE.into_response();
        }
        memory.saved = Some(body.to_vec());
        StatusCode::OK.into_response()
    }
    #[tokio::test]
    async fn schedule_claims_are_durable_and_storage_errors_do_not_consume_slots() {
        let memory = Arc::new(StdMutex::new(Memory::default()));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}", listener.local_addr().unwrap());
        let app = Router::new()
            .fallback(any(handler))
            .with_state(memory.clone());
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let cfg = Config {
            telegram_token: "unused".into(),
            endpoint,
            region: "test".into(),
            bucket: "test".into(),
            access_key: "a".into(),
            secret_key: "s".into(),
            posts_chat_id: 0,
            owner_chat_id: 0,
            silent: true,
        };
        let svc = Service::new(cfg.clone()).unwrap();
        let now = DateTime::parse_from_rfc3339("2026-10-25T00:00:00+07:00").unwrap();
        let mut plan = None;
        memory.lock().unwrap().fail_reads = true;
        assert!(svc.claim_slot(&mut plan, now).await.is_err());
        assert!(plan.is_none());
        memory.lock().unwrap().fail_reads = false;
        memory.lock().unwrap().fail_writes = true;
        assert!(svc.claim_slot(&mut plan, now).await.is_err());
        assert!(plan.is_none());
        memory.lock().unwrap().fail_writes = false;
        let fixed = Plan {
            date: "2026-10-25".into(),
            slots: vec![
                now + ChronoDuration::hours(6),
                now + ChronoDuration::hours(18),
            ],
            handled: vec![false, false],
        };
        memory.lock().unwrap().saved = Some(serde_json::to_vec(&fixed).unwrap());
        assert!(!svc.claim_slot(&mut plan, now).await.unwrap());
        let due = plan.as_ref().unwrap().slots[0] + ChronoDuration::seconds(20);
        memory.lock().unwrap().fail_writes = true;
        assert!(svc.claim_slot(&mut plan, due).await.is_err());
        assert!(!plan.as_ref().unwrap().handled[0]);
        memory.lock().unwrap().fail_writes = false;
        assert!(svc.claim_slot(&mut plan, due).await.unwrap());
        let restarted = Service::new(cfg.clone()).unwrap();
        let mut reloaded = None;
        assert!(!restarted.claim_slot(&mut reloaded, due).await.unwrap());
        assert!(reloaded.as_ref().unwrap().handled[0]);
        let missed = reloaded.as_ref().unwrap().slots[1] + ChronoDuration::minutes(2);
        assert!(!restarted.claim_slot(&mut reloaded, missed).await.unwrap());
        assert!(reloaded.as_ref().unwrap().handled.iter().all(|v| *v));
        let saved = memory.lock().unwrap().saved.clone().unwrap();
        let saved: Plan = serde_json::from_slice(&saved).unwrap();
        saved.validate().unwrap();
        let mut future = saved.clone();
        future.date = "2026-10-26".into();
        for s in &mut future.slots {
            *s += ChronoDuration::days(1);
        }
        memory.lock().unwrap().saved = Some(serde_json::to_vec(&future).unwrap());
        assert!(restarted.claim_slot(&mut None, now).await.is_err());
        let mut notifier = Service::new(cfg).unwrap();
        notifier.telegram = Telegram::with_base("unused".into(), &notifier.cfg.endpoint).unwrap();
        let song = Song {
            author: "Исполнитель".into(),
            title: "Трек".into(),
            ..Default::default()
        };
        notifier
            .notify_scheduled_publication(&song, &["telegram"])
            .await;
        assert!(memory.lock().unwrap().messages.is_empty());
        notifier.cfg.owner_chat_id = 123;
        notifier
            .notify_scheduled_publication(&song, &["telegram", "youtube"])
            .await;
        let messages = memory.lock().unwrap().messages.clone();
        assert_eq!(messages.len(), 1);
        assert_eq!(messages[0]["chat_id"], 123);
        assert_eq!(messages[0]["disable_notification"], true);
        assert_eq!(
            messages[0]["text"],
            "Сниппет опубликован по расписанию: Исполнитель — Трек\nПлощадки: telegram, youtube"
        );
        server.abort();
    }
}
