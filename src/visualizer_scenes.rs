use super::*;
impl Painter {
    pub(super) fn silver(&mut self, t: f64, a: AudioFrame) {
        let white = self.palette[0];
        for i_index in 0..110 {
            let i = i_index as f64;
            let x = i * 5.0 - 4.0;
            let mut wave = 660.0 + 55.0 * sin(x * 0.012 + t * 0.35 + self.phase);
            let mut height = 35.0
                + 210.0 * band_at(a, i / 110.0)
                + 60.0 * a.level
                + 140.0 * pow(abs(x - 270.0) / 270.0, 1.4);
            height *= 0.75 + 0.45 * self.morph;
            if self.variant == 1.0 {
                wave = 720.0 + 35.0 * cos(x * 0.018 - t * 0.3 + self.phase);
                height *= self.spread * (1.0 + 0.22 * sin(x * 0.023 + t * 0.4));
            }
            if self.variant == 2.0 {
                wave = 610.0 + 70.0 * sin(x * 0.009 + t * 0.25 + self.phase);
                height *= 0.6 + 0.35 * pow(sin(x * 0.015 + self.phase), 2.0);
            }
            self.line(x, wave - height, x, wave, white, 0.65);
            self.line(x, wave + 8.0, x, wave + height * 0.3 + 8.0, white, 0.08);
            self.point(x, wave - height, white, 1.7);
            if self.variant == 1.0 {
                self.line(
                    x,
                    wave + 10.0,
                    x,
                    wave + height * 0.55 + 10.0,
                    self.palette[1],
                    0.2,
                );
            }
            if self.variant == 2.0 {
                for j_index in 0..5 {
                    let j = j_index as f64;
                    let y = r#mod((i * 19.0 + j * 97.0) + t * (20.0 + 50.0 * a.level), 340.0);
                    self.line(
                        x,
                        y + 180.0,
                        x,
                        y + 184.0 + 10.0 * band_at(a, i / 110.0),
                        self.palette[1],
                        0.35,
                    );
                }
            }
        }
        for layer_index in 0..7 {
            let layer = layer_index as f64;
            let (mut last_x, mut last_y) = (0.0, 0.0);
            for i_index in 0..=400 {
                let i = i_index as f64;
                let x = i * 540.0 / 400.0;
                let y = 720.0
                    + layer * 10.0 * self.spread
                    + 38.0 * sin(x * 0.014 + t * 0.35 + self.phase)
                    + 20.0 * sin(x * 0.028 - t * 0.5)
                    + 26.0 * band_at(a, x / 540.0);
                if i > 0.0 {
                    self.line(
                        last_x,
                        last_y,
                        x,
                        y,
                        mix_light(white, self.palette[1], layer / 7.0),
                        0.17,
                    );
                }
                (last_x, last_y) = (x, y);
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let x = s.u * 540.0;
            let base = 700.0 + 45.0 * sin(x * 0.014 + t * 0.35 + self.phase);
            let y = base + (s.v - 0.5) * 280.0 + 8.0 * sin(t * (0.2 + s.depth) + s.phase);
            let brightness = (0.15 + s.depth * 0.8) * (0.35 + a.level * 0.65);
            self.point(x, y, white, brightness);
            if s.depth > 0.97 {
                self.line(x - 1.5, y, x + 1.5, y, white, brightness);
                self.line(x, y - 2.0, x, y + 2.0, white, brightness);
            }
            self.point(x, 830.0 + (830.0 - y) * 0.35, white, brightness * 0.16);
        }
    }
    pub(super) fn violet(&mut self, t: f64, a: AudioFrame) {
        let (violet, blue) = (self.palette[0], self.palette[1]);
        for layer_index in 0..12 {
            let layer = layer_index as f64;
            let (mut last_x, mut last_y) = (0.0, 0.0);
            for i_index in 0..=720 {
                let i = i_index as f64;
                let angle = i / 720.0 * 2.0 * PI;
                let energy = band_at(a, i / 720.0);
                let mut r = 1.0
                    + 0.035 * sin(angle * 7.0 + t * 0.8 + self.phase + layer * 0.4)
                    + 0.02 * sin(angle * 17.0 - t)
                    + 0.012 * sin(angle * 37.0 + t * 1.1 + layer * 0.85)
                    + 0.006 * cos(angle * 61.0 - t * 0.9 + layer)
                    + energy * 0.09;
                r += 0.055 * self.morph * sin(angle * 4.0 + self.phase + layer * 0.15);
                if self.variant == 1.0 {
                    r += 0.09 * sin(angle * 3.0 + t * 0.3 + self.phase + floor(layer / 6.0) * PI);
                }
                if self.variant == 2.0 {
                    r += (0.065 + 0.035 * a.level) * sin(angle * 6.0 - t * 0.35 + self.phase);
                }
                let x = 270.0 + (250.0 + layer * 1.5) * r * cos(angle) * self.spread;
                let y = 470.0 + (340.0 + layer * 1.5) * r * sin(angle);
                let color = mix_light(violet, blue, 0.5 + 0.5 * sin(angle * 2.0 + t * 0.3));
                if i > 0.0 {
                    self.line(last_x, last_y, x, y, color, 0.13 + 0.19 * energy);
                }
                (last_x, last_y) = (x, y);
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let angle = s.u * 2.0 * PI + t * (0.01 + s.depth * 0.025);
            let energy = band_at(a, s.u);
            let mut r = 1.0
                + (s.v - 0.5) * 0.28
                + energy * 0.07
                + 0.025 * sin(angle * 7.0 + t + self.phase);
            if self.variant == 1.0 {
                r += 0.09 * sin(angle * 3.0 + t * 0.3 + self.phase);
            }
            if self.variant == 2.0 {
                r += (0.065 + 0.035 * a.level) * sin(angle * 6.0 - t * 0.35 + self.phase);
            }
            let (x, y) = (
                270.0 + 267.0 * r * cos(angle) * self.spread,
                470.0 + 360.0 * r * sin(angle),
            );
            let c = mix_light(violet, blue, 0.5 + 0.5 * sin(angle * 2.0 + t * 0.3));
            self.point(x, y, c, (0.15 + s.depth * 0.8) * (0.4 + 0.6 * a.level));
            if s.depth > 0.98 {
                self.line(x - 1.0, y, x + 1.0, y, c, 1.0);
                self.line(x, y - 1.0, x, y + 1.0, c, 1.0);
            }
        }
    }
    pub(super) fn amber(&mut self, t: f64, a: AudioFrame) {
        let (gold, coral, teal) = (self.palette[0], self.palette[1], self.palette[2]);
        for stream_index in 0..2 {
            let stream = stream_index as f64;
            let phase = self.phase + stream * 1.8;
            for strand_index in 0..15 {
                let strand = strand_index as f64;
                let (mut last_x, mut last_y) = (0.0, 0.0);
                for i_index in 0..=450 {
                    let i = i_index as f64;
                    let x = -40.0 + i * 620.0 / 450.0;
                    let energy = band_at(a, i / 450.0);
                    let spread = (strand - 7.0)
                        * (2.0 + 3.0 * sin(x * 0.008 + t * 0.3 + phase))
                        * (0.65 + 0.7 * self.morph);
                    let mut y = 100.0
                        + stream * 600.0
                        + x * 0.38
                        + 45.0 * sin(x * 0.011 + t * 0.4 + phase)
                        + spread
                        + (14.0 + energy * 18.0) * sin(x * 0.018 - t * 0.5 + strand * 0.27)
                        + 8.0 * sin(x * 0.033 + t * 0.3 + strand * 0.45);
                    if self.variant == 1.0 {
                        y = 190.0
                            + stream * 560.0
                            + 95.0 * sin(x * 0.01 + t * 0.4 + phase)
                            + spread * 1.7
                            + (10.0 + 25.0 * energy) * sin(x * 0.018 - t * 0.4 + strand * 0.32);
                    }
                    if self.variant == 2.0 {
                        y = 150.0 + stream * 570.0 - x * 0.13
                            + 70.0 * sin(x * 0.007 + t * 0.25 + phase)
                            + spread * 2.1
                            + 20.0 * energy * sin(x * 0.02 + strand * 0.3);
                    }
                    let mut c = mix_light(gold, coral, 0.35 + 0.3 * sin(x * 0.006 + stream));
                    if strand < 2.0 {
                        c = teal;
                        y += 80.0;
                    }
                    if i > 0.0 {
                        self.line(last_x, last_y, x, y, c, 0.22 + 0.2 * energy);
                    }
                    (last_x, last_y) = (x, y);
                }
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let x = r#mod(s.u * 620.0 + t * (2.0 + s.depth * 6.0), 620.0) - 40.0;
            let stream = floor(s.v * 2.0);
            let mut y = 100.0
                + stream * 600.0
                + x * 0.38
                + 45.0 * sin(x * 0.011 + t * 0.4 + self.phase + stream * 1.8)
                + (r#mod(s.v * 2.0, 1.0) - 0.5) * 170.0;
            if self.variant == 1.0 {
                y = 190.0
                    + stream * 560.0
                    + 95.0 * sin(x * 0.01 + t * 0.4 + self.phase + stream * 1.8)
                    + (r#mod(s.v * 2.0, 1.0) - 0.5) * 210.0;
            }
            if self.variant == 2.0 {
                y = 150.0 + stream * 570.0 - x * 0.13
                    + 70.0 * sin(x * 0.007 + t * 0.25 + self.phase + stream * 1.8)
                    + (r#mod(s.v * 2.0, 1.0) - 0.5) * 210.0;
            }
            self.point(
                x,
                y,
                mix_light(gold, coral, s.depth),
                (0.1 + 0.8 * s.depth) * (0.4 + 0.6 * a.level),
            );
        }
    }
    pub(super) fn aurora(&mut self, t: f64, a: AudioFrame) {
        for curtain_index in 0..2 {
            let curtain = curtain_index as f64;
            let phase = self.phase + curtain * 1.7;
            for strand_index in 0..170 {
                let strand = strand_index as f64;
                let x = strand * 3.5 - 25.0;
                let energy = band_at(a, strand / 170.0);
                let mut base = 190.0 + curtain * 550.0 + 65.0 * sin(x * 0.009 + t * 0.25 + phase);
                if self.variant == 1.0 {
                    base += 45.0 * sin(x * 0.021 - t * 0.3 + phase);
                }
                if self.variant == 2.0 {
                    base += x * 0.22 - 70.0;
                }
                let height = (40.0 + 100.0 * energy + 25.0 * a.level)
                    * self.spread
                    * (0.7 + 0.6 * self.morph);
                let color = mix_light(
                    self.palette[0],
                    self.palette[1],
                    0.5 + 0.5 * sin(x * 0.007 + phase),
                );
                let (mut last_x, mut last_y) = (x, base);
                for step_index in 1..16 + 1 {
                    let step = step_index as f64;
                    let f = step / 16.0;
                    let xx = x + 14.0 * sin(f * 3.0 + t * 0.25 + phase) * f;
                    let yy = base - height * f;
                    self.line(
                        last_x,
                        last_y,
                        xx,
                        yy,
                        mix_light(color, self.palette[2], f * 0.6),
                        (0.12 + 0.2 * energy) * (1.0 - f * 0.65),
                    );
                    (last_x, last_y) = (xx, yy);
                }
                self.point(x, base, color, 0.7 + energy);
            }
            for layer_index in 0..5 {
                let layer = layer_index as f64;
                let (mut last_x, mut last_y) = (0.0, 0.0);
                for step_index in 0..=360 {
                    let step = step_index as f64;
                    let x = step * 540.0 / 360.0;
                    let mut y = 190.0
                        + curtain * 550.0
                        + 65.0 * sin(x * 0.009 + t * 0.25 + phase)
                        + layer * 4.0;
                    if self.variant == 1.0 {
                        y += 45.0 * sin(x * 0.021 - t * 0.3 + phase);
                    }
                    if self.variant == 2.0 {
                        y += x * 0.22 - 70.0;
                    }
                    y += 12.0 * band_at(a, x / 540.0) * sin(x * 0.025 - t * 0.4 + layer);
                    if step > 0.0 {
                        self.line(
                            last_x,
                            last_y,
                            x,
                            y,
                            self.palette[(layer % 3.0) as usize],
                            0.2,
                        );
                    }
                    (last_x, last_y) = (x, y);
                }
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let (x, y) = (
                s.u * 540.0,
                r#mod(s.v * 960.0 - t * (1.0 + 3.0 * s.depth) + 960.0, 960.0),
            );
            self.point(
                x,
                y,
                mix_light(self.palette[1], self.palette[2], s.depth),
                (0.05 + 0.4 * s.depth) * (0.4 + 0.6 * a.level),
            );
        }
    }
    pub(super) fn prism(&mut self, t: f64, a: AudioFrame) {
        let sides = [6.0, 3.0, 4.0][self.variant as usize];
        for layer_index in 0..9 {
            let layer = layer_index as f64;
            let mut rotation = self.phase + t * 0.07 + layer * (0.025 + 0.065 * self.morph);
            if self.variant == 1.0 {
                rotation += floor(layer / 3.0) * PI / 3.0;
            }
            let radius = (265.0 + layer * 7.0) * self.spread;
            let mut first_x: f64 = 0.0;
            let mut first_y: f64 = 0.0;
            let mut last_x: f64 = 0.0;
            let mut last_y: f64 = 0.0;
            for vertex_index in 0..sides as i32 + 1 {
                let vertex = vertex_index as f64;
                let angle = rotation + (vertex % sides) * 2.0 * PI / sides;
                let energy = band_at(a, (vertex % sides) / sides + layer * 0.013);
                let r = radius * (1.0 + 0.1 * energy);
                let x = 270.0 + r * cos(angle);
                let y = 470.0 + r * 1.22 * sin(angle);
                let color = mix_light(self.palette[0], self.palette[1], layer / 9.0);
                if vertex == 0.0 {
                    (first_x, first_y) = (x, y);
                } else {
                    self.line(last_x, last_y, x, y, color, 0.5 + 0.35 * energy);
                }
                self.point(x, y, self.palette[2], 1.2 + energy);
                (last_x, last_y) = (x, y);
            }
            if layer % 3.0 == 0.0 {
                let angle = rotation + 2.0 * PI / sides + 0.045;
                let x = 270.0 + (radius + 20.0) * cos(angle);
                let y = 470.0 + (radius + 20.0) * 1.22 * sin(angle);
                self.line(first_x, first_y, x, y, self.palette[2], 0.3 + 0.2 * a.level);
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let i = particle_index as f64;
            if i < 22.0 {
                let angle = s.u * 2.0 * PI + t * 0.035;
                let radius = 290.0 + s.v * 100.0 + 25.0 * band_at(a, s.u);
                let (x, y) = (
                    270.0 + radius * cos(angle),
                    470.0 + radius * 1.2 * sin(angle),
                );
                let size = 5.0 + 12.0 * s.depth + 8.0 * a.level;
                for edge_index in 0..3 {
                    let edge = edge_index as f64;
                    let (a0, a1) = (
                        s.phase + t * 0.1 + (edge) * 2.0 * PI / 3.0,
                        s.phase + t * 0.1 + (edge + 1.0) * 2.0 * PI / 3.0,
                    );
                    self.line(
                        x + size * cos(a0),
                        y + size * sin(a0),
                        x + size * cos(a1),
                        y + size * sin(a1),
                        self.palette[(i % 3.0) as usize],
                        0.65,
                    );
                }
            } else {
                self.point(
                    s.u * 540.0,
                    r#mod(s.v * 960.0 + t * (s.depth - 0.5) * 3.0 + 960.0, 960.0),
                    self.palette[(i % 3.0) as usize],
                    (0.06 + 0.35 * s.depth) * (0.4 + 0.6 * a.level),
                );
            }
        }
    }
    pub(super) fn galaxy(&mut self, t: f64, a: AudioFrame) {
        let arms = 2.0 + self.variant;
        for arm_index in 0..arms as i32 {
            let arm = arm_index as f64;
            for filament_index in 0..9 {
                let filament = filament_index as f64;
                let (mut last_x, mut last_y) = (0.0, 0.0);
                for step_index in 0..=420 {
                    let step = step_index as f64;
                    let f = step / 420.0;
                    let energy = band_at(a, f);
                    let r = 75.0 + f * 430.0 + energy * 12.0;
                    let angle = self.phase
                        + arm * 2.0 * PI / arms
                        + f * (3.5 + 2.2 * self.morph)
                        + t * 0.035
                        + filament * 0.025
                        + energy * 0.015;
                    let (x, y) = (
                        270.0 + r * cos(angle) * self.spread,
                        470.0 + r * 1.25 * sin(angle),
                    );
                    let color = mix_light(self.palette[0], self.palette[1], f);
                    if step > 0.0 {
                        self.line(
                            last_x,
                            last_y,
                            x,
                            y,
                            color,
                            (0.2 + 0.3 * energy) * (1.0 - f * 0.5),
                        );
                    }
                    (last_x, last_y) = (x, y);
                }
            }
        }
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let i = particle_index as f64;
            let energy = band_at(a, s.u);
            if i % 3.0 == 0.0 {
                self.point(
                    s.u * 540.0,
                    r#mod(s.v * 960.0 + t * (s.depth - 0.5) * 2.0 + 960.0, 960.0),
                    self.palette[2],
                    (0.1 + s.depth * 0.6) * (0.4 + 0.6 * a.level),
                );
                continue;
            }
            let f = s.u;
            let r = 75.0 + f * 430.0 + (s.v - 0.5) * 65.0 + energy * 12.0;
            let angle = self.phase
                + (i % arms) * 2.0 * PI / arms
                + f * (3.5 + 2.2 * self.morph)
                + t * (0.025 + s.depth * 0.02)
                + (s.v - 0.5) * 0.22
                + energy * 0.015;
            let (x, y) = (
                270.0 + r * cos(angle) * self.spread,
                470.0 + r * 1.25 * sin(angle),
            );
            let color = mix_light(self.palette[0], self.palette[1], f);
            let brightness = (0.15 + s.depth * 1.5) * (0.4 + 0.6 * energy);
            self.point(x, y, color, brightness);
            if s.depth > 0.97 {
                self.line(x - 1.5, y, x + 1.5, y, self.palette[2], brightness);
                self.line(x, y - 1.5, x, y + 1.5, self.palette[2], brightness);
            }
            if self.variant == 2.0 && s.depth > 0.92 {
                self.line(
                    x,
                    y,
                    x + 5.0 * sin(angle),
                    y - 6.0 * cos(angle),
                    color,
                    brightness * 0.25,
                );
            }
        }
    }
    pub(super) fn tunnel(&mut self, t: f64, a: AudioFrame) {
        for ring_index in 0..18 {
            let ring = ring_index as f64;
            let depth = r#mod(ring / 18.0 + t * 0.045, 1.0);
            let radius = 25.0 + depth * 560.0 * self.spread;
            let mut last_x: f64 = 0.0;
            let mut last_y: f64 = 0.0;
            for step_index in 0..=240 {
                let step = step_index as f64;
                let f = step / 240.0;
                let angle = f * 2.0 * PI + self.phase + t * 0.08;
                let energy = band_at(a, f + ring * 0.017);
                let mut r = radius * (1.0 + 0.07 * energy);
                r *= 1.0 + 0.035 * self.morph * sin(angle * 3.0 + depth * 5.0 + t * 0.2);
                if self.variant == 1.0 {
                    r *= pow(
                        pow(abs(cos(angle)), 6.0) + pow(abs(sin(angle)), 6.0),
                        -1.0 / 6.0,
                    );
                }
                if self.variant == 2.0 {
                    r *= 1.0 + 0.14 * cos(angle * 5.0 + t * 0.3 + depth * 4.0);
                }
                let x = 270.0 + r * cos(angle) + 35.0 * depth * sin(t * 0.2 + self.phase);
                let y = 450.0 + r * 1.35 * sin(angle);
                if step > 0.0 {
                    self.line(
                        last_x,
                        last_y,
                        x,
                        y,
                        mix_light(self.palette[0], self.palette[1], depth),
                        (0.12 + 0.5 * depth) * (0.5 + energy),
                    );
                }
                (last_x, last_y) = (x, y);
            }
        }
    }
    pub(super) fn orbit(&mut self, t: f64, a: AudioFrame) {
        for ring_index in 0..11 {
            let ring = ring_index as f64;
            let tilt = ring * PI / 11.0 + self.phase + t * 0.09;
            let mut last_x: f64 = 0.0;
            let mut last_y: f64 = 0.0;
            for step_index in 0..=300 {
                let step = step_index as f64;
                let f = step / 300.0;
                let angle = f * 2.0 * PI + t * 0.12;
                let energy = band_at(a, f + ring * 0.021);
                let radius = (220.0 + 25.0 * energy + 12.0 * a.level) * self.spread;
                let (mut x, mut y) = (
                    radius * cos(angle),
                    radius * (0.16 + 0.28 * self.morph) * sin(angle),
                );
                if self.variant == 1.0 {
                    x *= 0.65 + ring * 0.065;
                    y += 40.0 * sin(angle * 3.0 + t * 0.4);
                }
                if self.variant == 2.0 {
                    y *= 2.2;
                    x += 30.0 * sin(angle * 5.0 + tilt);
                }
                (x, y) = (
                    270.0 + x * cos(tilt) - y * sin(tilt),
                    440.0 + (x * sin(tilt) + y * cos(tilt)) * 1.35,
                );
                if step > 0.0 {
                    self.line(
                        last_x,
                        last_y,
                        x,
                        y,
                        self.palette[(ring % 3.0) as usize],
                        0.22 + 0.4 * energy,
                    );
                }
                (last_x, last_y) = (x, y);
            }
        }
    }
    pub(super) fn wave(&mut self, t: f64, a: AudioFrame) {
        for row_index in 0..30 {
            let row = row_index as f64;
            let depth = row / 29.0;
            let mut last_x: f64 = 0.0;
            let mut last_y: f64 = 0.0;
            for step_index in 0..=160 {
                let step = step_index as f64;
                let f = step / 160.0;
                let energy = band_at(a, f + depth * 0.15);
                let x = 270.0 + (f - 0.5) * (140.0 + depth * 620.0) * self.spread;
                let mut y = 290.0
                    + depth * 570.0
                    + (15.0 + 55.0 * depth + 65.0 * energy)
                        * (0.6 + 0.8 * self.morph)
                        * sin(f * 9.0 - depth * (3.0 + 4.0 * self.morph) + t * 0.7 + self.phase);
                if self.variant == 1.0 {
                    y += (35.0 + 70.0 * energy)
                        * (0.5 + self.morph)
                        * cos(f * 15.0 + depth * 8.0 - t * 0.5);
                }
                if self.variant == 2.0 {
                    y = 180.0
                        + depth * 620.0
                        + (30.0 + 100.0 * energy)
                            * (0.6 + 0.8 * self.morph)
                            * sin(f * 6.0 + t * 0.5 + self.phase)
                            * cos(depth * (4.0 + 5.0 * self.morph) - t * 0.3);
                }
                let color = mix_light(self.palette[0], self.palette[1], depth);
                if step > 0.0 {
                    self.line(
                        last_x,
                        last_y,
                        x,
                        y,
                        color,
                        0.15 + 0.35 * depth + 0.25 * energy,
                    );
                }
                if step % 8.0 == 0.0 {
                    self.point(x, y, self.palette[2], 0.4 + energy);
                }
                (last_x, last_y) = (x, y);
            }
        }
    }
    pub(super) fn rain(&mut self, t: f64, a: AudioFrame) {
        for particle_index in 0..self.particles.len() {
            let s = self.particles[particle_index];
            let i = particle_index as f64;
            let energy = band_at(a, s.u);
            let speed = 25.0 + s.depth * 90.0;
            let mut x = s.u * 620.0 - 40.0 + 18.0 * sin(t * 0.3 + s.phase) * self.spread;
            let mut y = r#mod(s.v * 1100.0 + t * speed, 1100.0) - 70.0;
            let (mut dx, mut dy) = (
                0.0,
                (5.0 + 18.0 * s.depth + 20.0 * energy) * (0.6 + 0.8 * self.morph),
            );
            if self.variant == 1.0 {
                x += (y - 480.0) * 0.32 + 15.0 * sin(self.phase);
                dx = dy * 0.32;
            }
            if self.variant == 2.0 {
                y = 960.0 - y;
                x += 30.0 * sin(y * 0.014 + t * 0.4 + self.phase);
                (dx, dy) = (-dy * 0.25 * cos(y * 0.014 + t * 0.4 + self.phase), -dy);
            }
            let color = mix_light(self.palette[0], self.palette[1], s.depth);
            let brightness = (0.1 + s.depth * 0.6) * (0.35 + 0.65 * energy + 0.3 * a.level);
            self.line(x, y, x - dx, y - dy, color, brightness * 0.45);
            self.point(x, y, self.palette[2], brightness * 1.5);
            if i % 9.0 == 0.0 {
                self.line(x - 2.0, y, x + 2.0, y, color, brightness);
            }
        }
    }
}
