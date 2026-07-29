# CloudStore

CloudStore is a Go and PostgreSQL service for storing and retrieving ordered
cloud-resource hierarchies. A hierarchy is submitted as recursive JSON, but is
stored as relational rows so nodes can be added, removed, moved, reordered, or
retrieved as a subtree efficiently.

This repository contains the complete home-assignment solution: API,
PostgreSQL schema and migrations, transactional reconciliation, unit and
integration tests, Docker Compose setup, CI, linting, and security checks.

## What the service guarantees

- `POST /hierarchy` atomically replaces the hierarchy identified by the
  submitted root ID.
- Roots not included in that request remain unchanged.
- Added, removed, moved, renamed, and reordered nodes are reconciled.
- `GET /hierarchy/{node_id}` returns that node and its complete ordered
  subtree, whether the requested node is a root or an internal node.
- Invalid input and conflicting global node IDs are rejected without partial
  writes.
- Concurrent replacements never produce a mixture of two snapshots.
- Child order is preserved exactly.

The implementation uses the supplied Go server on port `8080`.

## Architecture

The service is a modular monolith: one deployable Go process with explicit
boundaries between transport, application/domain logic, and infrastructure.
This keeps deployment simple while allowing each layer to be tested and
changed independently.

```mermaid
flowchart LR
    Client["API client"] --> Handler["HTTP handlers<br/>Gin"]
    Handler --> Service["Hierarchy service"]
    Service --> Domain["Flatten / BuildTree<br/>validation and reconstruction"]
    Service --> Contract["Repository interface"]
    Contract --> Repository["PostgreSQL repository"]
    Repository --> Database[("PostgreSQL")]
```

Dependencies point inward:

- `httpapi` understands HTTP but not SQL.
- `hierarchy` understands hierarchy rules but not HTTP or PostgreSQL.
- `hierarchy.Repository` defines the storage operations the service needs.
- `postgres` implements that interface and owns SQL and transactions.
- `cmd/api` creates the concrete components and connects them.

The service depends on a repository interface rather than the PostgreSQL
implementation. This dependency inversion keeps business logic testable with
a fake repository and prevents database details from leaking into the domain.

## Project structure

```text
.
├── .github/
│   ├── dependabot.yml              # Automated dependency updates
│   └── workflows/ci.yml            # Tests, builds, lint and security scans
├── go-server/
│   ├── cmd/api/main.go              # Startup, dependency wiring and shutdown
│   ├── internal/
│   │   ├── config/                  # Typed environment configuration
│   │   ├── hierarchy/               # Domain types and application workflows
│   │   ├── httpapi/                 # Routes, handlers and middleware
│   │   └── postgres/                # SQL repository and transactions
│   ├── .dockerignore
│   ├── .golangci.yml                # Go lint configuration
│   ├── Dockerfile
│   ├── go.mod
│   └── go.sum
├── migrator/
│   └── Dockerfile                   # Controlled schema-migration image
├── postgres/
│   ├── migrations/                  # Versioned up/down migrations
│   ├── Dockerfile
│   └── init.sql                     # Supplied reference database bootstrap
├── scripts/
│   └── precommit.sh                 # Containerized developer checks
├── tests/
│   ├── objects/                     # Supplied hierarchy fixtures
│   ├── pyproject.toml               # Python test lint configuration
│   ├── requirements.txt
│   ├── run_tests.py                 # Supplied fixture round-trip test
│   └── test_hierarchy.py            # Extended API behavior tests
├── .env.example
├── .gitignore
├── .pre-commit-config.yaml
├── docker-compose.yml
└── README.md
```

Go packages are grouped by capability instead of creating a separate folder
for every type. For example, `internal/hierarchy` keeps the model, use-case
service, tree transformations, repository contract, and their focused tests
close together. HTTP and PostgreSQL remain separate because they are external
adapters with different reasons to change.

## API contract

### Store or replace a hierarchy

```http
POST /hierarchy
Content-Type: application/json
```

Example request:

```json
{
  "id": 1,
  "type": "management_group",
  "children": [
    {
      "id": 2,
      "type": "subscription",
      "children": [
        {
          "id": 3,
          "type": "resource_group",
          "children": []
        }
      ]
    }
  ]
}
```

Success:

```http
HTTP/1.1 200 OK
```

```json
{
  "status": "ok"
}
```

The submitted document is authoritative for root `1`. If a node previously
belonged to root `1` but is absent from this request, it is deleted. Other
roots are not modified.

### Fetch a hierarchy or subtree

```http
GET /hierarchy/{node_id}
```

