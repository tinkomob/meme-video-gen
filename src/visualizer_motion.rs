use super::*;

const VOICES: usize = 12;
const GRID_W: usize = 19;
const GRID_H: usize = 33;

#[derive(Clone, Copy, Debug, Default)]
pub(super) struct Direction {
    pub(super) energy: f64,
    pub(super) space: f64,
    pub(super) tension: f64,
    pub(super) climax: f64,
    pub(super) change: f64,
}

fn direct(frames: &[AudioFrame]) -> Vec<Direction> {
    if frames.is_empty() {
        return Vec::new();
    }
    let window = |i: usize, radius: usize| {
        &frames[i.saturating_sub(radius)..(i + radius + 1).min(frames.len())]
    };
    let energy: Vec<_> = (0..frames.len())
        .map(|i| {
            let w = &frames[i.saturating_sub(FPS)..=i];
            w.iter()
                .map(|a| a.level * 0.6 + a.bands.iter().sum::<f64>() / BANDS as f64 * 0.4)
                .sum::<f64>()
                / w.len() as f64
        })
        .collect();
    let novelty: Vec<_> = (0..frames.len())
        .map(|i| {
            let before = window(i.saturating_sub(FPS / 2), FPS / 4);
            let after = window((i + FPS / 2).min(frames.len() - 1), FPS / 4);
            (0..BANDS)
                .map(|b| {
                    (before.iter().map(|a| a.bands[b]).sum::<f64>() / before.len() as f64
                        - after.iter().map(|a| a.bands[b]).sum::<f64>() / after.len() as f64)
                        .abs()
                })
                .sum::<f64>()
                / BANDS as f64
        })
        .collect();
    let maximum = energy.iter().copied().fold(0.0, f64::max);
    let interior =
        &energy[FPS.min(frames.len())..frames.len().saturating_sub(FPS).max(FPS.min(frames.len()))];
    let interior_max = interior.iter().copied().fold(0.0, f64::max);
    let minimum = interior.iter().copied().fold(1.0, f64::min);
    let novelty_peak = novelty.iter().copied().fold(0.0, f64::max);
    let mut candidates: Vec<_> = (FPS..frames.len().saturating_sub(FPS))
        .filter(|i| novelty[*i] > 0.04_f64.max(novelty_peak * 0.45))
        .collect();
    candidates.sort_by(|a, b| novelty[*b].total_cmp(&novelty[*a]));
    let mut sections = Vec::new();
    for i in candidates {
        if sections.iter().all(|s: &usize| s.abs_diff(i) >= FPS * 2) {
            sections.push(i);
        }
    }
    let climax = (FPS..frames.len().saturating_sub(FPS))
        .max_by(|a, b| {
            let score = |i: usize| {
                energy[i]
                    + (energy[i] - energy[i.saturating_sub(FPS * 2)]).max(0.0) * 1.5
                    + frames[i].attacks.iter().sum::<f64>() * 0.04
            };
            score(*a).total_cmp(&score(*b))
        })
        .filter(|_| interior_max - minimum > 0.08);
    (0..frames.len())
        .map(|i| {
            let current = energy[i];
            let future = energy[(i + FPS).min(frames.len() - 1)];
            let peak_distance = climax.map_or(100.0, |peak| (i as f64 - peak as f64) / FPS as f64);
            Direction {
                energy: current,
                space: (1.0 - current / maximum.max(0.01)).clamp(0.0, 1.0),
                tension: ((future - current).max(0.0) * 2.0).min(1.0),
                climax: (-peak_distance.powi(2) / 0.65).exp(),
                change: sections
                    .iter()
                    .map(|s| {
                        let distance = (i as f64 - *s as f64) / FPS as f64;
                        (-distance.powi(2) / 0.5).exp() * novelty[*s] / novelty_peak.max(0.01)
                    })
                    .fold(0.0, f64::max),
            }
        })
        .collect()
}

