package hierarchy

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeRepository struct {
	replaceRootID int64
	replaceNodes  []FlatNode
	replaceErr    error
	subtreeRows   []FlatNode
	subtreeErr    error
}

func (f *fakeRepository) ReplaceHierarchy(_ context.Context, rootID int64, nodes []FlatNode) error {
	f.replaceRootID = rootID
	f.replaceNodes = nodes
	return f.replaceErr
}

func (f *fakeRepository) GetSubtree(_ context.Context, _ int64) ([]FlatNode, error) {
	return f.subtreeRows, f.subtreeErr
}

func TestServiceStoreValidatesAndFlattensBeforeRepository(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, DefaultLimits())
	root := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "subscription", Children: []*Node{}},
		},
	}

	err := service.Store(context.Background(), root)

	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if repository.replaceRootID != 1 {
		t.Fatalf("repository root id = %d, want 1", repository.replaceRootID)
	}
	if len(repository.replaceNodes) != 2 || repository.replaceNodes[1].ParentID == nil {
		t.Fatalf("repository nodes = %#v, want flattened hierarchy", repository.replaceNodes)
	}
}

func TestServiceStoreDoesNotCallRepositoryForInvalidTree(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, DefaultLimits())
	root := &Node{ID: 1, Type: "", Children: []*Node{}}

	err := service.Store(context.Background(), root)

	if !errors.Is(err, ErrInvalidHierarchy) {
		t.Fatalf("Store() error = %v, want ErrInvalidHierarchy", err)
	}
	if repository.replaceRootID != 0 {
		t.Fatal("repository was called for an invalid hierarchy")
	}
}

func TestServiceGetBuildsSubtree(t *testing.T) {
	parentOne := int64(1)
	repository := &fakeRepository{
		subtreeRows: []FlatNode{
			{ID: 1, RootID: 1, Type: "management_group", Position: 0},
			{ID: 2, RootID: 1, ParentID: &parentOne, Type: "subscription", Position: 0},
		},
	}
	service := NewService(repository, DefaultLimits())

	got, err := service.Get(context.Background(), 1)

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	want := &Node{
		ID:   1,
		Type: "management_group",
		Children: []*Node{
			{ID: 2, Type: "subscription", Children: []*Node{}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %#v, want %#v", got, want)
	}
}

func TestServiceGetPropagatesNotFound(t *testing.T) {
	repository := &fakeRepository{subtreeErr: ErrNotFound}
	service := NewService(repository, DefaultLimits())

	_, err := service.Get(context.Background(), 999)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}
