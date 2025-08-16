package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentAccess hammers one table from several writer and reader
// goroutines at once. It is most useful under `go test -race`, which will
// flag any access to the tree that escapes the table's RWMutex.
//
// Each writer owns a disjoint slice of the keyspace, so at the end the
// table's contents are fully determined and can be checked exactly. Readers
// verify invariants that must hold at every instant: values decode to the
// key they were written for, and scans come back strictly ordered.
func TestConcurrentAccess(t *testing.T) {
	e, err := New(Options{Order: 8})
	if err != nil {
		t.Fatal(err)
	}
	tbl, _ := e.CreateTable("hot")

	const (
		writers       = 4
		readers       = 8
		keysPerWriter = 500
		rounds        = 3
	)

	keyFor := func(w, i int) []byte { return []byte(fmt.Sprintf("w%d-%05d", w, i)) }
	valueFor := func(k []byte, round int) []byte {
		v := make([]byte, 8, 8+len(k))
		binary.BigEndian.PutUint64(v, uint64(round))
		return append(v, k...)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, readers+writers)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				for i := 0; i < keysPerWriter; i++ {
					k := keyFor(w, i)
					if _, err := tbl.Put(k, valueFor(k, round)); err != nil {
						errs <- err
						return
					}
				}
				// Delete every third key, then re-add it in a batch.
				var batch []Entry
				for i := 0; i < keysPerWriter; i += 3 {
					k := keyFor(w, i)
					tbl.Delete(k)
					batch = append(batch, Entry{Key: k, Value: valueFor(k, round)})
				}
				if _, err := tbl.BatchPut(batch); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}

	var rwg sync.WaitGroup
	for r := 0; r < readers; r++ {
		rwg.Add(1)
		go func(seed int64) {
			defer rwg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				if rng.Intn(4) == 0 {
					var prev []byte
					var scanErr error
					tbl.Scan(nil, nil, 200, func(k, v []byte) bool {
						if prev != nil && bytes.Compare(prev, k) >= 0 {
							scanErr = fmt.Errorf("scan out of order: %q then %q", prev, k)
							return false
						}
						prev = append(prev[:0], k...)
						return true
					})
					if scanErr != nil {
						errs <- scanErr
						return
					}
					continue
				}
				k := keyFor(rng.Intn(writers), rng.Intn(keysPerWriter))
				if v, ok := tbl.Get(k); ok && !bytes.Equal(v[8:], k) {
					errs <- fmt.Errorf("Get(%s) returned value for %q", k, v[8:])
					return
				}
				_ = tbl.Stats()
			}
		}(int64(r))
	}

	wg.Wait()
	close(stop)
	rwg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if got, want := tbl.Len(), writers*keysPerWriter; got != want {
		t.Fatalf("Len = %d, want %d", got, want)
	}
	for w := 0; w < writers; w++ {
		for i := 0; i < keysPerWriter; i++ {
			k := keyFor(w, i)
			v, ok := tbl.Get(k)
			if !ok {
				t.Fatalf("missing %s", k)
			}
			if !bytes.Equal(v, valueFor(k, rounds-1)) {
				t.Fatalf("%s has stale value from round %d", k, binary.BigEndian.Uint64(v[:8]))
			}
		}
	}
}

// TestConcurrentCatalogue creates, looks up and drops tables from many
// goroutines while others write into them.
func TestConcurrentCatalogue(t *testing.T) {
	e, _ := New(Options{Order: 4})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				name := fmt.Sprintf("t%d", (g+i)%5)
				tbl, err := e.CreateTable(name)
				if err != nil {
					tbl, err = e.Table(name)
				}
				if err == nil {
					tbl.Put([]byte("k"), []byte("v"))
					tbl.Get([]byte("k"))
				}
				_ = e.ListTables()
				_ = e.Stats()
				if i%7 == 0 {
					_ = e.DropTable(name)
				}
			}
		}(g)
	}
	wg.Wait()
}
