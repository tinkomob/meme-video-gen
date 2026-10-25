use super::*;

impl Painter {
    pub fn set_cover(&mut self, image: &image::DynamicImage) {
        let image = image
            .resize_to_fill(256, 256, image::imageops::FilterType::Triangle)
            .to_rgb8();
        let mut rng = rand::rngs::StdRng::seed_from_u64(self.phase.to_bits());
        let mode = rng.random_range(0..3);
        if mode != 0 {
            // Frequent saturated buckets preserve accents instead of averaging to grey.
            let mut buckets = [(0_u32, [0_u32; 3]); 64];
            for pixel in image.pixels() {
                let [r, g, b] = pixel.0;
                if r.max(g).max(b) < 35 || r.max(g).max(b) - r.min(g).min(b) < 18 {
                    continue;
                }
                let bucket =
                    &mut buckets[(r as usize / 64) * 16 + (g as usize / 64) * 4 + b as usize / 64];
                bucket.0 += 1;
                for (sum, value) in bucket.1.iter_mut().zip(pixel.0) {
                    *sum += value as u32;
                }
            }
            buckets.sort_by_key(|b| std::cmp::Reverse(b.0));
            for (i, (count, sums)) in buckets.iter().filter(|b| b.0 > 0).take(3).enumerate() {
                let c = sums.map(|s| s as f64 / *count as f64);
                let peak = c.into_iter().fold(1.0, f64::max);
                let color = Color(
                    c[0] * 240.0 / peak,
                    c[1] * 240.0 / peak,
                    c[2] * 240.0 / peak,
                );
                self.palette[i] =
                    mix_light(self.palette[i], color, if mode == 1 { 0.35 } else { 0.75 });
            }
        }
        // An independent palette is also a valid choice, even with a colourful cover.
        if mode != 2 {
            let other = palette(
                STYLES[rng.random_range(0..STYLES.len())],
                rng.random_range(0..3),
            );
            for (color, extra) in self.palette.iter_mut().zip(other) {
                *color = mix_light(*color, extra, rng.random_range(0.15..0.55));
            }
        }
        for (i, pixel) in self.base.as_chunks_mut::<3>().0.iter_mut().enumerate() {
            let x = (i % WIDTH) as f64;
            let y = (i / WIDTH) as f64;
            let v = (1.0 - ((x - 270.0) / 430.0).hypot((y - 480.0) / 700.0)).max(0.0);
            let color = mix_light(Color(2.0, 3.0, 5.0), self.palette[0], 0.012 + 0.025 * v);
            pixel.copy_from_slice(&[color.0 as f32, color.1 as f32, color.2 as f32]);
        }
        self.cover_soft = Some(image::imageops::resize(
            &image::imageops::blur(&image, 22.0),
            128,
            128,
            image::imageops::FilterType::Triangle,
        ));
        self.cover = Some(image);
    }

    pub(super) fn accents(&mut self, t: f64, a: AudioFrame) {
        let radius = 230.0 + 35.0 * (t * 0.17 + self.phase).sin();
        for i in 0..180 {
            let angle = i as f64 * 2.0 * PI / 180.0;
            self.point(
                270.0 + radius * angle.cos(),
                460.0 + radius * angle.sin(),
                self.palette[1],
                band_at(&a, i as f64 / 180.0) * 0.7,
            );
        }
        for i in 0..75 {
            let p = self.particles[i];
            let drift = (t * (0.08 + p.depth * 0.1) + p.phase).fract();
            self.point(
                p.u * 540.0,
                (p.v * 960.0 - drift * 150.0).rem_euclid(960.0),
                self.palette[2],
                band_at(&a, p.u + p.v * 0.37) * (0.2 + p.depth),
            );
        }
    }

