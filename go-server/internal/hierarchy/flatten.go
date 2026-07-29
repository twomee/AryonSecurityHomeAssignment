package hierarchy

import "strings"

type pendingNode struct {
	node     *Node
	parentID *int64
	position int
	depth    int
}

// Flatten validates and converts the recursive API shape into one row per
// node. It uses an explicit stack so accepted deep hierarchies do not consume
// the goroutine call stack.
func Flatten(root *Node, limits Limits) ([]FlatNode, error) {
	if err := validateFlattenInput(root, limits); err != nil {
		return nil, err
	}

	// stack holds nodes still waiting to be visited, flat holds completed
	// database rows, and seen prevents the same node ID from appearing twice.
	stack := []pendingNode{{
		node:     root,
		parentID: nil,
		position: 0,
		depth:    1,
	}}
	seen := make(map[int64]struct{})
	flat := make([]FlatNode, 0)

	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]

		if err := validatePendingNode(current, seen, limits); err != nil {
			return nil, err
		}
		seen[current.node.ID] = struct{}{}

		flat = append(flat, FlatNode{
			ID:       current.node.ID,
			RootID:   root.ID,
			ParentID: current.parentID,
			Type:     current.node.Type,
			Position: current.position,
		})

		stack = pushChildren(stack, current)
	}

	return flat, nil
}

// validateFlattenInput checks the traversal setup before any nodes are
// visited, so invalid roots or safety limits fail without partial work.
func validateFlattenInput(root *Node, limits Limits) error {
	if root == nil {
		return invalid("root is required")
	}
	if limits.MaxNodes <= 0 || limits.MaxDepth <= 0 {
		return invalid("limits must be positive")
	}
	return nil
}

// validatePendingNode protects the tree invariants and resource limits for
// the next node taken from the traversal stack.
func validatePendingNode(current pendingNode, seen map[int64]struct{}, limits Limits) error {
	if current.depth > limits.MaxDepth {
		return ErrHierarchyTooDeep
	}
	if current.node == nil {
		return invalid("children cannot contain null")
	}
	if current.node.ID <= 0 {
		return invalid("node id must be positive")
	}
	if strings.TrimSpace(current.node.Type) == "" {
		return invalid("node %d has an empty type", current.node.ID)
	}
	if current.node.Children == nil {
		return invalid("node %d must include children", current.node.ID)
	}
	if _, exists := seen[current.node.ID]; exists {
		return invalid("duplicate node id %d", current.node.ID)
	}
	if len(seen) >= limits.MaxNodes {
		return ErrHierarchyTooLarge
	}
	return nil
}

// pushChildren schedules the current node's children for depth-first
// processing while carrying their parent, sibling position, and depth.
func pushChildren(stack []pendingNode, current pendingNode) []pendingNode {
	parentID := current.node.ID

	// A stack is last-in-first-out. Pushing children from right to left makes
	// the leftmost submitted child the next node processed.
	for position := len(current.node.Children) - 1; position >= 0; position-- {
		stack = append(stack, pendingNode{
			node:     current.node.Children[position],
			parentID: &parentID,
			position: position,
			depth:    current.depth + 1,
		})
	}
	return stack
}
