//! Deterministic, seekable ink drawings. Curves are bundles of nearby flow lines,
//! not independent random walks: time and integrated musical travel deform the
//! field continuously, so frames can be rendered out of order without popping.
use super::*;

pub(super) fn matches(style: &str) -> bool {
    matches!(style, "filament" | "neural" | "chaos")
}

#[derive(Clone, Copy, Default)]
pub(super) struct BranchMotion {
    bend: f64,
    velocity: f64,
    energy: f64,
    kick: f64,
    flow: f64,
}

impl Painter {
    pub(super) fn prepare_ink_motion(&mut self, frames: &[AudioFrame]) {
        let mut states = [BranchMotion::default(); 18];
        let mut previous = [0.0_f64; 18];
        let mut flux_average = [0.015_f64; 18];
        self.ink_motion = frames
            .iter()
            .map(|audio| {
                for (branch, state) in states.iter_mut().enumerate() {
                    let region = branch % 3;
                    let (start, count) = [(0, 16), (16, 24), (40, 24)][region];
                    let first = start + (branch / 3 * 3) % count;
                    let energy = (0..4)
                        .map(|i| audio.bands[start + (first - start + i) % count])
                        .sum::<f64>()
                        / 4.0;
                    let rise = (energy - previous[branch]).max(0.0);
                    let onset = ((rise - 0.006).max(0.0) / (flux_average[branch] * 2.5 + 0.018))
                        .clamp(0.0, 1.0);
                    flux_average[branch] += (rise - flux_average[branch]) * 0.07;
                    previous[branch] = energy;
                    let attack = audio.attacks[region] * (-audio.attack_age[region] * 14.0).exp();
                    let impulse = onset.max(attack * 0.65);
                    let phase = self.particles[branch + 300].phase;
                    // A fast damped spring, independently excited by each branch's
                    // spectrum. Impulses change velocity, not position: no teleports.
                    let direction = (phase + state.flow * 1.7).sin();
                    state.velocity += impulse * direction * 5.5;
                    let target = (energy - 0.25) * (phase + state.flow * 0.6).sin() * 1.4;
                    for _ in 0..3 {
                        let dt = 1.0 / (FPS * 3) as f64;
                        state.velocity +=
                            ((target - state.bend) * 110.0 - state.velocity * 11.0) * dt;
                        state.bend += state.velocity * dt;
                    }
                    state.energy +=
                        (energy - state.energy) * if energy > state.energy { 0.6 } else { 0.18 };
                    state.kick = impulse.max(state.kick * 0.72);
                    state.flow += (0.12 + state.energy * 1.6 + state.kick * 4.5) / FPS as f64;
                }
                states
            })
            .collect();
    }

