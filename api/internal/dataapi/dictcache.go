package dataapi

// Cross-call caches for project structure, validation types, and effective
// authorization levels. Structure-derived values carry the generation
// observed before they were built, and a later mismatch drops the entry.
// Authorization entries are additionally checked against live assignment
// role and user-admin state. The caches serve one API process, matching the
// single instance deployment behind nginx (Technology_Stack_Design.md §5).

import (
	"container/list"
	"database/sql"
	"sync"

	"csms/api/internal/authz"
	"csms/api/internal/validate"
)

// maxCachedDicts bounds the dictionary cache so a long-lived process cannot
// grow it with every project it ever touched; dictionaries are evicted
// least-recently-used first. A miss only costs one rebuild, which is what
// every call paid before caching existed.
const maxCachedDicts = 32
const maxCachedLevels = 256

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

type levelsKey struct {
	userID    int64
	projectID int64
}

type levelsEntry struct {
	key       levelsKey
	gen       uint64
	isAdmin   bool
	isMember  bool
	roleID    int64
	hasRoleID bool
	levels    *authz.Levels
}

// authzCache holds immutable effective levels per user/project. Assignment
// role and administrator state remain live inputs, so a mismatch invalidates
// the cached entry even when it did not advance the structure generation.
type authzCache struct {
	mu      sync.Mutex
	entries map[levelsKey]*list.Element
	lru     *list.List
}

func (c *authzCache) get(userID, projectID int64, gen uint64, isAdmin, isMember bool,
	roleID sql.NullInt64) (*authz.Levels, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := levelsKey{userID: userID, projectID: projectID}
	el := c.entries[key]
	if el == nil {
		return nil, false
	}
	e := el.Value.(*levelsEntry)
	if e.gen != gen || e.isAdmin != isAdmin || e.isMember != isMember ||
		e.hasRoleID != roleID.Valid || (roleID.Valid && e.roleID != roleID.Int64) {
		c.lru.Remove(el)
		delete(c.entries, key)
		return nil, false
	}
	c.lru.MoveToFront(el)
	return e.levels, true
}

func (c *authzCache) put(userID, projectID int64, gen uint64, isAdmin, isMember bool,
	roleID sql.NullInt64, levels *authz.Levels) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[levelsKey]*list.Element{}
		c.lru = list.New()
	}
	key := levelsKey{userID: userID, projectID: projectID}
	e := &levelsEntry{
		key: key, gen: gen, isAdmin: isAdmin, isMember: isMember,
		roleID: roleID.Int64, hasRoleID: roleID.Valid, levels: levels,
	}
	if el := c.entries[key]; el != nil {
		el.Value = e
		c.lru.MoveToFront(el)
		return
	}
	c.entries[key] = c.lru.PushFront(e)
	for c.lru.Len() > maxCachedLevels {
		back := c.lru.Back()
		c.lru.Remove(back)
		delete(c.entries, back.Value.(*levelsEntry).key)
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
