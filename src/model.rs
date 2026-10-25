use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Song {
    pub id: String,
    pub title: String,
    pub author: String,
    pub source_url: String,
    pub audio_key: String,
    pub duration_s: f64,
    pub added_at: String,
    pub last_seen_at: String,
    pub sha256: String,
}
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct SongsIndex {
    pub updated_at: String,
    #[serde(default, deserialize_with = "nullable_vec")]
    pub items: Vec<Song>,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Draft {
    pub id: String,
    pub chat_id: i64,
    pub user_id: i64,
    pub song: Song,
    pub style: String,
    pub variant: i32,
    #[serde(default, deserialize_with = "nullable_vec")]
    pub selected: Vec<String>,
    #[serde(default, deserialize_with = "nullable_map")]
    pub targets: BTreeMap<String, String>,
    pub created_at: String,
}
pub(crate) fn nullable_vec<'de, D, T>(d: D) -> Result<Vec<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de>,
{
    Ok(Option::deserialize(d)?.unwrap_or_default())
}
fn nullable_map<'de, D: serde::Deserializer<'de>>(
    d: D,
) -> Result<BTreeMap<String, String>, D::Error> {
    Ok(Option::deserialize(d)?.unwrap_or_default())
}
pub fn valid_video_id(id: &str) -> bool {
    id.len() == 11
        && id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
}
pub fn draft_key(id: &str, ext: &str) -> anyhow::Result<String> {
    anyhow::ensure!(
        id.len() == 24 && hex::decode(id).is_ok(),
        "invalid snippet draft ID"
    );
    Ok(format!("snippet-drafts/{id}{ext}"))
}
pub fn clean_author(s: &str) -> String {
    let s = s.trim();
    s.strip_suffix(" - Topic").unwrap_or(s).trim().to_owned()
}
pub fn eligible(s: &Song) -> bool {
    !s.audio_key.is_empty() && (s.duration_s == 0.0 || s.duration_s >= 20.0)
}