`GET /hierarchy/2` for the example above returns:

```json
{
  "id": 2,
  "type": "subscription",
  "children": [
    {
      "id": 3,
      "type": "resource_group",
      "children": []
    }
  ]
}
```

### Error responses

Errors use a stable envelope:

```json
{
  "error": {
    "code": "invalid_hierarchy",
    "message": "invalid hierarchy: duplicate node id 2"
  }
}
```

| Status | Meaning |
| --- | --- |
| `400` | Malformed JSON, unknown JSON fields, multiple JSON values, or an invalid `node_id` in the GET path |
| `404` | Requested node does not exist |
| `409` | A submitted node ID is owned by another root |
| `413` | Request body or hierarchy node count exceeds its configured limit |
| `422` | Structurally invalid hierarchy, including a non-positive submitted node ID, or excessive hierarchy depth |
| `503` | PostgreSQL lock contention exceeded the bounded wait |
| `504` | The request operation exceeded its deadline |
| `500` | Unexpected internal failure; implementation details are not exposed |

## Database design

The hierarchy uses the adjacency-list model: every node stores the ID of its
direct parent.

```text
hierarchies
└── hierarchy_nodes
    ├── id
    ├── root_id
    ├── parent_id
    ├── node_type
    └── sibling_position
```

### `hierarchies`

This is the metadata and concurrency boundary for one complete hierarchy.

| Column | Purpose |
| --- | --- |
| `root_id` | Stable identity of the hierarchy and primary key |
| `revision` | Incremented only after a complete successful replacement |
| `updated_at` | Time of the last successful replacement |

The root metadata row is locked during replacement. Two writers for the same
root therefore run one after the other instead of interleaving their rows.

### `hierarchy_nodes`

This table stores one row per hierarchy node.

| Column | Purpose |
| --- | --- |
| `id` | Globally unique node identity |
| `root_id` | Hierarchy that owns the node |
| `parent_id` | Direct parent; `NULL` only for the hierarchy root |
| `node_type` | Cloud-resource type from the API |
| `sibling_position` | Original zero-based position under the parent |
| `updated_at` | Time this row's stored values last changed |

Important database invariants:

- `id` is the primary key, making node IDs globally unique.
- `(id = root_id) = (parent_id IS NULL)` ensures that exactly the root node
  has no parent and that its node ID equals the hierarchy root ID.
- `(root_id, id)` is unique so a composite foreign key can prove that parent
  and child belong to the same hierarchy.
- `(root_id, parent_id)` references a node in the same root.
- `(root_id, parent_id, sibling_position)` prevents two children from
  occupying the same ordered position.
- The parent and sibling constraints are deferrable until commit. This allows
  set-based moves and reorders without rejecting a valid final snapshot
  because of an intermediate statement state.
- `(root_id, parent_id, sibling_position)` is indexed for ordered child
  traversal.
- `root_id` is indexed for root-scoped reconciliation and deletion.

### Why adjacency lists

The main operation in this assignment is replacing a complete snapshot, while
the main query is loading a subtree.

Adjacency lists fit those requirements:

- Adding a node inserts one row.
- Removing a node deletes one row plus descendants removed by the snapshot.
- Moving a node changes its `parent_id`; descendants are not rewritten.
- A recursive PostgreSQL CTE retrieves a subtree in one query.
- The representation is relational, constrained, and easy to inspect.

Alternatives considered:

| Model | Why it was not selected |
| --- | --- |
| One JSONB document per root | Simple writes, but poor node-level constraints and inefficient internal-subtree lookup |
| Nested sets | Fast subtree reads, but moving a node can renumber a large part of the hierarchy |
| Materialized paths | Convenient subtree reads, but moving a node rewrites every descendant path |
| Closure table | Flexible ancestor queries, but stores many relationship rows and makes snapshot replacement more expensive |

## Write path: authoritative snapshot reconciliation

```mermaid
flowchart TD
    Request["Recursive JSON"] --> Decode["Strict decode and body limit"]
    Decode --> Flatten["Validate and flatten in memory"]
    Flatten --> Transaction["Begin PostgreSQL transaction"]
    Transaction --> RootLock["Ensure and lock root metadata"]
    RootLock --> Temp["Create temporary staging table"]
    Temp --> Copy["Bulk-copy incoming nodes"]
    Copy --> Conflict1["Check cross-root ID ownership"]
    Conflict1 --> Merge["Insert new and update changed rows"]
    Merge --> Conflict2["Recheck ownership after lock waits"]
    Conflict2 --> Delete["Delete rows missing from snapshot"]
    Delete --> Revision["Increment revision"]
    Revision --> Commit["Commit atomically"]
```

