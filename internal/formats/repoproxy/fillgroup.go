package repoproxy

import "sync"

type outcome struct {
	status int
	err    error
}

type fillEntry struct {
	done    chan struct{}
	outcome outcome // Published by closing done; readers must wait first.
}

// fillGroup shares completion and upstream failures, never a database connection.
type fillGroup struct {
	mu    sync.Mutex
	fills map[string]*fillEntry
}

var cacheFills fillGroup

func (g *fillGroup) acquire(key string) (*fillEntry, func(...outcome)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if entry, ok := g.fills[key]; ok {
		return entry, nil
	}
	if g.fills == nil {
		g.fills = make(map[string]*fillEntry)
	}
	entry := &fillEntry{done: make(chan struct{})}
	g.fills[key] = entry
	var once sync.Once
	return entry, func(result ...outcome) {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if len(result) > 0 {
				entry.outcome = result[0]
			}
			delete(g.fills, key)
			close(entry.done)
		})
	}
}
