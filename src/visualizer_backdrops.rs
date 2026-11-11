//! Six distinct abstract night environments with analytic, softly lit geometry.
use super::{Color, mix_light};
use rand::Rng;

pub(super) struct Night {
    phase: f64,
}

impl Night {
    pub(super) fn new(rng: &mut impl Rng) -> Self {
        Self {
            phase: rng.random_range(0.0..std::f64::consts::TAU),
        }
    }

    pub(super) fn color(
        &self,
        scene: u64,
        uv: (f64, f64),
        local: (f64, f64),
        pigment: Color,
        accent: Color,
        _rim: f64,
    ) -> Color {
        let (u, v) = uv;
        let (a, b) = local;
        let p = self.phase;
        let g = |d: f64, w: f64| (-d.powi(2) / w.powi(2)).exp();
        let x = a + 0.055 * (b * 5.0 + p).sin();
        let y = b + 0.045 * (a * 6.0 - p).sin();
        let radius = (x * x + y * y).sqrt();
        let theta = y.atan2(x);
        // Body, emitted light, near-white highlights, local atmospheric scatter.
        // Each scene has its own topology and lighting, not a shared ribbon overlay.
        let (body, glow, core, haze) = match scene {
            7 => {
                // A luminous fissure between broad, opaque asymmetric walls.
                let path = x + 0.14 * (y * 4.0 + p).sin() + y * 0.32;
                let width = 0.018 + 0.028 * (y * 3.0 + p).cos().powi(2);
                let gap = g(path, width);
                let depth = g(y + 0.13, 0.27);
                let wall = g(path.abs() - width * 2.5, 0.075) * (0.6 + 0.4 * (y * 5.0).sin());
                (
                    wall * 0.18,
                    gap * depth * 0.65,
                    g(path, width * 0.28) * depth * 0.48,
                    g(path, 0.16) * depth * 0.12,
                )
            }
            8 => {
                // A broken smoky spiral: light follows a winding path into a dark eye.
                let twist = theta + radius * 11.0 + p;
                let arm = (0.5 + 0.5 * twist.sin()).powi(5);
                let envelope = g(radius - 0.24, 0.18);
                let eye = 1.0 - g(radius, 0.075);
                let flecks = 0.6 + 0.4 * (x * 19.0 + y * 13.0).sin();
                (
                    arm * envelope * eye * 0.22,
                    arm * envelope * eye * flecks * 0.23,
                    g(radius - 0.12, 0.026) * arm * 0.20,
                    envelope * eye * 0.065,
                )
            }
            9 => {
                // Overlapping translucent membranes, backlit through their thickness.
                let mut density = 0.0;
                let mut light = 0.0;
                for layer in 0..3 {
                    let z = layer as f64;
                    let curve = y + (z - 1.0) * 0.18 + 0.17 * (x * (3.2 + z) + p + z * 1.4).sin();
                    let mask = g(x + z * 0.08 - 0.12, 0.42);
                    density += g(curve, 0.085) * mask * (0.14 - z * 0.025);
                    light += g(curve + 0.035, 0.018) * mask * (0.13 + z * 0.04);
                }
                let lamp = g(x - 0.12, 0.15) * g(y + 0.07, 0.22);
                (density, light, lamp * 0.30, lamp * 0.10)
            }
            10 => {
                // Organic liquid caustics: irregular illuminated cells, not stripes.
                let wx = x * 8.0 + (y * 6.0 + p).sin() * 1.4;
                let wy = y * 7.0 + (x * 5.0 - p).cos() * 1.2;
                let field = wx.sin() + wy.cos() + (wx * 0.7 - wy * 0.8).sin() * 0.55;
                let pool = g(x + 0.10, 0.40) * g(y - 0.02, 0.38);
                let caustic = g(field - 0.3, 0.16);
                let submerged = g(field + 0.15, 0.65);
                (
                    submerged * pool * 0.11,
                    caustic * pool * 0.28,
                    caustic.powi(3) * pool * 0.10,
                    pool * 0.045,
                )
            }
            11 => {
                // Nested off-centre cavities: successive dark lips reveal a lit interior.
                let mut surface = 0.0;
                let mut rim = 0.0;
                for layer in 0..4 {
                    let z = layer as f64;
                    let cx = x + 0.035 * z;
                    let cy = y - 0.025 * z;
                    let r = (cx * cx + (cy * 0.8).powi(2)).sqrt();
                    let boundary = 0.12 + z * 0.085 + 0.025 * (theta * 3.0 + p + z).sin();
                    let face = g(r - boundary - 0.025, 0.04);
                    surface += face * (0.15 - z * 0.025);
                    rim += g(r - boundary, 0.012 + z * 0.003)
                        * (0.5 + 0.5 * (theta + z * 0.6).cos())
                        * 0.16;
                }
                let heart = g(x + 0.10, 0.075) * g(y - 0.06, 0.10);
                (surface, rim, heart * 0.42, heart * 0.17)
            }
            _ => {
                // Suspended dust in billowing light pockets, without a regular star grid.
                let cloud = (0.5
                    + 0.22 * (x * 9.0 + (y * 5.0 + p).sin()).sin()
                    + 0.18 * (y * 11.0 - x * 4.0).cos())
                .clamp(0.0, 1.0);
                let envelope = g(x + 0.15 * (y * 4.0 + p).sin(), 0.24);
                let light = g(x - 0.10, 0.14) * g(y + 0.12, 0.22);
                let mut dust = 0.0;
                for point in 0..13 {
                    let t = point as f64 * 2.399 + p;
                    let px = 0.32 * t.sin();
                    let py = 0.40 * (t * 1.73).cos();
                    let size = 0.0025 + 0.004 * (t * 2.1).sin().powi(2);
                    dust += g(x - px, size) * g(y - py, size * 1.3) * 0.24;
                }
                (
                    cloud * envelope * 0.18,
                    light * cloud * 0.32,
                    dust + light * cloud.powi(4) * 0.15,
                    envelope * cloud * 0.075,
                )
            }
        };
        let material = mix_light(pigment, Color(65.0, 80.0, 105.0), 0.22);
        let light = mix_light(pigment, accent, 0.58);
        let white = mix_light(light, Color(215.0, 230.0, 245.0), 0.65);
        let vignette = (1.0 - ((u - 0.5).powi(2) + (v - 0.5).powi(2)) * 0.7).clamp(0.6, 1.0);
        Color(
            ((3.0 + material.0 * body + light.0 * (glow + haze) + white.0 * core) * vignette)
                .clamp(0.0, 255.0),
            ((5.0 + material.1 * body + light.1 * (glow + haze) + white.1 * core) * vignette)
                .clamp(0.0, 255.0),
            ((10.0 + material.2 * body + light.2 * (glow + haze) + white.2 * core) * vignette)
                .clamp(0.0, 255.0),
        )
    }
}
