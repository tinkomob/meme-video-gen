use anyhow::{Result, bail, ensure};
use chrono::{DateTime, Duration, FixedOffset, NaiveDate, TimeZone, Timelike};
use rand::Rng;
use serde::{Deserialize, Serialize};
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Plan {
    pub date: String,
    pub slots: Vec<DateTime<FixedOffset>>,
    pub handled: Vec<bool>,
}
pub fn tomsk() -> FixedOffset {
    FixedOffset::east_opt(7 * 3600).unwrap()
}
impl Plan {
    pub fn validate(&self) -> Result<()> {
        let day = NaiveDate::parse_from_str(&self.date, "%Y-%m-%d")?
            .and_hms_opt(0, 0, 0)
            .unwrap();
        let day = tomsk().from_local_datetime(&day).unwrap();
        let (window, min, max) = match self.slots.len() {
            2 => (720, 600, 840),
            3 => (480, 360, 600),
            _ => bail!("invalid snippet slot count"),
        };
        ensure!(
            self.handled.len() == self.slots.len(),
            "invalid handled state"
        );
        for (i, slot) in self.slots.iter().enumerate() {
            ensure!(
                i == 0 || !self.handled[i] || self.handled[i - 1],
                "invalid handled order"
            );
            ensure!(
                *slot >= day + Duration::minutes(i as i64 * window)
                    && *slot < day + Duration::minutes((i as i64 + 1) * window)
                    && slot.second() == 0
                    && slot.nanosecond() == 0,
                "invalid slot"
            );
            if i > 0 {
                let gap = (*slot - self.slots[i - 1]).num_minutes();
                ensure!((min..=max).contains(&gap), "invalid snippet interval");
            }
        }
        Ok(())
    }
    pub fn new(
        now: DateTime<FixedOffset>,
        previous: Option<&Self>,
        rng: &mut impl Rng,
    ) -> Result<Self> {
        let now = now.with_timezone(&tomsk());
        let day = now.date_naive().and_hms_opt(0, 0, 0).unwrap();
        let day = tomsk().from_local_datetime(&day).unwrap();
        let mut last = previous
            .filter(|p| p.date == (day - Duration::days(1)).format("%Y-%m-%d").to_string())
            .and_then(|p| p.slots.last())
            .copied();
        let mut p = Self {
            date: day.format("%Y-%m-%d").to_string(),
            slots: vec![],
            handled: vec![],
        };
        for i in 0..2 {
            let (mut low, mut high) = (i * 720, (i + 1) * 720 - 1);
            if let Some(last) = last {
                let m = (last - day).num_minutes();
                low = low.max(m + 600);
                high = high.min(m + 840);
            }
            ensure!(low <= high, "no valid snippet slot");
            let slot = day + Duration::minutes(rng.random_range(low..=high));
            p.slots.push(slot);
            p.handled.push(slot < now);
            last = Some(slot);
        }
        Ok(p)
    }
    pub fn migrate(now: DateTime<FixedOffset>, old: &Self, rng: &mut impl Rng) -> Result<Self> {
        let mut p = Self::new(now, None, rng)?;
        let processed = old.handled.iter().filter(|v| **v).count();
        if processed >= 2 {
            p.handled.fill(true);
        } else if processed == 1 {
            p.slots[0] = old.slots[0];
            p.handled[0] = true;
            let day = tomsk()
                .from_local_datetime(
                    &NaiveDate::parse_from_str(&p.date, "%Y-%m-%d")?
                        .and_hms_opt(0, 0, 0)
                        .unwrap(),
                )
                .unwrap();
            let first = (p.slots[0] - day).num_minutes();
            p.slots[1] = day
                + Duration::minutes(rng.random_range(720.max(first + 600)..=1439.min(first + 840)));
            p.handled[1] = p.slots[1] < now;
        }
        p.validate()?;
        Ok(p)
    }
    pub fn claim(&mut self, now: DateTime<FixedOffset>) -> (bool, bool) {
        let (mut changed, mut due) = (false, false);
        for (i, s) in self.slots.iter().enumerate() {
            if !self.handled[i] && *s <= now {
                self.handled[i] = true;
                changed = true;
                due |= (now - *s) < Duration::minutes(1);
            }
        }
        (changed, due)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use rand::SeedableRng;
    #[test]
    fn schedules_keep_gaps_across_midnight() {
        let mut rng = rand::rngs::StdRng::seed_from_u64(42);
        let mut prev = None;
        let mut now = DateTime::parse_from_rfc3339("2026-10-25T00:00:00+07:00").unwrap();
        for _ in 0..1000 {
            let p = Plan::new(now, prev.as_ref(), &mut rng).unwrap();
            p.validate().unwrap();
            if let Some(old) = prev {
                let gap = (p.slots[0] - old.slots[1]).num_minutes();
                assert!((600..=840).contains(&gap));
            }
            prev = Some(p);
            now += Duration::days(1);
        }
    }
    #[test]
    fn claims_skip_old_slots_and_do_not_repeat() {
        let now = DateTime::parse_from_rfc3339("2026-10-25T00:00:00+07:00").unwrap();
        let mut p = Plan::new(now, None, &mut rand::rng()).unwrap();
        assert_eq!(p.claim(p.slots[0] + Duration::seconds(20)), (true, true));
        assert_eq!(p.claim(p.slots[0] + Duration::seconds(40)), (false, false));
        assert_eq!(p.claim(p.slots[1] + Duration::minutes(2)), (true, false));
    }
}
#[cfg(test)]
mod migration_tests {
    use super::*;
    use rand::SeedableRng;
    #[test]
    fn legacy_schedule_migration_preserves_processed_slots() {
        let now = DateTime::parse_from_rfc3339("2026-10-25T10:00:00+07:00").unwrap();
        let midnight = now - Duration::hours(10);
        for processed in 0..=3 {
            let old = Plan {
                date: "2026-10-25".into(),
                slots: [4, 12, 20].map(|h| midnight + Duration::hours(h)).to_vec(),
                handled: (0..3).map(|i| i < processed).collect(),
            };
            old.validate().unwrap();
            let new = Plan::migrate(now, &old, &mut rand::rngs::StdRng::seed_from_u64(42)).unwrap();
            assert_eq!(new.slots.len(), 2);
            new.validate().unwrap();
            if processed >= 2 {
                assert!(new.handled.iter().all(|h| *h));
            }
            if processed == 1 {
                assert_eq!(new.slots[0], old.slots[0]);
                assert!(new.handled[0]);
            }
        }
    }
    #[test]
    fn rejects_corrupt_handled_order_and_slot_times() {
        let now = DateTime::parse_from_rfc3339("2026-10-25T00:00:00+07:00").unwrap();
        let mut p = Plan::new(now, None, &mut rand::rng()).unwrap();
        p.handled = vec![false, true];
        assert!(p.validate().is_err());
        p.handled.fill(false);
        p.slots[0] += Duration::seconds(1);
        assert!(p.validate().is_err());
    }
}
