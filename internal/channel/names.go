package channel

import (
	"context"
	"sync"
	"time"
)

// How long a looked-up display name is trusted. A failed lookup (usually a
// missing permission) is not retried for a while, so it costs one call, not
// one per message.
const (
	nameTTL       = 24 * time.Hour
	nameRetry     = 10 * time.Minute
	NameTimeout   = 3 * time.Second // budget for one lookup while receiving
	maxCachedName = 10000           // entries kept before the cache starts over
)

// Names caches display names for platforms whose events carry only user
// ids (Lark, Slack). The zero value is not usable; set Lookup.
type Names struct {
	// Lookup asks the platform for the display name of a user id.
	Lookup func(ctx context.Context, id string) (string, error)

	mu    sync.Mutex
	known map[string]cachedName
	now   func() time.Time // replaced in tests
}

type cachedName struct {
	name    string
	expires time.Time
}

// Name returns the display name of id, "" when it is unknown. err is set
// only when a fresh lookup failed; a cached failure returns "" and nil, so
// callers can log errors without repeating them for every message.
func (n *Names) Name(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}

	now := n.clock()

	n.mu.Lock()
	c, ok := n.known[id]
	n.mu.Unlock()

	if ok && now.Before(c.expires) {
		return c.name, nil
	}

	name, err := n.Lookup(ctx, id)

	c = cachedName{name: name, expires: now.Add(nameTTL)}
	if err != nil || name == "" {
		c = cachedName{expires: now.Add(nameRetry)}
	}

	n.mu.Lock()
	if n.known == nil || len(n.known) >= maxCachedName {
		n.known = map[string]cachedName{}
	}

	n.known[id] = c
	n.mu.Unlock()

	return c.name, err
}

func (n *Names) clock() time.Time {
	if n.now != nil {
		return n.now()
	}

	return time.Now()
}
