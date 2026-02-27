package main

import (
	"container/list"
	"sync"
	"time"
)

// BoundedCache is a thread-safe, size-limited cache with TTL and LRU eviction.
// When the cache exceeds MaxSize, the least recently used entry is evicted.
type BoundedCache[V any] struct {
	mu      sync.RWMutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
	maxSize int
}

type boundedEntry[V any] struct {
	key       string
	value     V
	expiresAt time.Time
}

// NewBoundedCache creates a cache that holds at most maxSize entries.
func NewBoundedCache[V any](maxSize int) *BoundedCache[V] {
	return &BoundedCache[V]{
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// Get retrieves a value if it exists and hasn't expired.
func (c *BoundedCache[V]) Get(key string) (V, bool) {
	c.mu.RLock()
	elem, exists := c.entries[key]
	if !exists {
		c.mu.RUnlock()
		var zero V
		return zero, false
	}
	entry := elem.Value.(*boundedEntry[V])
	if time.Now().After(entry.expiresAt) {
		c.mu.RUnlock()
		// Expired — remove lazily
		c.mu.Lock()
		c.removeLocked(key)
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	c.mu.RUnlock()

	// Promote to front (most recently used)
	c.mu.Lock()
	c.order.MoveToFront(elem)
	c.mu.Unlock()

	return entry.value, true
}

// Set stores a value with a TTL. Evicts the LRU entry if at capacity.
func (c *BoundedCache[V]) Set(key string, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry
	if elem, exists := c.entries[key]; exists {
		c.order.MoveToFront(elem)
		entry := elem.Value.(*boundedEntry[V])
		entry.value = value
		entry.expiresAt = time.Now().Add(ttl)
		return
	}

	// Evict LRU if at capacity
	for c.order.Len() >= c.maxSize {
		back := c.order.Back()
		if back == nil {
			break
		}
		evicted := back.Value.(*boundedEntry[V])
		c.removeLocked(evicted.key)
	}

	// Insert new entry
	entry := &boundedEntry[V]{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	elem := c.order.PushFront(entry)
	c.entries[key] = elem
}

// Delete removes an entry by key.
func (c *BoundedCache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}

// CleanExpired removes all expired entries. Called by background workers.
func (c *BoundedCache[V]) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for key, elem := range c.entries {
		entry := elem.Value.(*boundedEntry[V])
		if now.After(entry.expiresAt) {
			c.removeLocked(key)
			cleaned++
		}
	}

	return cleaned
}

// Len returns the current number of entries (including possibly expired ones).
func (c *BoundedCache[V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order.Len()
}

// DeleteByPrefix removes all entries whose key starts with the given prefix.
func (c *BoundedCache[V]) DeleteByPrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	deleted := 0
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			c.removeLocked(key)
			deleted++
		}
	}
	return deleted
}

func (c *BoundedCache[V]) removeLocked(key string) {
	elem, exists := c.entries[key]
	if !exists {
		return
	}
	c.order.Remove(elem)
	delete(c.entries, key)
}
