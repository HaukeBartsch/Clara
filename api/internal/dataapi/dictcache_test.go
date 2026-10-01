package dataapi

import (
	"context"
	"testing"

	"csms/api/internal/db"
)

// The dictionary and the validation registry are reused across calls until a
// structure write moves the store generation, then rebuilt — an import must
// see a field created moments earlier without any restart (dictcache.go).
func TestDictCacheInvalidatedByStructureWrites(t *testing.T) {
	h, _, _ := testHandler(t)
	ctx := context.Background()

	pid, err := h.Store.CreateProject(ctx, &db.Project{ProjectName: "cache-" + t.Name()})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	iid, err := h.Store.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "form1"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	addField := func(name string) {
		t.Helper()
		if _, err := h.Store.AddField(ctx, &db.Field{
			ProjectID: pid, InstrumentID: iid, FieldName: name, FieldType: "text",
		}); err != nil {
			t.Fatalf("AddField %s: %v", name, err)
		}
	}
	addField("record_id")

	d1, err := h.loadDict(ctx, pid)
	if err != nil {
		t.Fatalf("loadDict: %v", err)
	}
	d2, err := h.loadDict(ctx, pid)
	if err != nil {
		t.Fatalf("loadDict: %v", err)
	}
	if d2 != d1 {
		t.Errorf("second loadDict rebuilt the dictionary; want the cached value")
	}

	addField("extra")
	d3, err := h.loadDict(ctx, pid)
	if err != nil {
		t.Fatalf("loadDict after AddField: %v", err)
	}
	if d3 == d1 {
		t.Errorf("dictionary survived a structure write; want a rebuild")
	}
	if _, ok := d3.byName["extra"]; !ok {
		t.Errorf("rebuilt dictionary lacks the new field (fields: %d)", len(d3.fields))
	}
	d4, err := h.loadDict(ctx, pid)
	if err != nil {
		t.Fatalf("loadDict: %v", err)
	}
	if d4 != d3 {
		t.Errorf("third loadDict rebuilt again; want the cache settled on the new generation")
	}

	// The registry follows the same generation: shared while it holds,
	// rebuilt after the next structure write.
	r1, err := h.validationRegistry(ctx)
	if err != nil {
		t.Fatalf("validationRegistry: %v", err)
	}
	r2, err := h.validationRegistry(ctx)
	if err != nil {
		t.Fatalf("validationRegistry: %v", err)
	}
	if r2 != r1 {
		t.Errorf("second validationRegistry rebuilt; want the cached registry")
	}
	addField("third")
	r3, err := h.validationRegistry(ctx)
	if err != nil {
		t.Fatalf("validationRegistry after AddField: %v", err)
	}
	if r3 == r1 {
		t.Errorf("registry survived a structure write; want a rebuild")
	}
}

// The cache is bounded: the least recently used dictionary leaves first, and
// touching an entry saves it from the next eviction. A stale generation
// drops the entry outright.
func TestDictCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := &dictCache{}
	n := maxCachedDicts + 1
	for i := 1; i <= n; i++ {
		c.put(int64(i), 7, &projectDict{})
	}
	if _, ok := c.get(1, 7); ok {
		t.Errorf("oldest entry survived the overflow; want LRU eviction")
	}
	if _, ok := c.get(int64(n), 7); !ok {
		t.Errorf("newest entry missing; want it cached")
	}
	if _, ok := c.get(2, 7); !ok {
		t.Fatalf("second-oldest entry missing before the next eviction round")
	}
	c.put(int64(n+1), 7, &projectDict{}) // evicts LRU — id 3, since 2 was just touched
	if _, ok := c.get(3, 7); ok {
		t.Errorf("entry 3 survived although 2 was used more recently")
	}
	if _, ok := c.get(2, 7); !ok {
		t.Errorf("recently used entry was evicted")
	}

	c.put(100, 7, &projectDict{})
	if _, ok := c.get(100, 8); ok {
		t.Errorf("stale generation served a hit; want a miss")
	}
	if _, ok := c.get(100, 7); ok {
		t.Errorf("entry survived the stale lookup; want it dropped")
	}
}
