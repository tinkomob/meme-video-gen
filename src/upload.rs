use crate::{model::Song, storage::Storage};
use anyhow::{Context, Result, ensure};
use reqwest::{Client, multipart::Form};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{path::Path, time::Duration};
#[derive(Clone)]
pub struct Telegram {
    client: Client,
    token: String,
    base: String,
}
impl Telegram {
    pub fn new(token: String) -> Result<Self> {
        Self::with_base(token, "https://api.telegram.org")
    }
    pub fn with_base(token: String, base: &str) -> Result<Self> {
        Ok(Self {
            client: Client::builder()
                .connect_timeout(Duration::from_secs(10))
                .timeout(Duration::from_secs(300))
                .build()?,
            token,
            base: base.trim_end_matches('/').into(),
        })
    }
    pub fn redact(&self, s: &str) -> String {
        s.replace(&self.token, "[redacted]")
    }
    fn endpoint(&self, method: &str) -> String {
        format!("{}/bot{}/{method}", self.base, self.token)
    }
    async fn response(&self, r: Result<reqwest::Response, reqwest::Error>) -> Result<Value> {
        let r = r.map_err(|e| anyhow::anyhow!("{}", e.without_url()))?;
        let status = r.status();
        let v: Value = r
            .json()
            .await
            .map_err(|e| anyhow::anyhow!("{}", e.without_url()))?;
        ensure!(
            status.is_success() && v["ok"].as_bool() == Some(true),
            "Telegram: {}",
            self.redact(v["description"].as_str().unwrap_or("request failed"))
        );
        Ok(v["result"].clone())
    }
    pub async fn call(&self, method: &str, value: Value) -> Result<Value> {
        self.response(
            self.client
                .post(self.endpoint(method))
                .timeout(Duration::from_secs(60))
                .json(&value)
                .send()
                .await,
        )
        .await
    }
    pub async fn text(&self, chat: i64, text: &str, keyboard: Option<Value>) -> Result<i64> {
        let mut data = json!({"chat_id":chat,"text":self.redact(text),"disable_notification":true});
        if let Some(k) = keyboard {
            data["reply_markup"] = k;
        }
        self.call("sendMessage", data).await?["message_id"]
            .as_i64()
            .context("missing message ID")
    }
    pub async fn media(
        &self,
        method: &str,
        field: &str,
        path: &Path,
        mut data: Value,
    ) -> Result<Value> {
        data["disable_notification"] = json!(true);
        let mut form = Form::new().file(field.to_owned(), path).await?;
        for (k, v) in data.as_object().context("invalid media fields")? {
            form = form.text(
                k.clone(),
                v.as_str()
                    .map(str::to_owned)
                    .unwrap_or_else(|| v.to_string()),
            );
        }
        self.response(
            self.client
                .post(self.endpoint(method))
                .multipart(form)
                .send()
                .await,
        )
        .await
    }
    pub async fn video(
        &self,
        chat: i64,
        path: &Path,
        caption: &str,
        keyboard: Option<Value>,
    ) -> Result<Value> {
        let mut data =
            json!({"chat_id":chat,"caption":caption,"supports_streaming":true,"duration":20});
        if let Some(k) = keyboard {
            data["reply_markup"] = k;
        }
        self.media("sendVideo", "video", path, data).await
    }
}
#[derive(Debug, Clone, Deserialize)]
pub struct OAuthClient {
    pub client_id: String,
    pub client_secret: String,
    #[serde(default)]
    pub redirect_uris: Vec<String>,
    #[serde(default = "default_auth_uri")]
    pub auth_uri: String,
    #[serde(default = "default_token_uri")]
    pub token_uri: String,
}
fn default_auth_uri() -> String {
    "https://accounts.google.com/o/oauth2/auth".into()
}
fn default_token_uri() -> String {
    "https://oauth2.googleapis.com/token".into()
}
impl OAuthClient {
    pub fn parse(value: Value) -> Result<Self> {
        serde_json::from_value(
            value
                .get("installed")
                .or_else(|| value.get("web"))
                .context("missing installed/web OAuth config")?
                .clone(),
        )
        .map_err(Into::into)
    }
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct OAuthToken {
    #[serde(alias = "token")]
    pub access_token: String,
    #[serde(default)]
    pub refresh_token: String,
    #[serde(default = "bearer")]
    pub token_type: String,
    #[serde(default)]
    pub expiry: String,
}
fn bearer() -> String {
    "Bearer".into()
}
impl OAuthToken {
    pub fn needs_refresh(&self) -> bool {
        self.access_token.is_empty()
            || (!self.expiry.is_empty()
                && chrono::DateTime::parse_from_rfc3339(&self.expiry)
                    .map(|d| d <= chrono::Utc::now() + chrono::Duration::seconds(30))
                    .unwrap_or(true))
    }
}
pub async fn exchange(
    client: &Client,
    config: &OAuthClient,
    fields: &[(&str, &str)],
) -> Result<OAuthToken> {
    let mut fields = fields.to_vec();
    fields.extend([
        ("client_id", config.client_id.as_str()),
        ("client_secret", config.client_secret.as_str()),
    ]);
    let v: Value = client
        .post(&config.token_uri)
        .form(&fields)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let mut token: OAuthToken = serde_json::from_value(v.clone())?;
    ensure!(
        !token.access_token.is_empty(),
        "OAuth response has no access token"
    );
    token.expiry = (chrono::Utc::now()
        + chrono::Duration::seconds(v["expires_in"].as_i64().unwrap_or(3600)))
    .to_rfc3339();
    Ok(token)
}
async fn credentials(store: &Storage, name: &str) -> Result<(String, Value)> {
    for key in [
        name.to_owned(),
        format!("tokens/{name}"),
        format!("bot-uploads/{name}"),
    ] {
        if let Some(v) = store.read(&key).await? {
            return Ok((key, v));
        }
    }
    anyhow::bail!("{name} not found in S3")
}
pub async fn youtube_available(store: &Storage) -> Result<()> {
    credentials(store, "client_secrets.json").await?;
    credentials(store, "token.json").await?;
    Ok(())
}
pub async fn youtube(store: &Storage, path: &Path, song: &Song) -> Result<()> {
    let client = Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(300))
        .build()?;
    let (_, cfg) = credentials(store, "client_secrets.json").await?;
    let cfg = OAuthClient::parse(cfg)?;
    let (key, value) = credentials(store, "token.json").await?;
    let mut token: OAuthToken = serde_json::from_value(value)?;
    if token.needs_refresh() {
        ensure!(
            !token.refresh_token.is_empty(),
            "YouTube token expired; authenticate again"
        );
        let mut new = exchange(
            &client,
            &cfg,
            &[
                ("grant_type", "refresh_token"),
                ("refresh_token", &token.refresh_token),
            ],
        )
        .await?;
        if new.refresh_token.is_empty() {
            new.refresh_token = token.refresh_token;
        }
        token = new;
        store.write(&key, &token).await?;
    }
    let title = format!("{} — {}", song.author, song.title);
    let metadata = json!({"snippet":{"title":title,"description":title,"tags":["shorts","music"],"categoryId":"24"},"status":{"privacyStatus":"public","selfDeclaredMadeForKids":false}});
    let size = tokio::fs::metadata(path).await?.len();
    let response = client
        .post("https://www.googleapis.com/upload/youtube/v3/videos")
        .query(&[("uploadType", "resumable"), ("part", "snippet,status")])
        .bearer_auth(&token.access_token)
        .header("X-Upload-Content-Type", "video/mp4")
        .header("X-Upload-Content-Length", size)
        .json(&metadata)
        .send()
        .await?
        .error_for_status()?;
    let location = response
        .headers()
        .get("location")
        .context("missing YouTube upload URL")?
        .to_str()?;
    let file = tokio::fs::File::open(path).await?;
    let body = reqwest::Body::wrap_stream(tokio_util::io::ReaderStream::new(file));
    client
        .put(location)
        .bearer_auth(&token.access_token)
        .header("Content-Type", "video/mp4")
        .header("Content-Length", size)
        .body(body)
        .send()
        .await?
        .error_for_status()?;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn legacy_token_and_expiry() {
        let t: OAuthToken = serde_json::from_value(
            json!({"token":"old","refresh_token":"refresh","expiry":"2020-01-01T00:00:00Z"}),
        )
        .unwrap();
        assert!(t.needs_refresh());
        assert_eq!(t.access_token, "old");
        let t: OAuthToken = serde_json::from_value(json!({"access_token":"current"})).unwrap();
        assert!(!t.needs_refresh());
    }
}
