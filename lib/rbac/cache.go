// Package rbac resolves "may this user do X?" against permissions owned by
// core-service. This service has NO permissions catalog of its own.
package rbac

import (
	"context"
	"sync"
	"time"
)

// Cache is an in-process, read-through, per-role permission cache with a TTL.
// Go analogue of order-service's lib/rbac/permission-cache.service.ts
// (Map + cachedAt), guarded by an RWMutex because Go handlers run in parallel.
//
// Each process has its own copy. Staleness is bounded by the TTL until the
// rbac.permissions_changed consumer (homework) calls Invalidate.
type Cache struct {
	fetcher PermissionFetcher
	ttl     time.Duration
	now     func() time.Time

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

func NewCache(fetcher PermissionFetcher, ttl time.Duration) *Cache {
	return &Cache{fetcher: fetcher, ttl: ttl, now: time.Now, entries: map[string]cacheEntry{}}
}

// Has reports whether role holds perm ("resource:action"). Fresh entries are
// served from memory; misses/expired entries go to core and are stored.
// A core failure is returned (fail closed) — never treated as "no permissions"
// and never cached.
func (c *Cache) Has(ctx context.Context, role, perm string) (bool, error) {
	if perms, ok := c.fresh(role); ok {
		_, has := perms[perm]
		return has, nil
	}

	list, err := c.fetcher.PermissionsByRole(ctx, role)
	if err != nil {
		return false, err
	}
	perms := make(map[string]struct{}, len(list))
	for _, p := range list {
		perms[p] = struct{}{}
	}

	c.mu.Lock()
	c.entries[role] = cacheEntry{perms: perms, cachedAt: c.now()}
	c.mu.Unlock()

	_, has := perms[perm]
	return has, nil
}

// Invalidate drops one role, or every role when role == "".
// Hook for the rbac.permissions_changed consumer (homework).
func (c *Cache) Invalidate(role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if role == "" {
		clear(c.entries)
		return
	}
	delete(c.entries, role)
}

func (c *Cache) fresh(role string) (map[string]struct{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[role]
	if !ok || c.now().Sub(e.cachedAt) >= c.ttl {
		return nil, false
	}
	return e.perms, true
}
