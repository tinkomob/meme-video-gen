//! Dark matte tunnel with sparse, delayed audio wave packets and 2x SSAA.
use super::*;

const SCALE: usize = 2;
const SW: usize = WIDTH * SCALE;
const SH: usize = HEIGHT * SCALE;
const NEAR: f64 = 0.18;
type Vec3 = [f64; 3];

fn noise(x: i32, z: i32, side: i32, seed: u64) -> f64 {
    let mut h = seed
        ^ (x as u64).wrapping_mul(0x9e3779b97f4a7c15)
        ^ (z as u64).wrapping_mul(0xbf58476d1ce4e5b9)
        ^ (side as u64).wrapping_mul(0x94d049bb133111eb);
    h = (h ^ (h >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    h = (h ^ (h >> 27)).wrapping_mul(0x94d049bb133111eb);
    ((h ^ (h >> 31)) >> 11) as f64 / (1_u64 << 53) as f64
}

// Three authored environments, not palette-only variants. Geometry is authored
// in canonical tunnel coordinates so packet sources still reach all four walls.
#[derive(Clone, Copy)]
struct CaveLook {
    scale: Vec3,
    pitch: f64,
    speed: f64,
    drift: [f64; 2],
    drift_rate: [f64; 2],
    wave_rate: f64,
    displacement: f64,
    palette: [[f64; 3]; 3],
}
fn cave_look(variant: usize) -> CaveLook {
    match variant % 3 {
        // Monumental, broad terraces; long, calm architectural flight.
        0 => CaveLook {
            scale: [1.12, 1.08, 1.22],
            pitch: 1.15,
            speed: 2.85,
            drift: [0.28, 0.10],
            drift_rate: [0.13, 0.17],
            wave_rate: 0.85,
            displacement: 0.72,
            palette: [
                [38.0, 207.0, 191.0],
                [113.0, 108.0, 237.0],
                [239.0, 164.0, 91.0],
            ],
        },
        // Intimate rhythmic vault, stronger stepped relief and compact pulses.
        1 => CaveLook {
            scale: [0.83, 0.94, 0.92],
            pitch: 1.05,
            speed: 3.35,
            drift: [0.37, 0.14],
            drift_rate: [0.22, 0.27],
            wave_rate: 1.13,
            displacement: 0.98,
            palette: [
                [23.0, 157.0, 243.0],
                [161.0, 54.0, 237.0],
                [244.0, 132.0, 42.0],
            ],
        },
        // Wide crystalline gallery: smaller facets, cool jewel illumination.
        _ => CaveLook {
            scale: [1.30, 1.02, 1.0],
            pitch: 0.78,
            speed: 3.05,
            drift: [0.43, 0.11],
            drift_rate: [0.15, 0.21],
            wave_rate: 0.96,
            displacement: 0.64,
            palette: [
                [40.0, 228.0, 163.0],
                [53.0, 130.0, 249.0],
                [219.0, 92.0, 231.0],
            ],
        },
    }
}
impl CaveLook {
    fn world(self, p: Vec3) -> Vec3 {
        std::array::from_fn(|c| p[c] * self.scale[c])
    }
    fn camera(self, time: f64, phase: f64) -> Vec3 {
        [
            (time * self.drift_rate[0] + phase).sin() * self.drift[0],
            (time * self.drift_rate[1] + phase * 0.3).sin() * self.drift[1],
            time * self.speed,
        ]
    }
}

// Smooth compact support, zero outside the interval (including derivatives).
fn window(value: f64) -> f64 {
    if value.abs() >= 1.0 {
        0.0
    } else {
        (0.5 + 0.5 * (PI * value).cos()).powi(2)
    }
}

// Each emission epoch owns its source for its entire lifetime. Old packets
// keep travelling from their original world position when the next one fires.
fn packet_source(region: usize, segment: i32, epoch: i32, seed: u64) -> Vec3 {
    let key = seed ^ (epoch as u64).wrapping_mul(0xd6e8feb86659fd93);
    let n = noise(region as i32, segment, 9, key);
    let wall = (noise(region as i32, segment, 10, key) * 4.0) as usize;
    let transverse = n * 2.0 - 1.0;
    let z = segment as f64 * 18.0 + 2.0 + noise(region as i32, segment, 13, key) * 13.0;
    match wall {
        0 => [transverse * 4.0, 3.2, z],
        1 => [transverse * 4.0, -3.2, z],
        2 => [-5.2, transverse * 2.5, z],
        _ => [5.2, transverse * 2.5, z],
    }
}

fn ambient_emission(p: Vec3, palette: [[f64; 3]; 3]) -> [f64; 3] {
    surface_tint(p, palette).map(|v| 2.0 + v * 0.045)
}

fn wave(p: Vec3, time: f64, audio: AudioFrame, seed: u64, history: &[AudioFrame]) -> [f64; 3] {
    std::array::from_fn(|region| {
        let (start, end) = [(0, 16), (16, 40), (40, BANDS)][region];
        let cell = (p[2] / 18.0).floor() as i32;
        let mut field = 0.0_f64;
        for segment in cell - 1..=cell + 1 {
            let period = 1.6 + noise(region as i32, segment, 11, seed) * 0.8;
            let phase = noise(region as i32, segment, 12, seed) * period;
            let latest = ((time + phase) / period).floor() as i32;
            // Maximum delay is 7.5 / 4s; retain overlapping epochs instead of
            // changing a source in place and teleporting its existing wave.
            for epoch in latest - 2..=latest {
                let source = packet_source(region, segment, epoch, seed);
                let n = noise(region as i32, segment, epoch, seed ^ 719);
                let distance = ((p[0] - source[0]).powi(2)
                    + (p[1] - source[1]).powi(2)
                    + (p[2] - source[2]).powi(2))
                .sqrt();
                // Hard upper bound on the spatial footprint even under DC audio.
                let spatial = window(distance / 6.8);
                if spatial == 0.0 {
                    continue;
                }
                let source_time = time - distance / (4.0 + region as f64 * 0.65);
                // Each source emits only one short packet per independent cycle.
                // Retarded source time makes its envelope physically travel outward.
                let age = source_time - (epoch as f64 * period - phase);
                let gate = window((age - 0.55) / 0.55);
                if gate == 0.0 {
                    continue;
                }
                if source_time < 0.0 {
                    continue;
                }
                let drive = |signal: AudioFrame| {
                    // Local voices retain their own spectral envelope, rather than
                    // reducing the whole frequency region to a single DC average.
                    let first = start + (n * (end - start - 4) as f64) as usize;
                    let energy = signal.bands[first..first + 4].iter().sum::<f64>() / 4.0;
                    let attack = signal.attacks[region] * (-signal.attack_age[region] * 7.0).exp();
                    let crest = (0.5 + 0.5 * (distance * 1.5 - source_time * 3.8).cos()).powi(2);
                    // Onsets bypass the clock gate, but NEVER the compact spatial
                    // support. Music decides when a wave fires, even between cycles.
                    (energy * gate * 0.75 * (0.35 + crest * 0.65)
                        + attack * gate * 1.5 * window(distance / 3.5))
                    .clamp(0.0, 1.0)
                };
                let amplitude = if history.is_empty() {
                    drive(audio)
                } else {
                    let position = source_time * FPS as f64;
                    let index = position.floor() as usize;
                    let a = history.get(index).copied().unwrap_or_default();
                    let b = history.get(index + 1).copied().unwrap_or(a);
                    drive(a) + (drive(b) - drive(a)) * position.fract()
                };
                // Lift quiet musical details without widening the compact footprint.
                field = field.max((amplitude * 1.7).min(1.0).powf(0.75) * spatial);
            }
        }
        field
    })
}

fn clip_near(vertices: &[Vec3], camera_z: f64) -> Vec<Vec3> {
    let plane = camera_z + NEAR;
    let mut output = Vec::with_capacity(5);
    let mut previous = vertices[vertices.len() - 1];
    for &current in vertices {
        let inside = current[2] >= plane;
        if inside != (previous[2] >= plane) {
            let t = (plane - previous[2]) / (current[2] - previous[2]);
            output.push(std::array::from_fn(|c| {
                previous[c] + (current[c] - previous[c]) * t
            }));
        }
        if inside {
            output.push(current);
        }
        previous = current;
    }
    output
}

#[derive(Clone, Copy)]
struct FogLight {
    position: Vec3,
    color: [f64; 3],
}

// Scale all channels together: bright saturated emitters retain their hue,
// instead of three independent shoulder curves converging to white.
fn tone_map(color: [f64; 3]) -> [u8; 3] {
    let peak = color.into_iter().fold(0.0_f64, f64::max);
    let mapped = if peak > 150.0 {
        150.0 + 100.0 * (1.0 - (-(peak - 150.0) / 160.0).exp())
    } else {
        peak
    };
    color.map(|v| (v * mapped / peak.max(1e-9)).round().clamp(0.0, 250.0) as u8)
}

fn surface_tint(p: Vec3, palette: [[f64; 3]; 3]) -> [f64; 3] {
    let phase = (p[2] * 0.10 + p[0] * 0.11 + p[1] * 0.13).rem_euclid(3.0);
    let first = phase.floor() as usize;
    let t = phase.fract();
    std::array::from_fn(|c| palette[first][c] * (1.0 - t) + palette[(first + 1) % 3][c] * t)
}

fn dot(a: Vec3, b: Vec3) -> f64 {
    (0..3).map(|c| a[c] * b[c]).sum()
}
fn unit(v: Vec3) -> Vec3 {
    let length = dot(v, v).sqrt().max(1e-12);
    v.map(|x| x / length)
}

// A closed truncated box: six inset faces, twelve chamfers, eight corner
// triangles. Every vertex stays INSIDE the original solid; no collar planes.
fn bevel_faces(lo: Vec3, hi: Vec3, radius: f64) -> Vec<(Vec<Vec3>, Vec3)> {
    let r = radius.min(
        (0..3)
            .map(|c| (hi[c] - lo[c]) * 0.24)
            .fold(f64::INFINITY, f64::min),
    );
    let mut faces = Vec::with_capacity(26);
    for axis in 0..3 {
        let a = (axis + 1) % 3;
        let b = (axis + 2) % 3;
        for high in [false, true] {
            let mut normal = [0.0; 3];
            normal[axis] = if high { 1.0 } else { -1.0 };
            let vertices =
                [(false, false), (true, false), (true, true), (false, true)].map(|(sa, sb)| {
                    let mut p = [0.0; 3];
                    p[axis] = if high { hi[axis] } else { lo[axis] };
                    p[a] = if sa { hi[a] - r } else { lo[a] + r };
                    p[b] = if sb { hi[b] - r } else { lo[b] + r };
                    p
                });
            faces.push((vertices.to_vec(), normal));
        }
    }
    for axis in 0..3 {
        let a = (axis + 1) % 3;
        let b = (axis + 2) % 3;
        for sa in [-1.0, 1.0] {
            for sb in [-1.0, 1.0] {
                let mut n = [0.0; 3];
                n[a] = sa;
                n[b] = sb;
                let vertices = [(false, false), (true, false), (true, true), (false, true)].map(
                    |(end, cross)| {
                        let mut p = [0.0; 3];
                        p[axis] = if end { hi[axis] - r } else { lo[axis] + r };
                        p[a] =
                            if sa > 0.0 { hi[a] } else { lo[a] } - sa * if cross { r } else { 0.0 };
                        p[b] =
                            if sb > 0.0 { hi[b] } else { lo[b] } - sb * if cross { 0.0 } else { r };
                        p
                    },
                );
                faces.push((vertices.to_vec(), unit(n)));
            }
        }
    }
    for corner in 0..8 {
        let signs: Vec3 = std::array::from_fn(|c| if corner & (1 << c) == 0 { -1.0 } else { 1.0 });
        let vertices = (0..3)
            .map(|axis| {
                std::array::from_fn(|c| {
                    let edge = if signs[c] > 0.0 { hi[c] } else { lo[c] };
                    edge - signs[c] * if c == axis { 0.0 } else { r }
                })
            })
            .collect();
        faces.push((vertices, unit(signs)));
    }
    faces
}

// Correlated architectural terraces, with a restrained fine-scale break-up.
// This is geometry only: the ceramic has no procedural surface texture.
fn terrain_height(u: i32, z: i32, side: i32, seed: u64, variant: f64) -> f64 {
    let phase = noise(side, 0, 77, seed) * 6.0;
    match variant as usize % 3 {
        0 => {
            let broad =
                ((u as f64 * 0.27 + phase).sin() + (z as f64 * 0.12 + phase).cos()) * 0.25 + 0.5;
            0.32 + broad * 0.77
                + noise(u.div_euclid(3), z.div_euclid(5), side, seed ^ 883) * 0.22
                + noise(u, z, side, seed) * 0.035
        }
        1 => {
            let step = ((z as f64 * 0.34 + phase).sin() * 0.5 + 0.5) * 4.0;
            0.30 + step.floor() * 0.21
                + noise(u.div_euclid(2), z.div_euclid(2), side, seed ^ 883) * 0.33
                + noise(u, z, side, seed) * 0.06
        }
        _ => {
            let ridge = (u as f64 * 0.63 + z as f64 * 0.39 + phase).sin().abs();
            0.25 + ridge * 0.50
                + noise(u.div_euclid(2), z.div_euclid(2), side, seed ^ 883) * 0.28
                + noise(u, z, side, seed) * 0.16
        }
    }
}

#[derive(Clone, Copy)]
struct Ceramic {
    normal: Vec3,
    lo: Vec3,
    hi: Vec3,
    wall_axis: usize,
    inward: f64,
    tint: Vec3,
}
impl Ceramic {
    fn shade(self, p: Vec3, camera: Vec3, color: Vec3) -> Vec3 {
        let view = unit(std::array::from_fn(|c| camera[c] - p[c]));
        let key = unit([-0.45, -0.65, -0.6]);
        let fill = unit([0.7, 0.35, -0.4]);
        let diffuse =
            0.48 + dot(self.normal, key).max(0.0) * 0.36 + dot(self.normal, fill).max(0.0) * 0.19;
        let halfway = unit(std::array::from_fn(|c| view[c] + key[c]));
        let half_fill = unit(std::array::from_fn(|c| view[c] + fill[c]));
        let specular = dot(self.normal, halfway).max(0.0).powi(30) * 21.0
            + dot(self.normal, half_fill).max(0.0).powi(22) * 12.0;
        // Analytic contact AO: darken the rooted side walls and narrow cap joints,
        // never draw an extra polygon. It approximates neighbouring terrace occlusion.
        let axis = self.wall_axis;
        let height = (self.hi[axis] - self.lo[axis]).max(0.001);
        let root = if self.inward < 0.0 {
            self.hi[axis]
        } else {
            self.lo[axis]
        };
        let raised = ((p[axis] - root).abs() / height).clamp(0.0, 1.0);
        let mut joint = 1.0_f64;
        for (c, coordinate) in p.iter().enumerate() {
            if c != axis {
                let edge = (coordinate - self.lo[c])
                    .min(self.hi[c] - coordinate)
                    .max(0.0);
                joint = joint.min(0.82 + 0.18 * (edge / 0.075).min(1.0));
            }
        }
        let ao = (0.55 + 0.45 * raised.sqrt()) * joint;
        std::array::from_fn(|c| {
            color[c] * diffuse * ao + specular * (0.55 + self.tint[c] / 255.0 * 0.45)
        })
    }
}

struct Raster {
    rgb: Vec<[f64; 3]>,
    emission: Vec<[f64; 3]>,
    depth: Vec<f64>,
    camera: Vec3,
    lights: Vec<FogLight>,
    crack_lights: Vec<([Vec3; 4], [f64; 3])>,
    ceramic: Option<Ceramic>,
}
impl Raster {
    fn new(camera: Vec3) -> Self {
        Self {
            rgb: vec![[1.0, 2.0, 4.0]; SW * SH],
            emission: vec![[0.0; 3]; SW * SH],
            depth: vec![f64::INFINITY; SW * SH],
            camera,
            lights: Vec::new(),
            crack_lights: Vec::new(),
            ceramic: None,
        }
    }
    #[cfg(test)]
    fn face(&mut self, vertices: [Vec3; 4], color: [f64; 3], shade: f64) {
        self.emissive_face(vertices, color, shade, [0.0; 3]);
    }
    fn emissive_face(
        &mut self,
        vertices: [Vec3; 4],
        color: [f64; 3],
        shade: f64,
        emission: [f64; 3],
    ) {
        let clipped = clip_near(&vertices, self.camera[2]);
        for i in 1..clipped.len().saturating_sub(1) {
            self.triangle(
                [clipped[0], clipped[i], clipped[i + 1]],
                color,
                shade,
                emission,
                false,
            );
        }
    }
    // Deferred transparent light never changes geometry, even for an inactive tile.
    fn composite_crack_lights(&mut self) {
        for (vertices, emission) in std::mem::take(&mut self.crack_lights) {
            if emission.iter().all(|v| *v <= 0.0) {
                continue;
            }
            let clipped = clip_near(&vertices, self.camera[2]);
            for i in 1..clipped.len().saturating_sub(1) {
                self.triangle(
                    [clipped[0], clipped[i], clipped[i + 1]],
                    [0.0; 3],
                    1.0,
                    emission,
                    true,
                );
            }
        }
    }
    fn triangle(
        &mut self,
        world: [Vec3; 3],
        color: [f64; 3],
        shade: f64,
        emission: [f64; 3],
        additive: bool,
    ) {
        let projected = world.map(|p| {
            let iz = 1.0 / (p[2] - self.camera[2]);
            [
                SW as f64 * 0.5 + (p[0] - self.camera[0]) * 920.0 * iz,
                SH as f64 * 0.49 + (p[1] - self.camera[1]) * 920.0 * iz,
                iz,
            ]
        });
        let edge = |a: Vec3, b: Vec3, x: f64, y: f64| {
            (b[0] - a[0]) * (y - a[1]) - (b[1] - a[1]) * (x - a[0])
        };
        let area = edge(projected[0], projected[1], projected[2][0], projected[2][1]);
        if area.abs() < 1e-8 {
            return;
        }
        let min_x = projected
            .iter()
            .map(|p| p[0])
            .fold(f64::INFINITY, f64::min)
            .floor()
            .max(0.0) as usize;
        let max_x = projected
            .iter()
            .map(|p| p[0])
            .fold(f64::NEG_INFINITY, f64::max)
            .ceil()
            .clamp(0.0, SW as f64) as usize;
        let min_y = projected
            .iter()
            .map(|p| p[1])
            .fold(f64::INFINITY, f64::min)
            .floor()
            .max(0.0) as usize;
        let max_y = projected
            .iter()
            .map(|p| p[1])
            .fold(f64::NEG_INFINITY, f64::max)
            .ceil()
            .clamp(0.0, SH as f64) as usize;
        for y in min_y..max_y {
            for x in min_x..max_x {
                let weights = [
                    edge(projected[1], projected[2], x as f64 + 0.5, y as f64 + 0.5) / area,
                    edge(projected[2], projected[0], x as f64 + 0.5, y as f64 + 0.5) / area,
                    edge(projected[0], projected[1], x as f64 + 0.5, y as f64 + 0.5) / area,
                ];
                if weights.iter().any(|w| *w < -1e-9) {
                    continue;
                }
                let iz: f64 = (0..3).map(|i| weights[i] * projected[i][2]).sum();
                let z = 1.0 / iz;
                let index = y * SW + x;
                if z >= self.depth[index] {
                    continue;
                }
                let fog = (-z / 24.0).exp();
                if additive {
                    for (c, value) in emission.iter().enumerate() {
                        // Max coverage avoids a doubled seam on the two triangles'
                        // shared edge or where neighbouring collars overlap.
                        self.rgb[index][c] += (value * fog - self.emission[index][c]).max(0.0);
                        self.emission[index][c] = self.emission[index][c].max(value * fog);
                    }
                    continue;
                }
                self.depth[index] = z;
                // Always replace emission with the winning surface, including zero.
                self.emission[index] = emission.map(|v| v * fog);
                let surface = self.ceramic.map_or(color.map(|v| v * shade), |material| {
                    let p = std::array::from_fn(|c| {
                        (0..3)
                            .map(|i| world[i][c] * weights[i] * projected[i][2])
                            .sum::<f64>()
                            / iz
                    });
                    material.shade(p, self.camera, color)
                });
                self.rgb[index] =
                    std::array::from_fn(|c| surface[c] * fog + [1.0, 2.0, 4.0][c] * (1.0 - fog));
            }
        }
    }
    fn block(&mut self, lo: Vec3, hi: Vec3, color: [f64; 3]) {
        self.emissive_block(lo, hi, color, [0.0; 3]);
    }
    fn emissive_block(&mut self, lo: Vec3, hi: Vec3, color: [f64; 3], emission: [f64; 3]) {
        let vertices: [Vec3; 8] = std::array::from_fn(|i| {
            std::array::from_fn(|c| if i & (1 << c) == 0 { lo[c] } else { hi[c] })
        });
        for (axis, low, high, shade) in [
            (0, [0, 2, 6, 4], [1, 5, 7, 3], 0.74),
            (1, [0, 4, 5, 1], [2, 3, 7, 6], 1.0),
            (2, [0, 1, 3, 2], [4, 6, 7, 5], 0.58),
        ] {
            if self.camera[axis] < lo[axis] {
                self.emissive_face(low.map(|i| vertices[i]), color, shade, emission);
            } else if self.camera[axis] > hi[axis] {
                self.emissive_face(high.map(|i| vertices[i]), color, shade, emission);
            }
        }
    }
    fn ceramic_block(
        &mut self,
        lo: Vec3,
        hi: Vec3,
        color: Vec3,
        emission: Vec3,
        mut material: Ceramic,
    ) {
        // Distant chamfers are subpixel: retain geometry while avoiding unnecessary
        // per-pixel shading where the existing fog already removes fine detail.
        for (vertices, normal) in bevel_faces(lo, hi, 0.045) {
            let center: Vec3 = std::array::from_fn(|c| {
                vertices.iter().map(|p| p[c]).sum::<f64>() / vertices.len() as f64
            });
            if dot(normal, std::array::from_fn(|c| self.camera[c] - center[c])) <= 0.0 {
                continue;
            }
            material.normal = normal;
            self.ceramic = Some(material);
            let clipped = clip_near(&vertices, self.camera[2]);
            for i in 1..clipped.len().saturating_sub(1) {
                self.triangle(
                    [clipped[0], clipped[i], clipped[i + 1]],
                    color,
                    1.0,
                    emission,
                    false,
                );
            }
        }
        self.ceramic = None;
    }
    // Integrate scattering in air BEFORE the first opaque surface, not an
    // extinction-only distance tint. Bounded emitters keep unlit air black.
    fn fog_sample(&self, x: f64, y: f64, end: f64) -> [f64; 3] {
        let ray = [
            (x - SW as f64 * 0.5) / 920.0,
            (y - SH as f64 * 0.49) / 920.0,
            1.0,
        ];
        let end = end.min(38.0);
        let step = end / 16.0;
        let mut light = [0.0; 3];
        for i in 0..16 {
            let distance = (i as f64 + 0.5) * step;
            let p: Vec3 = std::array::from_fn(|c| self.camera[c] + ray[c] * distance);
            let density = 0.65 + 0.35 * (p[0] * 0.7 + p[2] * 0.43).sin().powi(2);
            for source in &self.lights {
                let d2 = (0..3)
                    .map(|c| (p[c] - source.position[c]).powi(2))
                    .sum::<f64>();
                if d2 > 16.0 {
                    continue;
                }
                let scattering =
                    (-d2 / 3.5).exp() * (-distance / 24.0).exp() * density * step * 0.12;
                for (c, value) in light.iter_mut().enumerate() {
                    *value += source.color[c] * scattering;
                }
            }
        }
        light
    }
    fn volume(&self) -> image::Rgb32FImage {
        image::Rgb32FImage::from_fn((WIDTH / 4) as u32, (HEIGHT / 4) as u32, |x, y| {
            let sx = x as usize * 8 + 4;
            let sy = y as usize * 8 + 4;
            let depth = self.depth[sy * SW + sx];
            image::Rgb(
                self.fog_sample(sx as f64, sy as f64, depth)
                    .map(|v| v as f32),
            )
        })
    }
    fn bloom(&self) -> image::Rgb32FImage {
        let w = WIDTH / 4;
        let h = HEIGHT / 4;
        let mut source = image::Rgb32FImage::new(w as u32, h as u32);
        for y in 0..h {
            for x in 0..w {
                let mut sum = [0.0_f32; 3];
                for sy in 0..8 {
                    for sx in 0..8 {
                        let e = self.emission[(y * 8 + sy) * SW + x * 8 + sx];
                        for c in 0..3 {
                            sum[c] += e[c] as f32 / 64.0;
                        }
                    }
                }
                source.put_pixel(x as u32, y as u32, image::Rgb(sum));
            }
        }
        let tight = image::imageops::blur(&source, 1.25);
        let wide = image::imageops::blur(&source, 6.0);
        image::Rgb32FImage::from_fn(w as u32, h as u32, |x, y| {
            image::Rgb(std::array::from_fn(|c| {
                tight.get_pixel(x, y)[c] * 1.1 + wide.get_pixel(x, y)[c] * 0.85
            }))
        })
    }
    fn downsample(&self, output: &mut [u8]) {
        let bloom = self.bloom();
        let volume = self.volume();
        let sample = |image: &image::Rgb32FImage, x: usize, y: usize, c: usize| {
            let fx = (x as f64 / 4.0 - 0.5).max(0.0);
            let fy = (y as f64 / 4.0 - 0.5).max(0.0);
            let x0 = (fx as u32).min(image.width() - 1);
            let y0 = (fy as u32).min(image.height() - 1);
            let x1 = (x0 + 1).min(image.width() - 1);
            let y1 = (y0 + 1).min(image.height() - 1);
            let tx = fx.fract() as f32;
            let ty = fy.fract() as f32;
            ((image.get_pixel(x0, y0)[c] * (1.0 - tx) + image.get_pixel(x1, y0)[c] * tx)
                * (1.0 - ty)
                + (image.get_pixel(x0, y1)[c] * (1.0 - tx) + image.get_pixel(x1, y1)[c] * tx) * ty)
                as f64
        };
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let hdr = std::array::from_fn(|c| {
                    let i = y * 2 * SW + x * 2;
                    let base = (self.rgb[i][c]
                        + self.rgb[i + 1][c]
                        + self.rgb[i + SW][c]
                        + self.rgb[i + SW + 1][c])
                        * 0.25;
                    let glow = sample(&bloom, x, y, c);
                    base + glow + sample(&volume, x, y, c)
                });
                output[(y * WIDTH + x) * 3..(y * WIDTH + x + 1) * 3]
                    .copy_from_slice(&tone_map(hdr));
            }
        }
    }
}

