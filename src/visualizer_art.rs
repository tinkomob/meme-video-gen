use super::*;

impl Painter {
    pub fn set_cover(&mut self, image: &image::DynamicImage) {
        let image = image
            .resize_to_fill(256, 256, image::imageops::FilterType::Triangle)
            .to_rgb8();
        self.cover_soft = Some(image::imageops::resize(
            &image::imageops::blur(&image, 22.0),
            128,
            128,
            image::imageops::FilterType::Triangle,
        ));
        self.cover = Some(image);
    }

    pub(super) fn volume(&mut self, t: f64, pose: Pose, a: AudioFrame) {
        let (w, h) = (WIDTH / 2, HEIGHT / 2);
        let mut field = image::Rgba32FImage::new(w as u32, h as u32);
        let bodies: [_; 7] = std::array::from_fn(|i| {
            let p = self.particles[i];
            let r = self.response[i];
            let time = r.travel * (0.65 + p.u * 0.5);
            let x = (p.u - 0.5) * 2.4
                + 0.25 * (time + p.phase).sin()
                + r.opening * 0.23 * p.phase.cos();
            let y = (i as f64 / 6.0 - 0.5) * 4.0
                + 0.25 * (time * 0.71 - p.phase).sin()
                + r.opening * 0.2;
            let z = (p.depth - 0.5) * 1.6 + 0.2 * (time * 0.83 + p.phase).cos();
            let color = mix_light(
                self.palette[i % 3],
                self.palette[(i + 1) % 3],
                0.2 + 0.15 * (t * 0.12 + p.phase).sin(),
            );
            (x, y, z, 0.19 + p.v * 0.11 + r.opening * 0.12, color)
        });
        for y in 0..h {
            for x in 0..w {
                let local = self.field_at(x as f64 * 2.0, y as f64 * 2.0);
                let u = (x as f64 / w as f64 - 0.5) * 3.0 / pose.zoom.max(0.7) - pose.x / 900.0
                    + local.dx / 420.0;
                let v = (y as f64 / h as f64 - 0.5) * 5.0 / pose.zoom.max(0.7) - pose.y / 1100.0
                    + local.dy / 500.0;
                let mut light = [0.0_f32; 3];
                let mut transmission = 1.0;
                let slices = if self.style == "nebula" { 6 } else { 16 };
                for slice in 0..slices {
                    let z = slice as f64 * 3.3 / (slices - 1) as f64 - 1.65;
                    let flow_time = local.travel;
                    let breath = (t * 0.72 + u * 1.3 - v * 0.8 + z + self.phase).sin();
                    let curl = (u * 2.1 + v * 1.7 + flow_time * 0.4).sin();
                    let u = u + 0.045 * breath + 0.065 * curl * (v + z).cos();
                    let v = v + 0.045 * (breath + z).cos() - 0.065 * curl * (u - z).sin();
                    let u = u + local.opening * 0.12 * (v * 2.3 + z).sin();
                    let v = v + local.opening * 0.1 * (u * 3.1 - z).cos();
                    let flow = (u * 1.7 + z * 1.3 + flow_time * 0.45).sin()
                        * (v * 1.4 - z * 1.7 - flow_time * 0.32).cos();
                    let folds = ((u + flow * 0.5) * 4.3 + z * 3.1).sin()
                        * ((v - flow * 0.4) * 3.5 - z * 2.3 + flow_time * 0.38).cos();
                    let grain = ((u + folds * 0.09) * 17.0 + z * 11.0).sin()
                        * ((v + flow * 0.08) * 14.0 - z * 9.0).cos();
                    let mut density;
                    let mut normal = [0.0; 3];
                    let mut color;
                    if self.style == "liquid" {
                        let mut mass = 0.0;
                        let mut pigment = [0.0; 3];
                        for &(cx, cy, cz, radius, c) in &bodies {
                            let (dx, dy, dz) = (u - cx, v - cy, z - cz);
                            let d = 0.1 + dx * dx + dy * dy + dz * dz;
                            let influence = radius / d;
                            mass += influence;
                            for (n, delta) in normal.iter_mut().zip([dx, dy, dz]) {
                                *n += delta * influence / d;
                            }
                            for (out, value) in pigment.iter_mut().zip([c.0, c.1, c.2]) {
                                *out += influence * value;
                            }
                        }
                        let distance = (u * u + v * v * 0.4 + z * z * 0.5).sqrt();
                        let wave = audio_wave(a, 0, distance);
                        let folds_audio = audio_wave(a, 1, distance);
                        let veins = (folds * 3.0 + grain * 0.3 + flow_time * 0.12).sin().abs();
                        let surface = mass - 1.3
                            + folds * (0.14 + folds_audio * 0.07)
                            + grain * 0.025
                            + wave * 0.16
                            + breath * 0.045;
                        normal[0] += wave * u * 0.6;
                        normal[1] += wave * v * 0.4;
                        density = 0.55 / (1.0 + (-surface * 14.0).exp());
                        color = Color(pigment[0] / mass, pigment[1] / mass, pigment[2] / mass);
                        color = mix_light(color, self.palette[2], (-veins * 18.0).exp() * 0.48);
                        density *= 0.78 + veins * 0.22;
                    } else {
                        let radius = (u * u + v * v * 0.44).sqrt();
                        let structure = if self.style == "bloom" {
                            let lobes = (u.atan2(v) * (4.0 + self.variant) + z * 1.7).sin();
                            let surface = radius
                                - 1.0
                                - lobes * (0.18 + pose.unfold * 0.23)
                                - flow * 0.25
                                - folds * 0.08
                                - local.opening * 0.15;
                            let petal = audio_wave(a, 1, radius + z.abs() * 0.3);
                            (-((surface - petal * 0.08) * 7.0).powi(2) - z * z * 0.5).exp()
                        } else {
                            let threads = ((u + folds * 0.12) * 41.0 + z * 7.0).sin()
                                * ((v + flow * 0.16) * 29.0 - z * 11.0).cos();
                            let cloud = flow * 0.55 + folds * 0.3 + grain * 0.13 + threads * 0.06;
                            let ridge = (1.0 - (cloud * 3.2 + 0.15).abs()).max(0.0).powi(4);
                            (ridge * (0.65 + grain * 0.3 + threads * 0.12)
                                + (cloud - 0.3).max(0.0) * 0.35)
                                * (-u * u * 0.28 - v * v * 0.09 - z * z * 0.25).exp()
                        };
                        density = structure * (0.22 + 0.22 * (folds * 2.0).sin().abs());
                        normal = [flow + folds * 0.5, (v * 2.1 - z).cos(), 0.45 + grain * 0.12];
                        let pigment =
                            0.5 + 0.5 * (v * 1.1 + u * 0.6 + z * 0.8 + flow + t * 0.04).sin();
                        let sector = pigment * 2.0;
                        let index = (sector as usize).min(1);
                        color = mix_light(
                            self.palette[index],
                            self.palette[index + 1],
                            sector - index as f64,
                        );
                        color = self.local_color(color, local, z * 0.2);
                    }
                    density *= 0.8 + 0.2 * local.opening;
                    let length = (normal.iter().map(|n| n * n).sum::<f64>())
                        .sqrt()
                        .max(0.001);
                    let light_z = if self.style == "liquid" { -0.7 } else { 0.7 };
                    let diffuse = ((normal[0] * -0.45 + normal[1] * -0.55 + normal[2] * light_z)
                        / length)
                        .max(0.0);
                    let rim = (1.0 - (normal[2] / length).abs()).powi(3);
                    let reflection = if self.style == "liquid" {
                        (1.0 - (normal[0] / length * 2.1 + normal[1] / length * 3.0 + flow * 0.4)
                            .sin()
                            .abs())
                        .powi(12)
                            * diffuse
                    } else {
                        0.0
                    };
                    let light_front =
                        (0.5 + 0.5 * (u * 2.4 + v * 1.8 + z - flow_time * 2.0).sin()).powi(4);
                    let glint = audio_wave(
                        a,
                        if self.style == "nebula" { 0 } else { 2 },
                        (u * 0.6 + v * 0.4 + z * 0.3).abs(),
                    )
                    .abs();
                    let accent = local.light * (0.25 + 0.75 * light_front) + glint * 0.7;
                    let highlight = diffuse.powi(14) * 0.45 + reflection * (0.5 + accent);
                    let backlight = rim * (0.5 + 0.5 * (z + flow_time * 0.2).sin());
                    let shadow = mix_light(self.palette[1], color, 0.3);
                    color = mix_light(shadow, color, 0.45 + diffuse * 0.55);
                    let neon = self.neon_strength(x as f64 * 2.0, y as f64 * 2.0);
                    let illumination = 0.25
                        + diffuse * 0.9
                        + rim * 0.4
                        + backlight * 0.45
                        + neon * rim * 1.4
                        + accent * (1.6 + rim * 2.5);
                    let opacity = density.clamp(0.0, 0.85);
                    let brightness = opacity * transmission * illumination;
                    let tint = mix_light(color, self.palette[0], 0.35);
                    for ((channel, value), specular) in light
                        .iter_mut()
                        .zip([color.0, color.1, color.2])
                        .zip([tint.0, tint.1, tint.2])
                    {
                        *channel += ((value * brightness
                            + highlight * (45.0 + specular * 0.3) * opacity * transmission)
                            / 255.0) as f32;
                    }
                    transmission *= 1.0 - opacity;
                }
                field.put_pixel(
                    x as u32,
                    y as u32,
                    image::Rgba([light[0], light[1], light[2], transmission as f32]),
                );
            }
        }
        let field = image::imageops::resize(
            &field,
            WIDTH as u32,
            HEIGHT as u32,
            image::imageops::FilterType::CatmullRom,
        );
        for (i, (target, value)) in self
            .pixels
            .as_chunks_mut::<3>()
            .0
            .iter_mut()
            .zip(field.as_raw().as_chunks::<4>().0.iter())
            .enumerate()
        {
            if (i % WIDTH).is_multiple_of(4) && (i / WIDTH).is_multiple_of(4) {
                let gi = ((i / WIDTH / 4) * (WIDTH / 4) + i % WIDTH / 4) * 3;
                for glow in &mut self.glow[gi..gi + 3] {
                    *glow *= value[3].clamp(0.0, 1.0);
                }
            }
            for c in 0..3 {
                target[c] = target[c] * value[3].clamp(0.0, 1.0) + value[c] * 255.0;
            }
        }
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
        let size = (180.0 + self.variant * 8.0) * self.spread;
        let metal = mix_light(Color(205.0, 214.0, 225.0), self.palette[1], 0.08);
        for y in 0..HEIGHT {
            let v = (y as f64 - 70.0) / (size * 3.0);
            if !(0.0..1.0).contains(&v) {
                continue;
            }
            let taper = (v * PI).sin().powf(0.65);
            let bend = v * PI * (1.35 + self.variant * 0.12) + self.phase * 0.15 + t * 0.015;
            let centers = [
                270.0 - size * 0.9 + size * 0.3 * bend.sin(),
                270.0 + size * 0.88 + size * 0.18 * (bend + 1.8).sin(),
            ];
            let widths = [
                (22.0 + 14.0 * self.morph) * taper * self.spread,
                (16.0 + 11.0 * self.morph) * taper * self.spread,
            ];
            for x in 0..WIDTH {
                let local = self.field_at(x as f64, y as f64);
                let xx = x as f64 + local.dx * 0.15;
                let offsets = [
                    (xx - centers[0]) / widths[0].max(0.5),
                    (xx - centers[1]) / widths[1].max(0.5),
                ];
                let strip = usize::from(offsets[1].abs() < offsets[0].abs());
                let q = offsets[strip].clamp(-1.0, 1.0);
                let coverage = (widths[strip] - (xx - centers[strip]).abs() + 0.5).clamp(0.0, 1.0);
                if coverage == 0.0 {
                    continue;
                }
                // Fold normals reflect broad studio lights across a thin opaque metal sheet.
                let twist = (bend + strip as f64 * 1.8).cos() * 0.7;
                let mut nx = -(1.4 * (q * PI + twist).sin() + twist);
                let mut ny = nx * (bend + strip as f64 * 1.8).cos() * 0.25
                    + 0.15 * (v * PI * 2.0 + self.phase).sin();
                let mut nz = 1.0;
                let length = (nx * nx + ny * ny + nz * nz).sqrt().max(0.001);
                nx /= length;
                ny /= length;
                nz /= length;
                let rx = 2.0 * nx * nz;
                let ry = 2.0 * ny * nz;
                let rz = 2.0 * nz * nz - 1.0;
                let key = (-((rx + 0.48) / 0.23).powi(2) - ((ry + 0.15) / 0.85).powi(2)).exp();
                let fill = (-((rx - 0.65) / 0.32).powi(2) - ((ry - 0.35) / 0.6).powi(2)).exp();
                let ceiling = (-(rx / 0.8).powi(2) - ((ry + 0.8) / 0.16).powi(2)).exp();
                let glint = audio_wave(a, 1, v * 2.8 + strip as f64 * 0.2).abs();
                let reflection = (key * (0.8 + glint * 0.35) + fill * 0.32 + ceiling * 0.45)
                    * (0.35 + 0.65 * (rz * 0.5 + 0.5));
                let light = 0.055 + reflection;
                let i = (y * WIDTH + x) * 3;
                if x.is_multiple_of(4) && y.is_multiple_of(4) {
                    let gi = ((y / 4) * (WIDTH / 4) + x / 4) * 3;
                    for glow in &mut self.glow[gi..gi + 3] {
                        *glow *= (1.0 - coverage) as f32;
                    }
                }
                for (j, value) in [metal.0, metal.1, metal.2].into_iter().enumerate() {
                    self.pixels[i + j] = self.pixels[i + j] * (1.0 - coverage) as f32
                        + ((value * light + reflection.powi(3) * 55.0) * coverage) as f32;
                }
            }
        }
    }

