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

    pub(super) fn volume(&mut self, t: f64, pose: Pose, _a: AudioFrame) {
        let (w, h) = (WIDTH / 2, HEIGHT / 2);
        let mut field = image::Rgb32FImage::new(w as u32, h as u32);
        // Music advances flow; attacks illuminate the material without displacing it.
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
                motion::hue_color(p.u + t * (0.008 + p.v * 0.01) + r.opening * 0.1),
                0.55,
            );
            (x, y, z, 0.19 + p.v * 0.11 + r.opening * 0.12, color)
        });
        for y in 0..h {
            for x in 0..w {
                let local = self.field_at(x as f64 * 2.0, y as f64 * 2.0);
                let u = (x as f64 / w as f64 - 0.5) * 3.0 - pose.x / 900.0 + local.dx / 420.0;
                let v = (y as f64 / h as f64 - 0.5) * 5.0 - pose.y / 1100.0 + local.dy / 500.0;
                let mut light = [0.0_f32; 3];
                let mut transmission = 1.0;
                let slices = if self.style == "nebula" { 6 } else { 16 };
                for slice in 0..slices {
                    let z = slice as f64 * 3.3 / (slices - 1) as f64 - 1.65;
                    let flow_time = local.travel;
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
                        let surface = mass - 1.3 + folds * 0.14 + grain * 0.025;
                        density = 0.55 / (1.0 + (-surface * 14.0).exp());
                        color = Color(pigment[0] / mass, pigment[1] / mass, pigment[2] / mass);
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
                            (-(surface * 7.0).powi(2) - z * z * 0.5).exp()
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
                    let accent = local.light * (0.25 + 0.75 * light_front);
                    let highlight = diffuse.powi(14) * 0.8 + reflection * (0.7 + accent);
                    let neon = self.neon_strength(x as f64 * 2.0, y as f64 * 2.0);
                    let illumination = 0.25
                        + diffuse * 0.9
                        + rim * 0.55
                        + neon * rim * 1.4
                        + accent * (1.6 + rim * 2.5);
                    let opacity = density.clamp(0.0, 0.85);
                    let brightness = opacity * transmission * illumination;
                    for (channel, value) in light.iter_mut().zip([color.0, color.1, color.2]) {
                        *channel += ((value * brightness
                            + highlight * 100.0 * opacity * transmission)
                            / 255.0) as f32;
                    }
                    transmission *= 1.0 - opacity;
                }
                field.put_pixel(x as u32, y as u32, image::Rgb(light));
            }
        }
        let field = image::imageops::resize(
            &field,
            WIDTH as u32,
            HEIGHT as u32,
            image::imageops::FilterType::CatmullRom,
        );
        for (target, value) in self.pixels.iter_mut().zip(field.as_raw()) {
            *target += value * 255.0;
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
        let size = 185.0 + self.variant * 10.0;
        let cy = 430.0 + 25.0 * (t * 0.25 + self.phase).sin();
        for y in 0..HEIGHT {
            for x in 0..WIDTH {
                let local = self.field_at(x as f64, y as f64);
                let nx = (x as f64 - 270.0 + local.dx) / size;
                let ny = (y as f64 - cy + local.dy) / (size * 1.25);
                let angle = ny.atan2(nx);
                let edge = 1.0
                    + (0.06 + self.morph * 0.09)
                        * (angle * (3.0 + self.variant) + self.phase).sin()
                        * (t * 0.37).sin()
                    + 0.035 * (angle * 7.0 + self.phase).cos() * (t * 0.29).cos();
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
                let color = self.local_color(
                    mix_light(
                        self.palette[0],
                        self.palette[1],
                        (reflected * 0.5 + 0.5) * 0.65,
                    ),
                    local,
                    z,
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
                        (0.15 + 0.45 * (v * PI).sin().powi(3)) * (0.6 + energy * 0.4),
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
                let local = self.field_at(x as f64, y as f64);
                let dx = (x as f64 - 270.0 + local.dx) / (960.0 * zoom);
                let dy = (y as f64 - 480.0 + local.dy) / (960.0 * zoom);
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
