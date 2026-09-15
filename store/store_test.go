package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentStoresAndCorruptState(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir)
	b, _ := Open(dir)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				var value struct{ Count int }
				if e := s.Update("counter", &value, func() error { value.Count++; return nil }); e != nil {
					t.Error(e)
					return
				}
			}
		}([]*Store{a, b}[i%2])
	}
	wg.Wait()
	var value struct{ Count int }
	if e := a.Update("counter", &value, nil); e != nil || value.Count != 120 {
		t.Fatalf("lost update: %d %v", value.Count, e)
	}
	_ = os.WriteFile(filepath.Join(dir, "counter.json"), []byte("corrupt"), 0600)
	if e := a.Update("counter", &value, func() error { value.Count = 0; return nil }); e == nil {
		t.Fatal("corruption silently reset")
	}
}