    pub(super) fn silk(&mut self, t: f64, a: AudioFrame) {
        for ribbon in 0..4 {
            let phase = self.phase + ribbon as f64 * 1.5;
            let head = silk_head(t, self.phase + ribbon as f64 * 0.75);
            let mut previous = [None; 12];
            for step in 0..=360 {
                let along = step as f64 / 360.0;
                let u = head - 0.95 + along * 0.95;
                let y = -240.0 + u * 1440.0;
                let twist = u * (7.0 + self.variant * 2.0) + phase;
                let energy = band_at(&a, u + ribbon as f64 * 0.23);
                let fold = audio_wave(a, 1, (u * 3.0 - ribbon as f64 * 0.25).abs());
                let shimmer = audio_wave(a, 2, u * 3.0 + ribbon as f64 * 0.12).abs();
                let x = 270.0
                    + (125.0 + energy * 35.0) * twist.sin()
                    + 40.0 * (u * 4.0 + phase).cos()
                    + fold * 24.0;
                let taper = (along / 0.12).min((1.0 - along) / 0.08).clamp(0.0, 1.0);
                let width = (20.0 + 65.0 * (u * PI).sin().abs())
                    * (0.25 + 0.75 * twist.cos().abs())
                    * taper;
                let c = mix_light(self.palette[ribbon % 3], self.palette[(ribbon + 1) % 3], u);
                for (strip, previous) in previous.iter_mut().enumerate() {
                    let v = strip as f64 / 11.0;
                    let xx = x + (v - 0.5) * width;
                    let yy = y + (v - 0.5) * (30.0 * twist.sin() + fold * 35.0);
                    let facing = (twist.cos() * (v - 0.5) * 2.0).max(0.0);
                    if let Some((last_x, last_y)) = *previous {
                        self.line(
                            last_x,
                            last_y,
                            xx,
                            yy,
                            c,
                            (0.09
                                + 0.38 * (v * PI).sin().powi(3)
                                + facing.powi(8) * (0.4 + shimmer))
                                * (0.6 + energy * 0.4)
                                * taper,
                        );
                    }
                    *previous = Some((xx, yy));
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
                    + band_at(&a, u) * 45.0 * (u * 7.0 - t).cos()
                    + audio_wave(a, 1, u * 3.0 + lag) * 35.0;
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
                let local = self.field_at(x as f64, y as f64);
                let dx = (x as f64 - 270.0 + local.dx) / (960.0 * zoom);
                let dy = (y as f64 - 480.0 + local.dy) / (960.0 * zoom);
                let energy = band_at(
                    &a,
                    x as f64 / WIDTH as f64 + y as f64 / HEIGHT as f64 * 0.37,
                );
                let u = 0.5 + cos * dx + sin * dy + pose.x / 2200.0 + energy * 0.012;
                let v = 0.5 - sin * dx + cos * dy + pose.y / 2200.0;
                let sample = image::imageops::sample_bilinear(
                    soft,
                    u.clamp(0.0, 1.0) as f32,
                    v.clamp(0.0, 1.0) as f32,
                )
                .unwrap()
                .0;
                let vignette = (1.0 - 0.5 * (dx * dx + dy * dy)).max(0.0);
                let pulse = audio_wave(a, 0, dx.hypot(dy) * 4.0).abs();
                let strength = if collage { 0.055 } else { 0.24 }
                    * vignette
                    * (0.85 + pose.unfold * 0.3 + pulse * 0.2);
                let grain = ((x * 17 + y * 31 + x * y % 23) % 7) as f64 * 0.12;
                let i = (y * WIDTH + x) * 3;
                for (j, value) in sample.into_iter().enumerate() {
                    let base = if collage { [17.0, 16.0, 15.0][j] } else { 3.0 };
                    self.pixels[i + j] =
                        (base + value as f64 * strength * 0.65 + local.color[j] * 0.035 + grain)
                            as f32;
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
            let cy = if layer == 0 { 40.0 } else { 890.0 };
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
                    let coverage =
                        (u.min(1.0 - u) * width).min((v - tear).min(1.0 + tear - v) * height);
                    let coverage = (coverage + 0.5).clamp(0.0, 1.0) as f32;
                    let shadow =
                        ((u - 0.02).min(1.02 - u) * width).min((v - 0.04).min(1.04 - v) * height);
                    let shadow = (shadow + 0.5).clamp(0.0, 1.0) as f32;
                    for channel in &mut self.pixels[i..i + 3] {
                        *channel *= 1.0 - 0.35 * shadow;
                    }
                    if coverage > 0.0 {
                        let paper =
                            ((0.025 - v).max(v - 0.975) * height + 0.5).clamp(0.0, 1.0) as f32;
                        let sample = image::imageops::sample_bilinear(
                            image,
                            (0.15 + p.u * 0.2 + u.clamp(0.0, 1.0) * 0.55) as f32,
                            (0.15 + p.v * 0.4 + v.clamp(0.0, 1.0) * 0.22) as f32,
                        )
                        .unwrap()
                        .0;
                        for (j, value) in sample.into_iter().enumerate() {
                            let value = 92.0 * paper + value as f32 * 0.42 * (1.0 - paper);
                            self.pixels[i + j] =
                                self.pixels[i + j] * (1.0 - coverage) + value * coverage;
                        }
                    }
                }
            }
        }
    }
}

// Restart only after the tail has left the frame, including the depth-layer overscan.
fn silk_head(t: f64, phase: f64) -> f64 {
    -0.35 + (t * 0.22 + phase).rem_euclid(3.0)
}

#[cfg(test)]
mod silk_tests {
    use super::*;

    #[test]
    fn ribbons_advance_continuously_and_restart_outside_the_frame() {
        let dt = 1.0 / FPS as f64;
        for frame in 0..20 * FPS {
            let before = silk_head(frame as f64 / FPS as f64, 1.5);
            let after = silk_head(frame as f64 / FPS as f64 + dt, 1.5);
            if after < before {
                assert!(before - 0.95 > 1.6, "tail still visible at restart");
                assert!(after < -0.3, "head restarted inside the frame");
            } else {
                assert!((after - before - dt * 0.22).abs() < 1e-12);
            }
        }
    }
}
