package hierarchy

import "context"

type Repository interface {
	ReplaceHierarchy(ctx context.Context, rootID int64, nodes []FlatNode) error
	GetSubtree(ctx context.Context, nodeID int64) ([]FlatNode, error)
}
