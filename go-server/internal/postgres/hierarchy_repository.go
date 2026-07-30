package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go-server/internal/hierarchy"

	"github.com/lib/pq"
)

type HierarchyRepository struct {
	db          *sql.DB
	lockTimeout time.Duration
}

// NewHierarchyRepository binds hierarchy persistence to a PostgreSQL
// connection pool and the maximum time a replacement may wait for locks.
func NewHierarchyRepository(db *sql.DB, lockTimeout time.Duration) *HierarchyRepository {
	return &HierarchyRepository{db: db, lockTimeout: lockTimeout}
}

// ReplaceHierarchy owns the complete write transaction. It stages the desired
// snapshot, reconciles it with stored rows, and commits only when every
// integrity and ownership check succeeds.
func (r *HierarchyRepository) ReplaceHierarchy(
	ctx context.Context,
	rootID int64,
	nodes []hierarchy.FlatNode,
) (err error) {
	if err := validateFlatSnapshot(rootID, nodes); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin hierarchy transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			err = mapOperationalError(err)
		}
	}()

	if err = configureLockTimeout(ctx, tx, r.lockTimeout); err != nil {
		return err
	}

	if err = ensureAndLockRoot(ctx, tx, rootID); err != nil {
		return err
	}

	if err = createIncomingNodesTable(ctx, tx); err != nil {
		return err
	}

	if err = copyIncomingNodes(ctx, tx, nodes); err != nil {
		return err
	}

	if err = rejectCrossRootConflict(ctx, tx, rootID); err != nil {
		return err
	}

	if err = mergeIncomingNodes(ctx, tx); err != nil {
		return err
	}

	// The merge can wait for a concurrent transaction that claimed the same
	// global ID after our first check. Recheck after that wait so the loser gets
	// ErrNodeConflict and rolls back instead of silently accepting fewer rows.
	if err = rejectCrossRootConflict(ctx, tx, rootID); err != nil {
		return err
	}

	if err = deleteMissingNodes(ctx, tx, rootID); err != nil {
		return err
	}

	if err = incrementHierarchyRevision(ctx, tx, rootID); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit hierarchy transaction: %w", err)
	}
	return nil
}

// mapOperationalError translates PostgreSQL lock contention into the domain
// availability error understood by the service and HTTP layers.
func mapOperationalError(err error) error {
	var postgresError *pq.Error
	if errors.As(err, &postgresError) && postgresError.Code == "55P03" {
		return fmt.Errorf("%w: database lock timeout", hierarchy.ErrUnavailable)
	}
	return err
}

// GetSubtree loads one node and all of its descendants as flat rows in a
// single recursive query; the service later passes those rows to BuildTree.
func (r *HierarchyRepository) GetSubtree(
	ctx context.Context,
	nodeID int64,
) ([]hierarchy.FlatNode, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT
				id,
				root_id,
				parent_id,
				node_type,
				sibling_position,
				ARRAY[id] AS path
			FROM hierarchy_nodes
			WHERE id = $1

			UNION ALL

			SELECT
				child.id,
				child.root_id,
				child.parent_id,
				child.node_type,
				child.sibling_position,
				parent.path || child.id
			FROM hierarchy_nodes AS child
			JOIN subtree AS parent
			  ON child.root_id = parent.root_id
			 AND child.parent_id = parent.id
			WHERE NOT child.id = ANY(parent.path)
		)
		SELECT id, root_id, parent_id, node_type, sibling_position
		FROM subtree
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query hierarchy subtree: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]hierarchy.FlatNode, 0)
	for rows.Next() {
		var node hierarchy.FlatNode
		var parentID sql.NullInt64
		if err := rows.Scan(
			&node.ID,
			&node.RootID,
			&parentID,
			&node.Type,
			&node.Position,
		); err != nil {
			return nil, fmt.Errorf("scan hierarchy subtree: %w", err)
		}
		if parentID.Valid {
			value := parentID.Int64
			node.ParentID = &value
		}
		result = append(result, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hierarchy subtree: %w", err)
	}
	if len(result) == 0 {
		return nil, hierarchy.ErrNotFound
	}
	return result, nil
}

// validateFlatSnapshot protects the repository boundary. The service uses
// Flatten, but repository callers must not be able to begin a transaction
// with rows that describe a different or ambiguous root.
func validateFlatSnapshot(rootID int64, nodes []hierarchy.FlatNode) error {
	if rootID <= 0 {
		return fmt.Errorf("%w: root id must be positive", hierarchy.ErrInvalidHierarchy)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("%w: snapshot is empty", hierarchy.ErrInvalidHierarchy)
	}

	rootCount := 0
	for _, node := range nodes {
		if node.RootID != rootID {
			return fmt.Errorf("%w: node %d has root %d, want %d", hierarchy.ErrInvalidHierarchy, node.ID, node.RootID, rootID)
		}
		if node.ParentID == nil {
			rootCount++
			if node.ID != rootID {
				return fmt.Errorf("%w: node %d is not root %d", hierarchy.ErrInvalidHierarchy, node.ID, rootID)
			}
		}
	}
	if rootCount != 1 {
		return fmt.Errorf("%w: snapshot has %d roots", hierarchy.ErrInvalidHierarchy, rootCount)
	}
	return nil
}
