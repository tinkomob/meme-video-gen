use crate::{
    audio,
    model::{Draft, eligible, valid_video_id},
    service::Service,
    snippet,
};
use anyhow::{Context, Result, bail, ensure};
use serde_json::{Value, json};
use std::{
    collections::{HashMap, HashSet},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use tokio_util::sync::CancellationToken;
const HELP: &str = "Команды:\n/start — приветствие\n/help — помощь\n/snippet [стиль] [0|1|2] — 20-секундный сниппет\nСтили: silver, violet, amber, aurora, prism, galaxy, tunnel, orbit, wave, rain, chrome, collage, silk, analog, cover\nПод видео: площадки, новый визуал, смена песни, удаление черновика.\nПлановые публикации: два случайных времени в день (Томск), интервал 10–14 часов.\n/song [query] — найти и скачать песню\n/chatid — показать ID чата";
pub fn command_args(args: &str) -> Result<(String, i32)> {
    let parts: Vec<_> = args.split_whitespace().collect();
    ensure!(parts.len() <= 2, "использование: /snippet [стиль] [0|1|2]");
    let style = parts.first().unwrap_or(&"random").to_lowercase();
    let variant = if parts.len() == 2 {
        let v: i32 = parts[1].parse().context("вариант: 0, 1 или 2")?;
        ensure!((0..=2).contains(&v), "вариант: 0, 1 или 2");
        v
    } else {
        -1
    };
    snippet::validate(&style, variant)?;
    Ok((style, variant))
}
pub fn parse_callback(data: &str) -> Result<(&str, &str, Option<&str>)> {
    let p: Vec<_> = data.split(':').collect();
    ensure!(
        data.len() <= 64 && (3..=4).contains(&p.len()) && p[0] == "snip",
        "invalid snippet callback"
    );
    crate::model::draft_key(p[2], ".json")?;
    match p[1] {
        "telegram" | "youtube" | "send" | "all" | "visual" | "random" | "delete" | "back"
        | "status" => ensure!(p.len() == 3, "unexpected callback argument"),
        "list" | "track" => {
            ensure!(p.len() == 4, "missing callback argument");
            if p[1] == "list" {
                p[3].parse::<usize>()?;
            } else {
                ensure!(valid_video_id(p[3]), "invalid song ID");
            }
        }
        _ => bail!("unsupported snippet action"),
    };
    Ok((p[1], p[2], p.get(3).copied()))
}
fn button(text: &str, data: &str) -> Value {
    json!({"text":text,"callback_data":data})
}
fn keyboard(rows: Vec<Vec<Value>>) -> Value {
    json!({"inline_keyboard":rows})
}
pub fn controls(d: &Draft) -> Value {
    let b = |label: &str, action: &str| button(label, &format!("snip:{action}:{}", d.id));
    let mut targets = vec![];
    for (p, name) in [("telegram", "Instagram"), ("youtube", "YouTube")] {
        let mut label = name.to_owned();
        let (mut icon, mut action) = (
            if d.selected.iter().any(|s| s == p) {
                "☑️"
            } else {
                "⬜"
            },
            p,
        );
        match d.targets.get(p).map(String::as_str) {
            Some("sent") => {
                icon = "✅";
                action = "status";
            }
            Some("sending") => {
                icon = "⏳";
                action = "status";
            }
            Some("failed") => label.push_str(" · ошибка"),
            _ => {}
        }
        targets.push(b(&format!("{icon} {label}"), action));
    }
    keyboard(vec![
        targets,
        vec![b("📤 Отправить выбранные", "send"), b("📤 Во все", "all")],
        vec![
            b("🎨 Новый визуал", "visual"),
            b("🎲 Другая песня", "random"),
        ],
        vec![
            b("📋 Выбрать песню", "random"),
            b("🗑 Удалить черновик", "delete"),
        ],
    ])
}
pub fn is_owner(d: &Draft, chat: i64, user: i64) -> bool {
    d.chat_id == chat && d.user_id == user
}
#[derive(Clone)]
struct Bot {
    svc: Service,
    search: Arc<Mutex<HashMap<i64, Instant>>>,
    actions: Arc<Mutex<HashSet<String>>>,
}
struct Action {
    set: Arc<Mutex<HashSet<String>>>,
    id: String,
}
impl Drop for Action {
    fn drop(&mut self) {
        self.set.lock().unwrap().remove(&self.id);
    }
}
impl Bot {
    async fn generate(
        &self,
        chat: i64,
        user: i64,
        style: &str,
        variant: i32,
        song: &str,
    ) -> Result<()> {
        let Ok(_permit) = self.svc.generation.clone().try_acquire_owned() else {
            self.svc
                .telegram
                .text(
                    chat,
                    "Сниппет уже генерируется. Дождитесь завершения.",
                    None,
                )
                .await?;
            return Ok(());
        };
        self.svc
            .telegram
            .text(chat, "Генерирую 20-секундный сниппет…", None)
            .await?;
        let generated = self.svc.generate(style, variant, song).await?;
        let draft = self
            .svc
            .store_draft(&generated, chat, user, style, variant)
            .await?;
        self.svc
            .telegram
            .video(
                chat,
                &generated.path(),
                &format!("{} — {}", draft.song.author, draft.song.title),
                Some(controls(&draft)),
            )
            .await?;
        Ok(())
    }
    async fn song_download(&self, chat: i64, id: &str) -> Result<()> {
        let song = audio::songs(&self.svc.store)
            .await?
            .into_iter()
            .find(|s| s.id == id)
            .context("песня не найдена")?;
        let status = self
            .svc
            .telegram
            .text(chat, "⏳ Скачиваю трек...", None)
            .await?;
        let dir = tempfile::tempdir()?;
        let path = audio::download(&self.svc.store, &song, dir.path()).await?;
        self.svc.telegram.media("sendAudio","audio",&path,json!({"chat_id":chat,"title":song.title,"performer":song.author,"caption":format!("🎵 {}\n👤 {}",song.title,song.author)})).await?;
        self.svc
            .telegram
            .call("deleteMessage", json!({"chat_id":chat,"message_id":status}))
            .await?;
        Ok(())
    }
    async fn song_list(&self, chat: i64, offset: usize, query: Option<&str>) -> Result<()> {
        let mut songs = audio::songs(&self.svc.store).await?;
        if let Some(q) = query {
            let q = q.trim().to_lowercase();
            ensure!(!q.is_empty(), "search query is empty");
            songs.retain(|s| {
                s.title.to_lowercase().contains(&q) || s.author.to_lowercase().contains(&q)
            });
        }
        ensure!(!songs.is_empty(), "нет доступных песен");
        let offset = offset.min(songs.len() - 1);
        let count = if query.is_some() { 10 } else { 8 };
        let end = (offset + count).min(songs.len());
        let mut rows: Vec<_> = songs[offset..end]
            .iter()
            .map(|s| {
                vec![button(
                    &format!(
                        "{} — {}",
                        s.author,
                        s.title.chars().take(30).collect::<String>()
                    ),
                    &format!("songdl:{}", s.id),
                )]
            })
            .collect();
        if query.is_none() {
            let mut nav = vec![];
            if offset > 0 {
                nav.push(button(
                    "⬅️ Предыдущие",
                    &format!("songlist:{}", offset.saturating_sub(8)),
                ));
            }
            if end < songs.len() {
                nav.push(button("➡️ Следующие", &format!("songlist:{end}")));
            }
            if !nav.is_empty() {
                rows.push(nav);
            }
        }
        self.svc
            .telegram
            .text(
                chat,
                &format!(
                    "🎵 Выберите трек ({}–{} из {}):",
                    offset + 1,
                    end,
                    songs.len()
                ),
                Some(keyboard(rows)),
            )
            .await?;
        Ok(())
    }
    async fn update_controls(&self, chat: i64, message: i64, draft: &Draft) -> Result<()> {
        self.svc
            .telegram
            .call(
                "editMessageReplyMarkup",
                json!({"chat_id":chat,"message_id":message,"reply_markup":controls(draft)}),
            )
            .await?;
        Ok(())
    }
    async fn answer(&self, id: &str, text: &str) -> Result<()> {
        self.svc
            .telegram
            .call(
                "answerCallbackQuery",
                json!({"callback_query_id":id,"text":text}),
            )
            .await?;
        Ok(())
    }
    async fn snippet_callback(&self, cb: &Value) -> Result<()> {
        let data = cb["data"].as_str().context("missing callback data")?;
        let cb_id = cb["id"].as_str().context("missing callback ID")?;
        let (action, id, value) = match parse_callback(data) {
            Ok(v) => v,
            Err(_) => return self.answer(cb_id, "Некорректная кнопка.").await,
        };
        let claimed = self.actions.lock().unwrap().insert(id.into());
        if !claimed {
            return self.answer(cb_id, "Действие уже выполняется.").await;
        }
        let _guard = Action {
            set: self.actions.clone(),
            id: id.into(),
        };
        let mut d =
            match tokio::time::timeout(Duration::from_secs(5), self.svc.load_draft(id)).await {
                Ok(Ok(d)) => d,
                _ => {
                    return self
                        .answer(cb_id, "Сниппет удалён или временно недоступен.")
                        .await;
                }
            };
        let chat = cb["message"]["chat"]["id"]
            .as_i64()
            .context("missing chat")?;
        let user = cb["from"]["id"].as_i64().context("missing user")?;
        let message = cb["message"]["message_id"]
            .as_i64()
            .context("missing message")?;
        if !is_owner(&d, chat, user) {
            return self
                .answer(cb_id, "Кнопки доступны автору сниппета в исходном чате.")
                .await;
        }
        self.answer(cb_id, "").await?;
        match action {
            "telegram" | "youtube" => {
                if d.targets
                    .get(action)
                    .is_some_and(|s| s == "sent" || s == "sending")
                {
                    return Ok(());
                }
                if d.selected.iter().any(|s| s == action) {
                    d.selected.retain(|s| s != action);
                } else {
                    d.selected.push(action.into());
                }
                self.svc.save_draft(&d).await?;
                self.update_controls(chat, message, &d).await?;
            }
            "send" | "all" => {
                let platforms = if action == "all" {
                    vec!["telegram".into(), "youtube".into()]
                } else {
                    d.selected.clone()
                };
                if platforms.is_empty() {
                    self.svc
                        .telegram
                        .text(chat, "Сначала выберите Instagram или YouTube.", None)
                        .await?;
                    return Ok(());
                }
                let status = self.svc.telegram.text(chat, "Отправляю…", None).await?;
                let results = self.svc.publish_draft(&mut d, &platforms).await;
                let text = match results {
                    Ok(results) => {
                        if results.is_empty() {
                            "Выбранные площадки уже обработаны. Повторной отправки не будет.".into()
                        } else {
                            results
                                .into_iter()
                                .map(|(p, r)| match r {
                                    Ok(()) => format!("✅ {}", platform_name(&p)),
                                    Err(e) => format!("❌ {}: {e}", platform_name(&p)),
                                })
                                .collect::<Vec<_>>()
                                .join("\n")
                        }
                    }
                    Err(e) => format!("❌ {e}"),
                };
                self.svc.telegram.call("editMessageText",json!({"chat_id":chat,"message_id":status,"text":self.svc.telegram.redact(&text)})).await?;
                self.update_controls(chat, message, &d).await?;
            }
            "visual" | "random" | "list" => {
                let pool: Vec<_> = audio::songs(&self.svc.store)
                    .await?
                    .into_iter()
                    .filter(|s| s.id != d.song.id && eligible(s) && valid_video_id(&s.id))
                    .collect();
                ensure!(!pool.is_empty(), "в библиотеке нет другой подходящей песни");
                use rand::Rng;
                let index = rand::rng().random_range(0..pool.len());
                let (style, variant) = if action == "visual" {
                    ("random", -1)
                } else {
                    (d.style.as_str(), d.variant)
                };
                self.generate(chat, user, style, variant, &pool[index].id)
                    .await?;
            }
            "track" => {
                self.update_controls(chat, message, &d).await?;
                self.generate(chat, user, &d.style, d.variant, value.unwrap())
                    .await?;
            }
            "back" => self.update_controls(chat, message, &d).await?,
            "delete" => {
                self.svc.delete_draft(id).await?;
                if self
                    .svc
                    .telegram
                    .call(
                        "deleteMessage",
                        json!({"chat_id":chat,"message_id":message}),
                    )
                    .await
                    .is_err()
                {
                    self.svc.telegram.call("editMessageReplyMarkup",json!({"chat_id":chat,"message_id":message,"reply_markup":keyboard(vec![])})).await?;
                    self.svc
                        .telegram
                        .text(
                            chat,
                            "Черновик удалён. Старое сообщение можно удалить вручную.",
                            None,
                        )
                        .await?;
                }
            }
            "status" => {
                self.svc.telegram.text(chat,"✅ — уже отправлено. ⏳ — отправка была начата; после сбоя проверьте результат на площадке. Автоповтора не будет.",None).await?;
            }
            _ => unreachable!(),
        };
        Ok(())
    }
    async fn handle(&self, update: Value) -> Result<()> {
        if let Some(cb) = update.get("callback_query") {
            let Some(chat) = cb["message"]["chat"]["id"].as_i64() else {
                return Ok(());
            };
            let Some(data) = cb["data"].as_str() else {
                return Ok(());
            };
            if data.starts_with("snip:") {
                return self.snippet_callback(cb).await;
            }
            self.answer(cb["id"].as_str().context("missing callback id")?, "")
                .await?;
            match data {
                "songsearch" => {
                    {
                        let mut search = self.search.lock().unwrap();
                        search.retain(|_, t| t.elapsed() < Duration::from_secs(300));
                        search.insert(chat, Instant::now());
                    }
                    self.svc
                        .telegram
                        .text(chat, "🔍 Введите название трека или артиста:", None)
                        .await?;
                }
                "songrand" => {
                    let songs = audio::songs(&self.svc.store).await?;
                    use rand::Rng;
                    let i = rand::rng().random_range(0..songs.len());
                    self.song_download(chat, &songs[i].id).await?;
                }
                _ => {
                    if let Some((action, value)) = data.split_once(':') {
                        match action {
                            "songlist" => {
                                self.song_list(chat, value.parse().unwrap_or(0), None)
                                    .await?
                            }
                            "songdl" => self.song_download(chat, value).await?,
                            _ => {}
                        }
                    }
                }
            }
            return Ok(());
        }
        let Some(msg) = update.get("message") else {
            return Ok(());
        };
        let Some(text) = msg["text"].as_str() else {
            return Ok(());
        };
        let chat = msg["chat"]["id"].as_i64().context("missing chat ID")?;
        let user = msg["from"]["id"].as_i64().unwrap_or(0);
        if text.starts_with('/') {
            let (command, args) = text.split_once(char::is_whitespace).unwrap_or((text, ""));
            let command = command.split('@').next().unwrap();
            match command {
                "/start" => {
                    self.svc
                        .telegram
                        .text(
                            chat,
                            "Привет! Я бот для музыкальных сниппетов. /help — список команд.",
                            None,
                        )
                        .await?;
                }
                "/help" => {
                    self.svc.telegram.text(chat, HELP, None).await?;
                }
                "/chatid" => {
                    self.svc
                        .telegram
                        .text(chat, &format!("Ваш Chat ID: {chat}"), None)
                        .await?;
                }
                "/snippet" => {
                    if user != 0 {
                        let (style, variant) = command_args(args)?;
                        self.generate(chat, user, &style, variant, "").await?;
                    }
                }
                "/song" => {
                    if !args.trim().is_empty() {
                        self.song_list(chat, 0, Some(args)).await?;
                    } else {
                        audio::songs(&self.svc.store).await?;
                        self.svc
                            .telegram
                            .text(
                                chat,
                                "🎵 Скачать трек",
                                Some(keyboard(vec![
                                    vec![button("🎲 Случайный трек", "songrand")],
                                    vec![button("📋 Выбрать трек из списка", "songlist:0")],
                                    vec![button("🔍 Поиск трека", "songsearch")],
                                ])),
                            )
                            .await?;
                    }
                }
                _ => {
                    self.svc
                        .telegram
                        .text(chat, "Неизвестная команда. Используйте /help", None)
                        .await?;
                }
            }
        } else {
            let search = self
                .search
                .lock()
                .unwrap()
                .remove(&chat)
                .is_some_and(|t| t.elapsed() < Duration::from_secs(300));
            if search {
                self.song_list(chat, 0, Some(text)).await?;
            }
        }
        Ok(())
    }
}
fn platform_name(p: &str) -> &str {
    if p == "telegram" {
        "Instagram"
    } else {
        "YouTube"
    }
}
pub async fn run(svc: Service, cancel: CancellationToken) -> Result<()> {
    let bot = Bot {
        svc,
        search: Arc::new(Mutex::new(HashMap::new())),
        actions: Arc::new(Mutex::new(HashSet::new())),
    };
    let mut initialized = false;
    for attempt in 1..=5 {
        match bot.svc.telegram.call("getMe", json!({})).await {
            Ok(_) => {
                initialized = true;
                break;
            }
            Err(e) => tracing::warn!(
                "Telegram init {attempt}/5: {}",
                bot.svc.telegram.redact(&e.to_string())
            ),
        }
        tokio::select! {_=cancel.cancelled()=>return Ok(()),_=tokio::time::sleep(Duration::from_secs(10))=>{}}
    }
    if !initialized {
        tracing::error!("continuing without Telegram after five attempts");
        return Ok(());
    }
    let commands = json!({"commands":[{"command":"start","description":"Запустить бота"},{"command":"help","description":"Список команд"},{"command":"snippet","description":"Создать музыкальный сниппет"},{"command":"song","description":"Найти и скачать песню"},{"command":"chatid","description":"Показать ID чата"}]});
    if let Err(e) = bot.svc.telegram.call("setMyCommands", commands).await {
        tracing::warn!("command menu: {e}");
    }
    if bot.svc.cfg.owner_chat_id != 0
        && let Err(e) = bot
            .svc
            .telegram
            .text(
                bot.svc.cfg.owner_chat_id,
                "✅ Бот запущен и готов к работе.",
                None,
            )
            .await
    {
        tracing::warn!("startup message: {e}");
    }
    let mut offset = 0;
    let mut workers = tokio::task::JoinSet::new();
    loop {
        let result = tokio::select! {
            _ = cancel.cancelled() => break,
            Some(result) = workers.join_next(), if !workers.is_empty() => {
                if let Err(e) = result { tracing::error!("bot worker: {e}"); }
                continue;
            }
            result = bot.svc.telegram.call("getUpdates", json!({
                "offset": offset, "timeout": 30,
                "allowed_updates": ["message", "callback_query"]
            })), if workers.len() < 128 => result,
        };
        let updates = match result {
            Ok(updates) => updates,
            Err(e) => {
                tracing::error!(
                    "Telegram polling: {}",
                    bot.svc.telegram.redact(&e.to_string())
                );
                tokio::select! {
                    _ = cancel.cancelled() => break,
                    _ = tokio::time::sleep(Duration::from_secs(3)) => {}
                }
                continue;
            }
        };
        for update in updates.as_array().context("invalid Telegram updates")? {
            offset = offset.max(update["update_id"].as_i64().context("missing update ID")? + 1);
            let bot = bot.clone();
            let update = update.clone();
            workers.spawn(async move {
                let chat = update["message"]["chat"]["id"]
                    .as_i64()
                    .or(update["callback_query"]["message"]["chat"]["id"].as_i64());
                let result =
                    tokio::time::timeout(Duration::from_secs(900), bot.handle(update)).await;
                let error = match result {
                    Ok(Ok(())) => return,
                    Ok(Err(e)) => e.to_string(),
                    Err(e) => e.to_string(),
                };
                let error = bot.svc.telegram.redact(&error);
                tracing::error!("bot action: {error}");
                if let Some(chat) = chat {
                    let _ = bot
                        .svc
                        .telegram
                        .text(
                            chat,
                            &format!("Не удалось выполнить действие: {error}"),
                            None,
                        )
                        .await;
                }
            });
        }
    }
    workers.abort_all();
    while workers.join_next().await.is_some() {}
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn validates_commands() {
        assert_eq!(command_args("").unwrap(), ("random".into(), -1));
        for style in crate::visualizer::STYLES {
            for v in 0..3 {
                assert!(command_args(&format!("{style} {v}")).is_ok())
            }
        }
        for args in ["silver -1", "silver 3", "random x", "bad", "rain 1 extra"] {
            assert!(command_args(args).is_err())
        }
    }
    #[test]
    fn callbacks_and_ownership() {
        let id = "0123456789abcdef01234567";
        for action in ["all", "send", "visual", "delete"] {
            assert!(parse_callback(&format!("snip:{action}:{id}")).is_ok())
        }
        for data in [
            "snip:delete:../../bad".to_string(),
            format!("snip:list:{id}:-1"),
            format!("snip:track:{id}:../bad"),
            format!("snip:send:{id}:extra"),
        ] {
            assert!(parse_callback(&data).is_err())
        }
        let d = Draft {
            id: id.into(),
            chat_id: 1,
            user_id: 2,
            song: Default::default(),
            style: "rain".into(),
            variant: 0,
            selected: vec![],
            targets: Default::default(),
            created_at: String::new(),
        };
        assert!(is_owner(&d, 1, 2));
        assert!(!is_owner(&d, 2, 2));
        assert!(!is_owner(&d, 1, 1));
    }
}
#[cfg(test)]
mod callback_flow_tests {
    use super::*;
    use crate::{config::Config, upload::Telegram};
    use axum::{
        Router,
        body::Bytes,
        extract::State,
        http::{Method, Uri},
        response::IntoResponse,
        routing::any,
    };
    #[derive(Clone)]
    struct StateData {
        draft: Arc<Mutex<Draft>>,
        calls: Arc<Mutex<Vec<(String, Value)>>>,
    }
    async fn mock(
        State(state): State<StateData>,
        method: Method,
        uri: Uri,
        body: Bytes,
    ) -> axum::response::Response {
        if uri.path().starts_with("/bottest/") {
            let value: Value = serde_json::from_slice(&body).unwrap();
            state
                .calls
                .lock()
                .unwrap()
                .push((uri.path().to_owned(), value));
            return axum::Json(json!({"ok":true,"result":{"message_id":42}})).into_response();
        }
        if method == Method::PUT {
            *state.draft.lock().unwrap() = serde_json::from_slice(&body).unwrap();
        }
        axum::Json(state.draft.lock().unwrap().clone()).into_response()
    }
    #[tokio::test]
    async fn callback_handler_enforces_author_chat_and_busy_guard_before_mutation() {
        let draft = Draft {
            id: "0123456789abcdef01234567".into(),
            chat_id: 10,
            user_id: 20,
            song: Default::default(),
            style: "silver".into(),
            variant: 0,
            selected: vec![],
            targets: Default::default(),
            created_at: "2026-10-25T00:00:00Z".into(),
        };
        let state = StateData {
            draft: Arc::new(Mutex::new(draft)),
            calls: Default::default(),
        };
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let router = Router::new().fallback(any(mock)).with_state(state.clone());
        let task = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let cfg = Config {
            telegram_token: "test".into(),
            endpoint: base.clone(),
            region: "r".into(),
            bucket: "b".into(),
            access_key: "a".into(),
            secret_key: "s".into(),
            posts_chat_id: 0,
            owner_chat_id: 0,
            silent: true,
        };
        let mut svc = Service::new(cfg).unwrap();
        svc.telegram = Telegram::with_base("test".into(), &base).unwrap();
        let bot = Bot {
            svc,
            search: Default::default(),
            actions: Default::default(),
        };
        let update = |chat, user| json!({"callback_query":{"id":"callback","data":"snip:telegram:0123456789abcdef01234567","from":{"id":user},"message":{"chat":{"id":chat},"message_id":30}}});
        bot.handle(update(10, 99)).await.unwrap();
        bot.handle(update(99, 20)).await.unwrap();
        assert!(state.draft.lock().unwrap().selected.is_empty());
        assert!(
            state
                .calls
                .lock()
                .unwrap()
                .iter()
                .all(|(p, v)| p.ends_with("answerCallbackQuery")
                    && v["text"].as_str().unwrap().contains("автору"))
        );
        bot.handle(update(10, 20)).await.unwrap();
        assert_eq!(state.draft.lock().unwrap().selected, vec!["telegram"]);
        assert!(
            state
                .calls
                .lock()
                .unwrap()
                .iter()
                .any(|(p, _)| p.ends_with("editMessageReplyMarkup"))
        );
        bot.actions
            .lock()
            .unwrap()
            .insert("0123456789abcdef01234567".into());
        bot.handle(update(10, 20)).await.unwrap();
        assert_eq!(state.draft.lock().unwrap().selected, vec!["telegram"]);
        assert!(
            state.calls.lock().unwrap().last().unwrap().1["text"]
                .as_str()
                .unwrap()
                .contains("уже выполняется")
        );
        let _permit = bot.svc.generation.clone().try_acquire_owned().unwrap();
        bot.handle(json!({"message":{"chat":{"id":10},"from":{"id":20},"text":"/snippet"}}))
            .await
            .unwrap();
        let calls = state.calls.lock().unwrap();
        let (_, v) = calls.last().unwrap();
        assert_eq!(v["disable_notification"], true);
        assert!(v["text"].as_str().unwrap().contains("уже генерируется"));
        task.abort();
    }
}
