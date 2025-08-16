package btree

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// reference is a deliberately simple ordered map used as an oracle: a Go map
// plus sorting on demand.
type reference map[string]string

func (r reference) sortedKeys() []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (r reference) rangeKeys(start, end []byte) []string {
	var out []string
	for _, k := range r.sortedKeys() {
		if start != nil && k < string(start) {
			continue
		}
		if end != nil && k >= string(end) {
			break
		}
		out = append(out, k)
	}
	return out
}

func randKey(rng *rand.Rand, space int) []byte {
	return []byte(fmt.Sprintf("%05d", rng.Intn(space)))
}

func TestRandomizedAgainstReference(t *testing.T) {
	for _, order := range []int{3, 4, 5, 7, 32} {
		for _, seed := range []int64{1, 2, 3} {
			t.Run(fmt.Sprintf("order=%d/seed=%d", order, seed), func(t *testing.T) {
				runRandomized(t, order, seed, 6000, 800)
			})
		}
	}
}

func runRandomized(t *testing.T, order int, seed int64, ops, keySpace int) {
	rng := rand.New(rand.NewSource(seed))
	tr := MustNew(order)
	ref := reference{}

	for op := 0; op < ops; op++ {
		k := randKey(rng, keySpace)
		switch r := rng.Intn(100); {
		case r < 50: // put
			v := fmt.Sprintf("v%d", rng.Int())
			_, existed := ref[string(k)]
			if replaced := tr.Put(k, []byte(v)); replaced != existed {
				t.Fatalf("op %d: Put(%s) replaced=%v, reference says existed=%v", op, k, replaced, existed)
			}
			ref[string(k)] = v
		case r < 80: // delete
			_, existed := ref[string(k)]
			if removed := tr.Delete(k); removed != existed {
				t.Fatalf("op %d: Delete(%s)=%v, reference says existed=%v", op, k, removed, existed)
			}
			delete(ref, string(k))
		case r < 95: // get
			want, existed := ref[string(k)]
			got, ok := tr.Get(k)
			if ok != existed || (ok && string(got) != want) {
				t.Fatalf("op %d: Get(%s)=%q,%v want %q,%v", op, k, got, ok, want, existed)
			}
		default: // range scan
			start := randKey(rng, keySpace)
			end := randKey(rng, keySpace)
			if bytes.Compare(start, end) > 0 {
				start, end = end, start
			}
			if rng.Intn(4) == 0 {
				start = nil
			}
			if rng.Intn(4) == 0 {
				end = nil
			}
			want := ref.rangeKeys(start, end)
			got := collect(tr, start, end)
			if len(got) != len(want) {
				t.Fatalf("op %d: scan [%s,%s) returned %d keys, want %d", op, start, end, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("op %d: scan position %d = %s, want %s", op, i, got[i], want[i])
				}
			}
		}

		if tr.Len() != len(ref) {
			t.Fatalf("op %d: Len=%d, reference has %d", op, tr.Len(), len(ref))
		}
		if op%200 == 0 {
			mustCheck(t, tr)
		}
	}
	mustCheck(t, tr)

	// Final full comparison, including values.
	keys := ref.sortedKeys()
	i := 0
	tr.AscendAll(func(k, v []byte) bool {
		if i >= len(keys) || string(k) != keys[i] || string(v) != ref[keys[i]] {
			t.Fatalf("final scan mismatch at %d: %s=%s", i, k, v)
		}
		i++
		return true
	})
	if i != len(keys) {
		t.Fatalf("final scan saw %d keys, want %d", i, len(keys))
	}

	// Drain the tree in random order and make sure it collapses cleanly.
	rng.Shuffle(len(keys), func(a, b int) { keys[a], keys[b] = keys[b], keys[a] })
	for n, k := range keys {
		if !tr.Delete([]byte(k)) {
			t.Fatalf("drain: Delete(%s) failed", k)
		}
		if n%50 == 0 {
			mustCheck(t, tr)
		}
	}
	mustCheck(t, tr)
	if tr.Len() != 0 || tr.Height() != 1 {
		t.Fatalf("after drain Len=%d Height=%d", tr.Len(), tr.Height())
	}
}
