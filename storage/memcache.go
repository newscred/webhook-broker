package storage

import (
	"sync"
	"time"
)

// CacheItem represents a cached item with its expiration time.
type CacheItem[K comparable, V any] struct {
	Value      V
	Expiration time.Time
}

// MemoryCache is a generic in-memory cache with TTL support.
type MemoryCache[K comparable, V any] struct {
	cache    map[K]*CacheItem[K, V]
	ttl      time.Duration
	mutex    sync.RWMutex
	closeCh  chan struct{}
	closeWg  sync.WaitGroup
	isClosed bool
}

// NewMemoryCache creates a new MemoryCache instance.
// ttl: Time-to-live for cached items. 0 means no expiration.
func NewMemoryCache[K comparable, V any](ttl time.Duration) *MemoryCache[K, V] {
	c := &MemoryCache[K, V]{
		cache:   make(map[K]*CacheItem[K, V]),
		ttl:     ttl,
		closeCh: make(chan struct{}),
	}
	if ttl > 0 {
		c.closeWg.Add(1)
		go c.cleanup() // Start cleanup goroutine if TTL is set
	}
	return c
}

// Get retrieves an item from the cache.
// Returns the item and a boolean indicating if it was found.
func (c *MemoryCache[K, V]) Get(key K) (V, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if item, ok := c.cache[key]; ok {
		if c.ttl == 0 || time.Now().Before(item.Expiration) { // Check for expiration
			return item.Value, true
		}
		// Remove expired items since they aren't going to be returned
		delete(c.cache, key)
	}
	var emptyValue V
	return emptyValue, false
}

// Set adds or updates an item in the cache.
func (c *MemoryCache[K, V]) Set(key K, value V) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	expiration := time.Now().Add(c.ttl)
	if c.ttl == 0 {
		expiration = time.Time{}
	}
	c.cache[key] = &CacheItem[K, V]{Value: value, Expiration: expiration}
}

// Delete removes an item from the cache.
func (c *MemoryCache[K, V]) Delete(key K) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.cache, key)
}

// cleanup periodically removes expired items from the cache.
func (c *MemoryCache[K, V]) cleanup() {
	ticker := time.NewTicker(c.ttl / 2) // Check for expirations every half the TTL duration
	defer ticker.Stop()
	defer c.closeWg.Done()
	for {
		select {
		case <-ticker.C:
			c.mutex.Lock()
			now := time.Now()
			for key, item := range c.cache {
				if now.After(item.Expiration) {
					delete(c.cache, key)
				}
			}
			c.mutex.Unlock()
		case <-c.closeCh:
			return
		}
	}

}

func (c *MemoryCache[K, V]) Close() {
	if c.ttl > 0 && !c.isClosed {
		close(c.closeCh)
		c.closeWg.Wait()
		c.isClosed = true
	}
}
