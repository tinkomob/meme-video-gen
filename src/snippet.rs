use crate::{
    audio,
    model::{Song, clean_author, valid_video_id},
    storage::Storage,
    visualizer::{self, Painter, STYLES},
};
use anyhow::{Context, Result, ensure};
use rand::Rng;
use std::{
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
};
use tokio::sync::Semaphore;
use tokio_util::sync::CancellationToken;
static RENDERER: Semaphore = Semaphore::const_new(1);
pub struct Generated {
    pub dir: tempfile::TempDir,
    pub song: Song,
    pub style: String,
    pub variant: i32,
}
impl Generated {
    pub fn path(&self) -> PathBuf {
        self.dir.path().join("snippet.mp4")
    }
}
pub fn validate(style: &str, variant: i32) -> Result<()> {
    ensure!(
        style == "random" || STYLES.contains(&style),
        "unknown snippet style"
    );
    ensure!((-1..=2).contains(&variant), "variant must be -1, 0, 1 or 2");
    Ok(())
}
pub fn escape_path(p: &Path) -> String {
    p.to_string_lossy()
        .replace('\\', "\\\\")
        .replace(':', "\\:")
        .replace('\'', "'\\''")
}
pub fn valid_cover(data: &[u8]) -> bool {
    if data.len() > 8 << 20 || image::guess_format(data).ok() != Some(image::ImageFormat::Jpeg) {
        return false;
    }
    let reader =
        image::ImageReader::with_format(std::io::Cursor::new(data), image::ImageFormat::Jpeg);
    reader
        .into_dimensions()
        .is_ok_and(|(w, h)| (300..=8192).contains(&w) && (170..=8192).contains(&h))
}
async fn cover(store: &Storage, id: &str, dir: &Path) -> Result<PathBuf> {
    ensure!(valid_video_id(id), "invalid YouTube video ID");
    let path = dir.join("cover.jpg");
    let key = format!("covers/{id}.jpg");
    if let Ok(Some(bytes)) = store.get(&key).await
        && valid_cover(&bytes)
    {
        tokio::fs::write(&path, bytes).await?;
        return Ok(path);
    }
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(4))
        .build()?;
    for host in [
        "https://i.ytimg.com",
        "https://img.youtube.com",
        "http://i.ytimg.com",
    ] {
        for size in ["maxresdefault", "sddefault", "hqdefault"] {
            let r = match client
                .get(format!("{host}/vi/{id}/{size}.jpg"))
                .send()
                .await
            {
                Ok(r) => r,
                Err(_) => break,
            };
            if !r.status().is_success() || r.content_length().is_some_and(|s| s > 8 << 20) {
                continue;
            }
            use futures_util::StreamExt;
            let mut data = Vec::new();
            let mut stream = r.bytes_stream();
            while let Some(chunk) = stream.next().await {
                data.extend_from_slice(&chunk?);
                ensure!(data.len() <= 8 << 20, "cover exceeds limit");
            }
            if !valid_cover(&data) {
                continue;
            }
            tokio::fs::write(&path, &data).await?;
            if let Err(e) = store.put(&key, data, "image/jpeg").await {
                tracing::warn!("cover cache: {e}");
            }
            return Ok(path);
        }
    }
    anyhow::bail!("no valid cover")
}
pub async fn generate(
    store: &Storage,
    style: &str,
    variant: i32,
    song_id: &str,
) -> Result<Generated> {
    validate(style, variant)?;
    let mut song = audio::choose_song(audio::songs(store).await?, song_id)?;
    song.author = clean_author(&song.author);
    let dir = tempfile::tempdir()?;
    let audio = audio::download(store, &song, dir.path()).await?;
    let duration = audio::probe(&audio).await?;
    ensure!(
        duration >= 20.0,
        "snippet requires at least 20 seconds of audio"
    );
    let start = rand::rng().random::<f64>() * (duration * 0.75 - 20.0).max(0.0);
    let style = if style == "random" {
        STYLES[rand::rng().random_range(0..STYLES.len())]
    } else {
        style
    }
    .to_owned();
    let variant = if variant < 0 {
        rand::rng().random_range(0..3)
    } else {
        variant
    };
    let title = song.title.split_whitespace().collect::<Vec<_>>().join(" ");
    let artist = song.author.split_whitespace().collect::<Vec<_>>().join(" ");
    let title_size = if title.chars().count() > 30 {
        (1440 / title.chars().count()).max(18)
    } else {
        48
    };
    let elapsed =
        format!("%{{eif:floor((t+{start:.6})/60):d:2}}:%{{eif:mod(t+{start:.6},60):d:2}}");
    for (name, value) in [
        ("title.txt", title),
        ("artist.txt", artist),
        ("elapsed.txt", elapsed),
        (
            "duration.txt",
            format!("{:02}:{:02}", duration as u64 / 60, duration as u64 % 60),
        ),
    ] {
        tokio::fs::write(dir.path().join(name), value).await?;
    }
    let _permit = RENDERER.acquire().await?;
    let cover = match cover(store, &song.id, dir.path()).await {
        Ok(p) => p,
        Err(e) => {
            tracing::warn!("cover unavailable, using waveform: {e}");
            let p = dir.path().join("wave-cover.png");
            audio::command(
                "ffmpeg",
                &[
                    "-hide_banner",
                    "-loglevel",
                    "error",
                    "-nostdin",
                    "-y",
                    "-i",
                    &audio.to_string_lossy(),
                    "-filter_complex",
                    "aformat=channel_layouts=mono,showwavespic=s=600x600:colors=white,format=rgb24",
                    "-frames:v",
                    "1",
                    &p.to_string_lossy(),
                ],
            )
            .await?;
            p
        }
    };
    let pcm = audio::command(
        "ffmpeg",
        &[
            "-v",
            "error",
            "-ss",
            &format!("{start:.6}"),
            "-i",
            &audio.to_string_lossy(),
            "-t",
            "20",
            "-af",
            FADE,
            "-ac",
            "1",
            "-ar",
            "16000",
            "-f",
            "s16le",
            "pipe:1",
        ],
    )
    .await?;
    ensure!(pcm.len() >= 636800, "decoded audio is shorter than snippet");
    let token = CancellationToken::new();
    let guard = token.clone().drop_guard();
    let path = dir.path().to_owned();
    let output = path.join("snippet.mp4");
    let permit = _permit;
    // The worker owns its temp directory and render permit until FFmpeg has stopped.
    let result = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        let frames = visualizer::analyze(
            &pcm.as_chunks::<2>()
                .0
                .iter()
                .map(|b| i16::from_le_bytes([b[0], b[1]]) as f64 / 32768.0)
                .collect::<Vec<_>>(),
        );
        let mut painter = Painter::new(&style, rand::random(), variant);
        painter.set_cover(&image::open(&cover).context("decode snippet cover")?);
        render(
            &audio, &cover, &output, &path, start, duration, title_size, painter, frames, &token,
        )?;
        Ok::<_, anyhow::Error>(Generated {
            dir,
            song,
            style,
            variant,
        })
    })
    .await?;
    guard.disarm();
    result
}
pub const FADE: &str = "afade=t=in:d=0.15,afade=t=out:st=19.5:d=0.5";
fn font(serif: bool) -> String {
    let fonts = if serif {
        [
            "C:/Windows/Fonts/georgia.ttf",
            "/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf",
            "/usr/share/fonts/ttf-dejavu/DejaVuSerif.ttf",
        ]
    } else {
        [
            "C:/Windows/Fonts/arial.ttf",
            "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
            "/usr/share/fonts/ttf-dejavu/DejaVuSans.ttf",
        ]
    };
    fonts
        .into_iter()
        .find(|f| Path::new(f).exists())
        .map(|f| format!("fontfile='{}':", escape_path(Path::new(f))))
        .unwrap_or_default()
}
fn filter(accent: &str, dir: &Path, start: f64, duration: f64, title_size: usize) -> String {
    let sans = font(false);
    let serif = font(true);
    let title = escape_path(&dir.join("title.txt"));
    let artist = escape_path(&dir.join("artist.txt"));
    let elapsed = escape_path(&dir.join("elapsed.txt"));
    let total = escape_path(&dir.join("duration.txt"));
    format!(
        "[0:v]scale=1080:1920:flags=lanczos,setsar=1[bg];[1:a]aformat=sample_rates=48000:channel_layouts=stereo,asetpts=PTS-STARTPTS,{FADE}[audio];[2:v]trim=end_frame=1,setpts=PTS-STARTPTS,scale=520:520:force_original_aspect_ratio=increase,crop=520:520,setsar=1,format=rgba,geq=r='r(X,Y)':g='g(X,Y)':b='b(X,Y)':a='if(lte(hypot(max(abs(X-W/2)-(W/2-14),0),max(abs(Y-H/2)-(H/2-14),0)),14),255,0)'[cover];[bg][cover]overlay=x=280:y=560:eof_action=repeat,drawtext={serif}textfile='{title}':expansion=none:fontsize={title_size}:fontcolor=white:shadowcolor=black@0.7:shadowx=1:shadowy=2:x=(w-tw)/2:y=1130,drawtext={sans}textfile='{artist}':expansion=none:fontsize=28:fontcolor=0xb7b7c5:x=(w-tw)/2:y=1210,drawtext={sans}textfile='{elapsed}':fontsize=22:fontcolor=0xd0d0d8:x=210:y=1320,drawtext={sans}textfile='{total}':expansion=none:fontsize=22:fontcolor=0xd0d0d8:x=870-tw:y=1320[metadata];color=c=0x454550:s=660x4:r=30:d=20[track];color=c={accent}:s=660x4:r=30:d=20[fill];[track][fill]overlay=x='-660+660*min(({start:.6}+t)/{duration:.6},1)':y=0:eval=frame[progress];[metadata][progress]overlay=x=210:y=1285:shortest=1,drawtext={sans}text='●':fontsize=25:fontcolor=white:x='210+660*min(({start:.6}+t)/{duration:.6},1)-text_w/2':y=1287-text_h/2,format=yuv420p[video]"
    )
}
struct Child(std::process::Child);
impl Drop for Child {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
#[allow(clippy::too_many_arguments)]
fn render(
    audio: &Path,
    cover: &Path,
    output: &Path,
    dir: &Path,
    start: f64,
    duration: f64,
    size: usize,
    mut painter: Painter,
    frames: Vec<visualizer::AudioFrame>,
    cancel: &CancellationToken,
) -> Result<()> {
    painter.prepare_audio(&frames);
    let graph = filter(&painter.accent(), dir, start, duration, size);
    let mut child = Child(
        Command::new("ffmpeg")
            .args([
                "-hide_banner",
                "-loglevel",
                "error",
                "-nostdin",
                "-y",
                "-probesize",
                "32",
                "-analyzeduration",
                "0",
                "-thread_queue_size",
                "8",
                "-f",
                "rawvideo",
                "-pixel_format",
                "rgb24",
                "-video_size",
                "540x960",
                "-framerate",
                "30",
                "-i",
                "pipe:0",
                "-thread_queue_size",
                "8",
                "-ss",
                &format!("{start:.6}"),
                "-t",
                "20",
                "-i",
            ])
            .arg(audio)
            .arg("-i")
            .arg(cover)
            .args([
                "-filter_complex_threads",
                "1",
                "-filter_complex",
                &graph,
                "-map",
                "[video]",
                "-map",
                "[audio]",
                "-t",
                "20",
                "-r",
                "30",
                "-c:v",
                "libx264",
                "-preset",
                "fast",
                "-crf",
                "20",
                "-maxrate",
                "7M",
                "-bufsize",
                "2M",
                "-pix_fmt",
                "yuv420p",
                "-c:a",
                "aac",
                "-b:a",
                "192k",
                "-ar",
                "48000",
                "-movflags",
                "+faststart",
            ])
            .arg(output)
            .stdin(Stdio::piped())
            .stderr(Stdio::piped())
            .stdout(Stdio::null())
            .spawn()
            .context("start FFmpeg renderer")?,
    );
    let mut stderr = child.0.stderr.take().unwrap();
    let reader = std::thread::spawn(move || {
        let mut b = String::new();
        let _ = stderr.read_to_string(&mut b);
        b
    });
    let mut pipe = child.0.stdin.take().unwrap();
    let mut write_error = None;
    for (f, a) in frames.into_iter().enumerate() {
        if cancel.is_cancelled() {
            let _ = child.0.kill();
            write_error = Some(anyhow::anyhow!("render cancelled"));
            break;
        }
        if let Err(e) = pipe.write_all(painter.frame(f, a)) {
            write_error = Some(e.into());
            break;
        }
        if (f + 1) % 30 == 0 {
            tracing::info!("rendered {}/600 frames", f + 1);
        }
    }
    drop(pipe);
    let status = child.0.wait()?;
    let stderr = reader.join().unwrap_or_default();
    ensure!(status.success(), "render snippet: {stderr}");
    if let Some(e) = write_error {
        return Err(e);
    }
    ensure!(
        std::fs::metadata(output)?.len() <= 20_000_000,
        "rendered snippet exceeds 20 MB"
    );
    Ok(())
}
pub async fn save_new(source: &Path, dest: &Path) -> Result<()> {
    use tokio::io::AsyncWriteExt;
    let mut src = tokio::fs::File::open(source).await?;
    let mut dst = tokio::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(dest)
        .await?;
    let result = async {
        tokio::io::copy(&mut src, &mut dst).await?;
        dst.flush().await
    }
    .await;
    drop(dst);
    if let Err(e) = result {
        let _ = tokio::fs::remove_file(dest).await;
        return Err(e.into());
    }
    Ok(())
}
#[cfg(test)]
mod cover_tests {
    use super::*;

