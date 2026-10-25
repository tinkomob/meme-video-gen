use anyhow::{Result, ensure};
use std::collections::HashMap;
#[derive(Clone)]
pub struct Config {
    pub telegram_token: String,
    pub endpoint: String,
    pub region: String,
    pub bucket: String,
    pub access_key: String,
    pub secret_key: String,
    pub posts_chat_id: i64,
    pub owner_chat_id: i64,
    pub silent: bool,
}
impl Config {
    pub fn load() -> Result<Self> {
        Self::from_map(std::env::vars().collect())
    }
    pub fn from_map(env: HashMap<String, String>) -> Result<Self> {
        let get = |keys: &[&str]| {
            keys.iter()
                .find_map(|k| env.get(*k).filter(|v| !v.is_empty()))
                .cloned()
                .unwrap_or_default()
        };
        let cfg = Self {
            telegram_token: get(&["TELEGRAM_BOT_TOKEN"]),
            endpoint: get(&["S3_ENDPOINT"]),
            region: get(&["S3_REGION"]),
            bucket: get(&["S3_BUCKET"]),
            access_key: get(&["S3_ACCESS_KEY", "S3_ACCESS_KEY_ID"]),
            secret_key: get(&["S3_SECRET_ACCESS_KEY", "S3_SECRET_ACCESS_KEY_ID"]),
            posts_chat_id: get(&["POSTS_CHAT_ID", "POSTS_CHATID"]).parse().unwrap_or(0),
            owner_chat_id: get(&["OWNER_CHAT_ID"]).parse().unwrap_or(0),
            silent: !matches!(get(&["SILENT"]).as_str(), "false" | "0"),
        };
        ensure!(
            !cfg.telegram_token.is_empty(),
            "TELEGRAM_BOT_TOKEN is required"
        );
        ensure!(
            [
                &cfg.endpoint,
                &cfg.region,
                &cfg.bucket,
                &cfg.access_key,
                &cfg.secret_key
            ]
            .iter()
            .all(|v| !v.is_empty()),
            "S3_* env vars are required"
        );
        Ok(cfg)
    }
}
pub fn load_env() {
    for path in [".env", "../.env", "../../.env"] {
        let _ = dotenvy::from_path(path);
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn aliases_and_validation() {
        assert!(Config::from_map(HashMap::new()).is_err());
        let m = [
            ("TELEGRAM_BOT_TOKEN", "x"),
            ("S3_ENDPOINT", "http://localhost"),
            ("S3_REGION", "r"),
            ("S3_BUCKET", "b"),
            ("S3_ACCESS_KEY_ID", "a"),
            ("S3_SECRET_ACCESS_KEY_ID", "s"),
            ("POSTS_CHATID", "-100"),
            ("SILENT", "false"),
        ]
        .into_iter()
        .map(|(k, v)| (k.into(), v.into()))
        .collect();
        let c = Config::from_map(m).unwrap();
        assert_eq!(c.posts_chat_id, -100);
        assert!(!c.silent);
    }
}