#[derive(Clone, Copy, Default)]
pub(super) struct Response {
    pub(super) energy: f64,
    pub(super) attack: f64,
    pub(super) travel: f64,
    pub(super) opening: f64,
    pub(super) light: f64,
    pub(super) displacement: f64,
    pub(super) velocity: f64,
}
#[derive(Clone, Copy, Default)]
pub(super) struct Field {
    pub(super) dx: f64,
    pub(super) dy: f64,
    pub(super) energy: f64,
    pub(super) travel: f64,
    pub(super) opening: f64,
    pub(super) light: f64,
    pub(super) color: [f64; 3],
}

pub(super) fn hue_color(hue: f64) -> Color {
    let channel = |offset: f64| {
        let wave = ((hue + offset).rem_euclid(1.0) * 6.0 - 3.0).abs() - 1.0;
        16.0 + 229.0 * wave.clamp(0.0, 1.0)
    };
    Color(channel(0.0), channel(2.0 / 3.0), channel(1.0 / 3.0))
}

impl Painter {
    pub(super) fn musical_pose(&self, frame: usize, mut pose: Pose) -> Pose {
        let energy = |responses: &[Response; VOICES]| {
            responses.iter().map(|r| r.opening).sum::<f64>() / VOICES as f64
        };
        let current = energy(&self.response);
        let upcoming = self.responses.get(frame + FPS / 2).map_or(current, energy);
        // The entire clip is known: gently gather the scene before a rise.
        let gathering = (upcoming - current).max(0.0);
        pose.zoom *= 0.96 + current * 0.12 - gathering * 0.08;
        pose.unfold = (pose.unfold * 0.7 + current * 0.5).min(1.0);
        pose.density = (pose.density * 0.65 + current * 0.5 - gathering * 0.15).clamp(0.0, 1.0);
        if let Some(d) = self.direction.get(frame) {
            pose.zoom *= 1.0 + d.climax * 0.1 - d.tension * 0.06 - d.space * 0.035;
            pose.unfold = (pose.unfold + d.climax * 0.22 - d.tension * 0.12).clamp(0.0, 1.0);
            pose.density = (pose.density + d.energy * 0.12 - d.space * 0.15).clamp(0.0, 1.0);
            pose.color = (pose.color * 0.75 + d.change * 0.25).clamp(0.0, 1.0);
            pose.turn += d.change * 0.035 * self.phase.sin();
        }
        pose
    }

    pub(super) fn elastic(&self) -> bool {
        matches!(
            self.style.as_str(),
            "silver" | "amber" | "aurora" | "wave" | "silk" | "analog"
        )
    }

    pub(super) fn shape_audio(&self, mut a: AudioFrame) -> AudioFrame {
        if !self.elastic() {
            a.bands = std::array::from_fn(|band| {
                self.response[(band * VOICES / BANDS).min(VOICES - 1)].opening
            });
            a.level = self.response.iter().map(|r| r.opening).sum::<f64>() / VOICES as f64;
            a.bass = a.bands[..16].iter().sum::<f64>() / 16.0;
            a.treble = a.bands[40..].iter().sum::<f64>() / 24.0;
            a.beat = 0.0;
            a.event = 0;
            a.event_age = 1.0;
        }
        a
    }

    fn voice_drive(&self, a: AudioFrame, voice: usize) -> f64 {
        let first = (voice * BANDS / VOICES + (self.phase * 7.0) as usize) % BANDS;
        let energy = (0..6).map(|i| a.bands[(first + i) % BANDS]).sum::<f64>() / 6.0;
        let location = voice as f64 / VOICES as f64;
        (energy * (0.7 + 0.3 * self.particles[voice].depth) + a.beat * band_at(&a, location) * 0.25)
            .clamp(0.0, 1.0)
    }

