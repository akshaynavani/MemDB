// Package btree implements an in-memory B+ tree keyed by byte slices.
//
// Keys are ordered with bytes.Compare. All key/value pairs live in the leaves,
// which are chained left to right so that range scans are a single descent
// followed by a linear walk. Internal nodes only carry separator keys.
//
// A Tree is not safe for concurrent use; callers (see package engine) are
// expected to provide their own synchronisation.
package btree

import (
	"bytes"
	"fmt"
)

// MinOrder is the smallest supported order.
const MinOrder = 3

// DefaultOrder is used when New is given an order of zero.
const DefaultOrder = 64

// Tree is a B+ tree of order m: internal nodes have at most m children and
// every node holds at most m-1 keys. Non-root nodes hold at least
// ceil(m/2)-1 keys.
type Tree struct {
	root    *node
	order   int
	maxKeys int
	minKeys int
	size    int
	height  int
}

// New returns an empty tree with the given order. An order of zero selects
// DefaultOrder; any other value below MinOrder is an error.
func New(order int) (*Tree, error) {
	if order == 0 {
		order = DefaultOrder
	}
	if order < MinOrder {
		return nil, fmt.Errorf("btree: order %d is below minimum %d", order, MinOrder)
	}
	return &Tree{
		root:    newLeaf(),
		order:   order,
		maxKeys: order - 1,
		minKeys: (order+1)/2 - 1,
		height:  1,
	}, nil
}

// MustNew is like New but panics on an invalid order.
func MustNew(order int) *Tree {
	t, err := New(order)
	if err != nil {
		panic(err)
	}
	return t
}

// Len returns the number of keys stored in the tree.
func (t *Tree) Len() int { return t.size }

// Height returns the number of levels in the tree; an empty tree has height 1.
func (t *Tree) Height() int { return t.height }

// Order returns the tree's branching factor.
func (t *Tree) Order() int { return t.order }

// findLeaf descends from the root to the leaf that would contain key.
func (t *Tree) findLeaf(key []byte) *node {
	n := t.root
	for !n.leaf {
		n = n.children[n.childIndex(key)]
	}
	return n
}

// Get returns the value stored under key. The returned slice is owned by the
// tree and must not be modified.
func (t *Tree) Get(key []byte) ([]byte, bool) {
	leaf := t.findLeaf(key)
	i := leaf.lowerBound(key)
	if i < len(leaf.keys) && bytes.Equal(leaf.keys[i], key) {
		return leaf.values[i], true
	}
	return nil, false
}

// Has reports whether key is present.
func (t *Tree) Has(key []byte) bool {
	_, ok := t.Get(key)
	return ok
}

// Put stores value under key, replacing any existing value. Both key and
// value are copied. It reports whether an existing value was replaced.
func (t *Tree) Put(key, value []byte) (replaced bool) {
	sep, right, replaced := t.insert(t.root, key, value)
	if right != nil {
		t.root = &node{
			keys:     [][]byte{sep},
			children: []*node{t.root, right},
		}
		t.height++
	}
	if !replaced {
		t.size++
	}
	return replaced
}

// insert adds key/value below n. If n overflows it is split, and insert
// returns the separator key and the new right sibling for the caller to link
// into the parent.
func (t *Tree) insert(n *node, key, value []byte) (sep []byte, right *node, replaced bool) {
	if n.leaf {
		i := n.lowerBound(key)
		if i < len(n.keys) && bytes.Equal(n.keys[i], key) {
			n.values[i] = cloneBytes(value)
			return nil, nil, true
		}
		n.keys = insertAt(n.keys, i, cloneBytes(key))
		n.values = insertAt(n.values, i, cloneBytes(value))
		if len(n.keys) > t.maxKeys {
			sep, right = t.splitLeaf(n)
		}
		return sep, right, false
	}

	i := n.childIndex(key)
	childSep, childRight, replaced := t.insert(n.children[i], key, value)
	if childRight != nil {
		n.keys = insertAt(n.keys, i, childSep)
		n.children = insertAt(n.children, i+1, childRight)
		if len(n.keys) > t.maxKeys {
			sep, right = t.splitInternal(n)
		}
	}
	return sep, right, replaced
}

// splitLeaf moves the upper half of an overflowing leaf into a new right
// sibling. The separator is the first key of the new leaf.
func (t *Tree) splitLeaf(n *node) ([]byte, *node) {
	mid := len(n.keys) / 2
	right := &node{
		leaf:   true,
		keys:   append([][]byte(nil), n.keys[mid:]...),
		values: append([][]byte(nil), n.values[mid:]...),
		next:   n.next,
	}
	n.keys = truncate(n.keys, mid)
	n.values = truncate(n.values, mid)
	n.next = right
	return right.keys[0], right
}

// splitInternal moves the upper half of an overflowing internal node into a
// new sibling. The middle key moves up to the parent rather than being copied.
func (t *Tree) splitInternal(n *node) ([]byte, *node) {
	mid := len(n.keys) / 2
	sep := n.keys[mid]
	right := &node{
		keys:     append([][]byte(nil), n.keys[mid+1:]...),
		children: append([]*node(nil), n.children[mid+1:]...),
	}
	n.keys = truncate(n.keys, mid)
	n.children = truncate(n.children, mid+1)
	return sep, right
}
