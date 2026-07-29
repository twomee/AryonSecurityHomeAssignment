package hierarchy

import (
	"context"
	"fmt"
)

type Service struct {
	repository Repository
	limits     Limits
}

// NewService creates the hierarchy use-case layer with the storage dependency
// and safety limits that every write must obey.
func NewService(repository Repository, limits Limits) *Service {
	return &Service{
		repository: repository,
		limits:     limits,
	}
}

// Store is the write-side application flow. It converts the submitted JSON
// tree into validated rows, then asks the repository to atomically replace
// the snapshot for that root.
func (s *Service) Store(ctx context.Context, root *Node) error {
	nodes, err := Flatten(root, s.limits)
	if err != nil {
		return err
	}
	if err := s.repository.ReplaceHierarchy(ctx, root.ID, nodes); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("replace hierarchy %d: %w", root.ID, ctxErr)
		}
		return fmt.Errorf("replace hierarchy %d: %w", root.ID, err)
	}
	return nil
}

// Get is the read-side application flow. It loads the requested subtree as
// flat rows, then rebuilds the recursive JSON shape returned by the API.
func (s *Service) Get(ctx context.Context, nodeID int64) (*Node, error) {
	rows, err := s.repository.GetSubtree(ctx, nodeID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("load hierarchy subtree %d: %w", nodeID, ctxErr)
		}
		return nil, fmt.Errorf("load hierarchy subtree %d: %w", nodeID, err)
	}
	root, err := BuildTree(nodeID, rows)
	if err != nil {
		return nil, fmt.Errorf("build hierarchy subtree %d: %w", nodeID, err)
	}
	return root, nil
}