    pub(super) fn filament_frame(&mut self, frame: usize, audio: AudioFrame) -> &[u8] {
        let time = frame as f64 / FPS as f64;
        self.neon_time = time;
        self.update_field(frame, audio);
        self.shade.fill(0.0);
        let variant = self.variant as usize % 3;
        let kind = match self.style.as_str() {
            "neural" => 1,
            "chaos" => 2,
            _ => 0,
        };
        let bass = self.response[..3].iter().map(|r| r.energy).sum::<f64>() / 3.0;
        let mid = self.response[3..8]
            .iter()
            .map(|r| r.displacement)
            .sum::<f64>()
            / 5.0;
        let high = self.response[8..].iter().map(|r| r.light).sum::<f64>() / 4.0;
        let attacks: [f64; 3] = std::array::from_fn(|region| {
            audio.attacks[region] * (-audio.attack_age[region] * 7.0).exp()
        });
        let travel = self.response.iter().map(|r| r.travel).sum::<f64>() / 12.0;
        let drift = time * 0.12 + self.phase;
        let center = (
            WIDTH as f64 * (0.5 + 0.055 * (drift * 0.73).sin()),
            HEIGHT as f64
                * (if self.cover.is_some() { 0.22 } else { 0.49 } + 0.045 * (drift * 0.59).cos()),
        );
        let expansion = self.spread * (0.70 + bass * 0.8 - attacks[0] * 0.15);
        // Bundles share a backbone, with depth-dependent curl and fine splitting.
        // Stronger local spectrum bands alter individual bundles, not just zoom.
        for strand in 0..216 {
            let p = self.particles[strand];
            let bundle = strand / 12;
            let b = self.particles[bundle + 300];
            let voice = self.response[bundle % 12];
            let phase = b.phase + self.phase;
            let motion = self
                .ink_motion
                .get(frame)
                .map(|states| states[bundle])
                .unwrap_or(BranchMotion {
                    bend: attacks[bundle % 3] * b.phase.sin() * 0.8,
                    kick: attacks[bundle % 3],
                    energy: voice.energy,
                    flow: travel,
                    ..BranchMotion::default()
                });
            let clock = motion.flow * (0.9 + b.depth * 0.7) + time * 0.09;
            let reach = (48.0 + b.u.powi(3) * 480.0)
                * expansion
                * (0.8 + motion.energy * 0.55 + motion.bend * 0.18);
            let spread = (p.u - 0.5) * (0.012 + p.depth * 0.07);
            let rotation = phase + clock * 0.22 + motion.bend * 1.1;
            let (sn, cs) = rotation.sin_cos();
            let mut previous = None;
            for step in 0..128 {
                let s = step as f64 / 127.0;
                let envelope = s.powf(1.75);
                let bend = (s * 9.0 + phase + clock).sin();
                let curl = (s * 19.0 - clock * 1.3 + phase).sin();
                let local = (voice.displacement * 0.4 + motion.bend * 2.0)
                    * s
                    * (s * 11.0 + phase - clock * 2.5).sin()
                    + motion.kick * 0.65 * s * (s * 17.0 + phase - clock * 4.0).sin();
                let (x, y) = match kind {
                    1 => {
                        // Branching dendrites: related twigs separate gradually.
                        let branch = (s * 5.0 + phase + clock * 0.4).sin();
                        let split = (s - 0.35).max(0.0).powi(2);
                        (
                            reach * envelope,
                            reach
                                * (0.12 * bend * s
                                    + 0.22 * branch * split
                                    + spread * split * 9.0
                                    + local * 0.13),
                        )
                    }
                    2 => {
                        // Folded, precessing orbits with an irregular dense focus.
                        let angle = s * (8.0 + b.v * 12.0) + clock + phase;
                        let radius = reach * envelope * (0.65 + 0.3 * bend);
                        (
                            radius * angle.cos(),
                            radius * angle.sin() * (0.4 + b.depth * 0.5)
                                + reach * spread * curl * s
                                + local * 16.0,
                        )
                    }
                    _ => {
                        let angle = s * (3.0 + b.v * 5.0) + bend * (0.6 + mid) + clock;
                        (
                            reach * envelope * angle.cos(),
                            reach * envelope * angle.sin() * 0.65
                                + reach * (curl * 0.075 * s + spread * s + local * 0.12),
                        )
                    }
                };
                let flutter = (s * 61.0 + phase + p.phase * 0.15 - clock * 8.0).sin()
                    * (0.5 + high * 7.0 + motion.kick * 5.0)
                    * s;
                // Small-scale eddies dominate near the nucleus, then unravel
                // into long clean tails. Neighboring strands share the large
                // flow but separate inside these vortices like suspended ink.
                let knot = (s * 11.0).min(1.0) * (-s * 5.5).exp();
                let turbulence = (6.0 + b.v * 19.0 + high * 15.0 + motion.kick * 22.0) * knot;
                let eddy = s * (64.0 + b.depth * 39.0) + phase - clock * 1.7;
                let whipping = reach * motion.bend * 0.24 * s.powi(2);
                let x = x
                    + flutter
                    + turbulence * (eddy + p.phase * 0.4).sin()
                    + whipping * (s * 8.0 - clock * 3.0 + phase).cos();
                let y = y
                    + whipping * (s * 9.0 - clock * 3.0 + phase).sin()
                    + flutter * (p.phase + s * 7.0).sin()
                    + turbulence * (eddy * 0.73 + curl + p.phase * 0.4).cos();
                let turn = if variant == 1 { 0.65 * s.sin() } else { 0.0 };
                let point = (
                    center.0 + cs * x - sn * y + turn * y,
                    center.1 + sn * x + cs * y * if variant == 1 { 1.3 } else { 1.0 },
                );
                if let Some((px, py)) = previous {
                    // Far filaments dissolve into paper; nearby strands accumulate
                    // into ink instead of becoming additive neon/glowing strokes.
                    let opacity =
                        (0.045 + p.depth * 0.12) * (1.0 - s).powf(0.8) * (0.7 + voice.energy * 0.6);
                    self.ink_segment((px, py), point, opacity as f32);
                }
                previous = Some(point);
            }
        }
        let inverse = variant == 2;
        let density = &self.shade;
        let player = self.cover.is_some();
        parallel_rows(&mut self.rgb, WIDTH * 3, |y, row| {
            for (x, pixel) in row.as_chunks_mut::<3>().0.iter_mut().enumerate() {
                let i = y * WIDTH + x;
                // Small diffusion halo, retaining the crisp subpixel ink core.
                let mut mist = 0.0;
                for (dx, dy) in [(-2, 0), (2, 0), (0, -2), (0, 2)] {
                    let xx = (x as isize + dx).clamp(0, WIDTH as isize - 1) as usize;
                    let yy = (y as isize + dy).clamp(0, HEIGHT as isize - 1) as usize;
                    mist += density[yy * WIDTH + xx];
                }
                let ink = 1.0 - (-(density[i] * 1.7 + mist * 0.10)).exp();
                let value = if inverse {
                    8.0 + ink * 239.0
                } else {
                    250.0 - ink * 239.0
                };
                // Snippets overlay a fixed white player. Fade only its metadata
                // area to charcoal; raw previews retain the reference's paper.
                let scrim = if player {
                    (-((x as f64 - 270.0) / 290.0).powi(4) - ((y as f64 - 615.0) / 100.0).powi(4))
                        .exp() as f32
                } else {
                    0.0
                };
                let value = value * (1.0 - scrim * 0.94);
                pixel.fill(value.clamp(0.0, 255.0) as u8);
            }
        });
        &self.rgb
    }

