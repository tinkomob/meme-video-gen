//! Local, credential-free review of mystery colors: cargo run --example mystery-colors -- OUTPUT.png
use anyhow::{Context, Result, ensure};
use meme_video_gen::visualizer::{AudioFrame, HEIGHT, Painter, WIDTH};

fn main() -> Result<()> {
    let output = std::env::args().nth(1).context("provide output PNG path")?;
    ensure!(
        !std::path::Path::new(&output).exists(),
        "output already exists"
    );
    let styles = [
        "veil",
        "parallax",
        "residue",
        "silver-mystery",
        "wave-mystery",
        "silk-mystery",
    ];
    let audio = AudioFrame {
        bands: [0.45; 64],
        level: 0.5,
        bass: 0.4,
        treble: 0.3,
        ..AudioFrame::default()
    };
    let mut sheet = image::RgbImage::new(6 * 180, 6 * 320);
    for (row, style) in styles.iter().enumerate() {
        for look in 0..6 {
            let mut p = Painter::new(style, 72 + look, 0);
            p.prepare_audio(&vec![audio; 120]);
            let frame =
                image::RgbImage::from_raw(WIDTH as u32, HEIGHT as u32, p.frame(90, audio).to_vec())
                    .context("RGB size")?;
            let thumb =
                image::imageops::resize(&frame, 180, 320, image::imageops::FilterType::Triangle);
            image::imageops::replace(&mut sheet, &thumb, look as i64 * 180, row as i64 * 320);
        }
    }
    sheet.save(output)?;
    Ok(())
}
