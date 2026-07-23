namespace NotificationAgent.Core.Dedup;

/// <summary>In-memory duplicate suppression, bounded by entry count and TTL (design §5
/// "Local state": bounded deduplication state). Not persistent — POC scope.</summary>
public sealed class DeduplicationCache
{
    private readonly object _gate = new();
    private readonly Dictionary<string, long> _expiryByKey = new();
    private readonly Queue<(string Key, long ExpiresAtTicks)> _insertionOrder = new();
    private readonly int _capacity;
    private readonly TimeSpan _ttl;
    private readonly TimeProvider _time;

    public DeduplicationCache(int capacity, TimeSpan ttl, TimeProvider? timeProvider = null)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(capacity, 1);
        _capacity = capacity;
        _ttl = ttl;
        _time = timeProvider ?? TimeProvider.System;
    }

    public bool TryAdd(string key)
    {
        var now = _time.GetUtcNow().UtcTicks;
        lock (_gate)
        {
            PurgeExpired(now);
            if (_expiryByKey.TryGetValue(key, out var existing) && existing > now)
            {
                return false;
            }

            while (_expiryByKey.Count >= _capacity && _insertionOrder.Count > 0)
            {
                DequeueOne();
            }

            var expires = now + _ttl.Ticks;
            _expiryByKey[key] = expires;
            _insertionOrder.Enqueue((key, expires));
            return true;
        }
    }

    public int Count
    {
        get
        {
            lock (_gate)
            {
                return _expiryByKey.Count;
            }
        }
    }

    private void PurgeExpired(long now)
    {
        while (_insertionOrder.Count > 0 && _insertionOrder.Peek().ExpiresAtTicks <= now)
        {
            DequeueOne();
        }
    }

    private void DequeueOne()
    {
        var (key, expiresAt) = _insertionOrder.Dequeue();

        // A re-added key leaves a stale queue entry behind; only remove on exact match.
        if (_expiryByKey.TryGetValue(key, out var current) && current == expiresAt)
        {
            _expiryByKey.Remove(key);
        }
    }
}
