package hierarchy

import (
	"fmt"
	"sort"
)

type traversalState uint8

const (
	nodeUnvisited traversalState = iota
	nodeVisiting
	nodeBuilt
)

type treeBuilder struct {
	rowsByID map[int64]FlatNode
	children map[int64][]FlatNode
	state    map[int64]traversalState
}

// build materializes one node and its descendants from the indexed rows. Its
// traversal state turns cycles or repeated paths into corruption errors.
func (b *treeBuilder) build(id int64) (*Node, error) {
	switch b.state[id] {
	case nodeVisiting:
		return nil, fmt.Errorf("%w: cycle at node %d", ErrCorruptHierarchy, id)
	case nodeBuilt:
		return nil, fmt.Errorf("%w: node %d has multiple paths", ErrCorruptHierarchy, id)
	}

	// A node seen again while it is still being built points back into the
	// current ancestor chain, which is a cycle.
	b.state[id] = nodeVisiting

	row := b.rowsByID[id]
	node := &Node{
		ID:       row.ID,
		Type:     row.Type,
		Children: make([]*Node, 0, len(b.children[id])),
	}
	for _, childRow := range b.children[id] {
		child, err := b.build(childRow.ID)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}

	b.state[id] = nodeBuilt
	return node, nil
}

// indexRows creates constant-time lookup by node ID while rejecting row-level
// corruption before relationships are connected.
func indexRows(rows []FlatNode) (map[int64]FlatNode, error) {
	rowsByID := make(map[int64]FlatNode, len(rows))
	for _, row := range rows {
		if _, exists := rowsByID[row.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate node %d", ErrCorruptHierarchy, row.ID)
		}
		if row.Position < 0 {
			return nil, fmt.Errorf("%w: negative position for node %d", ErrCorruptHierarchy, row.ID)
		}
		rowsByID[row.ID] = row
	}
	return rowsByID, nil
}

// groupChildrenByParent validates root and parent-link relationships, groups
// children under their parent, and restores the submitted sibling order.
func groupChildrenByParent(
	requestedID int64,
	rootRow FlatNode,
	rows []FlatNode,
	rowsByID map[int64]FlatNode,
) (map[int64][]FlatNode, error) {
	if rootRow.ParentID != nil {
		// An internal subtree root can reference a parent outside this result. If the
		// parent is also present, the query returned an ancestor that does not belong
		// in the requested subtree.
		if _, parentReturned := rowsByID[*rootRow.ParentID]; parentReturned {
			return nil, fmt.Errorf("%w: requested subtree root has an internal parent", ErrCorruptHierarchy)
		}
	}

	children := make(map[int64][]FlatNode)
	for _, row := range rows {
		if row.RootID != rootRow.RootID {
			return nil, fmt.Errorf("%w: multiple roots returned", ErrCorruptHierarchy)
		}
		if row.ID == requestedID {
			continue
		}
		if row.ParentID == nil {
			return nil, fmt.Errorf("%w: node %d has no parent", ErrCorruptHierarchy, row.ID)
		}
		if _, parentExists := rowsByID[*row.ParentID]; !parentExists {
			return nil, fmt.Errorf("%w: missing parent %d", ErrCorruptHierarchy, *row.ParentID)
		}
		children[*row.ParentID] = append(children[*row.ParentID], row)
	}

	for parentID := range children {
		sort.Slice(children[parentID], func(i, j int) bool {
			if children[parentID][i].Position == children[parentID][j].Position {
				return children[parentID][i].ID < children[parentID][j].ID
			}
			return children[parentID][i].Position < children[parentID][j].Position
		})
	}
	return children, nil
}

// BuildTree coordinates the read-side transformation from flat database rows
// back into the recursive hierarchy shape expected by API clients.
func BuildTree(requestedID int64, rows []FlatNode) (*Node, error) {
	if len(rows) == 0 {
		return nil, ErrNotFound
	}

	// rowsByID answers "what is this node?", while children below answers
	// "which ordered nodes belong directly under this parent?"
	rowsByID, err := indexRows(rows)
	if err != nil {
		return nil, err
	}
	rootRow, exists := rowsByID[requestedID]
	if !exists {
		return nil, ErrNotFound
	}

	children, err := groupChildrenByParent(requestedID, rootRow, rows, rowsByID)
	if err != nil {
		return nil, err
	}

	builder := treeBuilder{
		rowsByID: rowsByID,
		children: children,
		state:    make(map[int64]traversalState, len(rows)),
	}
	root, err := builder.build(requestedID)
	if err != nil {
		return nil, err
	}

	// Rows not reached from requestedID are disconnected from the requested
	// subtree and indicate corrupt stored relationships.
	if len(builder.state) != len(rows) {
		return nil, fmt.Errorf("%w: disconnected nodes", ErrCorruptHierarchy)
	}
	return root, nil
}
