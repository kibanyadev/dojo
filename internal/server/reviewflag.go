package server

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// DefaultFlagCacheTTL is how long the API keeps ai_reviews_enabled (design 3.10: 30 s).
const DefaultFlagCacheTTL = 30 * time.Second

// ParseFlagCacheTTL reads DOJO_FLAG_CACHE_SECONDS: empty is the 30 s default, 0 turns the cache off
// (the integration stack uses it so a flipped flag shows at once), at most 300.
func ParseFlagCacheTTL(s string) (time.Duration, error) {
	if s == "" {
		return DefaultFlagCacheTTL, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 300 {
		return 0, fmt.Errorf("DOJO_FLAG_CACHE_SECONDS must be 0 to 300, got %q", s)
	}
	return time.Duration(n) * time.Second, nil
}

// flagCache holds one boolean flag for ttl. A zero ttl reads every time. A failed read is never cached.
type flagCache struct {
	ttl time.Duration
	now func() time.Time // nil is time.Now

	mu  sync.Mutex
	val bool
	at  time.Time
	ok  bool
}

func (c *flagCache) enabled(ctx context.Context, load func(context.Context) (bool, error)) (bool, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if c.ttl > 0 {
		c.mu.Lock()
		if c.ok && now().Sub(c.at) < c.ttl {
			v := c.val
			c.mu.Unlock()
			return v, nil
		}
		c.mu.Unlock()
	}
	v, err := load(ctx)
	if err != nil {
		return false, err
	}
	if c.ttl > 0 {
		c.mu.Lock()
		c.val, c.at, c.ok = v, now(), true
		c.mu.Unlock()
	}
	return v, nil
}
