package dataapi

// Cross-call caches for the structure a record action needs: the project
// dictionary (loadDict) and the validation-type registry
// (validationRegistry). Both are read on every import/export/delete and
// change only through design writes, so they are kept until the store's
// structure generation moves (db.Store.StructureGeneration): each cached
// value carries the generation observed before it was built, and a later
// mismatch — including a write that landed while the build was running —
// drops the entry. The caches serve one API process, matching the single
// instance deployment behind nginx (Technology_Stack_Design.md §5).

import (
	"container/list"
	"sync"

	"csms/api/internal/validate"
)

// maxCachedDicts bounds the dictionary cache so a long-lived process cannot
// grow it with every project it ever touched; dictionaries are evicted
// least-recently-used first. A miss only costs one rebuild, which is what
// every call paid before caching existed.
const maxCachedDicts = 32

type dictEntry struct {
	projectID int64
	gen       uint64
	d         *projectDict
}

// dictCache holds one projectDict per project. Cached dictionaries are
// shared across requests and must stay read-only after build — loadDict's
// builder is the only writer (the dictionary has no lazy fields).
type dictCache struct {
	mu      sync.Mutex
	entries map[int64]*list.Element // front of lru = most recently used
	lru     *list.List              // elements carry *dictEntry
}

func (c *dictCache) get(projectID int64, gen uint64) (*projectDict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el := c.entries[projectID]
	if el == nil {
		return nil, false
	}
	e := el.Value.(*dictEntry)
	if e.gen != gen {
		// A structure write landed at or after this entry was read: drop it.
		c.lru.Remove(el)
		delete(c.entries, projectID)
		return nil, false
	}
	c.lru.MoveToFront(el)
	return e.d, true
}

func (c *dictCache) put(projectID int64, gen uint64, d *projectDict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[int64]*list.Element{}
		c.lru = list.New()
	}
	if el := c.entries[projectID]; el != nil {
		e := el.Value.(*dictEntry)
		e.gen, e.d = gen, d
		c.lru.MoveToFront(el)
		return
	}
	c.entries[projectID] = c.lru.PushFront(&dictEntry{projectID: projectID, gen: gen, d: d})
	for c.lru.Len() > maxCachedDicts {
		back := c.lru.Back()
		c.lru.Remove(back)
		delete(c.entries, back.Value.(*dictEntry).projectID)
	}
}

// registryCache is the single-entry counterpart for the validation-type
// registry: one global value, tagged with the same generation. Validation
// types are seeded by migrations and have no runtime write path today; the
// generation tag keeps a future writer honest as long as it lands in the
// paths that bump (db write methods or the administration surface).
type registryCache struct {
	mu   sync.Mutex
	gen  uint64
	reg  *validate.Registry
	seen bool
}

func (c *registryCache) get(gen uint64, build func() (*validate.Registry, error)) (*validate.Registry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen && c.gen == gen {
		return c.reg, nil
	}
	reg, err := build()
	if err != nil {
		return nil, err // failures are never cached
	}
	c.reg, c.gen, c.seen = reg, gen, true
	return reg, nil
}
