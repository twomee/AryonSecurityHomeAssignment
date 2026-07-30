package hierarchy

import (
	"fmt"
	"testing"
)

func BenchmarkFlattenWideTree(b *testing.B) {
	const children = 10_000
	root := &Node{
		ID:       1,
		Type:     "management_group",
		Children: make([]*Node, 0, children),
	}
	for index := 0; index < children; index++ {
		root.Children = append(root.Children, &Node{
			ID:       int64(index + 2),
			Type:     "resource_group",
			Children: []*Node{},
		})
	}
	limits := Limits{MaxNodes: children + 1, MaxDepth: 2}

	b.ResetTimer()
	for range b.N {
		if _, err := Flatten(root, limits); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildTreeWideTree(b *testing.B) {
	const children = 10_000
	parentID := int64(1)
	rows := make([]FlatNode, 0, children+1)
	rows = append(rows, FlatNode{
		ID:       1,
		RootID:   1,
		Type:     "management_group",
		Position: 0,
	})
	for index := 0; index < children; index++ {
		rows = append(rows, FlatNode{
			ID:       int64(index + 2),
			RootID:   1,
			ParentID: &parentID,
			Type:     fmt.Sprintf("resource_group_%d", index),
			Position: index,
		})
	}

	b.ResetTimer()
	for range b.N {
		if _, err := BuildTree(1, rows); err != nil {
			b.Fatal(err)
		}
	}
}
