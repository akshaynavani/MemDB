package btree

import "bytes"

// Delete removes key from the tree and reports whether it was present.
//
// When a node drops below the minimum fill it first tries to borrow a key
// from an adjacent sibling with keys to spare and otherwise merges with that
// sibling, which may cascade up the tree. If the root ends up as an internal
// node with a single child, the tree loses a level.
func (t *Tree) Delete(key []byte) bool {
	if !t.remove(t.root, key) {
		return false
	}
	t.size--
	if !t.root.leaf && len(t.root.keys) == 0 {
		t.root = t.root.children[0]
		t.height--
	}
	return true
}

func (t *Tree) remove(n *node, key []byte) bool {
	if n.leaf {
		i := n.lowerBound(key)
		if i >= len(n.keys) || !bytes.Equal(n.keys[i], key) {
			return false
		}
		n.keys = removeAt(n.keys, i)
		n.values = removeAt(n.values, i)
		return true
	}

	i := n.childIndex(key)
	if !t.remove(n.children[i], key) {
		return false
	}
	if len(n.children[i].keys) < t.minKeys {
		t.rebalance(n, i)
	}
	return true
}

// rebalance fixes an underflowing child at parent.children[i].
func (t *Tree) rebalance(parent *node, i int) {
	child := parent.children[i]
	var left, right *node
	if i > 0 {
		left = parent.children[i-1]
	}
	if i+1 < len(parent.children) {
		right = parent.children[i+1]
	}

	switch {
	case left != nil && len(left.keys) > t.minKeys:
		t.borrowFromLeft(parent, i, left, child)
	case right != nil && len(right.keys) > t.minKeys:
		t.borrowFromRight(parent, i, child, right)
	case left != nil:
		t.merge(parent, i-1, left, child)
	case right != nil:
		t.merge(parent, i, child, right)
	}
}

// borrowFromLeft moves the last entry of left into the front of child.
func (t *Tree) borrowFromLeft(parent *node, i int, left, child *node) {
	last := len(left.keys) - 1
	if child.leaf {
		child.keys = insertAt(child.keys, 0, left.keys[last])
		child.values = insertAt(child.values, 0, left.values[last])
		left.keys = removeAt(left.keys, last)
		left.values = removeAt(left.values, last)
		parent.keys[i-1] = child.keys[0]
		return
	}
	// Rotate through the parent: the separator comes down into child and
	// left's last key goes up to replace it.
	child.keys = insertAt(child.keys, 0, parent.keys[i-1])
	child.children = insertAt(child.children, 0, left.children[len(left.children)-1])
	parent.keys[i-1] = left.keys[last]
	left.keys = removeAt(left.keys, last)
	left.children = removeAt(left.children, len(left.children)-1)
}

// borrowFromRight moves the first entry of right onto the end of child.
func (t *Tree) borrowFromRight(parent *node, i int, child, right *node) {
	if child.leaf {
		child.keys = append(child.keys, right.keys[0])
		child.values = append(child.values, right.values[0])
		right.keys = removeAt(right.keys, 0)
		right.values = removeAt(right.values, 0)
		parent.keys[i] = right.keys[0]
		return
	}
	child.keys = append(child.keys, parent.keys[i])
	child.children = append(child.children, right.children[0])
	parent.keys[i] = right.keys[0]
	right.keys = removeAt(right.keys, 0)
	right.children = removeAt(right.children, 0)
}

// merge folds parent.children[sep+1] (right) into parent.children[sep]
// (left) and removes the separator between them from the parent.
func (t *Tree) merge(parent *node, sep int, left, right *node) {
	if left.leaf {
		left.keys = append(left.keys, right.keys...)
		left.values = append(left.values, right.values...)
		left.next = right.next
	} else {
		left.keys = append(left.keys, parent.keys[sep])
		left.keys = append(left.keys, right.keys...)
		left.children = append(left.children, right.children...)
	}
	parent.keys = removeAt(parent.keys, sep)
	parent.children = removeAt(parent.children, sep+1)
}
