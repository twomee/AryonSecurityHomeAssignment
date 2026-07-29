package hierarchy

import (
	"errors"
	"reflect"
	"testing"
)

func TestFlattenPreservesStructureAndSiblingOrder(t *testing.T) {
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "management_group", Children: []*Node{}},
			{
				ID:   3,
				Type: "subscription",
				Children: []*Node{
					{ID: 4, Type: "resource_group", Children: []*Node{}},
				},
			},
		},
	}

	got, err := Flatten(root, Limits{MaxNodes: 10, MaxDepth: 10})
	if err != nil {
		t.Fatalf("Flatten() error = %v", err)
	}

	parentOne := int64(1)
	parentThree := int64(3)
	want := []FlatNode{
		{ID: 1, RootID: 1, ParentID: nil, Type: "management_group", Position: 0},
		{ID: 2, RootID: 1, ParentID: &parentOne, Type: "management_group", Position: 0},
		{ID: 3, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 1},
		{ID: 4, RootID: 1, ParentID: &parentThree, Type: "resource_group", Position: 0},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flatten() = %#v, want %#v", got, want)
	}
}

func TestFlattenProcessesNestedSiblingsInSubmittedOrder(t *testing.T) {
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{
				ID:   2,
				Type: "subscription",
				Children: []*Node{
					{ID: 4, Type: "resource_group", Children: []*Node{}},
					{ID: 5, Type: "resource_group", Children: []*Node{}},
				},
			},
			{ID: 3, Type: "subscription", Children: []*Node{}},
		},
	}

	rows, err := Flatten(root, DefaultLimits())
	if err != nil {
		t.Fatalf("Flatten() error = %v", err)
	}

	gotIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		gotIDs = append(gotIDs, row.ID)
	}
	wantIDs := []int64{1, 2, 4, 5, 3}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("flattened ids = %v, want %v", gotIDs, wantIDs)
	}
}

func TestFlattenRejectsDuplicateIDs(t *testing.T) {
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "subscription", Children: []*Node{}},
			{ID: 2, Type: "resource_group", Children: []*Node{}},
		},
	}

	_, err := Flatten(root, DefaultLimits())

	if !errors.Is(err, ErrInvalidHierarchy) {
		t.Fatalf("Flatten() error = %v, want ErrInvalidHierarchy", err)
	}
}

func TestFlattenRejectsMissingChildren(t *testing.T) {
	root := &Node{ID: 1, Type: "management_group", Children: nil}

	_, err := Flatten(root, DefaultLimits())

	if !errors.Is(err, ErrInvalidHierarchy) {
		t.Fatalf("Flatten() error = %v, want ErrInvalidHierarchy", err)
	}
}

func TestFlattenRejectsExcessiveDepth(t *testing.T) {
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{
				ID:   2,
				Type: "subscription",
				Children: []*Node{
					{ID: 3, Type: "resource_group", Children: []*Node{}},
				},
			},
		},
	}

	_, err := Flatten(root, Limits{MaxNodes: 10, MaxDepth: 2})

	if !errors.Is(err, ErrHierarchyTooDeep) {
		t.Fatalf("Flatten() error = %v, want ErrHierarchyTooDeep", err)
	}
}

func TestFlattenRejectsExcessiveNodeCount(t *testing.T) {
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "subscription", Children: []*Node{}},
		},
	}

	_, err := Flatten(root, Limits{MaxNodes: 1, MaxDepth: 10})

	if !errors.Is(err, ErrHierarchyTooLarge) {
		t.Fatalf("Flatten() error = %v, want ErrHierarchyTooLarge", err)
	}
}

func TestBuildTreeRestoresOrderedHierarchy(t *testing.T) {
	parentOne := int64(1)
	parentThree := int64(3)
	rows := []FlatNode{
		{ID: 4, RootID: 1, ParentID: &parentThree, Type: "resource_group", Position: 0},
		{ID: 3, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 1},
		{ID: 2, RootID: 1, ParentID: &parentOne, Type: "management_group", Position: 0},
		{ID: 1, RootID: 1, ParentID: nil, Type: "management_group", Position: 0},
	}

	got, err := BuildTree(1, rows)
	if err != nil {
		t.Fatalf("BuildTree() error = %v", err)
	}

	want := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "management_group", Children: []*Node{}},
			{
				ID:   3,
				Type: "subscription",
				Children: []*Node{
					{ID: 4, Type: "resource_group", Children: []*Node{}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildTree() = %#v, want %#v", got, want)
	}
}

func TestBuildTreeSupportsInternalSubtreeRoot(t *testing.T) {
	parentOne := int64(1)
	parentThree := int64(3)
	rows := []FlatNode{
		{ID: 3, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 1},
		{ID: 4, RootID: 1, ParentID: &parentThree, Type: "resource_group", Position: 0},
	}

	got, err := BuildTree(3, rows)
	if err != nil {
		t.Fatalf("BuildTree() error = %v", err)
	}

	if got.ID != 3 || len(got.Children) != 1 || got.Children[0].ID != 4 {
		t.Fatalf("BuildTree() returned unexpected subtree: %#v", got)
	}
}

func TestBuildTreeRejectsDisconnectedRows(t *testing.T) {
	parentOne := int64(1)
	missingParent := int64(99)
	rows := []FlatNode{
		{ID: 1, RootID: 1, ParentID: nil, Type: "management_group", Position: 0},
		{ID: 2, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 0},
		{ID: 3, RootID: 1, ParentID: &missingParent, Type: "resource_group", Position: 0},
		{ID: 99, RootID: 1, ParentID: &missingParent, Type: "subscription", Position: 1},
	}

	_, err := BuildTree(1, rows)
	if !errors.Is(err, ErrCorruptHierarchy) {
		t.Fatalf("BuildTree() error = %v, want ErrCorruptHierarchy", err)
	}
}

func TestBuildTreeRejectsDuplicateRows(t *testing.T) {
	rows := []FlatNode{
		{ID: 1, RootID: 1, Type: "management_group", Position: 0},
		{ID: 1, RootID: 1, Type: "management_group", Position: 0},
	}

	_, err := BuildTree(1, rows)
	if !errors.Is(err, ErrCorruptHierarchy) {
		t.Fatalf("BuildTree() error = %v, want ErrCorruptHierarchy", err)
	}
}

func TestBuildTreeRejectsCycle(t *testing.T) {
	parentTwo := int64(2)
	parentOne := int64(1)
	rows := []FlatNode{
		{ID: 1, RootID: 1, ParentID: &parentTwo, Type: "subscription", Position: 0},
		{ID: 2, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 0},
	}

	_, err := BuildTree(1, rows)

	if !errors.Is(err, ErrCorruptHierarchy) {
		t.Fatalf("BuildTree() error = %v, want ErrCorruptHierarchy", err)
	}
}
