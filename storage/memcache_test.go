package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemoryCache(t *testing.T) {
	t.Run("NoExpiration", func(t *testing.T) {
		cache := NewMemoryCache[string, int](0)
		defer cache.Close()

		cache.Set("foo", 123)
		value, ok := cache.Get("foo")
		assert.True(t, ok)
		assert.Equal(t, 123, value)

		time.Sleep(1 * time.Second) // Wait a bit

		value, ok = cache.Get("foo")
		assert.True(t, ok)
		assert.Equal(t, 123, value)

		cache.Delete("foo")
		value, ok = cache.Get("foo")
		assert.False(t, ok)

	})

	t.Run("WithExpiration", func(t *testing.T) {
		cache := NewMemoryCache[string, string](1 * time.Second)
		defer cache.Close()

		cache.Set("bar", "baz")
		value, ok := cache.Get("bar")
		assert.True(t, ok)
		assert.Equal(t, "baz", value)

		time.Sleep(1100 * time.Millisecond) // Wait a bit longer than TTL

		value, ok = cache.Get("bar")
		assert.False(t, ok)
		assert.Equal(t, "", value) // Check if zero value is returned
	})
	t.Run("ZeroTTLClose", func(t *testing.T) {
		cache := NewMemoryCache[string, string](0)
		cache.Close()
		cache.Set("a", "b")
		v, ok := cache.Get("a")
		assert.True(t, ok)
		assert.Equal(t, "b", v)
	})

	t.Run("NonZeroTTLClose", func(t *testing.T) {
		cache := NewMemoryCache[string, string](1 * time.Second)
		cache.Close()
		cache.Set("a", "b")
		v, ok := cache.Get("a")
		assert.True(t, ok)
		assert.Equal(t, "b", v)
		time.Sleep(1100 * time.Millisecond)
		v, ok = cache.Get("a")
		assert.False(t, ok)

	})
}

func TestMemoryCache_Race(t *testing.T) {
	t.Run("ConcurrentGetExpired", func(t *testing.T) {
		cache := NewMemoryCache[string, int](time.Hour)
		defer cache.Close()

		// Insert an already-expired item, then read it from many goroutines at once.
		cache.cache["k"] = &CacheItem[string, int]{Value: 1, Expiration: time.Now().Add(-time.Minute)}

		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, ok := cache.Get("k")
				assert.False(t, ok)
			}()
		}
		wg.Wait()
	})

	t.Run("ConcurrentGetSetDelete", func(t *testing.T) {
		// Short TTL so the cleanup goroutine also runs during the test.
		cache := NewMemoryCache[int, int](10 * time.Millisecond)
		defer cache.Close()

		const keys = 8
		var wg sync.WaitGroup
		for g := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 500 {
					k := (g + i) % keys
					switch i % 3 {
					case 0:
						cache.Set(k, k)
					case 1:
						if v, ok := cache.Get(k); ok {
							assert.Equal(t, k, v)
						}
					case 2:
						cache.Delete(k)
					}
				}
			}()
		}
		wg.Wait()
	})
}
