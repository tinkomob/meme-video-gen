//! Quiet, incomplete forms: translucent veils, occluded depth planes, and
//! decaying impressions. All motion is a function of the requested frame and
//! the prepared audio response, never of the previously rendered image.
use super::*;

/// Three dark pigment looks and three genuinely light, subtractive looks.
/// Shared by new families and legacy mystery editions, independent of geometry.
#[derive(Clone, Copy)]
pub(super) struct Look {
    pub(super) background: [f64; 3],
    pub(super) pigment: [f64; 3],
    pub(super) light: bool,
}

pub(super) fn look(index: usize) -> Look {
    let (background, pigment) = match index % 6 {
        0 => ([6.0, 10.0, 19.0], [94.0, 173.0, 218.0]),
        1 => ([19.0, 8.0, 17.0], [216.0, 126.0, 158.0]),
        2 => ([7.0, 18.0, 16.0], [116.0, 194.0, 164.0]),
        3 => ([239.0, 231.0, 216.0], [103.0, 128.0, 148.0]),
        4 => ([213.0, 231.0, 242.0], [115.0, 101.0, 156.0]),
        _ => ([237.0, 219.0, 201.0], [155.0, 111.0, 80.0]),
    };
    Look {
        background,
        pigment,
        light: index % 6 >= 3,
    }
}

impl Look {
    pub(super) fn develop(&self, signal: [f64; 3]) -> [f64; 3] {
        std::array::from_fn(|c| {
            let amount = (signal[c] / 150.0).clamp(0.0, 1.0);
            if self.light {
                self.background[c] + (self.pigment[c] - self.background[c]) * amount
            } else {
                self.background[c] + self.pigment[c] * amount
            }
        })
    }
}

pub(super) fn matches(style: &str) -> bool {
    matches!(style, "veil" | "parallax" | "residue")
}

// Compact analytic fields instead of a volume march or a low-resolution buffer.
// This rational bell has a gentle optical tail and needs no per-pixel exp/powf.
fn bell(x: f64) -> f64 {
    let d = 1.0 + x * x;
    1.0 / (d * d)
}

fn ease(x: f64) -> f64 {
    let x = x.clamp(0.0, 1.0);
    x * x * (3.0 - 2.0 * x)
}

#[derive(Clone, Copy, Default)]
struct Layer {
    x: f64,
    y: f64,
    angle: f64,
    bend: f64,
    width: f64,
    length: f64,
    phase: f64,
    opening: f64,
    light: f64,
}

struct Mystery {
    kind: usize,
    variant: usize,
    phase: f64,
    layers: [Layer; 5],
    colors: [[f64; 3]; 3],
}

impl Mystery {
    fn new(p: &Painter, time: f64) -> Self {
        let kind = match p.style.as_str() {
            "parallax" => 1,
            "residue" => 2,
            _ => 0,
        };
        let variant = p.variant as usize % 3;
        let layers = std::array::from_fn(|i| {
            let particle = p.particles[90 + i];
            let voice = p.response[(i * 2 + kind) % 12];
            let phase = particle.phase + p.phase;
            // Each depth has its own slow musical clock. Neither a beat nor an
            // event number changes position; integrated travel is continuous.
            let clock = time * (0.045 + particle.depth * 0.025) + voice.travel * 0.055;
            Layer {
                x: (i as f64 - 2.0) * 0.24
                    + 0.09 * (clock + phase).sin()
                    + 0.05 * voice.displacement * phase.cos(),
                y: (particle.v - 0.5) * 0.7 + 0.07 * (clock * 0.73 - phase).sin(),
                angle: (phase.sin() * 0.16
                    + [0.0, 0.68, -0.45][variant]
                    + 0.025 * (clock + phase).sin()),
                bend: 0.06 + particle.u * 0.16 + voice.opening * 0.08,
                width: 0.065 + particle.depth * 0.07 + voice.opening * 0.035,
                length: 0.6 + particle.u * 0.55,
                phase: phase + clock,
                opening: voice.opening,
                light: 0.66 + voice.opening * 0.72,
            }
        });
        // Pigment is deliberately low-chroma. The seed changes light temperature
        // and spatial phase, not merely the palette of an otherwise fixed loop.
        let tint = (p.phase * 0.71).sin() * 0.5 + 0.5;
        let colors = [
            [105.0 + tint * 21.0, 121.0, 139.0 - tint * 18.0],
            [143.0, 116.0 + tint * 12.0, 112.0 + tint * 24.0],
            [108.0 + tint * 18.0, 139.0 - tint * 10.0, 132.0],
        ];
        Self {
            kind,
            variant,
            phase: p.phase,
            layers,
            colors,
        }
    }

