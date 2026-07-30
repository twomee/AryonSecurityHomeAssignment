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

// configureLockTimeout bounds how long this transaction may wait for a
// PostgreSQL lock, preventing one busy root from consuming a connection
// indefinitely.
func configureLockTimeout(
	ctx context.Context,
	tx *sql.Tx,
	timeout time.Duration,
) error {
	if _, err := tx.ExecContext(ctx, `
		SELECT set_config('lock_timeout', $1, true)
	`, timeout.String()); err != nil {
		return fmt.Errorf("configure hierarchy lock timeout: %w", err)
	}
	return nil
}

// ensureAndLockRoot creates the hierarchy metadata when needed, then locks
// that root so two replacements cannot interleave into a mixed snapshot.
func ensureAndLockRoot(ctx context.Context, tx *sql.Tx, rootID int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hierarchies (root_id)
		VALUES ($1)
		ON CONFLICT (root_id) DO NOTHING
	`, rootID); err != nil {
		return fmt.Errorf("ensure hierarchy root: %w", err)
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT root_id
		FROM hierarchies
		WHERE root_id = $1
		FOR UPDATE
	`, rootID).Scan(&rootID); err != nil {
		return fmt.Errorf("lock hierarchy root: %w", err)
	}
	return nil
}

// createIncomingNodesTable creates transaction-local staging for the complete
// desired snapshot; reconciliation reads from it in the following steps.
func createIncomingNodesTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		CREATE TEMPORARY TABLE incoming_hierarchy_nodes (
			id BIGINT PRIMARY KEY,
			root_id BIGINT NOT NULL,
			parent_id BIGINT,
			node_type TEXT NOT NULL,
			sibling_position INTEGER NOT NULL
		) ON COMMIT DROP
	`); err != nil {
		return fmt.Errorf("create incoming hierarchy staging table: %w", err)
	}
	return nil
}

// copyIncomingNodes bulk-loads the validated snapshot into staging so the
// database can reconcile the hierarchy with set-based operations.
func copyIncomingNodes(
	ctx context.Context,
	tx *sql.Tx,
	nodes []hierarchy.FlatNode,
) (err error) {
	statement, err := tx.PrepareContext(ctx, pq.CopyIn(
		"incoming_hierarchy_nodes",
		"id",
		"root_id",
		"parent_id",
		"node_type",
		"sibling_position",
	))
	if err != nil {
		return fmt.Errorf("prepare hierarchy bulk copy: %w", err)
	}
	defer func() {
		if closeErr := statement.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close hierarchy bulk copy: %w", closeErr)
		}
	}()

	for _, node := range nodes {
		if _, err = statement.ExecContext(
			ctx,
			node.ID,
			node.RootID,
			node.ParentID,
			node.Type,
			node.Position,
		); err != nil {
			return fmt.Errorf("copy hierarchy node %d: %w", node.ID, err)
		}
	}
	if _, err = statement.ExecContext(ctx); err != nil {
		return fmt.Errorf("finish hierarchy bulk copy: %w", err)
	}
	return nil
}

// rejectCrossRootConflict prevents a globally unique node ID from being
// claimed by a hierarchy other than the one currently being replaced.
func rejectCrossRootConflict(ctx context.Context, tx *sql.Tx, rootID int64) error {
	var conflictingID int64
	err := tx.QueryRowContext(ctx, `
		SELECT incoming.id
		FROM incoming_hierarchy_nodes AS incoming
		JOIN hierarchy_nodes AS existing ON existing.id = incoming.id
		WHERE existing.root_id <> $1
		LIMIT 1
	`, rootID).Scan(&conflictingID)
	switch {
	case err == nil:
		return fmt.Errorf("%w: node %d", hierarchy.ErrNodeConflict, conflictingID)
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("check cross-root node conflicts: %w", err)
	}
}

// mergeIncomingNodes inserts new nodes and updates changed nodes from staging
// without rewriting rows whose stored values are already current.
func mergeIncomingNodes(ctx context.Context, tx *sql.Tx) error {
	// Global IDs are ordered so overlapping replacements acquire unique-index
	// row locks in the same order instead of deadlocking.
	//
	// A conflicting global ID may only update a row already owned by this root.
	// Cross-root ownership is reported by rejectCrossRootConflict.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hierarchy_nodes (
			id,
			root_id,
			parent_id,
			node_type,
			sibling_position
		)
		SELECT
			id,
			root_id,
			parent_id,
			node_type,
			sibling_position
		FROM incoming_hierarchy_nodes
		ORDER BY id
		ON CONFLICT (id) DO UPDATE SET
			parent_id = EXCLUDED.parent_id,
			node_type = EXCLUDED.node_type,
			sibling_position = EXCLUDED.sibling_position,
			updated_at = NOW()
		WHERE hierarchy_nodes.root_id = EXCLUDED.root_id
		  AND (
				hierarchy_nodes.parent_id,
				hierarchy_nodes.node_type,
				hierarchy_nodes.sibling_position
			  ) IS DISTINCT FROM (
				EXCLUDED.parent_id,
				EXCLUDED.node_type,
				EXCLUDED.sibling_position
			  )
	`); err != nil {
		return fmt.Errorf("merge hierarchy nodes: %w", err)
	}
	return nil
}

// deleteMissingNodes removes rows that belong to this root but are absent
// from the incoming authoritative snapshot.
func deleteMissingNodes(ctx context.Context, tx *sql.Tx, rootID int64) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM hierarchy_nodes AS existing
		WHERE existing.root_id = $1
		  AND NOT EXISTS (
			SELECT 1
			FROM incoming_hierarchy_nodes AS incoming
			WHERE incoming.id = existing.id
		  )
	`, rootID); err != nil {
		return fmt.Errorf("delete missing hierarchy nodes: %w", err)
	}
	return nil
}

// incrementHierarchyRevision records that one complete replacement succeeded;
// it runs last so rolled-back attempts never advance the revision.
func incrementHierarchyRevision(ctx context.Context, tx *sql.Tx, rootID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE hierarchies
		SET revision = revision + 1,
		    updated_at = NOW()
		WHERE root_id = $1
	`, rootID); err != nil {
		return fmt.Errorf("update hierarchy revision: %w", err)
	}
	return nil
}
