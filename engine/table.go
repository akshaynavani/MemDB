package engine

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/akshaynavani/MemDB/btree"
)

// Entry is a single key/value pair.
type Entry struct {
	Key   []byte
	Value []byte
}

// Table is one named keyspace backed by a B+ tree. All methods are safe for
// concurrent use: lookups and scans share a read lock, mutations take the
// write lock.
type Table struct {
	name string
	opts Options

	mu   sync.RWMutex
	tree *btree.Tree

	// Operation counters are atomics so readers can bump them while holding
	// only the read lock.
	puts    atomic.Uint64
	gets    atomic.Uint64
	hits    atomic.Uint64
	deletes atomic.Uint64
	scans   atomic.Uint64
	batches atomic.Uint64
}

func newTable(name string, tree *btree.Tree, opts Options) *Table {
	return &Table{name: name, tree: tree, opts: opts}
}

// Name returns the table's name.
func (t *Table) Name() string { return t.name }

func (t *Table) validate(key, value []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key) > t.opts.MaxKeySize {
		return fmt.Errorf("%w: %d > %d bytes", ErrKeyTooLarge, len(key), t.opts.MaxKeySize)
	}
	if len(value) > t.opts.MaxValueSize {
		return fmt.Errorf("%w: %d > %d bytes", ErrValueTooLarge, len(value), t.opts.MaxValueSize)
	}
	return nil
}

// Put stores value under key and reports whether an existing value was
// replaced. The tree copies both slices, so the caller may reuse them.
func (t *Table) Put(key, value []byte) (replaced bool, err error) {
	if err := t.validate(key, value); err != nil {
		return false, err
	}
	t.mu.Lock()
	replaced = t.tree.Put(key, value)
	t.mu.Unlock()
	t.puts.Add(1)
	return replaced, nil
}

// Get returns a copy of the value stored under key.
func (t *Table) Get(key []byte) ([]byte, bool) {
	t.gets.Add(1)
	t.mu.RLock()
	v, ok := t.tree.Get(key)
	var out []byte
	if ok {
		out = make([]byte, len(v))
		copy(out, v)
	}
	t.mu.RUnlock()
	if ok {
		t.hits.Add(1)
	}
	return out, ok
}

// Delete removes key and reports whether it was present.
func (t *Table) Delete(key []byte) bool {
	t.mu.Lock()
	removed := t.tree.Delete(key)
	t.mu.Unlock()
	t.deletes.Add(1)
	return removed
}

// BatchPut validates every entry and then applies all of them under a single
// write lock, so concurrent readers observe either none or all of the batch.
// If any entry is invalid nothing is written. Later entries win when the
// batch repeats a key. It returns how many keys were newly inserted (as
// opposed to overwritten).
func (t *Table) BatchPut(entries []Entry) (inserted int, err error) {
	for i, e := range entries {
		if err := t.validate(e.Key, e.Value); err != nil {
			return 0, fmt.Errorf("entry %d: %w", i, err)
		}
	}
	t.mu.Lock()
	for _, e := range entries {
		if !t.tree.Put(e.Key, e.Value) {
			inserted++
		}
	}
	t.mu.Unlock()
	t.batches.Add(1)
	t.puts.Add(uint64(len(entries)))
	return inserted, nil
}

// Scan calls fn for keys in [start, end) in ascending order, stopping after
// limit entries (limit <= 0 means no limit) or when fn returns false. It
// returns the number of entries visited.
//
// fn runs while the table's read lock is held, so it must be quick and must
// not call back into this table's write methods. The slices passed to fn
// must not be modified or retained; use ScanPage to get owned copies.
func (t *Table) Scan(start, end []byte, limit int, fn func(key, value []byte) bool) int {
	t.scans.Add(1)
	n := 0
	t.mu.RLock()
	defer t.mu.RUnlock()
	t.tree.Ascend(start, end, func(k, v []byte) bool {
		if limit > 0 && n >= limit {
			return false
		}
		n++
		return fn(k, v)
	})
	return n
}

// ScanPage returns up to limit entries from [start, end) as owned copies,
// along with the key at which the next page begins. next is nil when the
// range is exhausted. limit must be positive.
//
// Paging lets callers stream large ranges without holding the read lock for
// the whole transfer; each page is a consistent snapshot of its own slice of
// the range, but writes may land between pages.
func (t *Table) ScanPage(start, end []byte, limit int) (entries []Entry, next []byte) {
	if limit <= 0 {
		limit = 1
	}
	t.scans.Add(1)
	t.mu.RLock()
	defer t.mu.RUnlock()
	t.tree.Ascend(start, end, func(k, v []byte) bool {
		if len(entries) == limit {
			next = append([]byte(nil), k...)
			return false
		}
		entries = append(entries, Entry{
			Key:   append([]byte(nil), k...),
			Value: append([]byte{}, v...),
		})
		return true
	})
	return entries, next
}

// Len returns the number of keys in the table.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tree.Len()
}
