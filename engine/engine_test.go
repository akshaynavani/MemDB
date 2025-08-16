package engine

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Options{Order: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestNewRejectsBadOrder(t *testing.T) {
	if _, err := New(Options{Order: 2}); err == nil {
		t.Fatal("expected error for order 2")
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Order() == 0 {
		t.Fatal("default order not applied")
	}
}

func TestTableLifecycle(t *testing.T) {
	e := newEngine(t)
	if _, err := e.CreateTable("users"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateTable("users"); !errors.Is(err, ErrTableExists) {
		t.Fatalf("duplicate create: got %v, want ErrTableExists", err)
	}
	if _, err := e.CreateTable("orders"); err != nil {
		t.Fatal(err)
	}
	if got := e.ListTables(); !reflect.DeepEqual(got, []string{"orders", "users"}) {
		t.Fatalf("ListTables = %v", got)
	}
	if _, err := e.Table("users"); err != nil {
		t.Fatal(err)
	}
	if err := e.DropTable("users"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Table("users"); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("lookup after drop: got %v", err)
	}
	if err := e.DropTable("users"); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("double drop: got %v", err)
	}
}

func TestInvalidTableNames(t *testing.T) {
	e := newEngine(t)
	for _, name := range []string{"", "has space", "slash/name", strings.Repeat("x", 200)} {
		if _, err := e.CreateTable(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CreateTable(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	for _, name := range []string{"a", "A_b-c.d", "t123"} {
		if _, err := e.CreateTable(name); err != nil {
			t.Errorf("CreateTable(%q) = %v", name, err)
		}
	}
}

func TestPutGetDelete(t *testing.T) {
	e := newEngine(t)
	tbl, _ := e.CreateTable("kv")

	replaced, err := tbl.Put([]byte("a"), []byte("1"))
	if err != nil || replaced {
		t.Fatalf("first Put: replaced=%v err=%v", replaced, err)
	}
	replaced, err = tbl.Put([]byte("a"), []byte("2"))
	if err != nil || !replaced {
		t.Fatalf("second Put: replaced=%v err=%v", replaced, err)
	}
	v, ok := tbl.Get([]byte("a"))
	if !ok || string(v) != "2" {
		t.Fatalf("Get = %q, %v", v, ok)
	}

	// The returned value is a copy: mutating it must not affect the table.
	v[0] = 'X'
	if v2, _ := tbl.Get([]byte("a")); string(v2) != "2" {
		t.Fatalf("Get result aliased table storage: %q", v2)
	}

	if !tbl.Delete([]byte("a")) {
		t.Fatal("Delete reported missing")
	}
	if _, ok := tbl.Get([]byte("a")); ok {
		t.Fatal("key still present after Delete")
	}
	if tbl.Delete([]byte("a")) {
		t.Fatal("second Delete reported success")
	}
}

func TestPutValidation(t *testing.T) {
	e, _ := New(Options{Order: 4, MaxKeySize: 8, MaxValueSize: 16})
	tbl, _ := e.CreateTable("kv")
	if _, err := tbl.Put(nil, []byte("v")); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("empty key: %v", err)
	}
	if _, err := tbl.Put([]byte("123456789"), nil); !errors.Is(err, ErrKeyTooLarge) {
		t.Errorf("long key: %v", err)
	}
	if _, err := tbl.Put([]byte("k"), make([]byte, 17)); !errors.Is(err, ErrValueTooLarge) {
		t.Errorf("long value: %v", err)
	}
	if _, err := tbl.Put([]byte("k"), nil); err != nil {
		t.Errorf("empty value should be allowed: %v", err)
	}
	if v, ok := tbl.Get([]byte("k")); !ok || len(v) != 0 {
		t.Errorf("empty value round trip: %q %v", v, ok)
	}
}

func TestBatchPutIsAllOrNothing(t *testing.T) {
	e := newEngine(t)
	tbl, _ := e.CreateTable("kv")
	tbl.Put([]byte("b"), []byte("old"))

	_, err := tbl.BatchPut([]Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: nil, Value: []byte("bad")},
	})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("expected ErrEmptyKey, got %v", err)
	}
	if _, ok := tbl.Get([]byte("a")); ok {
		t.Fatal("invalid batch was partially applied")
	}

	inserted, err := tbl.BatchPut([]Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("b"), Value: []byte("new")},
		{Key: []byte("c"), Value: []byte("3")},
		{Key: []byte("a"), Value: []byte("1b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 2 {
		t.Fatalf("inserted = %d, want 2 (a and c are new, b and the repeat of a overwrite)", inserted)
	}
	for k, want := range map[string]string{"a": "1b", "b": "new", "c": "3"} {
		if v, _ := tbl.Get([]byte(k)); string(v) != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
}

func fill(t *testing.T, tbl *Table, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := tbl.Put([]byte(fmt.Sprintf("k%04d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanLimitAndBounds(t *testing.T) {
	e := newEngine(t)
	tbl, _ := e.CreateTable("kv")
	fill(t, tbl, 100)

	var keys []string
	n := tbl.Scan([]byte("k0010"), []byte("k0020"), 0, func(k, _ []byte) bool {
		keys = append(keys, string(k))
		return true
	})
	if n != 10 || keys[0] != "k0010" || keys[9] != "k0019" {
		t.Fatalf("bounded scan: n=%d keys=%v", n, keys)
	}

	n = tbl.Scan(nil, nil, 5, func(_, _ []byte) bool { return true })
	if n != 5 {
		t.Fatalf("limited scan visited %d, want 5", n)
	}
}

func TestScanPagePaginatesWholeRange(t *testing.T) {
	e := newEngine(t)
	tbl, _ := e.CreateTable("kv")
	fill(t, tbl, 95)

	var all []string
	start := []byte(nil)
	pages := 0
	for {
		entries, next := tbl.ScanPage(start, nil, 10)
		pages++
		for _, en := range entries {
			all = append(all, string(en.Key))
		}
		if next == nil {
			break
		}
		start = next
	}
	if pages != 10 {
		t.Fatalf("pages = %d, want 10", pages)
	}
	if len(all) != 95 {
		t.Fatalf("collected %d keys, want 95", len(all))
	}
	for i, k := range all {
		if k != fmt.Sprintf("k%04d", i) {
			t.Fatalf("position %d = %s", i, k)
		}
	}
}

func TestStats(t *testing.T) {
	e := newEngine(t)
	tbl, _ := e.CreateTable("kv")
	fill(t, tbl, 50)
	tbl.Get([]byte("k0001"))
	tbl.Get([]byte("missing"))
	tbl.Delete([]byte("k0002"))
	tbl.BatchPut([]Entry{{Key: []byte("x"), Value: []byte("y")}})
	tbl.Scan(nil, nil, 1, func(_, _ []byte) bool { return true })

	s := tbl.Stats()
	want := TableStats{
		Name: "kv", Keys: 50, Height: s.Height, Order: 4,
		Puts: 51, Gets: 2, Hits: 1, Deletes: 1, Scans: 1, Batches: 1,
	}
	if s != want {
		t.Fatalf("Stats = %+v\nwant    %+v", s, want)
	}
	if s.Height < 2 {
		t.Fatalf("height %d: expected 50 keys at order 4 to need more than one level", s.Height)
	}

	e.CreateTable("empty")
	all := e.Stats()
	if len(all) != 2 || all[0].Name != "empty" || all[1].Name != "kv" {
		t.Fatalf("engine Stats = %+v", all)
	}
}