impl Painter {
    pub(super) fn cave_frame(&mut self, frame: usize, audio: AudioFrame) -> &[u8] {
        let time = frame as f64 / FPS as f64;
        let seed = self.phase.to_bits();
        let look = cave_look(self.variant as usize);
        let camera = look.camera(time, self.phase);
        let travel = camera[2] / look.scale[2];
        let mut raster = Raster::new(camera);
        let palette = look.palette;
        let first = (travel / look.pitch).floor() as i32;
        // Continuous backing slabs seal the gaps between relief blocks, including
        // the corners. Z-buffering handles all four intersecting terrain planes.
        raster.block(
            look.world([-6.0, 3.5, travel - 1.0]),
            look.world([6.0, 4.3, travel + 86.0]),
            [16.0, 18.0, 23.0],
        );
        raster.block(
            look.world([-6.0, -4.3, travel - 1.0]),
            look.world([6.0, -3.5, travel + 86.0]),
            [16.0, 18.0, 23.0],
        );
        raster.block(
            look.world([-6.0, -4.3, travel - 1.0]),
            look.world([-5.5, 4.3, travel + 86.0]),
            [16.0, 18.0, 23.0],
        );
        raster.block(
            look.world([5.5, -4.3, travel - 1.0]),
            look.world([6.0, 4.3, travel + 86.0]),
            [16.0, 18.0, 23.0],
        );
        for z in (first..=first + (85.0 / look.pitch) as i32).rev() {
            for side in 0..4 {
                let extent = ((if side < 2 { 5.0 } else { 3.0 }) / look.pitch).round() as i32;
                for u in -extent..=extent {
                    let center = if side < 2 {
                        [
                            u as f64 * look.pitch,
                            if side == 0 { 3.2 } else { -3.2 },
                            (z as f64 + 0.5) * look.pitch,
                        ]
                    } else {
                        [
                            if side == 2 { -5.2 } else { 5.2 },
                            u as f64 * look.pitch,
                            (z as f64 + 0.5) * look.pitch,
                        ]
                    };
                    let wave_point = [center[0], center[1], center[2] * look.wave_rate];
                    let waves = wave(wave_point, time, audio, seed, &self.cave_audio);
                    let pulse = waves.iter().copied().fold(0.0_f64, f64::max);
                    let height =
                        terrain_height(u, z, side, seed, self.variant) + pulse * look.displacement;
                    let half = look.pitch * (0.456 + noise(u, z, side, seed ^ 91) * 0.009);
                    let mut lo = [center[0] - half, center[1] - half, center[2] - half];
                    let mut hi = [center[0] + half, center[1] + half, center[2] + half];
                    match side {
                        0 => {
                            lo[1] = 3.5 - height;
                            hi[1] = 4.0;
                        }
                        1 => {
                            lo[1] = -4.0;
                            hi[1] = -3.5 + height;
                        }
                        2 => {
                            lo[0] = -5.8;
                            hi[0] = -5.5 + height;
                        }
                        _ => {
                            lo[0] = 5.5 - height;
                            hi[0] = 5.8;
                        }
                    }
                    let tint = surface_tint(center, palette);
                    // Gentle colored indirect illumination reveals unlit relief,
                    // Constant underlight and low emission are independent of music.
                    let ambient = ambient_emission(center, palette);
                    let color = std::array::from_fn(|c| {
                        ambient[c] * 0.8
                            + [29.0, 32.0, 39.0][c]
                            + tint[c] * 0.095
                            + (0..3)
                                .map(|r| palette[r][c] * waves[r] * 1.9)
                                .fold(0.0_f64, f64::max)
                    });
                    let emission = std::array::from_fn(|c| {
                        ambient[c]
                            + (0..3)
                                .map(|r| palette[r][c] * waves[r] * 0.7)
                                .fold(0.0_f64, f64::max)
                    });
                    // Hidden tiles sit just in front of the opaque backing slab,
                    // but behind relief blocks. Only real inter-block gaps expose them.
                    let axis = if side < 2 { 1 } else { 0 };
                    // A recessed luminous collar follows each cap, rather than sitting
                    // at the far backing plane where tall neighbours hide all light.
                    let plane = match side {
                        0 => lo[1] + 0.10,
                        1 => hi[1] - 0.10,
                        2 => hi[0] - 0.10,
                        _ => lo[0] + 0.10,
                    };
                    let tangent = if side < 2 { 0 } else { 1 };
                    let tile = std::array::from_fn(|i| {
                        let mut p = center;
                        p[axis] = plane;
                        p[tangent] += if i == 0 || i == 3 {
                            -look.pitch * 0.5
                        } else {
                            look.pitch * 0.5
                        };
                        p[2] += if i < 2 {
                            -look.pitch * 0.5
                        } else {
                            look.pitch * 0.5
                        };
                        p
                    });
                    let hidden = std::array::from_fn(|c| {
                        (0..3)
                            .map(|r| {
                                (palette[r][c] * 0.90 + palette[(r + 1) % 3][c] * 0.10)
                                    * waves[r].sqrt()
                                    * 2.2
                            })
                            .fold(0.0_f64, f64::max)
                    });
                    // Queue only light, not a physical shelf. Resolve visibility
                    // against the complete opaque tunnel after every block is drawn.
                    raster
                        .crack_lights
                        .push((tile.map(|p| look.world(p)), hidden));
                    let lo = look.world(lo);
                    let hi = look.world(hi);
                    raster.ceramic_block(
                        lo,
                        hi,
                        color,
                        emission,
                        Ceramic {
                            normal: [0.0; 3],
                            lo,
                            hi,
                            wall_axis: axis,
                            inward: if side == 0 || side == 3 { -1.0 } else { 1.0 },
                            tint,
                        },
                    );
                    if pulse > 0.06 && center[2] - travel < 40.0 {
                        let mut position = center;
                        position[axis] = plane - plane.signum() * 0.75;
                        let position = look.world(position);
                        let candidate = FogLight {
                            position,
                            color: hidden.map(|v| v * 0.35),
                        };
                        // One strongest emitter per 3-unit cell bounds the ray marcher.
                        let existing = raster.lights.iter_mut().find(|l| {
                            (0..3).all(|c| {
                                (l.position[c] / 3.0).floor() == (position[c] / 3.0).floor()
                            })
                        });
                        if let Some(light) = existing {
                            if candidate.color.iter().sum::<f64>() > light.color.iter().sum::<f64>()
                            {
                                *light = candidate;
                            }
                        } else if raster.lights.len() < 48 {
                            raster.lights.push(candidate);
                        }
                    }
                }
            }
        }
        raster.composite_crack_lights();
        raster.downsample(&mut self.rgb);
        &self.rgb
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn variants_have_authored_geometry_palettes_and_calm_distinct_paths() {
        let looks = [cave_look(0), cave_look(1), cave_look(2)];
        assert!(looks[1].scale[0] < looks[0].scale[0]);
        assert!(looks[0].scale[0] < looks[2].scale[0]);
        assert!(looks[2].pitch < looks[1].pitch && looks[1].pitch < looks[0].pitch);
        for a in 0..3 {
            assert_eq!(looks[a].palette, cave_look(a + 3).palette);
            for b in a + 1..3 {
                assert_ne!(looks[a].palette, looks[b].palette);
                assert_ne!(looks[a].camera(7.0, 0.7), looks[b].camera(7.0, 0.7));
                let difference: f64 = (0..30)
                    .map(|z| {
                        (terrain_height(1, z, 0, 712, a as f64)
                            - terrain_height(1, z, 0, 712, b as f64))
                        .abs()
                    })
                    .sum();
                assert!(difference > 2.0);
            }
            for frame in 0..600 {
                let t = frame as f64 / FPS as f64;
                let camera = looks[a].camera(t, 0.7);
                let next = looks[a].camera(t + 1.0 / FPS as f64, 0.7);
                assert!((next[0] - camera[0]).abs() < 0.004);
                assert!((next[1] - camera[1]).abs() < 0.002);
                assert!(camera[0].abs() < 0.5 && camera[1].abs() < 0.2);
            }
        }
    }

    #[test]
    fn every_variant_is_seed_reproducible_with_distinct_silent_geometry() {
        let mut rendered = Vec::new();
        for variant in 0..3 {
            let mut a = Painter::new("cube-cave", 712, variant);
            let mut b = Painter::new("cube-cave", 712, variant);
            let image = a.frame(90, AudioFrame::default()).to_vec();
            assert_eq!(image, b.frame(90, AudioFrame::default()));
            rendered.push(image);
        }
        for a in 0..3 {
            for b in a + 1..3 {
                assert_ne!(rendered[a], rendered[b]);
            }
        }
    }

    #[test]
    fn bevel_is_closed_bounded_solid_with_unit_outward_normals() {
        let lo = [-0.5, -0.8, 3.0];
        let hi = [0.5, 0.8, 4.0];
        let faces = bevel_faces(lo, hi, 0.045);
        assert_eq!(faces.len(), 26);
        let mut edges = std::collections::BTreeMap::new();
        let key = |p: Vec3| p.map(|v| (v * 1_000_000.0).round() as i64);
        for (vertices, normal) in faces {
            assert!((dot(normal, normal) - 1.0).abs() < 1e-10);
            for (i, p) in vertices.iter().enumerate() {
                assert!((0..3).all(|c| p[c] >= lo[c] && p[c] <= hi[c]));
                let a = key(*p);
                let b = key(vertices[(i + 1) % vertices.len()]);
                *edges
                    .entry(if a < b { (a, b) } else { (b, a) })
                    .or_insert(0) += 1;
            }
            let center: Vec3 = std::array::from_fn(|c| {
                vertices.iter().map(|p| p[c]).sum::<f64>() / vertices.len() as f64
                    - (lo[c] + hi[c]) * 0.5
            });
            assert!(dot(center, normal) > 0.0);
        }
        assert!(edges.values().all(|count| *count == 2));
    }

    #[test]
    fn ceramic_highlights_are_view_dependent_and_terrain_is_correlated() {
        let material = Ceramic {
            normal: unit([0.0, -1.0, -1.0]),
            lo: [-0.5, 2.0, 4.0],
            hi: [0.5, 3.0, 5.0],
            wall_axis: 1,
            inward: -1.0,
            tint: [40.0, 150.0, 240.0],
        };
        let p = [0.0, 2.0, 4.5];
        let a = material.shade(p, [0.0; 3], [40.0; 3]);
        let b = material.shade(p, [4.0, 0.0, 0.0], [40.0; 3]);
        assert_ne!(a, b);
        let small = (0..50)
            .map(|z| {
                (terrain_height(0, z, 0, 712, 0.0) - terrain_height(0, z + 1, 0, 712, 0.0)).abs()
            })
            .sum::<f64>();
        let large = (0..50)
            .map(|z| {
                (terrain_height(0, z, 0, 712, 0.0) - terrain_height(0, z + 9, 0, 712, 0.0)).abs()
            })
            .sum::<f64>();
        assert!(small < large * 0.5);
    }

    #[test]
    fn epochs_reseed_sources_on_all_walls_without_moving_old_packets() {
        let mut walls = [false; 4];
        for region in 0..3 {
            for epoch in 0..24 {
                let a = packet_source(region, 2, epoch, 712);
                assert_eq!(a, packet_source(region, 2, epoch, 712));
                assert_ne!(a, packet_source(region, 2, epoch + 1, 712));
                assert_ne!(a, packet_source(region, 3, epoch, 712));
                let side = if a[1] == 3.2 {
                    0
                } else if a[1] == -3.2 {
                    1
                } else if a[0] == -5.2 {
                    2
                } else {
                    3
                };
                walls[side] = true;
            }
        }
        assert!(walls.into_iter().all(|v| v));
        let audio = AudioFrame {
            bands: [0.8; BANDS],
            ..AudioFrame::default()
        };
        // Epoch boundaries introduce a new zero-envelope packet, never teleport
        // the previous one; verify temporal continuity across many boundaries.
        for i in 1..80 {
            let t = i as f64 * 0.1;
            for p in [[0.0, 3.2, 28.0], [-5.2, 1.0, 37.0]] {
                let a = wave(p, t - 0.00001, audio, 712, &[]);
                let b = wave(p, t + 0.00001, audio, 712, &[]);
                assert!((0..3).all(|r| (a[r] - b[r]).abs() < 0.001));
            }
        }
    }

    #[test]
    fn constant_ambient_is_soft_emissive_and_audio_independent() {
        let palette = [
            [45.0, 170.0, 240.0],
            [180.0, 55.0, 230.0],
            [240.0, 140.0, 35.0],
        ];
        let p = [2.0, 3.2, 20.0];
        let ambient = ambient_emission(p, palette);
        assert!(ambient.into_iter().all(|v| v > 2.0 && v < 14.0));
        let mut raster = Raster::new([0.0; 3]);
        raster.emissive_block([-1.0, -1.0, 3.0], [1.0, 1.0, 4.0], [40.0; 3], ambient);
        assert!(raster.emission.iter().any(|e| e[0] > 1.0));
        assert!(raster.bloom().pixels().any(|e| e[0] > 1.0));
        let audio = AudioFrame {
            bands: [1.0; BANDS],
            attacks: [1.0; 3],
            attack_age: [0.0; 3],
            ..AudioFrame::default()
        };
        let peak = surface_points(0)
            .into_iter()
            .map(|p| {
                wave(p, 3.0, audio, 712, &[])
                    .into_iter()
                    .fold(0.0_f64, f64::max)
            })
            .fold(0.0_f64, f64::max);
        assert!(peak * 240.0 * 1.4 > ambient.into_iter().fold(0.0_f64, f64::max) * 8.0);
        assert_eq!(ambient, ambient_emission(p, palette));
    }

    #[test]
    fn hdr_mapping_preserves_saturated_hue_without_clipping() {
        let mapped = tone_map([1800.0, 300.0, 90.0]);
        assert!(mapped[0] <= 250 && mapped[0] > 200);
        assert!(mapped[1] < 50 && mapped[2] < 20, "{mapped:?}");
        assert_eq!(tone_map([30.0, 40.0, 50.0]), [30, 40, 50]);
    }

    #[test]
    fn quiet_relief_has_readable_soft_ambient_not_active_accents() {
        let mut painter = Painter::new("cube-cave", 712, 0);
        let quiet = painter.frame(90, AudioFrame::default());
        let readable = quiet
            .as_chunks::<3>()
            .0
            .iter()
            .filter(|p| p.iter().copied().max().unwrap() >= 25)
            .count();
        let bright = quiet
            .as_chunks::<3>()
            .0
            .iter()
            .filter(|p| p.iter().copied().max().unwrap() >= 100)
            .count();
        assert!(readable > WIDTH * HEIGHT / 4, "{readable}");
        assert_eq!(bright, 0);
    }

    #[test]
    fn every_frequency_region_drives_its_own_wave_and_onset() {
        let points = surface_points(0);
        for region in 0..3 {
            let (start, end) = [(0, 16), (16, 40), (40, BANDS)][region];
            let mut signal = AudioFrame::default();
            signal.bands[start..end].fill(0.55);
            let mut hit = signal;
            hit.attacks[region] = 1.0;
            hit.attack_age[region] = 0.0;
            let mut energy_sum = 0.0;
            let mut onset_gain = 0.0;
            for p in &points {
                let a = wave(*p, 3.0, signal, 712, &[]);
                let b = wave(*p, 3.0, hit, 712, &[]);
                energy_sum += a[region];
                onset_gain += b[region] - a[region];
                assert!(
                    (0..3)
                        .filter(|r| *r != region)
                        .all(|r| a[r] == 0.0 && b[r] == 0.0)
                );
            }
            assert!(energy_sum > 1.0, "region={region} energy={energy_sum}");
            assert!(onset_gain > 0.5, "region={region} onset={onset_gain}");
        }
    }

    #[test]
    fn volume_is_local_and_stops_at_opaque_depth() {
        let mut raster = Raster::new([0.0; 3]);
        assert_eq!(
            raster.fog_sample(SW as f64 / 2.0, SH as f64 * 0.49, 20.0),
            [0.0; 3]
        );
        raster.lights.push(FogLight {
            position: [0.0, 0.0, 8.0],
            color: [200.0, 90.0, 40.0],
        });
        let sample = |depth| raster.fog_sample(SW as f64 / 2.0, SH as f64 * 0.49, depth)[0];
        assert!(sample(12.0) > 20.0);
        assert_eq!(sample(3.0), 0.0);
        assert_eq!(raster.fog_sample(0.0, 0.0, 20.0), [0.0; 3]);
    }
    #[test]
    fn matte_has_no_bloom_even_when_bright() {
        let mut raster = Raster::new([0.0; 3]);
        raster.block([-1.0, -1.0, 3.0], [1.0, 1.0, 4.0], [240.0; 3]);
        assert!(raster.emission.iter().all(|e| *e == [0.0; 3]));
        assert!(raster.bloom().pixels().all(|e| e.0 == [0.0; 3]));
    }

    #[test]
    fn hidden_emission_is_occluded_and_resets_in_both_draw_orders() {
        let front = [
            [-1.0, -1.0, 3.0],
            [1.0, -1.0, 3.0],
            [1.0, 1.0, 3.0],
            [-1.0, 1.0, 3.0],
        ];
        let back = front.map(|p| [p[0], p[1], 5.0]);
        let mut a = Raster::new([0.0; 3]);
        let mut b = Raster::new([0.0; 3]);
        a.emissive_face(back, [200.0; 3], 1.0, [200.0; 3]);
        a.face(front, [20.0; 3], 1.0);
        b.face(front, [20.0; 3], 1.0);
        b.emissive_face(back, [200.0; 3], 1.0, [200.0; 3]);
        assert_eq!(a.emission, b.emission);
        assert!(a.emission.iter().all(|e| *e == [0.0; 3]));
    }

    #[test]
    fn transparent_collars_never_draw_dark_shelves_or_occlude() {
        let tile = [
            [-0.8, -0.8, 3.0],
            [0.8, -0.8, 3.0],
            [0.8, 0.8, 3.0],
            [-0.8, 0.8, 3.0],
        ];
        let mut raster = Raster::new([0.0; 3]);
        raster.emissive_block([-1.0, -1.0, 4.0], [1.0, 1.0, 5.0], [50.0; 3], [3.0; 3]);
        let rgb = raster.rgb.clone();
        let depth = raster.depth.clone();
        let emission = raster.emission.clone();
        raster.crack_lights.push((tile, [0.0; 3]));
        raster.composite_crack_lights();
        assert_eq!(rgb, raster.rgb);
        assert_eq!(depth, raster.depth);
        assert_eq!(emission, raster.emission);
        raster.crack_lights.push((tile, [180.0, 90.0, 30.0]));
        raster.composite_crack_lights();
        assert_eq!(depth, raster.depth);
        assert!(
            raster
                .rgb
                .iter()
                .zip(&rgb)
                .all(|(a, b)| (0..3).all(|c| a[c] >= b[c]))
        );
        assert_ne!(rgb, raster.rgb);

        let mut hidden = Raster::new([0.0; 3]);
        hidden.crack_lights.push((tile, [240.0; 3]));
        // Geometry added after queuing the light still occludes it in the deferred pass.
        hidden.block([-2.0, -2.0, 2.0], [2.0, 2.0, 2.5], [40.0; 3]);
        let rgb = hidden.rgb.clone();
        let depth = hidden.depth.clone();
        hidden.composite_crack_lights();
        assert_eq!(rgb, hidden.rgb);
        assert_eq!(depth, hidden.depth);
        assert!(hidden.emission.iter().all(|e| *e == [0.0; 3]));
    }

    #[test]
    fn genuine_gap_exposes_hidden_tile_and_local_halo() {
        let mut raster = Raster::new([0.0; 3]);
        // Backing slab y=3.5; emitter y=3.49 is not buried inside it.
        raster.block([-2.0, 3.5, 4.0], [2.0, 4.0, 8.0], [16.0; 3]);
        raster.crack_lights.push((
            [
                [-1.0, 3.49, 4.0],
                [1.0, 3.49, 4.0],
                [1.0, 3.49, 8.0],
                [-1.0, 3.49, 8.0],
            ],
            [240.0; 3],
        ));
        raster.block([-1.0, 2.7, 4.0], [-0.08, 4.0, 8.0], [20.0; 3]);
        raster.block([0.08, 2.7, 4.0], [1.0, 4.0, 8.0], [20.0; 3]);
        let depth = raster.depth.clone();
        raster.composite_crack_lights();
        assert_eq!(depth, raster.depth, "crack light must not become geometry");
        let visible = raster.emission.iter().filter(|e| e[0] > 0.0).count();
        assert!(visible > 100, "gap must expose light: {visible}");
        assert!(visible < SW * SH / 20, "light stays in the narrow gap");
        let halo = raster.bloom();
        assert!(halo.pixels().any(|p| p[0] > 1.0));
        assert_eq!(halo.get_pixel(0, 0).0, [0.0; 3]);
        let outside_source = (0..HEIGHT / 4).any(|y| {
            (0..WIDTH / 4).any(|x| {
                halo.get_pixel(x as u32, y as u32)[0] > 0.1
                    && (0..8).all(|sy| {
                        (0..8).all(|sx| raster.emission[(y * 8 + sy) * SW + x * 8 + sx][0] == 0.0)
                    })
            })
        });
        assert!(
            outside_source,
            "blur must create a halo outside visible emitter"
        );
    }

    #[test]
    fn cave_is_seekable_and_audio_reactive() {
        let mut painter = Painter::new("cube-cave", 712, 0);
        let quiet = painter.frame(60, AudioFrame::default()).to_vec();
        let loud = AudioFrame {
            bands: [0.8; BANDS],
            attacks: [1.0; 3],
            attack_age: [0.0; 3],
            ..AudioFrame::default()
        };
        assert_ne!(quiet, painter.frame(60, loud));
        assert_eq!(quiet, painter.frame(60, AudioFrame::default()));
        assert_ne!(quiet, painter.frame(90, AudioFrame::default()));
    }
    #[test]
    fn clipping_and_depth_are_order_independent() {
        let clipped = clip_near(
            &[
                [-1.0, -1.0, -1.0],
                [1.0, -1.0, 1.0],
                [1.0, 1.0, 1.0],
                [-1.0, 1.0, -1.0],
            ],
            0.0,
        );
        assert_eq!(clipped.len(), 4);
        assert!(clipped.iter().all(|p| p[2] >= NEAR - 1e-12));
        let mut a = Raster::new([0.0; 3]);
        let mut b = Raster::new([0.0; 3]);
        let front = [
            [-1.0, -1.0, 3.0],
            [1.0, -1.0, 3.0],
            [1.0, 1.0, 3.0],
            [-1.0, 1.0, 3.0],
        ];
        let back = front.map(|p| [p[0], p[1], 5.0]);
        a.face(front, [100.0; 3], 1.0);
        a.face(back, [200.0; 3], 1.0);
        b.face(back, [200.0; 3], 1.0);
        b.face(front, [100.0; 3], 1.0);
        assert_eq!(a.rgb, b.rgb);
    }
    #[test]
    fn waves_are_smooth_independent_and_move() {
        let audio = AudioFrame {
            bands: [0.8; BANDS],
            ..AudioFrame::default()
        };
        let p = surface_points(0)
            .into_iter()
            .find(|p| wave(*p, 3.0, audio, 712, &[]).iter().any(|v| *v > 0.02))
            .unwrap();
        let a = wave(p, 3.0, audio, 712, &[]);
        let b = wave([p[0] + 0.001, p[1], p[2]], 3.0, audio, 712, &[]);
        assert!((0..3).all(|r| (a[r] - b[r]).abs() < 0.002));
        assert_ne!(a, wave(p, 2.5, audio, 712, &[]));
        assert!(a.iter().any(|v| (*v - a[0]).abs() > 0.001));
        assert_eq!(wave(p, 2.0, AudioFrame::default(), 712, &[]), [0.0; 3]);
    }
    #[test]
    fn prepared_audio_propagates_with_delay_and_is_seekable() {
        let signal = AudioFrame {
            bands: [0.8; BANDS],
            attacks: [1.0; 3],
            attack_age: [0.0; 3],
            ..AudioFrame::default()
        };
        let mut history = vec![AudioFrame::default(); FPS * 8];
        history[FPS..FPS * 2].fill(signal);
        let p = [1.0, 3.0, 25.0];
        assert_eq!(wave(p, 0.0, signal, 712, &history), [0.0; 3]);
        assert!(surface_points(0).into_iter().any(|p| (1..70).any(|i| {
            wave(p, i as f64 / 10.0, AudioFrame::default(), 712, &history)
                .iter()
                .any(|v| *v > 0.01)
        })));
        let mut painter = Painter::new("cube-cave", 712, 0);
        painter.cave_audio = history;
        let before = painter.frame(80, signal).to_vec();
        painter.frame(100, signal);
        assert_eq!(before, painter.frame(80, signal));
    }
    fn surface_points(first: i32) -> Vec<Vec3> {
        let look = CaveLook {
            pitch: 1.0,
            ..cave_look(0)
        };
        let mut points = Vec::new();
        for z in first..first + 85 {
            for side in 0..4 {
                let extent = ((if side < 2 { 5.0 } else { 3.0 }) / look.pitch).round() as i32;
                for u in -extent..=extent {
                    points.push(if side < 2 {
                        [
                            u as f64 * look.pitch,
                            if side == 0 { 3.2 } else { -3.2 },
                            (z as f64 + 0.5) * look.pitch,
                        ]
                    } else {
                        [
                            if side == 2 { -5.2 } else { 5.2 },
                            u as f64 * look.pitch,
                            (z as f64 + 0.5) * look.pitch,
                        ]
                    });
                }
            }
        }
        points
    }

    #[test]
    fn constant_loud_history_keeps_surface_sparse_for_twenty_seconds() {
        let loud = AudioFrame {
            bands: [1.0; BANDS],
            attacks: [1.0; 3],
            attack_age: [0.0; 3],
            ..AudioFrame::default()
        };
        let history = vec![loud; FPS * 20];
        for seed in [712, 13, 999] {
            for time in [3.0, 8.0, 15.0, 19.0] {
                let points = surface_points((time * 3.6_f64).floor() as i32);
                let mut lit = 0;
                let mut peak = 0.0_f64;
                for p in &points {
                    let value = wave(*p, time, loud, seed, &history)
                        .into_iter()
                        .fold(0.0_f64, f64::max);
                    peak = peak.max(value);
                    lit += usize::from(value > 0.04);
                    assert_eq!(wave(*p, time, AudioFrame::default(), seed, &[]), [0.0; 3]);
                }
                let coverage = lit as f64 / points.len() as f64;
                assert!(coverage < 0.20, "seed={seed} t={time}: coverage={coverage}");
                assert!(
                    coverage > 0.002 && peak > 0.15,
                    "seed={seed} t={time}: coverage={coverage}, peak={peak}"
                );
            }
        }
    }

    #[test]
    fn supersampling_averages_four_samples() {
        let mut raster = Raster::new([0.0; 3]);
        raster.rgb[0] = [0.0; 3];
        raster.rgb[1] = [40.0; 3];
        raster.rgb[SW] = [80.0; 3];
        raster.rgb[SW + 1] = [120.0; 3];
        let mut rgb = vec![0; WIDTH * HEIGHT * 3];
        raster.downsample(&mut rgb);
        assert_eq!(&rgb[..3], &[60; 3]);
    }
}
