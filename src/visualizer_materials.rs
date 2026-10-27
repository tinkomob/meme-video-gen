use super::*;

pub(super) fn spatial(style: &str) -> bool {
    matches!(
        style,
        "ferro"
            | "cymatics"
            | "topography"
            | "glass"
            | "ink"
            | "kinetic"
            | "lightning"
            | "interference"
    )
}

// World coordinates continue through time; geometry never wraps in screen space.
struct Space {
    kind: usize,
    variant: f64,
    phase: f64,
    time: f64,
    flow: f64,
    drive: [f64; 3],
    palette: [Color; 3],
}

fn dot(a: [f64; 3], b: [f64; 3]) -> f64 {
    a.into_iter().zip(b).map(|(x, y)| x * y).sum()
}
fn unit(v: [f64; 3]) -> [f64; 3] {
    let length = dot(v, v).sqrt().max(0.00001);
    v.map(|x| x / length)
}

const RADIANCE: f32 = 1024.0;

fn covered_pixel(x: f64, y: f64, sample: impl Fn(f64, f64) -> [f32; 4]) -> [f32; 4] {
    let mut result = [0.0; 4];
    for (dx, dy) in [
        (0.125, 0.375),
        (0.625, 0.125),
        (0.875, 0.625),
        (0.375, 0.875),
    ] {
        let value = sample(x + dx, y + dy);
        for channel in 0..4 {
            result[channel] += value[channel] * 0.25;
        }
    }
    result
}

fn render_samples(
    field: &mut image::Rgba32FImage,
    edges: Option<&[bool]>,
    sample: impl Fn(f64, f64) -> [f32; 4] + Sync,
) {
    let (width, height) = (field.width() as usize, field.height() as usize);
    let workers = std::thread::available_parallelism()
        .map_or(1, |n| n.get())
        .min(4);
    let rows = height.div_ceil(workers);
    std::thread::scope(|scope| {
        for (block, chunk) in field.as_mut().chunks_mut(width * 4 * rows).enumerate() {
            let sample = &sample;
            scope.spawn(move || {
                for (index, pixel) in chunk.as_chunks_mut::<4>().0.iter_mut().enumerate() {
                    let x = index % width;
                    let y = block * rows + index / width;
                    let value = if let Some(edges) = edges {
                        if !edges[y * width + x] {
                            continue;
                        }
                        covered_pixel(x as f64, y as f64, sample)
                    } else {
                        sample(x as f64 + 0.5, y as f64 + 0.5)
                    };
                    pixel.copy_from_slice(&value);
                }
            });
        }
    });
}

