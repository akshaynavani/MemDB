package btree

import (
	"bytes"
	"fmt"
)

// Check walks the whole tree and verifies its structural invariants:
//
//   - keys inside every node are strictly increasing
//   - every key lies within the bounds implied by its ancestors' separators
//   - no node exceeds the maximum fill, and non-root nodes meet the minimum
//   - internal nodes have exactly len(keys)+1 children
//   - all leaves sit at the same depth, which equals Height()
//   - the leaf sibling chain visits every leaf exactly once, in order
//   - the number of stored keys equals Len()
//
// It is intended for tests and debugging; it runs in O(n).
func (t *Tree) Check() error {
	var leaves []*node
	leafDepth := -1
	count := 0

	var walk func(n *node, lo, hi []byte, depth int, isRoot bool) error
	walk = func(n *node, lo, hi []byte, depth int, isRoot bool) error {
		if len(n.keys) > t.maxKeys {
			return fmt.Errorf("node at depth %d has %d keys, max %d", depth, len(n.keys), t.maxKeys)
		}
		if !isRoot && len(n.keys) < t.minKeys {
			return fmt.Errorf("node at depth %d has %d keys, min %d", depth, len(n.keys), t.minKeys)
		}
		for i, k := range n.keys {
			if i > 0 && bytes.Compare(n.keys[i-1], k) >= 0 {
				return fmt.Errorf("keys out of order at depth %d: %q >= %q", depth, n.keys[i-1], k)
			}
			if lo != nil && bytes.Compare(k, lo) < 0 {
				return fmt.Errorf("key %q below lower bound %q", k, lo)
			}
			if hi != nil && bytes.Compare(k, hi) >= 0 {
				return fmt.Errorf("key %q not below upper bound %q", k, hi)
			}
		}

		if n.leaf {
			if len(n.values) != len(n.keys) {
				return fmt.Errorf("leaf has %d keys but %d values", len(n.keys), len(n.values))
			}
			if n.children != nil {
				return fmt.Errorf("leaf has children")
			}
			if leafDepth == -1 {
				leafDepth = depth
			} else if depth != leafDepth {
				return fmt.Errorf("leaf at depth %d, expected %d", depth, leafDepth)
			}
			leaves = append(leaves, n)
			count += len(n.keys)
			return nil
		}

		if len(n.children) != len(n.keys)+1 {
			return fmt.Errorf("internal node has %d keys and %d children", len(n.keys), len(n.children))
		}
		if isRoot && len(n.keys) == 0 {
			return fmt.Errorf("internal root has no keys")
		}
		for i, c := range n.children {
			clo, chi := lo, hi
			if i > 0 {
				clo = n.keys[i-1]
			}
			if i < len(n.keys) {
				chi = n.keys[i]
			}
			if err := walk(c, clo, chi, depth+1, false); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(t.root, nil, nil, 1, true); err != nil {
		return err
	}
	if leafDepth != t.height {
		return fmt.Errorf("leaves at depth %d but height is %d", leafDepth, t.height)
	}
	if count != t.size {
		return fmt.Errorf("found %d keys but size is %d", count, t.size)
	}

	n := t.leftmostLeaf()
	for i, want := range leaves {
		if n != want {
			return fmt.Errorf("leaf chain diverges at leaf %d", i)
		}
		if n.next != nil && len(n.keys) > 0 && len(n.next.keys) > 0 &&
			bytes.Compare(n.keys[len(n.keys)-1], n.next.keys[0]) >= 0 {
			return fmt.Errorf("leaf chain out of order at leaf %d", i)
		}
		n = n.next
	}
	if n != nil {
		return fmt.Errorf("leaf chain has extra nodes past the last leaf")
	}
	return nil
}
