using Microsoft.Extensions.Time.Testing;
using NotificationAgent.Core.Dedup;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class DeduplicationCacheTests
{
    [Fact]
    public void First_add_true_second_false()
    {
        var cache = new DeduplicationCache(capacity: 10, ttl: TimeSpan.FromMinutes(10));
        Assert.True(cache.TryAdd("k1"));
        Assert.False(cache.TryAdd("k1"));
        Assert.True(cache.TryAdd("k2"));
    }

    [Fact]
    public void Key_expires_after_ttl()
    {
        var time = new FakeTimeProvider();
        var cache = new DeduplicationCache(10, TimeSpan.FromMinutes(10), time);

        Assert.True(cache.TryAdd("k1"));
        time.Advance(TimeSpan.FromMinutes(9));
        Assert.False(cache.TryAdd("k1"));      // still within TTL
        time.Advance(TimeSpan.FromMinutes(2)); // now 11 min since insert
        Assert.True(cache.TryAdd("k1"));
    }

    [Fact]
    public void Evicts_oldest_when_over_capacity()
    {
        var cache = new DeduplicationCache(capacity: 2, ttl: TimeSpan.FromHours(1));
        Assert.True(cache.TryAdd("a"));
        Assert.True(cache.TryAdd("b"));
        Assert.True(cache.TryAdd("c"));  // evicts "a"
        Assert.True(cache.TryAdd("a"));  // "a" was forgotten
        Assert.True(cache.Count <= 2);
    }

    [Fact]
    public void Is_thread_safe_under_concurrent_adds()
    {
        var cache = new DeduplicationCache(10_000, TimeSpan.FromMinutes(10));
        var wins = 0;
        Parallel.For(0, 1000, i =>
        {
            if (cache.TryAdd("same-key"))
            {
                Interlocked.Increment(ref wins);
            }

            cache.TryAdd($"key-{i}");
        });
        Assert.Equal(1, wins); // exactly one thread may win a given key
    }
}