    fn sample(&self, x: f64, y: f64) -> [f64; 3] {
        let mut rgb = [5.0, 6.0, 9.0];
        // Sparse light, with off-center concentration and an unoccupied margin.
        let vignette = bell(x * 0.55) * bell(y * 0.33);
        for (i, layer) in self.layers.iter().enumerate() {
            // A shear rather than a spinning camera preserves quiet composition.
            let u = x - layer.x + layer.angle * (y - layer.y);
            let v = y - layer.y - layer.angle * 0.24 * x;
            let phase = layer.phase;
            let (coverage, highlight) = match self.kind {
                0 => {
                    // Veil: broad translucent membranes with folded, fading lips.
                    // Variants: hanging clefts / oblique folded bands / bowed fans.
                    let (across, along) = match self.variant {
                        1 => (v + 0.3 * u, u),
                        2 => (u + 0.38 * v * v - 0.22, v + 0.25 * u),
                        _ => (u, v),
                    };
                    let fold = across
                        + layer.bend * (along * 2.4 + phase).sin()
                        + 0.035 * (along * 5.3 - phase * 0.61).sin();
                    let end = bell((along + 0.18 * phase.sin()) / layer.length);
                    let interruption = ease((along + 0.85 - i as f64 * 0.12) * 3.0);
                    let membrane = bell(fold / (layer.width * 2.6));
                    let lip = bell((fold - layer.width * 0.55) / 0.022);
                    let hollow = ease((across + 0.06) * 8.0);
                    (
                        membrane * end * interruption * hollow * 0.37,
                        lip * end * interruption * 0.32,
                    )
                }
                1 => {
                    // Parallax: near-opaque planes conceal more distant luminous
                    // edges. There are no boxes, corridors or closed silhouettes.
                    // Variants change the topology of the cut, not its hue.
                    let (edge, along) = match self.variant {
                        1 => (v + 0.18 * u + i as f64 * 0.15 - 0.35, u),
                        2 => (u.abs() * 0.72 + v * 0.64 - 0.28, u - v * 0.45),
                        _ => (u + 0.14 * v, v),
                    };
                    let edge = edge
                        + layer.bend * 0.3 * (along * 1.8 + phase).sin()
                        + layer.opening * 0.055;
                    let end = bell((along + (i as f64 - 2.0) * 0.17) / layer.length);
                    let plane = ease(-edge / 0.028) * end;
                    let slit = bell((edge - 0.018) / (0.018 + i as f64 * 0.005));
                    let interrupted = ease((along + 0.8 - i as f64 * 0.13) * 4.0);
                    (plane * 0.78, slit * end * interrupted * 0.7)
                }
                _ => {
                    // Residue: eroded impressions, separated by untouched dark.
                    // Variants: granular islands / dragged strata / broken rims.
                    let ripple = (u * 11.0 + v * 7.0 + phase).sin();
                    let fleck = (u * 27.0 - v * 19.0 - phase * 0.67).sin();
                    let (distance, texture) = match self.variant {
                        1 => (
                            (v + 0.14 * (u * 4.0 + phase).sin()) / layer.width,
                            0.52 + ripple * 0.24 + fleck * 0.16,
                        ),
                        2 => (
                            ((u * u + v * v * 0.56).sqrt() - 0.3 - layer.bend * ripple * 0.22)
                                / (layer.width * 0.38),
                            ease((u + v * 0.3 + 0.1) * 4.0) * (0.6 + ripple * 0.21 + fleck * 0.1),
                        ),
                        _ => (
                            ((u * 1.7).powi(2) + (v * 1.25).powi(2)).sqrt() / (0.3 + layer.width),
                            ease((ripple * 0.62 + fleck * 0.3 + 0.22) * 1.7),
                        ),
                    };
                    let footprint =
                        bell(distance) * bell((u + 0.15) / (layer.length * 0.65)) * bell(v / 1.1);
                    (footprint * texture * 0.62, footprint * texture * 0.12)
                }
            };
            let color = self.colors[i % 3];
            let coverage = (coverage * vignette).clamp(0.0, 0.9);
            for channel in 0..3 {
                // Veils transmit; parallax planes occlude; residue accumulates
                // pigment. Same restrained exposure, different depth semantics.
                let surface = match self.kind {
                    1 => color[channel] * 0.12,
                    2 => color[channel] * 0.64,
                    _ => color[channel] * 0.42,
                };
                rgb[channel] = rgb[channel] * (1.0 - coverage)
                    + surface * coverage * layer.light
                    + color[channel] * highlight * layer.light * vignette;
            }
        }
        // Static sub-byte grain cannot shimmer, jump on seeks, or hide motion.
        let grain = (x * 193.7 + y * 281.9 + self.phase).sin() * 0.38;
        // Lift only the developed light, retaining the quiet near-black floor.
        // This keeps low-chroma partial forms legible on ordinary displays.
        std::array::from_fn(|c| {
            let floor = [5.0, 6.0, 9.0][c];
            floor + (rgb[c] - floor) * 1.85 + grain
        })
    }
}

