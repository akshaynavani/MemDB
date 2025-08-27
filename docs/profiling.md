# Profiling MemDB

MemDB can be profiled two ways: through the `testing` benchmarks (repeatable,
no network in the way) and against a running `memdb-server` via
`net/http/pprof` (real gRPC traffic). This page covers both and lists what is
worth looking at. It deliberately contains no numbers — results depend on the
machine, Go version and workload, so record your own baselines.

## 1. Benchmarks

Benchmarks live in `btree/bench_test.go` (the raw tree, no locking) and
`engine/bench_test.go` (tables with their `RWMutex`, including
`b.RunParallel` workloads).

```sh
make bench                                # everything in ./btree and ./engine
make bench BENCH=ParallelMixed COUNT=5    # one family, repeated for benchstat
make bench BENCH=TableGet BENCHTIME=3s
```

To compare a change, save before/after output and use
[`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```sh
make bench COUNT=10 > old.txt
# ...change something...
make bench COUNT=10 > new.txt
benchstat old.txt new.txt
```

Useful knobs:

- `-cpu 1,2,4,8` on the parallel benchmarks shows how read throughput scales
  with cores. Because `Get` and `Scan` take only the read lock,
  `BenchmarkParallelGet` should scale better than `BenchmarkParallelMixed`.
- The `order=` sub-benchmarks in `btree` show the effect of the branching
  factor; `-order` on the server picks it for real tables.

## 2. Profiles from benchmarks

`go test` can only profile one package per run, so `bench-profile` takes a
package name:

```sh
make bench-profile                                   # engine, all benchmarks
make bench-profile PROFILE_PKG=btree BENCH=PutRandom
```

This writes to `profiles/`:

| File              | Contents                                         |
|-------------------|--------------------------------------------------|
| `<pkg>.cpu.out`   | CPU profile                                      |
| `<pkg>.mem.out`   | heap allocation profile                          |
| `<pkg>.mutex.out` | mutex contention (who held a lock others wanted) |
| `<pkg>.block.out` | time goroutines spent blocked (incl. on RWMutex) |
| `<pkg>.test`      | the test binary, for symbolisation               |
| `<pkg>.bench.txt` | the benchmark output                             |

Open them with:

```sh
make pprof-cpu          # web UI with flame graph
make pprof-mem          # allocations by bytes allocated
go tool pprof -top profiles/engine.test profiles/engine.mutex.out
```

## 3. Profiling a live server

Start the server with the pprof listener enabled. Mutex and block profiling
are off by default in the Go runtime; turn them on only while investigating,
since sampling adds overhead.

```sh
./bin/memdb-server -addr :7070 -pprof localhost:6060 \
    -mutex-profile-fraction 5 -block-profile-rate 10000
```

Drive some load (for example a shell loop around `memdb-cli`, or your own
client), then:

```sh
go tool pprof -http=:8080 'http://localhost:6060/debug/pprof/profile?seconds=30'
go tool pprof -http=:8080 http://localhost:6060/debug/pprof/heap
go tool pprof -http=:8080 http://localhost:6060/debug/pprof/mutex
curl -o trace.out 'http://localhost:6060/debug/pprof/trace?seconds=5' && go tool trace trace.out
```

The pprof endpoints are served on their own mux and only when `-pprof` is
set. Bind it to `localhost`; it exposes process internals.

## 4. What to look at

**CPU profile**

- `bytes.Compare` and `sort.Search` under `lowerBound` / `childIndex` — the
  cost of binary search inside nodes. A larger order means fewer levels but
  more comparisons per node; compare `order=` sub-benchmarks.
- `insertAt` / `removeAt` (`memmove`) — shifting slice elements inside a node.
  This grows with order, so very large orders make writes more expensive.
- `splitLeaf` / `splitInternal` / `merge` — should be a small fraction; if
  not, the workload is churning around node boundaries.
- In server profiles, protobuf marshalling and gRPC transport frames usually
  sit alongside engine work; check how much of the total is actually the tree.

**Heap / allocation profile** (`-sample_index=alloc_space` or `alloc_objects`)

- `cloneBytes` — every `Put` copies its key and value. This is intentional
  (callers may reuse buffers) and is the expected main allocation site for
  writes.
- `Table.Get` copies the value out under the lock; `ScanPage` copies each
  entry. Large values make these dominate.
- Slice growth in node `keys`/`values`/`children` during splits.

**Mutex and block profiles**

- `Table.Put` / `Table.Delete` / `Table.BatchPut` holding the write lock while
  readers wait shows up here. Larger batches hold the lock longer per call but
  take it fewer times.
- `Engine.Table` should be cheap: it only takes the catalogue's read lock.
  If it shows up, something is creating or dropping tables in a hot loop.
- `Table.Scan` runs its callback under the read lock; a slow callback blocks
  writers. The gRPC `Scan` handler uses `ScanPage` so that network sends happen
  without the lock held — tune `-scan-page` if scans and writes contend.

## 5. Tuning levers

- `-order` (tree order): trade tree height against per-node search and shift cost.
- `-scan-page`: entries copied per read-lock acquisition in streaming scans.
- Spread hot keys across tables: each table has its own lock, so independent
  tables never contend with each other.
- `GOMAXPROCS` / `-cpu` when benchmarking to see where scaling flattens.
