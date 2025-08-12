# MemDB — Implementation Plan

MemDB is an in-memory database engine written in Go. Each table is backed by a
`[]byte`-keyed B+ tree, guarded by a `sync.RWMutex` so that point lookups and range
scans run in parallel while writes are exclusive. The engine is exposed over gRPC,
ships with a CLI client, and comes with `testing` benchmarks plus pprof tooling.

Module path: `github.com/akshaynavani/MemDB`

## Phase 1 — B+ tree core

Deliverables:
- `btree/node.go`: node layout (internal nodes hold separator keys + children, leaves hold
  keys + values + a `next` sibling pointer), binary-search helpers, slice insert/remove helpers.
- `btree/tree.go`: `Tree` type with configurable order (`New(order)`), `Put` with leaf and
  internal node splits and root growth, `Get`, `Len`, `Height`, `Order`.
- `btree/iter.go`: `Ascend(start, end, fn)` range scan that walks leaf sibling links,
  plus `Min` / `Max`.
- Keys and values are copied on insert so callers can reuse their buffers.

## Phase 2 — Deletion, invariants and tests

Deliverables:
- `btree/delete.go`: `Delete` with underflow handling — borrow from a left/right sibling
  when it has spare keys, otherwise merge; root shrinks when it has a single child.
- `btree/validate.go`: `Check()` verifies key ordering, separator bounds, fill factors,
  uniform leaf depth, the leaf chain, and the cached size.
- `btree/tree_test.go`: table-driven unit tests for put/get/overwrite/delete/scan edge cases.
- `btree/random_test.go`: randomized operations checked against a sorted-map reference
  across several orders, calling `Check()` along the way.

## Phase 3 — Engine layer

Deliverables:
- `engine/engine.go`: `Engine` holding named tables (`CreateTable`, `DropTable`, `Table`,
  `ListTables`), the table map itself guarded by an `RWMutex`.
- `engine/table.go`: `Table` wrapping one B+ tree behind a `sync.RWMutex`:
  `Put`, `Get`, `Delete`, `Scan`, `ScanPage` (bounded page + resume key), `BatchPut`
  (validated up front, applied under a single write lock), and atomic operation counters.
- `engine/stats.go`: `TableStats` snapshot (keys, height, order, op counters).
- `engine/*_test.go`: unit tests and a concurrent reader/writer test intended for `-race`.

## Phase 4 — gRPC API, server and CLI

Deliverables:
- `proto/memdb.proto`: `CreateTable`, `DropTable`, `ListTables`, `Put`, `Get`, `Delete`,
  `Scan` (server streaming), `BatchPut`, `Stats`.
- `make proto` generating Go code into `api/memdbpb` (generated code is not committed).
- `internal/server`: service implementation mapping engine errors to gRPC status codes,
  paging scans so the table lock is not held while streaming to the network.
- `cmd/memdb-server`: flags for listen address, tree order and an optional
  `net/http/pprof` listener; graceful shutdown on SIGINT/SIGTERM.
- `cmd/memdb-cli`: subcommands `create`, `drop`, `tables`, `put`, `get`, `del`, `scan`,
  `batch`, `stats`.
- Server test over `bufconn`.

## Phase 5 — Benchmarks, profiling and packaging

Deliverables:
- `btree/bench_test.go` and `engine/bench_test.go`: sequential/random insert, point lookup,
  range scan, and parallel mixed read/write (`b.RunParallel`) at several read ratios.
- `Makefile` targets: `test`, `race`, `bench`, `bench-profile` (writes `-cpuprofile` /
  `-memprofile` output), `proto`, `build`.
- `docs/profiling.md`: how to collect and read CPU / heap / mutex profiles.
- `Dockerfile`, `.gitignore`, `README.md` with a Mermaid architecture diagram.