impl Painter {
    pub(super) fn mystery_frame(&mut self, f: usize, a: AudioFrame) -> &[u8] {
        self.neon_time = f as f64 / FPS as f64;
        self.update_field(f, a);
        let scene = Mystery::new(self, self.neon_time * self.speed);
        let shade = &self.shade;
        let cover = self.cover.is_some();
        let look = look(self.mystery_look);
        parallel_rows(&mut self.rgb, WIDTH * 3, |y, row| {
            // Full-resolution analytic sampling: no upscaled field or mutable
            // trail buffer. A vertical composition leaves the player readable.
            let v = (y as f64 + 0.5 - if cover { 360.0 } else { 470.0 }) / 300.0;
            for (x, pixel) in row.as_chunks_mut::<3>().0.iter_mut().enumerate() {
                let u = (x as f64 + 0.5 - 270.0) / (300.0 * self.spread);
                let sample = scene.sample(u, v);
                let light = look.develop(std::array::from_fn(|c| {
                    (sample[c] - [5.0, 6.0, 9.0][c]).max(0.0)
                }));
                // shade also carries the metadata reservation when no cover is
                // installed yet. Never overwrite it or reuse it as scratch.
                let reservation = shade[y * WIDTH + x] as f64;
                for channel in 0..3 {
                    pixel[channel] = (light[channel] * reservation).clamp(0.0, 255.0) as u8;
                }
            }
        });
        &self.rgb
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn music() -> Vec<AudioFrame> {
        (0..180)
            .map(|f| AudioFrame {
                bands: std::array::from_fn(|b| {
                    0.22 + 0.6 * (f as f64 * 0.034 + b as f64 * 0.17).sin().powi(2)
                }),
                level: 0.7,
                bass: 0.6,
                treble: 0.4,
                ..AudioFrame::default()
            })
            .collect()
    }

    fn difference(a: &[u8], b: &[u8]) -> f64 {
        a.iter()
            .zip(b)
            .map(|(&a, &b)| (a as f64 - b as f64).abs())
            .sum::<f64>()
            / a.len() as f64
    }

    #[test]
    fn every_geometry_supports_dark_and_light_colored_looks() {
        for style in [
            "veil",
            "parallax",
            "residue",
            "silver-mystery",
            "wave-mystery",
            "silk-mystery",
        ] {
            for variant in 0..3 {
                let mut p = Painter::new(style, 72, variant);
                let mut means = Vec::new();
                for index in 0..6 {
                    p.mystery_look = index;
                    let frame = p.frame(90, AudioFrame::default()).to_vec();
                    let patch: Vec<_> = frame.as_chunks::<3>().0.iter().take(WIDTH * 160).collect();
                    let mean = patch
                        .iter()
                        .flat_map(|p| p.iter())
                        .map(|v| *v as f64)
                        .sum::<f64>()
                        / (patch.len() * 3) as f64;
                    assert!(
                        patch
                            .iter()
                            .any(|p| p.iter().max().unwrap() - p.iter().min().unwrap() > 8),
                        "achromatic {style}/{variant}/{index}"
                    );
                    means.push(mean);
                    assert_eq!(frame, p.frame(90, AudioFrame::default()));
                }
                assert!(
                    means[3..].iter().all(|v| *v > 160.0),
                    "missing light {style}: {means:?}"
                );
                assert!(
                    means[..3].iter().all(|v| *v < 150.0),
                    "missing dark {style}: {means:?}"
                );
            }
        }
    }

    #[test]
    fn generation_seeds_select_all_looks_independently_of_geometry() {
        let mut seen = [false; 6];
        for seed in 0..12 {
            for variant in 0..3 {
                let p = Painter::new("veil", seed, variant);
                assert_eq!(p.mystery_look, (seed % 6) as usize);
                assert_eq!(p.variant, variant as f64);
                seen[p.mystery_look] = true;
            }
        }
        assert!(seen.into_iter().all(|look| look));
    }

    #[test]
    fn exact_family_matching() {
        for style in ["veil", "parallax", "residue"] {
            assert!(matches(style));
        }
        for style in ["Veil", "veil-extra", "glass", "", "residues"] {
            assert!(!matches(style));
        }
    }

    #[test]
    fn prepared_and_unprepared_random_access_is_deterministic() {
        let audio = music();
        for style in ["veil", "parallax", "residue"] {
            for prepared in [false, true] {
                let mut p = Painter::new(style, 37, 1);
                if prepared {
                    p.prepare_audio(&audio);
                }
                let expected = p.mystery_frame(120, audio[120]).to_vec();
                p.mystery_frame(12, audio[12]);
                p.mystery_frame(179, audio[179]);
                assert_eq!(expected, p.mystery_frame(120, audio[120]), "{style}");
                assert_eq!(expected.len(), WIDTH * HEIGHT * 3);
            }
        }
    }

    #[test]
    fn each_family_moves_and_responds_to_prepared_music() {
        let audio = music();
        for style in ["veil", "parallax", "residue"] {
            let mut p = Painter::new(style, 37, 0);
            p.prepare_audio(&audio);
            let first = p.mystery_frame(30, audio[30]).to_vec();
            let later = p.mystery_frame(150, audio[150]).to_vec();
            assert!(difference(&first, &later) > 0.3, "{style}: frozen");
            p.prepare_audio(&vec![AudioFrame::default(); audio.len()]);
            let quiet = p.mystery_frame(150, AudioFrame::default());
            assert!(difference(&later, quiet) > 0.3, "{style}: deaf");
        }
    }

    #[test]
    fn families_variants_and_seeds_change_spatial_structure() {
        let audio = music();
        let mut frames = Vec::new();
        for style in ["veil", "parallax", "residue"] {
            for variant in 0..3 {
                let mut p = Painter::new(style, 37, variant);
                p.prepare_audio(&audio);
                frames.push(p.mystery_frame(120, audio[120]).to_vec());
            }
        }
        for i in 0..frames.len() {
            for j in i + 1..frames.len() {
                assert!(difference(&frames[i], &frames[j]) > 1.0, "{i} vs {j}");
                // Compare luminance, so a palette-only distinction cannot pass.
                let spatial = frames[i]
                    .as_chunks::<3>()
                    .0
                    .iter()
                    .zip(frames[j].as_chunks::<3>().0)
                    .map(|(a, b)| {
                        let luma = |p: &[u8]| (p[0] as f64 + p[1] as f64 + p[2] as f64) / 3.0;
                        (luma(a) - luma(b)).abs()
                    })
                    .sum::<f64>()
                    / (WIDTH * HEIGHT) as f64;
                assert!(spatial > 1.0, "{i} vs {j}: same geometry");
            }
        }
        let mut other = Painter::new("veil", 91, 0);
        other.prepare_audio(&audio);
        assert!(difference(&frames[0], other.mystery_frame(120, audio[120])) > 1.0);
    }

    #[test]
    fn preserves_shade_and_dark_metadata_reservation() {
        let mut p = Painter::new("veil", 37, 0);
        p.cover = Some(image::RgbImage::new(1, 1));
        let shade = p.shade.clone();
        let rendered = p.mystery_frame(90, AudioFrame::default()).to_vec();
        assert_eq!(p.shade, shade);
        for y in 585..645 {
            let i = (y * WIDTH + WIDTH / 2) * 3;
            assert!(rendered[i..i + 3].iter().all(|&c| c < 30));
        }
        assert!(rendered.iter().all(|&c| c < 250), "pigment clipped");
    }
}
