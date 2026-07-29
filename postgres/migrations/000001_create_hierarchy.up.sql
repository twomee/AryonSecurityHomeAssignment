CREATE TABLE hierarchies (
    root_id BIGINT PRIMARY KEY,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE hierarchy_nodes (
    id BIGINT PRIMARY KEY,
    root_id BIGINT NOT NULL,
    parent_id BIGINT,
    node_type TEXT NOT NULL CHECK (LENGTH(BTRIM(node_type)) > 0),
    sibling_position INTEGER NOT NULL CHECK (sibling_position >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT hierarchy_nodes_root_fk
        FOREIGN KEY (root_id)
        REFERENCES hierarchies(root_id)
        ON DELETE CASCADE,

    CONSTRAINT hierarchy_nodes_root_identity
        CHECK ((id = root_id) = (parent_id IS NULL)),

    CONSTRAINT hierarchy_nodes_root_id_id_unique
        UNIQUE (root_id, id),

    CONSTRAINT hierarchy_nodes_parent_fk
        FOREIGN KEY (root_id, parent_id)
        REFERENCES hierarchy_nodes(root_id, id)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED,

    CONSTRAINT hierarchy_nodes_sibling_position_unique
        UNIQUE (root_id, parent_id, sibling_position)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX hierarchy_nodes_one_root_per_hierarchy
    ON hierarchy_nodes(root_id)
    WHERE parent_id IS NULL;

CREATE INDEX hierarchy_nodes_parent_position_idx
    ON hierarchy_nodes(root_id, parent_id, sibling_position);

CREATE INDEX hierarchy_nodes_root_idx
    ON hierarchy_nodes(root_id);