impl Space {
    fn origin(&self) -> [f64; 3] {
        if self.kind == 0 {
            [
                0.32 + 0.12 * (self.time * 0.17 + self.phase).sin(),
                1.65,
                self.flow,
            ]
        } else {
            [
                0.17 * (self.time * 0.13 + self.phase).sin(),
                0.16 * (self.time * 0.11).cos(),
                self.flow,
            ]
        }
    }
    fn strand(&self, z: f64, index: usize) -> [f64; 2] {
        let phase = index as f64 * 1.256 + self.phase;
        [
            (z * (0.55 + self.variant * 0.07) + phase).sin() * (0.8 + self.drive[0] * 0.006)
                + (z * 4.1 + phase * 2.0).sin() * (0.07 + self.drive[1] * 0.0024),
            (z * 0.79 + phase).cos() * 1.1 + (z * 5.3 - phase).sin() * 0.1,
        ]
    }
    fn distance(&self, [mut x, mut y, z]: [f64; 3]) -> f64 {
        let [bass, mid, high] = self.drive.map(|value| value * 0.03);
        let phase = self.phase + self.variant * 0.6;
        x += 0.065 * (z * 0.63 + y * 0.8 + phase).sin();
        y += 0.045 * (x * 1.1 - z * 0.4 + phase).cos();
        match self.kind {
            0 => {
                let scale = 1.6 + self.variant * 0.15;
                let peaks =
                    (0.5 + 0.5 * (x * scale + phase).sin() * (z * 1.1 - x * 0.45).cos()).powi(3);
                let fold = (x * 1.2 + z * 0.9 + (z * 0.35 + x * 0.5 + phase).sin() * 0.5).sin();
                let ridge = (-(fold * 1.45).powi(2)).exp();
                let wave = (x * 2.0 + z * 1.3 - self.time * 0.6).sin();
                let height = 0.07
                    + peaks * (0.22 + bass * 0.26 + mid * 0.10)
                    + ridge * (0.18 + bass * 0.14 + mid * 0.08)
                    + wave * (0.025 + mid * 0.035)
                    + high * 0.008 * (x * 8.0 + z * 7.0 - self.time).sin();
                (y - height) * 0.4
            }
            1 => {
                let k = 1.8 + self.variant * 0.24;
                let u = x * k + mid * 0.18 * (z * 0.7).sin();
                let v = y * k + bass * 0.15;
                let w = z * k + phase;
                let nodes = u.sin() * v.cos() + v.sin() * w.cos() + w.sin() * u.cos();
                ((nodes.abs() - (0.065 + bass * 0.055)) / (k * 2.4))
                    .max(0.6 + 0.06 * (z * 0.7).sin() - x.hypot(y))
            }
            2 => {
                let u = x - 0.35 * (z * 0.28 + phase).sin();
                let v = y - 0.25 * (z * 0.23).cos();
                let angle = v.atan2(u);
                let folds = (angle * (5.0 + self.variant) + z * 0.65).sin();
                let strata = (z * 16.0 + angle * 2.0 + mid * 1.8).sin();
                let radius = 1.5 + folds * (0.18 + bass * 0.12) + strata * 0.035;
                (radius - u.hypot(v)) * 0.55
            }
            3 => {
                let cell = (z / 2.4).floor();
                let theta = cell * 0.63 + phase;
                let (sin, cos) = theta.sin_cos();
                let u = x * cos - y * sin;
                let v = x * sin + y * cos;
                let u = (u + 1.3).rem_euclid(2.6) - 1.3;
                let v = (v + 1.3).rem_euclid(2.6) - 1.3;
                let w = z.rem_euclid(2.4) - 1.2;
                let crystal = ((u * u + 0.008).sqrt() * 0.8
                    + (v * v + 0.008).sqrt()
                    + (w * w + 0.008).sqrt() * 0.7
                    - (0.94 + mid * 0.12 + bass * 0.16 + self.variant * 0.06))
                    * 0.55;
                (crystal.abs() - 0.025 - high * 0.008).max(0.52 - x.hypot(y))
            }
            5 => {
                let cell = (z / 1.7).floor();
                let theta = cell * 0.18 + (z * 0.22 + phase).sin() * 0.45 + mid * 0.14;
                let (sin, cos) = theta.sin_cos();
                let u = x * cos - y * sin - (cell * 0.7 + phase).sin() * 0.14;
                let v = x * sin + y * cos - (cell * 0.5 - phase).cos() * 0.14;
                let power = 3.0 + self.variant;
                let radius = (u.abs().powf(power) + (v * 0.8).abs().powf(power)).powf(1.0 / power)
                    - 0.1 * (v.atan2(u) * 3.0 + cell * 0.55 + phase).sin();
                let shell = (radius - (1.15 + bass * 0.12)).abs() - (0.07 + high * 0.018);
                let depth = (z.rem_euclid(1.7) - 0.85).abs() - 0.12;
                shell.max(0.0).hypot(depth.max(0.0)) + shell.max(depth).min(0.0) - 0.025
            }
            7 => {
                let fold = y * 1.55
                    + (x * 0.9 + z * 0.45 + phase).cos() * (0.9 + mid * 0.2)
                    + (z * 0.6).sin() * 0.8
                    + bass * 0.24;
                ((fold.sin().abs() - (0.025 + high * 0.01)) * 0.45).max(0.5 - x.hypot(y))
            }
            _ => unreachable!(),
        }
    }

