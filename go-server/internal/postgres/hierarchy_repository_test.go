package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"go-server/internal/hierarchy"

	_ "github.com/lib/pq"
)

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("database ping error = %v", err)
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		DROP TABLE IF EXISTS hierarchy_nodes CASCADE;
		DROP TABLE IF EXISTS hierarchies CASCADE;
	`); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}

	migrationPath := filepath.Join("..", "..", "..", "postgres", "migrations", "000001_create_hierarchy.up.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration %s: %v", migrationPath, err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	return db
}

func mustFlatten(t *testing.T, node *hierarchy.Node) []hierarchy.FlatNode {
	t.Helper()
	rows, err := hierarchy.Flatten(node, hierarchy.DefaultLimits())
	if err != nil {
		t.Fatalf("Flatten() error = %v", err)
	}
	return rows
}

func TestValidateFlatSnapshotRejectsMalformedRows(t *testing.T) {
	parentOne := int64(1)
	testCases := []struct {
		name   string
		rootID int64
		nodes  []hierarchy.FlatNode
	}{
		{name: "non-positive root", rootID: 0, nodes: []hierarchy.FlatNode{{ID: 1, RootID: 1}}},
		{name: "empty snapshot", rootID: 1},
		{
			name:   "row belongs to another root",
			rootID: 1,
			nodes:  []hierarchy.FlatNode{{ID: 1, RootID: 2}},
		},
		{
			name:   "parentless row is not expected root",
			rootID: 1,
			nodes:  []hierarchy.FlatNode{{ID: 2, RootID: 1}},
		},
		{
			name:   "multiple parentless rows",
			rootID: 1,
			nodes: []hierarchy.FlatNode{
				{ID: 1, RootID: 1},
				{ID: 1, RootID: 1},
			},
		},
		{
			name:   "no parentless row",
			rootID: 1,
			nodes:  []hierarchy.FlatNode{{ID: 2, RootID: 1, ParentID: &parentOne}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateFlatSnapshot(testCase.rootID, testCase.nodes)
			if !errors.Is(err, hierarchy.ErrInvalidHierarchy) {
				t.Fatalf("validateFlatSnapshot() error = %v, want ErrInvalidHierarchy", err)
			}
		})
	}
}

func TestReplaceHierarchyReconcilesAndPreservesOtherRoots(t *testing.T) {
	db := openTestDatabase(t)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	initial := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 2, Type: "subscription", Children: []*hierarchy.Node{}},
			{
				ID:   3,
				Type: "subscription",
				Children: []*hierarchy.Node{
					{ID: 4, Type: "resource_group", Children: []*hierarchy.Node{}},
				},
			},
		},
	}
	if err := repository.ReplaceHierarchy(ctx, 1, mustFlatten(t, initial)); err != nil {
		t.Fatalf("ReplaceHierarchy(initial) error = %v", err)
	}

	otherRoot := &hierarchy.Node{
		ID:   100,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 101, Type: "subscription", Children: []*hierarchy.Node{}},
		},
	}
	if err := repository.ReplaceHierarchy(ctx, 100, mustFlatten(t, otherRoot)); err != nil {
		t.Fatalf("ReplaceHierarchy(other root) error = %v", err)
	}

	updated := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 3, Type: "subscription", Children: []*hierarchy.Node{}},
			{
				ID:   4,
				Type: "resource_group",
				Children: []*hierarchy.Node{
					{ID: 5, Type: "resource_group", Children: []*hierarchy.Node{}},
				},
			},
		},
	}
	if err := repository.ReplaceHierarchy(ctx, 1, mustFlatten(t, updated)); err != nil {
		t.Fatalf("ReplaceHierarchy(updated) error = %v", err)
	}

	rows, err := repository.GetSubtree(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubtree(1) error = %v", err)
	}
	got, err := hierarchy.BuildTree(1, rows)
	if err != nil {
		t.Fatalf("BuildTree(1) error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("updated hierarchy = %#v, want %#v", got, updated)
	}

	if _, err := repository.GetSubtree(ctx, 2); !errors.Is(err, hierarchy.ErrNotFound) {
		t.Fatalf("GetSubtree(removed node) error = %v, want ErrNotFound", err)
	}

	otherRows, err := repository.GetSubtree(ctx, 100)
	if err != nil {
		t.Fatalf("GetSubtree(other root) error = %v", err)
	}
	gotOther, err := hierarchy.BuildTree(100, otherRows)
	if err != nil {
		t.Fatalf("BuildTree(other root) error = %v", err)
	}
	if !reflect.DeepEqual(gotOther, otherRoot) {
		t.Fatalf("other hierarchy changed: got %#v, want %#v", gotOther, otherRoot)
	}
}

func TestReplaceHierarchyRejectsNodeOwnedByAnotherRoot(t *testing.T) {
	db := openTestDatabase(t)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	first := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 2, Type: "subscription", Children: []*hierarchy.Node{}},
		},
	}
	if err := repository.ReplaceHierarchy(ctx, 1, mustFlatten(t, first)); err != nil {
		t.Fatalf("ReplaceHierarchy(first) error = %v", err)
	}

	conflicting := &hierarchy.Node{
		ID:   10,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 2, Type: "resource_group", Children: []*hierarchy.Node{}},
		},
	}
	err := repository.ReplaceHierarchy(ctx, 10, mustFlatten(t, conflicting))

	if !errors.Is(err, hierarchy.ErrNodeConflict) {
		t.Fatalf("ReplaceHierarchy(conflicting) error = %v, want ErrNodeConflict", err)
	}
	if _, err := repository.GetSubtree(ctx, 10); !errors.Is(err, hierarchy.ErrNotFound) {
		t.Fatalf("conflicting root was partially stored, error = %v", err)
	}
}

func TestConcurrentDifferentRootsReportSharedNodeConflict(t *testing.T) {
	db := openTestDatabase(t)
	db.SetMaxOpenConns(4)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		CREATE OR REPLACE FUNCTION delay_shared_node_insert() RETURNS trigger AS $$
		BEGIN
			IF NEW.id = 999 THEN
				PERFORM pg_sleep(0.1);
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;

		CREATE TRIGGER delay_shared_node_insert
		BEFORE INSERT ON hierarchy_nodes
		FOR EACH ROW EXECUTE FUNCTION delay_shared_node_insert();
	`); err != nil {
		t.Fatalf("create concurrent insert test trigger: %v", err)
	}

	first := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 999, Type: "subscription", Children: []*hierarchy.Node{}},
		},
	}
	second := &hierarchy.Node{
		ID:   10,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 999, Type: "resource_group", Children: []*hierarchy.Node{}},
		},
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, request := range []struct {
		rootID int64
		nodes  []hierarchy.FlatNode
	}{
		{rootID: first.ID, nodes: mustFlatten(t, first)},
		{rootID: second.ID, nodes: mustFlatten(t, second)},
	} {
		waitGroup.Add(1)
		go func(rootID int64, nodes []hierarchy.FlatNode) {
			defer waitGroup.Done()
			<-start
			errs <- repository.ReplaceHierarchy(ctx, rootID, nodes)
		}(request.rootID, request.nodes)
	}
	close(start)
	waitGroup.Wait()
	close(errs)

	var successCount, conflictCount int
	for err := range errs {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, hierarchy.ErrNodeConflict):
			conflictCount++
		default:
			t.Fatalf("concurrent ReplaceHierarchy() unexpected error = %v", err)
		}
	}
	if successCount != 1 || conflictCount != 1 {
		t.Fatalf(
			"concurrent results: successes = %d, conflicts = %d; want one of each",
			successCount,
			conflictCount,
		)
	}

	var storedRoots int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hierarchies`).Scan(&storedRoots); err != nil {
		t.Fatalf("count stored roots: %v", err)
	}
	if storedRoots != 1 {
		t.Fatalf("stored roots = %d, want 1", storedRoots)
	}
}

func TestConcurrentDifferentRootsAcquireSharedIDsInStableOrder(t *testing.T) {
	db := openTestDatabase(t)
	db.SetMaxOpenConns(4)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		CREATE OR REPLACE FUNCTION delay_shared_node_insert() RETURNS trigger AS $$
		BEGIN
			IF NEW.id IN (998, 999) THEN
				PERFORM pg_sleep(0.05);
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;

		CREATE TRIGGER delay_shared_node_insert
		BEFORE INSERT ON hierarchy_nodes
		FOR EACH ROW EXECUTE FUNCTION delay_shared_node_insert();
	`); err != nil {
		t.Fatalf("create concurrent insert test trigger: %v", err)
	}

	first := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 998, Type: "subscription", Children: []*hierarchy.Node{}},
			{ID: 999, Type: "resource_group", Children: []*hierarchy.Node{}},
		},
	}
	second := &hierarchy.Node{
		ID:   10,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 999, Type: "subscription", Children: []*hierarchy.Node{}},
			{ID: 998, Type: "resource_group", Children: []*hierarchy.Node{}},
		},
	}

	for iteration := 0; iteration < 5; iteration++ {
		if _, err := db.ExecContext(ctx, `
			TRUNCATE hierarchy_nodes, hierarchies CASCADE
		`); err != nil {
			t.Fatalf("reset iteration %d: %v", iteration, err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		var waitGroup sync.WaitGroup
		for _, request := range []struct {
			rootID int64
			nodes  []hierarchy.FlatNode
		}{
			{rootID: first.ID, nodes: mustFlatten(t, first)},
			{rootID: second.ID, nodes: mustFlatten(t, second)},
		} {
			waitGroup.Add(1)
			go func(rootID int64, nodes []hierarchy.FlatNode) {
				defer waitGroup.Done()
				<-start
				errs <- repository.ReplaceHierarchy(ctx, rootID, nodes)
			}(request.rootID, request.nodes)
		}
		close(start)
		waitGroup.Wait()
		close(errs)

		var successCount, conflictCount int
		for err := range errs {
			switch {
			case err == nil:
				successCount++
			case errors.Is(err, hierarchy.ErrNodeConflict):
				conflictCount++
			default:
				t.Fatalf("iteration %d unexpected error = %v", iteration, err)
			}
		}
		if successCount != 1 || conflictCount != 1 {
			t.Fatalf(
				"iteration %d results: successes = %d, conflicts = %d; want one of each",
				iteration,
				successCount,
				conflictCount,
			)
		}
	}
}

func TestReplaceHierarchyDoesNotRewriteIdenticalNodes(t *testing.T) {
	db := openTestDatabase(t)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	root := &hierarchy.Node{ID: 1, Type: "management_group", Children: []*hierarchy.Node{}}
	rows := mustFlatten(t, root)
	if err := repository.ReplaceHierarchy(ctx, 1, rows); err != nil {
		t.Fatalf("ReplaceHierarchy(first) error = %v", err)
	}

	var firstUpdatedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM hierarchy_nodes WHERE id = 1`).Scan(&firstUpdatedAt); err != nil {
		t.Fatalf("read first updated_at: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT pg_sleep(0.01)`); err != nil {
		t.Fatalf("pg_sleep: %v", err)
	}

	if err := repository.ReplaceHierarchy(ctx, 1, rows); err != nil {
		t.Fatalf("ReplaceHierarchy(second) error = %v", err)
	}

	var secondUpdatedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM hierarchy_nodes WHERE id = 1`).Scan(&secondUpdatedAt); err != nil {
		t.Fatalf("read second updated_at: %v", err)
	}
	if !firstUpdatedAt.Equal(secondUpdatedAt) {
		t.Fatalf("identical snapshot rewrote node: first %v, second %v", firstUpdatedAt, secondUpdatedAt)
	}
}

func TestGetSubtreeReturnsInternalNodeAndOrderedChildren(t *testing.T) {
	db := openTestDatabase(t)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	root := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{
				ID:   3,
				Type: "subscription",
				Children: []*hierarchy.Node{
					{ID: 8, Type: "resource_group", Children: []*hierarchy.Node{}},
					{ID: 4, Type: "resource_group", Children: []*hierarchy.Node{}},
				},
			},
		},
	}
	if err := repository.ReplaceHierarchy(ctx, 1, mustFlatten(t, root)); err != nil {
		t.Fatalf("ReplaceHierarchy() error = %v", err)
	}

	rows, err := repository.GetSubtree(ctx, 3)
	if err != nil {
		t.Fatalf("GetSubtree(3) error = %v", err)
	}
	got, err := hierarchy.BuildTree(3, rows)
	if err != nil {
		t.Fatalf("BuildTree(3) error = %v", err)
	}

	want := root.Children[0]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("internal subtree = %#v, want %#v", got, want)
	}
}

func TestConcurrentReplacementsProduceOneCompleteSnapshot(t *testing.T) {
	db := openTestDatabase(t)
	db.SetMaxOpenConns(4)
	repository := NewHierarchyRepository(db, time.Second)
	ctx := context.Background()

	first := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 2, Type: "subscription", Children: []*hierarchy.Node{}},
		},
	}
	second := &hierarchy.Node{
		ID:   1,
		Type: "management_group",
		Children: []*hierarchy.Node{
			{ID: 3, Type: "resource_group", Children: []*hierarchy.Node{}},
		},
	}
	firstRows := mustFlatten(t, first)
	secondRows := mustFlatten(t, second)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, snapshot := range [][]hierarchy.FlatNode{firstRows, secondRows} {
		waitGroup.Add(1)
		go func(nodes []hierarchy.FlatNode) {
			defer waitGroup.Done()
			<-start
			errs <- repository.ReplaceHierarchy(ctx, 1, nodes)
		}(snapshot)
	}
	close(start)
	waitGroup.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ReplaceHierarchy() error = %v", err)
		}
	}

	rows, err := repository.GetSubtree(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubtree() error = %v", err)
	}
	got, err := hierarchy.BuildTree(1, rows)
	if err != nil {
		t.Fatalf("BuildTree() error = %v", err)
	}
	if !reflect.DeepEqual(got, first) && !reflect.DeepEqual(got, second) {
		t.Fatalf("concurrent result mixed snapshots: %#v", got)
	}
}

func TestReplaceHierarchyBoundsDatabaseLockWait(t *testing.T) {
	db := openTestDatabase(t)
	db.SetMaxOpenConns(4)
	ctx := context.Background()
	root := &hierarchy.Node{ID: 1, Type: "management_group", Children: []*hierarchy.Node{}}
	nodes := mustFlatten(t, root)

	seedRepository := NewHierarchyRepository(db, time.Second)
	if err := seedRepository.ReplaceHierarchy(ctx, root.ID, nodes); err != nil {
		t.Fatalf("seed hierarchy: %v", err)
	}

	lockingTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin locking transaction: %v", err)
	}
	defer func() { _ = lockingTx.Rollback() }()
	if _, err := lockingTx.ExecContext(ctx, `
		SELECT root_id FROM hierarchies WHERE root_id = $1 FOR UPDATE
	`, root.ID); err != nil {
		t.Fatalf("lock hierarchy root: %v", err)
	}

	repository := NewHierarchyRepository(db, 25*time.Millisecond)
	err = repository.ReplaceHierarchy(ctx, root.ID, nodes)
	if !errors.Is(err, hierarchy.ErrUnavailable) {
		t.Fatalf("ReplaceHierarchy() error = %v, want ErrUnavailable", err)
	}
}

func TestSchemaRejectsParentFromAnotherRoot(t *testing.T) {
	db := openTestDatabase(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hierarchies (root_id) VALUES (1), (10);
		INSERT INTO hierarchy_nodes (id, root_id, parent_id, node_type, sibling_position)
		VALUES
			(1, 1, NULL, 'management_group', 0),
			(10, 10, NULL, 'management_group', 0),
			(2, 1, 10, 'subscription', 0);
	`); err != nil {
		t.Fatalf("insert cross-root parent before deferred check: %v", err)
	}

	if err := tx.Commit(); err == nil {
		t.Fatal("Commit() error = nil, want cross-root parent constraint failure")
	}
}
