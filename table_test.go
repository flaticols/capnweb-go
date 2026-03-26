package capnweb

import (
	"sync"
	"testing"
)

func TestImportTableAllocate(t *testing.T) {
	tbl := NewImportTable()

	e1 := tbl.Allocate()
	e2 := tbl.Allocate()
	e3 := tbl.Allocate()

	if e1.ID != 1 || e2.ID != 2 || e3.ID != 3 {
		t.Fatalf("IDs = %d, %d, %d; want 1, 2, 3", e1.ID, e2.ID, e3.ID)
	}
	if e1.RefCount != 1 {
		t.Fatalf("refcount = %d; want 1", e1.RefCount)
	}
}

func TestImportTableBootstrap(t *testing.T) {
	tbl := NewImportTable()

	e := tbl.Get(0)
	if e == nil {
		t.Fatal("bootstrap entry (ID 0) missing")
	}
	if e.RefCount != 1 {
		t.Fatalf("bootstrap refcount = %d; want 1", e.RefCount)
	}
}

func TestImportTableInsertAndAddRef(t *testing.T) {
	tbl := NewImportTable()

	// Simulate receiving a remote export at negative ID.
	e := tbl.Insert(-1)
	if e.RefCount != 1 {
		t.Fatalf("refcount = %d; want 1", e.RefCount)
	}

	// Receive same ID again.
	e2 := tbl.Insert(-1)
	if e2.RefCount != 2 {
		t.Fatalf("refcount after re-insert = %d; want 2", e2.RefCount)
	}

	// AddRef also increments.
	tbl.AddRef(-1)
	if e.RefCount != 3 {
		t.Fatalf("refcount after AddRef = %d; want 3", e.RefCount)
	}
}

func TestImportTableRemove(t *testing.T) {
	tbl := NewImportTable()
	tbl.Allocate() // ID 1

	tbl.Remove(1)
	if tbl.Get(1) != nil {
		t.Fatal("entry should be removed")
	}
}

func TestExportTableBootstrap(t *testing.T) {
	main := &struct{ Name string }{"main"}
	tbl := NewExportTable(main)

	e := tbl.Get(0)
	if e == nil {
		t.Fatal("bootstrap entry (ID 0) missing")
	}
	if e.Target != main {
		t.Fatal("bootstrap target mismatch")
	}
}

func TestExportTableNilBootstrap(t *testing.T) {
	tbl := NewExportTable(nil)
	if tbl.Get(0) != nil {
		t.Fatal("nil bootstrap should not create entry")
	}
}

func TestExportTableExport(t *testing.T) {
	tbl := NewExportTable(nil)

	obj := &struct{ Name string }{"service"}
	e1 := tbl.Export(obj)
	if e1.ID != -1 {
		t.Fatalf("first export ID = %d; want -1", e1.ID)
	}
	if e1.RefCount != 1 {
		t.Fatalf("refcount = %d; want 1", e1.RefCount)
	}

	obj2 := &struct{ Name string }{"other"}
	e2 := tbl.Export(obj2)
	if e2.ID != -2 {
		t.Fatalf("second export ID = %d; want -2", e2.ID)
	}
}

func TestExportTableDeduplication(t *testing.T) {
	tbl := NewExportTable(nil)

	obj := &struct{ Name string }{"service"}
	e1 := tbl.Export(obj)
	e2 := tbl.Export(obj)

	if e1.ID != e2.ID {
		t.Fatalf("dedup failed: IDs %d vs %d", e1.ID, e2.ID)
	}
	if e2.RefCount != 2 {
		t.Fatalf("refcount = %d; want 2", e2.RefCount)
	}
}

func TestExportTableExportWithID(t *testing.T) {
	tbl := NewExportTable(nil)
	target := "result"

	e := tbl.ExportWithID(1, target)
	if e.ID != 1 || e.RefCount != 1 {
		t.Fatalf("got ID=%d refcount=%d", e.ID, e.RefCount)
	}

	// Re-export same ID increments refcount.
	e2 := tbl.ExportWithID(1, target)
	if e2.RefCount != 2 {
		t.Fatalf("refcount after re-export = %d; want 2", e2.RefCount)
	}
}

func TestExportTableHandleRelease(t *testing.T) {
	tbl := NewExportTable(nil)

	obj := &struct{ Name string }{"svc"}
	tbl.Export(obj) // refcount 1
	tbl.Export(obj) // refcount 2
	tbl.Export(obj) // refcount 3

	// Partial release.
	freed := tbl.HandleRelease(-1, 2)
	if freed {
		t.Fatal("should not be freed yet (refcount 1 remaining)")
	}
	e := tbl.Get(-1)
	if e == nil || e.RefCount != 1 {
		t.Fatalf("after partial release: entry=%v", e)
	}

	// Full release.
	freed = tbl.HandleRelease(-1, 1)
	if !freed {
		t.Fatal("should be freed now")
	}
	if tbl.Get(-1) != nil {
		t.Fatal("entry should be gone")
	}

	// Re-exporting same object gets a new ID now.
	e2 := tbl.Export(obj)
	if e2.ID == -1 {
		t.Fatal("ID should not be reused (IDs are never reused)")
	}
}

func TestExportTableReleaseUnknown(t *testing.T) {
	tbl := NewExportTable(nil)
	freed := tbl.HandleRelease(999, 1)
	if freed {
		t.Fatal("releasing unknown ID should return false")
	}
}

func TestImportTableConcurrent(t *testing.T) {
	tbl := NewImportTable()
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := tbl.Allocate()
			tbl.Get(e.ID)
			tbl.AddRef(e.ID)
		}()
	}
	wg.Wait()

	// All 100 allocations + bootstrap = 101 entries.
	count := 0
	for id := int64(0); id <= 100; id++ {
		if tbl.Get(id) != nil {
			count++
		}
	}
	if count != 101 {
		t.Fatalf("expected 101 entries, got %d", count)
	}
}

func TestExportTableConcurrent(t *testing.T) {
	tbl := NewExportTable(nil)
	var wg sync.WaitGroup

	// Export 100 different objects concurrently.
	type obj struct{ i int }
	objs := make([]*obj, 100)
	for i := range objs {
		objs[i] = &obj{i}
	}

	for _, o := range objs {
		wg.Add(1)
		go func(target *obj) {
			defer wg.Done()
			tbl.Export(target)
		}(o)
	}
	wg.Wait()

	// All 100 should have unique negative IDs.
	seen := map[int64]bool{}
	for id := int64(-1); id >= -100; id-- {
		if e := tbl.Get(id); e != nil {
			seen[id] = true
		}
	}
	if len(seen) != 100 {
		t.Fatalf("expected 100 exports, got %d", len(seen))
	}
}
