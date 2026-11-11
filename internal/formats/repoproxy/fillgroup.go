package repoproxy

import "sync"

// fillGroup coordinates process-local fills without holding database connections.
// Only completion is shared; waiters read the cache with their own request options.
type fillGroup struct {
	mu    sync.Mutex
	fills map[string]chan struct{}
}

var cacheFills fillGroup

func (g *fillGroup) acquire(key string) (<-chan struct{}, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if done, ok := g.fills[key]; ok {
		return done, nil
	}
	if g.fills == nil {
		g.fills = make(map[string]chan struct{})
	}
	done := make(chan struct{})
	g.fills[key] = done
	return done, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.fills, key)
		close(done)
	}
}