### 1. Decode and validate the request shape

The HTTP handler:

- Bounds the request body size.
- Requires exactly one JSON object.
- Rejects unknown fields.
- Preserves request cancellation and deadline propagation.

### 2. Flatten the recursive tree

`hierarchy.Flatten` converts the submitted tree into one `FlatNode` per node.
It validates positive IDs, non-empty types, explicit child arrays, duplicate
IDs, maximum depth, and maximum node count.

Flattening uses an explicit stack rather than Go recursion. Accepted deep
hierarchies therefore do not consume the goroutine call stack. Children are
pushed right-to-left so the original left-to-right order is preserved.

### 3. Validate the repository boundary

The repository verifies that the flat snapshot has exactly one root and that
every row claims the requested `root_id`. This is intentional defense in
depth: repository callers cannot bypass the service and begin a transaction
with an ambiguous snapshot.

### 4. Begin and bound the transaction

All reconciliation steps use one `READ COMMITTED` transaction. A
transaction-local PostgreSQL `lock_timeout` prevents a busy root from
occupying a database connection indefinitely.

Any failed step rolls back the entire replacement.

### 5. Lock the hierarchy root

The transaction creates the `hierarchies` metadata row when necessary and
locks it with `SELECT ... FOR UPDATE`.

This serializes replacements of the same root. Different roots can still
proceed concurrently unless they compete for the same global node IDs.

### 6. Stage the complete incoming snapshot

The transaction creates `incoming_hierarchy_nodes`, a temporary table with
`ON COMMIT DROP`, and streams the validated rows into it with PostgreSQL
`COPY`.

The temporary table is important because it turns recursive application input
into a relational set that SQL can compare with the stored set. It enables:

- Bulk transfer rather than one independent insert request per node.
- Set-based insert, update, and delete operations.
- A primary-key check over staged node IDs.
- Automatic cleanup on transaction completion.
- Isolation between concurrent database sessions.

The staging table is intentionally temporary instead of permanent because the
incoming snapshot has no value after the transaction commits or rolls back.

### 7. Protect global node ownership

A node ID may belong to only one root. The transaction checks whether any
staged ID is already stored under a different `root_id`.

The merge also refuses to update a row unless the existing and incoming
`root_id` values match. This database-side guard prevents accidental
cross-root ownership changes.

The ownership check runs again after the merge. This second check handles a
race where another root claims a previously free ID after the first check
while this transaction waits on PostgreSQL's unique-index lock. The losing
transaction returns a conflict and rolls back instead of silently committing
an incomplete snapshot.

### 8. Merge only changed rows

Incoming nodes are ordered by global ID before insertion so competing
transactions acquire unique-index locks in a consistent order.

`INSERT ... ON CONFLICT DO UPDATE`:

- Inserts new nodes.
- Updates moved, renamed, or reordered nodes.
- Uses `IS DISTINCT FROM` to avoid rewriting identical rows.

Avoiding unchanged updates reduces write amplification, index churn, and
unnecessary `updated_at` changes.

### 9. Delete missing rows

After merging, a root-scoped `DELETE` removes stored nodes not found in the
incoming staging table. This is what makes POST an authoritative replacement
rather than a partial patch.

### 10. Record success and commit

The hierarchy revision and timestamp are updated last. A failed transaction
never advances the revision. Commit makes the complete new snapshot visible
atomically.

## Read path: one query and reconstruction

`GetSubtree` uses one recursive PostgreSQL CTE:

1. Select the requested node.
2. Recursively join children using `root_id` and `parent_id`.
3. Carry a path array to prevent a corrupt cycle from recursing forever.
4. Return flat rows to the hierarchy service.

`hierarchy.BuildTree` then:

1. Indexes rows by ID.
2. Rejects duplicate or malformed rows.
3. Validates root and parent relationships.
4. Groups and sorts children by `sibling_position`.
5. Builds the requested recursive JSON tree.
6. Uses traversal states to detect cycles or repeated paths.
7. Rejects rows disconnected from the requested subtree.

The repository protects database interaction; `BuildTree` protects the API
from returning structurally corrupt stored data.

## Concurrency and failure behavior

| Situation | Result |
| --- | --- |
| Two writers replace the same root | Root-row lock serializes them; one complete snapshot wins |
| Two roots submit the same node ID | One succeeds and the other receives `409 Conflict` |
| A writer waits too long for a lock | Transaction fails with bounded `503 Service Unavailable` |
| Validation fails | No transaction begins |
| SQL or constraint step fails | Entire transaction rolls back |
| Client deadline expires | Context cancellation reaches PostgreSQL and the API returns `504` when appropriate |
| Process receives SIGTERM/SIGINT | Server stops accepting work, drains requests within its shutdown timeout, and closes the database |

