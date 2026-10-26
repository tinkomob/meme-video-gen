use rand::{Rng, SeedableRng};
use std::f64::consts::PI;
pub const WIDTH: usize = 540;
pub const HEIGHT: usize = 960;
pub const FPS: usize = 30;
pub const BANDS: usize = 64;
pub const STYLES: [&str; 18] = [
    "silver", "violet", "amber", "aurora", "prism", "galaxy", "tunnel", "orbit", "wave", "rain",
    "chrome", "collage", "silk", "analog", "cover", "nebula", "liquid", "bloom",
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
    let (mut event, mut since_event) = (0, FPS);
    for frame in &mut frames {
        frame.level /= peak_level;
        for (b, value) in frame.bands.iter_mut().enumerate() {
            let v = (60.0 * (*value) / peak).ln_1p() / 61.0_f64.ln();
            let weight = if v < previous.bands[b] { 0.2 } else { 0.65 };
            *value = previous.bands[b] + weight * (v - previous.bands[b]);
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
    timeline: Vec<Pose>,
    cover: Option<image::RgbImage>,
    cover_soft: Option<image::RgbImage>,
    view: View,
    morph: f64,
    neon: bool,
    neon_time: f64,
    responses: Vec<[motion::Response; 12]>,
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
use palettes::palette;
#[path = "visualizer_art.rs"]
mod art;
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
            timeline: Vec::new(),
            cover: None,
            cover_soft: None,
            view: View::default(),
            morph: 0.0,
            neon: !seed.is_multiple_of(4),
            neon_time: 0.0,
            responses: Vec::new(),
            response: [motion::Response::default(); 12],
            field: vec![motion::Field::default(); 19 * 33],
        };
        let hue = r.random::<f64>();
        for i in 0..3 {
            p.palette[i] = mix_light(p.palette[i], motion::hue_color(hue + i as f64 * 0.31), 0.8);
        }
        let count = r.random_range(4..9);
        let mut times = vec![0.0, 1.0];
        // Stratified times keep transitions smooth without fixing their number or rhythm.
        for i in 1..count - 1 {
            times.push((i as f64 + r.random_range(-0.4..0.4)) / (count - 1) as f64);
        }
        times.sort_by(f64::total_cmp);
        for at in times {
            p.timeline.push(Pose {
                at,
                zoom: r.random_range(0.78..1.22),
                turn: r.random_range(-0.22..0.22),
                x: r.random_range(-45.0..45.0),
                y: r.random_range(-65.0..65.0),
                unfold: r.random_range(0.05..1.0),
                color: r.random_range(0.0..0.8),
                density: r.random_range(0.2..1.0),
            });
        }
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let d = ((x as f64 - 270.0) / 430.0).hypot((y as f64 - 480.0) / 700.0);
                let v = (1.0 - d).max(0.0);
                let c = mix_light(Color(2.0, 3.0, 5.0), p.palette[0], 0.012 + 0.025 * v);
                let i = (y * WIDTH + x) * 3;
                let text_y = 620.0;
                let (dx, dy) = ((x as f64 - 270.0) / 240.0, (y as f64 - text_y) / 85.0);
                p.shade[i / 3] = (1.0 - 0.55 * (-dx.powi(4) - dy.powi(4)).exp()) as f32;
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
        if neon == 0.0 {
            return;
        }
        let (gx, gy) = ((x / 4.0) as i32, (y / 4.0) as i32);
        for dy in -3..=3 {
            for dx in -3..=3 {
                let (xx, yy) = (gx + dx, gy + dy);
                if xx < 0 || xx >= WIDTH as i32 / 4 || yy < 0 || yy >= HEIGHT as i32 / 4 {
                    continue;
                }
                let a = intensity * neon * 0.11 / (1 + dx * dx + dy * dy) as f64;
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
        for i in 1..self.timeline.len() {
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
        *self.timeline.last().unwrap()
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
                    let fine = (dx * 9.0 + dy * 6.0 + fold * 3.0 - time * 0.7).sin();
                    let caustic = (1.0 - fine.abs()).powi(12);
                    let light = if matches!(self.style.as_str(), "cover" | "collage") {
                        (-dx * dx - dy * dy).exp() * (0.09 + 0.04 * r.energy)
                    } else {
                        veil * (0.14 + 0.1 * pose.density + 0.07 * r.opening)
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
    fn depth_particles(&mut self, seconds: f64, pose: Pose, a: AudioFrame) {
        for i in 0..720 {
            let s = self.particles[i];
            let z = (s.depth + seconds * (0.015 + 0.01 * s.u)) % 1.0;
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
        a.event = a.event.wrapping_add(self.phase.to_bits());
        self.pixels.copy_from_slice(&self.base);
        self.glow.fill(0.0);
        let seconds = f as f64 / FPS as f64;
        let pose = self.evolution(f as f64 / 599.0);
        self.neon_time = seconds;
        self.update_field(f, a);
        a = self.shape_audio(a);
        if matches!(self.style.as_str(), "collage" | "cover") {
            self.cover_scene(seconds, pose, a);
        } else {
            if matches!(self.style.as_str(), "nebula" | "liquid" | "bloom") {
                self.volume(seconds * self.speed, pose, a);
                self.depth_particles(seconds, pose, a);
            } else if self.style == "chrome" {
                self.morph = pose.unfold;
                self.chrome(self.response[0].travel * self.speed * 2.0, a);
            } else {
                self.depth_layers(seconds, pose, a);
            }
            if !matches!(
                self.style.as_str(),
                "analog" | "nebula" | "liquid" | "bloom"
            ) {
                self.depth_particles(seconds, pose, a);
                self.accents(seconds, a);
            }
        }
        self.atmosphere(seconds, pose);
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
                for c in 0..3 {
                    let halo = self.glow[gi + c] * (1.0 - fx) * (1.0 - fy)
                        + self.glow[gi + right + c] * fx * (1.0 - fy)
                        + self.glow[gi + below + c] * (1.0 - fx) * fy
                        + self.glow[gi + below + right + c] * fx * fy;
                    let mut value = (self.pixels[i + c] + halo) * self.shade[i / 3];
                    value = value.max(0.0);
                    value = value * 1.8 / (1.0 + value / 320.0);
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
    fn neon_is_optional_local_and_changes_over_time() {
        let mut p = Painter::new("silver", 4, 0);
        p.light_point(270.0, 480.0, Color(255.0, 100.0, 50.0), 1.0);
        assert!(p.glow.iter().all(|v| *v == 0.0));
        assert!(p.pixels.iter().any(|v| *v > 0.0));
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
        let mut lengths = std::collections::HashSet::new();
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
            lengths.insert(p.timeline.len());
            assert!(p.timeline.windows(2).all(|w| w[1].at > w[0].at));
            assert_eq!(p.timeline.first().unwrap().at, 0.0);
            assert_eq!(p.timeline.last().unwrap().at, 1.0);
        }
        assert!(lengths.len() > 1 && colors.len() > 8);
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
