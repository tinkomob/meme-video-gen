use rand::{Rng, SeedableRng};
use std::f64::consts::PI;
pub const WIDTH: usize = 540;
pub const HEIGHT: usize = 960;
pub const FPS: usize = 30;
pub const BANDS: usize = 64;
pub const STYLES: [&str; 25] = [
    "silver",
    "violet",
    "amber",
    "aurora",
    "prism",
    "galaxy",
    "tunnel",
    "orbit",
    "wave",
    "rain",
    "chrome",
    "silk",
    "analog",
    "cover",
    "nebula",
    "liquid",
    "bloom",
    "ferro",
    "cymatics",
    "topography",
    "glass",
    "ink",
    "kinetic",
    "lightning",
    "interference",
];
#[derive(Clone, Copy, Debug)]
pub struct AudioFrame {
    pub bands: [f64; BANDS],
    pub level: f64,
    pub bass: f64,
    pub treble: f64,
    pub beat: f64,
    pub event: u64,
    pub event_age: f64,
    /// Low, mid and high attacks, measured before visual spectrum compression.
    pub attacks: [f64; 3],
    /// Seconds since the latest attack in each frequency region.
    pub attack_age: [f64; 3],
}
impl Default for AudioFrame {
    fn default() -> Self {
        Self {
            bands: [0.0; BANDS],
            level: 0.0,
            bass: 0.0,
            treble: 0.0,
            beat: 0.0,
            event: 0,
            event_age: 0.0,
            attacks: [0.0; 3],
            attack_age: [10.0; 3],
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
    let mut previous_spectrum = [0.0; BANDS];
    let mut average_flux = [0.0_f64; 3];
    let (mut event, mut since_event) = (0, FPS);
    for frame in &mut frames {
        frame.level /= peak_level;
        let mut rises = [0.0; 3];
        for (b, value) in frame.bands.iter_mut().enumerate() {
            let magnitude = *value / peak;
            let v = (60.0 * (*value) / peak).ln_1p() / 61.0_f64.ln();
            let region = if b < 16 {
                0
            } else if b < 40 {
                1
            } else {
                2
            };
            rises[region] += (magnitude - previous_spectrum[b]).max(0.0).powi(2);
            previous_spectrum[b] = magnitude;
            let weight = if v < previous.bands[b] { 0.2 } else { 0.65 };
            *value = previous.bands[b] + weight * (v - previous.bands[b]);
        }
        for region in 0..3 {
            let rise = (rises[region] / [16.0, 24.0, 24.0][region]).sqrt();
            let age = previous.attack_age[region] + 1.0 / FPS as f64;
            if rise > (average_flux[region] * 1.8 + 0.012) && age >= 0.1 {
                frame.attacks[region] = (rise / (average_flux[region] * 3.0 + 0.06)).min(1.0);
                frame.attack_age[region] = 0.0;
            } else {
                frame.attacks[region] = previous.attacks[region] * 0.97;
                frame.attack_age[region] = age;
            }
            average_flux[region] += (rise - average_flux[region]) * 0.08;
        }
        frame.bass = frame.bands[..16].iter().sum::<f64>() / 16.0;
        frame.treble = frame.bands[40..].iter().sum::<f64>() / 24.0;
        let flux = frame
            .bands
            .iter()
            .zip(previous.bands)
            .map(|(now, before)| (now - before).max(0.0))
            .sum::<f64>()
            / BANDS as f64;
        let onset = ((frame.bass - previous.bass).max(0.0) * 5.0
            + (frame.level - previous.level).max(0.0) * 1.5
            + flux * 6.0)
            .clamp(0.0, 1.0);
        since_event += 1;
        if onset > 0.12 && since_event >= FPS / 6 {
            event += 1;
            since_event = 0;
        }
        frame.beat = onset.max(previous.beat * 0.78);
        frame.event = event;
        frame.event_age = since_event as f64 / FPS as f64;
        previous = *frame;
    }
    frames
}
pub fn smooth_material_audio(painter: &Painter, mut frames: Vec<AudioFrame>) -> Vec<AudioFrame> {
    if materials::spatial(&painter.style) {
        let mut previous = AudioFrame::default();
        for frame in &mut frames {
            for (value, old) in frame.bands.iter_mut().zip(previous.bands) {
                *value = old + (*value - old) * 0.06;
            }
            frame.level = previous.level + (frame.level - previous.level) * 0.06;
            frame.bass = frame.bands[..16].iter().sum::<f64>() / 16.0;
            frame.treble = frame.bands[40..].iter().sum::<f64>() / 24.0;
            frame.beat = previous.beat + (frame.beat - previous.beat) * 0.06;
            for region in 0..3 {
                let target = frame.attacks[region] * (-frame.attack_age[region] * 6.0).exp();
                frame.attacks[region] =
                    previous.attacks[region] + (target - previous.attacks[region]) * 0.06;
                frame.attack_age[region] = 0.0;
            }
            previous = *frame;
        }
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
    cover: Option<image::RgbImage>,
    cover_soft: Option<image::RgbImage>,
    view: View,
    morph: f64,
    neon: bool,
    neon_time: f64,
    responses: Vec<[motion::Response; 12]>,
    rhythm: Vec<rhythm::Rhythm>,
    response: [motion::Response; 12],
    field: Vec<motion::Field>,
}
fn mix_light(a: Color, b: Color, t: f64) -> Color {
    Color(
        a.0 + (b.0 - a.0) * t,
        a.1 + (b.1 - a.1) * t,
        a.2 + (b.2 - a.2) * t,
    )
}
fn audio_wave(a: AudioFrame, region: usize, distance: f64) -> f64 {
    // shortcut: one wave per region; retain overlapping waves if dense percussion clips tails.
    let age = a.attack_age[region];
    let front = distance - age * 2.8;
    if a.attacks[region] == 0.0 || front.abs() > 1.0 {
        return 0.0;
    }
    a.attacks[region] * (-age * 2.0 - front * front * 18.0).exp() * (front * 13.0).cos()
}
fn band_at(a: &AudioFrame, position: f64) -> f64 {
    let position = position.rem_euclid(1.0);
    // Each active region contains the entire spectrum, including sustained notes.
    let response = |event: u64| {
        let mut strength = 0.0_f64;
        for part in 0..1 + event % 3 {
            let bits = event
                .wrapping_add(part * 0x9e3779b9)
                .wrapping_mul(0x517cc1b727220a95);
            let center = (bits ^ (bits >> 32)) as u32 as f64 / u32::MAX as f64;
            let offset = (position - center + 0.5).rem_euclid(1.0) - 0.5;
            let window = (1.0 - offset.abs() / 0.065).clamp(0.0, 1.0);
            let gate = window * window * (3.0 - 2.0 * window);
            if gate == 0.0 {
                continue;
            }
            let x = ((offset / 0.13 + 0.5) * 63.0).clamp(0.0, 63.0);
            let i = x as usize;
            let energy = a.bands[i] + (a.bands[(i + 1).min(63)] - a.bands[i]) * (x - i as f64);
            strength =
                strength.max(gate * (energy * 0.65 + a.level * 0.25 + a.beat * 0.45).min(1.0));
        }
        strength
    };
    let fade = (a.event_age / 0.14).clamp(0.0, 1.0);
    response(a.event.wrapping_sub(1)) * (1.0 - fade) + response(a.event) * fade
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
#[path = "visualizer_rhythm.rs"]
mod rhythm;
use palettes::palette;
#[path = "visualizer_art.rs"]
mod art;
#[path = "visualizer_materials.rs"]
mod materials;
#[path = "visualizer_motion.rs"]
mod motion;
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
            palette: palette(
                STYLES[r.random_range(0..STYLES.len())],
                r.random_range(0..3),
            ),
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
            cover: None,
            cover_soft: None,
            view: View::default(),
            morph: 0.0,
            neon: !seed.is_multiple_of(4),
            neon_time: 0.0,
            responses: Vec::new(),
            rhythm: Vec::new(),
            response: [motion::Response::default(); 12],
            field: vec![motion::Field::default(); 19 * 33],
        };
        let hue = r.random::<f64>();
        let background = mix_light(motion::hue_color(hue), p.palette[1], 0.18);
        let background_light = [0.16, 0.29, 0.43][r.random_range(0..3)];
        for i in 0..3 {
            p.palette[i] = mix_light(
                p.palette[i],
                motion::hue_color(hue + [0.0, 0.09, 0.48][i]),
                0.55,
            );
        }
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let d = ((x as f64 - 270.0) / 430.0).hypot((y as f64 - 480.0) / 700.0);
                let v = (1.0 - d).max(0.0);
                let sweep = (x as f64 / WIDTH as f64 * 0.6 + y as f64 / HEIGHT as f64 * 0.4)
                    .clamp(0.0, 1.0);
                let pigment = mix_light(background, p.palette[1], sweep * 0.22);
                let c = mix_light(
                    Color(8.0, 9.0, 12.0),
                    pigment,
                    background_light * (0.65 + 0.35 * v),
                );
                let i = (y * WIDTH + x) * 3;
                let (dx, dy) = ((x as f64 - 270.0) / 250.0, (y as f64 - 615.0) / 100.0);
                let falloff = if materials::spatial(style) {
                    -dx * dx - dy * dy
                } else {
                    -dx.powi(4) - dy.powi(4)
                };
                p.shade[i / 3] = (1.0 - 0.82 * falloff.exp()) as f32;
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
        let field = self.field_at(x, y);
        let c = self.local_color(c, field, 1.0 - self.view.focus);
        let (x, y) = (
            x + field.dx * self.view.focus,
            y + field.dy * self.view.focus,
        );
        let light_front = (0.5 + 0.5 * (x * 0.012 + y * 0.008 - field.travel * 2.0).sin()).powi(3);
        let intensity = intensity
            * (1.15 + field.opening * 0.35 + field.light * (0.2 + 0.8 * light_front) * 3.5);
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
        let neon = self.neon_strength(x, y);
        let defocus = (1.0 - self.view.focus).clamp(0.0, 1.0);
        if neon == 0.0 && defocus == 0.0 {
            return;
        }
        let (gx, gy) = ((x / 4.0).floor() as i32, (y / 4.0).floor() as i32);
        let radius = if neon > 0.0 { 3 } else { 1 };
        let count = 2 * radius + 2;
        let axis = |fraction: f64, support: f64| {
            let mut weights: [f64; 8] = std::array::from_fn(|i| {
                if i >= count {
                    return 0.0;
                }
                let distance = (i as f64 - radius as f64 - fraction) / support;
                (1.0 - distance * distance).max(0.0).powi(2)
            });
            let sum = weights.iter().sum::<f64>();
            for weight in &mut weights {
                *weight /= sum;
            }
            weights
        };
        let (fx, fy) = (x / 4.0 - gx as f64, y / 4.0 - gy as f64);
        let (neon_x, neon_y) = (axis(fx, 3.5), axis(fy, 3.5));
        let (soft_x, soft_y) = (axis(fx, 1.5), axis(fy, 1.5));
        // Preserve the old kernels' total light while their edges fade continuously.
        let neon_energy = neon * 0.11 * 8.820338725601882;
        let soft_energy = defocus * 0.018 * (13.0 / 3.0);
        for dy in 0..count {
            for dx in 0..count {
                let (xx, yy) = (
                    gx + dx as i32 - radius as i32,
                    gy + dy as i32 - radius as i32,
                );
                if xx < 0 || xx >= WIDTH as i32 / 4 || yy < 0 || yy >= HEIGHT as i32 / 4 {
                    continue;
                }
                let a = intensity
                    * (neon_energy * neon_x[dx] * neon_y[dy]
                        + soft_energy * soft_x[dx] * soft_y[dy]);
                if a == 0.0 {
                    continue;
                }
                let i = (yy as usize * (WIDTH / 4) + xx as usize) * 3;
                for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                    self.glow[i + j] += (v * a) as f32;
                }
            }
        }
    }
    fn neon_strength(&self, x: f64, y: f64) -> f64 {
        if !self.neon {
            return 0.0;
        }
        let t = self.neon_time;
        let envelope = ((t * 0.73 + self.phase).sin() - 0.15).max(0.0);
        let cx = 270.0 + 240.0 * (t * 0.39 + self.phase).sin();
        let cy = 480.0 + 420.0 * (t * 0.31 - self.phase).cos();
        let distance = ((x - cx) / 170.0).hypot((y - cy) / 250.0);
        envelope * (1.0 - distance).max(0.0).powi(2)
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
        let length = (x1 - x0).hypot(y1 - y0);
        let n = (length * 2.0).ceil().max(1.0) as usize;
        // Integrate coverage without drawing shared segment endpoints twice.
        for i in 0..n {
            let t = (i as f64 + 0.5) / n as f64;
            self.light_point(
                x0 + (x1 - x0) * t,
                y0 + (y1 - y0) * t,
                c,
                intensity * self.view.light * length / n as f64,
            );
        }
    }
    fn evolution(&self, seconds: f64) -> Pose {
        let t = seconds * (0.65 + self.speed * 0.12) + self.variant * 0.7;
        let breath = (t + self.phase).sin();
        Pose {
            zoom: 1.0 + breath * 0.035,
            turn: (t * 0.37 + self.phase).sin() * 0.12,
            x: (t * 0.29 + self.phase).sin() * 22.0,
            y: (t * 0.23 - self.phase).cos() * 28.0,
            unfold: 0.45 + breath * 0.12 + (t * 0.71).sin() * 0.05,
            color: 0.18 + (t * 0.19 + self.phase).sin() * 0.1,
            density: 0.55 + (t * 0.83 - self.phase).sin() * 0.08,
        }
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
            "chrome" => self.chrome(t, a),
            "silk" => self.silk(t, a),
            "analog" => self.analog(t, a),
            _ => unreachable!(),
        }
    }
    fn depth_layers(&mut self, seconds: f64, pose: Pose, a: AudioFrame) {
        let (palette, phase, spread) = (self.palette, self.phase, self.spread);
        for layer in 0..3 {
            let depth = 0.35 + layer as f64 * 0.325;
            let angle = pose.turn * depth * 0.15;
            let bounded = matches!(self.style.as_str(), "violet" | "prism" | "orbit");
            let scale = if bounded {
                [0.85, 0.95, 1.02][layer]
            } else {
                [1.65, 1.4, 1.2][layer]
            } * (0.98 + (pose.zoom - 1.0) * 0.2);
            let (sin, cos) = angle.sin_cos();
            self.view = View {
                xx: scale * cos,
                xy: -scale * sin,
                yx: scale * sin,
                yy: scale * cos * (0.9 + 0.1 * depth),
                x: -pose.x * depth + (1.0 - depth) * 65.0 * (phase + seconds * 0.17).sin(),
                y: -pose.y * depth * 0.35
                    + if bounded {
                        0.0
                    } else {
                        [-650.0, -250.0, 150.0][layer]
                    },
                light: [0.28, 0.45, 0.9][layer] * (0.7 + 0.4 * pose.density),
                focus: [0.25, 0.65, 1.0][layer],
            };
            for i in 0..3 {
                self.palette[i] = mix_light(
                    palette[i],
                    palette[(i + 1) % 3],
                    pose.color * 0.65 + (1.0 - depth) * 0.2,
                );
            }
            self.phase = phase + (1.0 - depth) * 1.4 + pose.unfold * 0.4;
            self.spread = spread * (0.85 + 0.3 * pose.unfold + 0.04 * self.response[layer].opening);
            self.morph = (pose.unfold + self.response[layer + 3].opening * 0.2).min(1.0);
            self.scene(
                self.response[layer].travel * self.speed * (0.65 + 0.35 * depth) * 2.0
                    + (1.0 - depth) * 3.0,
                a,
            );
        }
        self.palette = palette;
        self.phase = phase;
        self.spread = spread;
        self.view = View::default();
    }
    fn atmosphere(&mut self, _seconds: f64, pose: Pose) {
        let (w, h) = (WIDTH / 4, HEIGHT / 4);
        for cloud in 0..4 {
            let p = self.particles[cloud + 8];
            let r = self.response[cloud + 8];
            let time = r.travel * (0.22 + p.u * 0.15);
            let cx = 30.0 + p.u * 75.0 + 15.0 * (time + p.phase).sin();
            let cy = 20.0 + cloud as f64 * 65.0 + 18.0 * (time * 0.71 - p.phase).sin();
            for y in 0..h {
                for x in 0..w {
                    let local = self.field_at(x as f64 * 4.0, y as f64 * 4.0);
                    let dx = (x as f64 - cx + local.dx * 0.08) / (34.0 + p.depth * 22.0);
                    let dy = (y as f64 - cy + local.dy * 0.08) / 70.0;
                    let fold = (dx * 3.7 + dy * 2.1 + p.phase).sin() * (dy * 3.1 - time).cos();
                    let veil = (-dx * dx - dy * dy).exp()
                        * (0.3 + 0.7 * (fold * 2.0 + local.opening).sin().powi(2));
                    // Integrate thin ridges over each low-resolution pixel in every style.
                    let samples = 4;
                    let mut caustic = 0.0;
                    for sy in 0..samples {
                        for sx in 0..samples {
                            let dx = dx
                                + ((sx as f64 + 0.5) / samples as f64 - 0.5)
                                    / (34.0 + p.depth * 22.0);
                            let dy = dy + ((sy as f64 + 0.5) / samples as f64 - 0.5) / 70.0;
                            let fold =
                                (dx * 3.7 + dy * 2.1 + p.phase).sin() * (dy * 3.1 - time).cos();
                            let fine = (dx * 9.0 + dy * 6.0 + fold * 3.0 - time * 0.7).sin();
                            caustic += (1.0 - fine.abs()).powi(12) / (samples * samples) as f64;
                        }
                    }
                    let light = if matches!(self.style.as_str(), "cover" | "collage") {
                        (-dx * dx - dy * dy).exp() * (0.09 + 0.04 * r.energy)
                    } else {
                        veil * (0.08 + 0.06 * pose.density + 0.04 * r.opening)
                            * (0.7 + caustic * 1.6)
                    };
                    let c = self.local_color(self.palette[cloud % 3], local, p.depth);
                    let i = (y * w + x) * 3;
                    for (j, v) in [c.0, c.1, c.2].iter().enumerate() {
                        self.glow[i + j] += (v * light) as f32;
                    }
                }
            }
        }
    }
    fn depth_particles(
        &mut self,
        seconds: f64,
        pose: Pose,
        a: AudioFrame,
        depths: std::ops::Range<f64>,
    ) {
        for i in 0..720 {
            let s = self.particles[i];
            let z = (s.depth + seconds * (0.015 + 0.01 * s.u)) % 1.0;
            if !depths.contains(&z) {
                continue;
            }
            let perspective = 0.35 + z * z * 1.9;
            let angle =
                self.phase + s.phase + 0.4 * (seconds * (0.07 + s.u * 0.12) + s.phase).sin();
            let x = 270.0 + (s.u - 0.5) * 650.0 * perspective - pose.x * perspective
                + 12.0 * angle.sin();
            let y = 470.0 + (s.v - 0.5) * 1050.0 * perspective - pose.y * perspective
                + 20.0 * angle.cos();
            let visibility = (z * PI).sin() * ((pose.density - s.v) * 3.0 + 0.5).clamp(0.0, 1.0);
            let brightness = visibility * (0.15 + 0.65 * band_at(&a, s.u)) * (0.25 + z * 0.6);
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
    pub fn frame(&mut self, f: usize, mut a: AudioFrame) -> &[u8] {
        let material = materials::spatial(&self.style);
        a.event = a.event.wrapping_add(self.phase.to_bits());
        self.pixels.copy_from_slice(&self.base);
        self.glow.fill(0.0);
        let seconds = f as f64 / FPS as f64;
        let pose = self.evolution(seconds);
        self.neon_time = seconds;
        self.update_field(f, a);
        let pose = self.musical_pose(f, pose);
        let material_audio = a;
        a = self.shape_audio(a);
        if matches!(self.style.as_str(), "collage" | "cover") {
            self.cover_scene(seconds, pose, a);
        } else if material {
            self.material(seconds * self.speed, pose, material_audio);
        } else {
            if matches!(self.style.as_str(), "nebula" | "liquid" | "bloom") {
                self.depth_particles(seconds, pose, a, 0.0..0.65);
                self.volume(seconds * self.speed, pose, a);
                self.depth_particles(seconds, pose, a, 0.65..1.0);
            } else if self.style == "chrome" {
                self.depth_particles(seconds, pose, a, 0.0..0.65);
                self.morph = pose.unfold;
                let spread = self.spread;
                self.spread *= pose.zoom;
                self.chrome(self.response[0].travel * self.speed * 2.0, a);
                self.spread = spread;
                self.depth_particles(seconds, pose, a, 0.65..1.0);
            } else {
                self.depth_layers(seconds, pose, a);
            }
            if !matches!(
                self.style.as_str(),
                "analog" | "chrome" | "nebula" | "liquid" | "bloom"
            ) {
                self.depth_particles(seconds, pose, a, 0.0..1.0);
                self.accents(seconds, a);
            }
        }
        if !material {
            self.atmosphere(seconds, pose);
        }
        {
            let glow = image::Rgb32FImage::from_raw(
                (WIDTH / 4) as u32,
                (HEIGHT / 4) as u32,
                std::mem::take(&mut self.glow),
            )
            .unwrap();
            self.glow = image::imageops::blur(&glow, 0.7).into_raw();
        }
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let i = (y * WIDTH + x) * 3;
                let gi = ((y / 4) * (WIDTH / 4) + x / 4) * 3;
                let right = if x / 4 + 1 < WIDTH / 4 { 3 } else { 0 };
                let below = if y / 4 + 1 < HEIGHT / 4 {
                    WIDTH / 4 * 3
                } else {
                    0
                };
                let (fx, fy) = ((x % 4) as f32 * 0.25, (y % 4) as f32 * 0.25);
                let light: [f32; 3] = std::array::from_fn(|c| {
                    let halo = self.glow[gi + c] * (1.0 - fx) * (1.0 - fy)
                        + self.glow[gi + right + c] * fx * (1.0 - fy)
                        + self.glow[gi + below + c] * (1.0 - fx) * fy
                        + self.glow[gi + below + right + c] * fx * fy;
                    ((self.pixels[i + c] + halo) * if material { 1.0 } else { self.shade[i / 3] })
                        .max(0.0)
                });
                // One exposure for all channels preserves pigment hues in bright light.
                let peak = light.into_iter().fold(0.0_f32, f32::max);
                let scrim = if material {
                    (1.0 - self.shade[i / 3]) / 30.0
                } else {
                    0.0
                };
                let exposure = 1.8 / (1.0 + peak / 320.0 + peak * scrim);
                for (c, value) in light.into_iter().enumerate() {
                    let mut value = value * exposure;
                    if self.style == "analog" {
                        let noise = ((x * 17 + y * 31 + f * 13) % 11) as f32 * 0.22;
                        value = value * (if y % 3 == 0 { 0.72 } else { 1.0 }) + noise;
                    }
                    self.rgb[i + c] = value.clamp(0.0, 255.0) as u8;
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
    fn chrome_keeps_an_open_center_and_uses_neutral_reflections() {
        for variant in 0..3 {
            let mut p = Painter::new("chrome", 71, variant);
            for time in [0.0, 4.0, 9.0] {
                p.pixels.fill(0.0);
                p.chrome(time, AudioFrame::default());
                let center = (365 * WIDTH + 270) * 3;
                assert_eq!(
                    &p.pixels[center..center + 3],
                    &[0.0; 3],
                    "solid bubble returned"
                );
                let tip = &p.pixels[70 * WIDTH * 3..71 * WIDTH * 3];
                assert!(
                    tip.as_chunks::<3>()
                        .0
                        .iter()
                        .filter(|rgb| rgb.iter().any(|v| *v > 0.0))
                        .count()
                        < 10,
                    "zero-width tips draw a horizontal line"
                );
                let surface: Vec<_> = p
                    .pixels
                    .as_chunks::<3>()
                    .0
                    .iter()
                    .filter(|rgb| rgb.iter().any(|v| *v > 1.0))
                    .collect();
                assert!(
                    (10000..WIDTH * HEIGHT / 3).contains(&surface.len()),
                    "missing or oversized metal surface: {}",
                    surface.len()
                );
                assert!(
                    surface.iter().any(|rgb| rgb.iter().any(|v| *v > 130.0)),
                    "missing reflected key light"
                );
                assert!(
                    surface.iter().any(|rgb| rgb.iter().all(|v| *v < 20.0)),
                    "missing dark reflections"
                );
                for rgb in surface {
                    let low = rgb.iter().copied().fold(f32::INFINITY, f32::min);
                    let high = rgb.iter().copied().fold(0.0, f32::max);
                    assert!(high - low < high * 0.3, "rainbow tint overwhelms metal");
                }
            }
        }
    }

    #[test]
    fn solid_edges_and_cover_fragments_move_without_pixel_jumps() {
        let cover = image::DynamicImage::ImageRgb8(image::RgbImage::from_fn(256, 256, |x, y| {
            let value = if (x / 4 + y / 4) % 2 == 0 { 0 } else { 255 };
            image::Rgb([value; 3])
        }));
        for style in ["chrome", "collage"] {
            for variant in 0..3 {
                let mut p = Painter::new(style, 42, variant);
                p.set_cover(&cover);
                for time in [0.0, 1.0, 3.0] {
                    let mut frames = Vec::new();
                    for t in [time, time + 0.002] {
                        p.pixels.copy_from_slice(&p.base);
                        if style == "chrome" {
                            p.chrome(t, AudioFrame::default());
                        } else {
                            p.cover_scene(t, Pose::default(), AudioFrame::default());
                        }
                        frames.push(p.pixels.clone());
                    }
                    let jump = frames[0]
                        .iter()
                        .zip(&frames[1])
                        .map(|(a, b)| (a - b).abs())
                        .fold(0.0_f32, f32::max);
                    assert!(
                        jump > 0.0 && jump < 16.0,
                        "pixel jump in {style}/{variant}: {jump}"
                    );
                }
            }
        }
    }

    #[test]
    fn line_coverage_is_independent_of_segment_boundaries() {
        let mut p = Painter::new("orbit", 42, 0);
        p.pixels.fill(0.0);
        let color = Color(100.0, 100.0, 100.0);
        p.line(100.0, 100.25, 120.0, 100.25, color, 1.0);
        let whole = p.pixels.clone();
        p.pixels.fill(0.0);
        for x in 100..120 {
            p.line(x as f64, 100.25, x as f64 + 1.0, 100.25, color, 1.0);
        }
        assert!(
            whole
                .iter()
                .zip(&p.pixels)
                .all(|(a, b)| (a - b).abs() < 0.001)
        );
        assert!(whole.iter().any(|v| *v > 0.0));
    }

    #[test]
    fn orbit_accents_move_the_rings_without_changing_their_light() {
        for variant in 0..3 {
            let mut p = Painter::new("orbit", 42, variant);
            p.pixels.fill(0.0);
            p.orbit(1.0, AudioFrame::default());
            let quiet = p.pixels.clone();
            p.pixels.fill(0.0);
            p.response[3].light = 1.0;
            p.orbit(1.0, AudioFrame::default());
            assert_ne!(quiet, p.pixels, "orbit/{variant} ignores accents");
        }
    }

    #[test]
    fn sound_attacks_select_local_regions_without_scaling_the_whole_shape() {
        let mut previous = vec![];
        for event in 1..12 {
            let audio = AudioFrame {
                bands: [1.0; BANDS],
                level: 1.0,
                bass: 1.0,
                treble: 1.0,
                beat: 1.0,
                event,
                event_age: 0.3,
                ..AudioFrame::default()
            };
            let regions: Vec<_> = (0..400)
                .map(|i| band_at(&audio, i as f64 / 400.0))
                .collect();
            let active = regions.iter().filter(|v| **v > 0.0).count();
            assert!(
                (10..160).contains(&active),
                "global or missing reaction: {active}"
            );
            assert!(
                previous != regions,
                "successive sounds reuse the same regions"
            );
            let start = AudioFrame {
                event_age: 0.0,
                ..audio
            };
            let before = AudioFrame {
                event: event - 1,
                ..audio
            };
            for i in 0..400 {
                let position = i as f64 / 400.0;
                assert!((band_at(&start, position) - band_at(&before, position)).abs() < 1e-9);
            }
            previous = regions;
        }
    }

    #[test]
    fn highlight_is_continuous_across_grid_cells_and_neon_boundary() {
        let mut p = Painter::new("silver", 4, 0);
        p.view.focus = 0.25;
        let mut sample = |x: f64, y: f64| {
            p.glow.fill(0.0);
            p.light_point(x, y, Color(255.0, 255.0, 255.0), 1.0);
            p.glow.clone()
        };
        let before = sample(251.999, 200.0);
        let after = sample(252.001, 200.0);
        let difference = |a: &[f32], b: &[f32]| {
            a.iter()
                .zip(b)
                .map(|(a, b)| (a - b).abs())
                .fold(0.0_f32, f32::max)
        };
        assert!(
            difference(&before, &after) < 0.01,
            "highlight jumps at a glow-grid boundary"
        );
        let energy = |a: &[f32]| a.iter().sum::<f32>();
        assert!((energy(&before) - energy(&after)).abs() < 0.01);

        p.neon = true;
        p.phase = 0.0;
        p.neon_time = PI / (2.0 * 0.73);
        let edge = 270.0 + 240.0 * (p.neon_time * 0.39).sin() - 170.0;
        let y = 480.0 + 420.0 * (p.neon_time * 0.31).cos();
        p.glow.fill(0.0);
        p.light_point(edge - 0.001, y, Color(255.0, 255.0, 255.0), 1.0);
        let outside = p.glow.clone();
        p.glow.fill(0.0);
        p.light_point(edge + 0.001, y, Color(255.0, 255.0, 255.0), 1.0);
        assert!(
            difference(&outside, &p.glow) < 0.01,
            "defocused line breaks at the edge of the neon light"
        );
    }

    #[test]
    fn highlight_survives_repeated_neon_fade_cycles() {
        let mut p = Painter::new("silver", 43, 0);
        p.phase = 0.0;
        p.view.focus = 0.25;
        let threshold = 0.15_f64.asin();
        for cycle in 0..3 {
            for phase in [threshold, PI - threshold] {
                let switch = (phase + cycle as f64 * 2.0 * PI) / 0.73;
                let x = 270.0 + 240.0 * (switch * 0.39).sin();
                let y = 480.0 + 420.0 * (switch * 0.31).cos();
                let mut frames = Vec::new();
                for time in [switch - 0.000001, switch + 0.000001] {
                    p.neon_time = time;
                    p.glow.fill(0.0);
                    p.line(x, y - 40.0, x, y + 40.0, Color(255.0, 255.0, 255.0), 1.0);
                    frames.push(p.glow.clone());
                }
                let jump = frames[0]
                    .iter()
                    .zip(&frames[1])
                    .map(|(a, b)| (a - b).abs())
                    .fold(0.0_f32, f32::max);
                assert!(
                    jump < 0.01,
                    "line highlight breaks when neon switches at {switch}: {jump}"
                );
            }
        }
    }

    #[test]
    fn neon_is_optional_local_and_changes_over_time() {
        let mut p = Painter::new("silver", 4, 0);
        p.light_point(270.0, 480.0, Color(255.0, 100.0, 50.0), 1.0);
        assert!(p.glow.iter().all(|v| *v == 0.0));
        assert!(p.pixels.iter().any(|v| *v > 0.0));
        p.view.focus = 0.25;
        p.light_point(270.0, 480.0, Color(255.0, 100.0, 50.0), 1.0);
        assert!(
            p.glow.iter().any(|v| *v > 0.0),
            "defocus disappears when neon is disabled"
        );
        p.view.focus = 1.0;
        p.glow.fill(0.0);
        p.neon = true;
        let mut masks = Vec::new();
        for second in 0..20 {
            p.neon_time = second as f64;
            let mask: Vec<_> = (0..100)
                .map(|i| p.neon_strength((i % 10) as f64 * 54.0, (i / 10) as f64 * 96.0))
                .collect();
            assert!(mask.iter().filter(|v| **v > 0.0).count() < 35);
            masks.push(mask);
        }
        assert!(masks.iter().any(|m| m.iter().all(|v| *v == 0.0)));
        assert!(masks.iter().any(|m| m.iter().any(|v| *v > 0.0)));
        assert!(masks.windows(2).any(|m| m[0] != m[1]));
        p.neon_time = (PI / 2.0 - p.phase).rem_euclid(2.0 * PI) / 0.73;
        let t = p.neon_time;
        let x = 270.0 + 240.0 * (t * 0.39 + p.phase).sin();
        let y = 480.0 + 420.0 * (t * 0.31 - p.phase).cos();
        p.light_point(x, y, Color(255.0, 100.0, 50.0), 1.0);
        assert!(p.glow.iter().any(|v| *v > 0.0));
    }

    #[test]
    fn procedural_volumes_react_to_audio() {
        for style in ["nebula", "liquid", "bloom"] {
            let mut p = Painter::new(style, 42, 1);
            let quiet = p.frame(120, AudioFrame::default()).to_vec();
            let loud = p.frame(
                120,
                AudioFrame {
                    bands: [0.8; BANDS],
                    level: 0.8,
                    bass: 0.7,
                    beat: 0.5,
                    ..AudioFrame::default()
                },
            );
            assert!(quiet != loud, "unresponsive volume: {style}");
        }
    }

    #[test]
    fn volume_detail_and_short_audio_attacks_are_visible() {
        for style in ["nebula", "liquid", "bloom"] {
            let mut p = Painter::new(style, 71, 1);
            let mut audio = vec![AudioFrame::default(); 180];
            p.prepare_audio(&audio);
            let quiet = p.frame(106, audio[106]).to_vec();
            for a in &mut audio[100..106] {
                a.bands.fill(0.8);
            }
            p.prepare_audio(&audio);
            let reactive = p.frame(106, audio[106]).to_vec();
            let difference = quiet
                .iter()
                .zip(&reactive)
                .map(|(a, b)| a.abs_diff(*b) as f64)
                .sum::<f64>()
                / quiet.len() as f64;
            let visible = quiet
                .as_chunks::<3>()
                .0
                .iter()
                .zip(reactive.as_chunks::<3>().0.iter())
                .filter(|(a, b)| a.iter().zip(*b).any(|(a, b)| a.abs_diff(*b) > 12))
                .count();
            assert!(
                difference > 3.0,
                "inaudible visual response: {style}: {difference}"
            );
            assert!(
                visible > WIDTH * HEIGHT / 10,
                "too little visible reaction: {style}"
            );
            if style == "nebula" {
                let gradient = reactive
                    .as_chunks::<{ WIDTH * 3 }>()
                    .0
                    .iter()
                    .map(|row| {
                        row.iter()
                            .skip(3)
                            .zip(row)
                            .map(|(a, b)| a.abs_diff(*b) as f64)
                            .sum::<f64>()
                    })
                    .sum::<f64>()
                    / reactive.len() as f64;
                assert!(gradient > 0.65, "structure is blurred away: {gradient}");
            }
        }
    }

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
    fn material_audio_rises_and_falls_gradually() {
        let painter = Painter::new("glass", 42, 0);
        let mut frames = vec![AudioFrame::default(); 20];
        frames[1].bands = [1.0; BANDS];
        frames[1].attacks = [1.0; 3];
        frames[1].attack_age = [0.0; 3];
        let frames = smooth_material_audio(&painter, frames);
        assert!((frames[1].bands[0] - 0.06).abs() < 1e-9);
        assert!((frames[1].attacks[0] - 0.06).abs() < 1e-9);
        assert!(frames[2].bands[0] > 0.0 && frames[2].bands[0] < frames[1].bands[0]);
        assert!(frames[2].attacks[0] > 0.0 && frames[2].attacks[0] < frames[1].attacks[0]);
    }

    #[test]
    fn every_style_and_variant_renders_and_evolves() {
        for style in STYLES {
            for v in 0..3 {
                let mut p = Painter::new(style, 42, v);
                let cover = image::RgbImage::from_fn(256, 256, |x, y| {
                    image::Rgb([x as u8, y as u8, (x / 2 + y / 2) as u8])
                });
                p.set_cover(&image::DynamicImage::ImageRgb8(cover));
                let a = AudioFrame {
                    level: 0.5,
                    bands: [0.3; 64],
                    ..AudioFrame::default()
                };
                p.prepare_audio(&vec![a; 600]);
                let first = p.frame(0, a).to_vec();
                assert_eq!(first.len(), WIDTH * HEIGHT * 3);
                assert!(first != p.frame(300, a), "static scene: {style}/{v}");
                assert!(
                    first == p.frame(0, a),
                    "non-deterministic scene: {style}/{v}"
                );
            }
        }
    }

    #[test]
    fn cover_background_has_no_card_edges_and_collage_leaves_metadata_clear() {
        let cover = image::DynamicImage::ImageRgb8(image::RgbImage::from_fn(256, 256, |x, y| {
            image::Rgb([x as u8, y as u8, 180])
        }));
        for style in ["cover", "collage"] {
            let mut previous = Vec::new();
            for variant in 0..3 {
                let mut p = Painter::new(style, 42, variant);
                p.set_cover(&cover);
                let frame = p.frame(200, AudioFrame::default());
                assert!(frame != previous, "identical variants: {style}/{variant}");
                previous = frame.to_vec();
                let rows = if style == "cover" {
                    0..HEIGHT
                } else {
                    160..780
                };
                for y in rows {
                    for x in 1..WIDTH {
                        let i = (y * WIDTH + x) * 3;
                        assert!(
                            (0..3).all(|c| frame[i + c].abs_diff(frame[i - 3 + c]) <= 4),
                            "hard background edge: {style}/{variant} at {x},{y}"
                        );
                    }
                }
            }
        }
    }

    #[test]
    fn backgrounds_have_colour_brightness_range_and_readable_metadata() {
        let mut backgrounds = std::collections::HashSet::new();
        let mut lightest = 0.0_f32;
        let mut darkest = f32::INFINITY;
        for seed in 0..24 {
            let p = Painter::new("silver", seed, 0);
            let i = (100 * WIDTH + 270) * 3;
            let rgb = &p.base[i..i + 3];
            let low = rgb.iter().copied().fold(f32::INFINITY, f32::min);
            let high = rgb.iter().copied().fold(0.0_f32, f32::max);
            assert!(high - low > 5.0, "achromatic background for seed {seed}");
            assert!(high > 20.0, "near-black background for seed {seed}");
            backgrounds.insert([rgb[0] as u8, rgb[1] as u8, rgb[2] as u8]);
            let luminance = rgb.iter().sum::<f32>() / 3.0;
            lightest = lightest.max(luminance);
            darkest = darkest.min(luminance);
            for y in [565, 605, 642, 660] {
                assert!(p.shade[y * WIDTH + 270] < 0.26, "metadata scrim too weak");
            }
        }
        assert!(backgrounds.len() > 16);
        assert!(lightest - darkest > 20.0);
    }

    #[test]
    fn new_material_variants_have_distinct_geometry() {
        for style in &STYLES[17..] {
            let mut previous = Vec::new();
            for variant in 0..3 {
                let mut p = Painter::new(style, 71, variant);
                let frame = p.frame(90, AudioFrame::default());
                let changed = frame
                    .iter()
                    .zip(&previous)
                    .filter(|(a, b)| a.abs_diff(**b) > 3)
                    .count();
                assert!(
                    previous.is_empty() || changed > 100,
                    "identical variants: {style}/{variant}"
                );
                previous = frame.to_vec();
            }
        }
    }

    #[test]
    fn random_motion_and_palettes_are_reproducible_and_diverse() {
        let red = image::DynamicImage::ImageRgb8(image::RgbImage::from_pixel(
            32,
            32,
            image::Rgb([220, 30, 40]),
        ));
        let blue = image::DynamicImage::ImageRgb8(image::RgbImage::from_pixel(
            32,
            32,
            image::Rgb([20, 40, 230]),
        ));
        let mut colors = std::collections::HashSet::new();
        for seed in 0..24 {
            let mut p = Painter::new("chrome", seed, 0);
            let mut other = Painter::new("chrome", seed, 0);
            p.set_cover(&red);
            other.set_cover(&red);
            assert_eq!(p.accent(), other.accent());
            let mut other = Painter::new("chrome", seed, 0);
            other.set_cover(&blue);
            assert_eq!(p.accent(), other.accent());
            assert_eq!(p.base, other.base);
            colors.insert(p.accent());
            assert_eq!(p.evolution(3.0).zoom, other.evolution(3.0).zoom);
            assert!((p.evolution(3.0).zoom - p.evolution(25.0).zoom).abs() > 1e-6);
        }
        assert!(colors.len() > 8);
    }

    #[test]
    fn sustained_notes_and_melody_react_without_drums_and_attacks_are_sharper() {
        let tone = |frequency: f64| {
            let samples: Vec<_> = (0..320000)
                .map(|i| {
                    let t = i as f64 / 16000.0;
                    (t / 2.0).min(1.0) * (2.0 * PI * frequency * t).sin()
                })
                .collect();
            analyze(&samples)[150]
        };
        let mut low = tone(440.0);
        let mut high = tone(1760.0);
        // Hold location and loudness fixed to test melodic response alone.
        for frame in [&mut low, &mut high] {
            frame.event = 10;
            frame.event_age = 1.0;
            frame.level = 0.5;
            frame.beat = 0.0;
        }
        let response: Vec<_> = (0..4000)
            .map(|i| {
                let position = i as f64 / 4000.0;
                (band_at(&low, position), band_at(&high, position))
            })
            .collect();
        assert!(response.iter().any(|(v, _)| *v > 0.15));
        assert!(response.iter().any(|(a, b)| (a - b).abs() > 0.05));
        let attack = AudioFrame { beat: 0.8, ..low };
        assert!((0..4000).any(|i| {
            let position = i as f64 / 4000.0;
            band_at(&attack, position) > band_at(&low, position) + 0.15
        }));
        assert_eq!(band_at(&AudioFrame::default(), 0.5), 0.0);
    }

    #[test]
    fn frequency_attacks_are_separate_and_sustained_notes_do_not_retrigger() {
        for (region, frequency) in [80.0, 500.0, 5000.0].into_iter().enumerate() {
            let samples: Vec<_> = (0..64000)
                .map(|i| {
                    if (16000..48000).contains(&i) {
                        (2.0 * PI * frequency * i as f64 / 16000.0).sin()
                    } else {
                        0.0
                    }
                })
                .collect();
            let frames = analyze(&samples);
            let peak: [f64; 3] = std::array::from_fn(|r| {
                frames[28..34]
                    .iter()
                    .map(|f| f.attacks[r])
                    .fold(0.0, f64::max)
            });
            assert!(peak[region] > 0.3, "missing attack: {region}: {peak:?}");
            for other in 0..3 {
                if other != region {
                    assert!(
                        peak[other] < peak[region] * 0.5,
                        "cross-band attack: {peak:?}"
                    );
                }
            }
            assert!(
                frames[75].attack_age[region] > 1.0,
                "sustain retriggers attack"
            );
            assert!(frames[220].attacks[region] < 0.05);
        }
        assert!(
            analyze(&vec![0.0; 32000])
                .iter()
                .all(|f| f.attacks == [0.0; 3])
        );
    }

    #[test]
    fn every_family_and_variant_has_its_own_material_attack() {
        let cover = image::DynamicImage::ImageRgb8(image::RgbImage::from_fn(256, 256, |x, y| {
            image::Rgb([x as u8, y as u8, 140])
        }));
        for (style, region) in [
            ("silver", 0),
            ("violet", 1),
            ("amber", 1),
            ("aurora", 2),
            ("prism", 1),
            ("galaxy", 0),
            ("tunnel", 0),
            ("orbit", 0),
            ("wave", 0),
            ("rain", 2),
            ("chrome", 1),
            ("silk", 1),
            ("analog", 1),
            ("cover", 0),
            ("nebula", 0),
            ("liquid", 0),
            ("bloom", 1),
            ("ferro", 0),
            ("cymatics", 1),
            ("topography", 0),
            ("glass", 2),
            ("ink", 1),
            ("kinetic", 0),
            ("lightning", 2),
            ("interference", 1),
        ] {
            for variant in 0..3 {
                let mut p = Painter::new(style, 71, variant);
                p.set_cover(&cover);
                let quiet = p.frame(240, AudioFrame::default()).to_vec();
                let mut attack = AudioFrame::default();
                attack.attacks[region] = 1.0;
                attack.attack_age[region] = if materials::spatial(style) { 0.0 } else { 0.3 };
                let active = p.frame(240, attack).to_vec();
                let changed = quiet
                    .iter()
                    .zip(&active)
                    .filter(|(a, b)| a.abs_diff(**b) > 3)
                    .count();
                assert!(
                    changed > 100,
                    "missing material attack: {style}/{variant}: {changed}"
                );
                assert_eq!(
                    active,
                    p.frame(240, attack),
                    "material state depends on render order: {style}/{variant}"
                );
            }
        }
    }

    #[test]
    fn volumes_occlude_background_light_but_keep_empty_space_transparent() {
        for style in ["liquid", "nebula", "bloom"] {
            let mut p = Painter::new(style, 71, 1);
            p.update_field(120, AudioFrame::default());
            p.pixels.fill(0.0);
            p.volume(4.0, Pose::default(), AudioFrame::default());
            let dark = p.pixels.clone();
            p.pixels.fill(100.0);
            p.volume(4.0, Pose::default(), AudioFrame::default());
            let transmission: Vec<_> = p
                .pixels
                .iter()
                .zip(&dark)
                .map(|(a, b)| (a - b) / 100.0)
                .collect();
            assert!(transmission.iter().all(|t| (-0.001..=1.001).contains(t)));
            assert!(
                transmission
                    .iter()
                    .any(|t| *t < if style == "liquid" { 0.3 } else { 0.95 }),
                "volume does not attenuate distant particles: {style}"
            );
            assert!(
                transmission.iter().any(|t| *t > 0.9),
                "empty space hides distant particles"
            );
        }
    }

    #[test]
    fn waves_travel_and_materials_keep_separate_audio_responses() {
        let mut a = AudioFrame {
            attacks: [1.0; 3],
            attack_age: [0.0; 3],
            ..AudioFrame::default()
        };
        assert!(audio_wave(a, 0, 0.0) > 0.9);
        assert!(audio_wave(a, 0, 1.4).abs() < 0.01);
        a.attack_age = [0.5; 3];
        assert!(audio_wave(a, 0, 1.4) > 0.3);
        assert!(audio_wave(a, 0, 0.0).abs() < 0.01);
        assert_eq!(audio_wave(AudioFrame::default(), 0, 0.0), 0.0);
        for (style, regions) in [
            ("liquid", &[0, 2][..]),
            ("silk", &[1, 2][..]),
            ("prism", &[1, 2][..]),
        ] {
            let mut painter = Painter::new(style, 71, 1);
            let quiet = painter.frame(120, AudioFrame::default()).to_vec();
            for &region in regions {
                let mut accent = AudioFrame::default();
                accent.attacks[region] = 1.0;
                accent.attack_age[region] = 0.3;
                let loud = painter.frame(120, accent).to_vec();
                let changed = quiet
                    .iter()
                    .zip(&loud)
                    .filter(|(a, b)| a.abs_diff(**b) > 3)
                    .count();
                assert!(changed > 100, "missing {style} region {region}: {changed}");
                assert_eq!(loud, painter.frame(120, accent), "non-deterministic render");
            }
        }
    }

    #[test]
    fn bass_and_treble_are_separate_and_beats_decay() {
        for frequency in [80.0, 5000.0] {
            let samples: Vec<_> = (0..320000)
                .map(|i| {
                    if (16000..32000).contains(&i) {
                        (2.0 * PI * frequency * i as f64 / 16000.0).sin()
                    } else {
                        0.0
                    }
                })
                .collect();
            let frames = analyze(&samples);
            if frequency < 100.0 {
                assert!(frames[45].bass > frames[45].treble);
            } else {
                assert!(frames[45].treble > frames[45].bass);
            }
            assert!(frames[29..33].iter().any(|f| f.beat > 0.1));
            assert!(frames[90].beat < 0.01);
            assert!(frames[33].event > frames[28].event);
            assert_eq!(frames[90].event, frames[100].event);
        }
    }
}