## Efficiency

For a submitted hierarchy containing `n` nodes:

- Validation and flattening are `O(n)` time and `O(n)` memory.
- Rows are streamed into PostgreSQL with `COPY`.
- Reconciliation uses a bounded sequence of set-based SQL phases rather than
  one application/database round trip per node.
- Moving a node updates that node; descendants are not rewritten.
- Identical rows are not updated.

For a returned subtree containing `k` nodes:

- PostgreSQL retrieves the subtree with one recursive query.
- Reconstruction is `O(k)` excluding sibling sorting.
- Sorting work is distributed by parent and preserves deterministic order.

The implementation deliberately favors predictable correctness and
maintainable SQL over clever encodings. Pagination and bitwise subtree
fingerprints are deferred because the assignment contract requires complete
subtrees and complete snapshot writes.

## Configuration

Configuration is loaded once from environment variables and validated before
the server starts.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `PORT` | `8080` | API listen port |
| `MAX_BODY_BYTES` | `10485760` | Maximum HTTP request body |
| `HIERARCHY_MAX_NODES` | `100000` | Maximum nodes per submitted hierarchy |
| `HIERARCHY_MAX_DEPTH` | `1000` | Maximum accepted hierarchy depth |
| `HTTP_READ_TIMEOUT` | `15s` | HTTP request read timeout |
| `HTTP_WRITE_TIMEOUT` | `30s` | HTTP response write timeout |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `OPERATION_TIMEOUT` | `20s` | Per-request operation deadline |
| `DATABASE_LOCK_TIMEOUT` | `5s` | Maximum PostgreSQL lock wait |
| `SHUTDOWN_TIMEOUT` | `30s` | Graceful-shutdown deadline |

See `.env.example` for a local template. Production credentials must be
provided through environment variables or a secrets manager; they must not be
committed.

## Run with Docker Compose

### Prerequisites

- Docker with Docker Compose v2
- `curl` for the manual examples
- Python 3.14 when running the black-box tests locally
- Go 1.26 plus cgo and a supported C compiler/toolchain when running
  `go test -race` directly instead of through CI

### Start the complete stack

```bash
docker compose up -d --build --wait
```

Startup order is deliberate:

1. PostgreSQL starts and becomes healthy.
2. The one-shot migration container applies versioned migrations.
3. The Go API starts only after migration succeeds.

Inspect all containers, including the completed migration job:

```bash
docker compose ps -a
```

Expected state:

- `postgres` is healthy.
- `migrate` exited with status `0`.
- `go-server` is healthy and exposes port `8080`.

### Check service health

Liveness checks whether the Go process can respond:

```bash
curl --fail http://localhost:8080/healthz
```

Readiness also checks PostgreSQL:

```bash
curl --fail http://localhost:8080/readyz
```

### Exercise the API manually

Store a hierarchy:

```bash
curl --fail-with-body \
  -X POST http://localhost:8080/hierarchy \
  -H 'Content-Type: application/json' \
  -d '{
    "id": 1,
    "type": "management_group",
    "children": [
      {
        "id": 2,
        "type": "subscription",
        "children": []
      }
    ]
  }'
```

Fetch the complete hierarchy:

```bash
curl --fail-with-body http://localhost:8080/hierarchy/1
```

Fetch only the internal subtree:

```bash
curl --fail-with-body http://localhost:8080/hierarchy/2
```

### Inspect logs

```bash
docker compose logs --no-color go-server
docker compose logs --no-color migrate
docker compose logs --no-color postgres
```

### Stop the stack

Preserve PostgreSQL data:

```bash
docker compose down
```

Intentionally delete local PostgreSQL data and start clean:

```bash
docker compose down -v
```

`down -v` is destructive for the local Compose database volume and should be
used only when a clean database is intended.

## Testing

### Go unit tests

From the repository root:

```bash
cd go-server
go test ./...
```

These tests cover configuration, hierarchy validation and reconstruction,
service orchestration, HTTP handlers, middleware, health checks, and routing.
Repository integration tests skip when `TEST_DATABASE_URL` is not provided.

### PostgreSQL integration and race tests

Start Compose first. Direct race testing requires cgo and a supported C
compiler/toolchain:

```bash
cd go-server
TEST_DATABASE_URL='postgresql://aryon:aryon@localhost:5432/aryondb?sslmode=disable' \
  go test -race ./...
```

