package btree

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

var benchOrders = []int{8, 32, 64, 128}

// benchKey encodes i as a fixed-width big-endian key so byte order matches
// numeric order.
func benchKey(i uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], i)
	return b[:]
}

func prefilled(b *testing.B, order, n int) *Tree {
	b.Helper()
	tr := MustNew(order)
	v := make([]byte, 32)
	for i := 0; i < n; i++ {
		tr.Put(benchKey(uint64(i)), v)
	}
	return tr
}

func BenchmarkPutSequential(b *testing.B) {
	for _, order := range benchOrders {
		b.Run(fmt.Sprintf("order=%d", order), func(b *testing.B) {
			tr := MustNew(order)
			v := make([]byte, 32)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tr.Put(benchKey(uint64(i)), v)
			}
		})
	}
}

func BenchmarkPutRandom(b *testing.B) {
	for _, order := range benchOrders {
		b.Run(fmt.Sprintf("order=%d", order), func(b *testing.B) {
			tr := MustNew(order)
			v := make([]byte, 32)
			rng := rand.New(rand.NewSource(1))
			keys := make([][]byte, b.N)
			for i := range keys {
				keys[i] = benchKey(rng.Uint64())
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tr.Put(keys[i], v)
			}
		})
	}
}

func BenchmarkGet(b *testing.B) {
	const n = 1 << 18
	for _, order := range benchOrders {
		b.Run(fmt.Sprintf("order=%d", order), func(b *testing.B) {
			tr := prefilled(b, order, n)
			rng := rand.New(rand.NewSource(2))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := tr.Get(benchKey(uint64(rng.Intn(n)))); !ok {
					b.Fatal("missing key")
				}
			}
		})
	}
}

func BenchmarkAscend(b *testing.B) {
	const n = 1 << 18
	for _, span := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("span=%d", span), func(b *testing.B) {
			tr := prefilled(b, DefaultOrder, n)
			rng := rand.New(rand.NewSource(3))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := uint64(rng.Intn(n - span))
				seen := 0
				tr.Ascend(benchKey(start), benchKey(start+uint64(span)), func(_, _ []byte) bool {
					seen++
					return true
				})
				if seen != span {
					b.Fatalf("scan saw %d keys, want %d", seen, span)
				}
			}
		})
	}
}

func BenchmarkDelete(b *testing.B) {
	for _, order := range benchOrders {
		b.Run(fmt.Sprintf("order=%d", order), func(b *testing.B) {
			tr := prefilled(b, order, b.N)
			rng := rand.New(rand.NewSource(4))
			perm := rng.Perm(b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for _, i := range perm {
				tr.Delete(benchKey(uint64(i)))
			}
		})
	}
}
