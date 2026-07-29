package hierarchy

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidHierarchy  = errors.New("invalid hierarchy")
	ErrHierarchyTooDeep  = errors.New("hierarchy exceeds maximum depth")
	ErrHierarchyTooLarge = errors.New("hierarchy exceeds maximum node count")
	ErrCorruptHierarchy  = errors.New("stored hierarchy is corrupt")
	ErrNotFound          = errors.New("hierarchy node not found")
	ErrNodeConflict      = errors.New("node belongs to another hierarchy")
	ErrUnavailable       = errors.New("hierarchy store is temporarily unavailable")
)

type Node struct {
	ID       int64   `json:"id"`
	Type     string  `json:"type"`
	Children []*Node `json:"children"`
}

type FlatNode struct {
	ID       int64
	RootID   int64
	ParentID *int64
	Type     string
	Position int
}

type Limits struct {
	MaxNodes int
	MaxDepth int
}

func DefaultLimits() Limits {
	return Limits{
		MaxNodes: 100_000,
		MaxDepth: 1_000,
	}
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidHierarchy, fmt.Sprintf(format, args...))
}
