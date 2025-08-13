package btree

import "bytes"

// Ascend calls fn for every key in [start, end) in ascending order. A nil
// start begins at the smallest key; a nil end scans to the last key. The
// scan stops early when fn returns false.
//
// The key and value slices passed to fn are owned by the tree and are only
// valid until the tree is next modified; copy them if they must outlive that.
func (t *Tree) Ascend(start, end []byte, fn func(key, value []byte) bool) {
	var leaf *node
	var i int
	if start == nil {
		leaf = t.leftmostLeaf()
	} else {
		leaf = t.findLeaf(start)
		i = leaf.lowerBound(start)
	}

	for leaf != nil {
		for ; i < len(leaf.keys); i++ {
			if end != nil && bytes.Compare(leaf.keys[i], end) >= 0 {
				return
			}
			if !fn(leaf.keys[i], leaf.values[i]) {
				return
			}
		}
		leaf = leaf.next
		i = 0
	}
}

// AscendAll calls fn for every key in the tree in ascending order.
func (t *Tree) AscendAll(fn func(key, value []byte) bool) {
	t.Ascend(nil, nil, fn)
}

// Min returns the smallest key and its value.
func (t *Tree) Min() (key, value []byte, ok bool) {
	leaf := t.leftmostLeaf()
	if len(leaf.keys) == 0 {
		return nil, nil, false
	}
	return leaf.keys[0], leaf.values[0], true
}

// Max returns the largest key and its value.
func (t *Tree) Max() (key, value []byte, ok bool) {
	n := t.root
	for !n.leaf {
		n = n.children[len(n.children)-1]
	}
	if len(n.keys) == 0 {
		return nil, nil, false
	}
	last := len(n.keys) - 1
	return n.keys[last], n.values[last], true
}

func (t *Tree) leftmostLeaf() *node {
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	return n
}
