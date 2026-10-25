use crate::config::Config;
use anyhow::{Context, Result, ensure};
use chrono::Utc;
use futures_util::StreamExt;
use hmac::{Hmac, Mac};
use reqwest::{Client, Method, Response, Url};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use sha2::{Digest, Sha256};
use std::{path::Path, time::Duration};
use tokio::io::AsyncWriteExt;
#[derive(Clone)]
pub struct Storage {
    cfg: Config,
    client: Client,
}
#[derive(Debug, Deserialize)]
pub struct Object {
    #[serde(rename = "Key")]
    pub key: String,
    #[serde(rename = "Size")]
    pub size: u64,
}
#[derive(Default, Deserialize)]
struct Listing {
    #[serde(rename = "Contents", default)]
    items: Vec<Object>,
    #[serde(rename = "NextContinuationToken")]
    next: Option<String>,
}
pub fn encode(s: &str, slash: bool) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) || (slash && b == b'/') {
            out.push(b as char)
        } else {
            out.push_str(&format!("%{b:02X}"))
        }
    }
    out
}
fn hash(data: &[u8]) -> String {
    hex::encode(Sha256::digest(data))
}
fn hmac(key: &[u8], msg: &str) -> Vec<u8> {
    let mut m = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts any key");
    m.update(msg.as_bytes());
    m.finalize().into_bytes().to_vec()
}
impl Storage {
    pub fn new(cfg: Config) -> Result<Self> {
        Ok(Self {
            cfg,
            client: Client::builder()
                .connect_timeout(Duration::from_secs(10))
                .timeout(Duration::from_secs(300))
                .build()?,
        })
    }
    async fn request(
        &self,
        method: Method,
        key: &str,
        query: &[(&str, String)],
        body: Vec<u8>,
        content_type: &str,
        range: Option<(u64, u64)>,
    ) -> Result<Response> {
        let mut url = Url::parse(&self.cfg.endpoint)?;
        let mut path = url.path().trim_end_matches('/').to_owned();
        if url.host_str().unwrap_or_default().contains("amazonaws.com") {
            let host = format!("{}.{}", self.cfg.bucket, url.host_str().unwrap());
            url.set_host(Some(&host))?;
        } else {
            path.push('/');
            path.push_str(&encode(&self.cfg.bucket, false));
        }
        path.push('/');
        path.push_str(&encode(key, true));
        url.set_path(&path);
        let mut query: Vec<_> = query
            .iter()
            .map(|(k, v)| (encode(k, false), encode(v, false)))
            .collect();
        query.sort();
        let query = query
            .into_iter()
            .map(|(k, v)| format!("{k}={v}"))
            .collect::<Vec<_>>()
            .join("&");
        if !query.is_empty() {
            url.set_query(Some(&query));
        }
        let now = Utc::now();
        let date = now.format("%Y%m%d").to_string();
        let time = now.format("%Y%m%dT%H%M%SZ").to_string();
        let payload = hash(&body);
        let host = match url.port() {
            Some(p) => format!("{}:{p}", url.host_str().unwrap()),
            None => url.host_str().unwrap().to_owned(),
        };
        let headers = format!("host:{host}\nx-amz-content-sha256:{payload}\nx-amz-date:{time}\n");
        let signed = "host;x-amz-content-sha256;x-amz-date";
        let canonical = format!(
            "{method}\n{}\n{query}\n{headers}\n{signed}\n{payload}",
            url.path()
        );
        let scope = format!("{date}/{}/s3/aws4_request", self.cfg.region);
        let to_sign = format!(
            "AWS4-HMAC-SHA256\n{time}\n{scope}\n{}",
            hash(canonical.as_bytes())
        );
        let k = hmac(format!("AWS4{}", self.cfg.secret_key).as_bytes(), &date);
        let k = hmac(&k, &self.cfg.region);
        let k = hmac(&k, "s3");
        let k = hmac(&k, "aws4_request");
        let auth = format!(
            "AWS4-HMAC-SHA256 Credential={}/{scope}, SignedHeaders={signed}, Signature={}",
            self.cfg.access_key,
            hex::encode(hmac(&k, &to_sign))
        );
        let mut req = self
            .client
            .request(method, url)
            .header("x-amz-date", time)
            .header("x-amz-content-sha256", payload)
            .header("Authorization", auth);
        if !content_type.is_empty() {
            req = req.header("Content-Type", content_type);
        }
        if let Some((a, b)) = range {
            req = req.header("Range", format!("bytes={a}-{b}"));
        }
        req.body(body).send().await.context("S3 request")
    }
    pub async fn open(&self, key: &str, range: Option<(u64, u64)>) -> Result<Response> {
        let r = self
            .request(Method::GET, key, &[], vec![], "", range)
            .await?
            .error_for_status()?;
        if range.is_some() {
            ensure!(r.status() == 206, "S3 ignored range request");
        }
        Ok(r)
    }
    pub async fn get(&self, key: &str) -> Result<Option<Vec<u8>>> {
        let r = self
            .request(Method::GET, key, &[], vec![], "", None)
            .await?;
        if r.status() == 404 {
            return Ok(None);
        }
        Ok(Some(r.error_for_status()?.bytes().await?.to_vec()))
    }
    pub async fn read<T: DeserializeOwned>(&self, key: &str) -> Result<Option<T>> {
        self.get(key)
            .await?
            .map(|b| serde_json::from_slice(&b).with_context(|| format!("invalid JSON at {key}")))
            .transpose()
    }
    pub async fn put(&self, key: &str, data: Vec<u8>, ct: &str) -> Result<()> {
        self.request(Method::PUT, key, &[], data, ct, None)
            .await?
            .error_for_status()?;
        Ok(())
    }
    pub async fn write<T: Serialize + ?Sized>(&self, key: &str, value: &T) -> Result<()> {
        self.put(key, serde_json::to_vec_pretty(value)?, "application/json")
            .await
    }
    pub async fn delete(&self, key: &str) -> Result<()> {
        self.request(Method::DELETE, key, &[], vec![], "", None)
            .await?
            .error_for_status()?;
        Ok(())
    }
    pub async fn list(&self, prefix: &str) -> Result<Vec<Object>> {
        let mut out = vec![];
        let mut next = None;
        loop {
            let mut q = vec![("list-type", "2".into()), ("prefix", prefix.into())];
            if let Some(n) = next {
                q.push(("continuation-token", n));
            }
            let text = self
                .request(Method::GET, "", &q, vec![], "", None)
                .await?
                .error_for_status()?
                .text()
                .await?;
            let page: Listing = quick_xml::de::from_str(&text)?;
            out.extend(page.items);
            next = page.next;
            if next.is_none() {
                return Ok(out);
            }
        }
    }
    pub async fn download(&self, key: &str, path: &Path) -> Result<()> {
        let r = self.open(key, None).await?;
        let expected = r.content_length();
        let mut stream = r.bytes_stream();
        let mut file = tokio::fs::File::create(path).await?;
        let mut count = 0;
        while let Some(chunk) = stream.next().await {
            let chunk = chunk?;
            file.write_all(&chunk).await?;
            count += chunk.len() as u64;
        }
        file.flush().await?;
        ensure!(expected.is_none_or(|n| n == count), "S3 stream truncated");
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn encoding() {
        assert_eq!(
            encode("friends/Season 2_Друзья.mp4", true),
            "friends/Season%202_%D0%94%D1%80%D1%83%D0%B7%D1%8C%D1%8F.mp4"
        );
        assert_eq!(encode("a+b/c", false), "a%2Bb%2Fc");
    }
    #[test]
    fn hmac_vector() {
        assert_eq!(
            hex::encode(hmac(b"key", "The quick brown fox jumps over the lazy dog")),
            "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
        );
    }
}