    fn normal(&self, p: [f64; 3], epsilon: f64) -> [f64; 3] {
        unit(std::array::from_fn(|axis| {
            let mut a = p;
            let mut b = p;
            a[axis] += epsilon;
            b[axis] -= epsilon;
            self.distance(a) - self.distance(b)
        }))
    }

    fn pigment(&self, p: [f64; 3], normal: [f64; 3], ray: [f64; 3], depth: f64) -> Color {
        let [x, y, z] = p;
        let [bass, mid, high] = self.drive;
        let flow = x * 1.2 + y * 0.7 + z * 0.38 + self.phase;
        let mut color = mix_light(self.palette[0], self.palette[1], 0.5 + 0.5 * flow.sin());
        if self.kind == 0 {
            color = mix_light(color, Color(205.0, 215.0, 228.0), 0.2);
        } else if self.kind == 2 {
            let layer = ((z * 7.0 + y * 4.0 + mid) * 0.7).sin() * 0.5 + 0.5;
            color = mix_light(self.palette[0], self.palette[2], layer * 0.65);
        } else if self.kind == 5 {
            color = mix_light(self.palette[0], Color(218.0, 211.0, 198.0), 0.35);
        } else if self.kind == 7 {
            let fringe = dot(normal, ray).abs() * 9.0 + flow * 0.9 + mid;
            color = mix_light(self.palette[0], self.palette[1], fringe.sin() * 0.5 + 0.5);
            color = mix_light(color, self.palette[2], (fringe * 0.7).cos().max(0.0) * 0.7);
        }
        let light = unit([-0.5, 0.8, -0.55]);
        let diffuse = (dot(normal, light) * 0.5 + 0.5).clamp(0.0, 1.0);
        let facing = dot(normal, ray).abs();
        let rim = (1.0 - facing).powi(2);
        let front = (z * 0.8 + x - y - self.time * 0.35).sin() * 0.5 + 0.5;
        let emission = 0.85 + bass * 0.22 + mid * 0.18 + high * front * 0.2;
        let core = mix_light(color, Color(245.0, 238.0, 255.0), 0.28 + diffuse * 0.22);
        let intensity = emission * (0.7 + diffuse * 0.32 + rim * 0.35);
        let haze = 1.0 / (1.0 + depth * 0.025);
        Color(
            core.0 * intensity * haze,
            core.1 * intensity * haze,
            core.2 * intensity * haze,
        )
    }

    fn ray(&self, origin: [f64; 3], direction: [f64; 3], background: Color) -> (Color, f64) {
        if matches!(self.kind, 4 | 6) {
            return self.cloud_ray(origin, direction, background);
        }
        let mut distance = 0.08;
        let mut transmission = 1.0;
        let mut light = [0.0; 3];
        let fog = mix_light(background, self.palette[1], 0.12);
        let steps = if self.kind == 0 { 96 } else { 64 };
        for _ in 0..steps {
            if distance > 24.0 || transmission < 0.06 {
                break;
            }
            let p = std::array::from_fn(|i| origin[i] + direction[i] * distance);
            let d = self.distance(p);
            let epsilon = 0.0015 + distance * 0.0003;
            if d < epsilon {
                let mut normal = self.normal(p, (0.003 + distance * 0.0005).max(epsilon));
                if dot(normal, direction) > 0.0 {
                    normal = normal.map(|v| -v);
                }
                let surface = self.pigment(p, normal, direction, distance);
                let haze = 1.0 - (-distance * 0.09).exp();
                let surface = mix_light(surface, fog, haze);
                let opacity = if self.kind == 3 {
                    0.27 + dot(normal, direction).abs() * 0.12
                } else if self.kind == 1 {
                    0.74 + dot(normal, direction).abs() * 0.12
                } else if self.kind == 7 {
                    0.38
                } else {
                    1.0
                };
                for (channel, value) in light.iter_mut().zip([surface.0, surface.1, surface.2]) {
                    *channel += value * transmission * opacity;
                }
                transmission *= 1.0 - opacity;
                distance += 0.14;
            } else {
                distance += (d * 0.8).clamp(0.002, 0.65);
            }
        }
        if self.kind == 0 && transmission == 1.0 && direction[1] < 0.0 {
            let point = |t: f64| std::array::from_fn(|i| origin[i] + direction[i] * t);
            if self.distance(point(24.0)) < 0.0 {
                let mut lo = distance.clamp(0.08, 24.0);
                if self.distance(point(lo)) < 0.0 {
                    lo = 0.08;
                }
                let mut hi = 24.0;
                for _ in 0..16 {
                    let mid = (lo + hi) * 0.5;
                    if self.distance(point(mid)) > 0.0 {
                        lo = mid;
                    } else {
                        hi = mid;
                    }
                }
                let p = point(hi);
                let mut normal = self.normal(p, 0.003 + hi * 0.0005);
                if dot(normal, direction) > 0.0 {
                    normal = normal.map(|v| -v);
                }
                let surface = self.pigment(p, normal, direction, hi);
                let surface = mix_light(surface, fog, 1.0 - (-hi * 0.09).exp());
                light = [surface.0, surface.1, surface.2];
                transmission = 0.0;
            }
        }
        (Color(light[0], light[1], light[2]), transmission)
    }

