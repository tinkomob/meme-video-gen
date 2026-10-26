use super::{AudioFrame, BANDS, FPS};

#[derive(Clone, Copy, Default)]
pub(super) struct Rhythm {
    pub(super) bpm: f64,
    pub(super) confidence: f64,
    /// Continuous beat count, including the fraction between adjacent beats.
    pub(super) phase: f64,
}

impl Rhythm {
    pub(super) fn pulse(self, delay: f64) -> f64 {
        let distance = (self.phase - delay + 0.5).rem_euclid(1.0) - 0.5;
        self.confidence * (-distance.powi(2) / 0.025).exp()
    }
}

fn sample(values: &[f64], at: f64) -> f64 {
    let i = at.max(0.0) as usize;
    let a = values.get(i).copied().unwrap_or(0.0);
    a + (values.get(i + 1).copied().unwrap_or(a) - a) * at.fract()
}

pub(super) fn track(frames: &[AudioFrame]) -> Vec<Rhythm> {
    let mut result = vec![Rhythm::default(); frames.len()];
    if frames.len() < FPS * 3 {
        return result;
    }
    let mut onset = vec![0.0; frames.len()];
    for (i, pair) in frames.windows(2).enumerate() {
        let flux = pair[1]
            .bands
            .iter()
            .zip(pair[0].bands)
            .map(|(now, before)| (now - before).max(0.0))
            .sum::<f64>()
            / BANDS as f64;
        onset[i + 1] = flux + (pair[1].level - pair[0].level).max(0.0) * 0.25;
    }
    let peak = onset.iter().copied().fold(0.0_f64, f64::max);
    if peak < 0.005 {
        return result;
    }
    for value in &mut onset {
        *value = ((*value / peak - 0.08).max(0.0) / 0.92).sqrt();
    }
    // shortcut: one tempo hypothesis per snippet; use windowed tempo tracking for large tempo changes.
    let mut period = 0.0;
    let mut correlation = 0.0;
    for step in 0..=450 {
        let lag = FPS as f64 * 60.0 / 190.0 + step as f64 * 0.06;
        if lag > FPS as f64 * 60.0 / 55.0 {
            break;
        }
        let (mut dot, mut left, mut right) = (0.0, 0.0, 0.0);
        for i in lag.ceil() as usize..onset.len() {
            let a = onset[i];
            let b = sample(&onset, i as f64 - lag);
            dot += a * b;
            left += a * a;
            right += b * b;
        }
        let score = dot / (left * right).sqrt().max(1e-9);
        if score > correlation + 0.001 {
            correlation = score;
            period = lag;
        }
    }
    if correlation < 0.35 || period == 0.0 {
        return result;
    }
    let phase = (0..(period * 4.0).ceil() as usize)
        .map(|i| i as f64 / 4.0)
        .max_by(|a, b| {
            let score = |offset: f64| {
                (0..(frames.len() as f64 / period) as usize)
                    .map(|beat| sample(&onset, offset + beat as f64 * period))
                    .sum::<f64>()
            };
            score(*a).total_cmp(&score(*b))
        })
        .unwrap_or(0.0);
    let mut beats = Vec::new();
    let mut expected = phase;
    while expected < frames.len() as f64 {
        let radius = (period * 0.18) as usize;
        let center = expected.round() as usize;
        let found = (center.saturating_sub(radius)..=(center + radius).min(onset.len() - 1))
            .max_by(|a, b| {
                let score = |i: usize| onset[i] * (1.0 - (i as f64 - expected).abs() / period);
                score(*a).total_cmp(&score(*b))
            })
            .unwrap_or(center.min(onset.len() - 1));
        let at = if onset[found] > 0.15 {
            found as f64
        } else {
            expected
        };
        beats.push(at);
        expected += period;
    }
    let peak_energy = frames
        .iter()
        .map(|a| a.level.max(a.bands.iter().sum::<f64>() / BANDS as f64))
        .fold(0.0_f64, f64::max);
    let mut confidence = 0.0;
    let mut activity = 0.0;
    for (i, frame) in frames.iter().enumerate() {
        let before = beats
            .partition_point(|beat| *beat <= i as f64)
            .saturating_sub(1);
        let start = beats[before];
        let end = beats.get(before + 1).copied().unwrap_or(start + period);
        let beat_phase = before as f64 + (i as f64 - start) / (end - start);
        let lo = i.saturating_sub((period * 2.0) as usize);
        let hi = (i + (period * 2.0) as usize + 1).min(onset.len());
        let total = onset[lo..hi].iter().sum::<f64>();
        let aligned = onset[lo..hi]
            .iter()
            .enumerate()
            .map(|(j, value)| {
                let near = beats
                    .iter()
                    .any(|beat| (*beat - (lo + j) as f64).abs() <= period * 0.15);
                if near { *value } else { 0.0 }
            })
            .sum::<f64>();
        let hits = beats
            .iter()
            .filter(|beat| {
                **beat >= lo as f64 && **beat < hi as f64 && sample(&onset, **beat) > 0.15
            })
            .count();
        let regularity = ((aligned / total.max(1e-9) - 0.45) / 0.4).clamp(0.0, 1.0);
        let target = ((correlation - 0.35) / 0.4).clamp(0.0, 1.0)
            * regularity
            * ((hits as f64 - 1.0) / 2.0).clamp(0.0, 1.0);
        let energy = frame
            .level
            .max(frame.bands.iter().sum::<f64>() / BANDS as f64);
        activity += (energy - activity) * 0.15;
        let target = target * (activity / (peak_energy * 0.04).max(1e-9)).clamp(0.0, 1.0);
        confidence += (target - confidence) * 0.08;
        result[i] = Rhythm {
            bpm: FPS as f64 * 60.0 / period,
            confidence,
            phase: beat_phase,
        };
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    fn notes(period: f64, offset: f64, band: usize) -> Vec<AudioFrame> {
        (0..600)
            .map(|i| {
                let phase = (i as f64 - offset).rem_euclid(period);
                let note = (-phase * 0.5).exp();
                let mut frame = AudioFrame::default();
                frame.bands[band..band + 6].fill(note);
                frame.level = note * 0.4;
                frame
            })
            .collect()
    }

    #[test]
    fn finds_tempo_and_offset_without_drums() {
        for (bpm, offset) in [(90.0, 7.0), (120.0, 5.0), (137.0, 8.0), (180.0, 3.0)] {
            let period = FPS as f64 * 60.0 / bpm;
            let rhythm = track(&notes(period, offset, 26));
            assert!(
                (rhythm[300].bpm - bpm).abs() < 4.0,
                "{bpm}: {}",
                rhythm[300].bpm
            );
            assert!(rhythm[300].confidence > 0.5, "weak confidence at {bpm}");
            let beat = (offset + period * 12.0).ceil() as usize;
            assert!(
                rhythm[beat].pulse(0.0) > 0.5,
                "wrong beat phase at {bpm}: bpm {}, phase {}, confidence {}",
                rhythm[beat].bpm,
                rhythm[beat].phase,
                rhythm[beat].confidence
            );
            assert!(rhythm[beat].pulse(0.0) > rhythm[beat + (period / 2.0) as usize].pulse(0.0));
        }
    }

    #[test]
    fn silence_drones_short_clips_and_free_notes_do_not_force_a_metronome() {
        for count in [0, 1, 60, 600] {
            for level in [0.0, 0.5] {
                let frames = vec![
                    AudioFrame {
                        level,
                        bands: [level; BANDS],
                        ..AudioFrame::default()
                    };
                    count
                ];
                assert!(track(&frames).iter().all(|r| r.confidence == 0.0));
            }
        }
        let mut free = vec![AudioFrame::default(); 600];
        for i in [17, 48, 103, 141, 222, 249, 337, 405, 443, 526] {
            free[i].bands[20..30].fill(0.8);
        }
        assert!(track(&free).iter().all(|r| r.confidence < 0.4));
    }

    #[test]
    fn confidence_fades_in_silence_and_beat_phase_is_continuous() {
        let mut frames = notes(15.0, 5.0, 0);
        frames[330..].fill(AudioFrame::default());
        let rhythm = track(&frames);
        assert!(rhythm[240].confidence > 0.5);
        assert!(rhythm[420].confidence < 0.01);
        assert!(
            rhythm
                .windows(2)
                .all(|r| r[1].phase > r[0].phase && r[1].phase - r[0].phase < 0.2)
        );
        assert!(
            rhythm
                .windows(2)
                .all(|r| (r[1].confidence - r[0].confidence).abs() < 0.1)
        );
    }

    #[test]
    fn decoded_pitched_notes_produce_a_pulse_without_percussion() {
        let samples: Vec<_> = (0..320_000)
            .map(|i| {
                let t = i as f64 / 16000.0;
                let age = (t - 0.17).rem_euclid(0.5);
                (t * 440.0 * std::f64::consts::TAU).sin() * (-age * 12.0).exp()
            })
            .collect();
        let rhythm = track(&super::super::analyze(&samples));
        assert!((rhythm[300].bpm - 120.0).abs() < 4.0);
        assert!(rhythm[300].confidence > 0.5);
        assert!(rhythm[305].pulse(0.0) > rhythm[312].pulse(0.0));
    }
}
