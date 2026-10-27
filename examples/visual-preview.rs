//! Local visual review: cargo run --release --example visual-preview -- OUTPUT AUDIO COVER
use anyhow::{Context, Result, ensure};
use clap::Parser;
use meme_video_gen::visualizer::{self, FPS, HEIGHT, Painter, STYLES, WIDTH};
use std::{
    io::Write,
    path::PathBuf,
    process::{Command, Stdio},
};

#[derive(Parser)]
struct Args {
    output: PathBuf,
    audio: PathBuf,
    cover: PathBuf,
    #[arg(long)]
    stills: bool,
    #[arg(long)]
    new_only: bool,
    #[arg(long,default_value_t=6,value_parser=clap::value_parser!(u32).range(1..=20))]
    seconds: u32,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let (output, audio, stills, new_only) = (args.output, args.audio, args.stills, args.new_only);
    let seconds = args.seconds as usize;
    let poster = (seconds * FPS / 2).min(90);
    let duration = seconds.to_string();
    let cover = image::open(&args.cover).context("open preview cover")?;
    ensure!(!output.exists(), "preview directory already exists");
    let pcm = Command::new("ffmpeg")
        .args(["-v", "error", "-nostdin", "-i"])
        .arg(&audio)
        .args([
            "-t", "20", "-f", "f32le", "-ac", "1", "-ar", "16000", "pipe:1",
        ])
        .output()?;
    ensure!(
        pcm.status.success(),
        "decode preview audio: {}",
        String::from_utf8_lossy(&pcm.stderr)
    );
    let samples: Vec<f64> = pcm
        .stdout
        .as_chunks::<4>()
        .0
        .iter()
        .map(|b| f32::from_le_bytes(*b) as f64)
        .collect();
    ensure!(
        samples.len() >= seconds * 16000,
        "preview audio must contain at least {seconds} seconds"
    );
    let frames = visualizer::analyze(&samples);
    std::fs::create_dir_all(&output)?;
    let mut sheet = image::RgbImage::new(5 * 108, 15 * 192);
    let mut concepts = image::RgbImage::new(4 * 270, 2 * 480);
    let mut audio_response = Vec::new();
    let mut html = String::from(
        "<!doctype html><meta charset=utf-8><title>75 visual variants</title><style>body{background:#171923;color:#eee;font:16px system-ui;margin:24px}main{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px}img,video{width:100%}figure{margin:0}a{color:#ace}</style><h1>8 новых визуалов</h1><p>Реальный CPU-рендер, 540×960, 30 fps. Видео: 6 секунд с музыкой, без обложки и текста. Ниже все 75 вариантов с разными seed.</p><main>",
    );
    html = html.replace("6 секунд", &format!("{seconds} сек."));
    for (index, style) in STYLES
        .iter()
        .enumerate()
        .filter(|(index, _)| !new_only || *index >= 17)
    {
        for variant in (0..3).filter(|variant| !new_only || *variant == 0) {
            let mut painter =
                Painter::new(style, 710 + index as u64 * 19 + variant as u64 * 7, variant);
            painter.set_cover(&cover);
            let frames = visualizer::smooth_material_audio(&painter, frames.clone());
            painter.prepare_audio(&frames);
            let frame = image::RgbImage::from_raw(
                WIDTH as u32,
                HEIGHT as u32,
                painter.frame(poster, frames[poster]).to_vec(),
            )
            .unwrap();
            let name = format!("{style}-{variant}.jpg");
            frame.save(output.join(&name))?;
            let thumb =
                image::imageops::resize(&frame, 108, 192, image::imageops::FilterType::Triangle);
            let tile = index * 3 + variant as usize;
            image::imageops::replace(
                &mut sheet,
                &thumb,
                (tile % 5 * 108) as i64,
                (tile / 5 * 192) as i64,
            );
            if index >= 17 && variant == if new_only { 0 } else { 1 } {
                let mut silent = Painter::new(style, 710 + index as u64 * 19 + 7, variant);
                silent.set_cover(&cover);
                silent.prepare_audio(&vec![visualizer::AudioFrame::default(); frames.len()]);
                let control = silent.frame(poster, visualizer::AudioFrame::default());
                let changed = frame
                    .as_raw()
                    .as_chunks::<3>()
                    .0
                    .iter()
                    .zip(control.as_chunks::<3>().0.iter())
                    .filter(|(a, b)| a.iter().zip(b.iter()).any(|(x, y)| x.abs_diff(*y) > 8))
                    .count();
                ensure!(
                    changed > 1000,
                    "weak actual music response for {style}: {changed} pixels"
                );
                let percent = changed as f64 * 100.0 / (WIDTH * HEIGHT) as f64;
                audio_response.push(serde_json::json!({"style":style,"variant":variant,"frame":poster,"changed_pixels":changed,"changed_percent":percent}));
                let thumb = image::imageops::resize(
                    &frame,
                    270,
                    480,
                    image::imageops::FilterType::Triangle,
                );
                image::imageops::replace(
                    &mut concepts,
                    &thumb,
                    ((index - 17) % 4 * 270) as i64,
                    ((index - 17) / 4 * 480) as i64,
                );
                if stills {
                    html.push_str(&format!(
                        "<figure><img src='{name}'><figcaption>{style}/1</figcaption></figure>"
                    ));
                    println!("Rendered still {style}; audio response {percent:.1}%");
                    continue;
                }
                let mut ffmpeg = Command::new("ffmpeg")
                    .args([
                        "-v", "error", "-nostdin", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s",
                        "540x960", "-r", "30", "-i", "pipe:0", "-i",
                    ])
                    .arg(&audio)
                    .args([
                        "-map",
                        "0:v",
                        "-map",
                        "1:a",
                        "-t",
                        &duration,
                        "-c:v",
                        "libx264",
                        "-preset",
                        "fast",
                        "-crf",
                        "22",
                        "-pix_fmt",
                        "yuv420p",
                        "-c:a",
                        "aac",
                        "-b:a",
                        "128k",
                        "-movflags",
                        "+faststart",
                    ])
                    .arg(output.join(format!("{style}.mp4")))
                    .stdin(Stdio::piped())
                    .stderr(Stdio::inherit())
                    .stdout(Stdio::null())
                    .spawn()?;
                let started = std::time::Instant::now();
                let write_result = (|| -> Result<()> {
                    let mut pipe = ffmpeg.stdin.take().unwrap();
                    for (f, a) in frames.iter().copied().take(seconds * FPS).enumerate() {
                        pipe.write_all(painter.frame(f, a))?;
                    }
                    Ok(())
                })();
                if write_result.is_err() {
                    let _ = ffmpeg.kill();
                }
                let status = ffmpeg.wait()?;
                write_result?;
                ensure!(status.success(), "encode {style}");
                html.push_str(&format!("<figure><video controls preload=metadata poster='{name}' src='{style}.mp4'></video><figcaption>{style}/1</figcaption></figure>"));
                println!(
                    "Rendered {style}; audio response {percent:.1}%; {:.3} s/frame",
                    started.elapsed().as_secs_f64() / (seconds * FPS) as f64
                );
            }
        }
    }
    html.push_str("</main><h1>Все 75 вариантов</h1><main>");
    for style in STYLES.iter().filter(|style| {
        !new_only
            || matches!(
                **style,
                "ferro"
                    | "cymatics"
                    | "topography"
                    | "glass"
                    | "ink"
                    | "kinetic"
                    | "lightning"
                    | "interference"
            )
    }) {
        for variant in (0..3).filter(|variant| !new_only || *variant == 0) {
            html.push_str(&format!("<figure><a href='{style}-{variant}.jpg'><img loading=lazy src='{style}-{variant}.jpg'></a><figcaption>{style}/{variant}</figcaption></figure>"));
        }
    }
    html.push_str("</main>");
    sheet.save(output.join("all-75.jpg"))?;
    concepts.save(output.join("new-8.jpg"))?;
    std::fs::write(output.join("index.html"), html)?;
    std::fs::write(
        output.join("audio-response.json"),
        serde_json::to_vec_pretty(&audio_response)?,
    )?;
    println!("Created {}", std::path::absolute(output)?.display());
    Ok(())
}