    #[test]
    fn collage_is_unavailable() {
        assert!(validate("collage", 0).is_err());
    }

    #[test]
    fn ffmpeg_renders_every_scene_with_fixed_player() {
        let dir = tempfile::tempdir().unwrap();
        for (name, text) in [
            ("title.txt", "Ночной свет <&> 'test'"),
            ("artist.txt", "Артист"),
            ("elapsed.txt", "%{eif:floor(t/60):d:2}:%{eif:mod(t,60):d:2}"),
            ("duration.txt", "03:20"),
        ] {
            std::fs::write(dir.path().join(name), text).unwrap();
        }
        let cover = image::DynamicImage::ImageRgb8(image::RgbImage::from_fn(600, 600, |x, y| {
            let sky = y as f64 / 600.0;
            let sun = ((x as f64 - 395.0).hypot(y as f64 - 240.0) / 62.0).powi(8);
            let mountain =
                360.0 + 55.0 * (x as f64 * 0.009).sin() + 25.0 * (x as f64 * 0.023).cos();
            let foreground = 470.0 + 25.0 * (x as f64 * 0.014 + 2.0).sin();
            if y as f64 > foreground {
                image::Rgb([15, 28, 36])
            } else if y as f64 > mountain {
                image::Rgb([43, 57, 68])
            } else {
                let glow = 1.0 / (1.0 + sun);
                image::Rgb([
                    (33.0 + sky * 120.0 + glow * 70.0) as u8,
                    (52.0 + sky * 65.0 + glow * 65.0) as u8,
                    (78.0 + sky * 15.0 + glow * 30.0) as u8,
                ])
            }
        }));
        let cover_path = dir.path().join("input-cover.png");
        cover.save(&cover_path).unwrap();
        for (index, style) in STYLES.into_iter().enumerate() {
            let mut painter = Painter::new(style, index as u64, 1);
            painter.set_cover(&cover);
            let background = dir.path().join(format!("{style}-bg.png"));
            let frame = painter
                .frame(
                    200,
                    visualizer::AudioFrame {
                        level: 0.6,
                        bass: 0.4,
                        treble: 0.25,
                        beat: 0.35,
                        bands: [0.4; 64],
                        ..visualizer::AudioFrame::default()
                    },
                )
                .to_vec();
            image::RgbImage::from_raw(540, 960, frame)
                .unwrap()
                .save(&background)
                .unwrap();
            let graph = filter(&painter.accent(), dir.path(), 0.0, 200.0, 48);
            let output = dir.path().join(format!("{style}.png"));
            let result = Command::new("ffmpeg")
                .args(["-v", "error", "-y", "-i"])
                .arg(&background)
                .args(["-f", "lavfi", "-i", "sine=duration=1", "-i"])
                .arg(&cover_path)
                .args([
                    "-filter_complex_threads",
                    "1",
                    "-filter_complex",
                    &graph,
                    "-map",
                    "[video]",
                    "-frames:v",
                    "1",
                ])
                .arg(&output)
                .args(["-map", "[audio]", "-t", "0.05", "-f", "null", "-"])
                .output()
                .unwrap();
            assert!(
                result.status.success(),
                "{style}: {}",
                String::from_utf8_lossy(&result.stderr)
            );
            let frame = image::open(output).unwrap();
            assert_eq!((frame.width(), frame.height()), (1080, 1920));
            let pixels = frame.to_rgb8();
            let white_rows: Vec<_> = (1260..1310)
                .filter(|&y| {
                    (195..225).any(|x| pixels.get_pixel(x, y).0.into_iter().all(|c| c > 230))
                })
                .collect();
            let center = (white_rows.first().unwrap() + white_rows.last().unwrap()) as f64 / 2.0;
            assert!(
                (center - 1287.0).abs() <= 1.0,
                "{style}: thumb center {center}"
            );
        }
    }

    fn jpeg(w: u32, h: u32) -> Vec<u8> {
        let image = image::RgbImage::from_pixel(w, h, image::Rgb([1, 2, 3]));
        let mut out = std::io::Cursor::new(Vec::new());
        image.write_to(&mut out, image::ImageFormat::Jpeg).unwrap();
        out.into_inner()
    }
    #[test]
    fn validates_cover_dimensions_format_and_size() {
        assert!(valid_cover(&jpeg(600, 600)));
        assert!(!valid_cover(&jpeg(120, 90)));
        assert!(!valid_cover(b"not an image"));
        assert!(!valid_cover(&vec![0; (8 << 20) + 1]));
    }
}
