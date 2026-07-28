use std::collections::{HashMap, VecDeque};
use std::sync::Mutex;
use tokio::time::{Duration, Instant};

/// In-memory duplicate suppression, bounded by entry count and TTL (design §5
/// "Local state"). Uses tokio::time::Instant so tests control the clock.
/// Not persistent — POC scope.
pub struct DedupCache {
    inner: Mutex<Inner>,
    capacity: usize,
    ttl: Duration,
}

struct Inner {
    expiry_by_key: HashMap<String, Instant>,
    insertion_order: VecDeque<(String, Instant)>,
}

impl DedupCache {
    pub fn new(capacity: usize, ttl: Duration) -> Self {
        assert!(capacity >= 1, "capacity must be >= 1");
        Self {
            inner: Mutex::new(Inner {
                expiry_by_key: HashMap::new(),
                insertion_order: VecDeque::new(),
            }),
            capacity,
            ttl,
        }
    }

    pub fn try_add(&self, key: &str) -> bool {
        let now = Instant::now();
        let mut g = self.inner.lock().unwrap();

        // Purge expired from the front: fixed TTL + monotonic clock means the
        // queue is expiry-ordered.
        while g.insertion_order.front().is_some_and(|(_, exp)| *exp <= now) {
            Self::dequeue_one(&mut g);
        }

        if g.expiry_by_key.get(key).is_some_and(|exp| *exp > now) {
            tracing::debug!(dedup_key = key, "dropping duplicate event (dedup key already seen within TTL)");
            return false;
        }

        while g.expiry_by_key.len() >= self.capacity && !g.insertion_order.is_empty() {
            Self::dequeue_one(&mut g);
        }

        let expires = now + self.ttl;
        g.expiry_by_key.insert(key.to_string(), expires);
        g.insertion_order.push_back((key.to_string(), expires));
        true
    }

    pub fn len(&self) -> usize {
        self.inner.lock().unwrap().expiry_by_key.len()
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    fn dequeue_one(g: &mut Inner) {
        if let Some((key, expires_at)) = g.insertion_order.pop_front() {
            // A re-added key leaves a stale queue entry behind; only remove on
            // exact expiry match.
            if g.expiry_by_key.get(&key) == Some(&expires_at) {
                g.expiry_by_key.remove(&key);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU32, Ordering};
    use std::sync::Arc;
    use tokio::time::{advance, Duration};

    #[tokio::test(start_paused = true)]
    async fn first_add_true_second_false() {
        let cache = DedupCache::new(10, Duration::from_secs(600));
        assert!(cache.try_add("k1"));
        assert!(!cache.try_add("k1"));
        assert!(cache.try_add("k2"));
    }

    #[tokio::test(start_paused = true)]
    async fn key_expires_after_ttl() {
        let cache = DedupCache::new(10, Duration::from_secs(600));
        assert!(cache.try_add("k1"));
        advance(Duration::from_secs(540)).await; // 9 min: still within TTL
        assert!(!cache.try_add("k1"));
        advance(Duration::from_secs(120)).await; // 11 min since insert
        assert!(cache.try_add("k1"));
    }

    #[tokio::test(start_paused = true)]
    async fn evicts_oldest_when_over_capacity() {
        let cache = DedupCache::new(2, Duration::from_secs(3600));
        assert!(cache.try_add("a"));
        assert!(cache.try_add("b"));
        assert!(cache.try_add("c")); // evicts "a"
        assert!(cache.try_add("a")); // "a" was forgotten
        assert!(cache.len() <= 2);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    async fn concurrent_adds_have_exactly_one_winner_per_key() {
        let cache = Arc::new(DedupCache::new(10_000, Duration::from_secs(600)));
        let wins = Arc::new(AtomicU32::new(0));
        let mut handles = Vec::new();
        for i in 0..1000 {
            let cache = cache.clone();
            let wins = wins.clone();
            handles.push(tokio::spawn(async move {
                if cache.try_add("same-key") {
                    wins.fetch_add(1, Ordering::Relaxed);
                }
                cache.try_add(&format!("key-{i}"));
            }));
        }
        for h in handles {
            h.await.unwrap();
        }
        assert_eq!(wins.load(Ordering::Relaxed), 1);
    }
}
