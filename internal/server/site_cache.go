package server

import (
	"sync"

	"github.com/zhong/droply/internal/staticweb"
)

// Only immutable artifact configuration is cached. The caller holds deploymentMu
// while loading and serving, so cleanup cannot remove an artifact during a miss.
// A fixed FIFO bounds memory without updating eviction metadata on every hit.
const siteCacheCapacity = 64

type siteCache struct {
	mu    sync.Mutex
	sites map[string]*staticweb.Site
	order [siteCacheCapacity]string
	next  int
}

func (c *siteCache) load(id, root string) (*staticweb.Site, error) {
	c.mu.Lock()
	site := c.sites[id]
	c.mu.Unlock()
	if site != nil {
		return site, nil
	}

	// Independent misses may compile in parallel; errors are never retained.
	site, err := staticweb.Load(root)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.sites[id]; existing != nil {
		return existing, nil
	}
	if c.sites == nil {
		c.sites = make(map[string]*staticweb.Site)
	}
	delete(c.sites, c.order[c.next])
	c.order[c.next] = id
	c.next = (c.next + 1) % siteCacheCapacity
	c.sites[id] = site
	return site, nil
}

// forget runs under deploymentMu's write lock, excluding in-flight loads.
func (c *siteCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sites, id)
	for i, key := range c.order {
		if key == id {
			c.order[i] = ""
		}
	}
}
