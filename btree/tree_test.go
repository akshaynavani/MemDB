package btree

import (
	"bytes"
	"fmt"
	"testing"
)

func key(i int) []byte { return []byte(fmt.Sprintf("k%06d", i)) }
func val(i int) []byte { return []byte(fmt.Sprintf("v%d", i)) }

func mustCheck(t *testing.T, tr *Tree) {
	t.Helper()
	if err := tr.Check(); err != nil {
		t.Fatalf("invariant violated: %v", err)
	}
}

func collect(tr *Tree, start, end []byte) []string {
	var out []string
	tr.Ascend(start, end, func(k, _ []byte) bool {
		out = append(out, string(k))
		return true
	})
	return out
}

func TestNewRejectsSmallOrder(t *testing.T) {
	for _, order := range []int{-1, 1, 2} {
		if _, err := New(order); err == nil {
			t.Errorf("New(%d) succeeded, want error", order)
		}
	}
	tr, err := New(0)
	if err != nil {
		t.Fatalf("New(0): %v", err)
	}
	if tr.Order() != DefaultOrder {
		t.Fatalf("Order() = %d, want %d", tr.Order(), DefaultOrder)
	}
}

func TestEmptyTree(t *testing.T) {
	tr := MustNew(4)
	if tr.Len() != 0 || tr.Height() != 1 {
		t.Fatalf("Len=%d Height=%d, want 0 and 1", tr.Len(), tr.Height())
	}
	if _, ok := tr.Get([]byte("x")); ok {
		t.Fatal("Get on empty tree found a key")
	}
	if tr.Delete([]byte("x")) {
		t.Fatal("Delete on empty tree reported success")
	}
	if _, _, ok := tr.Min(); ok {
		t.Fatal("Min on empty tree reported a key")
	}
	if _, _, ok := tr.Max(); ok {
		t.Fatal("Max on empty tree reported a key")
	}
	if got := collect(tr, nil, nil); len(got) != 0 {
		t.Fatalf("scan of empty tree returned %v", got)
	}
	mustCheck(t, tr)
}

func TestPutGetAndOverwrite(t *testing.T) {
	for _, order := range []int{3, 4, 5, 16} {
		t.Run(fmt.Sprintf("order=%d", order), func(t *testing.T) {
			tr := MustNew(order)
			const n = 500
			for i := 0; i < n; i++ {
				if tr.Put(key(i), val(i)) {
					t.Fatalf("Put(%d) reported replace on first insert", i)
				}
			}
			mustCheck(t, tr)
			if tr.Len() != n {
				t.Fatalf("Len = %d, want %d", tr.Len(), n)
			}
			if tr.Height() < 2 {
				t.Fatalf("Height = %d, expected the tree to have split", tr.Height())
			}
			for i := 0; i < n; i++ {
				v, ok := tr.Get(key(i))
				if !ok || !bytes.Equal(v, val(i)) {
					t.Fatalf("Get(%d) = %q, %v", i, v, ok)
				}
			}
			if !tr.Put(key(7), []byte("new")) {
				t.Fatal("overwrite did not report replace")
			}
			if v, _ := tr.Get(key(7)); string(v) != "new" {
				t.Fatalf("Get after overwrite = %q", v)
			}
			if tr.Len() != n {
				t.Fatalf("Len after overwrite = %d, want %d", tr.Len(), n)
			}
			mustCheck(t, tr)
		})
	}
}

func TestPutCopiesInputs(t *testing.T) {
	tr := MustNew(4)
	k := []byte("key")
	v := []byte("value")
	tr.Put(k, v)
	k[0] = 'X'
	v[0] = 'X'
	got, ok := tr.Get([]byte("key"))
	if !ok || string(got) != "value" {
		t.Fatalf("stored entry was aliased to caller buffers: %q %v", got, ok)
	}
}

func TestReverseAndInterleavedInsert(t *testing.T) {
	tr := MustNew(3)
	for i := 299; i >= 0; i-- {
		tr.Put(key(i), val(i))
		mustCheck(t, tr)
	}
	tr2 := MustNew(3)
	for i := 0; i < 150; i++ {
		tr2.Put(key(i), val(i))
		tr2.Put(key(299-i), val(299-i))
	}
	mustCheck(t, tr2)
	a, b := collect(tr, nil, nil), collect(tr2, nil, nil)
	if len(a) != 300 || len(b) != 300 {
		t.Fatalf("lengths %d %d, want 300", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] || a[i] != string(key(i)) {
			t.Fatalf("position %d: %s vs %s", i, a[i], b[i])
		}
	}
}