    pub fn prepare_audio(&mut self, frames: &[AudioFrame]) {
        self.direction = direct(frames);
        let drives: Vec<[f64; VOICES]> = (0..frames.len())
            .map(|frame| {
                std::array::from_fn(|voice| {
                    let lag = (self.particles[voice].v * 5.0) as usize;
                    frame
                        .checked_sub(lag)
                        .map_or(0.0, |i| self.voice_drive(frames[i], voice))
                })
            })
            .collect();
        let onset_scale: [f64; VOICES] = std::array::from_fn(|voice| {
            // Exclude the opening fade from accent calibration.
            let mut rises: Vec<_> = drives
                .windows(2)
                .skip(FPS)
                .map(|pair| pair[1][voice] - pair[0][voice])
                .filter(|rise| *rise > 0.008)
                .collect();
            rises.sort_by(f64::total_cmp);
            rises
                .get(rises.len() * 9 / 10)
                .copied()
                .unwrap_or(0.02)
                .max(0.02)
        });
        let mut states = [Response::default(); VOICES];
        self.responses = frames
            .iter()
            .enumerate()
            .map(|(frame, _)| {
                for (voice, state) in states.iter_mut().enumerate() {
                    let p = self.particles[voice];
                    let drive = drives[frame][voice];
                    let previous = frame.checked_sub(1).map_or(drive, |i| drives[i][voice]);
                    let onset = ((drive - previous - 0.008).max(0.0) / onset_scale[voice])
                        .min(1.0)
                        .powi(2);
                    let rate = if drive > state.energy {
                        0.12 + p.u * 0.2
                    } else {
                        0.025 + p.v * 0.055
                    };
                    state.attack = onset.max(state.attack * 0.58);
                    state.energy += (drive - state.energy) * rate;
                    state.opening += (state.energy - state.opening) * 0.025;
                    let target = state.attack.min(1.5);
                    let light_rate = if target > state.light { 0.65 } else { 0.18 };
                    state.light += (target - state.light) * light_rate;
                    // Accents change speed, never position: the flow cannot kick backwards.
                    state.travel +=
                        (0.18 + state.energy * 0.8) * (1.0 + state.light * 3.0) / FPS as f64;
                    let stiffness = 7.0 + p.depth * 18.0;
                    state.velocity += (stiffness * (state.energy - state.displacement)
                        - (2.0 + p.u * 3.0) * state.velocity)
                        / FPS as f64;
                    state.displacement += state.velocity / FPS as f64;
                }
                states
            })
            .collect();
    }

    pub(super) fn update_field(&mut self, frame: usize, a: AudioFrame) {
        self.response = self.responses.get(frame).copied().unwrap_or_else(|| {
            std::array::from_fn(|i| Response {
                energy: self.voice_drive(a, i),
                attack: a.beat * band_at(&a, i as f64 / VOICES as f64),
                travel: self.neon_time * (0.18 + self.voice_drive(a, i) * 0.8),
                opening: self.voice_drive(a, i),
                light: a.beat * band_at(&a, i as f64 / VOICES as f64),
                displacement: self.voice_drive(a, i),
                velocity: 0.0,
            })
        });
        let t = self.neon_time;
        let centers: [_; VOICES] = std::array::from_fn(|i| {
            let p = self.particles[i];
            (
                (i % 3) as f64 / 2.0 * 540.0 + (p.u - 0.5) * 100.0,
                (i / 3) as f64 / 3.0 * 960.0 + (p.v - 0.5) * 120.0,
            )
        });
        for gy in 0..GRID_H {
            for gx in 0..GRID_W {
                let x = gx as f64 * WIDTH as f64 / (GRID_W - 1) as f64;
                let y = gy as f64 * HEIGHT as f64 / (GRID_H - 1) as f64;
                let mut field = Field::default();
                let mut weight = 0.0;
                for (i, (cx, cy)) in centers.iter().copied().enumerate() {
                    let p = self.particles[i];
                    let r = self.response[i];
                    let (dx, dy) = ((x - cx) / 165.0, (y - cy) / 230.0);
                    let influence = (-dx * dx - dy * dy).exp();
                    let time = r.travel * (0.6 + p.u * 0.5);
                    let ripple = (dx * 2.7 - dy * 1.9 + p.phase).sin();
                    let flex = if self.elastic() {
                        r.displacement
                    } else {
                        r.opening * 0.2
                    };
                    field.dx += influence * (32.0 * (time + p.phase).sin() + 35.0 * flex * ripple);
                    field.dy += influence
                        * (38.0 * (time * 0.73 - p.phase).sin()
                            + 30.0 * flex * (dy * 2.3 + p.phase).cos());
                    field.energy += influence * r.energy;
                    field.travel += influence * r.travel;
                    field.opening += influence * r.opening;
                    field.light = field.light.max(influence * r.light);
                    let color = mix_light(
                        self.palette[i % 3],
                        hue_color(p.u + t * (0.006 + p.depth * 0.009) + r.opening * 0.12),
                        0.65,
                    );
                    for (out, c) in field.color.iter_mut().zip([color.0, color.1, color.2]) {
                        *out += influence * c;
                    }
                    weight += influence;
                }
                field.dx /= weight;
                field.dy /= weight;
                field.energy /= weight;
                field.travel /= weight;
                field.opening /= weight;
                field.color.iter_mut().for_each(|c| *c /= weight);
                self.field[gy * GRID_W + gx] = field;
            }
        }
    }

