package btree

import (
	"bytes"
	"sort"
)

// node is a single B+ tree node.
//
// Internal nodes hold len(keys) separator keys and len(keys)+1 children. Every
// key stored under children[i] satisfies keys[i-1] <= k < keys[i].
//
// Leaf nodes hold parallel keys/values slices and a pointer to the next leaf
// in key order, which is what makes range scans cheap.
type node struct {
	leaf     bool
	keys     [][]byte
	values   [][]byte // leaves only
	children []*node  // internal nodes only
	next     *node    // leaves only: right sibling
}

func newLeaf() *node {
	return &node{leaf: true}
}

// lowerBound returns the index of the first key >= key.
func (n *node) lowerBound(key []byte) int {
	return sort.Search(len(n.keys), func(i int) bool {
		return bytes.Compare(n.keys[i], key) >= 0
	})
}

// childIndex returns the index of the child that may contain key: the number
// of separator keys that are <= key.
func (n *node) childIndex(key []byte) int {
	return sort.Search(len(n.keys), func(i int) bool {
		return bytes.Compare(n.keys[i], key) > 0
	})
}

// insertAt inserts v at position i, shifting later elements right.
func insertAt[T any](s []T, i int, v T) []T {
	var zero T
	s = append(s, zero)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// removeAt removes the element at position i and clears the vacated slot so
// the garbage collector can reclaim whatever it pointed to.
func removeAt[T any](s []T, i int) []T {
	var zero T
	copy(s[i:], s[i+1:])
	s[len(s)-1] = zero
	return s[:len(s)-1]
}

// truncate shortens s to n elements, clearing the tail.
func truncate[T any](s []T, n int) []T {
	var zero T
	for i := n; i < len(s); i++ {
		s[i] = zero
	}
	return s[:n]
}

// cloneBytes returns a private copy of b. A nil input yields an empty,
// non-nil slice so stored values are never nil.
func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
