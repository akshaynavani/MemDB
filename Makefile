MODULE      := github.com/akshaynavani/MemDB
PROTO_DIR   := proto
PB_DIR      := api/memdbpb
BIN_DIR     := bin
PROFILE_DIR := profiles

# Benchmark knobs: `make bench BENCH=ParallelMixed BENCHTIME=2s`
BENCH       ?= .
BENCHTIME   ?= 1s
COUNT       ?= 1
# Package profiled by bench-profile (go test only profiles one package at a time).
PROFILE_PKG ?= engine

PROTOC_GEN_GO_VERSION      := v1.35.1
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

.PHONY: all tools proto deps build test race vet bench bench-profile pprof-cpu pprof-mem clean

all: proto deps build

## tools: install the protoc plugins used by `make proto`
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

## proto: generate Go code for the gRPC API into api/memdbpb (not committed)
proto:
	mkdir -p $(PB_DIR)
	protoc -I $(PROTO_DIR) \
		--go_out=. --go_opt=module=$(MODULE) \
		--go-grpc_out=. --go-grpc_opt=module=$(MODULE) \
		$(PROTO_DIR)/memdb.proto

## deps: resolve module dependencies and write go.sum
deps:
	go mod tidy

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/memdb-server ./cmd/memdb-server
	go build -o $(BIN_DIR)/memdb-cli ./cmd/memdb-cli

test:
	go test ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

## bench: run the btree and engine benchmarks
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -benchtime=$(BENCHTIME) -count=$(COUNT) ./btree ./engine

## bench-profile: run one package's benchmarks with CPU, heap, mutex and block profiling
bench-profile:
	mkdir -p $(PROFILE_DIR)
	go test -run='^$$' -bench='$(BENCH)' -benchmem -benchtime=$(BENCHTIME) -count=$(COUNT) \
		-cpuprofile=$(PROFILE_DIR)/$(PROFILE_PKG).cpu.out \
		-memprofile=$(PROFILE_DIR)/$(PROFILE_PKG).mem.out \
		-mutexprofile=$(PROFILE_DIR)/$(PROFILE_PKG).mutex.out \
		-blockprofile=$(PROFILE_DIR)/$(PROFILE_PKG).block.out \
		-o $(PROFILE_DIR)/$(PROFILE_PKG).test \
		./$(PROFILE_PKG) | tee $(PROFILE_DIR)/$(PROFILE_PKG).bench.txt

## pprof-cpu / pprof-mem: open the profiles from bench-profile in the pprof web UI
pprof-cpu:
	go tool pprof -http=localhost:8080 $(PROFILE_DIR)/$(PROFILE_PKG).test $(PROFILE_DIR)/$(PROFILE_PKG).cpu.out

pprof-mem:
	go tool pprof -http=localhost:8080 -sample_index=alloc_space $(PROFILE_DIR)/$(PROFILE_PKG).test $(PROFILE_DIR)/$(PROFILE_PKG).mem.out

clean:
	rm -rf $(BIN_DIR) $(PB_DIR) $(PROFILE_DIR)