    pub(super) fn chrome(&mut self, t: f64, a: AudioFrame) {
        let size = 185.0 + self.variant * 10.0;
        let cy = 430.0 + 25.0 * (t * 0.25 + self.phase).sin();
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let nx = (x as f64 - 270.0) / size;
                let ny = (y as f64 - cy) / (size * 1.25);
                let angle = ny.atan2(nx);
                let edge = 1.0
                    + (0.06 + self.morph * 0.09) * (angle * (3.0 + self.variant) + t * 0.7).sin()
                    + 0.035 * (angle * 7.0 - t * 0.4).cos();
                let energy = band_at(&a, angle / (2.0 * PI));
                let r = nx.hypot(ny) / (edge + energy * 0.09);
                if r >= 1.0 {
                    continue;
                }
                let z = (1.0 - r * r).sqrt();
                let reflected = (ny * 4.0 + z * 3.0 + (nx * 3.0 + t * 0.3).sin() * 0.6).sin();
                let stripe = (1.0 - reflected.abs()).powi(14);
                let rim = (1.0 - z).powi(5);
                let light = 0.09 + 0.35 * z + 0.55 * stripe + 0.5 * rim;
                let color = mix_light(
                    self.palette[0],
                    self.palette[1],
                    (reflected * 0.5 + 0.5) * 0.65,
                );
                let i = (y * WIDTH + x) * 3;
                for (j, value) in [color.0, color.1, color.2].into_iter().enumerate() {
                    self.pixels[i + j] += (value * light + stripe * 100.0) as f32;
                }
            }
        }
    }

    pub(super) fn silk(&mut self, t: f64, a: AudioFrame) {
        for ribbon in 0..4 {
            let phase = self.phase + ribbon as f64 * 1.5;
            for step in 0..190 {
                let u = step as f64 / 189.0;
                let y = -240.0 + u * 1440.0;
                let twist = u * (7.0 + self.variant * 2.0) - t * 0.6 + phase;
                let energy = band_at(&a, u + ribbon as f64 * 0.23);
                let x =
                    270.0 + (125.0 + energy * 35.0) * twist.sin() + 40.0 * (u * 4.0 + phase).cos();
                let width = (20.0 + 65.0 * (u * PI).sin()) * (0.25 + 0.75 * twist.cos().abs());
                let c = mix_light(self.palette[ribbon % 3], self.palette[(ribbon + 1) % 3], u);
                for strip in 0..12 {
                    let v = strip as f64 / 11.0;
                    let xx = x + (v - 0.5) * width;
                    let yy = y + (v - 0.5) * 30.0 * twist.sin();
                    self.line(
                        xx,
                        yy,
                        xx + width / 12.0,
                        yy + 2.0,
                        c,
                        (0.06 + 0.2 * (v * PI).sin().powi(3)) * (0.6 + energy * 0.4),
                    );
                }
            }
        }
    }

    pub(super) fn analog(&mut self, t: f64, a: AudioFrame) {
        let green = mix_light(
            Color(75.0, 255.0, 155.0),
            self.palette[0],
            self.variant * 0.25,
        );
        for row in 0..11 {
            let y = 160.0 + row as f64 * 55.0;
            self.line(-180.0, y, 720.0, y, green, 0.035);
        }
        for col in 0..9 {
            let x = 50.0 + col as f64 * 55.0;
            self.line(x, -300.0, x, 1260.0, green, 0.035);
        }
        for trail in (0..5).rev() {
            let lag = trail as f64 * 0.055;
            let mut last = (0.0, 0.0);
            for step in 0..460 {
                let u = step as f64 / 459.0;
                let x = -180.0 + u * 900.0;
                let y = 440.0
                    + (50.0 + 110.0 * band_at(&a, u))
                        * (u * (16.0 + self.variant * 7.0) + (t - lag) * 3.0).sin()
                    + band_at(&a, u) * 45.0 * (u * 7.0 - t).cos();
                if step > 0 {
                    self.line(last.0, last.1, x, y, green, 0.5 / (1.0 + trail as f64));
                }
                last = (x, y);
            }
        }
    }

    pub(super) fn cover_scene(&mut self, t: f64, pose: Pose, a: AudioFrame) {
        let collage = self.style == "collage";
        let Some(soft) = self.cover_soft.as_ref() else {
            return;
        };
        let zoom = 1.2 + self.variant * 0.06 + pose.zoom * 0.12;
        let angle = pose.turn * 0.12;
        let (sin, cos) = angle.sin_cos();
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let dx = (x as f64 - 270.0) / (960.0 * zoom);
                let dy = (y as f64 - 480.0) / (960.0 * zoom);
                let energy = band_at(
                    &a,
                    x as f64 / WIDTH as f64 + y as f64 / HEIGHT as f64 * 0.37,
                );
                let u = 0.5 + cos * dx + sin * dy + pose.x / 2200.0 + energy * 0.012;
                let v = 0.5 - sin * dx + cos * dy + pose.y / 2200.0;
                let sample = soft
                    .get_pixel(
                        (u.clamp(0.0, 1.0) * 127.0) as u32,
                        (v.clamp(0.0, 1.0) * 127.0) as u32,
                    )
                    .0;
                let vignette = (1.0 - 0.5 * (dx * dx + dy * dy)).max(0.0);
                let strength = if collage { 0.055 } else { 0.24 } * vignette;
                let grain = ((x * 17 + y * 31 + x * y % 23) % 7) as f64 * 0.12;
                let i = (y * WIDTH + x) * 3;
                for (j, value) in sample.into_iter().enumerate() {
                    let base = if collage { [17.0, 16.0, 15.0][j] } else { 3.0 };
                    self.pixels[i + j] = (base + value as f64 * strength + grain) as f32;
                }
            }
        }
        if !collage {
            return;
        }
        let image = self.cover.as_ref().unwrap();
        // Keep fragments above and below every layout's cover and metadata.
        for layer in 0..2 {
            let p = self.particles[layer];
            let width = 280.0 + self.variant * 25.0;
            let height = 115.0 + self.variant * 12.0;
            let cx = if layer == 0 { 75.0 } else { 465.0 };
            let cy = if layer == 0 { 60.0 } else { 865.0 };
            let cy = cy + 8.0 * (t * 0.13 + p.phase).sin() - pose.y * 0.04;
            let angle = (if layer == 0 { -0.09 } else { 0.07 }) + pose.turn * 0.06;
            let (sin, cos) = angle.sin_cos();
            let extent = width * 0.6;
            for y in (cy - extent).max(0.0) as usize..((cy + extent) as usize).min(HEIGHT) {
                for x in (cx - extent).max(0.0) as usize..((cx + extent) as usize).min(WIDTH) {
                    let dx = x as f64 - cx;
                    let dy = y as f64 - cy;
                    let u = (cos * dx + sin * dy) / width + 0.5;
                    let v = (-sin * dx + cos * dy) / height + 0.5;
                    let tear = 0.004 * (x as f64 * 0.31 + p.phase).sin();
                    let i = (y * WIDTH + x) * 3;
                    if (0.0..1.0).contains(&u) && v >= tear && v < 1.0 + tear {
                        let paper = !(0.025..0.975).contains(&v);
                        let sample = image
                            .get_pixel(
                                ((0.15 + p.u * 0.2 + u * 0.55) * 255.0) as u32,
                                ((0.15 + p.v * 0.4 + v.clamp(0.0, 1.0) * 0.22) * 255.0) as u32,
                            )
                            .0;
                        for (j, value) in sample.into_iter().enumerate() {
                            self.pixels[i + j] = if paper { 92.0 } else { value as f32 * 0.42 };
                        }
                    } else if (0.02..1.02).contains(&u) && (0.04..1.04).contains(&v) {
                        for channel in &mut self.pixels[i..i + 3] {
                            *channel *= 0.65;
                        }
                    }
                }
            }
        }
    }
}