For a portable alternative, run the verified Go 1.26.5 Alpine toolchain in
Docker from the repository root:

```bash
docker run --rm --add-host=host.docker.internal:host-gateway \
  -e TEST_DATABASE_URL='postgresql://aryon:aryon@host.docker.internal:5432/aryondb?sslmode=disable' \
  -v "$PWD:/repo" -w /repo/go-server golang:1.26.5-alpine \
  sh -c 'apk add --no-cache build-base && go test -race ./...'
```

`--add-host` makes `host.docker.internal` available on Linux Docker as well.

These tests use a real database because transaction boundaries, locks,
constraints, concurrent writers, lock timeouts, and recursive SQL cannot be
proven reliably with mocks.

### Install Python test dependencies

From the repository root:

```bash
python3 -m venv .venv
.venv/bin/pip install -r tests/requirements.txt
```

### Run the supplied fixtures

With Compose running:

```bash
.venv/bin/python tests/run_tests.py
```

The runner stores and fetches all six supplied JSON hierarchies and requires
all six round trips to match.

### Run the extended end-to-end tests

```bash
.venv/bin/pytest -q tests/test_hierarchy.py
```

These tests cover independent roots, internal subtrees, removal, rollback on
invalid input, and strict JSON decoding.

### Run benchmarks

```bash
cd go-server
go test -bench=. -benchmem ./internal/hierarchy
```

Benchmarks report CPU and allocation behavior without brittle timing
thresholds in CI.

### Run pre-commit checks

```bash
.venv/bin/pip install pre-commit
.venv/bin/pre-commit install
.venv/bin/pre-commit run --all-files
```

The hooks check YAML and whitespace, format and lint Python tests, verify Go
formatting, run Go unit tests, and scan staged content for secrets. Go and
Gitleaks commands are containerized by `scripts/precommit.sh`.

### Recommended full local verification

Database integration tests mutate test rows, so do not run them concurrently
with black-box API tests. Reset the local database before the final API
fixture run:

```bash
docker compose up -d --build --wait

cd go-server
TEST_DATABASE_URL='postgresql://aryon:aryon@localhost:5432/aryondb?sslmode=disable' \
  go test -race ./...
cd ..

.venv/bin/pre-commit run --all-files

docker compose down -v
docker compose up -d --build --wait

.venv/bin/python tests/run_tests.py
.venv/bin/pytest -q tests/test_hierarchy.py

curl --fail http://localhost:8080/healthz
curl --fail http://localhost:8080/readyz
docker compose ps -a
```

## CI, dependency maintenance, and security

GitHub Actions runs on pull requests, pushes to `main`, and a weekly schedule.
The pipeline includes:

- Deterministic Go and Python formatting
- Go static analysis with `golangci-lint`
- Race-enabled tests against PostgreSQL
- A minimum total Go coverage threshold of 50%
- Go binary build verification
- Supplied and extended end-to-end API tests
- Docker image builds
- Reachable Go dependency scanning with `govulncheck`
- Container vulnerability scanning with Trivy
- Full-history secret scanning with Gitleaks

Dependabot checks Go modules, Python packages, Docker images, and GitHub
Actions weekly.

The Go and migration images use multi-stage builds. Runtime containers are
kept small, base packages are upgraded, and the API runs as a non-root user.

## Assumptions and intentional limitations

- A POST contains one complete authoritative snapshot, not a partial patch.
- Node IDs are positive and globally unique across all roots.
- The root node's ID is also its hierarchy ID.
- Every node includes a non-empty `type` and an explicit `children` array.
- Child ordering is part of the API contract.
- Reads return complete subtrees; pagination is intentionally deferred.
- Bitwise/XOR fingerprints were considered but deferred: XOR loses child
  ordering and has unsafe collision cases, while every request must still be
  traversed for validation. The current bulk staging and conditional updates
  already avoid unnecessary row rewrites; an ordered deterministic hash should
  be added only if profiling identifies reconciliation as a bottleneck.
- Authentication, authorization, multi-tenancy, and public rate limiting are
  outside the supplied assignment contract.

## Design summary

The central design choice is to transform recursive JSON into validated flat
rows, reconcile those rows as a set in one PostgreSQL transaction, and rebuild
recursive JSON only at the API boundary.

That separation provides:

- A simple external tree-shaped API.
- Strong relational integrity internally.
- Atomic and concurrency-safe snapshot replacement.
- Efficient node moves and subtree reads.
- Clear package boundaries that are straightforward to test and explain.
