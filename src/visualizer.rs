use rand::{Rng, SeedableRng};
use std::f64::consts::PI;
pub const WIDTH: usize = 540;
pub const HEIGHT: usize = 960;
pub const FPS: usize = 30;
pub const BANDS: usize = 64;
pub const STYLES: [&str; 10] = [
    "silver", "violet", "amber", "aurora", "prism", "galaxy", "tunnel", "orbit", "wave", "rain",
];
#[derive(Clone, Copy, Debug)]
pub struct AudioFrame {
    pub bands: [f64; BANDS],
    pub level: f64,
}
impl Default for AudioFrame {
    fn default() -> Self {
        Self {
            bands: [0.0; BANDS],
            level: 0.0,
        }
    }
}
pub fn analyze(samples: &[f64]) -> Vec<AudioFrame> {
    let coefficients: [f64; BANDS] = std::array::from_fn(|b| {
        2.0 * (2.0 * PI * 40.0 * (7000.0_f64 / 40.0).powf(b as f64 / 63.0) / 16000.0).cos()
    });
    let hann: [f64; 1024] =
        std::array::from_fn(|i| 0.5 - 0.5 * (2.0 * PI * i as f64 / 1023.0).cos());
    let mut frames = vec![AudioFrame::default(); 20 * FPS];
    let (mut peak, mut peak_level) = (1e-9_f64, 1e-9_f64);
    for (f, frame) in frames.iter_mut().enumerate() {
        let block: [f64; 1024] = std::array::from_fn(|i| {
            let index = (f * 16000 / FPS + i) as isize - 512;
            if index >= 0 {
                samples.get(index as usize).copied().unwrap_or(0.0) * hann[i]
            } else {
                0.0
            }
        });
        frame.level = (block.iter().map(|s| s * s).sum::<f64>() / 1024.0).sqrt();
        peak_level = peak_level.max(frame.level);
        for (b, c) in coefficients.iter().enumerate() {
            let (mut q1, mut q2) = (0.0, 0.0);
            for sample in block {
                let q0 = sample + c * q1 - q2;
                q2 = q1;
                q1 = q0;
            }
            let magnitude = (q1 * q1 + q2 * q2 - c * q1 * q2).max(0.0).sqrt() / 1024.0;
            frame.bands[b] = magnitude;
            peak = peak.max(magnitude);
        }
    }
    let mut previous = AudioFrame::default();
    for frame in &mut frames {
        frame.level /= peak_level;
        for (b, value) in frame.bands.iter_mut().enumerate() {
            let v = (60.0 * (*value) / peak).ln_1p() / 61.0_f64.ln();
            let weight = if v < previous.bands[b] { 0.2 } else { 0.65 };
            *value = previous.bands[b] + weight * (v - previous.bands[b]);
        }
        previous = *frame;
    }
    frames
}
#[derive(Clone, Copy)]
struct Color(f64, f64, f64);
#[derive(Clone, Copy)]
struct Particle {
    u: f64,
    v: f64,
    depth: f64,
    phase: f64,
}
#[derive(Clone, Copy, Default)]
struct Pose {
    at: f64,
    zoom: f64,
    turn: f64,
    x: f64,
    y: f64,
    unfold: f64,
    color: f64,
    density: f64,
}
#[derive(Clone, Copy)]
struct View {
    xx: f64,
    xy: f64,
    yx: f64,
    yy: f64,
    x: f64,
    y: f64,
    light: f64,
    focus: f64,
}
impl Default for View {
    fn default() -> Self {
        Self {
            xx: 1.0,
            xy: 0.0,
            yx: 0.0,
            yy: 1.0,
            x: 0.0,
            y: 0.0,
            light: 1.0,
            focus: 1.0,
        }
    }
}
impl View {
    fn project(self, x: f64, y: f64) -> (f64, f64) {
        let (x, y) = (x - 270.0, y - 470.0);
        (
            270.0 + self.xx * x + self.xy * y + self.x,
            470.0 + self.yx * x + self.yy * y + self.y,
        )
    }
}
pub struct Painter {
    style: String,
    variant: f64,
    palette: [Color; 3],
    speed: f64,
    spread: f64,
    phase: f64,
    base: Vec<f32>,
    pixels: Vec<f32>,
    glow: Vec<f32>,
    shade: Vec<f32>,
    rgb: Vec<u8>,
    particles: Vec<Particle>,
    timeline: [Pose; 5],
    view: View,
    morph: f64,
}
fn mix_light(a: Color, b: Color, t: f64) -> Color {
    Color(
        a.0 + (b.0 - a.0) * t,
        a.1 + (b.1 - a.1) * t,
        a.2 + (b.2 - a.2) * t,
    )
}
fn band_at(a: AudioFrame, position: f64) -> f64 {
    let x = position.abs() % 1.0 * 63.0;
    let i = x as usize;
    let f = x - i as f64;
    let f = f * f * (3.0 - 2.0 * f);
    a.bands[i] + (a.bands[(i + 1).min(63)] - a.bands[i]) * f
}
fn sin(v: f64) -> f64 {
    v.sin()
}
fn cos(v: f64) -> f64 {
    v.cos()
}
fn abs(v: f64) -> f64 {
    v.abs()
}
fn pow(v: f64, e: f64) -> f64 {
    v.powf(e)
}
fn floor(v: f64) -> f64 {
    v.floor()
}
fn r#mod(v: f64, d: f64) -> f64 {
    v % d
}
#[path = "visualizer_palettes.rs"]
mod palettes;
use palettes::palette;
#[path = "visualizer_scenes.rs"]
mod scenes;
impl Painter {
    pub fn new(style: &str, seed: u64, variant: i32) -> Self {
        let mut r = rand::rngs::StdRng::seed_from_u64(seed);
        let variant = if variant < 0 {
            r.random_range(0..3)
        } else {
            variant as usize
        };
        let mut p = Self {
            style: style.into(),
            variant: variant as f64,
            palette: palette(style, variant),
            speed: 0.75 + r.random::<f64>() * 0.5,
            spread: 0.88 + r.random::<f64>() * 0.24,
            phase: r.random::<f64>() * 2.0 * PI,
            base: vec![0.0; WIDTH * HEIGHT * 3],
            pixels: vec![0.0; WIDTH * HEIGHT * 3],
            glow: vec![0.0; WIDTH / 4 * HEIGHT / 4 * 3],
            shade: vec![0.0; WIDTH * HEIGHT],
            rgb: vec![0; WIDTH * HEIGHT * 3],
            particles: (0..1800)
                .map(|_| Particle {
                    u: r.random(),
                    v: r.random(),
                    depth: r.random(),
                    phase: r.random::<f64>() * 2.0 * PI,
                })
                .collect(),
            timeline: [Pose::default(); 5],
            view: View::default(),
            morph: 0.0,
        };
        for (i, opening) in [0.2, 0.65, 0.9, 0.4, 0.75].into_iter().enumerate() {
            let mut at = i as f64 / 4.0;
            if i > 0 && i < 4 {
                at += (r.random::<f64>() - 0.5) * 0.1;
            }
            p.timeline[i] = Pose {
                at,
                zoom: 0.86 + r.random::<f64>() * 0.28,
                turn: (r.random::<f64>() - 0.5) * 0.28,
                x: (r.random::<f64>() - 0.5) * 65.0,
                y: (r.random::<f64>() - 0.5) * 80.0,
                unfold: opening + (r.random::<f64>() - 0.5) * 0.18,
                color: r.random::<f64>() * 0.6,
                density: 0.3 + opening * 0.55 + r.random::<f64>() * 0.15,
            };
        }
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let d = ((x as f64 - 270.0) / 430.0).hypot((y as f64 - 480.0) / 700.0);
                let v = (1.0 - d).max(0.0);
                let c = mix_light(Color(2.0, 3.0, 5.0), p.palette[0], 0.012 + 0.025 * v);
                let i = (y * WIDTH + x) * 3;
                let (dx, dy) = ((x as f64 - 270.0) / 205.0, (y as f64 - 620.0) / 95.0);
                p.shade[i / 3] = (1.0 - 0.87 * (-dx.powi(4) - dy.powi(4)).exp()) as f32;
                p.base[i] = c.0 as f32;
                p.base[i + 1] = c.1 as f32;
                p.base[i + 2] = c.2 as f32;
            }
        }
        p
    }
    pub fn accent(&self) -> String {
        format!(
            "0x{:02x}{:02x}{:02x}",
            self.palette[0].0 as u8, self.palette[0].1 as u8, self.palette[0].2 as u8
        )
    }
    fn point(&mut self, x: f64, y: f64, c: Color, intensity: f64) {
        let (x, y) = self.view.project(x, y);
        self.light_point(x, y, c, intensity * self.view.light);
    }
    fn light_point(&mut self, x: f64, y: f64, c: Color, intensity: f64) {
        if x < -16.0 || y < -16.0 || x >= WIDTH as f64 + 16.0 || y >= HEIGHT as f64 + 16.0 {
            return;
        }
        let (ix, iy) = (x.floor() as i32, y.floor() as i32);
        let (fx, fy) = (x - ix as f64, y - iy as f64);
        for dy in 0..=1 {
            for dx in 0..=1 {
                let (xx, yy) = (ix + dx, iy + dy);
                if xx < 0 || xx >= WIDTH as i32 || yy < 0 || yy >= HEIGHT as i32 {
                    continue;
                }
                let a = (if dx == 1 { fx } else { 1.0 - fx })
                    * (if dy == 1 { fy } else { 1.0 - fy })
                    * intensity
                    * self.view.focus;
                let i = (yy as usize * WIDTH + xx as usize) * 3;
                for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                    self.pixels[i + j] += (v * a) as f32;
                }
            }
        }
        let (gx, gy) = ((x / 4.0) as i32, (y / 4.0) as i32);
        for dy in -3..=3 {
            for dx in -3..=3 {
                let (xx, yy) = (gx + dx, gy + dy);
                if xx < 0 || xx >= WIDTH as i32 / 4 || yy < 0 || yy >= HEIGHT as i32 / 4 {
                    continue;
                }
                let a = intensity * 0.025 / (1 + dx * dx + dy * dy) as f64;
                let i = (yy as usize * (WIDTH / 4) + xx as usize) * 3;
                for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                    self.glow[i + j] += (v * a) as f32;
                }
            }
        }
    }
    fn line(&mut self, x0: f64, y0: f64, x1: f64, y1: f64, c: Color, intensity: f64) {
        let (x0, y0) = self.view.project(x0, y0);
        let (x1, y1) = self.view.project(x1, y1);
        if x0.max(x1) < -16.0
            || x0.min(x1) >= WIDTH as f64 + 16.0
            || y0.max(y1) < -16.0
            || y0.min(y1) >= HEIGHT as f64 + 16.0
        {
            return;
        }
        let n = (x1 - x0).hypot(y1 - y0).ceil().max(1.0) as usize;
        for i in 0..=n {
            let t = i as f64 / n as f64;
            self.light_point(
                x0 + (x1 - x0) * t,
                y0 + (y1 - y0) * t,
                c,
                intensity * self.view.light,
            );
        }
    }
    fn evolution(&self, progress: f64) -> Pose {
        let progress = progress.clamp(0.0, 1.0);
        for i in 1..5 {
            let b = self.timeline[i];
            if progress > b.at {
                continue;
            }
            let a = self.timeline[i - 1];
            let f = (progress - a.at) / (b.at - a.at);
            let f = f * f * (3.0 - 2.0 * f);
            let lerp = |a: f64, b: f64| a + (b - a) * f;
            return Pose {
                at: progress,
                zoom: lerp(a.zoom, b.zoom),
                turn: lerp(a.turn, b.turn),
                x: lerp(a.x, b.x),
                y: lerp(a.y, b.y),
                unfold: lerp(a.unfold, b.unfold),
                color: lerp(a.color, b.color),
                density: lerp(a.density, b.density),
            };
        }
        self.timeline[4]
    }
    fn scene(&mut self, t: f64, a: AudioFrame) {
        match self.style.as_str() {
            "silver" => self.silver(t, a),
            "violet" => self.violet(t, a),
            "amber" => self.amber(t, a),
            "aurora" => self.aurora(t, a),
            "prism" => self.prism(t, a),
            "galaxy" => self.galaxy(t, a),
            "tunnel" => self.tunnel(t, a),
            "orbit" => self.orbit(t, a),
            "wave" => self.wave(t, a),
            "rain" => self.rain(t, a),
            _ => unreachable!(),
        }
    }
    fn depth_layers(&mut self, seconds: f64, pose: Pose, a: AudioFrame) {
        let (palette, phase, spread) = (self.palette, self.phase, self.spread);
        for layer in 0..3 {
            let depth = 0.35 + layer as f64 * 0.325;
            let angle = pose.turn * depth + (1.0 - depth) * 0.2 * (phase + seconds * 0.12).sin();
            let scale = (0.68 + layer as f64 * 0.2) * pose.zoom * (1.0 + a.level * 0.015 * depth);
            let (sin, cos) = angle.sin_cos();
            self.view = View {
                xx: scale * cos,
                xy: -scale * sin,
                yx: scale * sin,
                yy: scale * cos * (0.9 + 0.1 * depth),
                x: -pose.x * depth + (1.0 - depth) * 65.0 * (phase + seconds * 0.17).sin(),
                y: -pose.y * depth
                    + (1.0 - depth) * (55.0 * (phase + seconds * 0.13).cos() - 160.0),
                light: [0.5, 0.4, 0.82][layer] * (0.8 + 0.3 * pose.density),
                focus: [0.4, 0.7, 1.0][layer],
            };
            for i in 0..3 {
                self.palette[i] = mix_light(
                    palette[i],
                    palette[(i + 1) % 3],
                    pose.color * 0.65 + (1.0 - depth) * 0.2,
                );
            }
            self.phase = phase + (1.0 - depth) * 1.4 + pose.unfold * 0.4;
            self.spread = spread * (0.85 + 0.3 * pose.unfold);
            self.morph = pose.unfold;
            self.scene(
                seconds * self.speed * (0.65 + 0.35 * depth) + (1.0 - depth) * 3.0,
                a,
            );
        }
        self.palette = palette;
        self.phase = phase;
        self.spread = spread;
        self.view = View::default();
    }
    fn atmosphere(&mut self, seconds: f64, pose: Pose, a: AudioFrame) {
        let (w, h) = (WIDTH / 4, HEIGHT / 4);
        for cloud in 0..3 {
            let phase = self.phase + cloud as f64 * 2.1;
            let cx = 67.0 + 48.0 * (phase + seconds * 0.08).sin() - pose.x * 0.12;
            let cy = 120.0 + 95.0 * (phase + seconds * 0.06).cos() - pose.y * 0.12;
            let (sx, sy) = (32.0 + 15.0 * pose.unfold, 55.0 + 25.0 * phase.sin().powi(2));
            let c = mix_light(
                self.palette[cloud],
                self.palette[(cloud + 1) % 3],
                pose.color,
            );
            let strength =
                (0.04 + 0.04 * pose.density) * (0.65 + 0.35 * band_at(a, cloud as f64 / 3.0));
            let horizontal: Vec<_> = (0..w)
                .map(|x| (-((x as f64 - cx) / sx).powi(2)).exp())
                .collect();
            for y in 0..h {
                let vertical = strength * (-((y as f64 - cy) / sy).powi(2)).exp();
                for (x, horizontal) in horizontal.iter().enumerate() {
                    let light = vertical * horizontal;
                    let i = (y * w + x) * 3;
                    for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                        self.glow[i + j] += (v * light) as f32;
                    }
                }
            }
        }
    }
    fn depth_particles(&mut self, seconds: f64, pose: Pose, a: AudioFrame) {
        for i in 0..360 {
            let s = self.particles[i];
            let z = (s.depth + seconds * (0.015 + 0.01 * s.u)) % 1.0;
            let perspective = 0.35 + z * z * 1.9;
            let angle = self.phase + s.phase + seconds * 0.035;
            let x = 270.0 + (s.u - 0.5) * 650.0 * perspective - pose.x * perspective
                + 12.0 * angle.sin();
            let y = 470.0 + (s.v - 0.5) * 1050.0 * perspective - pose.y * perspective
                + 20.0 * angle.cos();
            let visibility = (z * PI).sin() * ((pose.density - s.v) * 3.0 + 0.5).clamp(0.0, 1.0);
            let brightness = visibility * (0.15 + 0.65 * band_at(a, s.u)) * (0.25 + z * 0.6);
            let c = mix_light(self.palette[i % 3], self.palette[(i + 1) % 3], pose.color);
            self.view.focus = 1.0 - 0.8 * z * z;
            self.point(x, y, c, brightness);
            if z > 0.65 {
                let trail = ((z - 0.65) / 0.15).min(1.0);
                let (dx, dy) = (
                    (x - 270.0) * 0.012 * perspective,
                    (y - 470.0) * 0.012 * perspective,
                );
                self.line(x - dx, y - dy, x, y, c, brightness * 0.35 * trail);
                self.line(x - 2.0 * z, y, x + 2.0 * z, y, c, brightness * 0.35 * trail);
                if i % 4 == 0 {
                    let radius = 2.0 + 5.0 * z * z;
                    let (x, y) = (x / 4.0, y / 4.0);
                    for yy in
                        (y - radius).max(0.0) as usize..=((y + radius) as usize).min(HEIGHT / 4 - 1)
                    {
                        for xx in (x - radius).max(0.0) as usize
                            ..=((x + radius) as usize).min(WIDTH / 4 - 1)
                        {
                            let (dx, dy) = ((xx as f64 - x) / radius, (yy as f64 - y) / radius);
                            let light = brightness
                                * 0.2
                                * trail
                                * (1.0 - dx * dx - dy * dy).max(0.0).powi(2);
                            let idx = (yy * (WIDTH / 4) + xx) * 3;
                            for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                                self.glow[idx + j] += (v * light) as f32;
                            }
                        }
                    }
                }
            }
        }
        self.view.focus = 1.0;
    }
    pub fn frame(&mut self, f: usize, a: AudioFrame) -> &[u8] {
        self.pixels.copy_from_slice(&self.base);
        self.glow.fill(0.0);
        let seconds = f as f64 / FPS as f64;
        let pose = self.evolution(f as f64 / 599.0);
        self.atmosphere(seconds, pose, a);
        self.depth_layers(seconds, pose, a);
        self.depth_particles(seconds, pose, a);
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let i = (y * WIDTH + x) * 3;
                let gi = ((y / 4) * (WIDTH / 4) + x / 4) * 3;
                for c in 0..3 {
                    self.rgb[i + c] = ((self.pixels[i + c] + self.glow[gi + c]) * self.shade[i / 3])
                        .clamp(0.0, 255.0) as u8;
                }
            }
        }
        &self.rgb
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn silence_is_finite() {
        let frames = analyze(&vec![0.0; 320000]);
        assert_eq!(frames.len(), 600);
        assert!(
            frames
                .iter()
                .all(|f| f.level == 0.0 && f.bands.iter().all(|v| *v == 0.0))
        );
    }
    #[test]
    fn sine_has_energy() {
        let samples: Vec<_> = (0..320000)
            .map(|i| (2.0 * PI * 440.0 * i as f64 / 16000.0).sin())
            .collect();
        let f = analyze(&samples);
        assert!(f[30].level > 0.9);
        assert!(f[30].bands.iter().any(|v| *v > 0.7));
    }
    #[test]
    fn every_style_and_variant_renders_and_evolves() {
        for style in STYLES {
            for v in 0..3 {
                let mut p = Painter::new(style, 42, v);
                let a = AudioFrame {
                    level: 0.5,
                    bands: [0.3; 64],
                };
                let first = p.frame(0, a).to_vec();
                assert_eq!(first.len(), WIDTH * HEIGHT * 3);
                assert_ne!(first, p.frame(300, a));
                assert_eq!(first, p.frame(0, a));
            }
        }
    }
}