    fn cloud_ray(&self, origin: [f64; 3], direction: [f64; 3], background: Color) -> (Color, f64) {
        let mut transmission = 1.0;
        let mut light = [0.0; 3];
        let [bass, mid, high] = self.drive;
        let mut previous = 0.1;
        for slice in 0..40 {
            let depth = 0.15 + (slice as f64 / 39.0).powf(1.55) * 19.0;
            let step = depth - previous;
            previous = depth;
            let [x, y, z] = std::array::from_fn(|i| origin[i] + direction[i] * depth);
            let mut density;
            let mut color;
            if self.kind == 4 {
                let q1 =
                    (x * (1.3 + self.variant * 0.15) + z * 0.7 + (y * 1.6 - z * 0.4).sin()).sin();
                let q2 = (y * 1.4 - z * 0.9 + (x * 1.7 + z * 0.5).sin()).sin();
                let q3 = (z * 0.8 + q1 * 0.9 - q2 * 0.8 + self.phase).sin();
                let curl = q1 * q2;
                let fold = q1 + q2 * (0.8 + mid * 0.009) + q3 * (0.4 + mid * 0.0075) - 0.2;
                let vein =
                    (x * 13.0 + q1 * 4.0 + z * 7.0).sin() * (y * 11.0 + q2 * 3.0 - z * 4.0).cos();
                let center = 0.5 * (z * 0.35 + self.phase).sin();
                let envelope = (-((x - center).powi(2) * 0.32 + y * y * 0.13)).exp();
                density = (-fold.abs() * (9.0 - mid * 0.06)).exp()
                    * envelope
                    * 0.95
                    * (1.0 - (-(x * x + y * y) / 0.25).exp());
                color = mix_light(
                    self.palette[0],
                    self.palette[2],
                    0.5 + 0.5 * (z * 0.4 + curl).sin(),
                );
                let shade = 0.9 + vein.max(0.0) * 0.12 + q3.max(0.0) * 0.1;
                color = mix_light(color, Color(245.0, 238.0, 255.0), 0.3);
                let emission = shade * (1.0 + bass * 0.2 + mid * 0.16 + high * 0.18);
                color = Color(color.0 * emission, color.1 * emission, color.2 * emission);
            } else {
                let mut nearest = f64::INFINITY;
                for strand in 0..5 {
                    let [cx, cy] = self.strand(z, strand);
                    nearest = nearest.min((x - cx).hypot(y - cy));
                }
                let width = 0.025 + depth * 0.004 + high * 0.00054;
                density = (width / (nearest + width)).powi(3) * 0.9;
                color = mix_light(self.palette[0], Color(230.0, 240.0, 255.0), 0.65);
                color = Color(
                    color.0 * (1.5 + bass * 0.2 + mid * 0.18 + high * 0.2),
                    color.1 * (1.5 + bass * 0.2 + mid * 0.18 + high * 0.2),
                    color.2 * (1.5 + bass * 0.2 + mid * 0.18 + high * 0.2),
                );
            }
            density *= step * (if self.kind == 6 { 6.0 } else { 1.8 });
            let opacity = (1.0 - (-density).exp()).min(0.85);
            let fog = 1.0 - (-depth * 0.05).exp();
            color = mix_light(color, background, fog);
            for (channel, value) in light.iter_mut().zip([color.0, color.1, color.2]) {
                *channel += value * opacity * transmission;
            }
            transmission *= 1.0 - opacity;
            if transmission < 0.03 {
                break;
            }
        }
        (Color(light[0], light[1], light[2]), transmission)
    }
}

