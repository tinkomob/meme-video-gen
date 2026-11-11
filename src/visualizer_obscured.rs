//! Additive, opt-in mystery editions of legacy geometry. Original renders are untouched.
use super::*;

impl Painter {
    pub(super) fn obscure_frame(&mut self, seconds: f64) {
        // Always sample this frame's original render, never the previous output.
        let source = self.rgb.clone();
        let phase = self.phase;
        let look = mystery::look(self.mystery_look);
        let variant = self.variant as usize % 3;
        let response = self.response;
        let shade = &self.shade;
        let time = seconds * 0.12 * self.speed;
        let family = match self.style.as_str() {
            "wave" => 1.7,
            "silk" => 2.6,
            _ => 1.0,
        };
        parallel_rows(&mut self.rgb, WIDTH * 3, |y, row| {
            let v = y as f64 / HEIGHT as f64 * 2.0 - 1.0;
            for x in 0..WIDTH {
                let u = x as f64 / WIDTH as f64 * 2.0 - 1.0;
                let drift = response[variant + 2].travel * 0.08;
                let fold = (v * (3.1 + family) + time + phase).sin();
                let field = match variant {
                    0 => u * 2.8 + fold * 0.85 + time * 0.4,
                    1 => v * 3.5 + (u * 4.0 - time + phase).sin() * 0.9,
                    _ => u * 2.1 - v * 2.8 + (v * 3.0 + u * 2.0 + time).sin(),
                };
                let reveal = (0.5 + 0.5 * (field + phase + drift).sin()).powi(5);
                let haze = 0.5 + 0.5 * (field * 0.61 - time * 0.7 + 1.8).sin();
                let offset =
                    (8.0 + 12.0 * response[variant].opening) * (v * 4.0 + phase + time).sin();
                let sx = (x as f64 + offset).clamp(0.0, (WIDTH - 1) as f64) as usize;
                let sy = (y as f64 + 15.0 * (u * 3.0 - time + phase).sin())
                    .clamp(0.0, (HEIGHT - 1) as f64) as usize;
                let i = (y * WIDTH + x) * 3;
                let displaced = (sy * WIDTH + sx) * 3;
                let pixel = &source[i..i + 3];
                let luminance =
                    pixel[0] as f64 * 0.2126 + pixel[1] as f64 * 0.7152 + pixel[2] as f64 * 0.0722;
                let developed = look.develop(std::array::from_fn(|c| {
                    let detail = luminance * 0.65 + pixel[c] as f64 * 0.35;
                    detail * (0.10 + 0.68 * reveal) + source[displaced + c] as f64 * 0.10 * haze
                }));
                for c in 0..3 {
                    if look.light {
                        row[x * 3 + c] =
                            (developed[c] * shade[i / 3] as f64).clamp(0.0, 255.0) as u8;
                        continue;
                    }
                    let pigment = luminance * 0.65 + pixel[c] as f64 * 0.35;
                    let echo = source[displaced + c] as f64 * 0.10 * haze;
                    let fog = [9.0, 12.0, 16.0][c] * haze * shade[i / 3] as f64;
                    row[x * 3 + c] = ((pigment * (0.10 + 0.68 * reveal) + echo + fog) * 0.55
                        + developed[c] * 0.45 * shade[i / 3] as f64)
                        .clamp(0.0, 255.0) as u8;
                }
            }
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mystery_editions_preserve_legacy_geometry_and_seek_cleanly() {
        for style in ["silver", "wave", "silk"] {
            for variant in 0..3 {
                let mut original = Painter::new(style, 71, variant);
                let mut edition = Painter::new(&format!("{style}-mystery"), 71, variant);
                assert_eq!(edition.style, original.style);
                assert_eq!(edition.base, original.base);
                let audio = AudioFrame {
                    bands: [0.5; BANDS],
                    level: 0.5,
                    ..AudioFrame::default()
                };
                original.prepare_audio(&vec![audio; 301]);
                edition.prepare_audio(&vec![audio; 301]);
                let old = original.frame(90, audio).to_vec();
                let new = edition.frame(90, audio).to_vec();
                assert_ne!(old, new);
                // Mystery editions may now be pale/subtractive rather than darker.
                assert!(new.iter().any(|v| *v > 30));
                assert_ne!(new, edition.frame(300, audio));
                assert_eq!(new, edition.frame(90, audio));
            }
        }
    }
}
