use anyhow::{Context, Result};
use clap::Parser;
use meme_video_gen::upload::{OAuthClient, exchange};
use std::{
    io::{self, Write},
    path::PathBuf,
};
#[derive(Parser)]
#[command(about = "Generate YouTube OAuth credentials")]
struct Args {
    #[arg(long, default_value = "token.json")]
    token: PathBuf,
    #[arg(long, default_value = "client_secrets.json")]
    credentials: PathBuf,
}
#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    let cfg = OAuthClient::parse(serde_json::from_slice(
        &tokio::fs::read(&args.credentials).await?,
    )?)?;
    let redirect = cfg
        .redirect_uris
        .first()
        .context("OAuth client has no redirect URI")?;
    let mut url = reqwest::Url::parse(&cfg.auth_uri)?;
    url.query_pairs_mut().extend_pairs([("client_id",cfg.client_id.as_str()),("redirect_uri",redirect),("response_type","code"),("scope","https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube"),("access_type","offline"),("prompt","consent")]);
    println!(
        "Open this URL and authorize your YouTube account:\n{url}\n\nPaste the authorization code:"
    );
    io::stdout().flush()?;
    let mut code = String::new();
    io::stdin().read_line(&mut code)?;
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(60))
        .build()?;
    let token = exchange(
        &client,
        &cfg,
        &[
            ("grant_type", "authorization_code"),
            ("code", code.trim()),
            ("redirect_uri", redirect),
        ],
    )
    .await?;
    if let Some(parent) = args.token.parent()
        && !parent.as_os_str().is_empty()
    {
        tokio::fs::create_dir_all(parent).await?;
    }
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&args.token)?;
    file.write_all(&serde_json::to_vec_pretty(&token)?)?;
    file.sync_all()?;
    println!(
        "Token saved to {}. Upload it to S3 at tokens/token.json.",
        args.token.display()
    );
    Ok(())
}