impl Painter {
    pub(super) fn material(&mut self, t: f64, pose: Pose, a: AudioFrame) {
        self.atmosphere(t, pose);
        let kind = match self.style.as_str() {
            "ferro" => 0,
            "cymatics" => 1,
            "topography" => 2,
            "glass" => 3,
            "ink" => 4,
            "kinetic" => 5,
            "lightning" => 6,
            "interference" => 7,
            _ => unreachable!(),
        };
        let drive = std::array::from_fn(|region| {
            let energy = a.bands[[0..16, 16..40, 40..64][region].clone()]
                .iter()
                .sum::<f64>()
                / [16.0, 24.0, 24.0][region];
            (energy * 0.9 + a.attacks[region] * (-a.attack_age[region] * 6.0).exp() * 0.4).min(1.0)
        });
        let palette = std::array::from_fn(|index| {
            mix_light(
                self.palette[index],
                self.palette[(index + 1) % 3],
                drive[index] * 0.65,
            )
        });
        let space = Space {
            kind,
            variant: self.variant,
            phase: self.phase,
            time: t * 0.65,
            flow: t * 0.16,
            drive,
            palette,
        };
        let origin = space.origin();
        let divisor = if matches!(kind, 4 | 6) { 3 } else { 2 };
        let (w, h) = (WIDTH / divisor, HEIGHT / divisor);
        let mut field = image::Rgba32FImage::new(w as u32, h as u32);
        let roll = (t * 0.09 + self.phase).sin() * 0.12;
        let (sin, cos) = roll.sin_cos();
        let sample = |px: f64, py: f64| {
            let u = px / w as f64 - 0.5;
            let v = 0.5 - py / h as f64;
            let (dx, dy) = (u * 1.4, v * 2.48);
            let direction = unit([
                dx * cos - dy * sin + pose.x / 1800.0,
                dx * sin + dy * cos + if kind == 0 { -0.45 } else { pose.y / 2200.0 },
                1.0,
            ]);
            let ix = ((px * divisor as f64) as usize).min(WIDTH - 1);
            let iy = ((py * divisor as f64) as usize).min(HEIGHT - 1);
            let i = (iy * WIDTH + ix) * 3;
            let bg = Color(
                self.base[i] as f64,
                self.base[i + 1] as f64,
                self.base[i + 2] as f64,
            );
            let wash = mix_light(palette[0], palette[1], (v + 0.5).clamp(0.0, 1.0));
            let background = 0.04 + drive[0] * 0.1 + drive[1] * 0.1 + drive[2] * 0.08;
            let bg = Color(
                bg.0 + wash.0 * background,
                bg.1 + wash.1 * background,
                bg.2 + wash.2 * background,
            );
            let (c, transmission) = space.ray(origin, direction, bg);
            let c = Color(
                c.0 + wash.0 * background * transmission,
                c.1 + wash.1 * background * transmission,
                c.2 + wash.2 * background * transmission,
            );
            [
                c.0 as f32 / RADIANCE,
                c.1 as f32 / RADIANCE,
                c.2 as f32 / RADIANCE,
                transmission as f32,
            ]
        };
        render_samples(&mut field, None, sample);
        let edges = if !matches!(kind, 4 | 6) {
            let edges: Vec<_> = (0..w * h)
                .map(|index| {
                    let (x, y) = ((index % w) as u32, (index / w) as u32);
                    let center = field.get_pixel(x, y).0;
                    [
                        (x.saturating_sub(1), y),
                        ((x + 1).min(w as u32 - 1), y),
                        (x, y.saturating_sub(1)),
                        (x, (y + 1).min(h as u32 - 1)),
                    ]
                    .iter()
                    .any(|&(xx, yy)| {
                        let other = field.get_pixel(xx, yy).0;
                        (0..3).any(|c| (center[c] - other[c]).abs() * RADIANCE > 12.0)
                            || (center[3] - other[3]).abs() > 0.07
                    })
                })
                .collect();
            render_samples(&mut field, Some(&edges), sample);
            Some(edges)
        } else {
            None
        };
        let diffusion = image::imageops::blur(&field, 0.7);
        for (index, (value, soft)) in field
            .as_mut()
            .iter_mut()
            .zip(diffusion.as_raw())
            .enumerate()
        {
            let amount = if edges.as_ref().is_some_and(|mask| mask[index / 4]) {
                0.45
            } else {
                0.12
            };
            *value = *value * (1.0 - amount) + soft * amount;
        }
        let field = image::imageops::resize(
            &field,
            WIDTH as u32,
            HEIGHT as u32,
            image::imageops::FilterType::Triangle,
        );
        for (index, (target, rgba)) in self
            .pixels
            .as_chunks_mut::<3>()
            .0
            .iter_mut()
            .zip(field.as_raw().as_chunks::<4>().0.iter())
            .enumerate()
        {
            let transmission = rgba[3].clamp(0.0, 1.0);
            for c in 0..3 {
                target[c] = target[c] * transmission + rgba[c].max(0.0) * RADIANCE;
            }
            if (index % WIDTH).is_multiple_of(4) && (index / WIDTH).is_multiple_of(4) {
                let gi = ((index / WIDTH / 4) * (WIDTH / 4) + index % WIDTH / 4) * 3;
                for c in &mut self.glow[gi..gi + 3] {
                    *c *= transmission;
                }
            }
        }
        if kind == 6 {
            let project = |p: [f64; 3]| {
                let d = p[2] - origin[2];
                let dx = (p[0] - origin[0]) / d - pose.x / 1800.0;
                let dy = (p[1] - origin[1]) / d - pose.y / 2200.0;
                (
                    270.0 + (dx * cos + dy * sin) * WIDTH as f64 / 1.4,
                    480.0 - (-dx * sin + dy * cos) * HEIGHT as f64 / 2.48,
                )
            };
            for strand in 0..5 {
                let mut previous = None;
                for step in (1..600).rev() {
                    let depth = 0.2 + step as f64 * 0.04;
                    let z = origin[2] + depth;
                    let [x, y] = space.strand(z, strand);
                    let point = project([x, y, z]);
                    let intensity = (1.0 - depth / 25.0).powi(2) * (0.7 + drive[2] * 0.2);
                    let core = mix_light(self.palette[0], Color(225.0, 240.0, 255.0), 0.7);
                    if let Some((px, py)) = previous {
                        self.line(px, py, point.0, point.1, core, intensity);
                    }
                    if step % 80 == 0 {
                        let endpoint = project([
                            x + (z * 3.0).sin() * 0.35,
                            y + (z * 2.0).cos() * 0.35,
                            z + 0.3,
                        ]);
                        self.line(
                            point.0,
                            point.1,
                            endpoint.0,
                            endpoint.1,
                            self.palette[2],
                            intensity * 0.7,
                        );
                    }
                    previous = Some(point);
                }
            }
        }
        self.material_bloom();
    }