func TestAscendBounds(t *testing.T) {
	tr := MustNew(4)
	for i := 0; i < 100; i += 2 { // even keys only
		tr.Put(key(i), val(i))
	}
	cases := []struct {
		name       string
		start, end []byte
		first      string
		count      int
	}{
		{"full", nil, nil, string(key(0)), 50},
		{"from existing", key(10), nil, string(key(10)), 45},
		{"from missing", key(11), nil, string(key(12)), 44},
		{"bounded", key(10), key(20), string(key(10)), 5},
		{"end exclusive", key(0), key(2), string(key(0)), 1},
		{"empty range", key(30), key(30), "", 0},
		{"past the end", key(1000), nil, "", 0},
		{"before the start", []byte("a"), key(4), string(key(0)), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collect(tr, tc.start, tc.end)
			if len(got) != tc.count {
				t.Fatalf("got %d keys (%v), want %d", len(got), got, tc.count)
			}
			if tc.count > 0 && got[0] != tc.first {
				t.Fatalf("first key %s, want %s", got[0], tc.first)
			}
		})
	}
}

func TestAscendEarlyStop(t *testing.T) {
	tr := MustNew(4)
	for i := 0; i < 50; i++ {
		tr.Put(key(i), val(i))
	}
	seen := 0
	tr.AscendAll(func(_, _ []byte) bool {
		seen++
		return seen < 7
	})
	if seen != 7 {
		t.Fatalf("callback ran %d times, want 7", seen)
	}
}

func TestMinMax(t *testing.T) {
	tr := MustNew(5)
	for _, i := range []int{50, 3, 99, 42, 7} {
		tr.Put(key(i), val(i))
	}
	if k, _, _ := tr.Min(); string(k) != string(key(3)) {
		t.Fatalf("Min = %s", k)
	}
	if k, _, _ := tr.Max(); string(k) != string(key(99)) {
		t.Fatalf("Max = %s", k)
	}
}

func TestDeleteAll(t *testing.T) {
	for _, order := range []int{3, 4, 5, 8} {
		t.Run(fmt.Sprintf("order=%d", order), func(t *testing.T) {
			tr := MustNew(order)
			const n = 400
			for i := 0; i < n; i++ {
				tr.Put(key(i), val(i))
			}
			// Delete from the middle outwards to exercise both borrow directions.
			for d := 0; d < n/2; d++ {
				for _, i := range []int{n/2 - 1 - d, n/2 + d} {
					if !tr.Delete(key(i)) {
						t.Fatalf("Delete(%d) reported missing", i)
					}
					if tr.Has(key(i)) {
						t.Fatalf("key %d still present after delete", i)
					}
					mustCheck(t, tr)
				}
			}
			if tr.Len() != 0 || tr.Height() != 1 {
				t.Fatalf("after deleting everything Len=%d Height=%d", tr.Len(), tr.Height())
			}
		})
	}
}

func TestDeleteMissingAndTwice(t *testing.T) {
	tr := MustNew(4)
	for i := 0; i < 20; i++ {
		tr.Put(key(i), val(i))
	}
	if tr.Delete([]byte("nope")) {
		t.Fatal("deleted a key that was never inserted")
	}
	if !tr.Delete(key(5)) {
		t.Fatal("first delete failed")
	}
	if tr.Delete(key(5)) {
		t.Fatal("second delete of the same key succeeded")
	}
	if tr.Len() != 19 {
		t.Fatalf("Len = %d, want 19", tr.Len())
	}
	mustCheck(t, tr)
}

func TestScanAfterDeletes(t *testing.T) {
	tr := MustNew(3)
	for i := 0; i < 200; i++ {
		tr.Put(key(i), val(i))
	}
	for i := 0; i < 200; i += 3 {
		tr.Delete(key(i))
	}
	mustCheck(t, tr)
	got := collect(tr, nil, nil)
	j := 0
	for i := 0; i < 200; i++ {
		if i%3 == 0 {
			continue
		}
		if got[j] != string(key(i)) {
			t.Fatalf("position %d: got %s want %s", j, got[j], key(i))
		}
		j++
	}
	if j != len(got) {
		t.Fatalf("scan returned %d keys, want %d", len(got), j)
	}
}