    pub(super) fn field_at(&self, x: f64, y: f64) -> Field {
        let u = (x / WIDTH as f64).clamp(0.0, 1.0) * (GRID_W - 1) as f64;
        let v = (y / HEIGHT as f64).clamp(0.0, 1.0) * (GRID_H - 1) as f64;
        let ix = (u as usize).min(GRID_W - 2);
        let iy = (v as usize).min(GRID_H - 2);
        let (u, v) = (u - ix as f64, v - iy as f64);
        let mut out = Field::default();
        for (dx, dy, w) in [
            (0, 0, (1.0 - u) * (1.0 - v)),
            (1, 0, u * (1.0 - v)),
            (0, 1, (1.0 - u) * v),
            (1, 1, u * v),
        ] {
            let f = self.field[(iy + dy) * GRID_W + ix + dx];
            out.dx += f.dx * w;
            out.dy += f.dy * w;
            out.energy += f.energy * w;
            out.travel += f.travel * w;
            out.opening += f.opening * w;
            out.light += f.light * w;
            for c in 0..3 {
                out.color[c] += f.color[c] * w;
            }
        }
        out
    }

    pub(super) fn local_color(&self, original: Color, f: Field, depth: f64) -> Color {
        let local = Color(f.color[0], f.color[1], f.color[2]);
        mix_light(original, local, (0.65 + 0.15 * depth).clamp(0.0, 1.0))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn direction_finds_changes_peak_build_and_silence_without_inventing_constant_track_events() {
        assert!(direct(&[]).is_empty());
        for count in [1, 30, 600] {
            let constant = AudioFrame {
                level: 0.5,
                bands: [0.4; BANDS],
                ..AudioFrame::default()
            };
            assert!(
                direct(&vec![constant; count])
                    .iter()
                    .all(|d| d.climax == 0.0 && d.change == 0.0 && d.tension == 0.0)
            );
        }
        let mut frames = vec![AudioFrame::default(); 600];
        for (i, a) in frames.iter_mut().enumerate().take(450).skip(90) {
            a.level = if i >= 270 { 0.9 } else { 0.3 };
            if i < 270 {
                a.bands[..24].fill(0.4);
            } else {
                a.bands[32..].fill(0.9);
            }
        }
        let d = direct(&frames);
        assert!(
            d[240..270].iter().any(|d| d.tension > 0.3),
            "missing buildup"
        );
        assert!(
            d[270..360].iter().any(|d| d.climax > 0.9),
            "missing strongest phrase"
        );
        assert!(
            d[255..285].iter().any(|d| d.change > 0.8),
            "missing spectral change"
        );
        assert!(
            d[520].space > 0.99 && d[520].energy == 0.0,
            "pause never opens space"
        );
        for pair in d.windows(2) {
            assert!((pair[0].climax - pair[1].climax).abs() < 0.08);
            assert!((pair[0].change - pair[1].change).abs() < 0.08);
        }
        let shifted: Vec<_> = std::iter::repeat_n(AudioFrame::default(), 45)
            .chain(frames)
            .collect();
        let shifted = direct(&shifted);
        assert!(
            (shifted[345].climax - d[300].climax).abs() < 1e-9,
            "climax is tied to a fixed time"
        );
    }

    #[test]
    fn composition_follows_phrases_and_gathers_before_a_rise() {
        let mut p = Painter::new("liquid", 71, 1);
        let mut audio = vec![AudioFrame::default(); 300];
        for a in &mut audio[90..180] {
            a.bands.fill(0.8);
        }
        p.prepare_audio(&audio);
        let pose = Pose {
            zoom: 1.0,
            density: 0.5,
            ..Pose::default()
        };
        p.update_field(60, audio[60]);
        let quiet = p.musical_pose(60, pose);
        p.update_field(88, audio[88]);
        let before = p.musical_pose(88, pose);
        p.update_field(160, audio[160]);
        let loud = p.musical_pose(160, pose);
        assert!(before.zoom < quiet.zoom, "no anticipation");
        assert!(
            loud.zoom > quiet.zoom && loud.density > quiet.density && loud.unfold > quiet.unfold
        );
        p.update_field(299, audio[299]);
        assert!(p.musical_pose(299, pose).zoom.is_finite());
    }

    #[test]
    fn voices_select_frequencies_keep_inertia_and_replay_identically() {
        let mut p = Painter::new("liquid", 43, 1);
        for band in 0..BANDS {
            let mut a = AudioFrame::default();
            a.bands[band] = 1.0;
            let active = (0..VOICES).filter(|i| p.voice_drive(a, *i) > 0.01).count();
            assert!(
                (1..=3).contains(&active),
                "missing or global response to band {band}"
            );
        }
        let mut frames = vec![AudioFrame::default(); 180];
        let first = (p.phase * 7.0) as usize % BANDS;
        for frame in &mut frames[10..20] {
            for i in 0..5 {
                frame.bands[(first + i) % BANDS] = 1.0;
            }
        }
        p.prepare_audio(&frames);
        let active: Vec<_> = p.responses[25].iter().map(|r| r.energy).collect();
        assert!(active[0] > 0.1);
        assert!(
            active.iter().filter(|v| **v > 0.01).count() <= 3,
            "a narrow frequency excites all bodies"
        );
        assert!(
            p.responses[35][0].displacement > p.responses[15][0].displacement,
            "the body has no inertia after an attack"
        );
        assert!(p.responses[179][0].energy < 0.01);
        assert!(
            p.responses.iter().flatten().all(|r| r.energy.is_finite()
                && r.displacement.is_finite()
                && r.velocity.is_finite())
        );
        let first = p.frame(40, frames[40]).to_vec();
        p.frame(160, frames[160]);
        assert_eq!(first, p.frame(40, frames[40]));

        for frame in &mut frames {
            frame.bands.fill(0.6);
        }
        p.prepare_audio(&frames);
        let times: std::collections::HashSet<_> = p.responses[4]
            .iter()
            .map(|r| (r.displacement * 100000.0) as i64)
            .collect();
        assert!(times.len() > 6, "broadband attacks move bodies in lockstep");
    }

    #[test]
    fn flow_keeps_advancing_and_only_elastic_materials_recoil() {
        for style in ["nebula", "liquid", "bloom", "chrome", "prism", "silk"] {
            let mut p = Painter::new(style, 71, 1);
            let a = AudioFrame {
                bands: [0.5; BANDS],
                ..AudioFrame::default()
            };
            p.prepare_audio(&vec![a; 180]);
            assert!(p.responses.windows(2).all(|pair| {
                pair[1].iter().zip(pair[0]).all(|(now, before)| {
                    now.travel > before.travel && (now.opening - before.opening).abs() < 0.025
                })
            }));
            let first = p.frame(106, a).to_vec();
            for r in &mut p.responses[106] {
                r.displacement += 3.0;
                r.velocity += 30.0;
                r.attack += 3.0;
            }
            let second = p.frame(106, a);
            if style == "silk" {
                assert!(first != second, "strings lost their vibration");
            } else {
                assert_eq!(first, second, "spring recoil leaked into {style}");
            }
        }
        let mut p = Painter::new("nebula", 71, 1);
        let mut pulse = vec![AudioFrame::default(); 180];
        for a in &mut pulse[30..60] {
            a.bands.fill(0.8);
        }
        p.prepare_audio(&pulse);
        assert!(p.responses[40].iter().any(|r| r.light > 0.1));
        assert!(p.responses[179].iter().all(|r| r.light < 0.01));
        assert!(
            p.responses[100]
                .iter()
                .any(|r| r.travel > 101.0 * 0.18 / FPS as f64 + 0.2),
            "music no longer changes the flow"
        );
    }

    #[test]
    fn small_accents_in_compressed_music_remain_visible_without_exciting_every_voice() {
        let mut p = Painter::new("nebula", 71, 1);
        let mut frames = vec![AudioFrame::default(); 360];
        let first = (p.phase * 7.0) as usize % BANDS;
        for (f, a) in frames.iter_mut().enumerate() {
            for band in 0..6 {
                a.bands[(first + band) % BANDS] = if f >= 90 && f % 30 < 4 { 0.47 } else { 0.42 };
            }
        }
        p.prepare_audio(&frames);
        let peak = p.responses[90..]
            .iter()
            .flat_map(|r| r.iter())
            .map(|r| r.light)
            .fold(0.0, f64::max);
        assert!(peak > 0.35, "compressed track loses its accents: {peak}");
        assert!(
            p.responses[80].iter().all(|r| r.light < 0.01),
            "steady notes create false flashes"
        );
        assert!(
            p.responses[90..]
                .iter()
                .all(|r| r.iter().filter(|r| r.light > 0.1).count() <= 3),
            "a local accent lights the whole scene"
        );
        let boost = p
            .responses
            .windows(2)
            .skip(90)
            .flat_map(|r| {
                r[1].iter()
                    .zip(r[0])
                    .map(|(a, b)| (a.travel - b.travel) * FPS as f64 / (0.18 + a.energy * 0.8))
            })
            .fold(0.0, f64::max);
        assert!(
            boost > 2.0 && boost <= 4.01,
            "missing or excessive speed accent: {boost}"
        );
    }

    #[test]
    fn local_motion_is_non_rigid_and_colors_change_independently() {
        let mut p = Painter::new("prism", 71, 1);
        p.neon_time = 0.0;
        p.update_field(0, AudioFrame::default());
        let a = p.field_at(40.0, 50.0);
        let b = p.field_at(490.0, 880.0);
        let distance = |a: Field, b: Field| (450.0 + b.dx - a.dx).hypot(830.0 + b.dy - a.dy);
        let initial = distance(a, b);
        let color_distance =
            |a: [f64; 3], b: [f64; 3]| a.iter().zip(b).map(|(a, b)| (a - b).abs()).sum::<f64>();
        assert!(color_distance(a.color, b.color) > 45.0);
        p.neon_time = 13.0;
        p.update_field(390, AudioFrame::default());
        let c = p.field_at(40.0, 50.0);
        let d = p.field_at(490.0, 880.0);
        assert!(
            (initial - distance(c, d)).abs() > 3.0,
            "only a rigid transform: {} vs {}",
            initial,
            distance(c, d)
        );
        assert!(color_distance(a.color, c.color) > 30.0);
        assert!(color_distance(b.color, d.color) > 30.0);
        assert!(p.field.iter().all(|f| f.dx.is_finite()
            && f.dy.is_finite()
            && f.color.iter().all(|c| c.is_finite())));
    }
}