    fn material_bloom(&mut self) {
        let (w, h) = (WIDTH / 4, HEIGHT / 4);
        let mut highlights = image::Rgb32FImage::new(w as u32, h as u32);
        for y in 0..h {
            for x in 0..w {
                let mut color = [0.0; 3];
                for dy in 0..4 {
                    for dx in 0..4 {
                        let i = ((y * 4 + dy) * WIDTH + x * 4 + dx) * 3;
                        for (c, value) in color.iter_mut().enumerate() {
                            *value += self.pixels[i + c] / 16.0;
                        }
                    }
                }
                let peak = color.into_iter().fold(0.0_f32, f32::max);
                let weight = (peak - 45.0).max(0.0) / (peak + 1.0) * 0.55;
                highlights.put_pixel(
                    x as u32,
                    y as u32,
                    image::Rgb(color.map(|v| v * weight / RADIANCE)),
                );
            }
        }
        let near = image::imageops::blur(&highlights, 2.0);
        let wide = image::imageops::blur(&highlights, 7.0);
        for ((target, a), b) in self.glow.iter_mut().zip(near.as_raw()).zip(wide.as_raw()) {
            *target += (a * 0.55 + b * 0.45) * RADIANCE;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn subpixel_coverage_smooths_diagonal_and_preserves_radiance() {
        let edge = covered_pixel(0.0, 0.0, |x, y| {
            if x + y < 1.0 {
                [0.8, 0.4, 0.2, 0.0]
            } else {
                [0.0, 0.0, 0.0, 1.0]
            }
        });
        assert_eq!(edge, [0.4, 0.2, 0.1, 0.5]);
        let uniform = covered_pixel(0.0, 0.0, |_, _| [0.7, 0.3, 0.1, 0.6]);
        assert_eq!(uniform, [0.7, 0.3, 0.1, 0.6]);
    }

    #[test]
    fn parallel_rows_preserve_pixel_positions_and_uniform_filtering() {
        let mut field = image::Rgba32FImage::new(9, 11);
        let sample = |x: f64, y: f64| [(x / 20.0) as f32, (y / 20.0) as f32, 0.25, 1.0];
        render_samples(&mut field, None, sample);
        for y in 0..11 {
            for x in 0..9 {
                let actual = field.get_pixel(x, y).0;
                let expected = sample(x as f64 + 0.5, y as f64 + 0.5);
                for c in 0..4 {
                    assert!((actual[c] - expected[c]).abs() < 1e-6);
                }
            }
        }
        let original = field.clone();
        render_samples(&mut field, Some(&[true; 9 * 11]), sample);
        for (a, b) in field.as_raw().iter().zip(original.as_raw()) {
            assert!((a - b).abs() < 1e-6);
        }
    }

    #[test]
    fn music_changes_emission_with_only_small_surface_deformation() {
        for kind in [0, 1, 2, 3, 5, 7] {
            let mut space = Space {
                kind,
                variant: 0.0,
                phase: 1.3,
                time: 4.0,
                flow: 1.0,
                drive: [0.0; 3],
                palette: [Color(80.0, 150.0, 230.0); 3],
            };
            let point = [0.9, 0.7, 1.4];
            let distance = space.distance(point);
            let quiet = space.pigment(point, [0.0, 1.0, 0.0], [0.0, 0.0, 1.0], 2.0);
            space.drive = [1.0; 3];
            assert!(
                (space.distance(point) - distance).abs() < 0.025,
                "geometry kicks: {kind}"
            );
            let loud = space.pigment(point, [0.0, 1.0, 0.0], [0.0, 0.0, 1.0], 2.0);
            assert!(loud.0 + loud.1 + loud.2 > quiet.0 + quiet.1 + quiet.2 + 30.0);
        }
    }

    #[test]
    fn ferro_ground_rays_reach_the_surface_without_cracks() {
        let space = Space {
            kind: 0,
            variant: 1.0,
            phase: 1.3,
            time: 4.0,
            flow: 3.0,
            drive: [0.7; 3],
            palette: [Color(120.0, 180.0, 230.0); 3],
        };
        let origin = space.origin();
        for row in 0..8 {
            for col in 0..12 {
                let direction = unit([
                    -0.8 + col as f64 * 1.6 / 11.0,
                    -0.65 + row as f64 * 0.4 / 7.0,
                    1.0,
                ]);
                let (_, transmission) = space.ray(origin, direction, Color(20.0, 30.0, 40.0));
                assert!(transmission < 0.01, "missing surface at {row},{col}");
            }
        }
    }

    #[test]
    fn camera_path_stays_clear_of_solid_surfaces() {
        for kind in [0, 1, 2, 3, 5, 7] {
            for variant in 0..3 {
                for step in 0..80 {
                    let time = step as f64 * 0.5;
                    let space = Space {
                        kind,
                        variant: variant as f64,
                        phase: 1.3,
                        time,
                        flow: time * 0.65,
                        drive: [1.0; 3],
                        palette: [Color(120.0, 180.0, 230.0); 3],
                    };
                    let origin = space.origin();
                    assert!(
                        space.distance(origin) > 0.02,
                        "camera intersects kind {kind}, variant {variant}, time {time}"
                    );
                }
            }
        }
    }

    #[test]
    fn float_compositing_preserves_surface_light_and_metadata_contrast() {
        let mut p = Painter::new("topography", 71, 1);
        p.pixels.fill(0.0);
        p.material(3.0, Pose::default(), AudioFrame::default());
        assert!(
            p.pixels.iter().filter(|v| **v > 20.0).count() > WIDTH * HEIGHT,
            "surface illumination lost during resizing"
        );
        let frame = p.frame(90, AudioFrame::default());
        for y in [565, 605, 660] {
            let i = (y * WIDTH + 270) * 3;
            assert!(
                frame[i..i + 3].iter().all(|v| *v < 100),
                "metadata backdrop too bright"
            );
        }
    }

    #[test]
    fn solid_world_occludes_background_and_camera_keeps_travelling() {
        let mut space = Space {
            kind: 2,
            variant: 1.0,
            phase: 0.4,
            time: 3.0,
            flow: 1.0,
            drive: [0.3; 3],
            palette: [Color(120.0, 180.0, 230.0); 3],
        };
        let ray = unit([0.7, 0.3, 1.0]);
        let (near, alpha) = space.ray([0.0, 0.0, 1.0], ray, Color(20.0, 30.0, 40.0));
        assert!(alpha < 0.01);
        assert!(near.0 > 10.0);
        let first = space.distance([0.9, 0.7, 1.4]);
        space.time += 0.001;
        let next = space.distance([0.9, 0.7, 1.4002]);
        assert!((first - next).abs() < 0.01);
        let later = space.distance([0.9, 0.7, 20.7]);
        assert!((first - later).abs() > 0.01);
    }

    #[test]
    fn every_new_material_visibly_reacts_to_each_frequency_region() {
        for style in &STYLES[17..] {
            for variant in 0..3 {
                let mut p = Painter::new(style, 71, variant);
                let quiet = AudioFrame {
                    bands: [0.06; BANDS],
                    level: 0.06,
                    ..AudioFrame::default()
                };
                p.update_field(90, quiet);
                p.pixels.copy_from_slice(&p.base);
                p.material(3.0, Pose::default(), quiet);
                let baseline = p.pixels.clone();
                for region in 0..3 {
                    let mut active = quiet;
                    let range = [0..16, 16..40, 40..64][region].clone();
                    active.bands[range].fill(0.9);
                    active.attacks[region] = 0.9;
                    active.attack_age[region] = 0.0;
                    p.pixels.copy_from_slice(&p.base);
                    p.glow.fill(0.0);
                    p.material(3.0, Pose::default(), active);
                    let changed = baseline
                        .as_chunks::<3>()
                        .0
                        .iter()
                        .zip(p.pixels.as_chunks::<3>().0.iter())
                        .filter(|(before, after)| {
                            before
                                .iter()
                                .zip(after.iter())
                                .any(|(x, y)| (x - y).abs() > 8.0)
                        })
                        .count();
                    assert!(
                        changed > 5000,
                        "weak response: {style}/{variant} region {region}: {changed} pixels"
                    );
                    eprintln!("{style}/{variant} region {region}: {changed} changed pixels");
                }
            }
        }
    }
}