    fn ink_segment(&mut self, from: (f64, f64), to: (f64, f64), opacity: f32) {
        let dx = to.0 - from.0;
        let dy = to.1 - from.1;
        let length = dx.hypot(dy);
        // No clamped edge deposits: offscreen trajectories simply disappear.
        let steps = length.ceil().max(1.0) as usize;
        for step in 0..steps {
            let t = (step as f64 + 0.5) / steps as f64;
            let x = from.0 + t * dx;
            let y = from.1 + t * dy;
            if x < 0.0 || y < 0.0 || x >= (WIDTH - 1) as f64 || y >= (HEIGHT - 1) as f64 {
                continue;
            }
            let (ix, iy) = (x as usize, y as usize);
            let (fx, fy) = ((x - ix as f64) as f32, (y - iy as f64) as f32);
            let weight = opacity * (length / steps as f64) as f32;
            let i = iy * WIDTH + ix;
            self.shade[i] += weight * (1.0 - fx) * (1.0 - fy);
            self.shade[i + 1] += weight * fx * (1.0 - fy);
            self.shade[i + WIDTH] += weight * (1.0 - fx) * fy;
            self.shade[i + WIDTH + 1] += weight * fx * fy;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn isolated_hit_whips_branches_immediately_and_then_settles() {
        let mut frames = vec![AudioFrame::default(); 150];
        for region in 0..3 {
            let hit = 20 + region * 35;
            for age in 0..12 {
                let audio = &mut frames[hit + age];
                audio.bands[[0..16, 16..40, 40..64][region].clone()]
                    .fill(0.9 * (-(age as f64) / 4.0).exp());
                audio.attacks[region] = 0.9;
                audio.attack_age[region] = age as f64 / FPS as f64;
            }
        }
        let mut painter = Painter::new("filament", 813, 0);
        painter.prepare_ink_motion(&frames);
        let active = painter.ink_motion.clone();
        painter.prepare_ink_motion(&vec![AudioFrame::default(); frames.len()]);
        for region in [0, 1, 2] {
            let hit = 20 + region * 35;
            // No future anticipation; the transient must move its branches in
            // 2 frames, rather than merely changing a still three seconds later.
            for (a, b) in active.iter().zip(&painter.ink_motion).take(20) {
                assert_eq!(a[region].bend, b[region].bend);
            }
            let delta =
                (active[hit + 2][region].bend - painter.ink_motion[hit + 2][region].bend).abs();
            assert!(delta > 0.07, "slow branch attack: {region}/{delta}");
            let late =
                (active[hit + 30][region].bend - painter.ink_motion[hit + 30][region].bend).abs();
            assert!(late < 0.25, "branch does not settle: {region}/{late}");
        }
        for style in ["filament", "neural", "chaos"] {
            let mut painter = Painter::new(style, 813, 0);
            painter.prepare_audio(&frames);
            let before = painter.frame(19, frames[19]).to_vec();
            let after = painter.frame(22, frames[22]).to_vec();
            let movement = before
                .iter()
                .zip(&after)
                .filter(|(a, b)| a.abs_diff(**b) > 16)
                .count();
            let mut silent = Painter::new(style, 813, 0);
            silent.prepare_audio(&vec![AudioFrame::default(); frames.len()]);
            let still = silent.frame(19, AudioFrame::default()).to_vec();
            let idle = silent.frame(22, AudioFrame::default());
            let idle_movement = still
                .iter()
                .zip(idle)
                .filter(|(a, b)| a.abs_diff(**b) > 16)
                .count();
            assert!(
                movement > idle_movement * 2 + 1500,
                "attack motion not stronger than idle: {style}/{movement}/{idle_movement}"
            );
        }
    }

    #[test]
    fn each_frequency_region_moves_ink_and_player_stays_readable() {
        for style in ["filament", "neural", "chaos"] {
            let mut painter = Painter::new(style, 813, 0);
            painter.prepare_audio(&vec![AudioFrame::default(); 91]);
            let baseline = painter.frame(90, AudioFrame::default()).to_vec();
            for region in 0..3 {
                let mut audio = AudioFrame::default();
                audio.bands[[0..16, 16..40, 40..64][region].clone()].fill(0.8);
                audio.attacks[region] = 0.8;
                painter.prepare_audio(&vec![audio; 91]);
                let active = painter.frame(90, audio);
                let changed = baseline
                    .iter()
                    .zip(active)
                    .filter(|(a, b)| a.abs_diff(**b) > 8)
                    .count();
                assert!(changed > 3000, "{style} region {region}: {changed}");
            }
            painter.set_cover(&image::DynamicImage::new_rgb8(8, 8));
            let composed = painter.frame(90, AudioFrame::default());
            for y in [565, 605, 642, 660] {
                assert!(composed[(y * WIDTH + WIDTH / 2) * 3] < 80);
            }
        }
    }

    #[test]
    fn ink_styles_are_seekable_reactive_and_distinct() {
        let active = AudioFrame {
            bands: [0.65; BANDS],
            level: 0.7,
            bass: 0.7,
            treble: 0.5,
            beat: 0.8,
            attacks: [0.8; 3],
            ..AudioFrame::default()
        };
        let mut hashes = std::collections::HashSet::new();
        for style in ["filament", "neural", "chaos"] {
            for variant in 0..3 {
                let mut painter = Painter::new(style, 813, variant);
                painter.prepare_audio(&vec![active; 91]);
                let first = painter.frame(60, active).to_vec();
                assert_eq!(first.len(), WIDTH * HEIGHT * 3);
                painter.frame(10, active);
                assert_eq!(first, painter.frame(60, active), "seek: {style}/{variant}");
                assert!(
                    first
                        .as_chunks::<3>()
                        .0
                        .iter()
                        .all(|p| p[0] == p[1] && p[1] == p[2])
                );
                let dark = first
                    .as_chunks::<3>()
                    .0
                    .iter()
                    .filter(|p| p[0] < 120)
                    .count();
                assert!(dark > 100 && dark < WIDTH * HEIGHT - 100);
                let mut hash = 0_u64;
                for value in &first {
                    hash = hash.wrapping_mul(31).wrapping_add(*value as u64);
                }
                assert!(hashes.insert(hash), "duplicate {style}/{variant}");
                painter.prepare_audio(&vec![AudioFrame::default(); 91]);
                let silent = painter.frame(60, AudioFrame::default());
                let changed = first
                    .iter()
                    .zip(silent)
                    .filter(|(a, b)| a.abs_diff(**b) > 8)
                    .count();
                assert!(changed > 3000, "weak response {style}/{variant}: {changed}");
            }
        }
    }
}
