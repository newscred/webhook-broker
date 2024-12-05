package storage

import (
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

