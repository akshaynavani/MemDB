// Package engine is MemDB's storage layer: a set of named tables, each backed
// by its own B+ tree and guarded by its own sync.RWMutex.
//
// Locking is two-level. The engine's mutex protects only the table catalogue
// (create, drop, lookup); once a caller holds a *Table, operations on that
// table take the table's lock and never touch the engine's. Reads (Get, Scan)
// take the read lock and therefore run in parallel; writes (Put, Delete,
// BatchPut) take the write lock and are exclusive.
package engine

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/akshaynavani/MemDB/btree"
)

// Errors returned by the engine. Use errors.Is to test for them.
var (
	ErrTableExists   = errors.New("table already exists")
	ErrTableNotFound = errors.New("table not found")
	ErrInvalidName   = errors.New("invalid table name")
	ErrEmptyKey      = errors.New("key must not be empty")
	ErrKeyTooLarge   = errors.New("key exceeds maximum size")
	ErrValueTooLarge = errors.New("value exceeds maximum size")
)

// Default limits. They exist to keep one request from pinning an unbounded
// amount of memory, not because the tree needs them.
const (
	DefaultMaxKeySize   = 4 << 10  // 4 KiB
	DefaultMaxValueSize = 16 << 20 // 16 MiB
	maxTableNameLen     = 128
)

// Options configures an Engine.
type Options struct {
	// Order is the B+ tree order used for new tables. Zero means btree.DefaultOrder.
	Order int
	// MaxKeySize and MaxValueSize bound individual entries. Zero selects the defaults.
	MaxKeySize   int
	MaxValueSize int
}

// Engine is a catalogue of named tables. It is safe for concurrent use.
type Engine struct {
	mu     sync.RWMutex
	tables map[string]*Table
	opts   Options
}

// New creates an empty engine.
func New(opts Options) (*Engine, error) {
	if opts.Order == 0 {
		opts.Order = btree.DefaultOrder
	}
	if opts.Order < btree.MinOrder {
		return nil, fmt.Errorf("engine: tree order %d is below minimum %d", opts.Order, btree.MinOrder)
	}
	if opts.MaxKeySize <= 0 {
		opts.MaxKeySize = DefaultMaxKeySize
	}
	if opts.MaxValueSize <= 0 {
		opts.MaxValueSize = DefaultMaxValueSize
	}
	return &Engine{tables: make(map[string]*Table), opts: opts}, nil
}

// Order returns the tree order used for new tables.
func (e *Engine) Order() int { return e.opts.Order }

func validateName(name string) error {
	if name == "" || len(name) > maxTableNameLen {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	for _, r := range name {
		ok := r == '_' || r == '-' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("%w: %q (allowed: letters, digits, '_', '-', '.')", ErrInvalidName, name)
		}
	}
	return nil
}

// CreateTable adds a new, empty table.
func (e *Engine) CreateTable(name string) (*Table, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tables[name]; ok {
		return nil, fmt.Errorf("%w: %s", ErrTableExists, name)
	}
	t := newTable(name, btree.MustNew(e.opts.Order), e.opts)
	e.tables[name] = t
	return t, nil
}

// DropTable removes a table. Callers that still hold the *Table can keep
// using it, but it is no longer reachable through the engine.
func (e *Engine) DropTable(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tables[name]; !ok {
		return fmt.Errorf("%w: %s", ErrTableNotFound, name)
	}
	delete(e.tables, name)
	return nil
}

// Table looks up a table by name.
func (e *Engine) Table(name string) (*Table, error) {
	e.mu.RLock()
	t, ok := e.tables[name]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotFound, name)
	}
	return t, nil
}

// ListTables returns the table names in sorted order.
func (e *Engine) ListTables() []string {
	e.mu.RLock()
	names := make([]string, 0, len(e.tables))
	for name := range e.tables {
		names = append(names, name)
	}
	e.mu.RUnlock()
	sort.Strings(names)
	return names
}
