use anyhow::{Result, ensure};
use clap::Parser;
use meme_video_gen::{
    config::{self, Config},
    snippet,
    storage::Storage,
};
use std::{path::PathBuf, time::Duration};
#[derive(Parser)]
#[command(about = "Generate a local 20-second music snippet from S3")]
struct Args {
    #[arg(long, default_value = "song-snippet.mp4")]
    output: PathBuf,
    #[arg(long, default_value = "random")]
    style: String,
    #[arg(long,default_value_t=-1,allow_hyphen_values=true)]
    variant: i32,
    #[arg(long, default_value = "")]
    song: String,
}
#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    ensure!(
        !args.output.try_exists()?,
        "output already exists: {}",
        args.output.display()
    );
    snippet::validate(&args.style, args.variant)?;
    config::load_env();
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .init();
    let store = Storage::new(Config::load()?)?;
    let generated = tokio::select! {r=tokio::time::timeout(Duration::from_secs(600),snippet::generate(&store,&args.style,args.variant,&args.song))=>r??,_=tokio::signal::ctrl_c()=>anyhow::bail!("cancelled")};
    snippet::save_new(&generated.path(), &args.output).await?;
    println!(
        "Created {}\nSong: {} — {}\nS3 audio: {}",
        std::path::absolute(args.output)?.display(),
        generated.song.author,
        generated.song.title,
        generated.song.audio_key
    );
    Ok(())
}
