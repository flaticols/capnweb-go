package capnweb

import "sync"

// ImportTable tracks objects imported from the remote endpoint.
// The importing side allocates positive IDs starting from 1.
type ImportTable struct {
	mu      sync.Mutex
	nextID  int64
	entries map[int64]*ImportEntry
}

// ImportEntry represents a single import table slot.
type ImportEntry struct {
	ID       int64
	RefCount int64 // times this import was "introduced" to us
	Resolved bool
	Value    any // resolved value, or nil if pending
}

// NewImportTable creates an import table. ID zero is pre-populated as the
// remote's bootstrap (main) interface.
func NewImportTable() *ImportTable {
	return &ImportTable{
		nextID: 1,
		entries: map[int64]*ImportEntry{
			0: {ID: 0, RefCount: 1},
		},
	}
}

// Allocate reserves the next positive import ID and returns the entry.
// Used when sending push/stream/pipe.
func (t *ImportTable) Allocate() *ImportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := t.nextID
	t.nextID++
	e := &ImportEntry{ID: id, RefCount: 1}
	t.entries[id] = e
	return e
}

// Get returns the entry for the given import ID, or nil if not found.
func (t *ImportTable) Get(id int64) *ImportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.entries[id]
}

// AddRef increments the refcount for an import (called when we receive the
// same ID again via an export/promise expression from the remote).
func (t *ImportTable) AddRef(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[id]; ok {
		e.RefCount++
	}
}

// Insert adds an entry for a remotely-chosen (negative) import ID.
// Used when the remote exports an object to us via ["export", negativeId].
func (t *ImportTable) Insert(id int64) *ImportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	if e, ok := t.entries[id]; ok {
		e.RefCount++
		return e
	}
	e := &ImportEntry{ID: id, RefCount: 1}
	t.entries[id] = e
	return e
}

// Remove deletes an entry from the table. Called after we send a release
// message for this import.
func (t *ImportTable) Remove(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, id)
}

// ExportTable tracks objects exported to the remote endpoint.
// The exporting side allocates negative IDs starting from -1.
type ExportTable struct {
	mu      sync.Mutex
	nextID  int64
	entries map[int64]*ExportEntry
	targets map[any]int64 // target → ID for deduplication
}

// ExportEntry represents a single export table slot.
type ExportEntry struct {
	ID       int64
	Target   any
	RefCount int64 // times we've exported this ID
}

// NewExportTable creates an export table. The bootstrap object is placed at
// ID zero.
func NewExportTable(main any) *ExportTable {
	t := &ExportTable{
		nextID:  -1,
		entries: map[int64]*ExportEntry{},
		targets: map[any]int64{},
	}
	if main != nil {
		t.entries[0] = &ExportEntry{ID: 0, Target: main, RefCount: 1}
		t.targets[main] = 0
	}
	return t
}

// Export exports a target object. If the same target was already exported,
// its existing ID is reused and the refcount is incremented. Returns the
// export entry.
func (t *ExportTable) Export(target any) *ExportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	if id, ok := t.targets[target]; ok {
		e := t.entries[id]
		e.RefCount++
		return e
	}

	id := t.nextID
	t.nextID--
	e := &ExportEntry{ID: id, Target: target, RefCount: 1}
	t.entries[id] = e
	t.targets[target] = id
	return e
}

// ExportWithID registers an export at a specific ID (used for result exports
// where the remote chose the positive ID via push/stream/pipe).
func (t *ExportTable) ExportWithID(id int64, target any) *ExportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	if e, ok := t.entries[id]; ok {
		e.RefCount++
		return e
	}
	e := &ExportEntry{ID: id, Target: target, RefCount: 1}
	t.entries[id] = e
	return e
}

// Get returns the entry for the given export ID, or nil if not found.
func (t *ExportTable) Get(id int64) *ExportEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.entries[id]
}

// HandleRelease decrements the refcount for an export by the given amount.
// If the refcount reaches zero, the entry is removed and true is returned.
func (t *ExportTable) HandleRelease(id int64, refcount int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[id]
	if !ok {
		return false
	}
	e.RefCount -= refcount
	if e.RefCount <= 0 {
		delete(t.entries, id)
		delete(t.targets, e.Target)
		return true
	}
	return false
}
