package engine

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
)

const benchKeys = 1 << 17

func bkey(i uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, i)
	return b
}

func benchTable(b *testing.B, prefill int) *Table {
	b.Helper()
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	t, _ := e.CreateTable("bench")
	v := make([]byte, 64)
	entries := make([]Entry, 0, 1024)
	for i := 0; i < prefill; i++ {
		entries = append(entries, Entry{Key: bkey(uint64(i)), Value: v})
		if len(entries) == cap(entries) || i == prefill-1 {
			if _, err := t.BatchPut(entries); err != nil {
				b.Fatal(err)
			}
			entries = entries[:0]
		}
	}
	return t
}

func BenchmarkTablePut(b *testing.B) {
	t := benchTable(b, 0)
	v := make([]byte, 64)
	rng := rand.New(rand.NewSource(1))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := t.Put(bkey(rng.Uint64()), v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTableBatchPut(b *testing.B) {
	for _, size := range []int{16, 256} {
		b.Run(fmt.Sprintf("batch=%d", size), func(b *testing.B) {
			t := benchTable(b, 0)
			v := make([]byte, 64)
			rng := rand.New(rand.NewSource(2))
			batch := make([]Entry, size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := range batch {
					batch[j] = Entry{Key: bkey(rng.Uint64()), Value: v}
				}
				if _, err := t.BatchPut(batch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTableGet(b *testing.B) {
	t := benchTable(b, benchKeys)
	rng := rand.New(rand.NewSource(3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := t.Get(bkey(uint64(rng.Intn(benchKeys)))); !ok {
			b.Fatal("missing key")
		}
	}
}

func BenchmarkTableScan(b *testing.B) {
	t := benchTable(b, benchKeys)
	for _, span := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("span=%d", span), func(b *testing.B) {
			rng := rand.New(rand.NewSource(4))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := uint64(rng.Intn(benchKeys - span))
				n := t.Scan(bkey(start), nil, span, func(_, _ []byte) bool { return true })
				if n != span {
					b.Fatalf("scan visited %d, want %d", n, span)
				}
			}
		})
	}
}

func BenchmarkTableScanPage(b *testing.B) {
	t := benchTable(b, benchKeys)
	rng := rand.New(rand.NewSource(5))
	const page = 256
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := uint64(rng.Intn(benchKeys - page))
		entries, _ := t.ScanPage(bkey(start), nil, page)
		if len(entries) != page {
			b.Fatalf("page had %d entries", len(entries))
		}
	}
}

// BenchmarkParallelGet measures read scalability: every goroutine only
// takes the read lock, so they should not serialise on each other.
func BenchmarkParallelGet(b *testing.B) {
	t := benchTable(b, benchKeys)
	var seed atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(seed.Add(1)))
		for pb.Next() {
			t.Get(bkey(uint64(rng.Intn(benchKeys))))
		}
	})
}

// BenchmarkParallelMixed runs gets, short scans and puts from all
// GOMAXPROCS goroutines at once. The read percentage controls how often the
// exclusive write lock is taken; comparing the sub-benchmarks shows how much
// writer contention costs.
func BenchmarkParallelMixed(b *testing.B) {
	for _, readPct := range []int{50, 90, 99} {
		b.Run(fmt.Sprintf("reads=%d%%", readPct), func(b *testing.B) {
			t := benchTable(b, benchKeys)
			v := make([]byte, 64)
			var seed atomic.Int64
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				rng := rand.New(rand.NewSource(seed.Add(1)))
				for pb.Next() {
					k := bkey(uint64(rng.Intn(benchKeys)))
					switch r := rng.Intn(100); {
					case r >= readPct:
						t.Put(k, v)
					case r%10 == 0: // a tenth of reads are short range scans
						t.Scan(k, nil, 16, func(_, _ []byte) bool { return true })
					default:
						t.Get(k)
					}
				}
			})
		})
	}
}
